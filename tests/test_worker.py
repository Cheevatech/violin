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
            fake.write_text("#!/usr/bin/env python3\n" + script)
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

    def test_qwen_result_and_usage(self):
        code, report = self.run_worker("qwen", """import sys,json,pathlib
assert sys.argv[1] == 'exec'
assert sys.argv[sys.argv.index('-s')+1] == 'read-only'
assert 'Codex is the supervisor' in sys.stdin.read()
pathlib.Path(sys.argv[sys.argv.index('-o')+1]).write_text('evidence verified')
print(json.dumps({'type':'turn.completed','usage':{'input_tokens':42}}))
""")
        self.assertEqual(code, 0)
        self.assertEqual(report["usage"]["input_tokens"], 42)
        self.assertEqual(report["summary"], "evidence verified")

    def test_agy_exact_model_and_result(self):
        code, report = self.run_worker("agy", """import sys,json
assert sys.argv[sys.argv.index('--model')+1] == 'gemini-3.8-flash-medium'
assert '--sandbox' in sys.argv
print(json.dumps({'status':'SUCCESS','response':'a'*100,'usage':{'total_tokens':10}}))
""", "--summary-chars", "20")
        self.assertEqual(code, 0)
        self.assertEqual(len(report["summary"]), 20)
        self.assertTrue(report["summary_truncated"])

    def test_agy_failed_payload_with_zero_exit(self):
        code, report = self.run_worker("agy", "print('{\"status\":\"ERROR\"}')")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "failed")

    def test_missing_result_is_failure(self):
        code, report = self.run_worker("qwen", "pass")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "failed")

    def test_timeout(self):
        code, report = self.run_worker("qwen", "import time; time.sleep(30)", "--timeout", "1")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "timeout")
        self.assertLess(report["duration_seconds"], 8)

    def test_idle_timeout_after_command_event_writes_phase_and_report(self):
        code, report = self.run_worker("qwen", """import json,time
print(json.dumps({'type':'command_execution'}), flush=True)
time.sleep(30)
""", "--idle-timeout", "1", "--timeout", "10")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "idle_timeout")
        self.assertEqual(report["phase"], "timeout")
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

    def test_metadata_fallback_warning_is_a_terminal_failure(self):
        code, report = self.run_worker("qwen", """import time
print('metadata warning: using fallback', flush=True)
time.sleep(30)
""", "--idle-timeout", "10", "--timeout", "10")
        self.assertEqual(code, 1)
        self.assertEqual(report["status"], "metadata_unavailable")


if __name__ == "__main__":
    unittest.main()
