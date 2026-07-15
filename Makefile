.PHONY: format lint test test-race test-e2e build

format:
	go fmt ./...
	npm --prefix web run format

lint:
	go vet ./...
	npm --prefix web run lint
	npm --prefix web run format:check

test:
	go test ./...
	npm --prefix web test

test-race:
	go test -race ./...
	npm --prefix web test

test-e2e:
	npm --prefix web run test:e2e

build:
	npm --prefix web run build
	go build -o bin/0xbin ./cmd/0xbin
