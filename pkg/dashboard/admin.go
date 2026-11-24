package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Admin is what the admin views show and drive: the proxy's upstream
// servers, its client sessions and its recent tool calls. The proxy
// supplies it (cmd); a nil Admin leaves the dashboard with usage only.
//
// The read methods back views anyone who can open the dashboard sees. The
// write methods are only reachable when the dashboard has a token (see
// writable): adding a stdio server runs a command as the proxy's user, so
// it must never be open to every local process.
type Admin interface {
	Overview() Overview
	Servers() []ServerStatus
	Sessions() []Session
	Calls(limit int, server string) []Call

	RestartServer(ctx context.Context, name string) error
	SetServerEnabled(ctx context.Context, name string, enabled bool) error
	AddServer(ctx context.Context, spec ServerSpec) error
	RemoveServer(ctx context.Context, name string) error
	Reload(ctx context.Context) error
}

// Overview is the proxy process itself.
type Overview struct {
	Version    string    `json:"version"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"started_at"`
	Frontend   string    `json:"frontend"`
	ConfigPath string    `json:"config_path"`
	// LastReloadAt is when the server list was last (re)read from the
	// config file; LastReloadError is why the latest attempt failed.
	LastReloadAt    time.Time `json:"last_reload_at,omitzero"`
	LastReloadError string    `json:"last_reload_error,omitempty"`
}

// Server health classes, from worst to best. They drive the status dot.
const (
	HealthDown     = "down"     // error, or cannot connect
	HealthWarn     = "warn"     // up but something needs a look
	HealthOK       = "ok"       // up
	HealthIdle     = "idle"     // stopped on purpose, starts on demand
	HealthDisabled = "disabled" // enabled: false in the config
)

// ServerStatus is one configured upstream server and its runtime state.
type ServerStatus struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	// Target is the command (stdio) or URL (HTTP/SSE), without arguments,
	// query string or credentials.
	Target  string `json:"target,omitempty"`
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	Health  string `json:"health"`
	// Summary is one short line saying what Health means here.
	Summary string `json:"summary"`

	Tools      int  `json:"tools"`
	ToolsKnown bool `json:"tools_known"`
	// PendingPins is how many tool definitions await approval (tool
	// pinning).
	PendingPins int `json:"pending_pins"`

	Requests      int64     `json:"requests"`
	Errors        int64     `json:"errors"`
	AvgLatencyMs  float64   `json:"avg_latency_ms"`
	InFlight      int       `json:"in_flight"`
	LastRequestAt time.Time `json:"last_request_at,omitzero"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at,omitzero"`

	Initialized     bool      `json:"initialized"`
	ProtocolVersion string    `json:"protocol_version,omitempty"`
	ServerName      string    `json:"server_name,omitempty"`
	ServerVersion   string    `json:"server_version,omitempty"`
	Capabilities    []string  `json:"capabilities,omitempty"`
	PID             int       `json:"pid,omitempty"`
	Since           time.Time `json:"since,omitzero"`
	Restarts        int       `json:"restarts,omitempty"`
	RecentStderr    string    `json:"recent_stderr,omitempty"`

	// RefreshFailures counts consecutive failed tools/list refreshes;
	// NextRetryAt is when the next one is due.
	RefreshFailures int       `json:"refresh_failures,omitempty"`
	NextRetryAt     time.Time `json:"next_retry_at,omitzero"`
}

// Session is one connected MCP client.
type Session struct {
	ID              string    `json:"id"`
	Client          string    `json:"client"`
	ClientVersion   string    `json:"client_version,omitempty"`
	ProtocolVersion string    `json:"protocol_version,omitempty"`
	Exposure        string    `json:"exposure,omitempty"`
	Initialized     bool      `json:"initialized"`
	OpenedAt        time.Time `json:"opened_at"`
	LastCallAt      time.Time `json:"last_call_at,omitzero"`
	Calls           int64     `json:"calls"`
}

