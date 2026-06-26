package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SimilarityLimit bounds the number of "did you mean" suggestions returned by
// Install when the requested server id is not in the registry cache.
const SimilarityLimit = 5

// ServerSource is the minimal interface the installer needs from a registry
// cache. It is satisfied by *registry.FeedFetcher (which returns a
// *registry.FeedIndex) but defined here to avoid an import cycle with the
// registry package, which in turn depends on this package for the ServerConfig
// schema.
type ServerSource interface {
	// LookupCache returns the list of (name, transport, command, url,
	// tokens-per-turn) records available locally. The bool reports whether
	// any cache was present; an empty slice with bool == true means "cache
	// exists but is empty".
	LookupCache(ctx context.Context) (CacheSnapshot, error)
}

// CacheEntry is the subset of a registry feed record the installer cares
// about. Defining it here keeps the installer decoupled from the registry
// package's concrete types.
type CacheEntry struct {
	Name          string
	Transport     string
	Command       string
	Args          []string
	Env           map[string]string
	URL           string
	TokensPerTurn int64
}

// CacheSnapshot is the materialized view of the registry cache. It is
// returned by ServerSource.LookupCache.
type CacheSnapshot struct {
	Entries []CacheEntry
}

// ErrUnknownServer is returned by Install when the requested server id is not
// present in the local registry cache. It carries suggested similar names so
// callers can present a "did you mean" hint without re-running the search.
type ErrUnknownServer struct {
	ServerID  string
	Suggested []string
}

func (e *ErrUnknownServer) Error() string {
	if len(e.Suggested) == 0 {
		return fmt.Sprintf("unknown server: %q", e.ServerID)
	}
	return fmt.Sprintf("unknown server: %q (did you mean: %s)", e.ServerID, strings.Join(e.Suggested, ", "))
}

// IsUnknownServer reports whether err (or any wrapped error) is an
// ErrUnknownServer.
func IsUnknownServer(err error) bool {
	var target *ErrUnknownServer
	return errors.As(err, &target)
}

// ErrAlreadyInstalled is returned when the server name already exists in the
// user config and neither Force nor an interactive confirmation has been
// supplied.
type ErrAlreadyInstalled struct {
	ServerID string
}

func (e *ErrAlreadyInstalled) Error() string {
	return fmt.Sprintf("server already installed: %q (use --force to replace)", e.ServerID)
}

// IsAlreadyInstalled reports whether err (or any wrapped error) is an
// ErrAlreadyInstalled.
func IsAlreadyInstalled(err error) bool {
	var target *ErrAlreadyInstalled
	return errors.As(err, &target)
}

// LifecycleStopper is the narrow surface the installer needs from a server
// lifecycle manager. The concrete *registry.LifecycleManager satisfies it via
// its Stop method, but the installer accepts the interface so callers can
// pass a fake in tests.
type LifecycleStopper interface {
	Stop(ctx context.Context, id string) error
}

// InstallOptions controls Install behavior. The zero value performs an
// additive install with no graceful stop and no lifecycle manager.
type InstallOptions struct {
	// Force, when true, allows overwriting an existing server definition.
	Force bool

	// StopExisting, when true, asks the stopper to gracefully stop any
	// running instance that shares the target name before the new definition
	// is written.
	StopExisting bool

	// GracefulTimeout is the budget for the graceful stop before the caller
	// proceeds with the install regardless. Zero means use the stop call's
	// default context.
	GracefulTimeout time.Duration

	// Stopper, when set, is invoked to stop existing instances when
	// StopExisting is true. It may be nil for offline (config-only) installs.
	Stopper LifecycleStopper

	// Logger is used for operational logs. Defaults to slog.Default().
	Logger *slog.Logger

	// DryRun, when true, prevents any filesystem writes and any stopper
	// calls. The result still reflects what would have happened.
	DryRun bool
}

// InstallResult captures the outcome of a successful Install call. It is
// always non-nil when err is nil.
type InstallResult struct {
	ServerID        string
	ServerName      string
	Transport       string
	Replaced        bool
	Stopped         bool
	ConfigPath      string
	DryRun          bool
	EstimatedTools  int
	SnapshotSummary string
}

// Installer is the top-level facade used by cmd/add. It composes a cache
// source and a config path.
type Installer struct {
	Source     ServerSource
	ConfigPath string
	Logger     *slog.Logger
}

