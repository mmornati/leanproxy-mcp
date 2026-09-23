package mcpregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestServer serves the recorded fixtures under testdata/ so no test in
// this package ever reaches the real network. It emulates GET
// /v0/servers (paginated by cursor) and GET /v0/servers/{name}.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/servers", func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		file := "testdata/servers_page1.json"
		if cursor == "page2" {
			file = "testdata/servers_page2.json"
		}
		serveFixture(t, w, file)
	})
	mux.HandleFunc("/v0/servers/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/v0/servers/") {
		case "io.github.example/weather", "io.github.example%2Fweather":
			serveFixture(t, w, "testdata/server_weather.json")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	})
	return httptest.NewServer(mux)
}

func serveFixture(t *testing.T, w http.ResponseWriter, path string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- fixed testdata path under this package
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func TestListServers_FirstPage(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	c := New().WithBaseURL(ts.URL)
	page, err := c.ListServers(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(page.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(page.Servers))
	}
	if page.NextCursor != "page2" {
		t.Errorf("expected next_cursor=page2, got %q", page.NextCursor)
	}
	if page.Servers[0].Name != "io.github.example/weather" {
		t.Errorf("unexpected first server: %+v", page.Servers[0])
	}
}

func TestListAll_WalksAllPages(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	c := New().WithBaseURL(ts.URL)
	all, err := c.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 servers across both pages, got %d", len(all))
	}
}

func TestSearchServers_FiltersByNameAndDescription(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	c := New().WithBaseURL(ts.URL)
	matches, err := c.SearchServers(context.Background(), "weather", 10)
	if err != nil {
		t.Fatalf("SearchServers: %v", err)
	}
	if len(matches) != 1 || matches[0].Name != "io.github.example/weather" {
		t.Fatalf("unexpected matches: %+v", matches)
	}

	matches, err = c.SearchServers(context.Background(), "mcp server", 10)
	if err != nil {
		t.Fatalf("SearchServers: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("expected all 3 to match generic description text, got %d", len(matches))
	}
}

func TestSearchServers_LimitStopsEarly(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	c := New().WithBaseURL(ts.URL)
	matches, err := c.SearchServers(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("SearchServers: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected limit to cap results at 1, got %d", len(matches))
	}
}

func TestGetServer_ByName(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	c := New().WithBaseURL(ts.URL)
	s, err := c.GetServer(context.Background(), "io.github.example/weather", "")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if s.Version != "1.4.0" {
		t.Errorf("Version = %q, want 1.4.0", s.Version)
	}
	if len(s.Packages) != 1 || s.Packages[0].Identifier != "@example/weather-mcp" {
		t.Errorf("unexpected packages: %+v", s.Packages)
	}
	if s.Meta.Official == nil || !s.Meta.Official.IsVerified {
		t.Errorf("expected Meta.Official.IsVerified = true, got %+v", s.Meta.Official)
	}
}

func TestGetServer_NotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	c := New().WithBaseURL(ts.URL)
	_, err := c.GetServer(context.Background(), "missing", "")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected error to mention 404, got %v", err)
	}
}

func TestGetServer_EmptyName(t *testing.T) {
	c := New()
	if _, err := c.GetServer(context.Background(), "  ", ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestClient_ResponseSizeLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[`))
		big := strings.Repeat("x", maxResponseBytes+1024)
		_, _ = w.Write([]byte(`{"name":"` + big + `"}]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New().WithBaseURL(ts.URL).WithHTTPClient(&http.Client{Timeout: 5 * time.Second})
	_, err := c.ListServers(context.Background(), "", 0)
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size-limit error, got %v", err)
	}
}

func TestClient_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer ts.Close()

	c := New().WithBaseURL(ts.URL)
	_, err := c.ListServers(context.Background(), "", 0)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got %v", err)
	}
}

func TestClient_RequestHasTimeout(t *testing.T) {
	c := New()
	if c.client.Timeout <= 0 {
		t.Error("default client must have a non-zero timeout")
	}
}

func TestDefaultBaseURL_IsModelContextProtocolDomain(t *testing.T) {
	// Regression guard for issue #313: the previous default
	// (registry.mcp.io) was a domain the project does not own. The
	// official registry lives under modelcontextprotocol.io.
	if !strings.Contains(DefaultBaseURL, "modelcontextprotocol.io") {
		t.Errorf("DefaultBaseURL = %q, want it to reference modelcontextprotocol.io", DefaultBaseURL)
	}
}
