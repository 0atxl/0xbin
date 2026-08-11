.PHONY: format lint generate-live-fixtures test test-race test-e2e build

format:
	go fmt ./...
	npm --prefix web run format

lint:
	go vet ./...
	npm --prefix web run lint
	npm --prefix web run format:check

generate-live-fixtures:
	npm --prefix web run generate:live-fixtures
	git diff --exit-code -- tests/livecollab/fixtures.json

test: generate-live-fixtures
	go test ./...
	npm --prefix web test

test-race: generate-live-fixtures
	go test -race ./...
	npm --prefix web test

test-e2e:
	npm --prefix web run test:e2e

build:
	npm --prefix web run build
	go build -o bin/0xbin ./cmd/0xbin
