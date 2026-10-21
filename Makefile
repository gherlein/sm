# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

VULN_INSTALL=install golang.org/x/vuln/cmd/govulncheck@latest

BINDIR=bin
SM_BIN=sm
SM_PKG=./cmd/sm
MS_BIN=ms
MS_PKG=./cmd/ms

# Print the target list when make is invoked with no target.
.DEFAULT_GOAL := help

all: test build

build:
	mkdir -p $(BINDIR)
	$(GOBUILD) -o $(BINDIR)/$(SM_BIN) $(SM_PKG)
	$(GOBUILD) -o $(BINDIR)/$(MS_BIN) $(MS_PKG)

test: run-tests

run-tests:
	$(GOTEST) -v ./...

fmt:
	$(GOCMD) fmt ./...

vet:
	$(GOVET) ./...

# gofmt -l exits 0 even when it lists files, so the listing must be tested explicitly.
lint:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: unformatted files:"; echo "$$unformatted"; exit 1; \
	fi
	$(GOVET) ./...

# Kept out of all/test/build because it needs the network and an installed tool.
vuln:
	@if command -v govulncheck >/dev/null 2>&1; then \
		govulncheck ./...; \
	else \
		echo "govulncheck not installed; install with:"; \
		echo "  $(GOCMD) $(VULN_INSTALL)"; \
		exit 1; \
	fi

deps:
	$(GOMOD) download
	$(GOMOD) tidy

INSTALLDIR=$(HOME)/.local/bin

install:
	GOBIN=$(INSTALLDIR) $(GOCMD) install $(SM_PKG) $(MS_PKG)

clean:
	$(GOCLEAN)
	rm -rf $(BINDIR)

help:
	@echo "skills-mapper (sm, ms) make targets:"
	@echo "  make build       - Build the sm and ms binaries into $(BINDIR)/"
	@echo "  make test        - Run all tests (alias for run-tests)"
	@echo "  make run-tests   - Run all tests"
	@echo "  make fmt         - Format code (go fmt)"
	@echo "  make vet         - Run go vet"
	@echo "  make lint        - Fail on unformatted files (gofmt -l), then go vet"
	@echo "  make vuln        - Run govulncheck (must be installed; not part of all/test)"
	@echo "  make deps        - Download and tidy dependencies"
	@echo "  make install     - Install sm and ms to ~/.local/bin"
	@echo "  make clean       - Remove build artifacts"
	@echo "  make all         - Run tests then build"
	@echo "  make help        - Show this help message"

.PHONY: all build test run-tests fmt vet lint vuln deps install clean help
