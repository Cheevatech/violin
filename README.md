# Qwen Global Workflow

## Go control plane and managed Laya model

Install the public launcher without installing Go or Python:

```bash
npx @cheevatech/violin doctor
npx @cheevatech/violin install
npx @cheevatech/violin init --dry-run
npx @cheevatech/violin init --apply
npx @cheevatech/violin uninstall --dry-run
npx @cheevatech/violin uninstall --apply
npx @cheevatech/violin config init --dry-run
npx @cheevatech/violin config init --apply
npx @cheevatech/violin skills install --apply
npx @cheevatech/violin auth status all
npx @cheevatech/violin health all
npx @cheevatech/violin auth login claude
npx @cheevatech/violin auth login qwen
npx @cheevatech/violin auth set qwen-api < /path/to/qwen-api-key.txt
```

The bundled public skill is a single `$violin` entrypoint covering delegation,
implementation, review, and security. `skills install` migrates the previous
managed `violin-implement`, `violin-review`, and `violin-security` directories
after creating a backup. `$violin-external-delegation` remains an optional
personal skill for installations that already provide it.

The npm launcher downloads a platform-specific Go release binary and verifies
its checksum before execution. Release targets are macOS/Linux on x64/arm64.
Configuration changes are previewed by default and backups are created before
applying them. Provider credentials must come from environment variables or the
OS keychain; never commit them to this repository.

The native Go provider boundary is available for Qwen, AGY, and Claude, with
offline parser tests, credential lookup, lifecycle recovery, and MCP/job
parity coverage. Configured CLI and API transports execute through the Go
worker; the existing Python worker entrypoints remain available as an explicit
compatibility path for legacy configurations.

Authentication is global to the current OS user, not to a Violin job or
session. Claude uses `claude auth login`; Qwen uses `codex login`; AGY has no
CLI login command and must use `VIOLIN_AGY_API_KEY` or the OS keychain. MCP can
read `auth_status` without exposing credentials. Login is explicit and is not
started automatically by an MCP request.

Qwen has generic `cli`, `api`, and `auto` transports. Public defaults do not
select an endpoint, model, or local provider. The original self-hosted route is
available only as an opt-in example profile under `examples/profiles/`.

The repository now contains a Go control-plane binary built with `make go-build`:

```bash
./bin/violin mcp
./bin/violin install
./bin/violin run --backend auto -C /absolute/workspace --task-file /path/task
./bin/violin model status
./bin/violin model verify
./bin/violin model update --manifest /path/manifest.json --source-dir /path/model-bundle
```

The Go binary owns MCP, job lifecycle, evidence descriptors, provider health,
and model activation.
`violin install` also installs the built-in Go Laya inference engine's verified
English model under the shared worker state directory. Its manifest must declare `language: "en"`;
multilingual checkpoints are intentionally outside this control-plane contract.
Activation is atomic and checksum failures leave the current model untouched.
Set `[laya].runner` only for development adapters and choose `shadow`,
`advisory`, or `active` in `[laya].mode`. The runtime receives the verified
active model directory as `VIOLIN_LAYA_MODEL_DIR`; `VIOLIN_LAYA_RUNNER` and
`VIOLIN_LAYA_MODE` are environment overrides. Shadow and advisory modes record
decisions without changing execution policy. Active mode uses confidence and
margin per policy head for backend, task mode, risk, timeout, and retry.
High-risk, uncertain-risk, and fallback decisions return `review_required`
without spawning unless the caller confirms a second request with
`risk_reviewed: true`. Explicit caller mode and timeout values take precedence.
Automatic retry is limited to provider failures during inspect jobs and stops
when the workspace snapshot changes; timeout, cancellation, and side-effecting
jobs are not retried.

Train from a reviewed English JSONL dataset with separate `train` and `holdout`
records containing `task`, `backend`, `task_mode`, `risk`, `timeout_policy`
(`short`, `standard`, or `long`), `retry_policy` (`never` or `inspect_once`),
`language: "en"`, `split`, and `reviewed: true`. Training rejects records
without the explicit English language label. Keep holdout data independent and do not put raw
production prompts in it. `./bin/violin model train --dataset reviewed.jsonl --version v3` installs
a candidate only when each label has at least 100 holdout examples, automatic
precision is at least 95%, high-risk recall is 100%, and per-head ECE is at most
0.10. Training never activates a candidate. Review it, then explicitly run
`./bin/violin model activate v3`; retain the previous version for rollback.
`laya_feedback` stores corrected labels and outcome by job ID without task text;
these events do not become training examples automatically. `model recalibrate`
remains a metadata summary. Keep Laya in shadow until a candidate passes replay
and operational tests. A base checkpoint is not production routing policy;
when no verified model is available, deterministic scheduler policy remains
authoritative. The existing Python entrypoints remain available as a
compatibility path during migration.

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

