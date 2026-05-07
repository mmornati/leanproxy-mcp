.PHONY: help
help: ## Show this help message
	@echo "LeanProxy-MCP Makefile"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: all
all: lint test build ## Run lint, test, and build

.PHONY: lint-install
lint-install: ## Install golangci-lint
	@echo "Installing golangci-lint $(GOLANGCI_VERSION)..."
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_VERSION)

.PHONY: lint
lint: lint-install ## Run linter
	@echo "Running lint..."
	@$(GOPATH)/bin/golangci-lint run ./... || (echo "Error: Run 'make lint-install' first" && exit 1)

.PHONY: tidy
tidy: ## Tidy go modules
	@echo "Tidying modules..."
	$(GO) mod tidy

.PHONY: test
test: ## Run tests
	@echo "Running tests..."
	$(GO) test -v -race -coverprofile=coverage.out ./...

.PHONY: test-e2e
test-e2e: ## Run E2E tests (requires built binary)
	@echo "Building binary for E2E tests..."
	$(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY_NAME) .
	@echo "Running E2E tests..."
	$(GO) test -v -timeout 10m ./tests/e2e/...

.PHONY: test-e2e-short
test-e2e-short: ## Run E2E tests (short mode, requires built binary)
	@echo "Building binary for E2E tests..."
	$(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BINARY_NAME) .
	@echo "Running E2E tests (short mode)..."
	$(GO) test -v -short -timeout 2m ./tests/e2e/...

.PHONY: test-all
test-all: lint test test-e2e ## Run lint, unit tests, and E2E tests

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format code
	$(GO) fmt ./...

.PHONY: mod
mod: ## Check module status
	$(GO) mod verify

.PHONY: deps
deps: ## Download dependencies
	$(GO) mod download

.PHONY: tag
tag: ## Tag with VERSION (VERSION=v1.0.0)
	@git tag -a $(VERSION) -m "Release $(VERSION)"
	@echo "Tagged: $(VERSION)"

.sbom-install:
	@which syft >/dev/null 2>&1 || $(GO) install github.com/anchore/syft/cmd/syft@latest
	@touch .sbom-install

.PHONY: sbom
sbom: .sbom-install build-local ## Generate SBOM
	@echo "Generating SBOM..."
	@syft packages -o cyclonedx-json=$(DIST_DIR)/sbom.json $(BINARY_NAME)
	@echo "SBOM generated: $(DIST_DIR)/sbom.json"

.PHONY: release
release: tag build-version ## Create a release: tag and build
	@echo "Release $(VERSION) created with binaries in $(DIST_DIR)/"

.PHONY: changelog
changelog: ## Generate changelog from git log
	@git log --oneline --pretty=format:"%h %s" $(shell git describe --tags --abbrev=0 2>/dev/null || echo HEAD)...HEAD

BINARY_NAME := leanproxy-mcp
DIST_DIR := dist
GO := go
GOLANGCI_VERSION := v1.62.0
GOPATH := $(shell go env GOPATH)

LATEST_TAG := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "")
VERSION ?= $(LATEST_TAG)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
LDFLAGS := -s -w -X github.com/mmornati/leanproxy-mcp/internal/version.Version=$(VERSION) -X github.com/mmornati/leanproxy-mcp/internal/version.Commit=$(COMMIT) -X github.com/mmornati/leanproxy-mcp/internal/version.BuildTime=$(BUILD_TIME)
