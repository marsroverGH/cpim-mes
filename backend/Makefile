.PHONY: build test test-v cover run tidy download verify lint help

help:
	@echo "Targets:"
	@echo "  make tidy     - intentionally refresh go.mod/go.sum (dependency update only)"
	@echo "  make download - download locked modules"
	@echo "  make verify   - verify go.sum checksums"
	@echo "  make build    - compile the server"
	@echo "  make test     - run unit tests"
	@echo "  make test-v   - run unit tests with verbose output"
	@echo "  make cover    - run tests with coverage report"
	@echo "  make run      - go run the server"
	@echo "  make lint     - go vet"

tidy:
	go mod tidy

download:
	go mod download

verify:
	go mod verify

build:
	go build -mod=readonly -o bin/server ./cmd/server

test:
	go test -mod=readonly ./...

test-v:
	go test -mod=readonly -v ./...

cover:
	go test -mod=readonly -cover -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	@echo "詳細: go tool cover -html=coverage.out"

run:
	go run -mod=readonly ./cmd/server

lint:
	go vet -mod=readonly ./...
