package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp/responsecache"
	"github.com/mmornati/leanproxy-mcp/pkg/telemetry"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
	"github.com/mmornati/leanproxy-mcp/pkg/toolsearch"
	"github.com/mmornati/leanproxy-mcp/pkg/utils"
)

// ErrConfigNotFound is returned by LoadConfig when the config file does not
// exist on disk. Callers that need to distinguish "operator pointed at a
// missing file" from "operator pointed at an unreadable / unparseable file"
// (or a file that exists but is empty) can use errors.Is to detect this
// sentinel. Callers that want to fall back to defaults should treat this as a
// non-error and either branch on the sentinel explicitly or rely on the
// nil-config / nil-error return contract.
var ErrConfigNotFound = errors.New("config file not found")

type CacheSettings struct {
	Enabled  bool   `yaml:"enabled"`
	MaxSize  int    `yaml:"max_size"`
	TTL      string `yaml:"ttl"`
	TTLValue time.Duration
}

type SummarizeSettings struct {
	Enabled   bool   `yaml:"enabled"`
	MaxTokens int    `yaml:"max_tokens"`
	Strategy  string `yaml:"strategy"`
}

type SQLiteVectorConfig struct {
	Path string `yaml:"path"`
}

type QdrantVectorConfig struct {
	URL        string `yaml:"url"`
	APIKey     string `yaml:"api_key"`
	APIKeyEnv  string `yaml:"api_key_env"`
	Collection string `yaml:"collection"`
}

type PineconeVectorConfig struct {
	Index     string `yaml:"index"`
	APIKeyEnv string `yaml:"api_key_env"`
}

type VectorStoreConfig struct {
	Backend   string                `yaml:"backend"`
	Dimension int                   `yaml:"dimension"`
	SQLite    *SQLiteVectorConfig   `yaml:"sqlite,omitempty"`
	Qdrant    *QdrantVectorConfig   `yaml:"qdrant,omitempty"`
	Pinecone  *PineconeVectorConfig `yaml:"pinecone,omitempty"`
}

type CacheConfig struct {
	VectorStore *VectorStoreConfig `yaml:"vector_store,omitempty"`
}

type StdioConfig struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	// Env holds explicit "KEY=VALUE" entries added on top of the minimal
	// base environment (#311). A value may reference "${VAR}" to expand a
	// variable from the proxy's own (parent) environment; a reference to a
	// variable that is not set there fails the server's start with an
	// error naming the server and the variable.
	Env []string `yaml:"env"`
	// EnvPassthrough lists parent environment variable names to copy
	// through to the child as-is, in addition to the minimal base
	// environment and Env (#311). Only names — never values — are ever
	// logged for these.
	EnvPassthrough []string `yaml:"env_passthrough"`
	// InheritEnv restores the pre-#311 behavior of passing the proxy's
	// entire environment to the child. It logs one warning per server at
	// start and exists only to ease migration; least-privilege (the
	// default, false) should be preferred.
	InheritEnv bool   `yaml:"inherit_env"`
	CWD        string `yaml:"cwd"`
}

type HTTPConfig struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Auth    *AuthConfig       `yaml:"auth,omitempty"`
}

type AuthConfig struct {
	Type         string   `yaml:"type"` // bearer, oauth2
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes"`
	TokenURL     string   `yaml:"token_url"` // optional, for bearer token exchange
}

