package bouncer

import (
	"strings"
	"testing"
)

func TestLoadConfigValidYAML(t *testing.T) {
	yamlContent := `
enabled: true
custom_patterns:
  - name: "company-api-key"
    pattern: "my-company-key-[A-Z0-9]{20}"
  - name: "internal-token"
    pattern: "int_token_[a-f0-9]{32}"
`
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled to be true")
	}
	if len(cfg.CustomPatterns) != 2 {
		t.Errorf("expected 2 custom patterns, got %d", len(cfg.CustomPatterns))
	}
}

func TestLoadConfigMissingOptionalFields(t *testing.T) {
	yamlContent := `
enabled: false
`
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Enabled {
		t.Error("expected Enabled to be false")
	}
	if len(cfg.CustomPatterns) != 0 {
		t.Errorf("expected 0 custom patterns, got %d", len(cfg.CustomPatterns))
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	yamlContent := `
enabled: true
custom_patterns:
  - name: "test"
    pattern: invalid: yaml: content: with: too: many: colons
  invalid yaml here
`
	_, err := LoadConfig(strings.NewReader(yamlContent))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadConfigEmptyCustomPatterns(t *testing.T) {
	yamlContent := `
enabled: true
custom_patterns: []
`
	cfg, err := LoadConfig(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.CustomPatterns) != 0 {
		t.Errorf("expected 0 custom patterns, got %d", len(cfg.CustomPatterns))
	}
}

func TestCompilePatternsValidCustom(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{
			{Name: "company-key", Pattern: "my-company-key-[A-Z0-9]{20}"},
		},
	}
	loaded, err := cfg.CompilePatterns()
	if err != nil {
		t.Fatalf("CompilePatterns failed: %v", err)
	}
	if len(loaded.All) != len(BuiltInPatterns)+1 {
		t.Errorf("expected %d total patterns, got %d", len(BuiltInPatterns)+1, len(loaded.All))
	}
	if len(loaded.Custom) != 1 {
		t.Errorf("expected 1 custom pattern, got %d", len(loaded.Custom))
	}
	if loaded.Custom[0].Name != "company-key" {
		t.Errorf("expected custom pattern name 'company-key', got %s", loaded.Custom[0].Name)
	}
}

func TestCompilePatternsInvalidRegexSkipped(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{
			{Name: "invalid", Pattern: "[invalid(regex"},
			{Name: "valid", Pattern: "valid-pattern"},
		},
	}
	loaded, err := cfg.CompilePatterns()
	if err != nil {
		t.Fatalf("CompilePatterns should not error on invalid patterns: %v", err)
	}
	if len(loaded.Custom) != 1 {
		t.Errorf("expected 1 valid custom pattern, got %d", len(loaded.Custom))
	}
	if loaded.Custom[0].Name != "valid" {
		t.Errorf("expected only 'valid' pattern, got %s", loaded.Custom[0].Name)
	}
}

func TestCompilePatternsOrderCustomFirst(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{
			{Name: "custom1", Pattern: "CUSTOM1"},
			{Name: "custom2", Pattern: "CUSTOM2"},
		},
	}
	loaded, err := cfg.CompilePatterns()
	if err != nil {
		t.Fatalf("CompilePatterns failed: %v", err)
	}
	if len(loaded.All) != len(BuiltInPatterns)+2 {
		t.Errorf("expected %d total patterns, got %d", len(BuiltInPatterns)+2, len(loaded.All))
	}
	customCount := 0
	for _, p := range loaded.All {
		if p.String() == "CUSTOM1" || p.String() == "CUSTOM2" {
			customCount++
		}
	}
	if customCount != 2 {
		t.Errorf("expected 2 custom patterns in All, got %d", customCount)
	}
}

func TestCustomPatternRedaction(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{
			{Name: "company-key", Pattern: "my-company-key-[A-Z0-9]{20}"},
		},
	}
	loaded, err := cfg.CompilePatterns()
	if err != nil {
		t.Fatalf("CompilePatterns failed: %v", err)
	}

	input := `{"key": "my-company-key-ABC123XYZ789012345678"}`
	redacted := RedactWithPatterns(input, loaded.All)
	if !strings.Contains(redacted, "[SECRET_REDACTED]") {
		t.Error("expected secret to be redacted")
	}
	if strings.Contains(redacted, "ABC123XYZ789012345678") {
		t.Error("secret should not be present in redacted output")
	}
}

func TestCompilePatternsNoCustom(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{},
	}
	loaded, err := cfg.CompilePatterns()
	if err != nil {
		t.Fatalf("CompilePatterns failed: %v", err)
	}
	if len(loaded.All) != len(BuiltInPatterns) {
		t.Errorf("expected %d built-in patterns, got %d", len(BuiltInPatterns), len(loaded.All))
	}
	if len(loaded.Custom) != 0 {
		t.Errorf("expected 0 custom patterns, got %d", len(loaded.Custom))
	}
}

func TestLoadedPatternsContainsBuiltInAndCustom(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{
			{Name: "test", Pattern: "TEST\\d+"},
		},
	}
	loaded, err := cfg.CompilePatterns()
	if err != nil {
		t.Fatalf("CompilePatterns failed: %v", err)
	}
	if len(loaded.BuiltIn) != len(BuiltInPatterns) {
		t.Errorf("BuiltIn should contain all built-in patterns")
	}
	for i, bp := range BuiltInPatterns {
		if loaded.BuiltIn[i].Name != bp.Name {
			t.Errorf("BuiltIn pattern mismatch at index %d", i)
		}
	}
}

func TestPatternDefStruct(t *testing.T) {
	pd := PatternDef{
		Name:    "test-pattern",
		Pattern: "test-[a-z]+",
	}
	if pd.Name != "test-pattern" {
		t.Errorf("expected Name 'test-pattern', got %s", pd.Name)
	}
	if pd.Pattern != "test-[a-z]+" {
		t.Errorf("expected Pattern 'test-[a-z]+', got %s", pd.Pattern)
	}
}