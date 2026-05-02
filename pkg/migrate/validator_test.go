package migrate

import (
	"testing"
)

func TestNewValidator(t *testing.T) {
	v := NewValidator()
	if v == nil {
		t.Fatal("NewValidator() returned nil")
	}
	if !v.checkExecutables {
		t.Error("NewValidator() should have checkExecutables=true by default")
	}
}

func TestNewValidatorWithoutExecutableCheck(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()
	if v == nil {
		t.Fatal("NewValidatorWithoutExecutableCheck() returned nil")
	}
	if v.checkExecutables {
		t.Error("NewValidatorWithoutExecutableCheck() should have checkExecutables=false")
	}
}

func TestValidator_ValidateServers_MissingCommand(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "github",
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "npx",
			},
		},
	}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() got errors = %v, want none", result.Errors)
	}
}

func TestValidator_ValidateServers_MissingExecutable(t *testing.T) {
	v := NewValidator()

	servers := []DiscoveredServer{
		{
			Name:      "github",
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "nonexistent_command_xyz123",
			},
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for missing executable")
	}
	if len(result.Errors) != 1 {
		t.Errorf("ValidateServers() got %d errors, want 1", len(result.Errors))
	}
	if result.Errors[0].ServerName != "github" {
		t.Errorf("ValidateServers() error server name = %s, want github", result.Errors[0].ServerName)
	}
}

func TestValidator_ValidateServers_InvalidTransport(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "myserver",
			Source:    "opencode",
			Transport: "ftp",
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for invalid transport")
	}
	if len(result.Errors) != 1 {
		t.Errorf("ValidateServers() got %d errors, want 1", len(result.Errors))
	}
	if result.Errors[0].ServerName != "myserver" {
		t.Errorf("ValidateServers() error server name = %s, want myserver", result.Errors[0].ServerName)
	}
}

func TestValidator_ValidateServers_MissingName(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "",
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "/usr/bin/test",
			},
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for missing name")
	}
}

func TestValidator_ValidateServers_MissingTransport(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "",
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for missing transport")
	}
}

func TestValidator_ValidateServers_MissingRequiredField_Stdio(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "stdio",
			Stdio:     &StdioConfig{},
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for missing stdio command")
	}
	if len(result.Errors) != 1 {
		t.Errorf("ValidateServers() got %d errors, want 1", len(result.Errors))
	}
	if result.Errors[0].Field != "command" {
		t.Errorf("ValidateServers() error field = %s, want command", result.Errors[0].Field)
	}
}

func TestValidator_ValidateServers_MissingHTTPConfig(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "http",
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for missing http config")
	}
}

func TestValidator_ValidateServers_MissingHTTPURL(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "http",
			HTTP:      &HTTPConfig{},
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for missing http url")
	}
}

func TestValidator_ValidateServers_InvalidURL(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "http",
			HTTP: &HTTPConfig{
				URL: "invalid-url",
			},
		},
	}

	result := v.ValidateServers(servers)
	if !result.HasErrors() {
		t.Error("ValidateServers() expected error for invalid URL")
	}
}

func TestValidator_ValidateServers_ValidHTTP(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "http",
			HTTP: &HTTPConfig{
				URL: "https://example.com/mcp",
			},
		},
	}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() got errors = %v, want none", result.Errors)
	}
}

func TestValidator_ValidateServers_ValidSSE(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "sse",
			HTTP: &HTTPConfig{
				URL: "https://example.com/sse",
			},
		},
	}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() got errors = %v, want none", result.Errors)
	}
}

func TestValidator_ValidateServers_ValidStdio(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "/bin/cat",
			},
		},
	}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() got errors = %v, want none", result.Errors)
	}
}

func TestValidator_ValidateServers_MultipleErrors(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{Name: "", Source: "opencode", Transport: "invalid"},
		{Name: "test", Source: "opencode", Transport: ""},
	}

	result := v.ValidateServers(servers)
	if result.ErrorCount() < 2 {
		t.Errorf("ValidateServers() got %d errors, want at least 2", result.ErrorCount())
	}
}

