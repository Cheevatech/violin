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
import sys,pathlib,time
task=sys.stdin.read()
if 'SLOW_TEST' in task: time.sleep(30)
pathlib.Path(sys.argv[sys.argv.index('-o')+1]).write_text('fixture result')
""")
        fake.chmod(0o700)
        self.proc = subprocess.Popen([str(SERVER)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, text=True,
            env=dict(os.environ, VIOLIN_QWEN_BIN=str(fake), VIOLIN_WORKER_RUNS=str(self.root/"runs"),
                     VIOLIN_CODEX_MODELS_CACHE=str(cache)))

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
        self.assertEqual(result["summary"],"fixture result")
        self.assertTrue(result["supervisor_review_required"])

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

    def test_unknown_job_and_relative_workspace_fail(self):
        self.assertTrue(self.tool("wait_agent", {"agent_id":"missing"})["isError"])
        self.assertTrue(self.tool("spawn_agent", {"cwd":".","task":"Read"})["isError"])


if __name__ == "__main__":
    unittest.main()
