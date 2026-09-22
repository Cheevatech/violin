"""Shared configuration and machine-wide scheduler for external agents."""
import fcntl
import json
import os
from pathlib import Path
import shlex
import subprocess
import time
import uuid


BACKENDS = ("agy", "qwen", "claude")
SUPPORTED_PROTOCOLS = frozenset(("text", "json", "qwen", "agy", "claude"))
DEFAULTS = {
    "strategy": "round_robin",
    "order": list(BACKENDS),
    "session_max_concurrency": 10,
    "machine_max_concurrency": 13,
    "limits": {"agy": 10, "qwen": 1, "claude": 2},
    "commands": {},
    "custom_commands": {},
    "health_commands": {},
    "protocols": {"agy": "agy", "qwen": "qwen", "claude": "claude"},
    "stdin": {"agy": False, "qwen": True, "claude": False},
    "timeouts": {"max_seconds": 14400, "defaults": {"inspect": 900, "implement": 3600}},
}


def _int(value, default, minimum=0):
    try:
        return max(minimum, int(value))
    except (TypeError, ValueError):
        return default


def default_config_path():
    return Path(os.environ.get("VIOLIN_CONFIG", str(Path.home() / ".config/violin-agents/config.toml"))).expanduser()


def _normalise(value):
    result = dict(DEFAULTS)
    result["limits"] = dict(DEFAULTS["limits"])
    result["commands"] = {}
    result["custom_commands"] = {}
    result["health_commands"] = {}
    result["protocols"] = dict(DEFAULTS["protocols"])
    result["stdin"] = dict(DEFAULTS["stdin"])
    result["timeouts"] = {"max_seconds": DEFAULTS["timeouts"]["max_seconds"],
                           "defaults": dict(DEFAULTS["timeouts"]["defaults"])}
    if isinstance(value, dict):
        scheduler = value.get("scheduler", value)
        if isinstance(scheduler, dict):
            result["strategy"] = scheduler.get("strategy", result["strategy"])
            order = scheduler.get("order", result["order"])
            if isinstance(order, list):
                result["order"] = [x for x in order if x in BACKENDS]
            result["session_max_concurrency"] = _int(
                scheduler.get("session_max_concurrency"), result["session_max_concurrency"])
            result["machine_max_concurrency"] = _int(
                scheduler.get("machine_max_concurrency"), result["machine_max_concurrency"])
        backends = value.get("backend", value.get("backends", {}))
        if isinstance(backends, dict):
            for backend in BACKENDS:
                item = backends.get(backend, {})
                if isinstance(item, dict):
                    result["limits"][backend] = _int(item.get("max_concurrency"), result["limits"][backend])
                    if "command" in item and item["command"] is not None:
                        command = item["command"]
                        result["commands"][backend] = ([str(x) for x in command]
                                                        if isinstance(command, list)
                                                        else (str(command) if any(c.isspace() for c in str(command))
                                                              else str(Path(command).expanduser())))
                        result["custom_commands"][backend] = True
                        if ("protocol" not in item and
                                (isinstance(command, list) or any(c.isspace() for c in str(command)))):
                            result["protocols"][backend] = "text"
                            result["stdin"][backend] = False
                    if "health_command" in item and item["health_command"] is not None:
                        health_command = item["health_command"]
                        result["health_commands"][backend] = ([str(x) for x in health_command]
                                                               if isinstance(health_command, list)
                                                               else str(health_command))
                    if item.get("protocol"):
                        result["protocols"][backend] = str(item["protocol"])
                    if "stdin" in item:
                        result["stdin"][backend] = bool(item["stdin"])
        timeouts = value.get("timeouts", {})
        if isinstance(timeouts, dict):
            result["timeouts"]["max_seconds"] = _int(
                timeouts.get("max_seconds"), result["timeouts"]["max_seconds"], 1)
            defaults = timeouts.get("defaults", {})
            if isinstance(defaults, dict):
                for mode in ("inspect", "implement"):
                    result["timeouts"]["defaults"][mode] = _int(
                        defaults.get(mode), result["timeouts"]["defaults"][mode], 1)
    if not result["order"]:
        result["order"] = list(BACKENDS)
    return result


