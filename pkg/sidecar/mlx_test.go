package sidecar

import (
	"testing"
)

func TestConfig_EnabledWithMLX(t *testing.T) {
	t.Run("mlx provider is enabled", func(t *testing.T) {
		c := &Config{Provider: "mlx", Model: "test"}
		if !c.Enabled() {
			t.Error("mlx provider should be enabled")
		}
	})

	t.Run("whitespace-trimmed provider is enabled", func(t *testing.T) {
		c := &Config{Provider: "  mlx ", Model: "test"}
		if !c.Enabled() {
			t.Error("whitespace-trimmed mlx provider should be enabled")
		}
	})
}

func TestConfig_ValidateMLX(t *testing.T) {
	t.Run("mlx config with model is valid", func(t *testing.T) {
		c := &Config{Provider: "mlx", Model: "llama-3.2-3b"}
		if err := c.Validate(); err != nil {
			t.Errorf("valid mlx config should pass: %v", err)
		}
	})

	t.Run("mlx config without model is rejected", func(t *testing.T) {
		c := &Config{Provider: "mlx"}
		if err := c.Validate(); err == nil {
			t.Error("mlx config without model should be rejected")
		}
	})

	t.Run("mlx config with whitespace provider is recognized", func(t *testing.T) {
		c := &Config{Provider: "  MLX ", Model: "llama-3.2-3b"}
		if err := c.Validate(); err != nil {
			t.Errorf("trimmed provider should still validate: %v", err)
		}
	})
}

func TestNewClient_MLXDispatcher(t *testing.T) {
	t.Run("mlx provider returns error without build tag", func(t *testing.T) {
		c, err := NewClient(Config{Provider: "mlx", Model: "test"}, nil)
		if err == nil {
			t.Fatal("expected error for mlx without build tag")
		}
		if c != nil {
			t.Error("expected nil client on error")
		}
	})
}

func TestManager_MLXConfig(t *testing.T) {
	t.Run("mlx provider without build tag returns error", func(t *testing.T) {
		m, err := NewManager(Config{Provider: "mlx", Model: "test"}, nil)
		if err == nil {
			t.Fatal("expected error for mlx without build tag")
		}
		if m != nil {
			t.Error("expected nil manager on error")
		}
	})
}
