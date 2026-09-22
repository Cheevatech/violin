# ADR 0004: Repository-owned skill bundles

## Status

Accepted

## Decision

Skills live in the public repository as versioned English-first source files.
Installation into Codex or Claude directories is explicit, previewable, and
backup-protected.

## Consequences

Skill installation must detect unmanaged files and must never overwrite them
without an explicit user action.
