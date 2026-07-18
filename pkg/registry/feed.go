package registry

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/utils"
)

const (
	DefaultRegistryURL  = "https://registry.mcp.io/index.ndjson"
	DefaultSyncInterval = 1 * time.Hour
	CacheStaleThreshold = 24 * time.Hour
)

type RegistryFeedEntry struct {
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	URL           string            `json:"url,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Transport     string            `json:"transport,omitempty"`
	TrustScore    int               `json:"trust_score,omitempty"`
	LastRelease   string            `json:"last_release,omitempty"`
	OpenIssues    int               `json:"open_issues,omitempty"`
	Downloads     int               `json:"downloads,omitempty"`
	TokensPerTurn int64             `json:"tokens_per_turn,omitempty"`
	Categories    []string          `json:"categories,omitempty"`
}

type FeedIndex struct {
	SyncedAt time.Time           `json:"synced_at"`
	Entries  []RegistryFeedEntry `json:"entries"`
}

type FeedFetcher struct {
	registryURL string
	cacheDir    string
	logger      *slog.Logger
	client      *http.Client
	interval    time.Duration

	loadOnce sync.Once
	loadErr  error
	cached   *FeedIndex

	refreshWG sync.WaitGroup

	onSync func(entries []RegistryFeedEntry)
}

func (f *FeedFetcher) OnSync(fn func(entries []RegistryFeedEntry)) {
	f.onSync = fn
}

func NewFeedFetcher(logger *slog.Logger, cacheDir string) *FeedFetcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &FeedFetcher{
		registryURL: DefaultRegistryURL,
		cacheDir:    cacheDir,
		logger:      logger,
		client:      &http.Client{Timeout: 30 * time.Second},
		interval:    DefaultSyncInterval,
	}
}

func (f *FeedFetcher) WithURL(url string) *FeedFetcher {
	f.registryURL = url
	return f
}

func (f *FeedFetcher) WithInterval(d time.Duration) *FeedFetcher {
	f.interval = d
	return f
}

func (f *FeedFetcher) WithHTTPClient(c *http.Client) *FeedFetcher {
	f.client = c
	return f
}

func (f *FeedFetcher) RegistryDir() string {
	return filepath.Join(f.cacheDir, "registry")
}

func (f *FeedFetcher) IndexPath() string {
	return filepath.Join(f.RegistryDir(), "index.json")
}

func (f *FeedFetcher) Sync(ctx context.Context) error {
	f.logger.Debug("syncing registry feed", "url", f.registryURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.registryURL, nil)
	if err != nil {
		return fmt.Errorf("registry feed: create request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("registry feed: network error: %w\nHint: check your connection or try again later with `leanproxy marketplace sync`", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry feed: registry returned HTTP %d\nHint: the registry may be temporarily unavailable; try again later with `leanproxy marketplace sync`", resp.StatusCode)
	}

	entries := make([]RegistryFeedEntry, 0, 256)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry RegistryFeedEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			f.logger.Warn("registry feed: skipping malformed entry", "error", err)
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("registry feed: read error: %w", err)
	}

	index := FeedIndex{
		SyncedAt: time.Now(),
		Entries:  entries,
	}

	// Detect whether the feed content actually changed since the last sync
	// so downstream invalidation hooks only fire on real changes.
	changed := true
	if prev, err := f.readCacheFromDisk(); err == nil && prev != nil {
		if hashFeedEntries(prev.Entries) == hashFeedEntries(entries) {
			changed = false
		}
	}

	if err := f.store(index); err != nil {
		return fmt.Errorf("registry feed: store cache: %w", err)
	}

	// Reset singleflight so subsequent reads observe the freshly written index.
	f.loadOnce = sync.Once{}
	f.loadErr = nil
	f.cached = &index

	f.logger.Info("registry feed synced",
		"entries", len(entries),
		"cache", f.IndexPath(),
	)

	if f.onSync != nil && changed {
		f.invokeOnSync(entries)
	} else if !changed {
		f.logger.Debug("registry feed unchanged, skipping onSync hook")
	}

	return nil
}

// invokeOnSync runs the registered callback with panic isolation so a faulty
// hook cannot crash the process.
func (f *FeedFetcher) invokeOnSync(entries []RegistryFeedEntry) {
	defer func() {
		if r := recover(); r != nil {
			f.logger.Error("registry feed: onSync callback panicked", "panic", r)
		}
	}()
	f.onSync(entries)
}

// hashFeedEntries returns a stable hash of the entry list for change
// detection between syncs.
func hashFeedEntries(entries []RegistryFeedEntry) string {
	data, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (f *FeedFetcher) store(index FeedIndex) error {
	dir := f.RegistryDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	path := f.IndexPath()
	baseDir := filepath.Dir(filepath.Clean(path))
	if err := utils.ValidatePath(path, baseDir); err != nil {
		return fmt.Errorf("invalid cache path: %w", err)
	}

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temp index: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}

	return nil
}

func (f *FeedFetcher) LoadCache() (*FeedIndex, error) {
	f.loadOnce.Do(func() {
		f.cached, f.loadErr = f.readCacheFromDisk()
	})
	if f.loadErr != nil {
		// Allow re-attempt on next call after a transient failure (e.g. corrupt file).
		f.loadOnce = sync.Once{}
	}
	return f.cached, f.loadErr
}

func (f *FeedFetcher) readCacheFromDisk() (*FeedIndex, error) {
	path := f.IndexPath()
	baseDir := filepath.Dir(filepath.Clean(path))
	if err := utils.ValidatePath(path, baseDir); err != nil {
		return nil, fmt.Errorf("invalid cache path: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cache: %w", err)
	}

	var index FeedIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse cache: %w", err)
	}

	return &index, nil
}

func (f *FeedFetcher) CacheAge() (time.Duration, error) {
	index, err := f.LoadCache()
	if err != nil {
		return 0, err
	}
	if index == nil || index.SyncedAt.IsZero() {
		return 0, nil
	}
	return time.Since(index.SyncedAt), nil
}

func (f *FeedFetcher) CacheStaleInfo() string {
	age, err := f.CacheAge()
	if err != nil {
		f.logger.Warn("registry feed: failed to check cache age", "error", err)
		return "registry cache is unreadable. Run `leanproxy marketplace sync` to rebuild."
	}
	if age < 0 {
		f.logger.Warn("registry feed: cache timestamp is in the future; treating as unreadable")
		return "registry cache timestamp is invalid. Run `leanproxy marketplace sync` to rebuild."
	}
	if age == 0 {
		return ""
	}
	if age >= CacheStaleThreshold {
		return fmt.Sprintf("registry cache is %.0f hours old. Run `leanproxy marketplace sync` to refresh.", age.Hours())
	}
	return ""
}

func (f *FeedFetcher) StartPeriodicRefresh(ctx context.Context) {
	f.refreshWG.Add(1)
	go func() {
		defer f.refreshWG.Done()
		f.logger.Debug("starting periodic registry feed refresh", "interval", f.interval)
		ticker := time.NewTicker(f.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				f.logger.Debug("stopping periodic registry feed refresh")
				return
			case <-ticker.C:
				f.logger.Debug("periodic registry feed refresh triggered")
				if err := f.Sync(ctx); err != nil {
					f.logger.Warn("periodic registry feed sync failed", "error", err)
				}
			}
		}
	}()
}

func (f *FeedFetcher) WaitRefreshDone() {
	f.refreshWG.Wait()
}

func LeanProxyDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, ".leanproxy"), nil
}
