.PHONY: test smoke

test:
	sh -n bin/qwen-mode bin/qwen-verify-gate eval/run-smoke-tests

smoke: test
	./eval/run-smoke-tests