def load_config(path=None):
    config = {}
    path = Path(path).expanduser() if path else default_config_path()
    if path.exists():
        try:
            import tomllib
            config = tomllib.loads(path.read_text())
        except (OSError, ValueError):
            raise ValueError(f"Invalid agent config: {path}")
    result = _normalise(config)
    env_order = os.environ.get("VIOLIN_BACKEND_ORDER")
    if env_order:
        result["order"] = [x.strip() for x in env_order.split(",") if x.strip() in BACKENDS]
    result["strategy"] = os.environ.get("VIOLIN_SCHEDULER_STRATEGY", result["strategy"])
    result["session_max_concurrency"] = _int(
        os.environ.get("VIOLIN_SESSION_MAX_CONCURRENCY"), result["session_max_concurrency"])
    result["machine_max_concurrency"] = _int(
        os.environ.get("VIOLIN_MACHINE_MAX_CONCURRENCY"), result["machine_max_concurrency"])
    result["timeouts"]["max_seconds"] = _int(
        os.environ.get("VIOLIN_TIMEOUT_MAX_SECONDS"), result["timeouts"]["max_seconds"], 1)
    for mode in ("inspect", "implement"):
        result["timeouts"]["defaults"][mode] = _int(
            os.environ.get(f"VIOLIN_{mode.upper()}_TIMEOUT_SECONDS"),
            result["timeouts"]["defaults"][mode], 1)
    if any(value > result["timeouts"]["max_seconds"]
           for value in result["timeouts"]["defaults"].values()):
        raise ValueError("timeouts.defaults must not exceed timeouts.max_seconds")
    for backend in BACKENDS:
        result["limits"][backend] = _int(
            os.environ.get(f"VIOLIN_{backend.upper()}_MAX_CONCURRENCY"), result["limits"][backend])
        command = os.environ.get(f"VIOLIN_{backend.upper()}_BIN")
        if command:
            result["commands"][backend] = command
            result["custom_commands"][backend] = False
        command_template = os.environ.get(f"VIOLIN_{backend.upper()}_COMMAND")
        if command_template:
            try:
                command_template = json.loads(command_template)
            except ValueError:
                pass
            result["commands"][backend] = command_template
            result["custom_commands"][backend] = True
        health_command = os.environ.get(f"VIOLIN_{backend.upper()}_HEALTH_COMMAND")
        if health_command:
            try:
                health_command = json.loads(health_command)
            except ValueError:
                pass
            result["health_commands"][backend] = health_command
        protocol = os.environ.get(f"VIOLIN_{backend.upper()}_PROTOCOL")
        if protocol:
            result["protocols"][backend] = protocol
        stdin = os.environ.get(f"VIOLIN_{backend.upper()}_STDIN")
        if stdin is not None:
            result["stdin"][backend] = stdin.lower() in ("1", "true", "yes", "on")
    if result["strategy"] != "round_robin":
        raise ValueError("scheduler.strategy must be round_robin")
    if not result["order"]:
        raise ValueError("scheduler.order must contain at least one supported backend")
    if any(value > result["timeouts"]["max_seconds"]
           for value in result["timeouts"]["defaults"].values()):
        raise ValueError("timeouts.defaults must not exceed timeouts.max_seconds")
    for backend in BACKENDS:
        protocol = result["protocols"][backend]
        if protocol not in SUPPORTED_PROTOCOLS:
            raise ValueError(f"unsupported protocol for {backend}: {protocol}")
        if backend in result["commands"]:
            command = result["commands"][backend]
            if not isinstance(command, (str, list)) or not command:
                raise ValueError(f"command for {backend} must be a non-empty string or list")
            if isinstance(command, list) and not all(str(part) for part in command):
                raise ValueError(f"command for {backend} contains an empty argument")
        if backend in result["health_commands"]:
            health_command = result["health_commands"][backend]
            if not isinstance(health_command, (str, list)) or not health_command:
                raise ValueError(f"health_command for {backend} must be a non-empty string or list")
            if isinstance(health_command, list) and not all(str(part) for part in health_command):
                raise ValueError(f"health_command for {backend} contains an empty argument")
    return result


def config_view(config):
    return {
        "scheduler": {
            "strategy": config["strategy"],
            "order": config["order"],
            "session_max_concurrency": config["session_max_concurrency"],
            "machine_max_concurrency": config["machine_max_concurrency"],
        },
        "backend": {
            name: {"max_concurrency": config["limits"][name],
                   **({"command": config["commands"][name]} if name in config["commands"] else {}),
                   **({"health_command": config["health_commands"][name]}
                      if name in config.get("health_commands", {}) else {}),
                   "protocol": config["protocols"][name],
                   "stdin": config["stdin"][name]}
            for name in BACKENDS
        },
        "timeouts": config["timeouts"],
    }


def resolve_timeout(config, mode, requested=None):
    """Return (seconds, source) for a server/CLI job timeout."""
    policy = config["timeouts"]
    maximum = policy["max_seconds"]
    if requested is not None:
        if type(requested) is not int or not 1 <= requested <= maximum:
            raise ValueError(f"timeout_seconds must be between 1 and {maximum}")
        return requested, "request"
    return policy["defaults"][mode], f"default:{mode}"


