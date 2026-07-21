package dashboard

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireBearerTokenNoToken(t *testing.T) {
	called := false
	handler := requireBearerToken("", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called when no token configured")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRequireBearerTokenLoopbackBypass(t *testing.T) {
	called := false
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called for loopback with token configured")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRequireBearerTokenMissingAuth(t *testing.T) {
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without auth")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRequireBearerTokenWrongAuth(t *testing.T) {
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with wrong token")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer wrongtoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRequireBearerTokenValidAuth(t *testing.T) {
	called := false
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called with valid token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRequireBearerTokenInvalidAuthScheme(t *testing.T) {
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with invalid scheme")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("Authorization", "Basic secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestIsLoopbackLocalhostHost(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost:9090/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	if isLoopback(req) {
		t.Error("expected non-loopback remote addr to NOT be loopback even with Host header")
	}
}

func TestIsLoopbackRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com:9090/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	if !isLoopback(req) {
		t.Error("expected 127.0.0.1 remote addr to be loopback")
	}
}

func TestIsLoopbackNonLoopback(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com:9090/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	if isLoopback(req) {
		t.Error("expected 10.0.0.1 to NOT be loopback")
	}
}

func TestIsLoopbackIPv6LoopbackRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com:9090/", nil)
	req.RemoteAddr = "[::1]:12345"
	if !isLoopback(req) {
		t.Error("expected ::1 remote addr to be loopback")
	}
}
