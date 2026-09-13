# Qwen Global Workflow

## External workers under Codex supervision

`bin/violin-worker` runs Qwen through the existing Codex launcher, or AGY with
the exact model `gemini-3.8-flash-medium`. It accepts task text on stdin or via
`--task-file`, so the supervisor can send a bounded task instead of copying the
whole conversation.

```bash
printf '%s' 'Read README.md and summarize the commands with source paths.' |
  ./bin/violin-worker agy -C /absolute/workspace
./bin/violin-worker qwen --mode implement -C /absolute/workspace --task-file /path/task.txt
```

Inspection is the default. Qwen uses a read-only sandbox; AGY uses plan mode
with its terminal sandbox (these are different enforcement mechanisms).
Implementation uses Qwen workspace-write or AGY accept-edits with sandbox.
The runner does not bypass permissions. Run independent commands concurrently
through the supervisor's execution tool; retain its session handles and poll
the same handles to completion. Avoid overlapping writers.

One JSON report returns status, bounded summary, usage reported by the backend,
and an evidence directory under `~/.local/state/violin-workers`. Full logs and
the full result remain there when the summary is truncated. A completed worker
still requires Codex to inspect evidence and relevant validation before accepting
the task. Nonzero exits, missing results, backend errors, and timeouts fail the
run. No automatic retry or paid-model fallback occurs.

`VIOLIN_QWEN_BIN`, `VIOLIN_AGY_BIN`, and `VIOLIN_WORKER_RUNS` override executable
and evidence locations. Default timeout is 900 seconds; `--timeout` changes it.
Qwen requires valid effective metadata for `qwen3.8-27b`. Preflight checks the
cache first, then renders the effective Codex catalog with the pinned
`violin_lan` Responses configuration when the custom model is absent from the
official cache. It verifies the context window, reasoning modes, provider, and
Responses API support before starting Codex. If both sources are unavailable or
stale it returns `metadata_unavailable`; it never silently uses fallback
metadata. Set `VIOLIN_CODEX_MODELS_CACHE` to inspect a specific cache file.

Each run contains an atomic `status.json` with only phase, last activity,
elapsed time, PID, hard-timeout deadline, command type, and evidence directory.
The supported phases are `starting`, `metadata_check`, `backend_starting`,
`turn_started`, `reasoning`, `command_started`, `command_completed`,
`completed`, `failed`, `timeout`, and `interrupted`. `wait_agent` returns this
snapshot, including phase and evidence, when its wait interval expires. The
worker idle timeout defaults to 300 seconds and measures time since the last
event or heartbeat; configure it with `--idle-timeout` or
`idle_timeout_seconds` on `spawn_agent`.

When a run stops, inspect `report.json`, `status.json`, `metadata.json`,
`process.json`, `stdout.log`, and `stderr.log` under the reported evidence path.
`metadata_unavailable`, `idle_timeout`, `timeout`, `interrupted`, and
`failure_reason: provider_failure` identify different failure causes. A worker
PID is recorded in `process.json`; the long-running `violin-agent-server` has a
different parent process and owns the MCP stdio pipe. The investigated incident
occurred after a successful shell command during turn progression, which is why
command completion and later silence are tracked separately.

Qwen currently reports zero usage through this gateway; that is missing metering,
not proof of zero tokens. No percentage of Codex token savings is claimed.

`bin/install-violin-agents` installs the MCP server and global supervisor
guidance into `~/.codex`, with a timestamped backup. It refuses to overwrite an
unmanaged existing server and validates TOML before changing anything. The
MCP route was tested through Codex's real tool protocol; native
`collaboration.spawn_agent` remains unsupported for this external provider in
Codex 0.153.2.

Global launchers for running the self-hosted Qwen profile through the Codex
harness.

## Commands

```bash
violin-qwen-plan -C /path/to/repo "Design the implementation"
violin-qwen-implement -C /path/to/repo "Implement the change"
violin-qwen-review -C /path/to/repo "Review the current diff"
violin-qwen-verify -C /path/to/repo "Run the relevant checks"
```

Plan, review, and verify use a read-only sandbox. Implement uses
`workspace-write`.

## Verification gate

```bash
./bin/qwen-verify-gate --repo /path/to/repo -- npm test
```

## Development

```bash
make test
./eval/run-smoke-tests
./eval/run-tool-smoke-tests
```

Qwen uses `/Users/film/bin/serena-bridge` for semantic navigation because the
self-hosted Responses route currently cannot dispatch native Serena MCP calls.
Native Serena remains enabled for the regular GPT/Codex profile.
