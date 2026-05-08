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

func TestValidatePattern_Valid(t *testing.T) {
	validPatterns := []string{
		"[A-Z0-9]{20}",
		"api_key_[a-f0-9]{32}",
		"sk_live_[A-Za-z0-9]+",
		"ghp_[A-Za-z0-9]{36}",
		"Bearer [A-Za-z0-9\\-_]+\\.[A-Za-z0-9\\-_]+\\.[A-Za-z0-9\\-_]+",
		"[a-z]+",
		"\\d{3}-\\d{4}",
	}

	for _, p := range validPatterns {
		if err := ValidatePattern(p); err != nil {
			t.Errorf("pattern %q should be valid, got %v", p, err)
		}
	}
}

func TestValidatePattern_NestedQuantifiers(t *testing.T) {
	dangerousPatterns := []string{
		"(.+)+",
		"(.*)*",
		"(a+)+",
		"([a-z]+)+",
		"(x+)++",
	}

	for _, p := range dangerousPatterns {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("pattern %q should be rejected as dangerous nested quantifier", p)
		}
	}
}

func TestValidatePattern_OverlappingAlt(t *testing.T) {
	dangerousPatterns := []string{
		"(a|a)*",
		"(x|y)*",
		"(foo|bar)*",
		"(abc|def)*",
	}

	for _, p := range dangerousPatterns {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("pattern %q should be rejected as overlapping alternation", p)
		}
	}
}

func TestValidatePattern_GreedyQuantifiers(t *testing.T) {
	dangerousPatterns := []string{
		"(.*)*",
		"(a+)*",
		"(a+)++",
	}

	for _, p := range dangerousPatterns {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("pattern %q should be rejected as greedy quantifier", p)
		}
	}
}

func TestValidatePattern_ComplexBacktracking(t *testing.T) {
	dangerousPatterns := []string{
		"(a+)+(a+)+",
		"(a*)*(.*)*",
		"([a-z]+)([a-z]+)+",
	}

	for _, p := range dangerousPatterns {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("pattern %q should be rejected as complex backtracking", p)
		}
	}
}

func TestValidatePattern_EmptyPattern(t *testing.T) {
	if err := ValidatePattern(""); err != nil {
		t.Errorf("expected empty pattern to be valid, got %v", err)
	}
}

func TestValidatePattern_VeryLongPattern(t *testing.T) {
	longPattern := "a" + strings.Repeat("[a-z0-9]", 1000)
	if err := ValidatePattern(longPattern); err != nil {
		t.Errorf("expected long valid pattern to pass, got %v", err)
	}
}

func TestCompilePatterns_ValidRejectsDangerous(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{
			{Name: "dangerous", Pattern: "(.+)+"},
			{Name: "safe", Pattern: "safe-pattern"},
		},
	}
	loaded, err := cfg.CompilePatterns()
	if err != nil {
		t.Fatalf("CompilePatterns should not error on dangerous patterns: %v", err)
	}
	if len(loaded.Custom) != 1 {
		t.Errorf("expected 1 valid custom pattern after rejection, got %d", len(loaded.Custom))
	}
	if loaded.Custom[0].Name != "safe" {
		t.Errorf("expected only 'safe' pattern, got %s", loaded.Custom[0].Name)
	}
}
