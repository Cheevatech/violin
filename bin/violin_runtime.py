"""Shared worker-launch plumbing for the MCP server and CLI facade."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

from violin_scheduler import run_custom_health, worker_environment


ROOT = Path(__file__).resolve().parent
RUNNER = ROOT / "violin-worker"


def idle_timeout_enabled(config, backend):
    """Built-in Qwen and AGY use hard timeout as their only task guard."""
    return not (backend in ("agy", "qwen") and
                not config.get("custom_commands", {}).get(backend, False))


def qwen_health(config, health_script=None, timeout=45):
    """Run the canonical Qwen preflight used by both MCP and the CLI.

    The returned evidence is intentionally JSON-serialisable and is also safe
    to put in a bounded MCP response.  Custom health commands take precedence;
    built-in health keeps the metadata/smoke environment and exit semantics.
    """
    health_script = Path(health_script or ROOT.with_name("violin-health"))
    if config.get("custom_commands", {}).get("qwen"):
        healthy, evidence = run_custom_health(config, "qwen", timeout)
        if healthy is None:
            return True, {"status": "custom_command", "skipped": "builtin_qwen_health"}
        return healthy, evidence
    env = dict(os.environ, VIOLIN_METADATA_TIMEOUT="8", VIOLIN_SMOKE_TIMEOUT="35")
    qwen_bin = config.get("commands", {}).get("qwen")
    if qwen_bin and not config.get("custom_commands", {}).get("qwen"):
        env["VIOLIN_QWEN_BIN"] = str(qwen_bin)
    try:
        result = subprocess.run([str(health_script)], capture_output=True, text=True,
                                timeout=timeout, env=env)
        try:
            evidence = json.loads(result.stdout)
        except ValueError:
            evidence = {"status": "qwen_unhealthy", "stdout": result.stdout[-2000:]}
        evidence.setdefault("exit_code", result.returncode)
        return result.returncode == 0, evidence
    except (OSError, ValueError, subprocess.TimeoutExpired) as exc:
        return False, {"status": "qwen_unhealthy", "error": type(exc).__name__}


def launch_worker(root, cwd, backend, mode, task, timeout, idle_timeout, config,
                  prefix, stderr_filename="server-stderr.log", timeout_source=None):
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
                                  "evidence": str(directory),
                                  "effective_timeout_seconds": timeout,
                                  "timeout_source": config.get("timeout_source"),
                                  "idle_timeout_seconds": idle_timeout,
                                  "idle_timeout_enabled": idle_timeout_enabled(config, backend)}))
    config = dict(config)
    config["timeout_source"] = timeout_source
    env = worker_environment(dict(os.environ, VIOLIN_WORKER_STATUS=str(status),
                                  VIOLIN_TIMEOUT_SOURCE=str(timeout_source or "request")), config)
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
