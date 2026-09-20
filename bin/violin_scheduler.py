"""Shared configuration and machine-wide scheduler for external agents."""
import fcntl
import json
import os
from pathlib import Path
import time
import uuid


BACKENDS = ("agy", "qwen", "claude")
DEFAULTS = {
    "strategy": "round_robin",
    "order": list(BACKENDS),
    "session_max_concurrency": 10,
    "machine_max_concurrency": 13,
    "limits": {"agy": 10, "qwen": 1, "claude": 2},
    "commands": {},
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
                    if item.get("command"):
                        result["commands"][backend] = str(Path(item["command"]).expanduser())
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
    for backend in BACKENDS:
        result["limits"][backend] = _int(
            os.environ.get(f"VIOLIN_{backend.upper()}_MAX_CONCURRENCY"), result["limits"][backend])
        command = os.environ.get(f"VIOLIN_{backend.upper()}_BIN")
        if command:
            result["commands"][backend] = command
    if result["strategy"] != "round_robin":
        raise ValueError("scheduler.strategy must be round_robin")
    if not result["order"]:
        raise ValueError("scheduler.order must contain at least one supported backend")
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
                   **({"command": config["commands"][name]} if name in config["commands"] else {})}
            for name in BACKENDS
        },
    }


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