type ServerConfig struct {
	Name                string             `yaml:"name"`
	Enabled             *bool              `yaml:"enabled"`
	Transport           TransportType      `yaml:"transport"`
	ComplexityTier      string             `yaml:"complexity_tier,omitempty"`
	Stdio               *StdioConfig       `yaml:"stdio,omitempty"`
	HTTP                *HTTPConfig        `yaml:"http,omitempty"`
	Timeout             string             `yaml:"timeout"`
	TimeoutValue        time.Duration      `yaml:"-"`
	ConnectTimeout      string             `yaml:"connect_timeout"`
	ConnectTimeoutValue time.Duration      `yaml:"-"`
	IdleTimeout         string             `yaml:"idle_timeout"`
	IdleTimeoutValue    time.Duration      `yaml:"-"`
	CacheSettings       *CacheSettings     `yaml:"cache_settings,omitempty"`
	SummarizeSettings   *SummarizeSettings `yaml:"summarize_settings,omitempty"`
	RateLimit           *RateLimitConfig   `yaml:"rate_limit,omitempty"`
	// MaxInFlight caps how many requests the proxy multiplexes concurrently
	// over one stdio server's pipe. Callers beyond the cap wait for a free
	// slot (bounded by their own timeout) instead of being rejected. 0 or
	// absent means the default (32). Only used by stdio servers.
	MaxInFlight int `yaml:"max_in_flight,omitempty"`
	// MaxResponseBytes caps the size of one JSON-RPC message (one stdout
	// line) read from a stdio server. A larger response fails only the call
	// it answers; the server keeps working. 0 or absent means the default
	// (64 MiB). Only used by stdio servers.
	MaxResponseBytes int `yaml:"max_response_bytes,omitempty"`
	// AllowSampling lets this server send sampling/createMessage requests,
	// which the proxy relays to the client (issue #308). Sampling makes the
	// client's LLM generate text on the server's behalf, spending the
	// user's tokens, so it is off by default: the request is refused with
	// -32601 and the sampling capability is not declared to the server.
	// Every relayed sampling request is logged.
	AllowSampling bool `yaml:"allow_sampling,omitempty"`
	// Roots, when set, answers this server's roots/list requests from this
	// static list instead of relaying them to the client (issue #308).
	// Each uri must be a file:// URI.
	Roots []RootConfig `yaml:"roots,omitempty"`
	// InstalledFrom records provenance when this server was written by
	// `leanproxy add`/`marketplace update` (issue #313): which registry
	// source it came from, its name there, the exact version pinned, and
	// when. Absent for hand-written or migrated entries.
	InstalledFrom *InstalledFromConfig `yaml:"installed_from,omitempty"`
}

// InstalledFromConfig is servers[].installed_from.
type InstalledFromConfig struct {
	// Registry is the source name: "official" for the MCP Registry API, or
	// the configured name of a custom NDJSON source (registry.sources).
	Registry string `yaml:"registry"`
	// Name is the server's name as known to that registry (may differ
	// from the local server Name after a rename).
	Name string `yaml:"name"`
	// Version is the exact version pinned at install time.
	Version string `yaml:"version,omitempty"`
	// InstalledAt is when the install (or last `marketplace update`) ran,
	// RFC 3339.
	InstalledAt string `yaml:"installed_at"`
}

