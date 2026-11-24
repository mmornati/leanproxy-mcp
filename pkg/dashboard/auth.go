package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
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
//
// With a session store (what ListenAndServe passes), the cookie holds a
// random session id issued by /login, never the token itself: a leaked
// cookie expires and cannot be replayed as a bearer token.
func requireBearerToken(token string, logger *slog.Logger, sessions ...*browserSessions) func(http.Handler) http.Handler {
	store := firstSessions(sessions)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}

			if store != nil {
				if provided, ok := httpsecBearer(r); ok && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
				if c, err := r.Cookie(dashboardCookieName); err == nil && r.Header.Get("Authorization") == "" && store.valid(c.Value) {
					next.ServeHTTP(w, r)
					return
				}
			} else if provided, ok := bearerFromRequest(r); ok && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
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
//
// GET /login?code=... does the same with a one-time code minted by an
// authenticated POST /login/code (see loginCodes): that is what `daemon
// open` puts in the URL it hands to the browser, so the long-lived token
// never appears in a process listing or in the browser's history.
func handleLogin(token string, codes *loginCodes, sessions ...*browserSessions) http.HandlerFunc {
	store := firstSessions(sessions)
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			http.Error(w, "dashboard token authentication is not configured", http.StatusNotFound)
			return
		}

		provided := r.URL.Query().Get("token")
		byToken := provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
		if !byToken && !codes.redeem(r.URL.Query().Get("code")) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q", "dashboard"))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		// The cookie is a server-side session id with its own lifetime,
		// not the long-lived token.
		provided = token
		maxAge := 0
		if store != nil {
			id, err := store.create()
			if err != nil {
				http.Error(w, "cannot create a session", http.StatusInternalServerError)
				return
			}
			provided, maxAge = id, int(browserSessionTTL.Seconds())
		}

		http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly and SameSite=Strict are set; Secure is conditional on r.TLS by design (issue #316 requires Secure only when served over TLS, so the cookie still works on a plain-HTTP loopback bind)
			Name:     dashboardCookieName,
			Value:    provided,
			Path:     "/",
			MaxAge:   maxAge,
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteStrictMode,
		})

		// A browser lands on the dashboard; the token leaves the address
		// bar. Anything else (curl, a test) still reads a 200 "logged in"
		// unless it follows the redirect.
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("logged in"))
	}
}

// loginCodeTTL is how long a one-time sign-in code can be redeemed.
const loginCodeTTL = time.Minute

// loginCodes are one-time sign-in codes: each is random, valid for
// loginCodeTTL and redeemable once.
type loginCodes struct {
	mu    sync.Mutex
	codes map[string]time.Time
}

func newLoginCodes() *loginCodes {
	return &loginCodes{codes: make(map[string]time.Time)}
}

// mint returns a new code.
func (l *loginCodes) mint() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	code := hex.EncodeToString(b[:])
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for c, exp := range l.codes {
		if now.After(exp) {
			delete(l.codes, c)
		}
	}
	l.codes[code] = now.Add(loginCodeTTL)
	return code, nil
}

// redeem consumes code and reports whether it was valid.
func (l *loginCodes) redeem(code string) bool {
	if l == nil || code == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	exp, ok := l.codes[code]
	delete(l.codes, code)
	return ok && time.Now().Before(exp)
}

// handleLoginCode answers an authenticated request with a new one-time
// sign-in code, as plain text.
func handleLoginCode(codes *loginCodes) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code, err := codes.mint()
		if err != nil {
			http.Error(w, "cannot create a sign-in code", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(code))
	}
}

// browserSessionTTL is how long a browser stays signed in.
const browserSessionTTL = 12 * time.Hour

// browserSessions are the signed-in browsers: random ids kept in memory,
// each valid for browserSessionTTL. A proxy restart signs everyone out.
type browserSessions struct {
	mu  sync.Mutex
	ids map[string]time.Time
}

func newBrowserSessions() *browserSessions {
	return &browserSessions{ids: make(map[string]time.Time)}
}

func firstSessions(s []*browserSessions) *browserSessions {
	if len(s) == 0 {
		return nil
	}
	return s[0]
}

func (b *browserSessions) create() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, exp := range b.ids {
		if now.After(exp) {
			delete(b.ids, k)
		}
	}
	b.ids[id] = now.Add(browserSessionTTL)
	return id, nil
}

func (b *browserSessions) valid(id string) bool {
	if id == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	exp, ok := b.ids[id]
	if ok && time.Now().After(exp) {
		delete(b.ids, id)
		return false
	}
	return ok
}

// httpsecBearer extracts the token of an "Authorization: Bearer" header.
func httpsecBearer(r *http.Request) (string, bool) {
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1], true
	}
	return "", false
}
