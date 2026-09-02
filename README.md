# Qwen Global Workflow

Global launchers for running the self-hosted Qwen profile through the Codex
harness.

## Commands

```bash
violin-qwen-plan -C /path/to/repo "Design the implementation"
violin-qwen-implement -C /path/to/repo "Implement the change"
violin-qwen-review -C /path/to/repo "Review the current diff"
violin-qwen-verify -C /path/to/repo "Run the relevant checks"
```

Plan, review, and verify use a read-only sandbox. Implement uses
`workspace-write`.

## Verification gate

```bash
./bin/qwen-verify-gate --repo /path/to/repo -- npm test
```

## Development

```bash
make test
./eval/run-smoke-tests
./eval/run-tool-smoke-tests
```

Qwen uses `/Users/film/bin/serena-bridge` for semantic navigation because the
self-hosted Responses route currently cannot dispatch native Serena MCP calls.
Native Serena remains enabled for the regular GPT/Codex profile.