`~/.config/violin/config.toml` configures scheduler order, session and machine
limits, backend transport/auth policy, backend limits, and executable paths.
`violin config init` creates a credential-free starter file and refuses to
overwrite an existing config.
The legacy `~/.config/violin-agents/config.toml` remains supported during
migration, and `<workspace>/.violin/config.toml` can override non-secret project
policy. `VIOLIN_CONFIG`, the
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
timeouts are selected by mode: 900 seconds for `inspect` and 3600 seconds for
`implement`. `timeout_seconds` on MCP or `--timeout` on the CLI overrides the
default, subject to the configured maximum (14,400 seconds by default). The
effective timeout and its source are returned in the live response and stored
in the worker report.

Timeout policy can be changed without editing the launcher:

```toml
[timeouts]
max_seconds = 14400

[timeouts.defaults]
inspect = 900
implement = 3600
```

The equivalent environment overrides are
`VIOLIN_TIMEOUT_MAX_SECONDS`, `VIOLIN_INSPECT_TIMEOUT_SECONDS`, and
`VIOLIN_IMPLEMENT_TIMEOUT_SECONDS`. Built-in Qwen and AGY still use the hard
timeout only; custom commands retain idle-timeout protection.

For a machine-specific CLI command without storing it in a config file, set
`VIOLIN_<BACKEND>_CLI_COMMAND` to a JSON argv array. This value is never
treated as shell code. Built-in routes may set
`idle_timeout_enabled = false` explicitly when their provider can spend time
reasoning without emitting progress.

Backend commands can be replaced in the config with an argv list or a
shell-like string parsed without a shell. A config-file `command` is always
treated as a generic command, even when it contains only one executable name;
use the legacy `VIOLIN_<BACKEND>_BIN` environment variable when replacing only
the built-in launcher path. Supported placeholders are
`{workspace}`, `{task_file}`, `{result_file}`, `{prompt}`, `{mode}`,
`{timeout}`, and `{idle_timeout}`. Set `protocol` to `text` or `json` for a
generic CLI such as Hermes; `stdin = true` sends the generated task prompt on
stdin. Public defaults do not select a Qwen endpoint or model. The original
`qwen3.8-27b` through `violin_lan` route is an opt-in compatibility profile in
`examples/profiles/violin-lan-qwen.toml`.

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
elapsed time, PID, evidence directory, effective timeout policy, heartbeat
timestamps, and event count. The current lifecycle states are `starting`,
`progressing`, `reasoning`, `stalled`, `completed`, `failed`, and
`interrupted`. `wait_agent` returns this snapshot, including supervisor state
and evidence, when its wait interval expires. The worker idle timeout defaults
to 300 seconds and measures time since the last event or heartbeat; configure
it with `--idle-timeout` or `idle_timeout_seconds` on `spawn_agent`. Built-in
Qwen and AGY adapters do not apply idle timeout because their provider progress
streams are not reliable; they rely on the hard task timeout instead of
falsely treating normal reasoning time as idle. Custom commands still use idle
timeout.

The Go MCP server also exposes Laya tools in the same server: `laya_route`,
`laya_review_risk`, `laya_check_job`, `laya_wait_job`,
`laya_explain_decision`, and `laya_feedback`. Route and risk review are
read-only; feedback stores reviewed policy labels without task text. They reuse
Violin's existing job descriptors, evidence, supervisor, and verified English
model; there is no separate Laya MCP server.

When a run stops, inspect `report.json`, `status.json`, `task.txt`,
`output.json`, `stdout.log`, and `stderr.log` under the reported evidence path.
`provider_error`, `idle_timeout`, `timeout`, and `interrupted` identify common
failure causes. The descriptor persists the worker PID and lease so a new Go
MCP process can recover jobs while the process is still alive; completed jobs
are finalized from their report and their descriptor is cleaned up.

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
