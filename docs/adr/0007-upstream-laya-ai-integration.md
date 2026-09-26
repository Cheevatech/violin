# ADR 0007: Run upstream Laya decisions in Violin's Go runtime

## Status

Accepted

## Context

The name `Laya` has referred both to Violin's `internal/laya` policy component
and to the upstream Laya AI project by Convai Innovations. They are different
things. Upstream Laya AI is a local, non-autoregressive model family for typed
decisions (`choice`, `score`, and `noul`); it does not generate free-form text.
Its package provides an optional MCP server, but Violin already exposes its own
Laya tools and owns task routing, risk review, provider selection, job state,
and reporting.

The current local Qwen/llama.cpp runtime is a generative-model adapter. It is
not the upstream Laya AI model and must not be described or shipped as Laya AI.
ADR 0001 requires the production runtime, including model inference, to be Go;
the upstream Python SDK is a reference implementation and is not a production
dependency.

## Decision

Violin will use upstream Laya checkpoint artifacts and decision semantics
through a Go implementation behind Violin's existing `internal/laya` boundary.
Production code must not install or invoke the upstream Python package, a Python
runner, or another language sidecar. The Violin MCP server remains the only MCP
server configured for Violin.

The native inference path uses the upstream checkpoint weights exported as
ONNX, executed in-process through ONNX Runtime's C API via Go CGO bindings.
Violin owns the pinned export bundles and their SHA-256 manifests. The release
pipeline must generate and validate these bundles from the upstream checkpoint
revision before publishing them. User installations download both English
`typed-decisions` and multilingual checkpoints so inference does not need model
downloads later. Only one checkpoint session stays resident at a time.

The release workflow uses the ONNX conversion published at the pinned
`codenamev/laya-onnx` revision. Its exporter records upstream Laya source commit
`1c5edc17a7acd8701df6fc341c0d179f1c62c982`; the selected safetensors, configs,
and tokenizer blobs were checked against Violin's pinned source revision
`55cf4c4ebb4ebe31b2550e8bdf3bd21b99753851`. The workflow verifies each ONNX
and tokenizer/config SHA-256 before generating Violin's platform manifest, then
runs inference tests against the packaged artifacts before release publication.
The conversion is community maintained, so provenance and parity remain release
gates; Violin does not execute its exporter at install or runtime.

The runtime defaults to CPU. macOS may try the experimental CoreML execution
provider and fall back to CPU when unavailable. Other platforms use CPU. Builds
without CGO can still run Violin but cannot run upstream Laya inference; status
must report the fallback rather than claiming Laya is installed.

Do not substitute Qwen, llama.cpp, or another generative model for upstream
Laya AI. Use the pinned typed-decision and multilingual checkpoints. Explicit
non-English language tags select multilingual; otherwise Go routing uses
Unicode script and conservative Latin-language evidence. The implementation
must be checked against the pinned upstream reference on routing, sequence
construction, answers, probability calibration, and multilingual checkpoint
selection before it can be reported as upstream Laya or used by active policy.
Policy remains advisory until Violin's reviewed holdout activation gates pass.

## Consequences

- `laya_route`, `laya_review_risk`, and the other Violin MCP tools keep their
  existing names, schemas, and lifecycle behavior.
- Installation provisions the Go runtime and pinned local checkpoint artifacts;
  it must not install Python, `uv`, or Python packages. The first checkpoint
  download requires network access.
- The release artifact pipeline is part of shipping this decision: without
  published, revision-pinned ONNX bundles for each supported platform, install
  must fail clearly and report the classifier fallback. File hashes detect
  corruption relative to the downloaded manifest; bundle publication must be
  protected by the release process.
- The release workflow supports Apple Silicon macOS and Linux x64/ARM64. ONNX
  Runtime 1.29.0 does not publish a macOS x64 C API archive, so that platform is
  reported as unsupported rather than receiving an unverified substitute.
- The Go adapter translates Violin requests and typed Laya answers. It owns
  fallback reporting and distinguishes verified upstream-compatible results
  from the existing Go classifier fallback.
- The upstream `laya-mcp-server` extra is not required. Registering it beside
  Violin would duplicate MCP surfaces and split responsibility for routing and
  job policy.
- Replace the current Qwen/llama.cpp inference integration and update
  `violin install`, `violin model status`, and parity coverage before claiming
  upstream Laya AI is installed. Preserve the Go MCP and provider lifecycle.

## References

- https://github.com/NandhaKishorM/laya
- https://huggingface.co/convaiinnovations/laya
