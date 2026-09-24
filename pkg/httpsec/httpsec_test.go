package httpsec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"LOCALHOST", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"0.0.0.0", false},
		{"evil.example", false},
		{"10.0.0.1", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsLoopbackHost(tt.host); got != tt.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestIsLoopbackBindAddr(t *testing.T) {
	tests := []struct {
		bind string
		want bool
	}{
		{"127.0.0.1:9090", true},
		{"localhost:9090", true},
		{"[::1]:9090", true},
		{"0.0.0.0:9090", false},
		{":9090", false},
		{"10.0.0.1:9090", false},
		{"not-a-valid-addr", false},
	}
	for _, tt := range tests {
		if got := IsLoopbackBindAddr(tt.bind); got != tt.want {
			t.Errorf("IsLoopbackBindAddr(%q) = %v, want %v", tt.bind, got, tt.want)
		}
	}
}

func TestAllowedHosts(t *testing.T) {
	allowed := AllowedHosts("127.0.0.1", "9090", []string{"dashboard.internal", "extra.example:8443"})

	for _, want := range []string{
		"127.0.0.1:9090",
		"localhost:9090",
		"[::1]:9090",
		"dashboard.internal:9090",
		"extra.example:8443",
	} {
		if _, ok := allowed[want]; !ok {
			t.Errorf("expected %q in allowed hosts, got %v", want, allowed)
		}
	}
	if _, ok := allowed["evil.example:9090"]; ok {
		t.Error("evil.example:9090 should not be allowed")
	}
}

func TestAllowedHostsWildcardBind(t *testing.T) {
	allowed := AllowedHosts("0.0.0.0", "9090", nil)
	if _, ok := allowed["0.0.0.0:9090"]; ok {
		t.Error("0.0.0.0 must not be added as an allowed Host value")
	}
	if _, ok := allowed["127.0.0.1:9090"]; !ok {
		t.Error("127.0.0.1:9090 should still be allowed on a wildcard bind")
	}
}

func TestValidateHostRejectsUnknownHost(t *testing.T) {
	allowed := AllowedHosts("127.0.0.1", "9090", nil)
	handler := ValidateHost(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil.example"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestValidateHostAllowsKnownHost(t *testing.T) {
	allowed := AllowedHosts("127.0.0.1", "9090", nil)
	called := false
	handler := ValidateHost(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:9090"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called for allowed host")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestValidateHostRejectsCrossOriginStateChange(t *testing.T) {
	allowed := AllowedHosts("127.0.0.1", "9090", nil)
	handler := ValidateHost(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "127.0.0.1:9090"
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestValidateHostAllowsSameOriginStateChange(t *testing.T) {
	allowed := AllowedHosts("127.0.0.1", "9090", nil)
	called := false
	handler := ValidateHost(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "127.0.0.1:9090"
	req.Header.Set("Origin", "http://127.0.0.1:9090")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called for same-origin state change")
	}
}

func TestValidateHostAllowsGetWithoutOriginCheck(t *testing.T) {
	allowed := AllowedHosts("127.0.0.1", "9090", nil)
	called := false
	handler := ValidateHost(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:9090"
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("GET requests should not be Origin-checked")
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	tests := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
	}
	for header, want := range tests {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	allowed := OriginSet([]string{" https://App.Example/ ", ""})
	if len(allowed) != 1 {
		t.Fatalf("OriginSet = %v", allowed)
	}
	tests := []struct {
		origin, host string
		want         bool
	}{
		{"https://app.example", "127.0.0.1:8765", true},
		{"HTTPS://APP.EXAMPLE/", "127.0.0.1:8765", true},
		{"http://127.0.0.1:8765", "127.0.0.1:8765", true},
		{"http://localhost:8765", "LOCALHOST:8765", true},
		{"https://evil.example", "127.0.0.1:8765", false},
		{"http://127.0.0.1:9999", "127.0.0.1:8765", false},
		{"null", "127.0.0.1:8765", false},
		{"", "127.0.0.1:8765", false},
	}
	for _, tt := range tests {
		if got := OriginAllowed(tt.origin, tt.host, allowed); got != tt.want {
			t.Errorf("OriginAllowed(%q, %q) = %v, want %v", tt.origin, tt.host, got, tt.want)
		}
	}
}

func TestValidBearer(t *testing.T) {
	token := "tok-" + "0123456789abcdef"
	tests := []struct {
		header string
		want   bool
	}{
		{"Bearer " + token, true},
		{"bearer " + token, true},
		{"Bearer " + token + "x", false},
		{"Bearer", false},
		{"Basic " + token, false},
		{"", false},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if tt.header != "" {
			r.Header.Set("Authorization", tt.header)
		}
		if got := ValidBearer(r, token); got != tt.want {
			t.Errorf("ValidBearer(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer ")
	if ValidBearer(r, "") {
		t.Error("an empty token must never match")
	}
}
