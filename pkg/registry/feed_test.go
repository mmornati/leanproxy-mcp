package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	seedEntries := []RegistryFeedEntry{
		{Name: "github", Description: "GitHub API"},
		{Name: "slack", Description: "Slack API"},
	}
	upstream := newTestRegistryServer(t, seedEntries)
	fetcher.WithURL(upstream.URL)

	ctx := context.Background()
	if err := fetcher.Sync(ctx); err != nil {
		t.Fatalf("seed Sync() failed: %v", err)
	}

	loadedBefore, err := fetcher.LoadCache()
	if err != nil || loadedBefore == nil {
		t.Fatalf("seed cache load failed: err=%v index=%v", err, loadedBefore)
	}
	if len(loadedBefore.Entries) != 2 {
		t.Fatalf("seed cache should contain 2 entries, got %d", len(loadedBefore.Entries))
	}

	upstream.Close()

	if err := fetcher.Sync(ctx); err == nil {
		t.Fatal("expected Sync() to fail after server is taken down")
	} else if !strings.Contains(err.Error(), "Hint") {
		t.Errorf("error should contain retry guidance, got: %v", err)
	}

	loadedAfter, err := fetcher.LoadCache()
	if err != nil {
		t.Fatalf("post-failure LoadCache failed: %v", err)
	}
	if loadedAfter == nil {
		t.Fatal("cache must survive a failed Sync; got nil")
	}
	if len(loadedAfter.Entries) != 2 {
		t.Errorf("preserved cache must keep all %d entries, got %d", 2, len(loadedAfter.Entries))
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	var (
		mu        sync.Mutex
		callCount int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		fmt.Fprintln(w, `{"name":"github","description":"GitHub API"}`)
	}))
	defer ts.Close()

	fetcher.WithURL(ts.URL)
	fetcher.WithInterval(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	fetcher.StartPeriodicRefresh(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		cc := callCount
		mu.Unlock()
		if cc >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	cc := callCount
	mu.Unlock()
	if cc < 2 {
		t.Fatalf("expected at least 2 sync calls, got %d", cc)
	}

	cancel()

	done := make(chan struct{})
	go func() {
		fetcher.WaitRefreshDone()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPeriodicRefresh goroutine did not exit within 2s of ctx cancel")
	}
}

func TestFeedFetcher_Sync_MalformedLine_SkipsAndContinues(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"name":"github","description":"GitHub API"}`)
		fmt.Fprintln(w, `{not valid json`)
		fmt.Fprintln(w, `{"name":"slack","description":"Slack API"}`)
		fmt.Fprintln(w, ``)
	}))
	defer ts.Close()

	fetcher.WithURL(ts.URL)
	if err := fetcher.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() failed: %v", err)
	}

	loaded, err := fetcher.LoadCache()
	if err != nil || loaded == nil {
		t.Fatalf("LoadCache: err=%v index=%v", err, loaded)
	}
	if len(loaded.Entries) != 2 {
		t.Errorf("expected 2 valid entries (malformed skipped), got %d", len(loaded.Entries))
	}
}

func TestFeedFetcher_StoreIsAtomic(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	ts := newTestRegistryServer(t, []RegistryFeedEntry{{Name: "github"}})
	defer ts.Close()
	fetcher.WithURL(ts.URL)

	if err := fetcher.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() failed: %v", err)
	}

	tmpPath := filepath.Join(fetcher.RegistryDir(), "index.json.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file after a clean Sync, got err=%v", err)
	}

	indexPath := fetcher.IndexPath()
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("index.json should exist after Sync: %v", err)
	}
}

func TestFeedFetcher_CacheStaleInfo_FutureTimestamp(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	if err := os.MkdirAll(filepath.Join(cacheDir, "registry"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	future := FeedIndex{
		SyncedAt: time.Now().Add(48 * time.Hour),
		Entries:  []RegistryFeedEntry{{Name: "github"}},
	}
	data, _ := json.MarshalIndent(future, "", "  ")
	if err := os.WriteFile(filepath.Join(cacheDir, "registry", "index.json"), data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	notice := fetcher.CacheStaleInfo()
	if !strings.Contains(notice, "invalid") {
		t.Errorf("expected notice about invalid timestamp, got: %q", notice)
	}
}

func TestFeedFetcher_CacheStaleInfo_CorruptFile(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	if err := os.MkdirAll(filepath.Join(cacheDir, "registry"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "registry", "index.json"), []byte("{not json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	notice := fetcher.CacheStaleInfo()
	if notice == "" {
		t.Fatal("expected a notice for corrupt cache")
	}
	if !strings.Contains(notice, "leanproxy marketplace sync") {
		t.Errorf("expected notice to suggest sync, got: %q", notice)
	}
}

func TestFeedFetcher_CacheAge_ZeroSyncedAt(t *testing.T) {
	cacheDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFeedFetcher(logger, cacheDir)

	if err := os.MkdirAll(filepath.Join(cacheDir, "registry"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	empty := FeedIndex{Entries: []RegistryFeedEntry{{Name: "github"}}}
	data, _ := json.MarshalIndent(empty, "", "  ")
	if err := os.WriteFile(filepath.Join(cacheDir, "registry", "index.json"), data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	age, err := fetcher.CacheAge()
	if err != nil {
		t.Fatalf("CacheAge: %v", err)
	}
	if age != 0 {
		t.Errorf("zero SyncedAt should yield zero age, got %v", age)
	}
}

func TestLeanProxyDir(t *testing.T) {
	dir, err := LeanProxyDir()
	if err != nil {
		t.Fatalf("LeanProxyDir: %v", err)
	}
	if dir == "" {
		t.Fatal("LeanProxyDir returned empty string")
	}
	if !strings.HasSuffix(dir, ".leanproxy") {
		t.Errorf("expected path to end with .leanproxy, got %q", dir)
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
