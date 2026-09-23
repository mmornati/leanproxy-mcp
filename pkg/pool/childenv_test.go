package pool

import (
	"log/slog"
	"sort"
	"strings"
	"testing"
)

func envNames(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		m[name] = value
	}
	return m
}

// TestBuildChildEnvDefaultDropsSecrets verifies that, by default (#311), an
// arbitrary variable in the parent environment (e.g. a secret) is NOT
// passed to the child, while PATH and HOME always are.
func TestBuildChildEnvDefaultDropsSecrets(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/op",
		"FOO_SECRET=super-secret-value",
	}
	cfg := StdioServerConfig{Name: "test"}

	env, err := buildChildEnv(parent, cfg, slog.Default())
	if err != nil {
		t.Fatalf("buildChildEnv: %v", err)
	}
	m := envNames(env)

	if _, ok := m["FOO_SECRET"]; ok {
		t.Error("FOO_SECRET leaked into child env by default")
	}
	if m["PATH"] != "/usr/bin" {
		t.Errorf("PATH not passed through, got %v", m)
	}
	if m["HOME"] != "/home/op" {
		t.Errorf("HOME not passed through, got %v", m)
	}
	if m["PYTHONUNBUFFERED"] != "1" {
		t.Error("PYTHONUNBUFFERED=1 not set")
	}
}

// TestBuildChildEnvPassthrough verifies env_passthrough copies a named
// parent variable as-is.
func TestBuildChildEnvPassthrough(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "FOO_SECRET=x"}
	cfg := StdioServerConfig{Name: "test", EnvPassthrough: []string{"FOO_SECRET"}}

	env, err := buildChildEnv(parent, cfg, slog.Default())
	if err != nil {
		t.Fatalf("buildChildEnv: %v", err)
	}
	m := envNames(env)
	if m["FOO_SECRET"] != "x" {
		t.Errorf("FOO_SECRET not passed through via env_passthrough, got %v", m)
	}
}

// TestBuildChildEnvExplicitExpansion verifies "${VAR}" expansion in an
// explicit env entry, from the parent environment.
func TestBuildChildEnvExplicitExpansion(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "FOO_SECRET=shh"}
	cfg := StdioServerConfig{Name: "test", Env: []string{"BAR=${FOO_SECRET}"}}

	env, err := buildChildEnv(parent, cfg, slog.Default())
	if err != nil {
		t.Fatalf("buildChildEnv: %v", err)
	}
	m := envNames(env)
	if m["BAR"] != "shh" {
		t.Errorf("BAR not expanded, got %v", m)
	}
	// FOO_SECRET itself must still not leak just because it was referenced.
	if _, ok := m["FOO_SECRET"]; ok {
		t.Error("FOO_SECRET leaked into child env merely by being referenced")
	}
}

// TestBuildChildEnvExplicitMissingVar verifies a clear, named error when an
// explicit env value references an unset parent variable.
func TestBuildChildEnvExplicitMissingVar(t *testing.T) {
	parent := []string{"PATH=/usr/bin"}
	cfg := StdioServerConfig{Name: "myserver", Env: []string{"BAR=${MISSING_VAR}"}}

	_, err := buildChildEnv(parent, cfg, slog.Default())
	if err == nil {
		t.Fatal("expected an error for a missing referenced variable")
	}
	if !strings.Contains(err.Error(), "myserver") || !strings.Contains(err.Error(), "MISSING_VAR") {
		t.Errorf("error does not name the server and variable: %v", err)
	}
}

// TestBuildChildEnvInheritTrue verifies inherit_env: true restores full
// inheritance of the parent environment.
func TestBuildChildEnvInheritTrue(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "FOO_SECRET=x", "SOME_OTHER=y"}
	cfg := StdioServerConfig{Name: "test", InheritEnv: true}

	env, err := buildChildEnv(parent, cfg, slog.Default())
	if err != nil {
		t.Fatalf("buildChildEnv: %v", err)
	}
	m := envNames(env)
	if m["FOO_SECRET"] != "x" {
		t.Errorf("FOO_SECRET should be inherited with inherit_env: true, got %v", m)
	}
	if m["SOME_OTHER"] != "y" {
		t.Errorf("SOME_OTHER should be inherited with inherit_env: true, got %v", m)
	}
}

