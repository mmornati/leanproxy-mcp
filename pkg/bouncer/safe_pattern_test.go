package bouncer

import (
	"testing"
)

func TestValidatePattern_Empty(t *testing.T) {
	if err := ValidatePattern(""); err != nil {
		t.Errorf("expected empty pattern to be valid, got %v", err)
	}
}

func TestValidatePattern_ValidPatterns(t *testing.T) {
	validPatterns := []string{
		`[A-Z0-9]{20}`,
		`api_key_[a-f0-9]{32}`,
		`sk_live_[A-Za-z0-9]+`,
		`ghp_[A-Za-z0-9]{36}`,
		`Bearer [A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`,
		`[a-z]+`,
		`\d{3}-\d{4}`,
		`[^@]+@[^\.]+\.[a-z]{2,}`,
	}

	for _, p := range validPatterns {
		if err := ValidatePattern(p); err != nil {
			t.Errorf("pattern %q should be valid, got %v", p, err)
		}
	}
}

func TestValidatePattern_DangerousNestedQuantifiers(t *testing.T) {
	dangerousPatterns := []string{
		`(.+)+`,
		`(.*)*`,
		`(a+)*`,
		`([a-z]+)+`,
	}

	for _, p := range dangerousPatterns {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("pattern %q should be rejected as dangerous", p)
		}
	}
}

func TestValidatePattern_DangerousAlternation(t *testing.T) {
	dangerousPatterns := []string{
		`(a|a)*`,
		`(x|y)*`,
		`(foo|bar)*`,
	}

	for _, p := range dangerousPatterns {
		if err := ValidatePattern(p); err == nil {
			t.Errorf("pattern %q should be rejected as dangerous", p)
		}
	}
}

func TestValidatePattern_ErrorMessages(t *testing.T) {
	pattern := `(.+)+`
	err := ValidatePattern(pattern)
	if err == nil {
		t.Fatal("expected error for dangerous pattern")
	}

	if err != ErrDangerousPattern && err.Error()[:len(ErrDangerousPattern.Error())] != ErrDangerousPattern.Error() {
		t.Errorf("expected ErrDangerousPattern error, got %v", err)
	}
}

func TestSafeCompile_ValidPattern(t *testing.T) {
	re, err := SafeCompile(`[A-Z0-9]{20}`)
	if err != nil {
		t.Fatalf("SafeCompile failed for valid pattern: %v", err)
	}
	if re == nil {
		t.Fatal("expected non-nil regex")
	}
}

func TestSafeCompile_DangerousPattern(t *testing.T) {
	_, err := SafeCompile(`(.+)+`)
	if err == nil {
		t.Fatal("expected error for dangerous pattern")
	}
}

func TestSafeCompileMust_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for dangerous pattern")
		}
	}()
	SafeCompileMust(`(.+)+`)
}

func TestIsPatternSafe(t *testing.T) {
	if !IsPatternSafe(`[A-Z0-9]{20}`) {
		t.Error("expected valid pattern to be safe")
	}
	if IsPatternSafe(`(.+)+`) {
		t.Error("expected dangerous pattern to be unsafe")
	}
}

func TestFindDangerousPatterns(t *testing.T) {
	patterns := []string{
		`[A-Z0-9]{20}`,
		`(.+)+`,
		`api_key_[a-f0-9]{32}`,
		`(.*)*`,
	}

	unsafe, err := FindDangerousPatterns(patterns)
	if err != nil {
		t.Fatalf("FindDangerousPatterns failed: %v", err)
	}
	if len(unsafe) != 2 {
		t.Errorf("expected 2 unsafe patterns, got %d", len(unsafe))
	}
}

func TestFindDangerousPatterns_AllSafe(t *testing.T) {
	patterns := []string{
		`[A-Z0-9]{20}`,
		`api_key_[a-f0-9]{32}`,
	}

	unsafe, err := FindDangerousPatterns(patterns)
	if err != nil {
		t.Fatalf("FindDangerousPatterns failed: %v", err)
	}
	if len(unsafe) != 0 {
		t.Errorf("expected 0 unsafe patterns, got %d", len(unsafe))
	}
}

func TestStripComments(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`hello`, `hello`},
		{`hello #comment`, `hello `},
		{`hello  # inline comment`, `hello  `},
		{`[a-z] #class`, `[a-z] `},
	}

	for _, tc := range tests {
		result := StripComments(tc.input)
		if result != tc.expected {
			t.Errorf("StripComments(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestConfigCompilePatterns_RejectsDangerous(t *testing.T) {
	cfg := &Config{
		CustomPatterns: []PatternDef{
			{Name: "dangerous", Pattern: `(.+)+`},
			{Name: "safe", Pattern: `safe-pattern`},
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

func TestBoilerplatePruner_RejectsDangerous(t *testing.T) {
	customPatterns := []PatternDef{
		{Name: "dangerous", Pattern: `(.*)*`},
		{Name: "safe", Pattern: `safe-pattern`},
	}

	bp := NewBoilerplatePruner(true, true, true, customPatterns)

	customPatternsInBp := bp.patterns["custom"]
	if len(customPatternsInBp) != 1 {
		t.Errorf("expected 1 valid boilerplate pattern after rejection, got %d", len(customPatternsInBp))
	}
	if len(customPatternsInBp) > 0 && customPatternsInBp[0].name != "safe" {
		t.Errorf("expected only 'safe' pattern, got %s", customPatternsInBp[0].name)
	}
}

func TestCompilePatterns_RejectsDangerous(t *testing.T) {
	configs := []PatternConfig{
		{Name: "dangerous", Pattern: `(.+)+`},
		{Name: "safe", Pattern: `safe-pattern`},
	}

	_, err := CompilePatterns(configs)
	if err == nil {
		t.Fatal("expected error for dangerous pattern")
	}
}
