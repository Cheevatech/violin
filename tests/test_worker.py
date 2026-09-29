import json
import os
from pathlib import Path
import runpy
import subprocess
import tempfile
import time
import unittest
from unittest import mock

RUNNER = Path(__file__).resolve().parents[1] / "compat/python/bin/violin-worker"
HEALTH = Path(__file__).resolve().parents[1] / "compat/python/bin/violin-health"


class WorkerTests(unittest.TestCase):
    def run_worker(self, backend, script, *options):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "fake"
            fake.write_text("""#!/usr/bin/env python3
import sys
import json
if '--max-tool-calls' in sys.argv:
    print(json.dumps({'type':'result','subtype':'success','is_error':False,'result':'VIOLIN_QWEN_CODE_SMOKE_OK'}))
    raise SystemExit(0)
""" + script)
            fake.chmod(0o700)
            env = dict(os.environ, VIOLIN_WORKER_RUNS=str(root / "runs"))
            env["VIOLIN_" + backend.upper() + "_BIN"] = str(fake)
            if backend == "qwen":
                env["VIOLIN_QWEN_MODEL"] = "qwen3.8-27b"
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
assert '--safe-mode' in sys.argv
assert sys.argv[sys.argv.index('--auth-type')+1] == 'openai'
assert sys.argv[sys.argv.index('--model')+1] == 'qwen3.8-27b'
assert sys.argv[sys.argv.index('--approval-mode')+1] == 'plan'
assert 'Violin is the supervisor' in sys.stdin.read()
print(json.dumps({'type':'result','subtype':'success','result':'evidence verified','usage':{'input_tokens':42}}))
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

    def test_structured_qwen_metadata_warning_is_degraded(self):
        code, report = self.run_worker("qwen", """import json
print(json.dumps({'type':'item.completed','item':{'type':'error','message':'Model metadata for qwen3.8-27b not found. Defaulting to fallback metadata.'}}))
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'smoke ok'}}))
print(json.dumps({'type':'turn.completed'}))
""")
        self.assertEqual(code, 0)
        self.assertEqual(report["status"], "completed")
        self.assertTrue(report["final_message_seen"])
        self.assertEqual(report["status_detail"], "metadata_degraded")
        self.assertNotIn("failure_reason", report)

    def test_qwen_health_timeout_uses_child_budget_and_reports_detail(self):
        worker = runpy.run_path(str(RUNNER))
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.dict(os.environ, {"VIOLIN_SMOKE_TIMEOUT": "12"}), \
                mock.patch("subprocess.run", side_effect=subprocess.TimeoutExpired("health", 20)) as run:
            value, failure = worker["health"](Path(directory), time.monotonic() + 30)
        self.assertEqual(run.call_args.kwargs["timeout"], 17)
        self.assertEqual(failure, "qwen_unhealthy")
        self.assertEqual(value["status_detail"], "preflight_timeout")
        self.assertEqual(value["preflight_timeout_seconds"], 17)

    def test_qwen_health_command_budget_fits_go_health_deadline_and_hides_output(self):
        health = runpy.run_path(str(HEALTH))
        timeout = subprocess.TimeoutExpired("health", 35, output="credential", stderr="private detail")
        with mock.patch.dict(os.environ, {}, clear=True), \
                mock.patch("subprocess.run", side_effect=timeout) as run, \
                mock.patch("builtins.print") as write:
            code = health["main"]()
        self.assertEqual(code, 79)
        self.assertEqual(run.call_args.kwargs["timeout"], 38)
        report = json.loads(write.call_args.args[0])
        self.assertEqual(report["error"], "smoke_timeout")
        self.assertNotIn("credential", json.dumps(report))
        self.assertNotIn("private detail", json.dumps(report))

    def test_qwen_health_classifies_cli_budget_exit(self):
        health = runpy.run_path(str(HEALTH))
        with mock.patch.dict(os.environ, {}, clear=True), \
                mock.patch("subprocess.run", return_value=subprocess.CompletedProcess("qwen", 55, "", "private output")), \
                mock.patch("builtins.print") as write:
            code = health["main"]()
        self.assertEqual(code, 79)
        report = json.loads(write.call_args.args[0])
        self.assertEqual(report["error"], "qwen_cli_budget_exceeded")
        self.assertNotIn("private output", json.dumps(report))

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
if '--max-tool-calls' in sys.argv:
 print(json.dumps({'type':'result','subtype':'success','result':'VIOLIN_QWEN_CODE_SMOKE_OK'}))
 raise SystemExit(0)
pathlib.Path('tracked.txt').write_text('after')
print(json.dumps({'type':'result','subtype':'success','result':'implemented'}))
""")
            fake.chmod(0o700)
            env = dict(os.environ, VIOLIN_QWEN_BIN=str(fake), VIOLIN_QWEN_MODEL="qwen3.8-27b",
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

    def test_worker_stops_before_qwen_backend_when_cli_smoke_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "qwen"
            marker = root / "launched"
            fake.write_text("""#!/usr/bin/env python3
import json,pathlib,sys
if '--max-tool-calls' in sys.argv:
 print(json.dumps({'type':'result','subtype':'success','result':'wrong marker'}))
 raise SystemExit(0)
pathlib.Path(%r).touch()
""" % str(marker))
            fake.chmod(0o700)
            env = dict(os.environ, VIOLIN_QWEN_BIN=str(fake),
                       VIOLIN_WORKER_RUNS=str(root / "runs"))
            result = subprocess.run([str(RUNNER), "qwen", "-C", directory],
                                    input="task", text=True, capture_output=True, env=env)
            report = json.loads(result.stdout)
            self.assertEqual(report["status"], "qwen_unhealthy")
            self.assertFalse(marker.exists())

if __name__ == "__main__":
    unittest.main()
