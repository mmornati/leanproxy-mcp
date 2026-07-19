package metrics

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("test-tool", "test-server", 500)

	srv, err := ListenAndServe("127.0.0.1:0", slog.Default())
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

	var snap MetricsSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if snap.TotalSpend != 500 {
		t.Errorf("TotalSpend = %d, want 500", snap.TotalSpend)
	}
	if len(snap.ByServer) != 1 || snap.ByServer[0].ServerName != "test-server" {
		t.Errorf("ByServer: %+v", snap.ByServer)
	}
	if len(snap.ByTool) != 1 || snap.ByTool[0].ToolName != "test-tool" {
		t.Errorf("ByTool: %+v", snap.ByTool)
	}
}

func TestMetricsEndpointMethodNotAllowed(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

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
	reporter.GlobalCostTracker().Reset()
	defer reporter.GlobalCostTracker().Reset()

	reporter.TrackCost("tool-x", "server-x", 999)

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
