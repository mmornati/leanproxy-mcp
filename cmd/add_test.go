package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

func TestAddRegistryCmd_Registered(t *testing.T) {
	var found bool
	for _, c := range RootCmd.Commands() {
		if c.Use == "add <server-id>" {
			found = true
			if c.Short == "" {
				t.Error("add command should have a short description")
			}
			if !containsString(c.Aliases, "install") {
				t.Error("add command should alias 'install'")
			}
			break
		}
	}
	if !found {
		t.Fatal("'add' command not registered on RootCmd")
	}
}

func TestAddRegistryCmd_Flags(t *testing.T) {
	flags := addRegistryCmd.Flags()
	for _, name := range []string{"force", "yes", "dry-run", "stop-existing", "graceful-wait"} {
		if flags.Lookup(name) == nil {
			t.Errorf("add command missing --%s flag", name)
		}
	}
	if addRegistryCmd.Args == nil {
		t.Fatal("add command should declare an Args validator")
	}
	if err := addRegistryCmd.Args(addRegistryCmd, []string{}); err == nil {
		t.Error("add command should require exactly one arg")
	}
	if err := addRegistryCmd.Args(addRegistryCmd, []string{"a", "b"}); err == nil {
		t.Error("add command should reject more than one arg")
	}
}

// writeIndex writes a FeedIndex JSON to the cache dir consumed by FeedFetcher.
func writeIndex(t *testing.T, dir string, index registry.FeedIndex) {
	t.Helper()
	registryDir := filepath.Join(dir, "registry")
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registryDir, "index.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFeedSourceAdapter_NilFetcher(t *testing.T) {
	a := &feedSourceAdapter{}
	_, err := a.LookupCache(context.Background())
	if err == nil {
		t.Fatal("expected error for nil fetcher")
	}
}

func TestFeedSourceAdapter_PopulatedCache(t *testing.T) {
	dir := t.TempDir()
	writeIndex(t, dir, registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{
				Name:      "github",
				Transport: "stdio",
				Command:   "gh-mcp",
				Args:      []string{"--port", "8080"},
				Env:       map[string]string{"FOO": "bar"},
				URL:       "https://example.com",
			},
			{
				Name:      "remote",
				Transport: "http",
				URL:       "https://remote.example/mcp",
			},
		},
	})

	fetcher := registry.NewFeedFetcher(nil, dir)
	a := &feedSourceAdapter{fetcher: fetcher}
	snap, err := a.LookupCache(context.Background())
	if err != nil {
		t.Fatalf("LookupCache: %v", err)
	}
	if len(snap.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap.Entries))
	}
	var gh, remote *migrate.CacheEntry
	for i := range snap.Entries {
		e := &snap.Entries[i]
		switch e.Name {
		case "github":
			gh = e
		case "remote":
			remote = e
		}
	}
	if gh == nil || gh.Command != "gh-mcp" {
		t.Errorf("github entry malformed: %+v", gh)
	}
	if gh.Env["FOO"] != "bar" {
		t.Errorf("github env not copied: %+v", gh.Env)
	}
	if len(gh.Args) != 2 || gh.Args[0] != "--port" {
		t.Errorf("github args not copied: %v", gh.Args)
	}
	if remote == nil || remote.URL != "https://remote.example/mcp" {
		t.Errorf("remote entry malformed: %+v", remote)
	}
}

func TestFeedSourceAdapter_EmptyCache(t *testing.T) {
	dir := t.TempDir()
	fetcher := registry.NewFeedFetcher(nil, dir)
	a := &feedSourceAdapter{fetcher: fetcher}
	snap, err := a.LookupCache(context.Background())
	if err != nil {
		t.Fatalf("LookupCache: %v", err)
	}
	if len(snap.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(snap.Entries))
	}
}

