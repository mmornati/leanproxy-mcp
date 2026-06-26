package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFeedFetcher_Sync_Success(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fetcher := NewFeedFetcher(logger, cacheDir)

	entries := []RegistryFeedEntry{
		{Name: "github", Description: "GitHub API", URL: "https://github.com/mcp", Transport: "stdio"},
		{Name: "slack", Description: "Slack API", URL: "https://slack.com/mcp", Transport: "http"},
		{Name: "postgres", Description: "PostgreSQL", URL: "https://postgres.com/mcp", Transport: "stdio"},
	}
	ts := newTestRegistryServer(t, entries)
	defer ts.Close()

	fetcher.WithURL(ts.URL)

	ctx := context.Background()
	if err := fetcher.Sync(ctx); err != nil {
		t.Fatalf("Sync() failed: %v", err)
	}

	indexPath := filepath.Join(cacheDir, "registry", "index.json")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Fatalf("index.json not created at %s", indexPath)
	}

	loaded, err := fetcher.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache() failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadCache() returned nil")
	}
	if loaded.SyncedAt.IsZero() {
		t.Error("SyncedAt is zero")
	}
	if len(loaded.Entries) != 3 {
		t.Errorf("entries count = %d, want 3", len(loaded.Entries))
	}
	if loaded.Entries[0].Name != "github" {
		t.Errorf("first entry name = %s, want github", loaded.Entries[0].Name)
	}
}

func TestFeedFetcher_Sync_NetworkError_PreservesCache(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	entries := []RegistryFeedEntry{
		{Name: "github", Description: "GitHub API"},
	}
	ts := newTestRegistryServer(t, entries)
	ts.Close()

	fetcher.WithURL(ts.URL)

	ctx := context.Background()
	err := fetcher.Sync(ctx)
	if err == nil {
		t.Fatal("Sync() should fail when server is down")
	}
	if !strings.Contains(fmt.Sprintf("%v", err), "Hint") {
		t.Errorf("error should contain retry guidance, got: %v", err)
	}

	indexPath := filepath.Join(cacheDir, "registry", "index.json")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Log("cache does not exist yet (first sync failed — expected)")
	}
}

func TestFeedFetcher_StaleCacheNotice(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	os.MkdirAll(filepath.Join(cacheDir, "registry"), 0700)

	staleIndex := FeedIndex{
		SyncedAt: time.Now().Add(-48 * time.Hour),
		Entries:  []RegistryFeedEntry{{Name: "github"}},
	}
	data, _ := json.MarshalIndent(staleIndex, "", "  ")
	os.WriteFile(filepath.Join(cacheDir, "registry", "index.json"), data, 0600)

	notice := fetcher.CacheStaleInfo()
	if notice == "" {
		t.Fatal("CacheStaleInfo() returned empty for stale cache")
	}
	if !strings.Contains(notice, "leanproxy marketplace sync") {
		t.Errorf("notice should suggest sync command, got: %s", notice)
	}
	if !strings.Contains(notice, "48") {
		t.Errorf("notice should mention age in hours, got: %s", notice)
	}
}

func TestFeedFetcher_FreshCache_NoNotice(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	os.MkdirAll(filepath.Join(cacheDir, "registry"), 0700)

	freshIndex := FeedIndex{
		SyncedAt: time.Now().Add(-1 * time.Hour),
		Entries:  []RegistryFeedEntry{{Name: "github"}},
	}
	data, _ := json.MarshalIndent(freshIndex, "", "  ")
	os.WriteFile(filepath.Join(cacheDir, "registry", "index.json"), data, 0600)

	notice := fetcher.CacheStaleInfo()
	if notice != "" {
		t.Errorf("expected no notice for fresh cache, got: %s", notice)
	}
}

func TestFeedFetcher_NoCache_NoNotice(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	notice := fetcher.CacheStaleInfo()
	if notice != "" {
		t.Errorf("expected no notice for missing cache, got: %s", notice)
	}
}

func TestFeedFetcher_Sync_HTTPError(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	fetcher.WithURL(ts.URL)

	ctx := context.Background()
	err := fetcher.Sync(ctx)
	if err == nil {
		t.Fatal("Sync() should fail on HTTP 503")
	}
	if !strings.Contains(fmt.Sprintf("%v", err), "503") {
		t.Errorf("error should mention HTTP status, got: %v", err)
	}
	if !strings.Contains(fmt.Sprintf("%v", err), "Hint") {
		t.Errorf("error should contain retry guidance, got: %v", err)
	}
}

func TestFeedFetcher_LoadCache_MissingFile(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	index, err := fetcher.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache() on missing dir: %v", err)
	}
	if index != nil {
		t.Error("expected nil index for missing file")
	}
}

func TestFeedFetcher_Sync_EmptyBody(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fetcher.WithURL(ts.URL)

	ctx := context.Background()
	if err := fetcher.Sync(ctx); err != nil {
		t.Fatalf("Sync() with empty body: %v", err)
	}

	loaded, err := fetcher.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache() failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadCache() returned nil")
	}
	if len(loaded.Entries) != 0 {
		t.Errorf("expected 0 entries for empty body, got %d", len(loaded.Entries))
	}
}

func TestFeedFetcher_StartPeriodicRefresh(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		fmt.Fprintln(w, `{"name":"github","description":"GitHub API"}`)
	}))
	defer ts.Close()

	fetcher.WithURL(ts.URL)
	fetcher.WithInterval(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	fetcher.StartPeriodicRefresh(ctx)

	<-ctx.Done()

	if callCount < 2 {
		t.Errorf("expected at least 2 sync calls, got %d", callCount)
	}
}

func newTestRegistryServer(t *testing.T, entries []RegistryFeedEntry) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, entry := range entries {
			data, err := json.Marshal(entry)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Fprintln(w, string(data))
		}
	}))
}