// TestBuildChildEnvAlwaysHasPathAndHome is the unit-test acceptance
// criterion: PATH and HOME are always present, even without any config,
// so npx/uvx-based servers can still start.
func TestBuildChildEnvAlwaysHasPathAndHome(t *testing.T) {
	parent := []string{"PATH=/usr/bin:/bin", "HOME=/home/op", "IRRELEVANT=1"}
	cfg := StdioServerConfig{Name: "test"}

	env, err := buildChildEnv(parent, cfg, nil)
	if err != nil {
		t.Fatalf("buildChildEnv: %v", err)
	}
	m := envNames(env)
	if m["PATH"] == "" {
		t.Error("PATH missing from child env")
	}
	if m["HOME"] == "" {
		t.Error("HOME missing from child env")
	}
}

// TestBuildChildEnvNilLoggerSafe verifies buildChildEnv tolerates a nil
// logger (used by doctor's dry-run report).
func TestBuildChildEnvNilLoggerSafe(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "GITHUB_TOKEN=x"}
	cfg := StdioServerConfig{Name: "github", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}}

	if _, err := buildChildEnv(parent, cfg, nil); err != nil {
		t.Fatalf("buildChildEnv with nil logger: %v", err)
	}
}

// TestChildEnvReportNamesOnly verifies the doctor-facing report returns
// sorted name lists and never values, and that a var present in the parent
// but not passed shows up as dropped.
func TestChildEnvReportNamesOnly(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "FOO_SECRET=super-secret"}
	cfg := StdioServerConfig{Name: "test"}

	passed, dropped, err := ChildEnvReport(parent, cfg)
	if err != nil {
		t.Fatalf("ChildEnvReport: %v", err)
	}
	if !sort.StringsAreSorted(passed) || !sort.StringsAreSorted(dropped) {
		t.Error("passed/dropped must be sorted")
	}
	foundPath := false
	for _, n := range passed {
		if n == "PATH" {
			foundPath = true
		}
		if n == "FOO_SECRET" {
			t.Error("FOO_SECRET must not be in passed")
		}
	}
	if !foundPath {
		t.Error("PATH must be in passed")
	}
	foundDropped := false
	for _, n := range dropped {
		if n == "FOO_SECRET" {
			foundDropped = true
		}
	}
	if !foundDropped {
		t.Error("FOO_SECRET must be in dropped")
	}
}

// TestKnownServerEnvHintWarns verifies the migration-safety warning: a
// known server package (github) whose command matches, with GITHUB_TOKEN in
// the parent but not passed through, logs a warning naming the variable.
func TestKnownServerEnvHintWarns(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "GITHUB_TOKEN=ghp_x"}
	cfg := StdioServerConfig{
		Name:    "github",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-github"},
	}

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	if _, err := buildChildEnv(parent, cfg, logger); err != nil {
		t.Fatalf("buildChildEnv: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "GITHUB_TOKEN") {
		t.Errorf("expected a warning naming GITHUB_TOKEN, got: %s", out)
	}
	if strings.Contains(out, "ghp_x") {
		t.Error("warning must never contain the variable's value")
	}
}

// TestKnownServerEnvHintSilentWhenPassed verifies no warning is logged once
// the operator has passed the variable through explicitly.
func TestKnownServerEnvHintSilentWhenPassed(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "GITHUB_TOKEN=ghp_x"}
	cfg := StdioServerConfig{
		Name:           "github",
		Command:        "npx",
		Args:           []string{"-y", "@modelcontextprotocol/server-github"},
		EnvPassthrough: []string{"GITHUB_TOKEN"},
	}

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	if _, err := buildChildEnv(parent, cfg, logger); err != nil {
		t.Fatalf("buildChildEnv: %v", err)
	}
	if strings.Contains(buf.String(), "GITHUB_TOKEN") {
		t.Errorf("no warning expected once GITHUB_TOKEN is passed, got: %s", buf.String())
	}
}
