.PHONY: all build test clean lint install test-all

BINARY_NAME := leanproxy-mcp
VERSION ?= 0.1.0
DIST_DIR := dist
GO := go

all: test build

build:
	@mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 . 2>/dev/null || echo "Skipping darwin/amd64 (requires Linux for cross-compile)"
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 . 2>/dev/null || echo "Skipping darwin/arm64 (requires Linux for cross-compile)"
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe .

test:
	$(GO) test ./...

clean:
	rm -rf $(DIST_DIR)

lint:
	golangci-lint run ./...

build-local:
	@mkdir -p $(DIST_DIR)
	$(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME) .

install: build-local
	install -m 755 $(DIST_DIR)/$(BINARY_NAME) $(shell go env GOPATH)/bin/$(BINARY_NAME)

show-version:
	@echo "Building version: $(VERSION)"