package injection

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	yamlContent := `
enabled: true
threshold: 75
custom_patterns:
  - name: "custom-injection"
    pattern: "(?i)custom\\s+override"
    weight: 80
    enabled: true
    description: "Custom test pattern"
`
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled to be true")
	}
	if cfg.Threshold != 75 {
		t.Errorf("expected threshold 75, got %d", cfg.Threshold)
	}
	if len(cfg.CustomPatterns) != 1 {
		t.Errorf("expected 1 custom pattern, got %d", len(cfg.CustomPatterns))
	}
	if cfg.CustomPatterns[0].Name != "custom-injection" {
		t.Errorf("expected name 'custom-injection', got %s", cfg.CustomPatterns[0].Name)
	}
}

func TestLoadConfig_DefaultThreshold(t *testing.T) {
	yamlContent := "enabled: true\n"
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Threshold != 70 {
		t.Errorf("expected default threshold 70, got %d", cfg.Threshold)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	yamlContent := "enabled: true\ncustom_patterns:\n  - name: \"test\n"
	_, err := LoadConfig(strings.NewReader(yamlContent))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadConfig_EmptyCustomPatterns(t *testing.T) {
	yamlContent := "enabled: true\ncustom_patterns: []\n"
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.CustomPatterns) != 0 {
		t.Errorf("expected 0 custom patterns, got %d", len(cfg.CustomPatterns))
	}
}

func TestConfig_BuildClassifier(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CustomPatterns = []PatternDef{
		{
			Name:        "custom-pattern",
			Pattern:     `(?i)custom\s+override`,
			Weight:      60,
			Enabled:     true,
			Description: "Custom test",
		},
	}

	classifier, err := cfg.BuildClassifier()
	if err != nil {
		t.Fatalf("BuildClassifier failed: %v", err)
	}

	result := classifier.Classify("custom override test")
	if result.RiskScore < 60 {
		t.Errorf("expected risk_score >= 60, got %d", result.RiskScore)
	}
}

func TestConfig_BuildClassifier_WithInvalid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CustomPatterns = []PatternDef{
		{
			Name:    "invalid",
			Pattern: `[invalid`,
			Weight:  60,
			Enabled: true,
		},
		{
			Name:    "valid",
			Pattern: `(?i)valid\s+pattern`,
			Weight:  60,
			Enabled: true,
		},
	}

	classifier, err := cfg.BuildClassifier()
	if err != nil {
		t.Fatalf("BuildClassifier failed: %v", err)
	}

	defaultCount := len(DefaultPatternDefs)
	allPatterns := classifier.Patterns()
	if len(allPatterns) != defaultCount+1 {
		t.Errorf("expected %d patterns (1 valid custom), got %d", defaultCount+1, len(allPatterns))
	}
}

func TestConfig_BuildClassifier_Disabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false

	classifier, err := cfg.BuildClassifier()
	if err != nil {
		t.Fatalf("BuildClassifier failed: %v", err)
	}
	if classifier != nil {
		t.Fatal("expected nil classifier when disabled")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Enabled {
		t.Error("expected Enabled to be true")
	}
	if cfg.Threshold != 70 {
		t.Errorf("expected threshold 70, got %d", cfg.Threshold)
	}
	if len(cfg.CustomPatterns) != 0 {
		t.Errorf("expected 0 custom patterns, got %d", len(cfg.CustomPatterns))
	}
}

func TestLoadConfig_ThresholdBounds(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 70},
		{-1, 70},
		{50, 50},
		{75, 75},
		{100, 100},
		{150, 100},
	}

	for _, tt := range tests {
		yamlContent := fmt.Sprintf("enabled: true\nthreshold: %d\n", tt.input)
		cfg, err := LoadConfig(strings.NewReader(yamlContent))
		if err != nil {
			t.Fatalf("LoadConfig failed for threshold %d: %v", tt.input, err)
		}
		if cfg.Threshold != tt.expected {
			t.Errorf("for threshold input %d: expected %d, got %d", tt.input, tt.expected, cfg.Threshold)
		}
	}
}

func TestLoadConfig_CustomPatterns(t *testing.T) {
	yamlContent := `
enabled: true
threshold: 80
custom_patterns:
  - name: "pattern1"
    pattern: "(?i)p1"
    weight: 50
    enabled: true
    description: "Pattern 1"
  - name: "pattern2"
    pattern: "(?i)p2"
    weight: 75
    enabled: false
    description: "Pattern 2"
`
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Threshold != 80 {
		t.Errorf("expected threshold 80, got %d", cfg.Threshold)
	}
	if len(cfg.CustomPatterns) != 2 {
		t.Errorf("expected 2 custom patterns, got %d", len(cfg.CustomPatterns))
	}
	if cfg.CustomPatterns[0].Name != "pattern1" {
		t.Errorf("expected name 'pattern1', got %s", cfg.CustomPatterns[0].Name)
	}
	if !cfg.CustomPatterns[0].Enabled {
		t.Error("expected pattern1 to be enabled")
	}
	if cfg.CustomPatterns[1].Enabled {
		t.Error("expected pattern2 to be disabled")
	}
}

