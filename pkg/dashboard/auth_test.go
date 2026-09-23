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

// TestRequireBearerTokenLoopbackNoLongerBypasses covers issue #316: when a
// token is configured, it is required from every client, loopback included.
// The previous version of requireBearerToken let any loopback process (a
// reverse proxy, or any other local user) skip the token entirely.
func TestRequireBearerTokenLoopbackNoLongerBypasses(t *testing.T) {
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for loopback without the token")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for loopback without token", w.Code)
	}
}

func TestRequireBearerTokenLoopbackWithValidToken(t *testing.T) {
	called := false
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called for loopback with a valid token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRequireBearerTokenCookie(t *testing.T) {
	called := false
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.AddCookie(&http.Cookie{Name: dashboardCookieName, Value: "secret"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected handler to be called with a valid cookie")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRequireBearerTokenWrongCookie(t *testing.T) {
	handler := requireBearerToken("secret", slog.Default())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with a wrong cookie")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.AddCookie(&http.Cookie{Name: dashboardCookieName, Value: "wrong"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleLoginSetsCookie(t *testing.T) {
	handler := handleLogin("secret")

	req := httptest.NewRequest("GET", "/login?token=secret", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := w.Result()
	defer resp.Body.Close()
	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == dashboardCookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatal("expected dashboard cookie to be set")
	}
	if found.Value != "secret" {
		t.Errorf("cookie value = %q, want %q", found.Value, "secret")
	}
	if !found.HttpOnly {
		t.Error("expected cookie to be HttpOnly")
	}
	if found.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", found.SameSite)
	}
	if found.Secure {
		t.Error("expected cookie to not be Secure over plain HTTP")
	}
}

func TestHandleLoginWrongToken(t *testing.T) {
	handler := handleLogin("secret")

	req := httptest.NewRequest("GET", "/login?token=wrong", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("expected no cookie to be set for a wrong token")
	}
}

func TestHandleLoginNoTokenConfigured(t *testing.T) {
	handler := handleLogin("")

	req := httptest.NewRequest("GET", "/login?token=anything", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no token is configured", w.Code)
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
