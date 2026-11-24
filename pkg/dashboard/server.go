package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/httpsec"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

//go:embed assets/*
var assetsFS embed.FS

//go:embed views/*
var viewsFS embed.FS

// staticFS is assets/ served at /static/. The embedded paths start with
// "assets/": serving assetsFS itself would answer 404 for every file.
var staticFS = func() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}()

type Config struct {
	Bind  string
	Token string

	// AllowedHosts lists extra Host header values (--dashboard-allowed-hosts)
	// accepted in addition to the bind host, localhost, 127.0.0.1 and ::1.
	// Each is combined with the dashboard's own port unless it already
	// names one. See pkg/httpsec.AllowedHosts.
	AllowedHosts []string

	// Usage supplies the today / week-to-date figures, read from the usage
	// store (pkg/usage.Live). A nil func, or one returning nil (the store
	// could not be read), renders an "unavailable" state instead of zeros.
	Usage func() *metrics.UsageSummary

	// Admin supplies the servers, clients and calls views and their write
	// actions. Nil: the dashboard shows usage only. The write actions are
	// available only when Token is set.
	Admin Admin
}

// DashboardData is what the index and summary cards render: the usage
// store's today and week-to-date windows (see metrics.UsageWindow), with
// token counts pre-formatted.
type DashboardData struct {
	// Available is false when there is no usage data source.
	Available bool
	Estimator string

	TodaySaved    string
	TodayOriginal string
	TodayPercent  string
	WeekSaved     string
	WeekOriginal  string
	WeekPercent   string
	TopServer     string
	TopTool       string
	ServerCount   int
	ToolCount     int
	// NoToolData is true when today has no per-tool rows, which is what
	// the response governor being off (the default) looks like.
	NoToolData bool
	Servers    []ServerRow

	// HasAdmin and Admin carry the admin views of the index page.
	HasAdmin bool
	Admin    adminPage
}

// ServerRow is one row of the server table (today's window).
type ServerRow struct {
	// Name is the server's name, empty for tool results the governor did
	// not attribute to a server; Label is what the table shows and Path is
	// Name escaped for the drill-down URL.
	Name     string
	Label    string
	Path     string
	Tools    int
	Calls    int64
	Original string
	Returned string
	Saved    string
}

// ServerDrillDown is one server's tools in today's window.
type ServerDrillDown struct {
	ServerName     string
	OriginalTokens string
	Tools          []metrics.UsageTool
}

// pages holds every HTML template (views/*.html) in one set, so the index
// can include the partials the htmx endpoints also render on their own.
// Styles and scripts are static assets: the dashboard's CSP (httpsec) allows
// neither inline <style> nor inline <script>.
var pages = template.Must(template.New("pages").Funcs(template.FuncMap{
	"ago":        ago,
	"span":       span,
	"until":      until,
	"millis":     millis,
	"bytesShort": bytesShort,
	"domID":      domID,
}).ParseFS(viewsFS, "views/*.html"))

// toolPinTemplate renders this process's latest tool pinning events
// (#310): drift, identity changes, scanner findings, collisions.
var toolPinTemplate = template.Must(template.New("toolPins").Parse(`
{{if .}}
<table class="server-table">
  <thead><tr><th>Time</th><th>Event</th><th>Server</th><th>Tool</th><th>Severity</th><th>Detail</th></tr></thead>
  <tbody>
  {{range .}}
  <tr>
    <td class="cell-time">{{.Time.Format "2006-01-02 15:04:05"}}</td>
    <td>{{.Kind}}</td>
    <td class="cell-server">{{.Server}}</td>
    <td class="cell-tool">{{.Tool}}</td>
    <td>{{.Severity}}</td>
    <td>{{.Detail}}</td>
  </tr>
  {{end}}
  </tbody>
</table>
{{else}}
<div class="empty-state">No tool pinning events. Review pins with <code>leanproxy-mcp tools pins list</code>.</div>
{{end}}
`))

