# ADR 0006: Generic CLI and API provider transports

## Status

Accepted

## Decision

Every provider may expose a `cli`, `api`, or `auto` transport. CLI commands,
status commands, login commands, API endpoints, models, and API-key environment
names are configuration, not hardcoded local defaults.

`auto` chooses an authenticated configured transport but does not silently
switch from an explicitly configured, unauthenticated CLI to an API credential.
An unauthenticated request returns `auth_required` with a safe next action.

## Consequences

The public default is portable across users and harnesses. Local routes such as
`violin_lan` must be opt-in profiles. Global config and project policy can be
shared, while secrets remain in environment variables or the OS keychain.
