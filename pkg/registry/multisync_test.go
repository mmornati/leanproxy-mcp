package registry

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/registry/mcpregistry"
)

func TestMapOfficialServer_NpmPackage(t *testing.T) {
	s := mcpregistry.Server{
		Name:        "io.github.example/weather",
		Description: "Weather",
		Version:     "1.4.0",
		License:     "MIT",
		Repository:  mcpregistry.Repository{URL: "https://github.com/example/weather"},
		Packages: []mcpregistry.Package{{
			RegistryType: "npm",
			Identifier:   "@example/weather-mcp",
			Version:      "1.4.0",
			EnvironmentVariables: []mcpregistry.EnvVar{
				{Name: "WEATHER_API_KEY", IsRequired: true},
			},
		}},
		Meta: mcpregistry.Meta{Official: &mcpregistry.OfficialMeta{IsVerified: true}},
	}
	entry := mapOfficialServer(s)

	if entry.Command != "npx" {
		t.Errorf("Command = %q, want npx", entry.Command)
	}
	if want := []string{"-y", "@example/weather-mcp@1.4.0"}; !equalStrings(entry.Args, want) {
		t.Errorf("Args = %v, want %v", entry.Args, want)
	}
	if !entry.NamespaceVerified {
		t.Error("expected NamespaceVerified = true")
	}
	if entry.License != "MIT" {
		t.Errorf("License = %q", entry.License)
	}
	if entry.Version != "1.4.0" {
		t.Errorf("Version = %q", entry.Version)
	}
	if entry.Source != SourceOfficial {
		t.Errorf("Source = %q, want %q", entry.Source, SourceOfficial)
	}
	if _, ok := entry.Env["WEATHER_API_KEY"]; !ok {
		t.Errorf("expected env var name present, got %+v", entry.Env)
	}
	if entry.Env["WEATHER_API_KEY"] != "" {
		t.Error("env var value must not be populated from the registry (names only)")
	}
}

func TestMapOfficialServer_PypiPackage(t *testing.T) {
	s := mcpregistry.Server{
		Name: "example",
		Packages: []mcpregistry.Package{{
			RegistryType: "pypi",
			Identifier:   "example-fs-mcp",
			Version:      "0.9.2",
		}},
	}
	entry := mapOfficialServer(s)
	if entry.Command != "uvx" {
		t.Errorf("Command = %q, want uvx", entry.Command)
	}
	if want := []string{"example-fs-mcp==0.9.2"}; !equalStrings(entry.Args, want) {
		t.Errorf("Args = %v, want %v", entry.Args, want)
	}
}

func TestMapOfficialServer_OCIDigestPackage(t *testing.T) {
	s := mcpregistry.Server{
		Name: "example",
		Packages: []mcpregistry.Package{{
			RegistryType: "oci",
			Identifier:   "ghcr.io/example/mcp",
			Version:      "sha256:abc123",
		}},
	}
	entry := mapOfficialServer(s)
	if entry.Command != "docker" {
		t.Errorf("Command = %q, want docker", entry.Command)
	}
	last := entry.Args[len(entry.Args)-1]
	if last != "ghcr.io/example/mcp@sha256:abc123" {
		t.Errorf("last arg = %q", last)
	}
}

func TestMapOfficialServer_RemoteOnly(t *testing.T) {
	s := mcpregistry.Server{
		Name: "remote-search",
		Remotes: []mcpregistry.Remote{
			{TransportType: "streamable-http", URL: "https://mcp.example.com/search"},
		},
	}
	entry := mapOfficialServer(s)
	if entry.Transport != "http" {
		t.Errorf("Transport = %q, want http", entry.Transport)
	}
	if entry.URL != "https://mcp.example.com/search" {
		t.Errorf("URL = %q", entry.URL)
	}
}

