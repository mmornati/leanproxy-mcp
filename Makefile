.PHONY: all build test clean lint install test-all release tag

BINARY_NAME := leanproxy-mcp
DIST_DIR := dist
GO := go

LATEST_TAG := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "")
VERSION ?= $(LATEST_TAG)
DEFAULT_VERSION := 0.1.0

all: test build

tag:
	@if [ -z "$(VERSION)" ]; then \
		echo "WARNING: No version specified and could not find any git tag."; \
		echo "Usage: make tag VERSION=x.y.z or ensure you have at least one git tag"; \
		echo "Falling back to default version $(DEFAULT_VERSION)"; \
		VERSION="$(DEFAULT_VERSION)"; \
	fi
	@echo "Tagging version $(VERSION)"
	sed -i '' "s/var versionString = \".*\"/var versionString = \"$(VERSION)\"/" cmd/version.go
	git add cmd/version.go
	git commit -m "chore: bump version to $(VERSION)"
	git tag $(VERSION)
	@echo "Version $(VERSION) committed and tagged. Push with: git push origin $(VERSION)"

release: tag
	@echo "Pushing tag $(VERSION) to trigger release workflow..."
	git push origin $(VERSION)

build:
	@mkdir -p $(DIST_DIR)
	@echo "Building version: $(VERSION)"
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 . 2>/dev/null || echo "Skipping darwin/amd64 (requires macOS for cross-compile)"
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 . 2>/dev/null || echo "Skipping darwin/arm64 (requires macOS for cross-compile)"
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .

test:
	$(GO) test ./...

clean:
	rm -rf $(DIST_DIR)

lint:
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