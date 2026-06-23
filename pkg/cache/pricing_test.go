package cache

import (
	"testing"
)

func TestModelCostKnown(t *testing.T) {
	price, ok := ModelCost("claude-sonnet-4-20250514")
	if !ok {
		t.Fatal("expected pricing for claude-sonnet-4-20250514")
	}
	if price.InputCostPerMTok <= 0 {
		t.Errorf("InputCostPerMTok = %f, want > 0", price.InputCostPerMTok)
	}
	if price.CachedInputCostPerMTok <= 0 {
		t.Errorf("CachedInputCostPerMTok = %f, want > 0", price.CachedInputCostPerMTok)
	}
	if price.OutputCostPerMTok <= 0 {
		t.Errorf("OutputCostPerMTok = %f, want > 0", price.OutputCostPerMTok)
	}
	// Cached should be cheaper than input
	if price.CachedInputCostPerMTok >= price.InputCostPerMTok {
		t.Errorf("CachedInputCostPerMTok (%f) should be less than InputCostPerMTok (%f)",
			price.CachedInputCostPerMTok, price.InputCostPerMTok)
	}
}

func TestModelCostDefault(t *testing.T) {
	price, ok := ModelCost("")
	if !ok {
		t.Fatal("expected default pricing for empty model")
	}
	if price.ModelName != "claude-sonnet-4-20250514" {
		t.Errorf("ModelName = %q, want %q", price.ModelName, "claude-sonnet-4-20250514")
	}
}

func TestModelCostUnknown(t *testing.T) {
	_, ok := ModelCost("unknown-model-v1")
	if ok {
		t.Error("expected false for unknown model")
	}
}

func TestAllKnownModels(t *testing.T) {
	models := []string{
		"claude-sonnet-4-20250514",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
		"claude-3-haiku-20240307",
	}
	for _, model := range models {
		price, ok := ModelCost(model)
		if !ok {
			t.Errorf("missing pricing for known model: %s", model)
			continue
		}
		if price.InputCostPerMTok <= 0 {
			t.Errorf("%s: InputCostPerMTok = %f, want > 0", model, price.InputCostPerMTok)
		}
	}
}

func TestCalculateTokenSavingsCost(t *testing.T) {
	// 1M tokens cached on claude-sonnet-4: $3/M input - $0.30/M cached = $2.70 saved
	savings := CalculateTokenSavingsCost("claude-sonnet-4-20250514", 1000000)
	expected := 2.70
	if savings < expected-0.01 || savings > expected+0.01 {
		t.Errorf("savings = %.4f, want %.2f", savings, expected)
	}
}

func TestCalculateTokenSavingsCostZeroTokens(t *testing.T) {
	savings := CalculateTokenSavingsCost("claude-sonnet-4-20250514", 0)
	if savings != 0.0 {
		t.Errorf("savings = %.4f, want 0.0", savings)
	}
}

func TestCalculateTokenSavingsCostUnknownModel(t *testing.T) {
	savings := CalculateTokenSavingsCost("unknown-model", 1000000)
	if savings != 0.0 {
		t.Errorf("savings = %.4f, want 0.0 for unknown model", savings)
	}
}

func TestModelCostSonnet(t *testing.T) {
	price, ok := ModelCost("claude-sonnet-4-20250514")
	if !ok {
		t.Fatal("expected pricing")
	}
	if price.InputCostPerMTok != 3.0 {
		t.Errorf("InputCostPerMTok = %f, want 3.0", price.InputCostPerMTok)
	}
	if price.CachedInputCostPerMTok != 0.30 {
		t.Errorf("CachedInputCostPerMTok = %f, want 0.30", price.CachedInputCostPerMTok)
	}
}

func TestModelCost35Sonnet(t *testing.T) {
	price, ok := ModelCost("claude-3-5-sonnet-20241022")
	if !ok {
		t.Fatal("expected pricing")
	}
	if price.InputCostPerMTok != 3.0 {
		t.Errorf("InputCostPerMTok = %f, want 3.0", price.InputCostPerMTok)
	}
	if price.CachedInputCostPerMTok != 0.30 {
		t.Errorf("CachedInputCostPerMTok = %f, want 0.30", price.CachedInputCostPerMTok)
	}
}