func TestMapOfficialServer_SSERemote(t *testing.T) {
	s := mcpregistry.Server{
		Name:    "sse-server",
		Remotes: []mcpregistry.Remote{{TransportType: "sse", URL: "https://mcp.example.com/sse"}},
	}
	entry := mapOfficialServer(s)
	if entry.Transport != "sse" {
		t.Errorf("Transport = %q, want sse", entry.Transport)
	}
}

func TestSyncSources_MergesOfficialAndCustom(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[{"name":"official-one","version":"1.0.0","packages":[{"registryType":"npm","identifier":"official-one","version":"1.0.0"}]}],"metadata":{}}`))
	}))
	defer official.Close()

	custom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"custom-one","transport":"stdio","command":"custom-cmd","trust_score":99}` + "\n"))
	}))
	defer custom.Close()

	dir := t.TempDir()
	fetcher := NewFeedFetcher(slog.Default(), dir).
		WithOfficialClient(mcpregistry.New().WithBaseURL(official.URL)).
		WithCustomSources([]NamedFeedSource{{Name: "acme", URL: custom.URL}})

	if err := fetcher.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	index, err := fetcher.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(index.Entries) != 2 {
		t.Fatalf("expected 2 merged entries, got %d: %+v", len(index.Entries), index.Entries)
	}

	var official1, custom1 *RegistryFeedEntry
	for i := range index.Entries {
		e := &index.Entries[i]
		switch e.Name {
		case "official-one":
			official1 = e
		case "custom-one":
			custom1 = e
		}
	}
	if official1 == nil || official1.Source != SourceOfficial {
		t.Errorf("official entry missing or mis-tagged: %+v", official1)
	}
	if custom1 == nil || custom1.Source != "acme" {
		t.Errorf("custom entry missing or mis-tagged: %+v", custom1)
	}
	// The feed-provided trust_score of the custom entry must never affect
	// the computed trust score (issue #313), even though it is preserved
	// on the struct for backward-compat display of raw cache content.
	if custom1 != nil && CalculateTrustScore(*custom1) == 99 {
		t.Error("custom source's self-reported trust_score must not be used")
	}
}

func TestSyncSources_CustomSourceFailureDoesNotBlockOfficial(t *testing.T) {
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[{"name":"official-one","version":"1.0.0"}],"metadata":{}}`))
	}))
	defer official.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	dir := t.TempDir()
	fetcher := NewFeedFetcher(slog.Default(), dir).
		WithOfficialClient(mcpregistry.New().WithBaseURL(official.URL)).
		WithCustomSources([]NamedFeedSource{{Name: "broken", URL: broken.URL}})

	if err := fetcher.Sync(context.Background()); err != nil {
		t.Fatalf("Sync should not fail when only a custom source errors: %v", err)
	}
	index, err := fetcher.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(index.Entries) != 1 || index.Entries[0].Name != "official-one" {
		t.Errorf("expected only the official entry, got %+v", index.Entries)
	}
}

func TestSyncSources_OfficialFailureIsReturned(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	dir := t.TempDir()
	fetcher := NewFeedFetcher(slog.Default(), dir).
		WithOfficialClient(mcpregistry.New().WithBaseURL(broken.URL))

	if err := fetcher.Sync(context.Background()); err == nil {
		t.Fatal("expected error when the official registry sync fails")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNewFeedFetcher_DefaultsToOfficialSource is a light regression guard:
// a plain NewFeedFetcher (as cmd/marketplace uses) must default to the
// official registry rather than the old unowned registry.mcp.io domain.
func TestNewFeedFetcher_DefaultsToOfficialSource(t *testing.T) {
	f := NewFeedFetcher(slog.Default(), t.TempDir())
	if f.official == nil {
		t.Fatal("NewFeedFetcher should default to the official registry client")
	}
	if strings.Contains(DefaultRegistryURL, "registry.mcp.io") {
		t.Error("DefaultRegistryURL must not point at the unowned registry.mcp.io domain")
	}
}
