# Qwen Global Workflow

## External workers under Codex supervision

`bin/violin-worker` runs Qwen through the existing Codex launcher, AGY with
the exact model `gemini-3.8-flash-medium`. It accepts task text on stdin or via
`--task-file`, so the supervisor can send a bounded task instead of copying the
whole conversation.

```bash
printf '%s' 'Read README.md and summarize the commands with source paths.' |
  ./bin/violin-worker agy -C /absolute/workspace
./bin/violin-worker claude -C /absolute/workspace
./bin/violin-worker qwen --mode implement -C /absolute/workspace --task-file /path/task.txt
```

Inspection is the default. Qwen uses a read-only sandbox; AGY uses plan mode
with its terminal sandbox (these are different enforcement mechanisms).
Implementation uses Qwen workspace-write, AGY accept-edits with sandbox, or
Claude Code `acceptEdits`. The MCP server and `violin-agent` CLI share a
configurable global round-robin scheduler. Defaults are AGY (10), Qwen (1),
then Claude Code (2), with a 10-job per-session limit and a 13-job
machine-wide limit. Full backends are skipped instead of filling the first
backend before falling back.
The runner does not bypass permissions. Run independent commands concurrently
through the supervisor's execution tool; retain its session handles and poll
the same handles to completion. Avoid overlapping writers.

One JSON report returns status, bounded summary, usage reported by the backend,
and an evidence directory under `~/.local/state/violin-workers`. Full logs and
the full result remain there when the summary is truncated. A completed worker
still requires Codex to inspect evidence and relevant validation before accepting
the task. Nonzero exits, missing results, backend errors, and timeouts fail the
run. The report follows `schemas/worker-report.schema.json`. No automatic
retry occurs, and a partial Qwen implementation is never retried on AGY.

`~/.config/violin-agents/config.toml` configures scheduler order, session and
machine limits, backend limits, and executable paths. `VIOLIN_CONFIG`, the
`VIOLIN_*_MAX_CONCURRENCY` variables, `VIOLIN_BACKEND_ORDER`, and backend bin
variables override the file. The CLI facade supports `run`, `list`, `wait`,
`interrupt`, and `config show|validate`:

```bash
violin-agent run --backend auto -C /absolute/workspace --task-file /path/task
violin-agent config show
```

Use `--session-id NAME` or `VIOLIN_SESSION_ID` when separate CLI invocations
should share one per-session limit.

`VIOLIN_WORKER_RUNS` overrides the shared lease/evidence location. Default
timeout is 900 seconds; `--timeout` changes it.

Backend commands can be replaced in the config with an argv list or a
shell-like string parsed without a shell. A config-file `command` is always
treated as a generic command, even when it contains only one executable name;
use the legacy `VIOLIN_<BACKEND>_BIN` environment variable when replacing only
the built-in launcher path. Supported placeholders are
`{workspace}`, `{task_file}`, `{result_file}`, `{prompt}`, `{mode}`,
`{timeout}`, and `{idle_timeout}`. Set `protocol` to `text` or `json` for a
generic CLI such as Hermes; `stdin = true` sends the generated task prompt on
stdin. With no override, the Qwen backend uses the pinned Codex profile
`qwen3.8-27b` through `violin_lan`.

```toml
[backend.qwen]
command = ["hermes", "run", "--workspace", "{workspace}", "--task-file", "{task_file}"]
protocol = "text"
stdin = false
health_command = ["hermes", "--health"]
```
Custom Qwen commands skip the built-in Qwen metadata/smoke preflight. If
`health_command` is configured, MCP and CLI run it without a shell before an
auto-selected Qwen job; otherwise the custom command is accepted without the
built-in Qwen preflight.
Qwen preflight is shared by `bin/violin-health` and `bin/violin-worker`. It
reports metadata as `healthy`, `degraded`, or `unavailable`, then runs a real
runtime smoke that proves the final response contains the pinned model and
provider. A custom model missing from the catalog is `degraded`, not synthetic
`healthy`; it is usable when smoke passes. Otherwise the worker reports
`metadata_unavailable` or `qwen_unhealthy`. Set `VIOLIN_CODEX_MODELS_CACHE` to
inspect a specific cache file.

Each run contains an atomic `status.json` with only phase, last activity,
elapsed time, PID, hard-timeout deadline, command type, and evidence directory.
The supported phases are `starting`, `metadata_check`, `backend_starting`,
`turn_started`, `reasoning`, `command_started`, `command_completed`,
`completed`, `failed`, `timeout`, and `interrupted`. `wait_agent` returns this
snapshot, including phase and evidence, when its wait interval expires. The
worker idle timeout defaults to 300 seconds and measures time since the last
event or heartbeat; configure it with `--idle-timeout` or
`idle_timeout_seconds` on `spawn_agent`. A recognized Qwen command-execution
item keeps the worker alive until its completion event; the built-in AGY JSON
adapter has no progress stream, so it relies on the hard task timeout instead
of falsely treating normal reasoning time as idle.

When a run stops, inspect `report.json`, `status.json`, `metadata.json`,
`process.json`, `stdout.log`, and `stderr.log` under the reported evidence path.
`metadata_unavailable`, `qwen_unhealthy`, `provider_error`,
`empty_final_response`, `idle_timeout`, `timeout`, `no_changes`, and
`interrupted` identify different failure causes. A worker
PID is recorded in `process.json`; the long-running `violin-agent-server` has a
different parent process and owns the MCP stdio pipe. The investigated incident
occurred after a successful shell command during turn progression, which is why
command completion and later silence are tracked separately.

Qwen currently reports zero usage through this gateway; that is missing metering,
not proof of zero tokens. No percentage of Codex token savings is claimed.

`bin/install-violin-agents` installs the MCP server, canonical worker, health
check, launcher, gate, and report schema from this repository, with a
timestamped backup. It refuses to overwrite unmanaged files and validates TOML
before changing anything. The
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
