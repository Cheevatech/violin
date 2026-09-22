import json
from pathlib import Path
import os
import subprocess
import tempfile
import unittest


class WorkerReportSchemaTests(unittest.TestCase):
    def test_real_worker_report_contains_schema_contract(self):
        root_dir = Path(__file__).resolve().parents[1]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "backend"
            fake.write_text("#!/usr/bin/env python3\nprint('schema fixture')\n")
            fake.chmod(0o700)
            result = subprocess.run(
                [str(root_dir / "bin/violin-worker"), "qwen", "-C", directory,
                 "--timeout", "7", "--idle-timeout", "3"],
                input="Inspect", text=True, capture_output=True,
                env=dict(os.environ, VIOLIN_WORKER_RUNS=str(root / "runs"),
                         VIOLIN_QWEN_COMMAND=json.dumps([str(fake)]),
                         VIOLIN_QWEN_CUSTOM="1", VIOLIN_QWEN_PROTOCOL="text"),
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            report = json.loads(result.stdout)
            schema = json.loads((root_dir / "schemas/worker-report.schema.json").read_text())
            for name in schema["required"]:
                self.assertIn(name, report)
            self.assertEqual(report["effective_timeout_seconds"], 7)
            self.assertEqual(report["timeout_source"], "request")
            self.assertEqual(report["idle_timeout_seconds"], 3)
            self.assertTrue(report["idle_timeout_enabled"])


if __name__ == "__main__":
    unittest.main()
