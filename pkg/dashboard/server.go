package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
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

// pages holds every HTML template (the index, the cards partial and
// views/drilldown.html) in one set, so the index can include the partials
// the htmx endpoints also render on their own.
var pages = func() *template.Template {
	t := template.Must(template.New("index").Parse(indexHTML))
	template.Must(t.New("cards").Parse(cardsHTML))
	return template.Must(t.ParseFS(viewsFS, "views/drilldown.html"))
}()

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LeanProxy Dashboard</title>
<script src="/static/htmx.min.js"></script>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    background: #0f172a; color: #e2e8f0; min-height: 100vh; display: flex;
    flex-direction: column; align-items: center; padding: 2rem 1rem;
  }
  .container { max-width: 900px; width: 100%; }
  h1 {
    font-size: 1.75rem; font-weight: 700; margin-bottom: 0.5rem;
    text-align: center; color: #38bdf8;
  }
  .subtitle { text-align: center; font-size: 0.8rem; color: #64748b; margin-bottom: 2rem; }
  .cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem; margin-bottom: 1.5rem; }
  .card {
    background: #1e293b; border-radius: 0.75rem; padding: 1.25rem;
    border: 1px solid #334155; text-align: center; min-width: 0;
  }
  .card .label { font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; color: #94a3b8; margin-bottom: 0.5rem; }
  .card .value { font-size: 1.5rem; font-weight: 700; color: #f1f5f9; overflow-wrap: anywhere; }
  .card .value.token { color: #a78bfa; }
  .card .value.server { color: #34d399; }
  .card .value.tool { color: #f472b6; font-size: 1.1rem; }
  .card .detail { font-size: 0.75rem; color: #64748b; margin-top: 0.35rem; }
  .meta { text-align: center; font-size: 0.875rem; color: #64748b; margin-top: 1.5rem; }
  .meta span { margin: 0 0.75rem; }
  .error-card {
    background: #1e293b; border-radius: 0.75rem; padding: 2rem;
    border: 1px solid #ef4444; text-align: center; margin-bottom: 1.5rem;
  }
  .error-card .value { font-size: 1rem; color: #fca5a5; }
  .section-title {
    font-size: 1.1rem; font-weight: 600; color: #e2e8f0;
    margin: 1.5rem 0 0.75rem;
  }
  .server-table, .drilldown-table {
    width: 100%; border-collapse: collapse; margin-bottom: 1rem;
    background: #1e293b; border-radius: 0.75rem; overflow: hidden;
  }
  .server-table th, .drilldown-table th {
    text-align: left; padding: 0.75rem 1rem;
    font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em;
    color: #94a3b8; border-bottom: 1px solid #334155;
  }
  .server-table td, .drilldown-table td {
    padding: 0.75rem 1rem; border-bottom: 1px solid #1e293b;
    font-size: 0.875rem;
  }
  .server-table tbody tr.server-row:hover {
    background: #334155; cursor: pointer;
  }
  .cell-server { color: #34d399; font-weight: 600; }
  .cell-tool { color: #f472b6; font-weight: 600; }
  .cell-tokens { color: #a78bfa; font-family: monospace; }
  .cell-count { color: #e2e8f0; font-family: monospace; }
  .cell-avg { color: #94a3b8; font-family: monospace; }
  .cell-time { color: #64748b; font-family: monospace; font-size: 0.75rem; }
  .cell-arrow { color: #475569; text-align: right; font-size: 1.1rem; }
  .drilldown-panel {
    background: #1e293b; border-radius: 0.75rem; padding: 1.25rem;
    border: 1px solid #334155; margin-bottom: 1rem;
  }
  .drilldown-header {
    display: flex; justify-content: space-between; align-items: center;
    margin-bottom: 1rem;
  }
  .drilldown-header h2 { font-size: 1.1rem; color: #38bdf8; }
  .drilldown-total { font-size: 0.85rem; color: #a78bfa; font-family: monospace; }
  .empty-state { color: #64748b; text-align: center; padding: 2rem; font-style: italic; }
  .empty-state code { font-style: normal; color: #94a3b8; }
  @media (max-width: 600px) {
    .cards { grid-template-columns: repeat(2, 1fr); }
  }
</style>
</head>
<body>
<div class="container">
  <h1>LeanProxy Dashboard</h1>
  <p class="subtitle">Tokens measured by the proxy ({{or .Estimator "chars/4"}}), read from the usage store every front end on this machine writes to.
    Today is since 00:00 UTC; the week starts Monday 00:00 UTC.</p>
  <div id="dashboard-cards" hx-get="/api/dashboard" hx-trigger="every 5s" hx-swap="innerHTML">
    {{template "cards" .}}
  </div>
  <div class="meta">
    <span>Auto-refresh every 5s</span>
    <span>&middot;</span>
    <span>{{.ServerCount}} servers today</span>
    <span>&middot;</span>
    <span>{{.ToolCount}} tools today</span>
  </div>
  <div class="section-title">Servers today</div>
  <div id="server-table" hx-get="/api/dashboard/servers" hx-trigger="every 5s" hx-swap="innerHTML">
    {{template "serverRows" .}}
  </div>
  <div id="drilldown-content"></div>
  <div class="section-title">Tool pinning</div>
  <div id="tool-pins" hx-get="/api/dashboard/tool-pins" hx-trigger="load, every 10s" hx-swap="innerHTML"></div>
</div>
</body>
</html>
`

const cardsHTML = `
{{if .Available}}
<div class="cards">
  <div class="card">
    <div class="label">Saved today</div>
    <div class="value token">{{.TodaySaved}}</div>
    <div class="detail">of {{.TodayOriginal}} tokens ({{.TodayPercent}})</div>
  </div>
  <div class="card">
    <div class="label">Saved this week</div>
    <div class="value token">{{.WeekSaved}}</div>
    <div class="detail">of {{.WeekOriginal}} tokens ({{.WeekPercent}})</div>
  </div>
  <div class="card">
    <div class="label">Top server today</div>
    <div class="value server">{{.TopServer}}</div>
    <div class="detail">by response size</div>
  </div>
  <div class="card">
    <div class="label">Top tool today</div>
    <div class="value tool">{{.TopTool}}</div>
    <div class="detail">by response size</div>
  </div>
</div>
{{else}}
<div class="error-card"><div class="value">Usage data unavailable: the usage store (~/.leanproxy/usage) could not be read. See the proxy's log.</div></div>
{{end}}
`

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

	topMux := http.NewServeMux()
	topMux.HandleFunc("GET /login", handleLogin(cfg.Token))
	topMux.Handle("/", requireBearerToken(cfg.Token, logger)((&server{usage: cfg.Usage}).routes()))

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
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(assetsFS))))
	mux.HandleFunc("GET /{$}", s.handleIndex)
	return mux
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
	s.render(w, "index", collectDashboardData(s.summary()))
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
