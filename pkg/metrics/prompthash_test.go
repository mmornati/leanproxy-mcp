package metrics

import (
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

func TestServerToolPromptHashes(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	reporter.TrackCostFromStrings("tool-a", "server-1", `{"q":"hello"}`, `{"a":"world"}`)
	reporter.TrackCostFromStrings("tool-a", "server-1", `{"q":"hello"}`, `{"a":"world"}`)
	reporter.TrackCostFromStrings("tool-b", "server-2", `{"q":"foo"}`, `{"a":"bar"}`)

	ph := ServerToolPromptHashes("server-1", "tool-a")
	if len(ph.Hashes) == 0 {
		t.Fatal("expected at least 1 hash")
	}

	var total int64
	for _, h := range ph.Hashes {
		total += h.TokenCost
		if h.Hash == "" {
			t.Error("expected non-empty hash")
		}
	}

	if total == 0 {
		t.Error("expected non-zero total cost")
	}
}

func TestServerToolPromptHashesEmpty(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	ph := ServerToolPromptHashes("nonexistent", "nonexistent")
	if len(ph.Hashes) != 0 {
		t.Errorf("expected 0 hashes, got %d", len(ph.Hashes))
	}
	if ph.Total != 0 {
		t.Errorf("expected Total=0, got %d", ph.Total)
	}
}

func TestServerToolPromptHashesNoHashes(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	reporter.TrackCost("tool-a", "server-1", 100)

	ph := ServerToolPromptHashes("server-1", "tool-a")
	if len(ph.Hashes) != 0 {
		t.Errorf("expected 0 hashes, got %d", len(ph.Hashes))
	}
}

func TestServerToolPromptHashesSorting(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	reporter.TrackCostFromStrings("tool-a", "server-1", `{"q":"small"}`, `{"a":"x"}`)

	ph := ServerToolPromptHashes("server-1", "tool-a")
	if len(ph.Hashes) > 0 {
		for i := 1; i < len(ph.Hashes); i++ {
			if ph.Hashes[i-1].TokenCost < ph.Hashes[i].TokenCost {
				t.Errorf("hashes not sorted by token cost desc: %+v", ph.Hashes)
				break
			}
		}
	}
}
