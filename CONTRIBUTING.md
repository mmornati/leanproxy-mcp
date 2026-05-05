# Contributing to LeanProxy-MCP

Thank you for considering contributing to LeanProxy-MCP! This document provides guidelines for contributing to the project.

## Reporting Bugs

If you find a bug, please open a GitHub issue with the following information:

- **Use GitHub Issues**: Go to [Issues](https://github.com/mmornati/leanproxy-mcp/issues) and create a new issue.
- **Include reproduction steps**: Provide clear steps to reproduce the issue.
- **Include environment details**: Include your Go version, operating system, and any relevant configuration.
- **Include relevant logs**: Add any error messages or logs that help diagnose the issue.

## Pull Requests

We welcome contributions via Pull Requests. Here's how to submit a PR:

1. **Fork the repository**: Click the "Fork" button on GitHub.
2. **Create a feature branch**: Create a new branch for your feature or fix:
   ```bash
   git checkout -b feature/your-feature-name
   ```
3. **Follow code style**:
   - Run `go fmt` to format your code
   - Run `go vet` to check for issues
4. **Include tests**: Add tests for new functionality. We use `testify/assert` for assertions.
5. **Update documentation**: If your change adds or modifies features, update the relevant documentation.
6. **Run tests before submitting**:
   ```bash
   make test
   ```
7. **Run linter before submitting**:
   ```bash
   make lint
   ```

## Development Setup

### Prerequisites

- **Go 1.25 or later**: Download from [golang.org](https://golang.org/dl/)
- **Git**: For version control

### Initial Setup

```bash
# Clone the repository
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp

# Install dependencies
go mod download
```

### Available Commands

| Command | Description |
|---------|-------------|
| `make test` | Run all tests with race detection |
| `make build` | Build all platform binaries to dist/ |
| `make build-local` | Build for current platform only |
| `make lint` | Run linter (golangci-lint) |
| `make install` | Build and install to $GOPATH/bin |
| `make vet` | Run go vet |
| `make fmt` | Format code with go fmt |

### Running Tests

```bash
# Run all tests
go test ./...

# Run tests with verbose output and race detection
go test -v -race ./...

# Run tests with coverage
make test
```

### Building

```bash
# Build for current platform
go build -o leanproxy-mcp .

# Or use make
make build-local
```

## Code Style

We follow Go best practices:

- **Format code**: Always run `go fmt` before committing
- **Check for issues**: Run `go vet` to catch common mistakes
- **Test with race detector**: Run `go test -race` to detect race conditions

## Commit Message Conventions

When writing commit messages, follow these guidelines:

- **Use present tense**: "Add feature" not "Added feature"
- **Keep it concise**: First line should be under 72 characters
- **Reference issues**: Include issue numbers (e.g., "Fix #123")
- **Describe what and why**: Explain the change, not the implementation details

Example:
```
Add token savings calculator

Implement the token savings calculator to estimate potential
cost reductions when using LeanProxy vs native MCP.

Fixes #45
```

## Testing Patterns

We use table-driven tests where appropriate. Example:

```go
func TestExample(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"case 1", "input1", "expected1"},
        {"case 2", "input2", "expected2"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := process(tt.input)
            assert.Equal(t, tt.expected, result)
        })
    }
}
```

Remember to test both success and error paths.

## Project Structure

```
leanproxy-mcp/
├── cmd/              # CLI commands
├── pkg/              # Core packages
│   ├── bouncer/      # Redaction engine
│   ├── gateway/      # Gateway logic
│   ├── mcp/          # MCP protocol handling
│   ├── pool/         # Connection pooling
│   ├── proxy/        # Proxy server
│   ├── registry/     # Server registry
│   └── ...
├── docs/             # Documentation
├── Makefile          # Build automation
└── go.mod            # Go module definition
```

## Getting Help

- **GitHub Issues**: For bug reports and feature requests
- **Documentation**: See the [docs/](docs/) directory for detailed guides

## License

By contributing to LeanProxy-MCP, you agree that your contributions will be licensed under the MIT License.