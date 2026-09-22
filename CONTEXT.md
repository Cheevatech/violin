# Violin domain glossary

This document defines the public vocabulary for Violin. It intentionally does
not describe implementation details.

## Control Plane

The Violin service that accepts agent requests, applies policy, schedules work,
and reports lifecycle state.

## Provider

An external model service capable of executing a bounded task. Qwen, AGY, and
Claude are providers; Violin does not own their credentials or models.

## Provider Adapter

The boundary that translates the Violin provider contract to one Provider's
request, event, health, cancellation, and error semantics.

## Job

A durable unit of delegated work with an identity, lifecycle, policy metadata,
evidence, and a final report.

## Worker

The execution process associated with a Job. A Worker may be remote from the
Control Plane, but the Job remains owned by Violin's lifecycle contract.

## Laya Engine

Violin's policy component for producing typed routing or safety decisions. Laya
uses an English control protocol and may fall back to deterministic policy.

## Model Artifact

A versioned, checksummed model bundle managed independently from the Violin
binary.

## Release Artifact

A signed, platform-specific Violin binary distributed through GitHub Releases
and installed by the npm launcher.

## Skill Bundle

Versioned agent guidance shipped with the repository and optionally installed
into a user's agent skill directory.

## Credential Source

An environment variable or OS keychain entry from which a provider secret is
resolved at runtime. Credentials are never source-controlled.

## Compatibility Contract

The externally observable MCP tools, report fields, timeout semantics, job
lifecycle, and fallback behavior that releases must preserve.
