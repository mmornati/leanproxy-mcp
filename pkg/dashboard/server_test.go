package dashboard

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
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

func TestDashboardAuthSkippedLoopback(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", handleDashboardIndex)
	handler := requireBearerToken("mytoken", slog.Default())(mux)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for loopback", w.Code)
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
