.PHONY: test test-go test-web test-python vet run

test: test-go test-web test-python

test-go:
	go test ./...

test-web:
	node --test web/navigation.test.mjs

test-python:
	PYTHONPATH=python python3 -m unittest discover -s python/tests -v

vet:
	go vet ./...

run:
	go run ./cmd/server --config config/config.dev.yaml
