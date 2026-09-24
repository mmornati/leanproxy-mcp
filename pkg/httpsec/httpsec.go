// Package httpsec holds the Host/Origin validation, bearer-token and
// security-header helpers shared by LeanProxy's local HTTP servers: the
// admin endpoints (pkg/dashboard, pkg/metrics, issue #316) and the
// Streamable HTTP MCP front end (pkg/streamhttp, issue #309). The packages
// stay independent of each other; this package only avoids duplicating the
// DNS-rebinding and authentication defenses between them.
package httpsec

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// IsLoopbackHost reports whether host (a bare hostname or IP, no port) is a
// loopback name or address: "localhost" (case-insensitive) or any IP for
// which net.IP.IsLoopback is true (127.0.0.0/8, ::1).
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsLoopbackBindAddr reports whether bind ("host:port") only binds a
// loopback interface. An empty host (e.g. ":9090") binds every interface
// and is therefore not loopback. An unparsable bind address is treated as
// non-loopback so callers fail closed.
func IsLoopbackBindAddr(bind string) bool {
	host, _, err := net.SplitHostPort(bind)
	if err != nil || host == "" {
		return false
	}
	return IsLoopbackHost(host)
}

// AllowedHosts builds the canonical set of acceptable HTTP Host header
// values (lower-cased "host:port") for a server bound to bindHost:port.
// It always includes the bind host itself (when it names a specific
// interface rather than "0.0.0.0"/"::"/""), "localhost", "127.0.0.1" and
// "::1", each combined with port, plus any operator-supplied extra hosts:
// an extra entry that already has a port is taken literally, otherwise
// port is appended.
func AllowedHosts(bindHost, port string, extra []string) map[string]struct{} {
	set := make(map[string]struct{})
	add := func(h string) {
		if h == "" {
			return
		}
		set[strings.ToLower(net.JoinHostPort(h, port))] = struct{}{}
	}

	switch bindHost {
	case "", "0.0.0.0", "::":
		// Binds every interface; no single host name to add here. The
		// loopback names below still let local clients through.
	default:
		add(bindHost)
	}
	add("localhost")
	add("127.0.0.1")
	add("::1")

	for _, h := range extra {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(h); err == nil {
			set[strings.ToLower(h)] = struct{}{}
			continue
		}
		add(h)
	}

	return set
}

// isStateChanging reports whether method can change server state, i.e. is
// not one of the safe/read-only methods.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// ValidateHost returns middleware that rejects (403 Forbidden) any request
// whose Host header is not in allowed, and any state-changing request
// (anything but GET/HEAD/OPTIONS) whose Origin header, when present, does
// not match the request's own Host. This is the DNS-rebinding and
// cross-origin defense required by issue #316: a malicious page served from
// an unrelated origin cannot drive the dashboard or metrics endpoint even
// though it runs in the victim's browser and can reach 127.0.0.1.
func ValidateHost(allowed map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := strings.ToLower(r.Host)
			if _, ok := allowed[host]; !ok {
				http.Error(w, "Forbidden: unrecognized Host header", http.StatusForbidden)
				return
			}

			if isStateChanging(r.Method) {
				if origin := r.Header.Get("Origin"); origin != "" && !originMatchesHost(origin, host) {
					http.Error(w, "Forbidden: Origin does not match", http.StatusForbidden)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// originMatchesHost reports whether the Origin header value's host:port
// matches host (already lower-cased, "host:port" form).
func originMatchesHost(origin, host string) bool {
	origin = strings.ToLower(origin)
	// origin looks like "scheme://host[:port]"; strip the scheme.
	if idx := strings.Index(origin, "://"); idx != -1 {
		origin = origin[idx+3:]
	}
	origin = strings.TrimSuffix(origin, "/")
	return origin == host
}

// SecurityHeaders returns middleware that sets the response headers
// required by issue #316 for the dashboard: a same-origin-only CSP,
// clickjacking and MIME-sniffing protection, and no referrer leakage.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(w, r)
		})
	}
}

// OriginSet builds the canonical set of allowlisted browser origins
// ("scheme://host[:port]", lower-cased, without a trailing slash) from
// operator-supplied values. Empty entries are skipped.
func OriginSet(origins []string) map[string]struct{} {
	set := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		o = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(o)), "/")
		if o != "" {
			set[o] = struct{}{}
		}
	}
	return set
}

// OriginAllowed reports whether a request carrying the Origin header value
// origin may reach a server whose (already validated) Host header is host:
// the origin is either the server's own ("http://<host>", i.e. a page it
// served itself) or allowlisted. The opaque origin "null" (sandboxed
// frames, file:// pages) is never allowed. Callers only consult it when an
// Origin header is present: non-browser clients do not send one.
func OriginAllowed(origin, host string, allowed map[string]struct{}) bool {
	origin = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(origin)), "/")
	if origin == "" || origin == "null" {
		return false
	}
	if _, ok := allowed[origin]; ok {
		return true
	}
	return originMatchesHost(origin, strings.ToLower(host))
}

// BearerToken returns the token of an "Authorization: Bearer <token>"
// header (scheme matched case-insensitively), and whether there was one.
func BearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// ValidBearer reports whether r carries "Authorization: Bearer <token>"
// with exactly token, compared in constant time. An empty token never
// matches.
func ValidBearer(r *http.Request, token string) bool {
	provided, ok := BearerToken(r)
	if !ok || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}