// RootConfig is one static root of servers[].roots: a file:// URI and an
// optional display name.
type RootConfig struct {
	URI  string `yaml:"uri" json:"uri"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// RateLimitConfig is an optional, per-server request rate limit applied to
// requests forwarded to that server. It is unset by default, meaning no
// limit: local stdio servers in particular don't need one. When set,
// RequestsPerSecond of 0 (or an absent block) also means unlimited; a
// positive value enables waiting-based limiting (see pkg/pool).
type RateLimitConfig struct {
	// RequestsPerSecond is the sustained rate of requests allowed per
	// second. 0 or absent means unlimited.
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	// Burst is the maximum number of requests allowed to proceed
	// immediately before the sustained rate applies. Defaults to
	// RequestsPerSecond (rounded up, minimum 1) when RequestsPerSecond > 0
	// and Burst is not set.
	Burst int `yaml:"burst"`
}

// Validate returns an error if the rate limit configuration has negative
// values. A nil receiver is valid (unlimited).
func (c *RateLimitConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.RequestsPerSecond < 0 {
		return fmt.Errorf("requests_per_second must be >= 0, got %v", c.RequestsPerSecond)
	}
	if c.Burst < 0 {
		return fmt.Errorf("burst must be >= 0, got %d", c.Burst)
	}
	return nil
}

type ReconnectConfig struct {
	Enabled             *bool         `yaml:"enabled"`
	HealthInterval      string        `yaml:"health_check_interval"`
	HealthIntervalValue time.Duration `yaml:"-"`
	MaxFailures         int           `yaml:"health_check_failures"`
	MaxRestartAttempts  int           `yaml:"max_restart_attempts"`
	RestartBackoff      string        `yaml:"restart_backoff"`
	RestartBackoffValue time.Duration `yaml:"-"`
	StableWindow        string        `yaml:"stable_window"`
	StableWindowValue   time.Duration `yaml:"-"`
}

type ResolvedReconnect struct {
	Enabled            bool
	HealthInterval     time.Duration
	MaxFailures        int
	MaxRestartAttempts int
	RestartBackoff     time.Duration
	StableWindow       time.Duration
}

func (c *Config) EffectiveReconnect() ResolvedReconnect {
	r := ResolvedReconnect{
		Enabled:            true,
		HealthInterval:     30 * time.Second,
		MaxFailures:        3,
		MaxRestartAttempts: 5,
		RestartBackoff:     time.Second,
		StableWindow:       2 * time.Minute,
	}
	if c == nil || c.Reconnect == nil {
		return r
	}
	rc := c.Reconnect
	if rc.Enabled != nil {
		r.Enabled = *rc.Enabled
	}
	// health_check_interval is honored whenever explicitly set — including
	// "0", which disables the proactive health check as documented. The
	// string field (not the parsed value) is what distinguishes "unset"
	// (fall back to the 30s default) from an explicit 0.
	if rc.HealthInterval != "" {
		r.HealthInterval = rc.HealthIntervalValue
	}
	if rc.MaxFailures > 0 {
		r.MaxFailures = rc.MaxFailures
	}
	if rc.MaxRestartAttempts > 0 {
		r.MaxRestartAttempts = rc.MaxRestartAttempts
	}
	if rc.RestartBackoffValue > 0 {
		r.RestartBackoff = rc.RestartBackoffValue
	}
	if rc.StableWindowValue > 0 {
		r.StableWindow = rc.StableWindowValue
	}
	return r
}

type Config struct {
	Version       string                `yaml:"version"`
	Servers       []*ServerConfig       `yaml:"servers"`
	Reconnect     *ReconnectConfig      `yaml:"reconnect,omitempty"`
	Cache         *CacheConfig          `yaml:"cache,omitempty"`
	Injection     *injection.Config     `yaml:"injection,omitempty"`
	Bouncer       *bouncer.Config       `yaml:"bouncer,omitempty"`
	ResponseCache *responsecache.Config `yaml:"response_cache,omitempty"`
	// ToolSearch configures the search_tools index (synonyms, optional
	// hybrid embedding ranking). Absent means BM25 with the default
	// synonyms.
	ToolSearch *toolsearch.Config `yaml:"tool_search,omitempty"`
	// Server holds settings for the proxy's own MCP front end (the
	// `server run --stdio` loop the IDE talks to), as opposed to the
	// upstream servers listed under Servers.
	Server *FrontendConfig `yaml:"server,omitempty"`
	// Telemetry configures the optional OpenTelemetry traces/metrics
	// exporter (issue #317). Off by default; also enabled by the standard
	// OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_PROTOCOL env vars,
	// which take precedence over this block (see pkg/telemetry.Resolve).
	Telemetry *telemetry.Config `yaml:"telemetry,omitempty"`
	// Security holds the upstream-trust settings (issue #310: tool
	// pinning). Absent means the defaults (tool pinning in warn mode).
	Security *SecurityConfig `yaml:"security,omitempty"`
	// Registry configures marketplace server sources (issue #313). The
	// official MCP Registry (registry.modelcontextprotocol.io) is always
	// available as the default source and needs no entry here; Sources
	// lists additional opt-in custom NDJSON feeds.
	Registry *RegistrySettings `yaml:"registry,omitempty"`
}

// RegistrySettings is the `registry:` block.
type RegistrySettings struct {
	// Sources are opt-in custom NDJSON feed URLs, synced in addition to
	// the official MCP Registry by `marketplace sync`. Each must have a
	// unique, non-empty Name (used for provenance display and as the
	// InstalledFrom.Registry value for servers installed from it) and a
	// non-empty http(s) URL.
	Sources []RegistrySourceConfig `yaml:"sources,omitempty"`
}

// RegistrySourceConfig is one entry of registry.sources.
type RegistrySourceConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Validate checks that every source has a name and an http(s) URL, and that
// names are unique. A nil receiver is valid (no custom sources).
func (r *RegistrySettings) Validate() error {
	if r == nil {
		return nil
	}
	seen := make(map[string]bool, len(r.Sources))
	for i, s := range r.Sources {
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("registry.sources[%d]: name is required", i)
		}
		if seen[s.Name] {
			return fmt.Errorf("registry.sources[%d]: duplicate name %q", i, s.Name)
		}
		seen[s.Name] = true
		if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			return fmt.Errorf("registry.sources[%d] (%s): url must start with http:// or https://, got %q", i, s.Name, s.URL)
		}
	}
	return nil
}

// SecurityConfig is the `security:` block.
type SecurityConfig struct {
	// ToolPinning pins upstream tool definitions and reports or blocks
	// drift (off | warn | block; warn by default).
	ToolPinning *toolpin.Config `yaml:"tool_pinning,omitempty"`
}

// ToolPinningConfig returns security.tool_pinning (nil: the defaults).
func (c *Config) ToolPinningConfig() *toolpin.Config {
	if c == nil || c.Security == nil {
		return nil
	}
	return c.Security.ToolPinning
}

// DefaultMaxConcurrentRequests is the number of client requests the stdio
// front end processes at once when server.max_concurrent_requests is unset.
const DefaultMaxConcurrentRequests = 64

// FrontendConfig is the `server:` block. Unknown keys in the block (e.g. the
// legacy host/port documented for `serve`) are ignored.
type FrontendConfig struct {
	// MaxConcurrentRequests caps how many client requests `server run
	// --stdio` handles in parallel (and, for `serve`, how many requests one
	// TCP connection runs in parallel). When the cap is reached the reader
	// waits for a free slot; it never rejects. 0 or absent means
	// DefaultMaxConcurrentRequests.
	MaxConcurrentRequests int `yaml:"max_concurrent_requests,omitempty"`
	// MaxLineBytes caps the size of one newline-delimited JSON-RPC message
	// read by the `serve` TCP listener. A longer line gets an error reply
	// and the connection is closed. 0 or absent means
	// DefaultMaxLineBytes.
	MaxLineBytes int `yaml:"max_line_bytes,omitempty"`
	// MaxConnections caps how many client connections the `serve` TCP
	// listener accepts at once; extra connections are closed right away.
	// 0 or absent means DefaultMaxConnections.
	MaxConnections int `yaml:"max_connections,omitempty"`
}

