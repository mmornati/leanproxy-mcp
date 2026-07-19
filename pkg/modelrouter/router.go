package modelrouter

import (
	"context"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

type Tier string

const (
	TierLow    Tier = "low"
	TierMedium Tier = "medium"
	TierHigh   Tier = "high"
)

func (t Tier) Valid() bool {
	switch t {
	case TierLow, TierMedium, TierHigh:
		return true
	default:
		return false
	}
}

type ModelSelection struct {
	Tier     Tier
	Provider string
	Model    string
	APIKey   string
}

type ModelRouter interface {
	Select(ctx context.Context, tier Tier) (ModelSelection, error)
}

type ModelConfig struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	APIKey    string `yaml:"api_key,omitempty"`
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
}

type defaultModelRouter struct {
	defaultTier Tier
	lowCfg      ModelConfig
	mediumCfg   ModelConfig
	highCfg     ModelConfig
	logger      *slog.Logger
}

type Config struct {
	DefaultTier Tier        `yaml:"default_tier"`
	Low         ModelConfig `yaml:"low"`
	Medium      ModelConfig `yaml:"medium"`
	High        ModelConfig `yaml:"high"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if !cfg.DefaultTier.Valid() {
		cfg.DefaultTier = TierMedium
	}
	return cfg, nil
}

func DefaultConfig() Config {
	return Config{
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
}

func New(cfg Config, logger *slog.Logger) ModelRouter {
	if logger == nil {
		logger = slog.Default()
	}
	defaultTier := cfg.DefaultTier
	if !defaultTier.Valid() {
		defaultTier = TierMedium
	}
	return &defaultModelRouter{
		defaultTier: defaultTier,
		lowCfg:      cfg.Low,
		mediumCfg:   cfg.Medium,
		highCfg:     cfg.High,
		logger:      logger,
	}
}

func NewWithEnvOverride(cfg Config, logger *slog.Logger) ModelRouter {
	resolveAPIKey := func(mc ModelConfig) ModelConfig {
		if mc.APIKeyEnv != "" {
			if key := os.Getenv(mc.APIKeyEnv); key != "" {
				mc.APIKey = key
			}
		}
		return mc
	}
	cfg.Low = resolveAPIKey(cfg.Low)
	cfg.Medium = resolveAPIKey(cfg.Medium)
	cfg.High = resolveAPIKey(cfg.High)
	return New(cfg, logger)
}

func (m *defaultModelRouter) Select(ctx context.Context, tier Tier) (ModelSelection, error) {
	if !tier.Valid() || tier == "" {
		m.logger.Debug("modelrouter: no valid tier, using default",
			"provided_tier", tier,
			"default_tier", m.defaultTier,
		)
		tier = m.defaultTier
	}

	var cfg ModelConfig
	switch tier {
	case TierLow:
		cfg = m.lowCfg
	case TierMedium:
		cfg = m.mediumCfg
	case TierHigh:
		cfg = m.highCfg
	default:
		cfg = m.mediumCfg
		tier = TierMedium
	}

	sel := ModelSelection{
		Tier:     tier,
		Provider: cfg.Provider,
		Model:    cfg.Model,
		APIKey:   cfg.APIKey,
	}

	m.logger.Debug("modelrouter: selected model",
		"tier", tier,
		"provider", cfg.Provider,
		"model", cfg.Model,
	)
	return sel, nil
}
