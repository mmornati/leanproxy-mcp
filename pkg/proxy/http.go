package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
)

type HTTPTransportConfig struct {
	Port          string
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	MaxHeaderBytes int
}

func DefaultHTTPTransportConfig() HTTPTransportConfig {
	return HTTPTransportConfig{
		Port:          "8080",
		ReadTimeout:   30 * time.Second,
		WriteTimeout:  30 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
}

type HTTPTransport struct {
	addr     string
	handler  http.Handler
	server   *http.Server
	config   HTTPTransportConfig
	logger   *slog.Logger
	mu       sync.Mutex
	running  bool
}

type HTTPTransportOption func(*HTTPTransport)

func WithHTTPLogger(logger *slog.Logger) HTTPTransportOption {
	return func(t *HTTPTransport) {
		if logger != nil {
			t.logger = logger
		}
	}
}

func NewHTTPTransport(config HTTPTransportConfig, opts ...HTTPTransportOption) *HTTPTransport {
	t := &HTTPTransport{
		config: config,
		logger: slog.Default(),
	}

	for _, opt := range opts {
		opt(t)
	}

	if t.config.Port == "" {
		t.config.Port = "8080"
	}

	mux := http.NewServeMux()
	t.handler = mux

	t.server = &http.Server{
		Addr:           ":" + t.config.Port,
		Handler:        mux,
		ReadTimeout:    t.config.ReadTimeout,
		WriteTimeout:   t.config.WriteTimeout,
		MaxHeaderBytes: t.config.MaxHeaderBytes,
	}

	t.addr = ":" + t.config.Port

	return t
}

func (t *HTTPTransport) RegisterHandler(pattern string, handler http.Handler) {
	mux, ok := t.handler.(*http.ServeMux)
	if !ok {
		return
	}
	mux.Handle(pattern, handler)
}

func (t *HTTPTransport) ListenAndServe() error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return fmt.Errorf("http transport: server already running")
	}
	t.running = true
	t.mu.Unlock()

	t.logger.Info("http transport: starting server", "addr", t.addr)
	return t.server.ListenAndServe()
}

func (t *HTTPTransport) ListenAndServeContext(ctx context.Context) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return fmt.Errorf("http transport: server already running")
	}
	t.running = true
	t.mu.Unlock()

	t.logger.Info("http transport: starting server with context", "addr", t.addr)

	errChan := make(chan error, 1)
	go func() {
		errChan <- t.server.ListenAndServe()
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		t.logger.Info("http transport: shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return t.server.Shutdown(shutdownCtx)
	}
}

func (t *HTTPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.logger.Info("http transport: closing server")
	t.running = false
	return t.server.Shutdown(ctx)
}

func (t *HTTPTransport) GetAddr() string {
	return t.addr
}

func (t *HTTPTransport) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

type MCPHandler func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error)

func StreamableHTTPHandler(handler MCPHandler, logger *slog.Logger) http.Handler {
	return newStreamableHTTPHandler(handler, logger)
}

type streamableHTTPHandler struct {
	handler MCPHandler
	logger  *slog.Logger
}

func newStreamableHTTPHandler(handler MCPHandler, logger *slog.Logger) *streamableHTTPHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &streamableHTTPHandler{
		handler: handler,
		logger:  logger,
	}
}

func (h *streamableHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	contentType := r.Header.Get("Content-Type")
	accept := r.Header.Get("Accept")

	h.logger.Debug("http transport: request received",
		"method", r.Method,
		"path", r.URL.Path,
		"content_type", contentType,
		"accept", accept)

	if r.Method == http.MethodGet && r.URL.Path == "/mcp" {
		h.handleStreamableGet(w, r, ctx)
		return
	}

	if r.Method == http.MethodPost && (r.URL.Path == "/mcp" || r.URL.Path == "/sse") {
		h.handlePost(w, r, ctx)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/sse" {
		h.handleSSE(w, r, ctx)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/health" {
		h.handleHealth(w, r)
		return
	}

	http.Error(w, "Not Found", http.StatusNotFound)
}

func (h *streamableHTTPHandler) handlePost(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn("http transport: failed to read request body", "error", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	h.logger.Debug("http transport: received request", "body_length", len(body))

	req, err := ParseJSONRPCRequest(body)
	if err != nil {
		h.logger.Warn("http transport: failed to parse JSON-RPC request", "error", err)
		resp := &JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   errors.NewJSONRPCError(errors.ErrCodeParseError, "Parse error"),
			ID:      nil,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(resp)
		return
	}

	resp, err := h.handler(ctx, req)
	if err != nil {
		h.logger.Warn("http transport: handler error", "error", err)
		resp = &JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, err.Error()),
			ID:      req.ID,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (h *streamableHTTPHandler) handleStreamableGet(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	accept := r.Header.Get("Accept")

	if accept == "text/event-stream" {
		h.handleSSE(w, r, ctx)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Accept", "application/json, text/event-stream")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"jsonrpc": "2.0",
		"result":  "Streamable HTTP endpoint ready",
	})
}

func (h *streamableHTTPHandler) handleSSE(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		h.logger.Warn("http transport: flusher not available")
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	_, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn("http transport: failed to read SSE request body", "error", err)
		return
	}
	r.Body.Close()

	notify := ctx.Done()
	notifyChan := make(chan struct{})
	close(notifyChan)

	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-notify:
		}
	}()

	flusher.Flush()

	h.logger.Debug("http transport: SSE connection established")
}

func (h *streamableHTTPHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"service": "leanproxy-mcp",
	})
}

