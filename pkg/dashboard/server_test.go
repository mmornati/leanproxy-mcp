package dashboard

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("test-tool", "test-server", 500)

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

func TestDashboardJSONEndpoint(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-b", "server-1", 200)
	reporter.TrackCost("tool-c", "server-2", 50)

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

func TestDashboardAPI(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-b", "server-1", 200)
	reporter.TrackCost("tool-c", "server-2", 50)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard", DashboardJSON)
	mux.HandleFunc("GET /{$}", handleDashboardIndex)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard")
	if err != nil {
		t.Fatalf("GET /api/dashboard failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if v, ok := data["today_spend"].(float64); !ok || v != 350 {
		t.Errorf("today_spend = %v, want 350", data["today_spend"])
	}
	if v, ok := data["wtd_spend"].(float64); !ok || v != 350 {
		t.Errorf("wtd_spend = %v, want 350", data["wtd_spend"])
	}
	if v, ok := data["top_server"].(string); !ok || v != "server-1" {
		t.Errorf("top_server = %v, want server-1", data["top_server"])
	}
	if v, ok := data["top_tool"].(string); !ok || v != "tool-b" {
		t.Errorf("top_tool = %v, want tool-b", data["top_tool"])
	}
}

func TestDashboardAPIEmptyData(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard", DashboardJSON)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard")
	if err != nil {
		t.Fatalf("GET /api/dashboard failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	if v, ok := data["today_spend"].(float64); !ok || v != 0 {
		t.Errorf("today_spend = %v, want 0", data["today_spend"])
	}
	if v, ok := data["top_server"].(string); !ok || v != "" {
		t.Errorf("top_server = %v, want empty", data["top_server"])
	}
}

func TestDashboardJSONMethodNotAllowed(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleDashboardIndex)
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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleDashboardIndex)
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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleDashboardIndex)
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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("test-tool", "test-server", 1000)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleDashboardIndex)

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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-b", "server-1", 200)
	reporter.TrackCost("tool-c", "server-2", 50)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard/servers", handleServerTable)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard/servers")
	if err != nil {
		t.Fatalf("GET /api/dashboard/servers failed: %v", err)
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

func TestDashboardServerDrilldownEndpoint(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-b", "server-1", 200)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard/servers/{server}", handleServerDrilldown)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard/servers/server-1")
	if err != nil {
		t.Fatalf("GET /api/dashboard/servers/server-1 failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboardServerDrilldownEndpointInvalid(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard/servers/{server}", handleServerDrilldown)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard/servers/")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
}

func TestDashboardToolPromptsEndpoint(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCostFromStrings("tool-a", "server-1", `{"q":"hi"}`, `{"a":"there"}`)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard/servers/{server}/tools/{tool}/prompts", handleToolPrompts)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard/servers/server-1/tools/tool-a/prompts")
	if err != nil {
		t.Fatalf("GET /prompts failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDashboardPerServerPerTool(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-b", "server-2", 200)
	reporter.TrackCost("tool-c", "server-1", 300)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard", DashboardJSON)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/dashboard")
	if err != nil {
		t.Fatalf("GET /api/dashboard failed: %v", err)
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode: %v", err)
	}

	perServer, ok := data["per_server"].([]interface{})
	if !ok || len(perServer) != 2 {
		t.Fatalf("per_server = %v, want 2 entries", data["per_server"])
	}

	perTool, ok := data["per_tool"].([]interface{})
	if !ok || len(perTool) != 3 {
		t.Fatalf("per_tool = %v, want 3 entries", data["per_tool"])
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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

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
