package metrics

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

func ListenAndServe(addr string, logger *slog.Logger) (*http.Server, error) {
	if addr == "" || addr == "off" {
		logger.Info("metrics endpoint disabled")
		return nil, nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	if host == "" || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		logger.Warn("metrics endpoint listening on non-loopback interface; data is not encrypted",
			"bind", addr)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", handleMetrics)

	srv := &http.Server{
		Addr:    ln.Addr().String(),
		Handler: mux,
	}

	go func() {
		logger.Info("metrics endpoint started", "bind", srv.Addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics endpoint error", "error", err)
		}
	}()

	return srv, nil
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := Snapshot()

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
