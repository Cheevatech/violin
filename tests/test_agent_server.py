import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SERVER = Path(__file__).resolve().parents[1] / "bin/violin-agent-server"


class ServerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        cache = self.root / "models.json"
        cache.write_text(json.dumps({"fetched_at": "2099-01-01T00:00:00Z", "models": [{
            "slug": "qwen3.8-27b", "context_window": 200000,
            "supported_reasoning_levels": [{"effort": "medium"}],
            "supported_in_api": True, "provider": "violin_lan", "wire_api": "responses"}]}))
        fake = self.root / "fake-qwen"
        fake.write_text("""#!/usr/bin/env python3
import sys,pathlib,time,json
if len(sys.argv) > 1 and sys.argv[1] == 'smoke':
 print('QWEN_SMOKE_OK model=qwen3.8-27b provider=violin_lan')
 raise SystemExit(0)
task=sys.stdin.read()
if 'SLOW_TEST' in task: time.sleep(30)
pathlib.Path(sys.argv[sys.argv.index('-o')+1]).write_text('fixture result')
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'done'}}))
print(json.dumps({'type':'turn.completed'}))
""")
        fake.chmod(0o700)
        fake_agy = self.root / "fake-agy"
        fake_agy.write_text("""#!/usr/bin/env python3
import json,sys,time
if 'SLOW_TEST' in sys.argv[-1]: time.sleep(30)
if 'FAIL_TEST' in sys.argv[-1]:
 print(json.dumps({"status": "ERROR", "error": "fixture provider failed", "usage": {"total_tokens": 10}}))
else:
 response = 'x' * 2000 if 'LONG_TEST' in sys.argv[-1] else 'agy fixture result'
 print(json.dumps({"status": "SUCCESS", "response": response, "usage": {"total_tokens": 10}}))
""")
        fake_agy.chmod(0o700)
        fake_claude = self.root / "fake-claude"
        fake_claude.write_text("""#!/usr/bin/env python3
import json
print(json.dumps({"type": "result", "subtype": "success", "result": "claude fixture result", "usage": {"input_tokens": 7}}))
""")
        fake_claude.chmod(0o700)
        self.proc = subprocess.Popen([str(SERVER)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, text=True,
            env=dict(os.environ, VIOLIN_QWEN_BIN=str(fake), VIOLIN_WORKER_RUNS=str(self.root/"runs"),
                     VIOLIN_AGY_BIN=str(fake_agy), VIOLIN_CLAUDE_BIN=str(fake_claude),
                     VIOLIN_CODEX_MODELS_CACHE=str(cache),
                     VIOLIN_QWEN_MAX_CONCURRENCY="1", VIOLIN_AGY_MAX_CONCURRENCY="10"))

    def tearDown(self):
        self.proc.stdin.close()
        self.proc.wait(timeout=12)
        self.proc.stdout.close()
        self.proc.stderr.close()
        self.temp.cleanup()

    def rpc(self, method, params=None):
        self.proc.stdin.write(json.dumps({"jsonrpc":"2.0", "id":1, "method":method, "params":params or {}})+"\n")
        self.proc.stdin.flush()
        return json.loads(self.proc.stdout.readline())["result"]

    def tool(self, name, arguments=None):
        result=self.rpc("tools/call", {"name":name,"arguments":arguments or {}})
        if result.get("isError"):
            return result
        return json.loads(result["content"][0]["text"])

    def test_protocol_and_completed_worker(self):
        self.assertIn("tools", self.rpc("initialize")["capabilities"])
        self.assertEqual(len(self.rpc("tools/list")["tools"]),4)
        job=self.tool("spawn_agent", {"cwd":str(self.root),"task":"Read fixture"})
        result=self.tool("wait_agent", {"agent_id":job["agent_id"],"wait_seconds":5})
        self.assertEqual(result["status"],"completed")
        self.assertEqual(result["summary"],"agy fixture result")
        self.assertEqual(result["requested_backend"], "auto")
        self.assertEqual(result["selected_backend"], "agy")
        self.assertTrue(result["supervisor_review_required"])

    def test_auto_falls_back_to_qwen_when_agy_is_full(self):
        slow = [self.tool("spawn_agent", {"cwd":str(self.root),"task":"SLOW_TEST"}) for _ in range(10)]
        self.assertTrue(all(job["selected_backend"] == "agy" for job in slow))
        fallback = self.tool("spawn_agent", {"cwd":str(self.root),"task":"Read fixture"})
        self.assertEqual(fallback["selected_backend"], "qwen")
        self.assertEqual(fallback["fallback_reason"], "agy_capacity_full")
        result = self.tool("wait_agent", {"agent_id":fallback["agent_id"],"wait_seconds":5})
        self.assertEqual(result["summary"], "fixture result")
        for job in slow:
            self.tool("interrupt_agent", {"agent_id":job["agent_id"]})

    def test_interrupt_running_worker(self):
        job=self.tool("spawn_agent", {"cwd":str(self.root),"task":"SLOW_TEST"})
        result=self.tool("wait_agent", {"agent_id":job["agent_id"],"wait_seconds":1})
        self.assertEqual(result["status"],"running")
        result=self.tool("interrupt_agent", {"agent_id":job["agent_id"]})
        self.assertEqual(result["status"],"interrupted")

    def test_running_snapshot_contains_phase_and_evidence(self):
        job=self.tool("spawn_agent", {"cwd":str(self.root),"task":"SLOW_TEST", "idle_timeout_seconds": 10})
        self.assertIn(job["phase"], {"starting", "backend_starting", "turn_started"})
        self.assertIn("elapsed_seconds", job)
        self.assertTrue(job["evidence"].endswith("agent-" + job["agent_id"]) is False)
        self.tool("interrupt_agent", {"agent_id":job["agent_id"]})

    def test_success_response_is_bounded_and_evidence_is_full(self):
        job = self.tool("spawn_agent", {"backend": "agy", "cwd": str(self.root), "task": "LONG_TEST"})
        result = self.tool("wait_agent", {"agent_id": job["agent_id"], "wait_seconds": 5})
        self.assertEqual(result["status"], "completed")
        self.assertEqual(len(result["summary"]), 1500)
        self.assertTrue(result["summary_truncated"])
        self.assertNotIn("usage", result)
        self.assertNotIn("metadata", result)
        evidence = Path(result["evidence"])
        self.assertEqual(len((evidence / "result.txt").read_text()), 2000)
        full_report = json.loads((evidence / "report.json").read_text())
        self.assertEqual(len(full_report["summary"]), 2000)
        self.assertIn("usage", full_report)

    def test_failure_response_is_compact_but_keeps_error_and_evidence(self):
        job = self.tool("spawn_agent", {"backend": "agy", "cwd": str(self.root), "task": "FAIL_TEST"})
        result = self.tool("wait_agent", {"agent_id": job["agent_id"], "wait_seconds": 5})
        self.assertEqual(result["status"], "failed")
        self.assertEqual(result["error_message"], "fixture provider failed")
        self.assertEqual(result["failure_reason"], "failed")
        self.assertTrue(Path(result["evidence"]).is_dir())
        self.assertNotIn("usage", result)

    def test_claude_success_response_is_projected(self):
        job = self.tool("spawn_agent", {"backend": "claude", "cwd": str(self.root), "task": "Read fixture"})
        result = self.tool("wait_agent", {"agent_id": job["agent_id"], "wait_seconds": 5})
        self.assertEqual(result["status"], "completed")
        self.assertEqual(result["selected_backend"], "claude")
        self.assertEqual(result["summary"], "claude fixture result")
        self.assertNotIn("usage", result)
        self.assertNotIn("claude_model", result)

    def test_unknown_job_and_relative_workspace_fail(self):
        self.assertTrue(self.tool("wait_agent", {"agent_id":"missing"})["isError"])
        self.assertTrue(self.tool("spawn_agent", {"cwd":".","task":"Read"})["isError"])


if __name__ == "__main__":
    unittest.main()
