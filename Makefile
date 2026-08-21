# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

BIN=sm
PKG=./cmd/sm

# Print the target list when make is invoked with no target.
.DEFAULT_GOAL := help

all: test build

build:
	$(GOBUILD) -o $(BIN) $(PKG)

test: run-tests

run-tests:
	$(GOTEST) -v ./...

fmt:
	$(GOCMD) fmt ./...

vet:
	$(GOVET) ./...

lint:
	gofmt -l . && $(GOVET) ./...

deps:
	$(GOMOD) download
	$(GOMOD) tidy

install:
	$(GOCMD) install $(PKG)

clean:
	$(GOCLEAN)
	rm -f $(BIN)

help:
	@echo "skills-mapper (sm) make targets:"
	@echo "  make build       - Build the sm binary"
	@echo "  make test        - Run all tests (alias for run-tests)"
	@echo "  make run-tests   - Run all tests"
	@echo "  make fmt         - Format code (go fmt)"
	@echo "  make vet         - Run go vet"
	@echo "  make lint        - gofmt check + go vet"
	@echo "  make deps        - Download and tidy dependencies"
	@echo "  make install     - Install sm to GOPATH/bin"
	@echo "  make clean       - Remove build artifacts"
	@echo "  make all         - Run tests then build"
	@echo "  make help        - Show this help message"

.PHONY: all build test run-tests fmt vet lint deps install clean help
