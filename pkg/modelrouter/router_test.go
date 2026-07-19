package modelrouter

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultTier != TierMedium {
		t.Errorf("DefaultConfig().DefaultTier = %q, want %q", cfg.DefaultTier, TierMedium)
	}
	if cfg.Low.Model != "claude-3-haiku-20240307" {
		t.Errorf("DefaultConfig().Low.Model = %q, want claude-3-haiku-20240307", cfg.Low.Model)
	}
	if cfg.Medium.Model != "claude-3-sonnet-20240229" {
		t.Errorf("DefaultConfig().Medium.Model = %q, want claude-3-sonnet-20240229", cfg.Medium.Model)
	}
	if cfg.High.Model != "claude-3-opus-20240229" {
		t.Errorf("DefaultConfig().High.Model = %q, want claude-3-opus-20240229", cfg.High.Model)
	}
}

func TestTierValid(t *testing.T) {
	tests := []struct {
		tier Tier
		want bool
	}{
		{TierLow, true},
		{TierMedium, true},
		{TierHigh, true},
		{Tier(""), false},
		{Tier("invalid"), false},
		{Tier("LOW"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			if got := tt.tier.Valid(); got != tt.want {
				t.Errorf("Tier(%q).Valid() = %v, want %v", tt.tier, got, tt.want)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	cfg := Config{
		DefaultTier: TierMedium,
		Low: ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-haiku-20240307",
		},
		Medium: ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-sonnet-20240229",
		},
		High: ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-opus-20240229",
		},
	}

	mr := New(cfg, logger)

	t.Run("select low tier", func(t *testing.T) {
		sel, err := mr.Select(ctx, TierLow)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.Provider != "anthropic" {
			t.Errorf("sel.Provider = %q, want anthropic", sel.Provider)
		}
		if sel.Model != "claude-3-haiku-20240307" {
			t.Errorf("sel.Model = %q, want claude-3-haiku-20240307", sel.Model)
		}
		if sel.Tier != TierLow {
			t.Errorf("sel.Tier = %q, want low", sel.Tier)
		}
	})

	t.Run("select medium tier", func(t *testing.T) {
		sel, err := mr.Select(ctx, TierMedium)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.Model != "claude-3-sonnet-20240229" {
			t.Errorf("sel.Model = %q, want claude-3-sonnet-20240229", sel.Model)
		}
	})

	t.Run("select high tier", func(t *testing.T) {
		sel, err := mr.Select(ctx, TierHigh)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.Model != "claude-3-opus-20240229" {
			t.Errorf("sel.Model = %q, want claude-3-opus-20240229", sel.Model)
		}
	})

	t.Run("empty tier falls back to default medium", func(t *testing.T) {
		sel, err := mr.Select(ctx, Tier(""))
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.Tier != TierMedium {
			t.Errorf("sel.Tier = %q, want medium", sel.Tier)
		}
		if sel.Model != "claude-3-sonnet-20240229" {
			t.Errorf("sel.Model = %q, want claude-3-sonnet-20240229", sel.Model)
		}
	})

	t.Run("invalid tier falls back to default medium", func(t *testing.T) {
		sel, err := mr.Select(ctx, Tier("super-expensive"))
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.Tier != TierMedium {
			t.Errorf("sel.Tier = %q, want medium", sel.Tier)
		}
	})
}

func TestNewWithEnvOverride(t *testing.T) {
	os.Setenv("TEST_LOW_API_KEY", "sk-low-test")
	os.Setenv("TEST_HIGH_API_KEY", "sk-high-test")
	defer func() {
		os.Unsetenv("TEST_LOW_API_KEY")
		os.Unsetenv("TEST_HIGH_API_KEY")
	}()

	cfg := Config{
		DefaultTier: TierMedium,
		Low: ModelConfig{
			Provider:  "custom",
			Model:     "custom-cheap",
			APIKeyEnv: "TEST_LOW_API_KEY",
		},
		Medium: ModelConfig{
			Provider: "custom",
			Model:    "custom-mid",
		},
		High: ModelConfig{
			Provider:  "custom",
			Model:     "custom-expensive",
			APIKeyEnv: "TEST_HIGH_API_KEY",
		},
	}

	mr := NewWithEnvOverride(cfg, slog.Default())
	ctx := context.Background()

	t.Run("low tier gets API key from env", func(t *testing.T) {
		sel, err := mr.Select(ctx, TierLow)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.APIKey != "sk-low-test" {
			t.Errorf("sel.APIKey = %q, want sk-low-test", sel.APIKey)
		}
	})

	t.Run("high tier gets API key from env", func(t *testing.T) {
		sel, err := mr.Select(ctx, TierHigh)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.APIKey != "sk-high-test" {
			t.Errorf("sel.APIKey = %q, want sk-high-test", sel.APIKey)
		}
	})

	t.Run("medium tier has no API key (not configured)", func(t *testing.T) {
		sel, err := mr.Select(ctx, TierMedium)
		if err != nil {
			t.Fatalf("Select() error = %v", err)
		}
		if sel.APIKey != "" {
			t.Errorf("sel.APIKey = %q, want empty", sel.APIKey)
		}
	})
}

func TestDefaultTierConfigurable(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()

	cfg := Config{
		DefaultTier: TierLow,
		Low: ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-haiku-20240307",
		},
		Medium: ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-sonnet-20240229",
		},
		High: ModelConfig{
			Provider: "anthropic",
			Model:    "claude-3-opus-20240229",
		},
	}

	mr := New(cfg, logger)

	sel, err := mr.Select(ctx, Tier(""))
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if sel.Tier != TierLow {
		t.Errorf("sel.Tier = %q, want low", sel.Tier)
	}
	if sel.Model != "claude-3-haiku-20240307" {
		t.Errorf("sel.Model = %q, want claude-3-haiku-20240307", sel.Model)
	}
}

func TestNewNilLogger(t *testing.T) {
	mr := New(DefaultConfig(), nil)
	if mr == nil {
		t.Fatal("New() with nil logger returned nil")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "model_router.yaml")
	content := []byte(`
default_tier: low
low:
  provider: custom
  model: custom-cheap
  api_key_env: CUSTOM_KEY
medium:
  provider: custom
  model: custom-mid
high:
  provider: custom
  model: custom-expensive
`)
	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DefaultTier != TierLow {
		t.Errorf("DefaultTier = %q, want %q", cfg.DefaultTier, TierLow)
	}
	if cfg.Low.Provider != "custom" {
		t.Errorf("Low.Provider = %q, want custom", cfg.Low.Provider)
	}
	if cfg.Low.APIKeyEnv != "CUSTOM_KEY" {
		t.Errorf("Low.APIKeyEnv = %q, want CUSTOM_KEY", cfg.Low.APIKeyEnv)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path.yaml")
	if err == nil {
		t.Error("LoadConfig() expected error for missing file, got nil")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(cfgPath, []byte("{{invalid"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Error("LoadConfig() expected error for invalid YAML, got nil")
	}
}

func TestLoadConfig_DefaultsToMediumOnMissingDefaultTier(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "no_default.yaml")
	content := []byte(`
low:
  provider: custom
  model: cheap
medium:
  provider: custom
  model: mid
high:
  provider: custom
  model: expensive
`)
	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DefaultTier != TierMedium {
		t.Errorf("DefaultTier = %q, want %q", cfg.DefaultTier, TierMedium)
	}
}