func TestValidator_ValidateServers_EmptyList(t *testing.T) {
	v := NewValidator()

	servers := []DiscoveredServer{}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() got errors = %v, want none", result.Errors)
	}
}

func TestValidationResult_HasErrors(t *testing.T) {
	result := &ValidationResult{Errors: []ValidationError{{Message: "test"}}}
	if !result.HasErrors() {
		t.Error("HasErrors() should return true when errors exist")
	}

	result = &ValidationResult{Errors: []ValidationError{}}
	if result.HasErrors() {
		t.Error("HasErrors() should return false when no errors")
	}
}

func TestValidationResult_HasWarnings(t *testing.T) {
	result := &ValidationResult{Warnings: []ValidationError{{Message: "test"}}}
	if !result.HasWarnings() {
		t.Error("HasWarnings() should return true when warnings exist")
	}

	result = &ValidationResult{Warnings: []ValidationError{}}
	if result.HasWarnings() {
		t.Error("HasWarnings() should return false when no warnings")
	}
}

func TestValidationResult_ErrorCount(t *testing.T) {
	result := &ValidationResult{
		Errors: []ValidationError{{Message: "test1"}, {Message: "test2"}},
	}
	if result.ErrorCount() != 2 {
		t.Errorf("ErrorCount() = %d, want 2", result.ErrorCount())
	}
}

func TestValidationResult_WarningCount(t *testing.T) {
	result := &ValidationResult{
		Warnings: []ValidationError{{Message: "test1"}, {Message: "test2"}, {Message: "test3"}},
	}
	if result.WarningCount() != 3 {
		t.Errorf("WarningCount() = %d, want 3", result.WarningCount())
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		ServerName: "github",
		Message:    "command 'npx' not found in PATH",
		Field:      "command",
	}
	expected := "Server 'github': command 'npx' not found in PATH"
	if err.Error() != expected {
		t.Errorf("Error() = %s, want %s", err.Error(), expected)
	}
}

func TestFormatValidationSummary(t *testing.T) {
	result := &ValidationResult{
		Errors:   []ValidationError{{ServerName: "srv1", Message: "error1"}},
		Warnings: []ValidationError{{ServerName: "srv2", Message: "warning1"}},
	}

	summary := FormatValidationSummary(5, result)
	if summary != "Imported 5 server(s), 1 warning(s), 1 error(s)" {
		t.Errorf("FormatValidationSummary() = %s, want 'Imported 5 server(s), 1 warning(s), 1 error(s)'", summary)
	}

	summary = FormatValidationSummary(3, nil)
	if summary != "Imported 3 server(s)" {
		t.Errorf("FormatValidationSummary() with nil = %s, want 'Imported 3 server(s)'", summary)
	}

	resultOnlyWarnings := &ValidationResult{
		Warnings: []ValidationError{{ServerName: "srv1", Message: "warning1"}},
	}
	summary = FormatValidationSummary(2, resultOnlyWarnings)
	if summary != "Imported 2 server(s), 1 warning(s)" {
		t.Errorf("FormatValidationSummary() = %s, want 'Imported 2 server(s), 1 warning(s)'", summary)
	}
}

func TestValidator_ValidateServers_ValidSSEWithCommand(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "sse-server",
			Source:    "generic",
			Transport: "sse",
			HTTP: &HTTPConfig{
				URL: "http://localhost:8080/sse",
			},
		},
	}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() got errors = %v, want none", result.Errors)
	}
}

func TestValidator_ValidateServers_CommandExistsInPath(t *testing.T) {
	v := NewValidator()

	servers := []DiscoveredServer{
		{
			Name:      "ls-server",
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "ls",
			},
		},
	}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() got errors = %v, want none (ls should be in PATH)", result.Errors)
	}
}

func TestValidator_ValidateServers_ValidStdioWithoutCheck(t *testing.T) {
	v := NewValidatorWithoutExecutableCheck()

	servers := []DiscoveredServer{
		{
			Name:      "custom-cmd",
			Source:    "generic",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "some-custom-command-that-may-not-exist",
			},
		},
	}

	result := v.ValidateServers(servers)
	if result.HasErrors() {
		t.Errorf("ValidateServers() without executable check got errors = %v, want none", result.Errors)
	}
}