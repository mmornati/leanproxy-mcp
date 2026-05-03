.PHONY: all build test clean lint lint-install install test-all release tag

BINARY_NAME := leanproxy-mcp
DIST_DIR := dist
GO := go
GOLANGCI_VERSION := v1.62.0

LATEST_TAG := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "")
VERSION ?= $(LATEST_TAG)
DEFAULT_VERSION := 0.1.0

all: test build

lint-install:
	@echo "Installing golangci-lint $(GOLANGCI_VERSION)..."
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_VERSION)

lint: lint-install
	@echo "Running lint..."
	golangci-lint run ./...

build-local:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 . 2>/dev/null || echo "Skipping darwin/amd64 (requires macOS for cross-compile)"
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 . 2>/dev/null || echo "Skipping darwin/arm64 (requires macOS for cross-compile)"
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .

install: build-local
	install -m 755 $(DIST_DIR)/$(BINARY_NAME) $(shell go env GOPATH)/bin/$(BINARY_NAME)

show-version:
	@echo "Latest git tag: $(LATEST_TAG)"
	@echo "Version to build: $(VERSION)"
	@if [ -z "$(LATEST_TAG)" ]; then \
		echo "(using default: $(DEFAULT_VERSION))"; \
	fi