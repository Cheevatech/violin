"""Shared worker-launch plumbing for the MCP server and CLI facade."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

from violin_scheduler import worker_environment


ROOT = Path(__file__).resolve().parent
RUNNER = ROOT / "violin-worker"


def idle_timeout_enabled(config, backend):
    """Built-in Qwen and AGY use hard timeout as their only task guard."""
    return not (backend in ("agy", "qwen") and
                not config.get("custom_commands", {}).get(backend, False))


def launch_worker(root, cwd, backend, mode, task, timeout, idle_timeout, config,
                  prefix, stderr_filename="server-stderr.log"):
    """Create an evidence bundle and launch one canonical worker process."""
    root = Path(root)
    cwd = Path(cwd)
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    directory = Path(tempfile.mkdtemp(prefix=prefix, dir=root))
    taskfile = directory / "task.txt"
    taskfile.write_text(task)
    output = directory / "output.json"
    status = directory / "status.json"
    status.write_text(json.dumps({"phase": "starting", "last_event_at": time.time(),
                                  "elapsed_seconds": 0, "pid": None,
                                  "timeout_deadline": None, "command_type": None,
                                  "evidence": str(directory)}))
    env = worker_environment(dict(os.environ, VIOLIN_WORKER_STATUS=str(status)), config)
    with output.open("w") as out, (directory / stderr_filename).open("w") as err:
        process = subprocess.Popen(
            [str(RUNNER), backend, "--mode", mode, "-C", str(cwd),
             "--task-file", str(taskfile), "--timeout", str(timeout),
             "--idle-timeout", str(idle_timeout)],
            stdin=subprocess.DEVNULL, stdout=out, stderr=err, env=env,
            start_new_session=True)
    return {"directory": directory, "taskfile": taskfile, "output": output,
            "status": status, "process": process,
            "idle_timeout_enabled": idle_timeout_enabled(config, backend)}
