package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/internal/version"
	"github.com/mmornati/leanproxy-mcp/pkg/dashboard"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
	"gopkg.in/yaml.v3"
)

// configWatchInterval is how often a running proxy checks whether the
// config file changed.
const configWatchInterval = 2 * time.Second

// serverNamePattern is what a server name added from the dashboard or the
// CLI may look like: it ends up in tool names and in URLs.
var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// serverManager owns the set of upstream servers of a running proxy. It
// applies the `servers:` list of the config file to the pool, at startup
// and again whenever the file changes (a hot reload: `server add` from
// another terminal, an edit, or the dashboard), and it is the dashboard's
// dashboard.Admin.
//
// Only the server list is hot-reloaded. Every other block (policy,
// bouncer, exposure, response, ...) is read once at startup.
type serverManager struct {
	configPath string
	pool       *pool.UnifiedPool
	handler    *mcp.Handler
	calls      *mcp.CallLog
	pinner     *toolpin.Pinner
	frontend   string
	startedAt  time.Time
	logger     *slog.Logger

	// reloadMu serializes reloads and config writes.
	reloadMu sync.Mutex

	mu sync.Mutex
	// running maps the name of each server in the pool to the fingerprint
	// of the config it was started with.
	running map[string]string
	// configured is the latest server list read from the file, including
	// disabled entries.
	configured      []*migrate.ServerConfig
	lastReloadAt    time.Time
	lastReloadError string
	stamp           fileStamp
}

// fileStamp identifies a version of the config file cheaply.
type fileStamp struct {
	modTime time.Time
	size    int64
}

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}
}

func newServerManager(configPath string, p *pool.UnifiedPool, logger *slog.Logger) *serverManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &serverManager{
		configPath: configPath,
		pool:       p,
		startedAt:  time.Now(),
		logger:     logger,
		running:    make(map[string]string),
	}
}

// serverFingerprint identifies a server's configuration: a server whose
// fingerprint changed is restarted by a reload.
func serverFingerprint(srv *migrate.ServerConfig) string {
	data, err := yaml.Marshal(srv)
	if err != nil {
		return fmt.Sprintf("%p", srv)
	}
	return string(data)
}

func serverEnabled(srv *migrate.ServerConfig) bool {
	return srv.Enabled == nil || *srv.Enabled
}

// apply makes the pool match servers: it stops the servers that left the
// list, were disabled or changed, and starts the new and changed ones. It
// returns how many servers it started and stopped.
func (m *serverManager) apply(ctx context.Context, servers []*migrate.ServerConfig) (started, stopped int) {
	want := make(map[string]*migrate.ServerConfig, len(servers))
	for _, srv := range servers {
		if srv != nil && serverEnabled(srv) {
			want[srv.Name] = srv
		}
	}

	m.mu.Lock()
	running := make(map[string]string, len(m.running))
	for name, fp := range m.running {
		running[name] = fp
	}
	m.mu.Unlock()

	for name, fp := range running {
		srv, keep := want[name]
		if keep && serverFingerprint(srv) == fp {
			continue
		}
		if err := m.pool.RemoveServer(name); err != nil {
			m.logger.Warn("reload: failed to stop server", "name", name, "error", err)
		}
		if m.handler != nil {
			m.handler.ServerRemoved(name)
		}
		m.mu.Lock()
		delete(m.running, name)
		m.mu.Unlock()
		stopped++
		m.logger.Info("server stopped", "name", name, "reason", stopReason(keep))
	}

	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		srv := want[name]
		m.mu.Lock()
		_, already := m.running[name]
		m.mu.Unlock()
		if already {
			continue
		}
		if err := m.pool.StartServer(ctx, srv); err != nil {
			m.logger.Warn("failed to start server", "name", name, "transport", srv.Transport, "error", err)
			continue
		}
		m.mu.Lock()
		m.running[name] = serverFingerprint(srv)
		m.mu.Unlock()
		started++
		m.logger.Info("server started", "name", name, "transport", srv.Transport)
		if m.handler != nil {
			m.handler.SetTimeout(name, srv.TimeoutValue)
			m.handler.ConfigureRelay([]*migrate.ServerConfig{srv})
			m.handler.ServerAdded(name)
		}
	}
	return started, stopped
}

