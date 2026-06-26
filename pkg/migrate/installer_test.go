package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSource is an in-memory ServerSource used by installer tests.
type fakeSource struct {
	entries []CacheEntry
	err     error
}

func (f *fakeSource) LookupCache(_ context.Context) (CacheSnapshot, error) {
	if f.err != nil {
		return CacheSnapshot{}, f.err
	}
	return CacheSnapshot{Entries: f.entries}, nil
}

type fakeStopper struct {
	stopped []string
	err     error
}

func (f *fakeStopper) Stop(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	f.stopped = append(f.stopped, id)
	return nil
}

func TestResolve_ExactMatch(t *testing.T) {
	src := &fakeSource{entries: []CacheEntry{
		{Name: "github", Transport: "stdio", Command: "gh-mcp"},
		{Name: "filesystem", Transport: "stdio", Command: "fs-mcp"},
	}}
	inst := NewInstaller(src, "", nil)

	got, err := inst.Resolve(context.Background(), "github")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Name != "github" {
		t.Errorf("Resolve = %q, want %q", got.Name, "github")
	}
}

func TestResolve_CaseInsensitive(t *testing.T) {
	src := &fakeSource{entries: []CacheEntry{{Name: "GitHub", Transport: "stdio", Command: "gh"}}}
	inst := NewInstaller(src, "", nil)

	got, err := inst.Resolve(context.Background(), "github")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Name != "GitHub" {
		t.Errorf("Resolve = %q, want %q (case preserved)", got.Name, "GitHub")
	}
}

func TestResolve_UnknownWithSuggestions(t *testing.T) {
	src := &fakeSource{entries: []CacheEntry{
		{Name: "github", Transport: "stdio"},
		{Name: "gitlab", Transport: "stdio"},
		{Name: "bitbucket", Transport: "stdio"},
		{Name: "files", Transport: "stdio"},
	}}
	inst := NewInstaller(src, "", nil)

	_, err := inst.Resolve(context.Background(), "gitub")
	if !IsUnknownServer(err) {
		t.Fatalf("expected ErrUnknownServer, got %v", err)
	}
	var unk *ErrUnknownServer
	if !errors.As(err, &unk) {
		t.Fatalf("errors.As failed for %v", err)
	}
	if len(unk.Suggested) == 0 {
		t.Fatal("expected at least one suggestion for close typo")
	}
	if unk.Suggested[0] != "github" {
		t.Errorf("top suggestion = %q, want %q", unk.Suggested[0], "github")
	}
	if len(unk.Suggested) > SimilarityLimit {
		t.Errorf("suggestions exceed limit: %d", len(unk.Suggested))
	}
}

func TestResolve_UnknownEmptyID(t *testing.T) {
	inst := NewInstaller(&fakeSource{}, "", nil)
	_, err := inst.Resolve(context.Background(), "")
	if !IsUnknownServer(err) {
		t.Fatalf("expected ErrUnknownServer, got %v", err)
	}
}

func TestResolve_SourceErrorWrapped(t *testing.T) {
	src := &fakeSource{err: errors.New("disk on fire")}
	inst := NewInstaller(src, "", nil)
	_, err := inst.Resolve(context.Background(), "github")
	if err == nil || IsUnknownServer(err) {
		t.Fatalf("expected wrapped source error, got %v", err)
	}
	if !strings.Contains(err.Error(), "load cache") {
		t.Errorf("error should mention load cache, got %v", err)
	}
}

