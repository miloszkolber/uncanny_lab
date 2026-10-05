.PHONY: test test-go test-web test-python vet run

test: test-go test-web test-python

test-go:
	go test ./...

test-web:
	bun test web/navigation.test.mjs web/form-draft.test.mjs web/generation.test.mjs web/workflow-state.test.mjs scripts/ui-assets.test.mjs
	bun scripts/sync-mewa-ui.test.mjs

test-python:
	PYTHONPATH=python python3 -m unittest discover -s python/tests -v

vet:
	go vet ./...

run:
	go run ./cmd/server --config config/config.dev.yaml