// NewInstaller wires an Installer with the given cache source and config
// path. Either may be overridden after construction by setting the fields
// directly.
func NewInstaller(source ServerSource, configPath string, logger *slog.Logger) *Installer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Installer{Source: source, ConfigPath: configPath, Logger: logger}
}

// Resolve returns the registry entry for serverID (case-insensitive). If no
// exact match is found, it returns an *ErrUnknownServer populated with up to
// SimilarityLimit suggestions ranked by edit distance.
//
// Resolve never triggers a Sync; callers are expected to have a fresh cache.
func (i *Installer) Resolve(ctx context.Context, serverID string) (CacheEntry, error) {
	if serverID == "" {
		return CacheEntry{}, &ErrUnknownServer{ServerID: serverID}
	}

	snap, err := i.Source.LookupCache(ctx)
	if err != nil {
		return CacheEntry{}, fmt.Errorf("installer: load cache: %w", err)
	}

	want := strings.ToLower(serverID)
	for _, e := range snap.Entries {
		if strings.ToLower(e.Name) == want {
			return e, nil
		}
	}

	suggested := SuggestSimilar(serverID, snap.Entries, SimilarityLimit)
	return CacheEntry{}, &ErrUnknownServer{ServerID: serverID, Suggested: suggested}
}

// Install builds a ServerConfig from the cache entry, merges it into the user
// config at i.ConfigPath (writing through MarshalConfig), and — if
// StopExisting is true — gracefully stops any running instance that shares the
// target name via the provided Stopper.
//
// The returned result is always non-nil when err is nil.
func (i *Installer) Install(ctx context.Context, entry CacheEntry, opts InstallOptions) (*InstallResult, error) {
	if entry.Name == "" {
		return nil, fmt.Errorf("installer: cache entry has empty Name")
	}

	logger := opts.Logger
	if logger == nil {
		logger = i.Logger
	}

	sc, err := buildServerConfig(entry)
	if err != nil {
		return nil, fmt.Errorf("installer: build config for %q: %w", entry.Name, err)
	}

	path := i.ConfigPath
	if path == "" {
		path = defaultUserConfigPath()
	}

	cfg, err := LoadConfig(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("installer: load config %s: %w", path, err)
	}
	if cfg == nil {
		cfg = &Config{Version: "1.0", Servers: []*ServerConfig{}}
	}

	result := &InstallResult{
		ServerID:   sc.Name,
		ServerName: sc.Name,
		ConfigPath: path,
	}

	existingIdx := findServerIndex(cfg.Servers, sc.Name)
	replaced := existingIdx >= 0
	if replaced && !opts.Force {
		return nil, &ErrAlreadyInstalled{ServerID: sc.Name}
	}
	result.Replaced = replaced
	result.Transport = string(sc.Transport)

	if opts.StopExisting && replaced && opts.Stopper != nil {
		if !opts.DryRun {
			stopCtx := ctx
			if opts.GracefulTimeout > 0 {
				var cancel context.CancelFunc
				stopCtx, cancel = context.WithTimeout(ctx, opts.GracefulTimeout)
				defer cancel()
			}
			if err := opts.Stopper.Stop(stopCtx, sc.Name); err != nil {
				logger.Warn("installer: graceful stop failed; continuing with install",
					"server", sc.Name, "error", err)
			} else {
				result.Stopped = true
			}
		} else {
			result.Stopped = true
		}
	}

	if replaced {
		cfg.Servers[existingIdx] = sc
	} else {
		cfg.Servers = append(cfg.Servers, sc)
	}

	if opts.DryRun {
		result.DryRun = true
		return result, nil
	}

	if err := SaveConfig(path, cfg); err != nil {
		return nil, fmt.Errorf("installer: save config: %w", err)
	}

	logger.Info("server installed from registry",
		"server", sc.Name,
		"transport", sc.Transport,
		"replaced", result.Replaced,
		"stopped", result.Stopped,
		"path", path)

	return result, nil
}

