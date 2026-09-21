import json
from pathlib import Path
import os
import tempfile
import unittest

from bin import violin_scheduler


class SchedulerTests(unittest.TestCase):
    def config(self, **overrides):
        value = {
            "strategy": "round_robin",
            "order": ["agy", "qwen", "claude"],
            "session_max_concurrency": 10,
            "machine_max_concurrency": 13,
            "limits": {"agy": 10, "qwen": 1, "claude": 2},
            "commands": {},
        }
        value.update(overrides)
        return value

    def test_round_robin_skips_full_backend(self):
        with tempfile.TemporaryDirectory() as tmp:
            scheduler = violin_scheduler.Scheduler(tmp, self.config())
            leases = []
            selected = []
            reasons = []
            for _ in range(5):
                lease, _, _, reason = scheduler.reserve("auto")
                selected.append(json.loads(lease.read_text())["backend"])
                reasons.append(reason)
                leases.append(lease)
            self.assertEqual(selected, ["agy", "qwen", "claude", "agy", "claude"])
            self.assertEqual(reasons, [None, None, None, None, "round_robin_skip"])
            for lease in leases:
                scheduler.release_lease(lease)

    def test_session_and_machine_limits_are_both_enforced(self):
        with tempfile.TemporaryDirectory() as tmp:
            scheduler = violin_scheduler.Scheduler(
                tmp, self.config(session_max_concurrency=2, machine_max_concurrency=3))
            first = scheduler.reserve("auto")
            second = scheduler.reserve("auto")
            blocked = scheduler.reserve("auto")
            self.assertIsNotNone(first[0])
            self.assertIsNotNone(second[0])
            self.assertEqual(blocked[3], "session_capacity_full")
            for result in (first, second):
                scheduler.release_lease(result[0])

    def test_machine_limit_is_shared_across_sessions(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = self.config(session_max_concurrency=10, machine_max_concurrency=2)
            first_session = violin_scheduler.Scheduler(tmp, config, session_id="session-1")
            second_session = violin_scheduler.Scheduler(tmp, config, session_id="session-2")
            first = first_session.reserve("auto")
            second = second_session.reserve("auto")
            blocked = first_session.reserve("auto")
            self.assertIsNotNone(first[0])
            self.assertIsNotNone(second[0])
            self.assertEqual(blocked[3], "machine_capacity_full")
            first_session.release_lease(first[0])
            second_session.release_lease(second[0])

    def test_config_file_and_environment_overrides(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "config.toml"
            path.write_text("""[scheduler]
strategy = "round_robin"
order = ["claude", "agy", "qwen"]
session_max_concurrency = 4
machine_max_concurrency = 7

[backend.claude]
max_concurrency = 3
command = "/custom/claude"
""")
            old = {key: os.environ.get(key) for key in
                   ("VIOLIN_SESSION_MAX_CONCURRENCY", "VIOLIN_BACKEND_ORDER")}
            try:
                os.environ["VIOLIN_SESSION_MAX_CONCURRENCY"] = "8"
                os.environ["VIOLIN_BACKEND_ORDER"] = "qwen,agy,claude"
                config = violin_scheduler.load_config(path)
            finally:
                for key, value in old.items():
                    if value is None:
                        os.environ.pop(key, None)
                    else:
                        os.environ[key] = value
            self.assertEqual(config["session_max_concurrency"], 8)
            self.assertEqual(config["order"], ["qwen", "agy", "claude"])
            self.assertEqual(config["limits"]["claude"], 3)
            self.assertEqual(config["commands"]["claude"], "/custom/claude")

    def test_config_command_is_always_custom_even_without_arguments(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "config.toml"
            path.write_text('[backend.qwen]\ncommand = "hermes"\nprotocol = "text"\n')
            config = violin_scheduler.load_config(path)
            environment = violin_scheduler.worker_environment({}, config)
            self.assertEqual(config["custom_commands"]["qwen"], True)
            self.assertEqual(environment["VIOLIN_QWEN_COMMAND"], '"hermes"')
            self.assertEqual(environment["VIOLIN_QWEN_CUSTOM"], "1")
            self.assertNotIn("VIOLIN_QWEN_BIN", environment)

    def test_invalid_custom_protocol_is_rejected_early(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "config.toml"
            path.write_text('[backend.agy]\ncommand = ["hermes"]\nprotocol = "ndjson"\n')
            with self.assertRaisesRegex(ValueError, "unsupported protocol"):
                violin_scheduler.load_config(path)


if __name__ == "__main__":
    unittest.main()