func TestInstall_FreshStdioInstall(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")

	src := &fakeSource{entries: []CacheEntry{{
		Name: "github", Transport: "stdio", Command: "gh-mcp",
		Args: []string{"--port", "8080"},
		Env:  map[string]string{"GITHUB_TOKEN": "secret"},
	}}}
	inst := NewInstaller(src, cfgPath, nil)

	res, err := inst.Install(context.Background(), CacheEntry{
		Name: "github", Transport: "stdio", Command: "gh-mcp",
		Args: []string{"--port", "8080"},
		Env:  map[string]string{"GITHUB_TOKEN": "secret"},
	}, InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Replaced {
		t.Error("Replaced should be false on fresh install")
	}
	if res.ServerName != "github" {
		t.Errorf("ServerName = %q, want %q", res.ServerName, "github")
	}
	if res.ConfigPath != cfgPath {
		t.Errorf("ConfigPath = %q, want %q", res.ConfigPath, cfgPath)
	}

	// Verify the config file was written.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), "github") {
		t.Errorf("config missing server name: %s", data)
	}
	if !strings.Contains(string(data), "gh-mcp") {
		t.Errorf("config missing command: %s", data)
	}

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config perm = %o, want 0600", perm)
	}
}

func TestInstall_HTTPTransportUsesURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")

	entry := CacheEntry{Name: "remote", Transport: "http", URL: "https://example.com/mcp"}
	src := &fakeSource{entries: []CacheEntry{entry}}
	inst := NewInstaller(src, cfgPath, nil)

	res, err := inst.Install(context.Background(), entry, InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Transport != "http" {
		t.Errorf("Transport = %q, want http", res.Transport)
	}

	cfg, err := LoadConfig(context.Background(), cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].HTTP == nil {
		t.Fatalf("expected one HTTP server, got %+v", cfg.Servers)
	}
	if cfg.Servers[0].HTTP.URL != "https://example.com/mcp" {
		t.Errorf("HTTP.URL = %q", cfg.Servers[0].HTTP.URL)
	}
}

func TestInstall_StdioRequiresCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	src := &fakeSource{}
	inst := NewInstaller(src, cfgPath, nil)

	_, err := inst.Install(context.Background(), CacheEntry{Name: "broken", Transport: "stdio"}, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "Command") {
		t.Fatalf("expected command-required error, got %v", err)
	}
}

func TestInstall_HTTPRequiresURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)

	_, err := inst.Install(context.Background(), CacheEntry{Name: "broken", Transport: "http"}, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("expected url-required error, got %v", err)
	}
}

func TestInstall_UnsupportedTransport(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)

	_, err := inst.Install(context.Background(), CacheEntry{Name: "x", Transport: "grpc"}, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("expected unsupported transport error, got %v", err)
	}
}

func TestInstall_RejectsExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\nservers:\n  - name: github\n    transport: stdio\n    stdio:\n      command: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	_, err := inst.Install(context.Background(), CacheEntry{Name: "github", Transport: "stdio", Command: "new"}, InstallOptions{})
	if !IsAlreadyInstalled(err) {
		t.Fatalf("expected ErrAlreadyInstalled, got %v", err)
	}
}

