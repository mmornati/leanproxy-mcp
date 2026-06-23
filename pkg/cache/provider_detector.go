package cache

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOther     Provider = "other"
)

type providerPattern struct {
	provider Provider
	prefixes  []string
}

type ProviderConfig struct {
	Name     string   `yaml:"name"`
	Patterns []string `yaml:"patterns"`
}

type DetectorConfig struct {
	Providers []ProviderConfig `yaml:"providers"`
}

type ProviderDetector struct {
	mu         sync.RWMutex
	patterns   []providerPattern
	logger     *slog.Logger
	configPath string
	configFunc func(string) (io.ReadCloser, error)
}

type ProviderDetectorOption func(*ProviderDetector)

func WithLogger(logger *slog.Logger) ProviderDetectorOption {
	return func(d *ProviderDetector) {
		if logger != nil {
			d.logger = logger
		}
	}
}

func WithConfigPath(path string) ProviderDetectorOption {
	return func(d *ProviderDetector) {
		d.configPath = path
	}
}

func WithConfigReader(fn func(string) (io.ReadCloser, error)) ProviderDetectorOption {
	return func(d *ProviderDetector) {
		d.configFunc = fn
	}
}

func NewProviderDetector(opts ...ProviderDetectorOption) *ProviderDetector {
	d := &ProviderDetector{
		patterns:   defaultPatterns(),
		logger:     slog.Default(),
		configFunc: defaultConfigReader,
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.configPath != "" {
		if err := d.Load(); err != nil {
			d.logger.Warn("provider detector: failed to load config", "path", d.configPath, "error", err)
		}
	}
	return d
}

func defaultConfigReader(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func defaultPatterns() []providerPattern {
	return []providerPattern{
		{
			provider: ProviderAnthropic,
			prefixes: []string{
				"https://api.anthropic.com",
				"http://api.anthropic.com",
			},
		},
	}
}

func (d *ProviderDetector) Detect(url string) Provider {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, p := range d.patterns {
		for _, prefix := range p.prefixes {
			if strings.HasPrefix(url, prefix) {
				return p.provider
			}
		}
	}
	return ProviderOther
}

func (d *ProviderDetector) Load() error {
	if d.configPath == "" {
		return fmt.Errorf("provider detector: no config path set")
	}
	r, err := d.configFunc(d.configPath)
	if err != nil {
		return fmt.Errorf("provider detector: open config: %w", err)
	}
	defer r.Close()
	return d.LoadReader(r)
}

func (d *ProviderDetector) LoadReader(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("provider detector: read config: %w", err)
	}
	var cfg DetectorConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("provider detector: parse yaml: %w", err)
	}
	merged := defaultPatterns()
	for _, pc := range cfg.Providers {
		name := strings.TrimSpace(strings.ToLower(pc.Name))
		if name == "" || len(pc.Patterns) == 0 {
			continue
		}
		merged = append(merged, providerPattern{
			provider: Provider(name),
			prefixes: pc.Patterns,
		})
	}
	d.mu.Lock()
	d.patterns = merged
	d.mu.Unlock()
	d.logger.Info("provider detector: config loaded", "pattern_count", len(merged))
	return nil
}

func (d *ProviderDetector) Reload() {
	if d.configPath == "" {
		d.logger.Warn("provider detector: reload skipped, no config path set")
		return
	}
	if err := d.Load(); err != nil {
		d.logger.Error("provider detector: reload failed", "error", err)
	} else {
		d.logger.Info("provider detector: config reloaded")
	}
}
