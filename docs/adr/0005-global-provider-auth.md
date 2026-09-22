# ADR 0005: Global provider authentication

## Status

Accepted

## Decision

Violin treats provider authentication as OS-user-global state. Claude and Qwen
delegate interactive login to their existing CLIs; AGY uses an environment
variable or OS keychain because its CLI has no login command. Jobs and sessions
reference provider names only and never persist tokens.

The CLI exposes explicit `auth status` and `auth login` commands. MCP exposes a
read-only `auth_status` tool; it never starts an interactive login flow.

## Consequences

Authentication is performed once per user and reused by all Violin sessions.
MCP callers must request login through the local CLI when a provider reports
`auth_required`, and provider status output must never include secret values.
