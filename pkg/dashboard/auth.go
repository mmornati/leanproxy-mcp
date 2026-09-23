package dashboard

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// dashboardCookieName is the HttpOnly cookie requireBearerToken accepts as
// an alternative to the Authorization header, set by handleLogin after a
// GET /login?token=... with a valid token (issue #316).
const dashboardCookieName = "leanproxy_dashboard_token"

// requireBearerToken returns middleware enforcing cfg's token, if any, on
// every request. Unset token: everything is allowed (loopback-only binds
// without a token are still gated at startup by ListenAndServe, which
// refuses to bind a non-loopback address with no token configured).
//
// A configured token is required from every client, loopback included
// (issue #316 removes the previous loopback bypass, which let a reverse
// proxy or any other local process on the same host defeat the token). The
// token may be presented either as "Authorization: Bearer <token>" or as the
// dashboardCookieName cookie set by GET /login?token=....
func requireBearerToken(token string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			if provided, ok := bearerFromRequest(r); ok && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q", "dashboard"))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
}

// bearerFromRequest extracts the candidate token from the Authorization
// header (Bearer scheme) or, failing that, from the dashboard cookie.
func bearerFromRequest(r *http.Request) (string, bool) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return parts[1], true
		}
		return "", false
	}

	if c, err := r.Cookie(dashboardCookieName); err == nil {
		return c.Value, true
	}

	return "", false
}

func isLoopback(r *http.Request) bool {
	remoteAddr := r.RemoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteAddr = h
	}
	if remoteAddr == "localhost" || remoteAddr == "127.0.0.1" || remoteAddr == "::1" {
		return true
	}
	remoteIP := net.ParseIP(remoteAddr)
	if remoteIP != nil && remoteIP.IsLoopback() {
		return true
	}

	return false
}

// handleLogin returns the GET /login?token=... handler that exchanges a
// valid bearer token for an HttpOnly session cookie, so a browser can use
// the dashboard without attaching an Authorization header to every request.
// A wrong or missing token gets 401 and no cookie. The cookie is
// SameSite=Strict (never sent cross-site) and Secure when the request
// arrived over TLS.
func handleLogin(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.Error(w, "dashboard token authentication is not configured", http.StatusNotFound)
			return
		}

		provided := r.URL.Query().Get("token")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q", "dashboard"))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly and SameSite=Strict are set; Secure is conditional on r.TLS by design (issue #316 requires Secure only when served over TLS, so the cookie still works on a plain-HTTP loopback bind)
			Name:     dashboardCookieName,
			Value:    provided,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteStrictMode,
		})

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("logged in"))
	}
}