func stopReason(stillConfigured bool) string {
	if stillConfigured {
		return "config changed"
	}
	return "removed or disabled"
}

// start applies the startup config. The handler does not exist yet, so its
// per-server settings are applied by attach.
func (m *serverManager) start(ctx context.Context, cfg *migrate.Config) int {
	m.mu.Lock()
	m.configured = cfg.Servers
	m.lastReloadAt = time.Now()
	m.stamp = stampOf(m.configPath)
	m.mu.Unlock()
	started, _ := m.apply(ctx, cfg.Servers)
	return started
}

// attach gives the manager the handler (and the views' data sources) once
// they exist. Servers started before it get their handler settings here.
func (m *serverManager) attach(handler *mcp.Handler, calls *mcp.CallLog, pinner *toolpin.Pinner, frontend string) {
	m.handler = handler
	m.calls = calls
	m.pinner = pinner
	m.frontend = frontend
}

// Reload re-reads the config file and applies its server list. A file that
// does not load leaves the running servers untouched.
func (m *serverManager) Reload(ctx context.Context) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	return m.reloadLocked(ctx)
}

func (m *serverManager) reloadLocked(ctx context.Context) error {
	stamp := stampOf(m.configPath)
	cfg, err := migrate.LoadConfig(ctx, m.configPath)
	if err != nil && errors.Is(err, migrate.ErrConfigNotFound) {
		cfg, err = &migrate.Config{}, nil
	}
	m.mu.Lock()
	m.stamp = stamp
	if err != nil {
		m.lastReloadError = err.Error()
		m.mu.Unlock()
		m.logger.Warn("config reload failed, keeping the running servers", "path", m.configPath, "error", err)
		return fmt.Errorf("reload %s: %w", m.configPath, err)
	}
	m.configured = cfg.Servers
	m.lastReloadAt = time.Now()
	m.lastReloadError = ""
	m.mu.Unlock()

	started, stopped := m.apply(ctx, cfg.Servers)
	if started+stopped > 0 {
		m.logger.Info("config reloaded", "path", m.configPath, "started", started, "stopped", stopped)
	}
	return nil
}

// watch reloads the config whenever the file changes, until ctx ends. It
// polls the file's modification time and size: no extra dependency, and
// editors that replace the file (write + rename) are handled like any
// other change.
func (m *serverManager) watch(ctx context.Context) {
	ticker := time.NewTicker(configWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			known := m.stamp
			m.mu.Unlock()
			if stampOf(m.configPath) == known {
				continue
			}
			// Errors are logged and shown in the dashboard; the next
			// change is tried again.
			_ = m.Reload(ctx)
		}
	}
}

// editConfig loads the config file, applies fn, saves it and reloads.
func (m *serverManager) editConfig(ctx context.Context, fn func(cfg *migrate.Config) error) error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	cfg, err := migrate.LoadConfig(ctx, m.configPath)
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("load %s: %w", m.configPath, err)
		}
		cfg = nil
	}
	if cfg == nil {
		cfg = &migrate.Config{Version: "1.0"}
	}
	if err := fn(cfg); err != nil {
		return err
	}
	if err := saveConfig(m.configPath, cfg); err != nil {
		return fmt.Errorf("save %s: %w", m.configPath, err)
	}
	return m.reloadLocked(ctx)
}

// RestartServer restarts a stdio server's process, or reconnects a remote
// one.
func (m *serverManager) RestartServer(ctx context.Context, name string) error {
	if err := m.pool.RestartServer(ctx, name); err != nil {
		return err
	}
	if m.handler != nil {
		// Do not wait for the tool list: the dashboard shows it arrive.
		go func() { _ = m.handler.RefreshServerTools(context.WithoutCancel(ctx), name) }()
	}
	return nil
}

