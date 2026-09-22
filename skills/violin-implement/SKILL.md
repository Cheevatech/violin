---
name: violin-implement
description: Implement bounded changes through Violin while preserving contracts.
---

# Violin implementation

Use a bounded task, an absolute workspace path, explicit acceptance criteria,
and the smallest relevant validation. Violin's public MCP and job control are
Go-owned; Python entrypoints are compatibility paths.

Before spawning work, use `laya_route` for backend, mode, timeout, risk, and
execution-target advice. For a proposed implementation, use
`laya_review_risk` first. Use `laya_wait_job` instead of repeatedly polling a
worker; use `laya_check_job` only when an intermediate status is needed.

After completion, inspect the returned report, `status.json`, supervisor
state, changed files, and evidence. A completed process is not proof that the
task succeeded. Never let a worker revert unrelated work or expose credentials.
