package injection

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Enabled bool `yaml:"enabled"`
	// Threshold is the risk from which the default response policy
	// annotates a tool output (default 70).
	Threshold      int          `yaml:"threshold"`
	Action         string       `yaml:"action"`
	CustomPatterns []PatternDef `yaml:"custom_patterns"`
	// Policies is the historical name of RequestPolicies; it is used when
	// RequestPolicies is not set.
	Policies []Rule `yaml:"policies,omitempty"`
	// RequestPolicies are the risk bands applied to client requests
	// (actions: block, quarantine, redact, log).
	RequestPolicies []Rule `yaml:"request_policies,omitempty"`
	// ResponsePolicies are the risk bands applied to what tools, resources
	// and prompts return (actions: annotate, redact, block, log). Default:
	// annotate from Threshold, log below.
	ResponsePolicies []Rule `yaml:"response_policies,omitempty"`
	// ScanResponses turns response classification off when false (default
	// true while the guard is enabled).
	ScanResponses *bool `yaml:"scan_responses,omitempty"`
	// MaxScanBytes caps how much text of one message is classified; beyond
	// it the head and the tail are sampled (default 256 KiB).
	MaxScanBytes int `yaml:"max_scan_bytes,omitempty"`
	// Judge is the optional local LLM second opinion for borderline scores
	// (off by default).
	Judge *JudgeConfig `yaml:"judge,omitempty"`
}

// DefaultMaxScanBytes is the default MaxScanBytes.
const DefaultMaxScanBytes = 256 << 10

// DefaultThreshold is the default Threshold.
const DefaultThreshold = 70

func DefaultConfig() Config {
	return Config{
		Enabled:   true,
		Threshold: DefaultThreshold,
		Action:    "block",
	}
}

// EffectiveThreshold returns Threshold clamped to 1..100 (70 when unset).
func (c *Config) EffectiveThreshold() int {
	switch {
	case c == nil || c.Threshold <= 0:
		return DefaultThreshold
	case c.Threshold > 100:
		return 100
	default:
		return c.Threshold
	}
}

// EffectiveMaxScanBytes returns MaxScanBytes, or its default when unset.
func (c *Config) EffectiveMaxScanBytes() int {
	if c == nil || c.MaxScanBytes <= 0 {
		return DefaultMaxScanBytes
	}
	return c.MaxScanBytes
}

// ScansResponses reports whether responses are classified.
func (c *Config) ScansResponses() bool {
	return c != nil && (c.ScanResponses == nil || *c.ScanResponses)
}

// requestActions and responseActions are the actions each direction
// accepts.
var (
	requestActions  = map[Action]bool{ActionBlock: true, ActionQuarantine: true, ActionRedact: true, ActionLog: true}
	responseActions = map[Action]bool{ActionAnnotate: true, ActionRedact: true, ActionBlock: true, ActionLog: true}
)

// Validate checks the policy bands, actions, limits and judge settings. A
// nil or disabled config is valid.
func (c *Config) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	if c.Threshold < 0 || c.Threshold > 100 {
		return fmt.Errorf("injection: threshold must be between 0 and 100, got %d", c.Threshold)
	}
	if c.Action != "" && !requestActions[Action(c.Action)] {
		return fmt.Errorf("injection: action %q is not a request action (block, quarantine, redact, log)", c.Action)
	}
	if err := validateRules("policies", c.Policies, requestActions); err != nil {
		return err
	}
	if err := validateRules("request_policies", c.RequestPolicies, requestActions); err != nil {
		return err
	}
	if err := validateRules("response_policies", c.ResponsePolicies, responseActions); err != nil {
		return err
	}
	if c.MaxScanBytes < 0 {
		return fmt.Errorf("injection: max_scan_bytes must be >= 0, got %d", c.MaxScanBytes)
	}
	for _, p := range c.CustomPatterns {
		if _, err := p.Compile(); err != nil {
			return fmt.Errorf("injection: custom pattern: %w", err)
		}
	}
	return c.Judge.Validate()
}

