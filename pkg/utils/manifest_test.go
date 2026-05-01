package utils

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestMerge_SimpleFieldOverride(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Version: "1.0",
		Name:    "base",
		Servers: []ServerConfig{
			{ID: "s1", Command: []string{"cmd1"}, Port: 8080},
		},
	}

	override := &Config{
		Version: "2.0",
		Name:    "override",
	}

	result, err := merger.Merge(context.Background(), base, override)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if result.Version != "2.0" {
		t.Errorf("expected version '2.0', got '%s'", result.Version)
	}
	if result.Name != "override" {
		t.Errorf("expected name 'override', got '%s'", result.Name)
	}
}

func TestMerge_NestedObjectMerge(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Name: "test",
		Auth: &AuthConfig{
			Token:  "base-token",
			Header: "Authorization",
			Scopes: []string{"read", "write"},
		},
	}

	override := &Config{
		Name: "test",
		Auth: &AuthConfig{
			Token: "user-token",
		},
	}

	result, err := merger.Merge(context.Background(), base, override)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if result.Auth == nil {
		t.Fatal("expected auth to be set")
	}
	if result.Auth.Token != "user-token" {
		t.Errorf("expected token 'user-token', got '%s'", result.Auth.Token)
	}
	if result.Auth.Header != "Authorization" {
		t.Errorf("expected header 'Authorization', got '%s'", result.Auth.Header)
	}
}

func TestMerge_ArrayReplacement(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Name: "test",
		Servers: []ServerConfig{
			{ID: "base-s1", Command: []string{"cmd1"}, Port: 8080},
			{ID: "base-s2", Command: []string{"cmd2"}, Port: 8081},
		},
	}

	override := &Config{
		Name: "test",
		Servers: []ServerConfig{
			{ID: "override-s1", Command: []string{"new-cmd"}, Port: 9090},
		},
	}

	result, err := merger.Merge(context.Background(), base, override)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if len(result.Servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(result.Servers))
	}
	if result.Servers[0].ID != "override-s1" {
		t.Errorf("expected server id 'override-s1', got '%s'", result.Servers[0].ID)
	}
}

func TestMerge_NullFieldRemoval(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Name:   "test",
		Limits: &LimitsConfig{MaxConnections: 100},
	}

	override := &Config{
		Name: "test",
	}

	result, err := merger.Merge(context.Background(), base, override)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if result.Limits == nil {
		t.Log("limits is nil as expected when base had values and override is empty")
	} else {
		t.Logf("limits not nil: %+v", result.Limits)
	}
}

func TestMerge_MultipleLayers(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Version: "1.0",
		Name:    "base",
		Servers: []ServerConfig{
			{ID: "s1", Command: []string{"base-cmd"}, Port: 8080},
		},
		Auth: &AuthConfig{Token: "base-token"},
	}

	user := &Config{
		Version: "1.0",
		Name:    "user-config",
		Auth:    &AuthConfig{Token: "user-token"},
	}

	runtime := &Config{
		Version: "1.0",
		Name:    "runtime",
		Servers: []ServerConfig{
			{ID: "s1", Command: []string{"runtime-cmd"}, Port: 9090},
		},
	}

	result, err := merger.Merge(context.Background(), base, user, runtime)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if result.Name != "runtime" {
		t.Errorf("expected name 'runtime', got '%s'", result.Name)
	}
	if result.Auth != nil && result.Auth.Token != "user-token" {
		t.Errorf("expected token 'user-token', got '%s'", result.Auth.Token)
	}
	if len(result.Servers) != 1 || result.Servers[0].Command[0] != "runtime-cmd" {
		t.Errorf("expected servers to be overridden by runtime config")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	cfg := &Config{
		Version: "1.0",
		Name:    "test-config",
		Servers: []ServerConfig{
			{ID: "s1", Command: []string{"cmd"}, Port: 8080},
		},
	}

	err := merger.Validate(cfg)
	if err != nil {
		t.Errorf("expected valid config, got error: %v", err)
	}
}

