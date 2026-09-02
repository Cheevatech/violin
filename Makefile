.PHONY: test smoke

test:
	sh -n bin/qwen-mode bin/qwen-verify-gate eval/run-smoke-tests eval/run-tool-smoke-tests

tool-smoke: test
	./eval/run-tool-smoke-tests

smoke: test
	./eval/run-smoke-tests