def run_custom_health(config, backend, timeout=45):
    """Run an optional configured health command without invoking a shell."""
    command = config.get("health_commands", {}).get(backend)
    if not command:
        return None, None
    try:
        parts = [str(part) for part in command] if isinstance(command, list) else shlex.split(str(command))
        if not parts:
            raise ValueError("health command is empty")
        result = subprocess.run(parts, capture_output=True, text=True, timeout=timeout)
        evidence = {
            "status": "custom_health",
            "exit_code": result.returncode,
            "stdout": result.stdout[-2000:],
            "stderr": result.stderr[-2000:],
        }
        return result.returncode == 0, evidence
    except (OSError, ValueError, subprocess.TimeoutExpired) as exc:
        return False, {"status": "custom_health", "error": type(exc).__name__, "message": str(exc)}


def worker_environment(environment, config):
    """Export backend command adapters for the low-level worker."""
    result = dict(environment)
    for backend, command in config["commands"].items():
        prefix = f"VIOLIN_{backend.upper()}_"
        custom = (config.get("custom_commands", {}).get(backend, False) or
                  isinstance(command, list) or
                  (isinstance(command, str) and any(c.isspace() for c in command)) or
                  config["protocols"][backend] != backend or
                  config["stdin"][backend] != DEFAULTS["stdin"][backend])
        if custom:
            result[prefix + "COMMAND"] = json.dumps(command)
            result[prefix + "CUSTOM"] = "1"
            result[prefix + "PROTOCOL"] = config["protocols"][backend]
            result[prefix + "STDIN"] = "1" if config["stdin"][backend] else "0"
        else:
            result[prefix + "BIN"] = str(command)
    return result


def process_alive(pid):
    if not isinstance(pid, int) or pid <= 0:
        return False
    try:
        os.kill(pid, 0)
        return True
    except (ProcessLookupError, PermissionError):
        return False


class Scheduler:
    def __init__(self, root, config=None, session_id=None):
        self.root = Path(root).expanduser()
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.config = config or load_config()
        self.session_id = session_id or str(os.getpid())

    @property
    def lease_dir(self):
        directory = self.root / "leases"
        directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        return directory

    def _paths(self):
        directory = self.lease_dir
        return directory, directory / ".lock", directory / ".scheduler.json"

    def _read_active(self, directory):
        active = []
        for path in directory.glob("*.json"):
            if path.name == ".scheduler.json":
                continue
            try:
                lease = json.loads(path.read_text())
            except (ValueError, OSError):
                path.unlink(missing_ok=True)
                continue
            worker_alive = process_alive(lease.get("worker_pid"))
            owner_alive = process_alive(lease.get("owner_pid", lease.get("server_pid")))
            if (lease.get("worker_pid") is not None and not worker_alive) or (
                    lease.get("worker_pid") is None and not owner_alive):
                path.unlink(missing_ok=True)
                continue
            active.append((path, lease))
        return active

    def reserve(self, requested="auto", unavailable=()):
        directory, lock_path, state_path = self._paths()
        with lock_path.open("a+") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            active = self._read_active(directory)
            counts = {backend: sum(x.get("backend") == backend for _, x in active) for backend in BACKENDS}
            session_count = sum(x.get("session_id") == self.session_id for _, x in active)
            quota = dict(self.config["limits"])
            if session_count >= self.config["session_max_concurrency"]:
                return None, counts, quota, "session_capacity_full"
            if sum(counts.values()) >= self.config["machine_max_concurrency"]:
                return None, counts, quota, "machine_capacity_full"

            if requested != "auto":
                candidates = [requested]
            else:
                try:
                    state = json.loads(state_path.read_text())
                except (OSError, ValueError):
                    state = {"index": 0}
                start = state.get("index", 0) % len(self.config["order"])
                candidates = [self.config["order"][(start + i) % len(self.config["order"])]
                             for i in range(len(self.config["order"]))]

            selected = next((backend for backend in candidates
                             if backend not in unavailable and
                             counts[backend] < quota[backend]), None)
            if selected is None:
                return None, counts, quota, "backend_capacity_full"
            if requested == "auto":
                next_index = (self.config["order"].index(selected) + 1) % len(self.config["order"])
                state_path.write_text(json.dumps({"index": next_index}))
                selection_reason = "round_robin_skip" if selected != candidates[0] else None
            else:
                selection_reason = None
            lease_path = directory / f"{uuid.uuid4()}.json"
            lease_path.write_text(json.dumps({"backend": selected, "owner_pid": os.getpid(),
                                              "server_pid": os.getpid(), "worker_pid": None,
                                              "session_id": self.session_id, "created_at": time.time()}))
            return lease_path, counts, quota, selection_reason

    @staticmethod
    def update_lease(path, worker_pid):
        try:
            value = json.loads(Path(path).read_text())
            value["worker_pid"] = worker_pid
            Path(path).write_text(json.dumps(value))
        except (ValueError, OSError):
            pass

    @staticmethod
    def release_lease(path):
        if path:
            Path(path).unlink(missing_ok=True)
