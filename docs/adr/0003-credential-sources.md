# ADR 0003: Environment and OS keychain credentials

## Status

Accepted

## Decision

Violin resolves provider secrets from environment variables for CI/headless
execution and from the platform keychain for interactive use. Secrets are
never written to repository files, reports, logs, or generated configuration.

## Consequences

The CLI must provide diagnostics that identify missing credentials without
printing their values. macOS uses `security`; Linux uses `secret-tool` when
available.