func TestConfig_BuildClassifier_NoCustom(t *testing.T) {
	cfg := DefaultConfig()
	classifier, err := cfg.BuildClassifier()
	if err != nil {
		t.Fatalf("BuildClassifier failed: %v", err)
	}

	allPatterns := classifier.Patterns()
	if len(allPatterns) != len(DefaultPatternDefs) {
		t.Errorf("expected %d default patterns, got %d", len(DefaultPatternDefs), len(allPatterns))
	}
}

func TestConfig_BuildDispatcher_Default(t *testing.T) {
	cfg := DefaultConfig()
	d := cfg.BuildDispatcher()
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	rules := d.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 catch-all rule from Action shorthand, got %d", len(rules))
	}
	if rules[0].Action != ActionBlock {
		t.Errorf("expected block action from DefaultConfig, got %s", rules[0].Action)
	}
	if rules[0].MinRisk != 1 || rules[0].MaxRisk != 100 {
		t.Errorf("expected catch-all rule (1-100), got %d-%d", rules[0].MinRisk, rules[0].MaxRisk)
	}
}

func TestConfig_BuildDispatcher_WithPolicies(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Policies = []Rule{
		{MinRisk: 90, MaxRisk: 100, Action: ActionBlock},
		{MinRisk: 1, MaxRisk: 89, Action: ActionLog},
	}
	d := cfg.BuildDispatcher()
	rules := d.Rules()
	if len(rules) != 2 {
		t.Fatalf("expected 2 custom rules, got %d", len(rules))
	}
	if rules[0].MinRisk != 90 {
		t.Errorf("expected MinRisk 90, got %d", rules[0].MinRisk)
	}
	if rules[1].Action != ActionLog {
		t.Errorf("expected ActionLog for second rule, got %s", rules[1].Action)
	}
}

func TestConfig_BuildDispatcher_BlockPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Policies = []Rule{
		{MinRisk: 80, MaxRisk: 100, Action: ActionBlock},
	}
	d := cfg.BuildDispatcher()
	result := d.Dispatch(Result{RiskScore: 85, Payload: "test"})
	if result.Action != ActionBlock {
		t.Errorf("expected block, got %s", result.Action)
	}
}

func TestConfig_BuildDispatcher_EmptyPolicies(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Policies = []Rule{}
	d := cfg.BuildDispatcher()
	rules := d.Rules()
	if len(rules) != 0 {
		t.Errorf("expected 0 rules with empty policies, got %d", len(rules))
	}
}

func TestConfig_BuildDispatcher_ActionShorthand(t *testing.T) {
	cfg := &Config{
		Enabled:   true,
		Threshold: 70,
		Action:    "redact",
	}
	d := cfg.BuildDispatcher()
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	rules := d.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 catch-all rule from Action shorthand, got %d", len(rules))
	}
	if rules[0].Action != ActionRedact {
		t.Errorf("expected redact action, got %s", rules[0].Action)
	}
	if rules[0].MinRisk != 1 || rules[0].MaxRisk != 100 {
		t.Errorf("expected catch-all rule (1-100), got %d-%d", rules[0].MinRisk, rules[0].MaxRisk)
	}
}

func TestConfig_BuildDispatcher_NoActionNoPolicies(t *testing.T) {
	cfg := &Config{
		Enabled:   true,
		Threshold: 70,
	}
	d := cfg.BuildDispatcher()
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	rules := d.Rules()
	if len(rules) != 3 {
		t.Fatalf("expected 3 default rules, got %d", len(rules))
	}
}

func TestConfig_BuildDispatcher_PoliciesOverrideAction(t *testing.T) {
	cfg := &Config{
		Enabled:   true,
		Threshold: 70,
		Action:    "block",
		Policies: []Rule{
			{MinRisk: 50, MaxRisk: 100, Action: ActionLog},
		},
	}
	d := cfg.BuildDispatcher()
	rules := d.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 policy rule, got %d", len(rules))
	}
	if rules[0].Action != ActionLog {
		t.Errorf("expected log action from policies, got %s", rules[0].Action)
	}
}

func TestLoadConfig_WithPolicies(t *testing.T) {
	yamlContent := `
enabled: true
threshold: 70
policies:
  - min_risk: 80
    max_risk: 100
    action: block
  - min_risk: 1
    max_risk: 79
    action: log
`
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(cfg.Policies))
	}
	if cfg.Policies[0].Action != ActionBlock {
		t.Errorf("expected block action, got %s", cfg.Policies[0].Action)
	}
	if cfg.Policies[1].Action != ActionLog {
		t.Errorf("expected log action, got %s", cfg.Policies[1].Action)
	}
	d := cfg.BuildDispatcher()
	result := d.Dispatch(Result{RiskScore: 90, Payload: "test"})
	if result.Action != ActionBlock {
		t.Errorf("expected block from policies, got %s", result.Action)
	}
}
