.PHONY: all build test clean lint install test-all release tag

BINARY_NAME := leanproxy-mcp
VERSION ?= 0.1.0
DIST_DIR := dist
GO := go

all: test build

tag:
	@if [ -z "$(VERSION)" ]; then \
		echo "ERROR: VERSION is required. Usage: make tag VERSION=0.1.0"; \
		exit 1; \
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
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 . 2>/dev/null || echo "Skipping darwin/amd64 (requires Linux for cross-compile)"
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=$(VERSION)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 . 2>/dev/null || echo "Skipping darwin/arm64 (requires Linux for cross-compile)"
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
	@echo "Building version: $(VERSION)"