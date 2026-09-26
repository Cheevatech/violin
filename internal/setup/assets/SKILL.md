---
name: violin
description: Use Violin to route, delegate, implement, review, and secure bounded AI work.
---

# Violin

Use this single entrypoint for Violin workflows. Keep every task bounded,
provide an absolute workspace path, define acceptance criteria, and preserve
the main model as the supervisor.

## Delegate

Use Violin's external-agent MCP tools when an independent bounded slice will
benefit from Qwen, AGY, or Claude. Send only the smallest useful task and
necessary source paths. Use `backend: "auto"` by default, keep at most three
workers, poll the same agent id until terminal, and inspect evidence before
acceptance. Do not silently retry or fall back to a paid provider.

Before spawning, use `laya_route` for backend, mode, timeout, risk, and
execution-target advice.

When `spawn_agent` returns `status: "review_required"`, do not retry unchanged.
Have the main model review the task; spawn again with `risk_reviewed: true` only
after that review. Explicit backend, mode, and timeout settings take precedence.
Use `mode: "auto"` only when Laya may choose inspect versus implement.

## Implement

For implementation work, use `laya_review_risk` before changing files. Give the
worker explicit write scope and acceptance criteria. Use `laya_wait_job` rather
than repeatedly polling; use `laya_check_job` only when an intermediate status
is needed. The public MCP, job control, and Laya inference runtime are Go-owned;
Python entrypoints are not part of the supported installation or runtime.

After completion, inspect the returned report, `status.json`, supervisor state,
changed files, and evidence. A completed process is not proof that the task
succeeded, and unrelated work must not be reverted.

## Review

Review the Go MCP and job path end to end. Check public MCP names and schemas,
Laya decision fields (`confidence`, `fallback`, `model_version`, and reasons),
report/status parity, timeout and idle-timeout behavior, supervisor heartbeat
and lifecycle state, job recovery, credential handling, and tests.

Verify that explicit caller timeouts are never extended, implement jobs with
side effects are not retried automatically, and `laya_wait_job` reduces caller
polling without hiding a stalled worker.

## Security

Never commit provider credentials, local configuration, model caches, or
private keys. Preview configuration changes, preserve backups, redact secrets,
and run secret scanning before release.

Laya route, risk-review, and job-inspection tools are read-only/advisory: they
must not execute arbitrary shell, modify files, spawn workers during route/risk
review, or return API keys,
tokens, raw secret-bearing environment values, or private model paths. The
`laya_feedback` tool only appends reviewed labels and outcomes by known job ID;
it must never receive task text. Model updates must use the verified
manifest/checksum flow, and model activation must be explicit after replay
gates pass. Skill installation must remain previewable, backup-protected, and
must not overwrite unmanaged files.
