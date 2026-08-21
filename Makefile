# skills-mapper (sm)
BIN := sm
PKG := ./cmd/sm

# Print the target list when make is invoked with no target.
.DEFAULT_GOAL := help

## help: list available targets
help:
	@echo "skills-mapper make targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'

## build: compile the sm binary
build:
	go build -o $(BIN) $(PKG)

## test: run the full test suite
test: run-tests

## run-tests: run the full test suite
run-tests:
	go test ./...

## lint: gofmt check and go vet
lint:
	gofmt -l . && go vet ./...

## clean: remove build artifacts
clean:
	rm -f $(BIN)
	go clean

.PHONY: help build test run-tests lint clean
