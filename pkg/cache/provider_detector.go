package cache

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOther     Provider = "other"
)

const maxConfigBytes = 1 << 20

var providerNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type providerPattern struct {
	provider Provider
	prefixes []string
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
		if fn != nil {
			d.configFunc = fn
		}
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

func (d *ProviderDetector) Detect(rawURL string) Provider {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if rawURL == "" {
		return ProviderOther
	}
	host, ok := extractHost(rawURL)
	if !ok {
		return ProviderOther
	}
	for _, p := range d.patterns {
		for _, prefix := range p.prefixes {
			if matchHostPrefix(host, prefix) {
				return p.provider
			}
		}
	}
	return ProviderOther
}

func extractHost(rawURL string) (string, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", false
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return "", false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	host := u.Hostname()
	if host == "" {
		return "", false
	}
	return strings.ToLower(host), true
}

func matchHostPrefix(host, prefix string) bool {
	prefixHost, ok := extractHost(prefix)
	if !ok {
		return false
	}
	if host == prefixHost {
		return true
	}
	return strings.HasSuffix(host, "."+prefixHost)
}

func (d *ProviderDetector) Load() error {
	if d.configPath == "" {
		return fmt.Errorf("provider detector: no config path set")
	}
	r, err := d.configFunc(d.configPath)
	if err != nil {
		return fmt.Errorf("provider detector: open config: %w", err)
	}
	if r == nil {
		return fmt.Errorf("provider detector: open config: returned nil reader without error")
	}
	defer r.Close()
	return d.LoadReader(io.LimitReader(r, maxConfigBytes))
}

func (d *ProviderDetector) LoadReader(r io.Reader) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("provider detector: panic during load: %v", recovered)
		}
	}()

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("provider detector: read config: %w", err)
	}
	if int64(len(data)) >= maxConfigBytes {
		return fmt.Errorf("provider detector: config exceeds %d bytes", maxConfigBytes)
	}
	var cfg DetectorConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("provider detector: parse yaml: %w", err)
	}

	merged := append([]providerPattern(nil), defaultPatterns()...)
	seen := make(map[Provider]bool)
	seen[ProviderAnthropic] = true
	for _, pc := range cfg.Providers {
		name := strings.TrimSpace(strings.ToLower(pc.Name))
		if name == "" {
			continue
		}
		if !providerNameRe.MatchString(name) {
			d.logger.Warn("provider detector: skipping invalid provider name", "name", pc.Name)
			continue
		}
		if name == string(ProviderOther) {
			d.logger.Warn("provider detector: skipping reserved provider name", "name", name)
			continue
		}
		if seen[Provider(name)] {
			d.logger.Warn("provider detector: skipping duplicate provider name", "name", name)
			continue
		}
		cleaned := cleanPatterns(pc.Patterns)
		if len(cleaned) == 0 {
			d.logger.Warn("provider detector: skipping provider with no valid patterns", "name", name)
			continue
		}
		copied := append([]string(nil), cleaned...)
		merged = append(merged, providerPattern{
			provider: Provider(name),
			prefixes: copied,
		})
		seen[Provider(name)] = true
	}

	d.mu.Lock()
	d.patterns = merged
	d.mu.Unlock()
	d.logger.Info("provider detector: config loaded", "pattern_count", len(merged))
	return nil
}

func cleanPatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		if _, ok := extractHost(trimmed); !ok {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func (d *ProviderDetector) Reload() (err error) {
	if d.configPath == "" {
		d.logger.Warn("provider detector: reload skipped, no config path set")
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("provider detector: panic during reload: %v", recovered)
			d.logger.Error("provider detector: reload panic", "error", err)
		}
	}()
	err = d.Load()
	if err != nil {
		d.logger.Error("provider detector: reload failed", "error", err)
		return err
	}
	d.logger.Info("provider detector: config reloaded")
	return nil
}
