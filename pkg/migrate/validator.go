package migrate

import (
	"fmt"
	"os"
	"strings"
)

type ValidationError struct {
	ServerName string
	Message    string
	Field      string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("Server '%s': %s", e.ServerName, e.Message)
}

type ValidationResult struct {
	Errors   []ValidationError
	Warnings []ValidationError
}

func (r *ValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

func (r *ValidationResult) HasWarnings() bool {
	return len(r.Warnings) > 0
}

func (r *ValidationResult) ErrorCount() int {
	return len(r.Errors)
}

func (r *ValidationResult) WarningCount() int {
	return len(r.Warnings)
}

type Validator struct {
	checkExecutables bool
}

func NewValidator() *Validator {
	return &Validator{
		checkExecutables: true,
	}
}

func NewValidatorWithoutExecutableCheck() *Validator {
	return &Validator{
		checkExecutables: false,
	}
}

func (v *Validator) ValidateServers(servers []DiscoveredServer) *ValidationResult {
	result := &ValidationResult{
		Errors:   make([]ValidationError, 0),
		Warnings: make([]ValidationError, 0),
	}

	for _, server := range servers {
		v.validateServer(server, result)
	}

	return result
}

func (v *Validator) validateServer(server DiscoveredServer, result *ValidationResult) {
	if server.Name == "" {
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    "name is required",
			Field:     "name",
		})
		return
	}

	if server.Transport == "" {
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    "transport type is required",
			Field:     "transport",
		})
		return
	}

	switch server.Transport {
	case "stdio":
		v.validateStdioServer(server, result)
	case "http", "sse":
		v.validateHTTPSTransport(server, result)
	default:
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    fmt.Sprintf("invalid transport '%s'. Must be stdio, http, or sse", server.Transport),
			Field:     "transport",
		})
	}
}

func (v *Validator) validateStdioServer(server DiscoveredServer, result *ValidationResult) {
	if server.Stdio == nil {
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    "stdio configuration is required for stdio transport",
			Field:     "stdio",
		})
		return
	}

	if server.Stdio.Command == "" {
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    "command is required for stdio transport",
			Field:     "command",
		})
		return
	}

	if v.checkExecutables {
		if !v.commandExists(server.Stdio.Command) {
			result.Errors = append(result.Errors, ValidationError{
				ServerName: server.Name,
				Message:    fmt.Sprintf("command '%s' not found in PATH", server.Stdio.Command),
				Field:     "command",
			})
		}
	}
}

func (v *Validator) validateHTTPSTransport(server DiscoveredServer, result *ValidationResult) {
	if server.HTTP == nil {
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    fmt.Sprintf("http configuration is required for %s transport", server.Transport),
			Field:     "http",
		})
		return
	}

	if server.HTTP.URL == "" {
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    "url is required for http/sse transport",
			Field:     "url",
		})
		return
	}

	if !isValidURL(server.HTTP.URL) {
		result.Errors = append(result.Errors, ValidationError{
			ServerName: server.Name,
			Message:    fmt.Sprintf("invalid URL format: '%s'", server.HTTP.URL),
			Field:     "url",
		})
	}
}

func (v *Validator) commandExists(cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}

	executable := parts[0]
	path := os.Getenv("PATH")
	paths := strings.Split(path, ":")

	for _, dir := range paths {
		fullPath := dir + "/" + executable
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			return true
		}
	}

	baseCmd := executable
	if idx := strings.LastIndex(baseCmd, "/"); idx >= 0 {
		baseCmd = baseCmd[idx+1:]
	}
	if _, err := os.Stat(baseCmd); err == nil {
		return true
	}

	return false
}

func isValidURL(url string) bool {
	if url == "" {
		return false
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return true
	}
	return false
}

func FormatValidationSummary(imported int, result *ValidationResult) string {
	if result == nil {
		return fmt.Sprintf("Imported %d server(s)", imported)
	}

	warningCount := result.WarningCount()
	errorCount := result.ErrorCount()

	summary := fmt.Sprintf("Imported %d server(s)", imported)

	if warningCount > 0 {
		summary += fmt.Sprintf(", %d warning(s)", warningCount)
	}

	if errorCount > 0 {
		summary += fmt.Sprintf(", %d error(s)", errorCount)
	}

	return summary
}