func TestCloneStringMap(t *testing.T) {
	if cloneStringMap(nil) != nil {
		t.Error("nil should return nil")
	}
	if cloneStringMap(map[string]string{}) != nil {
		t.Error("empty map should return nil")
	}
	src := map[string]string{"a": "1", "b": "2"}
	dst := cloneStringMap(src)
	if len(dst) != 2 {
		t.Errorf("len = %d", len(dst))
	}
	dst["a"] = "modified"
	if src["a"] != "1" {
		t.Error("clone should be independent of source")
	}
}

func TestLoadExistingEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")

	if got, err := loadExistingEntry(path, "missing"); err != nil || got != nil {
		t.Errorf("non-existent config: got=%v err=%v", got, err)
	}

	if err := os.WriteFile(path, []byte(`version: "1.0"
servers:
  - name: github
    transport: stdio
    enabled: true
    stdio:
      command: gh
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadExistingEntry(path, "github")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name != "github" {
		t.Errorf("got = %+v, want github", got)
	}
	got, err = loadExistingEntry(path, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("absent server returned %+v", got)
	}
}

func TestDescriptionFor(t *testing.T) {
	if got := descriptionFor(migrate.CacheEntry{URL: "https://x"}); got != "https://x" {
		t.Errorf("URL precedence: got %q", got)
	}
	if got := descriptionFor(migrate.CacheEntry{Args: []string{"a", "b"}}); got != "a b" {
		t.Errorf("Args: got %q", got)
	}
	if got := descriptionFor(migrate.CacheEntry{Command: "gh-mcp"}); got != "gh-mcp" {
		t.Errorf("Command: got %q", got)
	}
	if got := descriptionFor(migrate.CacheEntry{}); got != "" {
		t.Errorf("empty: got %q", got)
	}
}

func TestRunAdd_EmptyArg(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := runAdd(cmd, []string{""}); err == nil {
		t.Fatal("expected error for empty arg")
	}
}

func TestRunAdd_NoCacheReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANPROXY_CONFIG", filepath.Join(cfgDir, "leanproxy_servers.yaml"))
	t.Setenv("HOME", dir)
	// No .leanproxy directory exists -> empty cache.

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := runAdd(cmd, []string{"github"})
	if err == nil {
		t.Fatal("expected error when cache is empty")
	}
	if !strings.Contains(err.Error(), "marketplace sync") {
		t.Errorf("error = %v, expected hint about marketplace sync", err)
	}
}

func TestRunAdd_UnknownServerWithSuggestion(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANPROXY_CONFIG", filepath.Join(cfgDir, "leanproxy_servers.yaml"))
	t.Setenv("HOME", dir)

	// FeedFetcher caches under $HOME/.leanproxy/registry/.
	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{Name: "github", Transport: "stdio", Command: "gh"},
		},
	})

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	err := runAdd(cmd, []string{"gitub"})
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
	if !strings.Contains(err.Error(), "unknown server") {
		t.Errorf("error = %v, expected 'unknown server' mention", err)
	}
	if !strings.Contains(stderr.String(), "leanproxy add github") {
		t.Errorf("stderr should suggest 'leanproxy add github', got %q", stderr.String())
	}
}

func TestRunAdd_FreshInstallWritesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "leanproxy_servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", cfgPath)
	t.Setenv("HOME", dir)

	writeIndex(t, filepath.Join(dir, ".leanproxy"), registry.FeedIndex{
		SyncedAt: testNow(t),
		Entries: []registry.RegistryFeedEntry{
			{
				Name:      "github",
				Transport: "stdio",
				Command:   "gh-mcp",
				Args:      []string{"--stdio"},
			},
		},
	})

	stdout := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := runAdd(cmd, []string{"github"}); err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), "github") {
		t.Errorf("config missing github: %s", data)
	}
	if !strings.Contains(stdout.String(), "Installed") {
		t.Errorf("stdout should report install, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Token-cost preview") {
		t.Errorf("stdout should include token-cost preview, got %q", stdout.String())
	}
}

func testNow(_ *testing.T) time.Time {
	return time.Now()
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