func handleToolPins(w http.ResponseWriter, r *http.Request) {
	events := toolpin.RecentEvents()
	if len(events) > 50 {
		events = events[:50]
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := toolPinTemplate.Execute(w, events); err != nil {
		globalLogger.Error("failed to render tool pinning events", "error", err)
	}
}

// server holds the dashboard's data source; its methods are the handlers.
type server struct {
	usage func() *metrics.UsageSummary
	admin Admin
	// writable is true when the dashboard has a token: only then are the
	// admin write actions offered and accepted.
	writable bool
	// detailed is true when the dashboard has a token: only then do the
	// admin views carry raw error text and stderr lines.
	detailed bool
}

func ListenAndServe(cfg Config, logger *slog.Logger) (*http.Server, error) {
	globalLogger = logger

	if cfg.Bind == "" || cfg.Bind == "off" {
		logger.Info("dashboard endpoint disabled")
		return nil, nil
	}

	host, port, err := net.SplitHostPort(cfg.Bind)
	if err != nil {
		if ip := net.ParseIP(cfg.Bind); ip != nil && strings.Contains(cfg.Bind, ":") {
			return nil, fmt.Errorf("invalid dashboard bind address %q: IPv6 addresses must be bracketed, e.g. [::1]:9090", cfg.Bind)
		}
		return nil, fmt.Errorf("invalid dashboard bind address %q: %w", cfg.Bind, err)
	}

	// Non-loopback bind without a token: refuse to start rather than warn,
	// since every request would otherwise be served unauthenticated to
	// anyone who can reach the bound interface (issue #316). A loopback
	// bind without a token is allowed; Host validation below covers DNS
	// rebinding from a malicious page in a local browser.
	if !httpsec.IsLoopbackHost(host) {
		if cfg.Token == "" {
			return nil, fmt.Errorf("refusing to start dashboard on non-loopback bind %q without a token: set --dashboard-token", cfg.Bind)
		}
		logger.Warn("dashboard endpoint listening on non-loopback interface; data is not encrypted",
			"bind", cfg.Bind)
	}

	ln, err := net.Listen("tcp", cfg.Bind)
	if err != nil {
		return nil, fmt.Errorf("dashboard listen failed: %w", err)
	}
	// The bind address may have used port 0; use the listener's actual port
	// for both the allowed-hosts set and the server's Addr below.
	_, actualPort, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		actualPort = port
	}

	codes := newLoginCodes()
	protected := newServer(cfg).routes()
	// Mints a one-time sign-in code; behind the token like everything else.
	protected.HandleFunc("POST /login/code", handleLoginCode(codes))
	topMux := http.NewServeMux()
	sessions := newBrowserSessions()
	topMux.HandleFunc("GET /login", handleLogin(cfg.Token, codes, sessions))
	topMux.Handle("/", requireBearerToken(cfg.Token, logger, sessions)(protected))

	allowedHosts := httpsec.AllowedHosts(host, actualPort, cfg.AllowedHosts)
	handler := httpsec.SecurityHeaders()(httpsec.ValidateHost(allowedHosts)(topMux))

	srv := &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("dashboard endpoint started", "bind", srv.Addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("dashboard endpoint error", "error", err)
		}
	}()

	return srv, nil
}

// routes returns every route that requires the dashboard token (when one
// is configured); /login is registered separately, unprotected, since it
// is how a browser exchanges the token for a cookie in the first place.
func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard", s.handleCards)
	mux.HandleFunc("GET /api/dashboard/json", s.handleJSON)
	mux.HandleFunc("GET /api/dashboard/servers", s.handleServerTable)
	mux.HandleFunc("GET /api/dashboard/tool-pins", handleToolPins)
	mux.HandleFunc("GET /api/dashboard/servers/{server}", s.handleServerDrilldown)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /{$}", s.handleIndex)
	s.adminRoutes(mux)
	return mux
}

