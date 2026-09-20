import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


CLI = Path(__file__).resolve().parents[1] / "bin/violin-agent"


class AgentCliTests(unittest.TestCase):
    def test_background_run_and_wait_use_shared_scheduler(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            fake = root / "fake-agy"
            fake.write_text("#!/usr/bin/env python3\nimport json\nprint(json.dumps({'status':'SUCCESS','response':'cli fixture'}))\n")
            fake.chmod(0o700)
            env = dict(os.environ, VIOLIN_WORKER_RUNS=str(root / "runs"), VIOLIN_AGY_BIN=str(fake))
            run = subprocess.run([str(CLI), "run", "--backend", "agy", "-C", str(root),
                                  "--task", "Read fixture", "--background"],
                                 capture_output=True, text=True, env=env, check=True)
            job = json.loads(run.stdout)
            self.assertEqual(job["status"], "running")
            result = subprocess.run([str(CLI), "wait", job["agent_id"], "--wait-seconds", "5"],
                                    capture_output=True, text=True, env=env, check=False)
            report = json.loads(result.stdout)
            self.assertEqual(report["status"], "completed")
            self.assertEqual(report["summary"], "cli fixture")
            self.assertEqual(report["selected_backend"], "agy")


if __name__ == "__main__":
    unittest.main()
