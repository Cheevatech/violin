.PHONY: test go-test go-build vet verify eval release-check smoke tool-smoke

test:
	sh -n bin/qwen-mode bin/qwen-verify-gate bin/violin-codex-qwen eval/run-smoke-tests eval/run-tool-smoke-tests
	python3 -m unittest discover -s tests
	go test ./...

go-test:
	go test ./...

go-build:
	go build -o bin/violin ./cmd/violin

vet:
	python3 -m py_compile bin/violin_scheduler.py bin/violin-agent bin/violin-worker bin/violin-health bin/violin-qwen-metadata bin/violin-agent-server bin/install-violin-agents
	gofmt -l cmd internal
	go vet ./...

verify: test vet

eval: smoke tool-smoke

release-check: verify
	git diff --check
	git status --short

tool-smoke: test
	./eval/run-tool-smoke-tests

smoke: test
	./eval/run-smoke-tests