// SaveConfig writes cfg to path with 0600 permissions, creating the parent
// directory if needed. The write is atomic: a temp file in the same
// directory is created, fsynced, then renamed over the target.
func SaveConfig(path string, cfg *Config) error {
	if path == "" {
		return fmt.Errorf("installer: empty config path")
	}
	data, err := MarshalConfig(cfg)
	if err != nil {
		return fmt.Errorf("installer: marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("installer: create config dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".leanproxy-*.yaml.tmp") // #nosec G304 -- path provided by caller via opts
	if err != nil {
		return fmt.Errorf("installer: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("installer: write temp file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		cleanup()
		return fmt.Errorf("installer: chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("installer: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("installer: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("installer: rename temp file: %w", err)
	}
	return nil
}

// defaultUserConfigPath returns the standard user config path used when no
// override is supplied via the Installer. It mirrors userConfigPath in cmd/.
func defaultUserConfigPath() string {
	if p := os.Getenv("LEANPROXY_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".config", "leanproxy_servers.yaml")
	}
	return filepath.Join(home, ".config", "leanproxy_servers.yaml")
}

// buildServerConfig converts a CacheEntry into a ServerConfig that the rest of
// the system can persist, validate, and start. Stdio is preferred when a
// Command is present; otherwise HTTP is used and the URL is promoted into
// http.url.
func buildServerConfig(entry CacheEntry) (*ServerConfig, error) {
	transport, err := normaliseTransport(entry.Transport)
	if err != nil {
		return nil, err
	}

	enabled := true
	sc := &ServerConfig{
		Name:           entry.Name,
		Enabled:        &enabled,
		Transport:      transport,
		Timeout:        "30s",
		ConnectTimeout: "10s",
	}

	switch transport {
	case TransportStdio:
		if entry.Command == "" {
			return nil, fmt.Errorf("registry entry %q has no Command for stdio transport", entry.Name)
		}
		sc.Stdio = &StdioConfig{
			Command: entry.Command,
			Args:    append([]string(nil), entry.Args...),
			Env:     flattenEnv(entry.Env),
		}
	case TransportHTTP, TransportSSE:
		if entry.URL == "" {
			return nil, fmt.Errorf("registry entry %q has no URL for %s transport", entry.Name, transport)
		}
		sc.HTTP = &HTTPConfig{
			URL:     entry.URL,
			Headers: cloneHeaders(entry.Env),
		}
	default:
		return nil, fmt.Errorf("registry entry %q has unsupported transport %q", entry.Name, transport)
	}

	return sc, nil
}

func normaliseTransport(raw string) (TransportType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "stdio":
		return TransportStdio, nil
	case "http", "streamable-http", "streamable_http":
		return TransportHTTP, nil
	case "sse":
		return TransportSSE, nil
	default:
		return "", fmt.Errorf("unsupported transport %q", raw)
	}
}

func flattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, env[k]))
	}
	return out
}

// cloneHeaders duplicates env as HTTP headers, picking keys prefixed with
// header_ (case-insensitive) and stripping the prefix. Returns nil when no
// header_* keys are present to avoid emitting an empty `headers: {}` block in
// YAML.
func cloneHeaders(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "header_") {
			out[strings.TrimPrefix(lower, "header_")] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func findServerIndex(servers []*ServerConfig, name string) int {
	for i, s := range servers {
		if s == nil {
			continue
		}
		if s.Name == name {
			return i
		}
	}
	return -1
}

// SuggestSimilar ranks entries by Levenshtein distance to needle and returns
// the top-n closest names. Identical matches are excluded (Resolve handles
// the exact case before calling this helper).
func SuggestSimilar(needle string, entries []CacheEntry, limit int) []string {
	type scored struct {
		name  string
		score int
	}
	want := strings.ToLower(needle)
	scoredEntries := make([]scored, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		low := strings.ToLower(e.Name)
		if low == want {
			continue
		}
		scoredEntries = append(scoredEntries, scored{
			name:  e.Name,
			score: levenshtein(want, low),
		})
	}
	sort.SliceStable(scoredEntries, func(a, b int) bool {
		return scoredEntries[a].score < scoredEntries[b].score
	})
	if limit > 0 && len(scoredEntries) > limit {
		scoredEntries = scoredEntries[:limit]
	}
	out := make([]string, 0, len(scoredEntries))
	for _, s := range scoredEntries {
		out = append(out, s.name)
	}
	return out
}

// levenshtein computes the edit distance between a and b using the classic
// dynamic-programming algorithm with O(min(|a|,|b|)) memory. Both inputs are
// expected to be already lower-cased by the caller.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	if len(a) < len(b) {
		a, b = b, a
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for i := 0; i <= len(b); i++ {
		prev[i] = i
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