func validateRules(key string, rules []Rule, allowed map[Action]bool) error {
	for i, r := range rules {
		if !allowed[r.Action] {
			names := make([]string, 0, len(allowed))
			for _, a := range []Action{ActionAnnotate, ActionBlock, ActionQuarantine, ActionRedact, ActionLog} {
				if allowed[a] {
					names = append(names, string(a))
				}
			}
			return fmt.Errorf("injection: %s[%d]: action %q not allowed here (%s)", key, i, r.Action, strings.Join(names, ", "))
		}
		if r.MinRisk < 0 || r.MinRisk > 100 || r.MaxRisk < 0 || r.MaxRisk > 100 {
			return fmt.Errorf("injection: %s[%d]: min_risk and max_risk must be between 0 and 100", key, i)
		}
		if r.MaxRisk != 0 && r.MaxRisk < r.MinRisk {
			return fmt.Errorf("injection: %s[%d]: max_risk %d is below min_risk %d", key, i, r.MaxRisk, r.MinRisk)
		}
	}
	return nil
}

func LoadConfig(r io.Reader) (*Config, error) {
	var cfg Config
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("injection config: %w", err)
	}
	if cfg.Threshold <= 0 {
		slog.Warn("injection: threshold clamped to 70", "original", cfg.Threshold)
		cfg.Threshold = 70
	}
	if cfg.Threshold > 100 {
		slog.Warn("injection: threshold clamped to 100", "original", cfg.Threshold)
		cfg.Threshold = 100
	}
	return &cfg, nil
}

func LoadConfigFile(path string) (*Config, error) {
	r, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("injection config file: %w", err)
	}
	defer r.Close()
	return LoadConfig(r)
}

func (c *Config) BuildClassifier() (*Classifier, error) {
	if !c.Enabled {
		slog.Info("injection classifier disabled by configuration")
		return nil, nil
	}

	slog.Info("building injection classifier",
		"enabled", c.Enabled,
		"threshold", c.Threshold,
		"custom_patterns", len(c.CustomPatterns))

	classifier := NewClassifier()

	for _, def := range c.CustomPatterns {
		_, err := def.Compile()
		if err != nil {
			slog.Warn("injection: invalid custom pattern, skipping",
				"name", def.Name,
				"error", err)
			continue
		}
		if err := classifier.AddPattern(def); err != nil {
			slog.Warn("injection: failed to add custom pattern",
				"name", def.Name,
				"error", err)
			continue
		}
		slog.Debug("injection: added custom pattern",
			"name", def.Name,
			"weight", def.Weight,
			"enabled", def.Enabled)
	}

	return classifier, nil
}

// BuildDispatcher builds the request-side dispatcher: request_policies,
// else the historical policies, else the action shorthand (one 1-100
// band), else DefaultRules.
func (c *Config) BuildDispatcher() *Dispatcher {
	if c.RequestPolicies != nil {
		return NewDispatcher(c.RequestPolicies)
	}
	if c.Policies != nil {
		return NewDispatcher(c.Policies)
	}
	if c.Action != "" {
		action := Action(c.Action)
		return NewDispatcher([]Rule{
			{MinRisk: 1, MaxRisk: 100, Action: action},
		})
	}
	return NewDispatcher(nil)
}

// DefaultResponseRules annotates a response from threshold up and logs
// below it.
func DefaultResponseRules(threshold int) []Rule {
	rules := []Rule{{MinRisk: threshold, MaxRisk: 100, Action: ActionAnnotate}}
	if threshold > 1 {
		rules = append(rules, Rule{MinRisk: 1, MaxRisk: threshold - 1, Action: ActionLog})
	}
	return rules
}

// BuildResponseDispatcher builds the response-side dispatcher, or returns
// nil when responses are not scanned.
func (c *Config) BuildResponseDispatcher() *Dispatcher {
	if !c.ScansResponses() {
		return nil
	}
	if c.ResponsePolicies != nil {
		return NewDispatcher(c.ResponsePolicies)
	}
	return NewDispatcher(DefaultResponseRules(c.EffectiveThreshold()))
}
