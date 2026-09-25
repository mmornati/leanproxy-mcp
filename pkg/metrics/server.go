package metrics

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/httpsec"
)

// Config holds the metrics endpoint's bind address and, since issue #316,
// its optional bearer token and extra allowed Host header values. It
// mirrors pkg/dashboard.Config; the two packages stay independent of each
// other but share the same DNS-rebinding and unauthenticated-access
// defenses via pkg/httpsec.
type Config struct {
	Bind  string
	Token string

	// AllowedHosts lists extra Host header values (--metrics-allowed-hosts)
	// accepted in addition to the bind host, localhost, 127.0.0.1 and ::1.
	AllowedHosts []string

	// Usage, when set, supplies the "usage" section (today / week-to-date
	// from the usage store, see UsageSummary). A nil func, or one that
	// returns nil (e.g. the store could not be read), omits the section.
	Usage func() *UsageSummary
}

// endpointSnapshot is the /metrics response: this process's live counters
// plus, when available, the usage store's windows. Usage is kept out of
// MetricsSnapshot itself because that struct is what the usage store
// records.
type endpointSnapshot struct {
	MetricsSnapshot
	Usage *UsageSummary `json:"usage,omitempty"`
}

// ListenAndServe starts the metrics endpoint from a bind address alone,
// with no token and no extra allowed hosts. It is a thin wrapper around
// ListenAndServeConfig kept for callers that have no need for
// authentication (a loopback-only bind); ListenAndServeConfig refuses to
// start on a non-loopback bind without a token exactly the same way either
// entry point is called.
func ListenAndServe(addr string, logger *slog.Logger) (*http.Server, error) {
	return ListenAndServeConfig(Config{Bind: addr}, logger)
}

// ListenAndServeConfig starts the metrics endpoint per cfg. A non-loopback
// bind (cfg.Bind's host is not loopback) without cfg.Token refuses to start,
// exactly like pkg/dashboard: otherwise the JSON snapshot — server names,
// tool names and token counts — would be served to anyone who can reach the
// bound interface. Every request's Host header is validated the same way
// the dashboard's is, to close the DNS-rebinding path from a malicious page
// in a local browser (issue #316).
func ListenAndServeConfig(cfg Config, logger *slog.Logger) (*http.Server, error) {
	if cfg.Bind == "" || cfg.Bind == "off" {
		logger.Info("metrics endpoint disabled")
		return nil, nil
	}

	host, port, err := net.SplitHostPort(cfg.Bind)
	if err != nil {
		return nil, err
	}

	if !httpsec.IsLoopbackHost(host) {
		if cfg.Token == "" {
			return nil, fmt.Errorf("refusing to start metrics endpoint on non-loopback bind %q without a token: set --metrics-token", cfg.Bind)
		}
		logger.Warn("metrics endpoint listening on non-loopback interface; data is not encrypted",
			"bind", cfg.Bind)
	}

	ln, err := net.Listen("tcp", cfg.Bind)
	if err != nil {
		return nil, err
	}
	_, actualPort, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		actualPort = port
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", handleMetrics(cfg.Token, cfg.Usage))

	allowedHosts := httpsec.AllowedHosts(host, actualPort, cfg.AllowedHosts)
	handler := httpsec.ValidateHost(allowedHosts)(mux)

	srv := &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("metrics endpoint started", "bind", srv.Addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics endpoint error", "error", err)
		}
	}()

	return srv, nil
}

// handleMetrics returns the /metrics handler, requiring token (via the
// "Authorization: Bearer <token>" header) when one is configured. An empty
// token allows every request, matching the dashboard's requireBearerToken.
func handleMetrics(token string, usage func() *UsageSummary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if token != "" && !validBearerToken(r, token) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q", "metrics"))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		snapshot := endpointSnapshot{MetricsSnapshot: Snapshot()}
		if usage != nil {
			snapshot.Usage = usage()
		}

		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			enc.SetIndent("", "")
		}
		if err := enc.Encode(snapshot); err != nil {
			slog.Error("failed to encode metrics snapshot", "error", err)
		}
	}
}

func validBearerToken(r *http.Request, token string) bool {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) == 1
}
