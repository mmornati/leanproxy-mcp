package sidecar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"
)

func TestConfig_Enabled(t *testing.T) {
	t.Run("nil config is not enabled", func(t *testing.T) {
		var c *Config
		if c.Enabled() {
			t.Error("nil config should not be enabled")
		}
	})

	t.Run("empty provider is not enabled", func(t *testing.T) {
		c := &Config{Provider: "", Model: "llama3.1:8b"}
		if c.Enabled() {
			t.Error("empty provider should not be enabled")
		}
	})

	t.Run("ollama provider is enabled", func(t *testing.T) {
		c := &Config{Provider: "ollama", Model: "llama3.1:8b", URL: "http://localhost:11434"}
		if !c.Enabled() {
			t.Error("ollama provider should be enabled")
		}
	})

	t.Run("case insensitive provider", func(t *testing.T) {
		c := &Config{Provider: "OLLAMA", Model: "llama3.1:8b"}
		if !c.Enabled() {
			t.Error("OLLAMA provider should be enabled (case insensitive)")
		}
	})
}

func TestConfig_Validate(t *testing.T) {
	t.Run("nil config is valid", func(t *testing.T) {
		var c *Config
		if err := c.Validate(); err != nil {
			t.Errorf("nil config should be valid: %v", err)
		}
	})

	t.Run("disabled config is valid", func(t *testing.T) {
		c := &Config{Provider: ""}
		if err := c.Validate(); err != nil {
			t.Errorf("disabled config should be valid: %v", err)
		}
	})

	t.Run("ollama config with model and url is valid", func(t *testing.T) {
		c := &Config{Provider: "ollama", Model: "llama3.1:8b", URL: "http://localhost:11434"}
		if err := c.Validate(); err != nil {
			t.Errorf("valid config should pass: %v", err)
		}
	})

	t.Run("ollama config defaults url and model", func(t *testing.T) {
		c := &Config{Provider: "ollama"}
		if err := c.Validate(); err != nil {
			t.Errorf("config should set defaults: %v", err)
		}
		if c.URL != defaultOllamaURL {
			t.Errorf("expected default URL %q, got %q", defaultOllamaURL, c.URL)
		}
		if c.Model != defaultOllamaModel {
			t.Errorf("expected default model %q, got %q", defaultOllamaModel, c.Model)
		}
	})
}

func TestNewClient(t *testing.T) {
	t.Run("disabled config returns nil", func(t *testing.T) {
		c, err := NewClient(Config{}, nil)
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		if c != nil {
			t.Error("expected nil client for disabled config")
		}
	})

	t.Run("ollama config creates client", func(t *testing.T) {
		c, err := NewClient(Config{Provider: "ollama", Model: "llama3.1:8b", URL: "http://localhost:11434"}, nil)
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		if c == nil {
			t.Fatal("expected non-nil client")
		}
		if c.Provider() != "ollama" {
			t.Errorf("expected provider ollama, got %q", c.Provider())
		}
		if c.Model() != "llama3.1:8b" {
			t.Errorf("expected model llama3.1:8b, got %q", c.Model())
		}
		c.Close()
	})
}

func TestClient_FallbackCount(t *testing.T) {
	t.Run("nil client returns 0", func(t *testing.T) {
		var c *Client
		if got := c.FallbackCount(); got != 0 {
			t.Errorf("FallbackCount() = %d, want 0", got)
		}
	})

	t.Run("no fallbacks yet returns 0", func(t *testing.T) {
		c := &Client{}
		if got := c.FallbackCount(); got != 0 {
			t.Errorf("FallbackCount() = %d, want 0", got)
		}
	})

	t.Run("increments on redact error", func(t *testing.T) {
		logger := slog.Default()
		c := &Client{
			cfg:    Config{Provider: "ollama", Model: "llama3.1:8b", URL: "http://127.0.0.1:1"},
			client: &http.Client{Timeout: time.Second},
			logger: logger,
		}
		result := c.Redact(context.Background(), "sensitive data")
		if result != "[VALUE_REDACTED]" {
			t.Errorf("expected [VALUE_REDACTED], got %q", result)
		}
		if got := c.FallbackCount(); got != 1 {
			t.Errorf("FallbackCount() = %d, want 1", got)
		}
	})
}