// Call is one recent tool call: who called what, how long it took, how it
// ended. Never its arguments or its result.
type Call struct {
	Time          time.Time `json:"time"`
	SessionID     string    `json:"session_id,omitempty"`
	Client        string    `json:"client,omitempty"`
	Server        string    `json:"server"`
	Tool          string    `json:"tool"`
	DurationMs    float64   `json:"duration_ms"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	ResponseBytes int       `json:"response_bytes"`
}

// ServerSpec is a server to add: a command (stdio) or a URL (http, sse).
type ServerSpec struct {
	Name      string
	Transport string
	// Stdio.
	Command string
	Args    []string
	Env     []string
	CWD     string
	// HTTP / SSE.
	URL     string
	Headers map[string]string
}

// ErrReadOnly is returned by the write endpoints of a dashboard that has
// no token.
var ErrReadOnly = errors.New("the dashboard is read-only: start it with --dashboard-token (or run `leanproxy-mcp daemon install`) to manage servers from here")

// adminActionTimeout bounds one write action (a restart waits for the
// server to come back).
const adminActionTimeout = 45 * time.Second

// maxAdminFormBytes bounds the body of a write request.
const maxAdminFormBytes = 64 << 10

// adminPage is what the admin fragments render.
type adminPage struct {
	Overview Overview
	Servers  []ServerStatus
	Sessions []Session
	Calls    []Call
	// Writable is true when the write actions are available.
	Writable bool
	// Notice and Failure are the outcome of the write action that
	// produced this fragment.
	Notice  string
	Failure string
	Now     time.Time
	// Counts for the header.
	Up, Down int
}

func (s *server) adminData(notice, failure string) adminPage {
	p := adminPage{Writable: s.writable, Notice: notice, Failure: failure, Now: time.Now()}
	if s.admin == nil {
		return p
	}
	p.Overview = s.admin.Overview()
	p.Servers = s.admin.Servers()
	for _, sv := range p.Servers {
		switch sv.Health {
		case HealthOK, HealthWarn, HealthIdle:
			p.Up++
		case HealthDown:
			p.Down++
		}
	}
	return p
}

func (s *server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/servers", s.handleAdminServers)
	mux.HandleFunc("GET /admin/sessions", s.handleAdminSessions)
	mux.HandleFunc("GET /admin/calls", s.handleAdminCalls)

	mux.HandleFunc("GET /api/admin/overview", s.adminJSON(func(*http.Request) any { return s.admin.Overview() }))
	mux.HandleFunc("GET /api/admin/servers", s.adminJSON(func(*http.Request) any { return s.admin.Servers() }))
	mux.HandleFunc("GET /api/admin/sessions", s.adminJSON(func(*http.Request) any { return s.admin.Sessions() }))
	mux.HandleFunc("GET /api/admin/calls", s.adminJSON(func(r *http.Request) any {
		return s.admin.Calls(callLimit(r), r.URL.Query().Get("server"))
	}))

	mux.HandleFunc("POST /admin/servers", s.adminWrite(s.actionAdd))
	mux.HandleFunc("POST /admin/reload", s.adminWrite(s.actionReload))
	mux.HandleFunc("POST /admin/servers/{server}/restart", s.adminWrite(s.actionRestart))
	mux.HandleFunc("POST /admin/servers/{server}/enable", s.adminWrite(s.actionEnable(true)))
	mux.HandleFunc("POST /admin/servers/{server}/disable", s.adminWrite(s.actionEnable(false)))
	mux.HandleFunc("POST /admin/servers/{server}/remove", s.adminWrite(s.actionRemove))
}

// callLimit reads ?limit= (default 50, at most 500).
func callLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return 50
	}
	return min(n, 500)
}

func (s *server) adminJSON(get func(*http.Request) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.admin == nil {
			http.Error(w, "admin data unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(get(r)); err != nil {
			globalLogger.Error("failed to encode admin JSON", "error", err)
		}
	}
}

func (s *server) handleAdminServers(w http.ResponseWriter, r *http.Request) {
	s.render(w, "adminServers", s.adminData("", ""))
}

func (s *server) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	p := adminPage{Now: time.Now()}
	if s.admin != nil {
		p.Sessions = s.admin.Sessions()
	}
	s.render(w, "adminSessions", p)
}

func (s *server) handleAdminCalls(w http.ResponseWriter, r *http.Request) {
	p := adminPage{Now: time.Now()}
	if s.admin != nil {
		p.Calls = s.admin.Calls(callLimit(r), r.URL.Query().Get("server"))
	}
	s.render(w, "adminCalls", p)
}

// adminWrite wraps a write action: it refuses when the dashboard is
// read-only, bounds the body and the time, and answers with the refreshed
// server table carrying the outcome. The request already passed the token
// check and httpsec.ValidateHost's same-origin check for state-changing
// methods; the X-LeanProxy-Admin header is one more thing a cross-site
// form cannot send.
func (s *server) adminWrite(action func(ctx context.Context, r *http.Request) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.admin == nil {
			http.Error(w, "admin unavailable", http.StatusServiceUnavailable)
			return
		}
		if !s.writable {
			http.Error(w, ErrReadOnly.Error(), http.StatusForbidden)
			return
		}
		if r.Header.Get("X-LeanProxy-Admin") == "" {
			http.Error(w, "Forbidden: missing X-LeanProxy-Admin header", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAdminFormBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), adminActionTimeout)
		defer cancel()
		notice, err := action(ctx, r)
		if err != nil {
			// 200 with the failure in the fragment: htmx does not swap
			// error responses, and the reader must see why it failed.
			globalLogger.Warn("dashboard: admin action failed", "path", r.URL.Path, "error", err)
			s.render(w, "adminServers", s.adminData("", err.Error()))
			return
		}
		globalLogger.Info("dashboard: admin action", "path", r.URL.Path)
		s.render(w, "adminServers", s.adminData(notice, ""))
	}
}

func (s *server) actionRestart(ctx context.Context, r *http.Request) (string, error) {
	name := r.PathValue("server")
	if err := s.admin.RestartServer(ctx, name); err != nil {
		return "", fmt.Errorf("restart %s: %w", name, err)
	}
	return fmt.Sprintf("%s restarted.", name), nil
}

func (s *server) actionEnable(enabled bool) func(ctx context.Context, r *http.Request) (string, error) {
	return func(ctx context.Context, r *http.Request) (string, error) {
		name := r.PathValue("server")
		if err := s.admin.SetServerEnabled(ctx, name, enabled); err != nil {
			return "", err
		}
		if enabled {
			return fmt.Sprintf("%s enabled.", name), nil
		}
		return fmt.Sprintf("%s disabled.", name), nil
	}
}

func (s *server) actionRemove(ctx context.Context, r *http.Request) (string, error) {
	name := r.PathValue("server")
	if err := s.admin.RemoveServer(ctx, name); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s removed from the config.", name), nil
}

func (s *server) actionReload(ctx context.Context, r *http.Request) (string, error) {
	if err := s.admin.Reload(ctx); err != nil {
		return "", err
	}
	return "Config reloaded.", nil
}

func (s *server) actionAdd(ctx context.Context, r *http.Request) (string, error) {
	spec, err := specFromForm(r)
	if err != nil {
		return "", err
	}
	if err := s.admin.AddServer(ctx, spec); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s added.", spec.Name), nil
}

// specFromForm reads the add-server form. Arguments are split on spaces
// (double quotes group); env and headers are one KEY=value / Name: value
// per line.
func specFromForm(r *http.Request) (ServerSpec, error) {
	spec := ServerSpec{
		Name:      strings.TrimSpace(r.PostFormValue("name")),
		Transport: strings.TrimSpace(r.PostFormValue("transport")),
		Command:   strings.TrimSpace(r.PostFormValue("command")),
		CWD:       strings.TrimSpace(r.PostFormValue("cwd")),
		URL:       strings.TrimSpace(r.PostFormValue("url")),
	}
	if spec.Name == "" {
		return spec, errors.New("a server needs a name")
	}
	args, err := splitArgs(r.PostFormValue("args"))
	if err != nil {
		return spec, err
	}
	spec.Args = args
	for _, line := range nonEmptyLines(r.PostFormValue("env")) {
		if !strings.Contains(line, "=") {
			return spec, fmt.Errorf("environment line %q is not KEY=value", line)
		}
		spec.Env = append(spec.Env, line)
	}
	for _, line := range nonEmptyLines(r.PostFormValue("headers")) {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return spec, fmt.Errorf("header line %q is not Name: value", line)
		}
		if spec.Headers == nil {
			spec.Headers = make(map[string]string)
		}
		spec.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return spec, nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// splitArgs splits a command line on whitespace; double quotes group
// words, and a backslash escapes the next character inside or outside
// them.
func splitArgs(s string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inWord  bool
		inQuote bool
		escaped bool
	)
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped, inWord = true, true
		case r == '"':
			inQuote = !inQuote
			inWord = true
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inQuote || escaped {
		return nil, errors.New("arguments: unbalanced quote or trailing backslash")
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}

// Template helpers.

// span renders the time elapsed since t as a bare duration ("12s", "4m",
// "3h", "2d"), or "-" for the zero time.
func span(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// ago renders how long ago t was ("just now", "12s ago", "3h ago"), or
// "-" for the zero time.
func ago(now, t time.Time) string {
	switch {
	case t.IsZero():
		return "-"
	case now.Sub(t) < time.Second:
		return "just now"
	default:
		return span(now, t) + " ago"
	}
}

// until renders how far in the future t is, or "" when it is not.
func until(now, t time.Time) string {
	if t.IsZero() || !t.After(now) {
		return ""
	}
	d := t.Sub(now)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds())+1)
	}
	return fmt.Sprintf("%dm", int(d.Minutes())+1)
}

// millis renders a latency ("-" for none, "0.8 ms", "12 ms", "1.4 s").
func millis(ms float64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms < 10:
		return fmt.Sprintf("%.1f ms", ms)
	case ms < 1000:
		return fmt.Sprintf("%.0f ms", ms)
	default:
		return fmt.Sprintf("%.1f s", ms/1000)
	}
}

// bytesShort renders a size ("812 B", "4.2 kB", "1.3 MB").
func bytesShort(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1f kB", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1_000_000)
	}
}

// domID turns a server name into a stable element id.
func domID(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "_%x", r)
		}
	}
	return b.String()
}
