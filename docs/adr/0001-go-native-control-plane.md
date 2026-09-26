# ADR 0001: Go-only Violin runtime

## Status

Accepted

## Context

Violin's public runtime and control plane are Go. A public release must not
require a Python interpreter, Python package environment, or Python sidecar.
The MCP server, worker lifecycle, provider adapters, and model execution must
share one portable lifecycle implementation.

## Decision

The complete production runtime is implemented in Go. Python files may remain
as development tools or legacy references, but production code must not invoke
them. This applies to model inference as well as the control plane.

## Consequences

The MCP/report contracts, provider behavior, and model inference are Go-owned
boundaries and need parity tests. Model weights may be provisioned as data
artifacts; user credentials and provider services remain external.
