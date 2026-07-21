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
	"sort"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

//go:embed assets/*
var assetsFS embed.FS

//go:embed views/*
var viewsFS embed.FS

type Config struct {
	Bind  string
	Token string
}

type DashboardData struct {
	TodaySpend  string
	WTDSpend    string
	TopServer   string
	TopTool     string
	ServerCount int
	ToolCount   int
	Servers     []ServerRow
}

type ServerRow struct {
	Name       string
	ToolCount  int
	TokenCount string
}

var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LeanProxy Cost Dashboard</title>
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
    font-size: 1.75rem; font-weight: 700; margin-bottom: 2rem;
    text-align: center; color: #38bdf8;
  }
  .cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem; margin-bottom: 1.5rem; }
  .card {
    background: #1e293b; border-radius: 0.75rem; padding: 1.25rem;
    border: 1px solid #334155; text-align: center;
  }
  .card .label { font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; color: #94a3b8; margin-bottom: 0.5rem; }
  .card .value { font-size: 1.5rem; font-weight: 700; color: #f1f5f9; }
  .card .value.token { color: #a78bfa; }
  .card .value.server { color: #34d399; }
  .card .value.tool { color: #f472b6; }
  .meta { text-align: center; font-size: 0.875rem; color: #64748b; margin-top: 1.5rem; }
  .meta span { margin: 0 0.75rem; }
  .error-card {
    background: #1e293b; border-radius: 0.75rem; padding: 2rem;
    border: 1px solid #ef4444; text-align: center;
  }
  .error-card .value { font-size: 1rem; color: #fca5a5; }
  .section-title {
    font-size: 1.1rem; font-weight: 600; color: #e2e8f0;
    margin: 1.5rem 0 0.75rem;
  }
  .server-table, .drilldown-table, .prompt-table {
    width: 100%; border-collapse: collapse; margin-bottom: 1rem;
    background: #1e293b; border-radius: 0.75rem; overflow: hidden;
  }
  .server-table th, .drilldown-table th, .prompt-table th {
    text-align: left; padding: 0.75rem 1rem;
    font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em;
    color: #94a3b8; border-bottom: 1px solid #334155;
  }
  .server-table td, .drilldown-table td, .prompt-table td {
    padding: 0.75rem 1rem; border-bottom: 1px solid #1e293b;
    font-size: 0.875rem;
  }
  .server-table tbody tr:hover, .drilldown-table tbody tr:hover {
    background: #334155; cursor: pointer;
  }
  .cell-server { color: #34d399; font-weight: 600; }
  .cell-tool { color: #f472b6; font-weight: 600; }
  .cell-tokens { color: #a78bfa; font-family: monospace; }
  .cell-count { color: #e2e8f0; font-family: monospace; }
  .cell-avg { color: #94a3b8; font-family: monospace; }
  .cell-time { color: #64748b; font-family: monospace; font-size: 0.75rem; }
  .cell-arrow { color: #475569; text-align: right; font-size: 1.1rem; }
  .cell-hash code { color: #facc15; font-size: 0.8rem; }
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
  .prompt-panel {
    background: #0f172a; border-radius: 0.75rem; padding: 1rem;
    border: 1px solid #334155; margin-top: 1rem;
  }
  .prompt-panel h3 { font-size: 0.95rem; color: #facc15; margin-bottom: 0.75rem; }
  .badge {
    font-size: 0.65rem; background: #334155; color: #94a3b8;
    padding: 0.15rem 0.5rem; border-radius: 0.5rem; vertical-align: middle;
  }
  .empty-state { color: #64748b; text-align: center; padding: 2rem; font-style: italic; }
  @media (max-width: 600px) {
    .cards { grid-template-columns: repeat(2, 1fr); }
  }
</style>
</head>
<body>
<div class="container">
  <h1>LeanProxy Cost Dashboard</h1>
  <div id="dashboard-cards" hx-get="/api/dashboard" hx-trigger="every 5s" hx-swap="innerHTML">
    {{template "cards" .}}
  </div>
  <div class="meta">
    <span>Auto-refresh every 5s</span>
    <span>&middot;</span>
    <span>{{.ServerCount}} servers</span>
    <span>&middot;</span>
    <span>{{.ToolCount}} tools</span>
  </div>
  <div class="section-title">Servers</div>
  <div id="server-table" hx-get="/api/dashboard/servers" hx-trigger="every 5s" hx-swap="innerHTML">
    {{template "serverRows" .Servers}}
  </div>
  <div id="drilldown-content"></div>
</div>
</body>
</html>
`))

var cardsTemplate = template.Must(template.New("cards").Parse(`
<div class="cards">
  <div class="card">
    <div class="label">Today&rsquo;s Spend</div>
    <div class="value token">{{.TodaySpend}}</div>
  </div>
  <div class="card">
    <div class="label">WTD Spend</div>
    <div class="value token">{{.WTDSpend}}</div>
  </div>
  <div class="card">
    <div class="label">Top Server</div>
    <div class="value server">{{.TopServer}}</div>
  </div>
  <div class="card">
    <div class="label">Top Tool</div>
    <div class="value tool">{{.TopTool}}</div>
  </div>
</div>
`))

var drilldownTemplates = template.Must(template.ParseFS(viewsFS, "views/drilldown.html"))

func ListenAndServe(cfg Config, logger *slog.Logger) (*http.Server, error) {
	globalLogger = logger

	if cfg.Bind == "" || cfg.Bind == "off" {
		logger.Info("dashboard endpoint disabled")
		return nil, nil
	}

	host, _, err := net.SplitHostPort(cfg.Bind)
	if err != nil {
		if ip := net.ParseIP(cfg.Bind); ip != nil && strings.Contains(cfg.Bind, ":") {
			return nil, fmt.Errorf("invalid dashboard bind address %q: IPv6 addresses must be bracketed, e.g. [::1]:9090", cfg.Bind)
		}
		return nil, fmt.Errorf("invalid dashboard bind address %q: %w", cfg.Bind, err)
	}

	if host == "" || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		logger.Warn("dashboard endpoint listening on non-loopback interface; data is not encrypted",
			"bind", cfg.Bind)
	}

	ln, err := net.Listen("tcp", cfg.Bind)
	if err != nil {
		return nil, fmt.Errorf("dashboard listen failed: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/dashboard", handleDashboardJSON)
	mux.HandleFunc("GET /api/dashboard/json", func(w http.ResponseWriter, r *http.Request) {
		DashboardJSON(w, r)
	})
	mux.HandleFunc("GET /api/dashboard/servers", handleServerTable)
	mux.HandleFunc("GET /api/dashboard/servers/{server}", handleServerDrilldown)
	mux.HandleFunc("GET /api/dashboard/servers/{server}/tools/{tool}/prompts", handleToolPrompts)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(assetsFS))))
	mux.HandleFunc("GET /{$}", handleDashboardIndex)

	handler := requireBearerToken(cfg.Token, logger)(mux)

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

func collectDashboardData() DashboardData {
	snap := metrics.Snapshot()
	tracker := reporter.GlobalCostTracker()

	sort.Slice(snap.ByServer, func(i, j int) bool {
		return snap.ByServer[i].TokenCount > snap.ByServer[j].TokenCount
	})
	sort.Slice(snap.ByTool, func(i, j int) bool {
		return snap.ByTool[i].TokenCount > snap.ByTool[j].TokenCount
	})

	data := DashboardData{
		TodaySpend:  formatTokens(snap.TotalSpend),
		WTDSpend:    formatTokens(snap.TotalSpend),
		ServerCount: len(snap.ByServer),
		ToolCount:   len(snap.ByTool),
		Servers:     make([]ServerRow, 0, len(snap.ByServer)),
	}

	if len(snap.ByServer) > 0 {
		data.TopServer = snap.ByServer[0].ServerName
	} else {
		data.TopServer = "-"
	}

	if len(snap.ByTool) > 0 {
		data.TopTool = snap.ByTool[0].ToolName
	} else {
		data.TopTool = "-"
	}

	for _, s := range snap.ByServer {
		stats := tracker.GetServerToolStats(s.ServerName)
		data.Servers = append(data.Servers, ServerRow{
			Name:       s.ServerName,
			ToolCount:  len(stats),
			TokenCount: formatTokens(s.TokenCount),
		})
	}

	return data
}

var globalLogger = slog.Default()

func handleDashboardIndex(w http.ResponseWriter, r *http.Request) {
	data := collectDashboardData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, data); err != nil {
		globalLogger.Error("failed to render dashboard index", "error", err)
	}
}

func handleDashboardJSON(w http.ResponseWriter, r *http.Request) {
	data := collectDashboardData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cardsTemplate.Execute(w, data); err != nil {
		globalLogger.Error("failed to render dashboard cards", "error", err)
	}
}

func handleServerTable(w http.ResponseWriter, r *http.Request) {
	data := collectDashboardData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := drilldownTemplates.ExecuteTemplate(w, "serverRows", data.Servers); err != nil {
		globalLogger.Error("failed to render server table", "error", err)
	}
}

func handleServerDrilldown(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server")
	if serverName == "" {
		http.Error(w, "server name required", http.StatusBadRequest)
		return
	}
	serverName, err := url.QueryUnescape(serverName)
	if err != nil {
		http.Error(w, "invalid server name", http.StatusBadRequest)
		return
	}

	since := parseSinceParam(r)
	dd := metrics.ServerDrilldown(serverName, since)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := drilldownTemplates.ExecuteTemplate(w, "drilldown", dd); err != nil {
		globalLogger.Error("failed to render drilldown", "error", err)
	}
}

func handleToolPrompts(w http.ResponseWriter, r *http.Request) {
	serverName := r.PathValue("server")
	toolName := r.PathValue("tool")
	if serverName == "" || toolName == "" {
		http.Error(w, "server and tool name required", http.StatusBadRequest)
		return
	}
	var err error
	serverName, err = url.QueryUnescape(serverName)
	if err != nil {
		http.Error(w, "invalid server name", http.StatusBadRequest)
		return
	}
	toolName, err = url.QueryUnescape(toolName)
	if err != nil {
		http.Error(w, "invalid tool name", http.StatusBadRequest)
		return
	}

	ph := metrics.ServerToolPromptHashes(serverName, toolName)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := drilldownTemplates.ExecuteTemplate(w, "prompts", ph); err != nil {
		globalLogger.Error("failed to render prompts", "error", err)
	}
}

func parseSinceParam(r *http.Request) time.Time {
	daysStr := r.URL.Query().Get("days")
	if daysStr == "" {
		daysStr = r.URL.Query().Get("since")
	}
	if daysStr == "" {
		return time.Time{}
	}
	var days int
	if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil || days <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour)
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

func DashboardJSON(w http.ResponseWriter, r *http.Request) {
	snap := metrics.Snapshot()

	sort.Slice(snap.ByServer, func(i, j int) bool {
		return snap.ByServer[i].TokenCount > snap.ByServer[j].TokenCount
	})
	sort.Slice(snap.ByTool, func(i, j int) bool {
		return snap.ByTool[i].TokenCount > snap.ByTool[j].TokenCount
	})

	var topServer, topTool string
	if len(snap.ByServer) > 0 {
		topServer = snap.ByServer[0].ServerName
	}
	if len(snap.ByTool) > 0 {
		topTool = snap.ByTool[0].ToolName
	}

	resp := map[string]interface{}{
		"today_spend":  snap.TotalSpend,
		"wtd_spend":    snap.TotalSpend,
		"top_server":   topServer,
		"top_tool":     topTool,
		"server_count": len(snap.ByServer),
		"tool_count":   len(snap.ByTool),
	}

	perServer := make([]map[string]interface{}, 0, len(snap.ByServer))
	for _, s := range snap.ByServer {
		perServer = append(perServer, map[string]interface{}{
			"server": s.ServerName,
			"tokens": s.TokenCount,
		})
	}

	perTool := make([]map[string]interface{}, 0, len(snap.ByTool))
	for _, t := range snap.ByTool {
		perTool = append(perTool, map[string]interface{}{
			"tool":   t.ToolName,
			"tokens": t.TokenCount,
		})
	}

	resp["per_server"] = perServer
	resp["per_tool"] = perTool

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		enc.SetIndent("", "")
	}
	if err := enc.Encode(resp); err != nil {
		globalLogger.Error("failed to encode dashboard JSON", "error", err)
	}
}