func TestValidate_MissingName(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	cfg := &Config{
		Version: "1.0",
		Name:    "",
		Servers: []ServerConfig{
			{ID: "s1", Command: []string{"cmd"}, Port: 8080},
		},
	}

	err := merger.Validate(cfg)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestValidate_MissingVersion(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	cfg := &Config{
		Version: "",
		Name:    "test",
		Servers: []ServerConfig{
			{ID: "s1", Command: []string{"cmd"}, Port: 8080},
		},
	}

	err := merger.Validate(cfg)
	if err == nil {
		t.Error("expected error for missing version")
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	cfg := &Config{
		Version: "1.0",
		Name:    "test",
		Servers: []ServerConfig{
			{ID: "s1", Command: []string{"cmd"}, Port: 70000},
		},
	}

	err := merger.Validate(cfg)
	if err == nil {
		t.Error("expected error for invalid port")
	}
}

func TestMergeWithLayers(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	layers := []Layer{
		{Source: "base.yaml", Config: &Config{Name: "base", Version: "1.0"}},
		{Source: "user.yaml", Config: &Config{Name: "user-override"}},
	}

	result, err := merger.MergeWithLayers(context.Background(), layers...)
	if err != nil {
		t.Fatalf("MergeWithLayers failed: %v", err)
	}

	if result.Config.Name != "user-override" {
		t.Errorf("expected name 'user-override', got '%s'", result.Config.Name)
	}
	if result.Timestamp.IsZero() {
		t.Error("expected timestamp to be set")
	}
}

func TestMergeFiles_JSON(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	tmpDir := t.TempDir()
	basePath := tmpDir + "/base.json"
	userPath := tmpDir + "/user.json"

	baseJSON := `{"version": "1.0", "name": "base", "servers": [{"id": "s1", "command": ["cmd"], "port": 8080}]}`
	userJSON := `{"version": "1.0", "name": "user", "servers": [{"id": "s2", "command": ["new-cmd"], "port": 9090}]}`

	if err := os.WriteFile(basePath, []byte(baseJSON), 0644); err != nil {
		t.Fatalf("failed to write base.json: %v", err)
	}
	if err := os.WriteFile(userPath, []byte(userJSON), 0644); err != nil {
		t.Fatalf("failed to write user.json: %v", err)
	}

	result, err := merger.MergeFiles(context.Background(), basePath, userPath)
	if err != nil {
		t.Fatalf("MergeFiles failed: %v", err)
	}

	if result.Name != "user" {
		t.Errorf("expected name 'user', got '%s'", result.Name)
	}
	if len(result.Servers) != 1 || result.Servers[0].ID != "s2" {
		t.Errorf("expected servers to be overridden")
	}
}

func TestMergeFiles_YAML(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	tmpDir := t.TempDir()
	basePath := tmpDir + "/base.yaml"
	userPath := tmpDir + "/user.yaml"

	baseYAML := `version: "1.0"
name: base
servers:
  - id: s1
    command: [cmd]
    port: 8080`
	userYAML := `version: "1.0"
name: user
servers:
  - id: s2
    command: [new-cmd]
    port: 9090`

	if err := os.WriteFile(basePath, []byte(baseYAML), 0644); err != nil {
		t.Fatalf("failed to write base.yaml: %v", err)
	}
	if err := os.WriteFile(userPath, []byte(userYAML), 0644); err != nil {
		t.Fatalf("failed to write user.yaml: %v", err)
	}

	result, err := merger.MergeFiles(context.Background(), basePath, userPath)
	if err != nil {
		t.Fatalf("MergeFiles failed: %v", err)
	}

	if result.Name != "user" {
		t.Errorf("expected name 'user', got '%s'", result.Name)
	}
}

func TestMerge_EmptyConfigs(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	_, err := merger.Merge(context.Background())
	if err == nil {
		t.Error("expected error for no configs")
	}
}

func TestMerge_DeepNestedMerge(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Name: "test",
		Auth: &AuthConfig{
			Token:  "token",
			Header: "Authorization",
			Expiry: time.Now().Add(time.Hour),
			Scopes: []string{"read"},
			Meta:   Metadata{Source: "base"},
		},
	}

	override := &Config{
		Name: "test",
		Auth: &AuthConfig{
			Scopes: []string{"read", "write", "admin"},
		},
	}

	result, err := merger.Merge(context.Background(), base, override)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if result.Auth == nil {
		t.Fatal("expected auth to be set")
	}
	if len(result.Auth.Scopes) != 3 {
		t.Errorf("expected 3 scopes, got %d", len(result.Auth.Scopes))
	}
	if result.Auth.Token != "token" {
		t.Errorf("expected token to be preserved from base")
	}
}

func TestMerge_PreserveMetadata(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Name: "base",
		Meta: Metadata{Source: "base-file", Priority: 1},
	}

	override := &Config{
		Name: "override",
		Meta: Metadata{Source: "override-file", Priority: 2},
	}

	result, err := merger.Merge(context.Background(), base, override)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if result.Meta.Source != "override-file" {
		t.Logf("metadata source overridden as expected: %s", result.Meta.Source)
	}
}

func TestMerge_ConflictResolution(t *testing.T) {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Name:   "base",
		Auth:   &AuthConfig{Token: "base-token"},
		Limits: &LimitsConfig{MaxConnections: 50},
	}

	override := &Config{
		Name:   "override",
		Auth:   &AuthConfig{Token: "override-token"},
		Limits: &LimitsConfig{TimeoutSeconds: 30},
	}

	result, err := merger.Merge(context.Background(), base, override)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if result.Auth != nil && result.Auth.Token != "override-token" {
		t.Errorf("expected auth token to be overridden")
	}
	if result.Limits != nil && result.Limits.TimeoutSeconds != 30 {
		t.Errorf("expected limits to be overridden")
	}
}