// SetServerEnabled flips a server's `enabled` in the config file and
// applies it.
func (m *serverManager) SetServerEnabled(ctx context.Context, name string, enabled bool) error {
	return m.editConfig(ctx, func(cfg *migrate.Config) error {
		return setServerEnabled(cfg, name, enabled)
	})
}

func setServerEnabled(cfg *migrate.Config, name string, enabled bool) error {
	for _, srv := range cfg.Servers {
		if srv.Name == name {
			srv.Enabled = &enabled
			return nil
		}
	}
	return fmt.Errorf("server %q not found", name)
}

// RemoveServer deletes a server's entry from the config file and stops it.
func (m *serverManager) RemoveServer(ctx context.Context, name string) error {
	return m.editConfig(ctx, func(cfg *migrate.Config) error {
		for i, srv := range cfg.Servers {
			if srv.Name == name {
				cfg.Servers = append(cfg.Servers[:i], cfg.Servers[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("server %q not found", name)
	})
}

// AddServer appends a server to the config file and starts it.
func (m *serverManager) AddServer(ctx context.Context, spec dashboard.ServerSpec) error {
	srv, err := serverConfigFromSpec(spec)
	if err != nil {
		return err
	}
	return m.editConfig(ctx, func(cfg *migrate.Config) error {
		return appendServer(cfg, srv)
	})
}

func appendServer(cfg *migrate.Config, srv *migrate.ServerConfig) error {
	for _, existing := range cfg.Servers {
		if existing.Name == srv.Name {
			return fmt.Errorf("server %q already exists", srv.Name)
		}
	}
	cfg.Servers = append(cfg.Servers, srv)
	return nil
}

// serverConfigFromSpec validates a server to add and builds its config
// entry. It is shared by `server add` and the dashboard.
func serverConfigFromSpec(spec dashboard.ServerSpec) (*migrate.ServerConfig, error) {
	if !serverNamePattern.MatchString(spec.Name) {
		return nil, fmt.Errorf("invalid server name %q: use letters, digits, '.', '_' and '-' (at most 63 characters)", spec.Name)
	}
	enabled := true
	srv := &migrate.ServerConfig{
		Name:           spec.Name,
		Enabled:        &enabled,
		Timeout:        "30s",
		ConnectTimeout: "10s",
	}
	switch registry.TransportType(spec.Transport) {
	case registry.TransportStdio, "":
		if spec.Command == "" {
			return nil, errors.New("a stdio server needs a command")
		}
		resolved, err := exec.LookPath(spec.Command)
		if err != nil {
			return nil, fmt.Errorf("command not found in PATH: %s", spec.Command)
		}
		cwd := spec.CWD
		if cwd == "" {
			cwd = filepath.Dir(resolved)
		}
		srv.Transport = registry.TransportStdio
		srv.Stdio = &migrate.StdioConfig{Command: spec.Command, Args: spec.Args, Env: spec.Env, CWD: cwd}
	case registry.TransportHTTP, registry.TransportSSE:
		u, err := url.Parse(spec.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("a %s server needs an http(s) URL, got %q", spec.Transport, spec.URL)
		}
		srv.Transport = registry.TransportType(spec.Transport)
		srv.HTTP = &migrate.HTTPConfig{URL: spec.URL, Headers: spec.Headers}
	default:
		return nil, fmt.Errorf("invalid transport type: %s (must be stdio, http, or sse)", spec.Transport)
	}
	return srv, nil
}

// Overview implements dashboard.Admin.
func (m *serverManager) Overview() dashboard.Overview {
	m.mu.Lock()
	defer m.mu.Unlock()
	return dashboard.Overview{
		Version:         version.Version,
		PID:             os.Getpid(),
		StartedAt:       m.startedAt,
		Frontend:        m.frontend,
		ConfigPath:      m.configPath,
		LastReloadAt:    m.lastReloadAt,
		LastReloadError: m.lastReloadError,
	}
}

// serverTarget is what a server points at, safe to display: the command
// without its arguments (they may carry secrets), or the URL without
// credentials and query string.
func serverTarget(srv *migrate.ServerConfig) string {
	switch {
	case srv.Stdio != nil && srv.Stdio.Command != "":
		return srv.Stdio.Command
	case srv.HTTP != nil && srv.HTTP.URL != "":
		u, err := url.Parse(srv.HTTP.URL)
		if err != nil {
			return ""
		}
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		return u.String()
	}
	return ""
}

// Servers implements dashboard.Admin: every configured server, enabled or
// not, with the runtime state of those that are in the pool.
func (m *serverManager) Servers() []dashboard.ServerStatus {
	m.mu.Lock()
	configured := append([]*migrate.ServerConfig(nil), m.configured...)
	m.mu.Unlock()

	out := make([]dashboard.ServerStatus, 0, len(configured))
	for _, srv := range configured {
		if srv == nil {
			continue
		}
		st := dashboard.ServerStatus{
			Name:      srv.Name,
			Transport: string(srv.Transport),
			Target:    serverTarget(srv),
			Enabled:   serverEnabled(srv),
		}
		snap, inPool := m.pool.Snapshot(srv.Name)
		if !st.Enabled || !inPool {
			describeAbsent(&st)
			out = append(out, st)
			continue
		}
		fillFromSnapshot(&st, snap)
		if m.handler != nil {
			st.Tools, st.ToolsKnown = m.handler.ToolCount(srv.Name)
			rs := m.handler.ToolRefreshStatus(srv.Name)
			st.RefreshFailures, st.NextRetryAt = rs.Failures, rs.NextRetryAt
			if rs.Failures > 0 && rs.LastErrorAt.After(st.LastErrorAt) {
				st.LastError, st.LastErrorAt = rs.LastError, rs.LastErrorAt
			}
		}
		if m.pinner != nil {
			if pending, ok := m.pinner.Unapproved(srv.Name); ok {
				st.PendingPins = len(pending)
			}
		}
		classifyServer(&st)
		out = append(out, st)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func describeAbsent(st *dashboard.ServerStatus) {
	if !st.Enabled {
		st.State, st.Health, st.Summary = "disabled", dashboard.HealthDisabled, "Disabled in the config"
		return
	}
	st.State, st.Health, st.Summary = "not started", dashboard.HealthDown, "Could not be started, see the proxy's log"
}

func fillFromSnapshot(st *dashboard.ServerStatus, snap pool.ServerSnapshot) {
	st.State = string(snap.State)
	st.Requests = snap.Requests
	st.Errors = snap.Errors
	st.AvgLatencyMs = snap.AvgLatencyMs
	st.InFlight = snap.InFlight
	st.LastRequestAt = snap.LastRequestAt
	st.LastError = snap.LastError
	st.LastErrorAt = snap.LastErrorAt
	st.Initialized = snap.Initialized
	st.ProtocolVersion = snap.ProtocolVersion
	st.ServerName = snap.ServerName
	st.ServerVersion = snap.ServerVersion
	st.Capabilities = snap.Capabilities
	st.PID = snap.PID
	st.Restarts = snap.RestartCount
	st.RecentStderr = snap.RecentStderr
	st.Since = snap.StartedAt
	if st.Since.IsZero() {
		st.Since = snap.ConnectedAt
	}
}

// classifyServer derives the health class and the one-line summary from a
// server's state. The order matters: the first match is the most useful
// thing to tell the reader.
func classifyServer(st *dashboard.ServerStatus) {
	state := pool.ServerState(st.State)
	authFailed := strings.Contains(strings.ToLower(st.LastError), "authorization required") ||
		strings.Contains(st.LastError, "401")
	switch {
	case state == pool.StateError && authFailed:
		st.Health, st.Summary = dashboard.HealthDown, "Authentication refused: check this server's token"
	case state == pool.StateError:
		st.Health, st.Summary = dashboard.HealthDown, firstLine(orText(st.LastError, "Failed, no error recorded"))
	case state == pool.StateDisconnected:
		st.Health, st.Summary = dashboard.HealthWarn, "Disconnected, reconnects on the next call"
	case state == pool.StateStopped || state == pool.StateStopping:
		st.Health, st.Summary = dashboard.HealthIdle, "Stopped while idle, starts on the next call"
	case state == pool.StateStarting || !st.Initialized:
		st.Health, st.Summary = dashboard.HealthWarn, "Starting, handshake not finished"
	case st.RefreshFailures > 0:
		st.Health, st.Summary = dashboard.HealthWarn, "Up, but its tool list could not be refreshed"
	case st.PendingPins > 0:
		st.Health, st.Summary = dashboard.HealthWarn, fmt.Sprintf("Up, %d tool definitions await approval", st.PendingPins)
	case st.InFlight > 0:
		st.Health, st.Summary = dashboard.HealthOK, fmt.Sprintf("Up, %d calls in flight", st.InFlight)
	default:
		st.Health, st.Summary = dashboard.HealthOK, "Up"
	}
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Sessions implements dashboard.Admin.
func (m *serverManager) Sessions() []dashboard.Session {
	if m.handler == nil {
		return nil
	}
	sessions := m.handler.Sessions()
	out := make([]dashboard.Session, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, dashboard.Session(s))
	}
	return out
}

// Calls implements dashboard.Admin.
func (m *serverManager) Calls(limit int, server string) []dashboard.Call {
	recent := m.calls.Recent(limit, server)
	out := make([]dashboard.Call, 0, len(recent))
	for _, c := range recent {
		out = append(out, dashboard.Call(c))
	}
	return out
}

// statusPublishInterval is how often a proxy rewrites its status file.
const statusPublishInterval = 5 * time.Second

// publishStatus keeps this process's status file current until ctx ends:
// it is what `leanproxy-mcp status` reads.
func (m *serverManager) publishStatus(ctx context.Context, store *statusfile.FileStatusStore) {
	ticker := time.NewTicker(statusPublishInterval)
	defer ticker.Stop()
	for {
		m.writeStatus(store)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *serverManager) writeStatus(store *statusfile.FileStatusStore) {
	servers := m.Servers()
	out := make([]statusfile.ServerStatus, 0, len(servers))
	for _, sv := range servers {
		if !sv.Enabled {
			continue
		}
		out = append(out, statusfile.ServerStatus{
			Name:         sv.Name,
			Status:       legacyStatus(sv),
			RequestCount: sv.Requests,
			ErrorCount:   sv.Errors,
			RestartCount: sv.Restarts,
			Uptime:       uptimeString(sv.Since),
			ToolCount:    sv.Tools,
			LastActivity: sv.LastRequestAt,
			Transport:    sv.Transport,
			State:        sv.State,
			Summary:      sv.Summary,
			LastError:    firstLine(sv.LastError),
			AvgLatencyMs: sv.AvgLatencyMs,
			InFlight:     sv.InFlight,
			PID:          sv.PID,
		})
	}
	if m.handler != nil {
		store.SetClients(len(m.handler.Sessions()))
	}
	store.UpdateServers(out)
	flushUsageSnapshot()
}

// legacyStatus maps a server to the running / error / stopped values the
// status file has always used.
func legacyStatus(sv dashboard.ServerStatus) string {
	switch sv.Health {
	case dashboard.HealthDown:
		return "error"
	case dashboard.HealthIdle:
		return "stopped"
	default:
		return "running"
	}
}

func uptimeString(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	return time.Since(since).Round(time.Second).String()
}
