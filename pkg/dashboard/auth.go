package dashboard

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

func requireBearerToken(token string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			if isLoopback(r) {
				next.ServeHTTP(w, r)
				return
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q", "dashboard"))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(auth, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q", "dashboard"))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q", "dashboard"))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
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