func TestInstall_ForceReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\nservers:\n  - name: github\n    transport: stdio\n    enabled: true\n    stdio:\n      command: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	res, err := inst.Install(context.Background(), CacheEntry{Name: "github", Transport: "stdio", Command: "new"}, InstallOptions{Force: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Replaced {
		t.Error("Replaced should be true")
	}

	cfg, err := LoadConfig(context.Background(), cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("expected 1 server after replace, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Stdio.Command != "new" {
		t.Errorf("Command = %q, want %q", cfg.Servers[0].Stdio.Command, "new")
	}
}

func TestInstall_StopExisting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\nservers:\n  - name: github\n    transport: stdio\n    enabled: true\n    stdio:\n      command: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopper := &fakeStopper{}
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	res, err := inst.Install(context.Background(), CacheEntry{Name: "github", Transport: "stdio", Command: "new"}, InstallOptions{
		Force:           true,
		StopExisting:    true,
		GracefulTimeout: 50 * time.Millisecond,
		Stopper:         stopper,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Stopped {
		t.Error("Stopped should be true")
	}
	if len(stopper.stopped) != 1 || stopper.stopped[0] != "github" {
		t.Errorf("stopper called with %v, want [github]", stopper.stopped)
	}
}

func TestInstall_StopperErrorContinues(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\nservers:\n  - name: github\n    transport: stdio\n    enabled: true\n    stdio:\n      command: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopper := &fakeStopper{err: errors.New("boom")}
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	res, err := inst.Install(context.Background(), CacheEntry{Name: "github", Transport: "stdio", Command: "new"}, InstallOptions{
		Force:        true,
		StopExisting: true,
		Stopper:      stopper,
	})
	if err != nil {
		t.Fatalf("Install should not fail when stopper fails: %v", err)
	}
	if res.Stopped {
		t.Error("Stopped should be false when stopper errors")
	}
	// Config should still be updated.
	cfg, err := LoadConfig(context.Background(), cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Stdio.Command != "new" {
		t.Errorf("config not updated: %+v", cfg.Servers)
	}
}

func TestInstall_DryRunNoWrite(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	res, err := inst.Install(context.Background(), CacheEntry{Name: "github", Transport: "stdio", Command: "gh"}, InstallOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.DryRun {
		t.Error("DryRun should be true in result")
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("config file should not exist on dry-run, got err=%v", err)
	}
}

func TestInstall_DryRunStopsExisting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: \"1.0\"\nservers:\n  - name: github\n    transport: stdio\n    enabled: true\n    stdio:\n      command: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopper := &fakeStopper{}
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	res, err := inst.Install(context.Background(), CacheEntry{Name: "github", Transport: "stdio", Command: "new"}, InstallOptions{
		Force:        true,
		StopExisting: true,
		Stopper:      stopper,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Stopped {
		t.Error("DryRun should report Stopped=true")
	}
	if len(stopper.stopped) != 0 {
		t.Errorf("stopper should not be called during dry-run, got %v", stopper.stopped)
	}
}

func TestInstall_CreatesMissingConfigDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nested", "deep", "leanproxy_servers.yaml")
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	_, err := inst.Install(context.Background(), CacheEntry{Name: "github", Transport: "stdio", Command: "gh"}, InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("expected config to be created: %v", err)
	}
}

func TestInstall_HeaderPrefixBecomesHTTPHeader(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	inst := NewInstaller(&fakeSource{}, cfgPath, nil)
	entry := CacheEntry{
		Name:      "remote",
		Transport: "http",
		URL:       "https://example.com",
		Env: map[string]string{
			"HEADER_AUTHORIZATION": "Bearer abc",
			"GITHUB_TOKEN":         "secret",
		},
	}
	_, err := inst.Install(context.Background(), entry, InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	cfg, err := LoadConfig(context.Background(), cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers[0].HTTP.Headers["authorization"] != "Bearer abc" {
		t.Errorf("expected authorization header, got %+v", cfg.Servers[0].HTTP.Headers)
	}
	if _, ok := cfg.Servers[0].HTTP.Headers["GITHUB_TOKEN"]; ok {
		t.Error("non-header env should not be promoted to header")
	}
}

func TestSuggestSimilar_LimitAndOrder(t *testing.T) {
	entries := []CacheEntry{
		{Name: "github"}, {Name: "gitlab"}, {Name: "bitbucket"},
		{Name: "dropbox"}, {Name: "filesystem"}, {Name: "sentry"},
	}
	got := SuggestSimilar("githob", entries, 3)
	if len(got) != 3 {
		t.Fatalf("got %d suggestions, want 3", len(got))
	}
	if got[0] != "github" {
		t.Errorf("top suggestion = %q, want github", got[0])
	}
}

func TestSuggestSimilar_ExcludesExactMatch(t *testing.T) {
	entries := []CacheEntry{{Name: "github"}, {Name: "gitlab"}}
	got := SuggestSimilar("github", entries, 5)
	for _, s := range got {
		if s == "github" {
			t.Error("exact match should not appear in suggestions")
		}
	}
}

func TestLevenshtein_KnownPairs(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"a", "b", 1},
	}
	for _, tc := range tests {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSaveConfig_EmptyPath(t *testing.T) {
	if err := SaveConfig("", &Config{}); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestNewInstaller_NilLogger(t *testing.T) {
	inst := NewInstaller(&fakeSource{}, "/tmp/x.yaml", nil)
	if inst.Logger == nil {
		t.Error("NewInstaller should default Logger to slog.Default()")
	}
}
