# ADR 0001: Go-native Violin runtime

## Status

Accepted

## Context

Violin currently has a Go control-plane foundation and Python worker/runtime
entrypoints. A public release must be installable without requiring Python and
must have one portable lifecycle implementation.

## Decision

The public runtime is implemented in Go. Python files may remain temporarily as
legacy behavior references during migration, but they are not a production
runtime dependency of the release artifact.

## Consequences

The MCP/report contracts and provider behavior need parity tests. Provider
adapters and native model execution become Go-owned boundaries, while user
credentials and provider services remain external.
