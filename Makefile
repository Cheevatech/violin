.PHONY: test vet verify eval release-check smoke tool-smoke

test:
	sh -n bin/qwen-mode bin/qwen-verify-gate bin/violin-codex-qwen eval/run-smoke-tests eval/run-tool-smoke-tests
	python3 -m unittest discover -s tests

vet:
	python3 -m py_compile bin/violin_scheduler.py bin/violin-agent bin/violin-worker bin/violin-health bin/violin-qwen-metadata bin/violin-agent-server bin/install-violin-agents

verify: test vet

eval: smoke tool-smoke

release-check: verify
	git diff --check
	git status --short

tool-smoke: test
	./eval/run-tool-smoke-tests

smoke: test
	./eval/run-smoke-tests
