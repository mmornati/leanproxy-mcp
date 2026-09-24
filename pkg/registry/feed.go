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

	"github.com/mmornati/leanproxy-mcp/pkg/registry/mcpregistry"
	"github.com/mmornati/leanproxy-mcp/pkg/utils"
)

const (
	// DefaultRegistryURL used to point FeedFetcher at an unowned domain
	// (registry.mcp.io) by default, which would have handed whoever
	// controls that domain code execution on every install (issue #313).
	// LeanProxy does not own that domain, so there is no default NDJSON
	// feed URL any more: FeedFetcher.Sync uses the official MCP Registry
	// (see pkg/registry/mcpregistry) unless WithURL configures a specific,
	// user-chosen custom feed. The constant is kept (empty) only so any
	// external reference to it still compiles; do not set a URL here.
	DefaultRegistryURL  = ""
	DefaultSyncInterval = 1 * time.Hour
	CacheStaleThreshold = 24 * time.Hour
)

type RegistryFeedEntry struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	URL         string            `json:"url,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Transport   string            `json:"transport,omitempty"`
	// TrustScore is read from the feed for backward compatibility with old
	// cache files only. It is never used by CalculateTrustScore (issue
	// #313): a feed cannot be trusted to grade itself.
	TrustScore    int      `json:"trust_score,omitempty"`
	LastRelease   string   `json:"last_release,omitempty"`
	OpenIssues    int      `json:"open_issues,omitempty"`
	Downloads     int      `json:"downloads,omitempty"`
	TokensPerTurn int64    `json:"tokens_per_turn,omitempty"`
	Categories    []string `json:"categories,omitempty"`

	// Version is the exact package/image version this entry pins, when
	// known. Empty means the source did not publish a version (a legacy
	// custom feed entry) — installers must not assume "latest" is safe and
	// should pin best-effort (see pkg/migrate.buildServerConfig).
	Version string `json:"version,omitempty"`
	// PackageRegistry identifies which package registry Command/Args were
	// derived from ("npm", "pypi", "oci"), when the entry came from a
	// structured source (the official MCP Registry). Empty for legacy
	// custom-feed entries that only ever provided a raw Command/Args pair.
	PackageRegistry string `json:"package_registry,omitempty"`
	// PackageIdentifier is the bare package name or image reference
	// (without a version suffix) when PackageRegistry is set.
	PackageIdentifier string `json:"package_identifier,omitempty"`
	// Source names where this entry came from: "official" for the MCP
	// Registry API, or the configured name of a custom NDJSON source
	// (registry.sources). Used for provenance display and for
	// InstalledFrom.Registry.
	Source string `json:"source,omitempty"`
	// NamespaceVerified reports whether the source registry verified that
	// the publisher owns the server's namespace (DNS/GitHub verification
	// on the official MCP Registry). Used only as a trust signal — see
	// CalculateTrustScore.
	NamespaceVerified bool `json:"namespace_verified,omitempty"`
	// License is the SPDX identifier or free-form license name reported by
	// the source, when known. Used only as a trust signal.
	License string `json:"license,omitempty"`
}

type FeedIndex struct {
	SyncedAt time.Time           `json:"synced_at"`
	Entries  []RegistryFeedEntry `json:"entries"`
}

// NamedFeedSource is one opt-in custom NDJSON feed the operator configured
// under registry.sources (pkg/migrate.RegistrySettings).
type NamedFeedSource struct {
	Name string
	URL  string
}

type FeedFetcher struct {
	registryURL string
	cacheDir    string
	logger      *slog.Logger
	client      *http.Client
	interval    time.Duration

	// official is the official MCP Registry client used as the default
	// source. Set by NewFeedFetcher; nil disables it (tests only).
	official *mcpregistry.Client
	// customSources are opt-in custom NDJSON feeds configured by the
	// operator, synced in addition to the official registry.
	customSources []NamedFeedSource

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
		official:    mcpregistry.New(),
	}
}

// WithOfficialClient overrides the official-registry client (e.g. to point
// it at a test server, or to disable it by passing nil).
func (f *FeedFetcher) WithOfficialClient(c *mcpregistry.Client) *FeedFetcher {
	f.official = c
	return f
}

// WithCustomSources sets the opt-in custom NDJSON feeds to sync alongside
// the official registry. Only used on the default (no WithURL) sync path.
func (f *FeedFetcher) WithCustomSources(sources []NamedFeedSource) *FeedFetcher {
	f.customSources = sources
	return f
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

// Sync refreshes the local cache. When registryURL is set (via WithURL —
// used directly by tests and by any caller that wants exactly one custom
// NDJSON feed), it fetches only that feed, unchanged from prior releases.
// Otherwise (the default) it syncs from the official MCP Registry plus any
// configured custom sources (WithCustomSources) — see SyncSources.
func (f *FeedFetcher) Sync(ctx context.Context) error {
	if f.registryURL == "" && (f.official != nil || len(f.customSources) > 0) {
		return f.SyncSources(ctx)
	}

	entries, err := f.fetchNDJSON(ctx, f.registryURL, "")
	if err != nil {
		return err
	}
	return f.finishSync(entries)
}

// fetchNDJSON downloads and parses one newline-delimited-JSON feed. source
// tags each parsed entry's Source field for provenance (empty leaves the
// entry's own Source untouched, matching pre-#313 behavior for the
// single-URL path).
func (f *FeedFetcher) fetchNDJSON(ctx context.Context, feedURL, source string) ([]RegistryFeedEntry, error) {
	f.logger.Debug("syncing registry feed", "url", feedURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("registry feed: create request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry feed: network error: %w\nHint: check your connection or try again later with `leanproxy marketplace sync`", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry feed: registry returned HTTP %d\nHint: the registry may be temporarily unavailable; try again later with `leanproxy marketplace sync`", resp.StatusCode)
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
		if source != "" && entry.Source == "" {
			entry.Source = source
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("registry feed: read error: %w", err)
	}
	return entries, nil
}

// finishSync stores entries as the new cache, mirroring the change-detection
// and onSync-hook behavior Sync always had.
func (f *FeedFetcher) finishSync(entries []RegistryFeedEntry) error {
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
