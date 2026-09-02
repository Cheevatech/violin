# Coding-Agent Tooling Research

Research date: 2026-09-02

## Recommendation

Do not install every agent or MCP server. Keep Codex as the harness, use GPT
for architecture/review, and use Qwen for bounded implementation. Add tools
that improve context quality and verification around that loop.

## Highest-value additions

### RTK (Rust Token Killer) — install first

RTK compresses noisy shell output before it reaches the model and documents a
Codex integration through `AGENTS.md`/`RTK.md`. It is a small Rust binary and
is useful for both GPT and Qwen, especially on `git`, test, build, and log
commands.

Source: [rtk](https://github.com/rtk-ai/rtk)

Install after reviewing the dry run:

```bash
rtk init --global --codex --dry-run
rtk init --global --codex
```

Do not assume hooks are installed; verify that the Codex/Qwen launcher actually
uses the generated instructions.

### Serena — consider for large codebases

Serena exposes language-server-based semantic retrieval and editing through
MCP. It can be more precise than text-only search when Qwen must find symbols,
references, and call sites across a large repository.

Source: [Serena MCP](https://github.com/sjstheesar/serena-mcp)

Use it first with GPT/Codex for exploration. Expose only read/search tools to
Qwen until the existing edit path is proven stable.

### ast-grep — install for mechanical refactors

ast-grep performs syntax-aware search, linting, and rewriting across many
languages. It is safer than regex for repeated API migrations and codemods.

Source: [ast-grep](https://ast-grep.github.io/)

Use it as a deterministic tool invoked by the agent, not as an always-enabled
MCP server.

### Semgrep Community Edition — add to verification

Semgrep provides pattern-based static analysis for bugs, security guardrails,
and project-specific coding rules. Run it after Qwen edits and in CI; do not
ask Qwen to be the only security checker.

Source: [Semgrep](https://github.com/semgrep/semgrep)

### OpenTelemetry — add to llmux observability

OpenTelemetry is a vendor-neutral framework for traces, metrics, and logs. Add
request, deployment, tool-call, retry, cooldown, and latency attributes to
llmux before building a dashboard.

Source: [OpenTelemetry documentation](https://opentelemetry.io/docs/)

## Conditional tools

### Archify

Use with GPT/Codex during plan and review to generate an architecture snapshot
and before/after delta. It is context/visualization support, not a coding
harness or tool-calling fix.

Source: [Archify](https://github.com/tt-a1i/archify)

### Ponytail

Use as a policy layer for GPT planning/review and optionally for Qwen after an
A/B test. It encourages YAGNI, reuse, standard-library-first solutions, and
minimal changes. Its published savings numbers are project-reported, not an
independent guarantee.

Source: [Ponytail](https://github.com/DietrichGebert/ponytail)

### Continue

Useful when the primary interface is VS Code or when source-controlled checks
should run on pull requests. It supports custom model capabilities and MCP,
but it would duplicate the Codex harness for terminal workflows.

Source: [Continue](https://github.com/continuedev/continue)

### OpenHands / OpenCode / Aider

Keep as A/B-test candidates, not simultaneous production installs:

- OpenHands for autonomous long-running tasks and custom agent workflows.
- OpenCode for a model-agnostic terminal harness.
- Aider for controlled, diff-oriented edits and architect/editor separation.

These do not automatically outperform the current Codex + llmux setup; compare
them with the same evaluation cases.

Sources: [OpenHands SDK](https://docs.openhands.dev/sdk/index),
[OpenCode providers](https://dev.opencode.ai/docs/providers),
[Aider modes](https://aider.chat/docs/usage/modes.html)

## MCP guidance

Use MCP only for external capabilities or semantic context. The official MCP
reference servers include filesystem, git, memory, fetch, and sequential
thinking, but they are reference implementations and require threat-model
review before production use.

Source: [MCP reference servers](https://github.com/modelcontextprotocol/servers)

For this stack, start with read-only semantic search or browser/docs access.
Do not replace Codex's native `exec`, file editing, and verification path with
MCP wrappers unless a concrete limitation requires it.

## Suggested rollout

1. Install and verify RTK with Codex and Qwen.
2. Add llmux telemetry and fix upstream cooldown/connection failures.
3. Add Semgrep and ast-grep to the verification/codemod path.
4. Test Serena on one large repository.
5. Add Archify and Ponytail to GPT plan/review.
6. Benchmark OpenHands/OpenCode only if Codex remains insufficient.

## Decision rule

Keep a tool only if it improves a measured metric: task success, wrong-file
edits, test pass rate, latency, context size, or review defects. Remove tools
that only add prompt length or duplicate existing Codex capabilities.
