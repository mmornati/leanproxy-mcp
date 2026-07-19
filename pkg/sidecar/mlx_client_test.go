//go:build mlx

package sidecar

import (
	"context"
	"testing"
)

func TestMLXClient_FallbackCount_Zero(t *testing.T) {
	c := &MLXClient{}
	if got := c.FallbackCount(); got != 0 {
		t.Errorf("FallbackCount() = %d, want 0", got)
	}
}

func TestMLXClient_Provider_ReturnsMLX(t *testing.T) {
	c := &MLXClient{}
	if got := c.Provider(); got != ProviderMLX {
		t.Errorf("Provider() = %q, want %q", got, ProviderMLX)
	}
}

func TestMLXClient_Model_ReturnsConfigured(t *testing.T) {
	c := &MLXClient{modelName: "llama-3.2-3b"}
	if got := c.Model(); got != "llama-3.2-3b" {
		t.Errorf("Model() = %q, want %q", got, "llama-3.2-3b")
	}
}

func TestMLXClient_Model_ReturnsEmpty(t *testing.T) {
	c := &MLXClient{}
	if got := c.Model(); got != "" {
		t.Errorf("Model() = %q, want empty", got)
	}
}

func TestMLXClient_Healthy_NoModelPath(t *testing.T) {
	c := &MLXClient{}
	if c.Healthy(context.Background()) {
		t.Error("expected not healthy without model path")
	}
}

func TestMLXClient_Healthy_CancelledContext(t *testing.T) {
	c := &MLXClient{modelPath: "/tmp/anything"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Healthy(ctx) {
		t.Error("expected not healthy with cancelled context")
	}
}

func TestMLXClient_Close_NilSafe(t *testing.T) {
	var c *MLXClient
	if err := c.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestMLXClient_Close_NonNil(t *testing.T) {
	c := &MLXClient{}
	if err := c.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestMLXClient_Redact_EmptyContent(t *testing.T) {
	c := &MLXClient{}
	if result := c.Redact(context.Background(), ""); result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestMLXClient_Redact_NonEmpty(t *testing.T) {
	c := &MLXClient{}
	if result := c.Redact(context.Background(), "sensitive data"); result != PlaceholderRedacted {
		t.Errorf("expected %q, got %q", PlaceholderRedacted, result)
	}
}

func TestMLXClient_Redact_CancelledContextPassthrough(t *testing.T) {
	c := &MLXClient{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := c.Redact(ctx, "sensitive data"); result != "sensitive data" {
		t.Errorf("expected passthrough on cancelled context, got %q", result)
	}
}

func TestMLXClient_NilClient_Operations(t *testing.T) {
	var c *MLXClient

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
}

func TestMLXClient_Redact_NilClient(t *testing.T) {
	var c *MLXClient
	if result := c.Redact(context.Background(), "sensitive data"); result != "sensitive data" {
		t.Errorf("expected passthrough, got %q", result)
	}
}

func TestManager_Close_WithMLXClient(t *testing.T) {
	m := &Manager{
		client: &MLXClient{},
	}
	m.enabled.Store(true)
	m.Close()
	if m.Enabled() {
		t.Error("expected disabled after close")
	}
}