// DefaultMaxLineBytes is the largest JSON-RPC message (one line) the `serve`
// listener reads when server.max_line_bytes is unset: 64 MiB.
const DefaultMaxLineBytes = 64 << 20

// DefaultMaxConnections is how many client connections the `serve` listener
// accepts at once when server.max_connections is unset.
const DefaultMaxConnections = 32

// Validate rejects negative values. A nil receiver is valid (defaults).
func (c *FrontendConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.MaxConcurrentRequests < 0 {
		return fmt.Errorf("server.max_concurrent_requests must be >= 0, got %d", c.MaxConcurrentRequests)
	}
	if c.MaxLineBytes < 0 {
		return fmt.Errorf("server.max_line_bytes must be >= 0, got %d", c.MaxLineBytes)
	}
	if c.MaxConnections < 0 {
		return fmt.Errorf("server.max_connections must be >= 0, got %d", c.MaxConnections)
	}
	return nil
}

// EffectiveMaxLineBytes returns server.max_line_bytes, or
// DefaultMaxLineBytes when it is unset or zero.
func (c *Config) EffectiveMaxLineBytes() int {
	if c == nil || c.Server == nil || c.Server.MaxLineBytes <= 0 {
		return DefaultMaxLineBytes
	}
	return c.Server.MaxLineBytes
}

// EffectiveMaxConnections returns server.max_connections, or
// DefaultMaxConnections when it is unset or zero.
func (c *Config) EffectiveMaxConnections() int {
	if c == nil || c.Server == nil || c.Server.MaxConnections <= 0 {
		return DefaultMaxConnections
	}
	return c.Server.MaxConnections
}

