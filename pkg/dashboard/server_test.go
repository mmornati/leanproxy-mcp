package dashboard

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

func waitForServer(addr string) bool {
	for i := 0; i < 20; i++ {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func TestListenAndServeDisabled(t *testing.T) {
	srv, err := ListenAndServe(Config{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error for empty config: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server for empty bind")
	}

	srv, err = ListenAndServe(Config{Bind: "off"}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error for 'off' bind: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server for 'off' bind")
	}
}

func TestListenAndServeInvalidAddr(t *testing.T) {
	_, err := ListenAndServe(Config{Bind: "not-a-valid-addr"}, slog.Default())
	if err == nil {
		t.Error("expected error for invalid address")
	}
}

func TestDashboardIndexRenders(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "127.0.0.1:0"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	defer srv.Close()

	if !waitForServer(srv.Addr) {
		t.Fatal("server did not start within timeout")
	}

	resp, err := http.Get("http://" + srv.Addr + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboardCardsEndpoint(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "127.0.0.1:0"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	defer srv.Close()

	if !waitForServer(srv.Addr) {
		t.Fatal("server did not start within timeout")
	}

	resp, err := http.Get("http://" + srv.Addr + "/api/dashboard")
	if err != nil {
		t.Fatalf("GET /api/dashboard failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// fixtureSummary is two servers' worth of today / week-to-date usage.
func fixtureSummary() *metrics.UsageSummary {
	sum := &metrics.UsageSummary{Estimator: "chars/4"}
	sum.Today = metrics.UsageWindow{Sessions: 1, OriginalTokens: 12000, SavedTokens: 6000, SavedPercent: 50}
	sum.Today.SetTools([]metrics.UsageTool{
		{Server: "server-1", Tool: "tool-a", Calls: 2, OriginalTokens: 1000, ReturnedTokens: 1000},
		{Server: "server-1", Tool: "tool-b", Calls: 1, OriginalTokens: 2000, ReturnedTokens: 500, SavedTokens: 1500},
		{Server: "server-2", Tool: "tool-c", Calls: 4, OriginalTokens: 9000, ReturnedTokens: 4500, SavedTokens: 4500},
	})
	sum.Week = metrics.UsageWindow{Sessions: 2, OriginalTokens: 50000, SavedTokens: 20000, SavedPercent: 40}
	sum.Week.SetTools([]metrics.UsageTool{
		{Server: "server-1", Tool: "tool-a", Calls: 20, OriginalTokens: 30000, ReturnedTokens: 20000, SavedTokens: 10000},
	})
	return sum
}

func fixtureServer() *server {
	sum := fixtureSummary()
	return &server{usage: func() *metrics.UsageSummary { return sum }}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestDashboardJSONServesUsageSummary(t *testing.T) {
	rec := get(t, fixtureServer().routes(), "/api/dashboard/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var sum metrics.UsageSummary
	if err := json.NewDecoder(rec.Body).Decode(&sum); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sum.Today.SavedTokens != 6000 || sum.Week.SavedTokens != 20000 {
		t.Errorf("today/week saved = %d/%d, want 6000/20000 (week is not today's value)", sum.Today.SavedTokens, sum.Week.SavedTokens)
	}
	if sum.Today.TopServer != "server-2" || sum.Today.TopTool != "server-2.tool-c" {
		t.Errorf("top = %q / %q", sum.Today.TopServer, sum.Today.TopTool)
	}
	if len(sum.Today.ByServer) != 2 || len(sum.Today.ByTool) != 3 {
		t.Errorf("by_server/by_tool = %d/%d, want 2/3", len(sum.Today.ByServer), len(sum.Today.ByTool))
	}
}

// Without a usage source the JSON endpoint says so instead of serving
// zeros, and the page shows an unavailable state.
func TestDashboardWithoutUsageSource(t *testing.T) {
	h := (&server{}).routes()
	if rec := get(t, h, "/api/dashboard/json"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("JSON status = %d, want 503", rec.Code)
	}
	rec := get(t, h, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Usage data unavailable") {
		t.Errorf("index = %d %s", rec.Code, rec.Body.String())
	}
}

func TestDashboardIndexRendersCardsAndServers(t *testing.T) {
	rec := get(t, fixtureServer().routes(), "/")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// The index includes the cards and server-table partials (it used to
	// fail mid-render: they were parsed into a separate template set).
	for _, want := range []string{"Saved today", "6.0K", "Saved this week", "20.0K", "40.0%", "server-2.tool-c", "server-1", "</html>"} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	for _, dead := range []string{"Spend", "Prompt Hashes"} {
		if strings.Contains(body, dead) {
			t.Errorf("index still shows %q", dead)
		}
	}
}

func TestDashboardCardsPartial(t *testing.T) {
	rec := get(t, fixtureServer().routes(), "/api/dashboard")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Top tool today") {
		t.Fatalf("cards = %d %s", rec.Code, rec.Body.String())
	}
}

// With no per-tool data (the governor is off by default), the server
// table explains where the figures come from.
func TestDashboardServerTableEmptyExplainsGovernor(t *testing.T) {
	sum := &metrics.UsageSummary{}
	sum.Today.SetTools(nil)
	h := (&server{usage: func() *metrics.UsageSummary { return sum }}).routes()
	rec := get(t, h, "/api/dashboard/servers")
	if !strings.Contains(rec.Body.String(), "response.enabled: true") {
		t.Fatalf("server table = %s", rec.Body.String())
	}
}

func TestDashboardJSONMethodNotAllowed(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "127.0.0.1:0"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	defer srv.Close()

	if !waitForServer(srv.Addr) {
		t.Fatal("server did not start within timeout")
	}

	resp, err := http.Post("http://"+srv.Addr+"/", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestDashboardAuthRequiredNonLoopback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", (&server{}).handleIndex)
	handler := requireBearerToken("mytoken", slog.Default())(mux)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without auth", w.Code)
	}
}

// TestDashboardAuthLoopbackRequiresTokenToo covers issue #316: a configured
// token is required from every client, including loopback ones.
func TestDashboardAuthLoopbackRequiresTokenToo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", (&server{}).handleIndex)
	handler := requireBearerToken("mytoken", slog.Default())(mux)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for loopback without token", w.Code)
	}
}

func TestDashboardAuthValidToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", (&server{}).handleIndex)
	handler := requireBearerToken("mytoken", slog.Default())(mux)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer mytoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with valid token", w.Code)
	}
}

func TestDashboardHTMLContent(t *testing.T) {

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", (&server{}).handleIndex)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatTokens(tt.input)
			if got != tt.want {
				t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDashboardServerTableEndpoint(t *testing.T) {
	rec := get(t, fixtureServer().routes(), "/api/dashboard/servers")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `hx-get="/api/dashboard/servers/server-2"`) || !strings.Contains(body, "9.0K") {
		t.Errorf("server table = %s", body)
	}
}

func TestDashboardServerDrilldownEndpoint(t *testing.T) {
	rec := get(t, fixtureServer().routes(), "/api/dashboard/servers/server-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Server: server-1", "tool-a", "tool-b", "3.0K response tokens today", "500.0"} {
		if !strings.Contains(body, want) {
			t.Errorf("drilldown missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "tool-c") {
		t.Error("drilldown shows another server's tool")
	}
}

// Server names are escaped in the drill-down link and decoded back.
func TestDashboardServerDrilldownEscapedName(t *testing.T) {
	sum := &metrics.UsageSummary{}
	sum.Today.SetTools([]metrics.UsageTool{{Server: "my server", Tool: "t", Calls: 1, OriginalTokens: 10}})
	h := (&server{usage: func() *metrics.UsageSummary { return sum }}).routes()
	if body := get(t, h, "/api/dashboard/servers").Body.String(); !strings.Contains(body, "/api/dashboard/servers/my%20server") {
		t.Fatalf("link not escaped: %s", body)
	}
	if body := get(t, h, "/api/dashboard/servers/my%20server").Body.String(); !strings.Contains(body, "Server: my server") || !strings.Contains(body, ">t<") {
		t.Fatalf("drilldown = %s", body)
	}
}

func TestDashboardPromptHashRouteRemoved(t *testing.T) {
	rec := get(t, fixtureServer().routes(), "/api/dashboard/servers/server-1/tools/tool-a/prompts")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (the usage store never records prompt hashes)", rec.Code)
	}
}

// The following tests cover issue #316 (dashboard hardening): Host/Origin
// validation, refusing to start a non-loopback bind without a token,
// security headers on every response, and the /login cookie exchange.

func TestListenAndServeNonLoopbackBindRefusesWithoutToken(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "0.0.0.0:0"}, slog.Default())
	if err == nil {
		if srv != nil {
			srv.Close()
		}
		t.Fatal("expected an error starting a non-loopback dashboard bind without a token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error %q does not mention the missing token", err.Error())
	}
}

func TestListenAndServeNonLoopbackBindStartsWithToken(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "0.0.0.0:0", Token: "mytoken"}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv == nil {
		t.Fatal("expected a non-nil server")
	}
	defer srv.Close()
}

func TestDashboardRejectsUnknownHostHeader(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "127.0.0.1:0"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	defer srv.Close()
	if !waitForServer(srv.Addr) {
		t.Fatal("server did not start within timeout")
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for Host: evil.example", resp.StatusCode)
	}
}

func TestDashboardAllowsConfiguredHostHeader(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "127.0.0.1:0"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	defer srv.Close()
	if !waitForServer(srv.Addr) {
		t.Fatal("server did not start within timeout")
	}

	resp, err := http.Get("http://" + srv.Addr + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for the server's own host:port", resp.StatusCode)
	}
}

func TestDashboardSecurityHeaders(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "127.0.0.1:0"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	defer srv.Close()
	if !waitForServer(srv.Addr) {
		t.Fatal("server did not start within timeout")
	}

	for _, path := range []string{"/", "/api/dashboard", "/api/dashboard/servers"} {
		resp, err := http.Get("http://" + srv.Addr + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		resp.Body.Close()

		checks := map[string]string{
			"Content-Security-Policy": "default-src 'self'; script-src 'self'",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
			"X-Content-Type-Options":  "nosniff",
		}
		for header, want := range checks {
			if got := resp.Header.Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
	}
}

func TestDashboardLoginCookieFlow(t *testing.T) {
	srv, err := ListenAndServe(Config{Bind: "127.0.0.1:0", Token: "mytoken"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	defer srv.Close()
	if !waitForServer(srv.Addr) {
		t.Fatal("server did not start within timeout")
	}

	base := "http://" + srv.Addr

	// No credentials at all: even a loopback client must now be rejected
	// once a token is configured (issue #316 removes the loopback bypass).
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without credentials = %d, want 401", resp.StatusCode)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	loginResp, err := client.Get(base + "/login?token=mytoken")
	if err != nil {
		t.Fatalf("GET /login failed: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200", loginResp.StatusCode)
	}

	// The cookie the client jar now holds should authenticate subsequent
	// requests without an Authorization header.
	resp2, err := client.Get(base + "/")
	if err != nil {
		t.Fatalf("GET / with cookie failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status with cookie = %d, want 200", resp2.StatusCode)
	}

	// A wrong token at /login gets 401 and no cookie is set.
	badResp, err := http.Get(base + "/login?token=wrongtoken")
	if err != nil {
		t.Fatalf("GET /login (wrong token) failed: %v", err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status for wrong login token = %d, want 401", badResp.StatusCode)
	}
}

func TestDashboardToolPinsEndpoint(t *testing.T) {
	store, err := toolpin.OpenStore(filepath.Join(t.TempDir(), "pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := toolpin.NewWithOptions(toolpin.Options{Mode: toolpin.ModeWarn, Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.Observe("dashsrv", nil, []toolpin.Definition{{Name: "t", Description: "one"}})
	p.Observe("dashsrv", nil, []toolpin.Definition{{Name: "t", Description: "<script>alert(1)</script> two"}})

	rec := httptest.NewRecorder()
	handleToolPins(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard/tool-pins", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "tool_changed") || !strings.Contains(body, "dashsrv") {
		t.Fatalf("tool pinning events missing: %s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("event details must be HTML-escaped: %s", body)
	}
}
