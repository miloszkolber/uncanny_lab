.PHONY: test test-go test-python vet run

test: test-go test-python

test-go:
	go test ./...

test-python:
	PYTHONPATH=python python3 -m unittest discover -s python/tests -v

vet:
	go vet ./...

run:
	go run ./cmd/server --config config/config.dev.yaml