// EffectiveMaxConcurrentRequests returns server.max_concurrent_requests, or
// DefaultMaxConcurrentRequests when it is unset or zero.
func (c *Config) EffectiveMaxConcurrentRequests() int {
	if c == nil || c.Server == nil || c.Server.MaxConcurrentRequests <= 0 {
		return DefaultMaxConcurrentRequests
	}
	return c.Server.MaxConcurrentRequests
}

func (c *ServerConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if c.Transport == "" {
		return fmt.Errorf("server %s: transport type is required", c.Name)
	}
	switch c.Transport {
	case TransportStdio:
		if c.Stdio == nil || c.Stdio.Command == "" {
			return fmt.Errorf("server %s: command is required for stdio transport", c.Name)
		}
	case TransportHTTP, TransportSSE:
		if c.HTTP == nil || c.HTTP.URL == "" {
			return fmt.Errorf("server %s: url is required for %s transport", c.Name, c.Transport)
		}
	default:
		return fmt.Errorf("server %s: invalid transport type %q (must be stdio, http, or sse)", c.Name, c.Transport)
	}
	if err := c.RateLimit.Validate(); err != nil {
		return fmt.Errorf("server %s: rate_limit: %w", c.Name, err)
	}
	if c.MaxInFlight < 0 {
		return fmt.Errorf("server %s: max_in_flight must be >= 0, got %d", c.Name, c.MaxInFlight)
	}
	if c.MaxResponseBytes < 0 {
		return fmt.Errorf("server %s: max_response_bytes must be >= 0, got %d", c.Name, c.MaxResponseBytes)
	}
	for i, r := range c.Roots {
		if !strings.HasPrefix(r.URI, "file://") {
			return fmt.Errorf("server %s: roots[%d].uri must be a file:// URI, got %q", c.Name, i, r.URI)
		}
	}
	return nil
}

func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if c.Servers != nil {
		for _, server := range c.Servers {
			if err := server.Validate(); err != nil {
				return err
			}
		}
	}
	if c.Bouncer != nil {
		if err := validateBouncerPatterns(c.Bouncer); err != nil {
			return err
		}
	}
	if err := c.Server.Validate(); err != nil {
		return err
	}
	if err := c.ToolSearch.Validate(); err != nil {
		return err
	}
	if err := c.Injection.Validate(); err != nil {
		return err
	}
	if err := c.ToolPinningConfig().Validate(); err != nil {
		return err
	}
	if err := c.Registry.Validate(); err != nil {
		return err
	}
	return nil
}

// validateBouncerPatterns returns the first dangerous-pattern violation found
// in the bouncer block. Patterns here that fail SafeCompile are rejected at
// load time so the redactor never silently falls back to built-ins only — a
// behavior that previously let ReDoS-prone custom patterns ship to production
// without any startup signal.
func validateBouncerPatterns(cfg *bouncer.Config) error {
	if cfg == nil {
		return nil
	}
	for _, p := range cfg.AllPatternDefs() {
		if err := bouncer.ValidatePattern(p.Pattern); err != nil {
			name := p.Name
			if name == "" {
				name = "<unnamed>"
			}
			return fmt.Errorf("bouncer pattern %q: %w", name, err)
		}
	}
	return nil
}

