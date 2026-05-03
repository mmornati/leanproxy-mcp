.PHONY: all build test clean lint lint-install install test-all release tag

BINARY_NAME := leanproxy-mcp
DIST_DIR := dist
GO := go
GOLANGCI_VERSION := v1.62.0
GOPATH := $(shell go env GOPATH)

LATEST_TAG := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "")
VERSION ?= $(LATEST_TAG)
DEFAULT_VERSION := 0.1.0

all: lint test build

lint-install:
	@echo "Installing golangci-lint $(GOLANGCI_VERSION)..."
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_VERSION)

lint: lint-install
	@echo "Running lint..."
	@$(GOPATH)/bin/golangci-lint run ./... || (echo "Error: Run 'make lint-install' first" && exit 1)