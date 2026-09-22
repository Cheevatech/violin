---
name: violin-review
description: Review a Violin change for behavior, security, and contract regressions.
---

# Violin review

Review the Go MCP and job path end to end. Check public MCP names and schemas,
Laya decision fields (`confidence`, `fallback`, `model_version`, and reasons),
report/status parity, timeout and idle-timeout behavior, supervisor heartbeat
and lifecycle state, job recovery, credential handling, and tests.

Verify that explicit caller timeouts are never extended, implement jobs with
side effects are not retried automatically, and `laya_wait_job` reduces caller
polling without hiding a stalled worker. Treat a completed process as evidence
to inspect, not as proof that a change is correct.
