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

.PHONY: test-coverage
test-coverage: test ## Run tests with coverage report
	@echo "Generating coverage report..."
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: build
build: tidy ## Build all platform binaries to dist/
	@echo "Building for all platforms..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 .
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe .
	@echo "Builds available in $(DIST_DIR)/"

.PHONY: build-local
build-local: tidy ## Build for current platform only
	@echo "Building for $(shell go env GOOS)/$(shell go env GOARCH)..."
	@mkdir -p $(DIST_DIR)
	$(GO) build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME) .

.PHONY: build-version
build-version: tidy ## Build with custom version (VERSION=x.x.x)
	@echo "Building version $(VERSION)..."
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="-s -w -X main.version=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w -X main.version=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 .
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w -X main.version=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w -X main.version=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags="-s -w -X main.version=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe .
	@echo "Version $(VERSION) builds available in $(DIST_DIR)/"

.PHONY: clean
clean: ## Remove build artifacts
	@echo "Cleaning..."
	@rm -rf $(DIST_DIR)
	@rm -f coverage.out coverage.html

.PHONY: install
install: tidy ## Build and install to GOPATH/bin
	@echo "Installing to $(GOPATH)/bin..."
	$(GO) install -ldflags="-s -w" .

.PHONY: run
run: ## Run the application (ARGS='serve --help')
	@echo "Running..."
	$(GO) run . $(ARGS)

.PHONY: dev
dev: tidy ## Run with file watcher (requires entr)
	@echo "Watching for changes..."
	@find . -name "*.go" -not -path "./vendor/*" | entr -r $(GO) run .

.PHONY: test-all
test-all: lint test ## Run lint and all tests

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
DEFAULT_VERSION := 0.1.0
