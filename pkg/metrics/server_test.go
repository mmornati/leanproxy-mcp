package metrics

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListenAndServeDisabled(t *testing.T) {
	srv, err := ListenAndServe("", slog.Default())
	if err != nil {
		t.Fatalf("unexpected error for empty addr: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server for empty addr")
	}

	srv, err = ListenAndServe("off", slog.Default())
	if err != nil {
		t.Fatalf("unexpected error for 'off' addr: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server for 'off' addr")
	}
}

func TestListenAndServeMetricsEndpoint(t *testing.T) {
	usage := &UsageSummary{Estimator: "chars/4"}
	usage.Today.SavedTokens = 500
	usage.Today.SetTools([]UsageTool{{Server: "test-server", Tool: "test-tool", Calls: 1, OriginalTokens: 800, ReturnedTokens: 300, SavedTokens: 500}})
	usage.Week = usage.Today

	srv, err := ListenAndServeConfig(Config{Bind: "127.0.0.1:0", Usage: func() *UsageSummary { return usage }}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	addr := srv.Addr
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var snap endpointSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if snap.Usage == nil {
		t.Fatalf("usage section missing: %s", body)
	}
	if snap.Usage.Today.SavedTokens != 500 {
		t.Errorf("usage.today.saved_tokens = %d, want 500", snap.Usage.Today.SavedTokens)
	}
	if snap.Usage.Today.TopServer != "test-server" || snap.Usage.Today.TopTool != "test-server.test-tool" {
		t.Errorf("top server/tool = %q/%q", snap.Usage.Today.TopServer, snap.Usage.Today.TopTool)
	}
	if len(snap.Usage.Week.ByServer) != 1 || snap.Usage.Week.ByServer[0].Server != "test-server" {
		t.Errorf("usage.week.by_server: %+v", snap.Usage.Week.ByServer)
	}
	// The tracker-fed fields that were always empty are gone.
	for _, dead := range []string{"total_spend", "top_5_expensive_tools", `"by_tool":null`} {
		if strings.Contains(string(body), dead) {
			t.Errorf("response still carries %s: %s", dead, body)
		}
	}
}

// Without a usage provider (or when it returns nil) the section is
// omitted; the live counters are still served.
func TestMetricsEndpointOmitsUsageWithoutProvider(t *testing.T) {
	for name, usage := range map[string]func() *UsageSummary{
		"no provider":  nil,
		"nil provider": func() *UsageSummary { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleMetrics("", usage)(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), `"usage"`) {
				t.Fatalf("usage section present: %s", rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"telemetry"`) {
				t.Fatalf("telemetry section missing: %s", rec.Body.String())
			}
		})
	}
}

func TestMetricsEndpointMethodNotAllowed(t *testing.T) {
	srv, err := ListenAndServe("127.0.0.1:0", slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Post("http://"+srv.Addr+"/metrics", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestListenAndServeInvalidAddr(t *testing.T) {
	_, err := ListenAndServe("not-a-valid-addr", slog.Default())
	if err == nil {
		t.Error("expected error for invalid address")
	}
}

func TestServeConcurrentRequests(t *testing.T) {
	srv, err := ListenAndServe("127.0.0.1:0", slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	type result struct {
		status int
		err    error
	}
	results := make(chan result, 10)
	for i := 0; i < 10; i++ {
		go func() {
			resp, err := http.Get("http://" + srv.Addr + "/metrics")
			if err != nil {
				results <- result{err: err}
				return
			}
			resp.Body.Close()
			results <- result{status: resp.StatusCode}
		}()
	}

	for i := 0; i < 10; i++ {
		r := <-results
		if r.err != nil {
			t.Errorf("concurrent request %d failed: %v", i, r.err)
		} else if r.status != http.StatusOK {
			t.Errorf("concurrent request %d status = %d, want 200", i, r.status)
		}
	}
}

// The following tests cover issue #316: Host validation and an optional
// bearer token on the metrics endpoint, matching the dashboard's.

func TestListenAndServeConfigNonLoopbackBindRefusesWithoutToken(t *testing.T) {
	srv, err := ListenAndServeConfig(Config{Bind: "0.0.0.0:0"}, slog.Default())
	if err == nil {
		if srv != nil {
			srv.Close()
		}
		t.Fatal("expected an error starting a non-loopback metrics bind without a token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error %q does not mention the missing token", err.Error())
	}
}

func TestListenAndServeConfigNonLoopbackBindStartsWithToken(t *testing.T) {
	srv, err := ListenAndServeConfig(Config{Bind: "0.0.0.0:0", Token: "mytoken"}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv == nil {
		t.Fatal("expected a non-nil server")
	}
	defer srv.Close()
}

func TestMetricsRejectsUnknownHostHeader(t *testing.T) {
	srv, err := ListenAndServe("127.0.0.1:0", slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServe failed: %v", err)
	}
	defer srv.Close()
	time.Sleep(50 * time.Millisecond)

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for Host: evil.example", resp.StatusCode)
	}
}

func TestMetricsRequiresConfiguredToken(t *testing.T) {
	srv, err := ListenAndServeConfig(Config{Bind: "127.0.0.1:0", Token: "mytoken"}, slog.Default())
	if err != nil {
		t.Fatalf("ListenAndServeConfig failed: %v", err)
	}
	defer srv.Close()
	time.Sleep(50 * time.Millisecond)

	// No token: 401.
	resp, err := http.Get("http://" + srv.Addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without token = %d, want 401", resp.StatusCode)
	}

	// Wrong token: 401.
	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics with wrong token failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("status with wrong token = %d, want 401", resp2.StatusCode)
	}

	// Correct token: 200.
	req2, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr+"/metrics", nil)
	req2.Header.Set("Authorization", "Bearer mytoken")
	resp3, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /metrics with correct token failed: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("status with correct token = %d, want 200", resp3.StatusCode)
	}
}
