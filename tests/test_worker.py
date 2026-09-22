import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

RUNNER = Path(__file__).resolve().parents[1] / "bin/violin-worker"


class WorkerTests(unittest.TestCase):
    def run_worker(self, backend, script, *options):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "fake"
            fake.write_text("""#!/usr/bin/env python3
import sys
if len(sys.argv) > 1 and sys.argv[1] == 'smoke':
    print('QWEN_SMOKE_OK model=qwen3.8-27b provider=violin_lan')
    raise SystemExit(0)
""" + script)
            fake.chmod(0o700)
            env = dict(os.environ, VIOLIN_WORKER_RUNS=str(root / "runs"))
            env["VIOLIN_" + backend.upper() + "_BIN"] = str(fake)
            if backend == "qwen":
                (root / "models.json").write_text(json.dumps({"fetched_at": "2099-01-01T00:00:00Z", "models": [{
                    "slug": "qwen3.8-27b", "context_window": 200000,
                    "supported_reasoning_levels": [{"effort": "medium"}],
                    "supported_in_api": True, "provider": "violin_lan", "wire_api": "responses"}]}))
                env["VIOLIN_CODEX_MODELS_CACHE"] = str(root / "models.json")
            result = subprocess.run([str(RUNNER), backend, "-C", directory, *options],
                                    input="Inspect this task", text=True, capture_output=True, env=env)
            report = json.loads(result.stdout)
            self.assertTrue((Path(report["evidence"]) / "stderr.log").exists())
            return result.returncode, report

    def test_configured_text_command_can_replace_qwen_launcher(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "hermes"
            fake.write_text("#!/usr/bin/env python3\nprint('hermes result')\n")
            fake.chmod(0o700)
            (root / "task.txt").write_text("Use Hermes")
            env = dict(os.environ, VIOLIN_WORKER_RUNS=str(root / "runs"),
                       VIOLIN_QWEN_COMMAND=json.dumps([str(fake)]),
                       VIOLIN_QWEN_CUSTOM="1", VIOLIN_QWEN_PROTOCOL="text")
            result = subprocess.run([str(RUNNER), "qwen", "-C", directory, "--task-file", str(root / "task.txt")],
                                    text=True, capture_output=True, env=env)
            report = json.loads(result.stdout)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(report["summary"], "hermes result")
            self.assertEqual(report["metadata_status"], "not_applicable")

    def test_invalid_command_placeholder_returns_failure_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            env = dict(os.environ, VIOLIN_WORKER_RUNS=str(root / "runs"),
                       VIOLIN_AGY_COMMAND=json.dumps(["hermes", "{unknown}"]),
                       VIOLIN_AGY_CUSTOM="1", VIOLIN_AGY_PROTOCOL="text")
            result = subprocess.run([str(RUNNER), "agy", "-C", directory],
                                    input="Inspect this task", text=True, capture_output=True, env=env)
            report = json.loads(result.stdout)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["failure_reason"], "config_error")
            self.assertIn("Unknown command placeholder", report["error_message"])

    def test_qwen_result_and_usage(self):
        code, report = self.run_worker("qwen", """import sys,json,pathlib
assert sys.argv[1] == 'exec'
assert sys.argv[sys.argv.index('-s')+1] == 'read-only'
assert 'Codex is the supervisor' in sys.stdin.read()
pathlib.Path(sys.argv[sys.argv.index('-o')+1]).write_text('evidence verified')
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'done'}}))
print(json.dumps({'type':'turn.completed','usage':{'input_tokens':42}}))
""")
        self.assertEqual(code, 0)
        self.assertEqual(report["usage"]["input_tokens"], 42)
        self.assertEqual(report["summary"], "evidence verified")

    def test_qwen_command_execution_can_run_longer_than_idle_timeout(self):
        code, report = self.run_worker("qwen", """import json,time
print(json.dumps({'type':'item.started','item':{'type':'command_execution','status':'in_progress'}}), flush=True)
time.sleep(2)
print(json.dumps({'type':'item.completed','item':{'type':'command_execution','status':'completed'}}), flush=True)
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'command finished'}}), flush=True)
print(json.dumps({'type':'turn.completed'}), flush=True)
""", "--timeout", "5", "--idle-timeout", "1")
        self.assertEqual(code, 0)
        self.assertEqual(report["summary"], "command finished")

    def test_qwen_non_streaming_reasoning_uses_hard_timeout(self):
        code, report = self.run_worker("qwen", """import json,time
time.sleep(2)
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'reasoned result'}}))
print(json.dumps({'type':'turn.completed'}))
""", "--timeout", "5", "--idle-timeout", "1")
        self.assertEqual(code, 0)
        self.assertEqual(report["summary"], "reasoned result")

    def test_agy_non_streaming_run_uses_hard_timeout(self):
        code, report = self.run_worker("agy", """import json,time
time.sleep(2)
print(json.dumps({'status':'SUCCESS','response':'agy finished'}))
""", "--timeout", "5", "--idle-timeout", "1")
        self.assertEqual(code, 0)
        self.assertEqual(report["summary"], "agy finished")

    def test_qwen_accumulates_response_deltas(self):
        code, report = self.run_worker("qwen", """import json
print(json.dumps({'type':'response.output_text.delta','delta':'hello '}))
print(json.dumps({'type':'response.output_text.delta','delta':'world'}))
print(json.dumps({'type':'response.completed','response':{'status':'completed'}}))
""")
        self.assertEqual(code, 0)
        self.assertEqual(report["summary"], "hello world")

    def test_qwen_response_completed_failure_is_provider_error(self):
        code, report = self.run_worker("qwen", """import json
print(json.dumps({'type':'response.output_text.delta','delta':'partial'}))
print(json.dumps({'type':'response.completed','response':{'status':'failed','error':{'message':'upstream failed'}}}))
""")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "provider_error")
        self.assertIn("upstream failed", report["error_message"])

    def test_agy_exact_model_and_result(self):
        code, report = self.run_worker("agy", """import sys,json
assert sys.argv[sys.argv.index('--model')+1] == 'gemini-3.8-flash-medium'
assert '--sandbox' in sys.argv
assert sys.argv[sys.argv.index('--print')+1].startswith('You are a delegated worker.')
print(json.dumps({'status':'SUCCESS','response':'a'*100,'usage':{'total_tokens':10}}))
""", "--summary-chars", "20")
        self.assertEqual(code, 0)
        self.assertEqual(len(report["summary"]), 20)
        self.assertTrue(report["summary_truncated"])

    def test_agy_failed_payload_with_zero_exit(self):
        code, report = self.run_worker("agy", "print('{\"status\":\"ERROR\"}')")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "failed")

    def test_claude_stream_result_and_inspect_permission(self):
        code, report = self.run_worker("claude", """import json,sys
assert sys.argv[sys.argv.index('--model')+1] == 'claude-sonnet-5'
assert sys.argv[sys.argv.index('--permission-mode')+1] == 'plan'
assert sys.argv[sys.argv.index('--output-format')+1] == 'stream-json'
assert 'Inspect this task' in sys.argv[-1]
print(json.dumps({'type':'assistant','message':{'content':[{'type':'text','text':'draft'}]}}))
print(json.dumps({'type':'result','subtype':'success','result':'final answer','usage':{'input_tokens':7}}))
""")
        self.assertEqual(code, 0)
        self.assertEqual(report["summary"], "final answer")
        self.assertEqual(report["claude_model"], "claude-sonnet-5")
        self.assertEqual(report["usage"]["input_tokens"], 7)

    def test_claude_implement_permission_and_error(self):
        code, report = self.run_worker("claude", """import json,sys
assert sys.argv[sys.argv.index('--permission-mode')+1] == 'acceptEdits'
print(json.dumps({'type':'result','subtype':'error_during_execution','is_error':True,'result':'OAuth session expired'}))
""", "--mode", "implement")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "provider_error")
        self.assertIn("OAuth session expired", report["error_message"])

    def test_claude_invalid_json_and_empty_result(self):
        code, report = self.run_worker("claude", "print('not json')")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "invalid_json")
        code, report = self.run_worker("claude", "import json; print(json.dumps({'type':'result','subtype':'success'}))")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "empty_final_response")

    def test_missing_result_is_failure(self):
        code, report = self.run_worker("qwen", "print('{\"type\":\"turn.completed\"}')")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "empty_final_response")

    def test_qwen_item_error_is_provider_error(self):
        code, report = self.run_worker("qwen", """import json
print(json.dumps({'type':'item.completed','item':{'type':'error','message':'route unavailable'}}))
print(json.dumps({'type':'turn.completed'}))
""")
        self.assertEqual(code, 1)
        self.assertEqual(report["failure_reason"], "provider_error")
        self.assertIn("route unavailable", report["error_message"])

    def test_implement_without_diff_is_no_changes(self):
        code, report = self.run_worker("qwen", """import json
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'finished'}}))
print(json.dumps({'type':'turn.completed'}))
""", "--mode", "implement")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "no_changes")
        self.assertTrue(report["final_message_seen"])

    def test_implement_with_diff_is_completed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            (root / "tracked.txt").write_text("before")
            subprocess.run(["git", "add", "tracked.txt"], cwd=root, check=True)
            subprocess.run(["git", "-c", "user.email=test@example.com", "-c", "user.name=test",
                            "commit", "-qm", "initial"], cwd=root, check=True)
            fake = root / "fake"
            fake.write_text("""#!/usr/bin/env python3
import json,pathlib
import sys
if len(sys.argv) > 1 and sys.argv[1] == 'smoke':
 print('QWEN_SMOKE_OK model=qwen3.8-27b provider=violin_lan')
 raise SystemExit(0)
pathlib.Path('tracked.txt').write_text('after')
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'implemented'}}))
print(json.dumps({'type':'turn.completed'}))
""")
            fake.chmod(0o700)
            cache = root / "models.json"
            cache.write_text(json.dumps({"fetched_at": "2099-01-01T00:00:00Z", "models": [{
                "slug": "qwen3.8-27b", "context_window": 200000,
                "supported_reasoning_levels": [{"effort": "medium"}],
                "supported_in_api": True, "provider": "violin_lan", "wire_api": "responses"}]}))
            env = dict(os.environ, VIOLIN_QWEN_BIN=str(fake), VIOLIN_CODEX_MODELS_CACHE=str(cache),
                       VIOLIN_WORKER_RUNS=str(root / "runs"))
            result = subprocess.run([str(RUNNER), "qwen", "--mode", "implement", "-C", directory],
                                    input="Implement", text=True, capture_output=True, env=env)
            report = json.loads(result.stdout)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(report["status"], "completed")
            self.assertEqual(report["changed_files"], ["tracked.txt"])

    def test_metadata_warning_is_degraded(self):
        code, report = self.run_worker("qwen", """import json
print('metadata warning: using fallback', flush=True)
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'smoke ok'}}))
print(json.dumps({'type':'turn.completed'}))
""")
        self.assertEqual(code, 0)
        self.assertEqual(report["status"], "completed")
        self.assertEqual(report["status_detail"], "metadata_degraded")

    def test_timeout(self):
        code, report = self.run_worker("qwen", "import time; time.sleep(30)", "--timeout", "1")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "timeout")
        self.assertLess(report["duration_seconds"], 8)

    def test_custom_command_idle_timeout_writes_phase_and_report(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "silent"
            fake.write_text("#!/usr/bin/env python3\nimport time\ntime.sleep(30)\n")
            fake.chmod(0o700)
            env = dict(os.environ, VIOLIN_WORKER_RUNS=str(root / "runs"),
                       VIOLIN_QWEN_COMMAND=json.dumps([str(fake)]),
                       VIOLIN_QWEN_CUSTOM="1", VIOLIN_QWEN_PROTOCOL="text")
            result = subprocess.run([str(RUNNER), "qwen", "-C", directory,
                                     "--idle-timeout", "1", "--timeout", "10"],
                                    input="Inspect this task", text=True,
                                    capture_output=True, env=env)
            report = json.loads(result.stdout)
            self.assertEqual(result.returncode, 1)
            self.assertEqual(report["status"], "idle_timeout")
            self.assertEqual(report["phase"], "timeout")

    def test_metadata_cache_entry_passes_preflight(self):
        checker = Path(__file__).resolve().parents[1] / "bin/violin-qwen-metadata"
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory) / "models.json"
            cache.write_text(json.dumps({"fetched_at": "2099-01-01T00:00:00Z", "models": [{
                "slug": "qwen3.8-27b", "context_window": 200000,
                "supported_reasoning_levels": [{"effort": "medium"}],
                "supported_in_api": True, "provider": "violin_lan", "wire_api": "responses"}]}))
            result = subprocess.run([str(checker), str(cache)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0)

    def test_incomplete_cache_entry_uses_effective_metadata(self):
        checker = Path(__file__).resolve().parents[1] / "bin/violin-qwen-metadata"
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory) / "models.json"
            cache.write_text(json.dumps({"fetched_at": "2099-01-01T00:00:00Z", "models": [{
                "slug": "qwen3.8-27b", "context_window": 200000,
                "supported_reasoning_levels": [{"effort": "medium"}],
                "supported_in_api": True}]}))
            effective = {"slug": "qwen3.8-27b", "shell_type": "responses",
                         "context_window": 200000,
                         "supported_reasoning_levels": [{"effort": "medium"}],
                         "supported_in_api": True}
            fake_codex = Path(directory) / "codex"
            fake_codex.write_text("#!/usr/bin/env python3\nimport json\nprint(json.dumps(%r))\n" % {"models": [effective]})
            fake_codex.chmod(0o700)
            result = subprocess.run([str(checker), str(cache)], capture_output=True, text=True,
                                    env=dict(os.environ, VIOLIN_CODEX_BIN=str(fake_codex)))
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("codex_debug_models", result.stdout)
            self.assertIn('"status": "degraded"', result.stdout)

    def test_missing_qwen_metadata_does_not_launch_backend(self):
        checker = Path(__file__).resolve().parents[1] / "bin/violin-qwen-metadata"
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory) / "models.json"
            cache.write_text(json.dumps({"fetched_at": "2099-01-01T00:00:00Z", "models": []}))
            result = subprocess.run([str(checker), str(cache)], capture_output=True, text=True,
                                    env=dict(os.environ, VIOLIN_CODEX_BIN=str(Path(directory) / "missing-codex")))
            self.assertEqual(result.returncode, 78)
            self.assertIn("model entry is missing", result.stdout)

    def test_worker_stops_before_qwen_backend_when_metadata_is_missing(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "violin-codex-qwen"
            marker = root / "launched"
            fake.write_text("#!/bin/sh\ntouch '%s'\n" % marker)
            fake.chmod(0o700)
            env = dict(os.environ, VIOLIN_QWEN_BIN=str(fake), VIOLIN_CODEX_BIN=str(root / "missing-codex"),
                       VIOLIN_CODEX_MODELS_CACHE=str(root / "missing.json"),
                       VIOLIN_WORKER_RUNS=str(root / "runs"))
            result = subprocess.run([str(RUNNER), "qwen", "-C", directory],
                                    input="task", text=True, capture_output=True, env=env)
            report = json.loads(result.stdout)
            self.assertEqual(report["status"], "metadata_unavailable")
            self.assertFalse(marker.exists())

if __name__ == "__main__":
    unittest.main()