func TestClient_Redact_Success(t *testing.T) {
	var callCount atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req generateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode error: %v", err)
		}
		if req.Stream {
			t.Error("expected stream=false")
		}
		resp := generateResponse{
			Model:    req.Model,
			Response: "redacted_output",
			Done:     true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c, err := NewClient(Config{Provider: "ollama", Model: "llama3.1:8b", URL: ts.URL}, nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer c.Close()

	result := c.Redact(context.Background(), "my api key is sk-12345")
	if result != "redacted_output" {
		t.Errorf("expected redacted_output, got %q", result)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
}

func TestClient_Redact_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c, err := NewClient(Config{Provider: "ollama", Model: "llama3.1:8b", URL: ts.URL}, nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer c.Close()

	result := c.Redact(context.Background(), "sensitive data")
	if result != "[VALUE_REDACTED]" {
		t.Errorf("expected [VALUE_REDACTED] on server error, got %q", result)
	}
}

func TestClient_Generate_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := generateResponse{
			Model:    "llama3.1:8b",
			Response: "Hello, world!",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c, err := NewClient(Config{Provider: "ollama", Model: "llama3.1:8b", URL: ts.URL}, nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer c.Close()

	result, err := c.Generate(context.Background(), "Say hello")
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if result != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", result)
	}
}

func TestClient_Generate_Unreachable(t *testing.T) {
	c := &Client{
		cfg:    Config{Provider: "ollama", Model: "llama3.1:8b", URL: "http://127.0.0.1:1"},
		client: &http.Client{Timeout: time.Second},
		logger: slog.Default(),
	}
	defer c.Close()

	_, err := c.Generate(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "sidecar unreachable") {
		t.Errorf("expected unreachable error, got: %v", err)
	}
}

func TestClient_Generate_NilClient(t *testing.T) {
	var c *Client
	_, err := c.Generate(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestClient_Healthy(t *testing.T) {
	t.Run("nil client not healthy", func(t *testing.T) {
		var c *Client
		if c.Healthy(context.Background()) {
			t.Error("nil client should not be healthy")
		}
	})

	t.Run("healthy server", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		c := &Client{
			cfg:    Config{Provider: "ollama", URL: ts.URL},
			client: &http.Client{Timeout: time.Second},
		}
		if !c.Healthy(context.Background()) {
			t.Error("expected healthy")
		}
	})

	t.Run("unhealthy server", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer ts.Close()

		c := &Client{
			cfg:    Config{Provider: "ollama", URL: ts.URL},
			client: &http.Client{Timeout: time.Second},
		}
		if c.Healthy(context.Background()) {
			t.Error("expected unhealthy")
		}
	})
}

func TestClient_NilClient_Operations(t *testing.T) {
	var c *Client

	if c.FallbackCount() != 0 {
		t.Error("expected 0")
	}
	if c.Provider() != "" {
		t.Error("expected empty provider")
	}
	if c.Model() != "" {
		t.Error("expected empty model")
	}
	if c.Healthy(context.Background()) {
		t.Error("expected not healthy")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestManager_New(t *testing.T) {
	t.Run("disabled config", func(t *testing.T) {
		m, err := NewManager(Config{}, nil)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		if m.Enabled() {
			t.Error("expected disabled manager")
		}
	})

	t.Run("enabled config", func(t *testing.T) {
		m, err := NewManager(Config{Provider: "ollama", Model: "llama3.1:8b", URL: "http://localhost:11434"}, nil)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		defer m.Close()
		if !m.Enabled() {
			t.Error("expected enabled manager")
		}
		if m.Provider() != "ollama" {
			t.Errorf("expected provider ollama, got %q", m.Provider())
		}
		if m.Model() != "llama3.1:8b" {
			t.Errorf("expected model llama3.1:8b, got %q", m.Model())
		}
	})
}

func TestManager_DisabledOperations(t *testing.T) {
	m, err := NewManager(Config{}, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if m.Enabled() {
		t.Error("expected disabled")
	}
	result := m.Redact(context.Background(), "test data")
	if result != "test data" {
		t.Errorf("expected passthrough, got %q", result)
	}
	if m.FallbackCount() != 0 {
		t.Error("expected 0 fallbacks")
	}
	if m.Provider() != "" {
		t.Error("expected empty provider")
	}
	if m.Healthy(context.Background()) {
		t.Error("expected not healthy")
	}
	m.Close()
}

func TestManager_NilOperations(t *testing.T) {
	var m *Manager
	if m.Enabled() {
		t.Error("expected not enabled")
	}
	if m.Redact(context.Background(), "data") != "data" {
		t.Error("expected passthrough")
	}
	if m.FallbackCount() != 0 {
		t.Error("expected 0")
	}
	if m.Provider() != "" {
		t.Error("expected empty")
	}
	if m.Healthy(context.Background()) {
		t.Error("expected not healthy")
	}
	m.Close()
}

func TestAggressiveRedact(t *testing.T) {
	c := &Client{}
	if got := c.aggressiveRedact(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := c.aggressiveRedact("anything"); got != "[VALUE_REDACTED]" {
		t.Errorf("expected [VALUE_REDACTED], got %q", got)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Provider != "" {
		t.Errorf("expected empty provider, got %q", cfg.Provider)
	}
}

func TestClient_Redact_HTTPError(t *testing.T) {
	c := &Client{
		cfg:    Config{Provider: "ollama", Model: "llama3.1:8b", URL: "http://invalid-host-that-does-not-exist.local"},
		client: &http.Client{Timeout: time.Second},
		logger: slog.Default(),
	}
	result := c.Redact(context.Background(), "sensitive data")
	if result != "[VALUE_REDACTED]" {
		t.Errorf("expected [VALUE_REDACTED], got %q", result)
	}
}

func TestClient_Generate_ContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer ts.Close()

	c, err := NewClient(Config{Provider: "ollama", Model: "llama3.1:8b", URL: ts.URL}, nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	_, err = c.Generate(ctx, "test")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestClient_Provider_and_Model(t *testing.T) {
	t.Run("nil client returns empty", func(t *testing.T) {
		var c *Client
		if c.Provider() != "" {
			t.Errorf("expected empty, got %q", c.Provider())
		}
		if c.Model() != "" {
			t.Errorf("expected empty, got %q", c.Model())
		}
	})

	t.Run("returns configured values", func(t *testing.T) {
		c := &Client{
			cfg: Config{Provider: "ollama", Model: "llama3.1:70b"},
		}
		if c.Provider() != "ollama" {
			t.Errorf("expected ollama, got %q", c.Provider())
		}
		if c.Model() != "llama3.1:70b" {
			t.Errorf("expected llama3.1:70b, got %q", c.Model())
		}
	})
}

func TestClient_Redact_NilClient(t *testing.T) {
	var c *Client
	result := c.Redact(context.Background(), "sensitive data")
	if result != "sensitive data" {
		t.Errorf("expected passthrough, got %q", result)
	}
}

func TestClient_Redact_MalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json`))
	}))
	defer ts.Close()

	c, err := NewClient(Config{Provider: "ollama", Model: "llama3.1:8b", URL: ts.URL}, nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer c.Close()

	result := c.Redact(context.Background(), "sensitive data")
	if result != "[VALUE_REDACTED]" {
		t.Errorf("expected [VALUE_REDACTED] on decode error, got %q", result)
	}
}

func TestClient_Redact_EmptyResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(generateResponse{
			Model:    "llama3.1:8b",
			Response: "",
			Done:     true,
		})
	}))
	defer ts.Close()

	c, err := NewClient(Config{Provider: "ollama", Model: "llama3.1:8b", URL: ts.URL}, nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer c.Close()

	result := c.Redact(context.Background(), "sensitive data")
	if result != "[VALUE_REDACTED]" {
		t.Errorf("expected [VALUE_REDACTED] on empty response, got %q", result)
	}
}

func TestIsHostUnreachable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("connection refused"), true},
		{fmt.Errorf("no such host"), true},
		{fmt.Errorf("connection reset by peer"), true},
		{fmt.Errorf("i/o timeout"), true},
		{fmt.Errorf("some other error"), false},
	}
	for _, tt := range tests {
		name := "nil"
		if tt.err != nil {
			s := tt.err.Error()
			if len(s) > 20 {
				s = s[:20]
			}
			name = s
		}
		t.Run(name, func(t *testing.T) {
			if got := isHostUnreachable(tt.err); got != tt.want {
				t.Errorf("isHostUnreachable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
