package registry

import (
	"bufio"
	"context"
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
	mu          sync.RWMutex
	cached      *FeedIndex
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

	var entries []RegistryFeedEntry
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

	if err := f.store(index); err != nil {
		return fmt.Errorf("registry feed: store cache: %w", err)
	}

	f.mu.Lock()
	f.cached = &index
	f.mu.Unlock()

	f.logger.Info("registry feed synced",
		"entries", len(entries),
		"cache", f.IndexPath(),
	)
	return nil
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

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write index: %w", err)
	}

	return nil
}

func (f *FeedFetcher) LoadCache() (*FeedIndex, error) {
	f.mu.RLock()
	if f.cached != nil {
		defer f.mu.RUnlock()
		return f.cached, nil
	}
	f.mu.RUnlock()

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

	f.mu.Lock()
	f.cached = &index
	f.mu.Unlock()

	return &index, nil
}

func (f *FeedFetcher) CacheAge() (time.Duration, error) {
	index, err := f.LoadCache()
	if err != nil {
		return 0, err
	}
	if index == nil {
		return 0, nil
	}
	return time.Since(index.SyncedAt), nil
}

func (f *FeedFetcher) CacheStaleInfo() string {
	age, err := f.CacheAge()
	if err != nil {
		f.logger.Warn("registry feed: failed to check cache age", "error", err)
		return ""
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
	go func() {
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

func LeanProxyDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	return filepath.Join(home, ".leanproxy"), nil
}