func LoadConfig(ctx context.Context, path string) (*Config, error) {
	baseDir := filepath.Dir(filepath.Clean(path))
	if err := utils.ValidatePath(path, baseDir); err != nil {
		return nil, fmt.Errorf("path validation: %w", err)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path validated via ValidatePath above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("read config file %q: %w", path, ErrConfigNotFound)
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	warnDeprecatedConfigKeys(data)

	if err := cfg.ResponseCache.Normalize(); err != nil {
		return nil, fmt.Errorf("response_cache: %w", err)
	}

	for _, server := range cfg.Servers {
		if server.Timeout != "" {
			d, err := time.ParseDuration(server.Timeout)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid timeout duration: %w", server.Name, err)
			}
			server.TimeoutValue = d
		} else {
			server.TimeoutValue = 30 * time.Second
		}

		if server.ConnectTimeout != "" {
			d, err := time.ParseDuration(server.ConnectTimeout)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid connect_timeout duration: %w", server.Name, err)
			}
			server.ConnectTimeoutValue = d
		} else {
			server.ConnectTimeoutValue = 10 * time.Second
		}

		if server.IdleTimeout != "" {
			d, err := time.ParseDuration(server.IdleTimeout)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid idle_timeout duration: %w", server.Name, err)
			}
			server.IdleTimeoutValue = d
		} else {
			server.IdleTimeoutValue = 30 * time.Minute
		}

		if server.CacheSettings != nil && server.CacheSettings.TTL != "" {
			d, err := time.ParseDuration(server.CacheSettings.TTL)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid cache TTL: %w", server.Name, err)
			}
			server.CacheSettings.TTLValue = d
		}

		if server.Enabled == nil {
			server.Enabled = ptr(true)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	if cfg.Reconnect != nil {
		rc := cfg.Reconnect
		if rc.HealthInterval != "" {
			d, err := time.ParseDuration(rc.HealthInterval)
			if err != nil {
				return nil, fmt.Errorf("invalid reconnect health_check_interval duration: %w", err)
			}
			if d < 0 {
				return nil, fmt.Errorf("invalid reconnect health_check_interval: must be >= 0 (0 disables the health check)")
			}
			rc.HealthIntervalValue = d
		} else {
			rc.HealthIntervalValue = 30 * time.Second
		}

		if rc.RestartBackoff != "" {
			d, err := time.ParseDuration(rc.RestartBackoff)
			if err != nil {
				return nil, fmt.Errorf("invalid reconnect restart_backoff duration: %w", err)
			}
			if d < 0 {
				return nil, fmt.Errorf("invalid reconnect restart_backoff: must be >= 0")
			}
			rc.RestartBackoffValue = d
		} else {
			rc.RestartBackoffValue = time.Second
		}

		if rc.StableWindow != "" {
			d, err := time.ParseDuration(rc.StableWindow)
			if err != nil {
				return nil, fmt.Errorf("invalid reconnect stable_window duration: %w", err)
			}
			if d < 0 {
				return nil, fmt.Errorf("invalid reconnect stable_window: must be >= 0")
			}
			rc.StableWindowValue = d
		} else {
			rc.StableWindowValue = 2 * time.Minute
		}

		if rc.MaxFailures <= 0 {
			rc.MaxFailures = 3
		}
		if rc.MaxRestartAttempts <= 0 {
			rc.MaxRestartAttempts = 5
		}
	}

	if cfg.Cache != nil && cfg.Cache.VectorStore != nil {
		vs := cfg.Cache.VectorStore
		if vs.Backend == "" {
			vs.Backend = "sqlite-vec"
		}
		if vs.Dimension <= 0 {
			vs.Dimension = 1536
		}
		if vs.SQLite != nil && vs.SQLite.Path == "" {
			home, err := os.UserHomeDir()
			if err == nil {
				vs.SQLite.Path = filepath.Join(home, ".leanproxy", "cache", "vectors.db")
			}
		}
		if vs.Qdrant != nil && vs.Qdrant.Collection == "" {
			vs.Qdrant.Collection = "leanproxy_cache"
		}
		if vs.Pinecone != nil && vs.Pinecone.APIKeyEnv == "" {
			vs.Pinecone.APIKeyEnv = "PINECONE_API_KEY"
		}
	}

	return &cfg, nil
}

// warnDeprecatedConfigKeys detects config keys that leanproxy.yaml used to
// support but no longer acts on, and logs one warning per key found so
// existing configs keep loading instead of failing outright (issue #303).
// It re-parses the raw YAML into an untyped map because the typed Config
// struct no longer has fields for these keys, so yaml.Unmarshal would
// otherwise drop them silently.
func warnDeprecatedConfigKeys(data []byte) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}

	if _, ok := raw["federation"]; ok {
		slog.Default().Warn(`config key "federation" is no longer supported and is ignored`)
	}

	if opt, ok := raw["optimization"].(map[string]interface{}); ok {
		if _, ok := opt["lazy_loading"]; ok {
			slog.Default().Warn(`config key "optimization.lazy_loading" is no longer supported and is ignored`)
		}
	}
}

func ptr(b bool) *bool { return &b }

func MarshalConfig(cfg *Config) ([]byte, error) {
	return yaml.Marshal(cfg)
}
