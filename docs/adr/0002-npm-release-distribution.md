# ADR 0002: npm launcher for signed release binaries

## Status

Accepted

## Context

Users should be able to install Violin without installing Go or Python.

## Decision

The `violin` npm package is a thin launcher. It downloads the matching Go
release artifact for macOS/Linux x64/arm64, verifies a signed manifest and
SHA-256 checksum, then executes the cached binary.

## Consequences

GitHub Releases and signing become part of the release process. The npm package
does not silently mutate user configuration during install.