func newServer(cfg Config) *server {
	return &server{usage: cfg.Usage, admin: cfg.Admin, writable: cfg.Admin != nil && cfg.Token != "", detailed: cfg.Token != ""}
}

func (s *server) summary() *metrics.UsageSummary {
	if s.usage == nil {
		return nil
	}
	return s.usage()
}

func collectDashboardData(sum *metrics.UsageSummary) DashboardData {
	if sum == nil {
		return DashboardData{TopServer: "-", TopTool: "-", Servers: []ServerRow{}}
	}
	today, week := sum.Today, sum.Week
	data := DashboardData{
		Available:     true,
		Estimator:     sum.Estimator,
		TodaySaved:    formatTokens(today.SavedTokens),
		TodayOriginal: formatTokens(today.OriginalTokens),
		TodayPercent:  fmt.Sprintf("%.1f%%", today.SavedPercent),
		WeekSaved:     formatTokens(week.SavedTokens),
		WeekOriginal:  formatTokens(week.OriginalTokens),
		WeekPercent:   fmt.Sprintf("%.1f%%", week.SavedPercent),
		TopServer:     orDash(today.TopServer),
		TopTool:       orDash(today.TopTool),
		ServerCount:   len(today.ByServer),
		ToolCount:     len(today.ByTool),
		NoToolData:    len(today.ByTool) == 0,
		Servers:       make([]ServerRow, 0, len(today.ByServer)),
	}
	for _, sv := range today.ByServer {
		data.Servers = append(data.Servers, ServerRow{
			Name:     sv.Server,
			Label:    serverLabel(sv.Server),
			Path:     url.PathEscape(sv.Server),
			Tools:    sv.Tools,
			Calls:    sv.Calls,
			Original: formatTokens(sv.OriginalTokens),
			Returned: formatTokens(sv.ReturnedTokens),
			Saved:    formatTokens(sv.SavedTokens),
		})
	}
	return data
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// serverLabel is how the tables show a server; tool results the governor
// did not attribute to a server have an empty name.
func serverLabel(name string) string {
	if name == "" {
		return "(no server)"
	}
	return name
}

var globalLogger = slog.Default()

func (s *server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		globalLogger.Error("failed to render dashboard template", "template", name, "error", err)
	}
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data := collectDashboardData(s.summary())
	if s.admin != nil {
		data.HasAdmin = true
		data.Admin = s.adminData("", "")
	}
	s.render(w, "index", data)
}

func (s *server) handleCards(w http.ResponseWriter, r *http.Request) {
	s.render(w, "cards", collectDashboardData(s.summary()))
}

func (s *server) handleServerTable(w http.ResponseWriter, r *http.Request) {
	s.render(w, "serverRows", collectDashboardData(s.summary()))
}

func (s *server) handleServerDrilldown(w http.ResponseWriter, r *http.Request) {
	// PathValue is already percent-decoded (rows link with Path).
	serverName := r.PathValue("server")
	if serverName == "" {
		http.Error(w, "server name required", http.StatusBadRequest)
		return
	}

	dd := ServerDrillDown{ServerName: serverName, Tools: []metrics.UsageTool{}, OriginalTokens: "0"}
	if sum := s.summary(); sum != nil {
		dd.Tools = sum.Today.ServerTools(serverName)
		var total int64
		for _, t := range dd.Tools {
			total += t.OriginalTokens
		}
		dd.OriginalTokens = formatTokens(total)
	}
	s.render(w, "drilldown", dd)
}

// handleJSON serves the usage summary (both windows, per server and per
// tool) as JSON: the same object /metrics serves under "usage". It answers
// 503 when there is no usage data source, rather than a misleading zero.
func (s *server) handleJSON(w http.ResponseWriter, r *http.Request) {
	sum := s.summary()
	if sum == nil {
		http.Error(w, "usage data unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(sum); err != nil {
		globalLogger.Error("failed to encode dashboard JSON", "error", err)
	}
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
