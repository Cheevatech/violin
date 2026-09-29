.PHONY: test go-test go-build npm-test vet verify eval release-check smoke tool-smoke

test:
	sh -n bin/qwen-mode bin/qwen-verify-gate eval/run-smoke-tests eval/run-tool-smoke-tests
	python3 -m unittest discover -s tests
	go test ./...

npm-test:
	npm test

go-test:
	go test ./...

go-build:
	go build -o bin/violin ./cmd/violin

vet:
	python3 -m py_compile compat/python/bin/violin_scheduler.py compat/python/bin/violin-agent compat/python/bin/violin-worker compat/python/bin/violin-health compat/python/bin/violin-agent-server compat/python/bin/install-violin-agents
	gofmt -l cmd internal
	go vet ./...

verify: test vet

eval: smoke tool-smoke

release-check: verify
	npm test
	git diff --check
	git status --short

tool-smoke: test
	./eval/run-tool-smoke-tests

smoke: test
	./eval/run-smoke-tests
