package metrics

import (
	"sync"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

func TestSnapshot(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-b", "server-1", 200)
	reporter.TrackCost("tool-c", "server-2", 50)

	snap := Snapshot()

	if snap.TotalSpend != 350 {
		t.Errorf("TotalSpend = %d, want 350", snap.TotalSpend)
	}

	if len(snap.ByTool) != 3 {
		t.Errorf("len(ByTool) = %d, want 3", len(snap.ByTool))
	}

	if len(snap.ByServer) != 2 {
		t.Errorf("len(ByServer) = %d, want 2", len(snap.ByServer))
	}

	if len(snap.Top5ExpensiveTools) != 3 {
		t.Errorf("len(Top5ExpensiveTools) = %d, want 3 (only 3 tools tracked)", len(snap.Top5ExpensiveTools))
	}

	if snap.Top5ExpensiveTools[0].ToolName != "tool-b" {
		t.Errorf("top1 tool = %s, want tool-b", snap.Top5ExpensiveTools[0].ToolName)
	}
}

func TestSnapshotEmpty(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	snap := Snapshot()

	if snap.TotalSpend != 0 {
		t.Errorf("TotalSpend = %d, want 0", snap.TotalSpend)
	}
	if len(snap.ByTool) != 0 {
		t.Errorf("len(ByTool) = %d, want 0", len(snap.ByTool))
	}
	if len(snap.ByServer) != 0 {
		t.Errorf("len(ByServer) = %d, want 0", len(snap.ByServer))
	}
	if len(snap.Top5ExpensiveTools) != 0 {
		t.Errorf("len(Top5ExpensiveTools) = %d, want 0", len(snap.Top5ExpensiveTools))
	}
}

func TestSnapshotTop5(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	toolNames := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for i, name := range toolNames {
		reporter.TrackCost("tool-"+name, "server-1", int64((i+1)*100))
	}

	snap := Snapshot()

	if len(snap.Top5ExpensiveTools) != 5 {
		t.Errorf("len(Top5ExpensiveTools) = %d, want 5", len(snap.Top5ExpensiveTools))
	}

	if snap.Top5ExpensiveTools[0].TokenCount != 1000 {
		t.Errorf("top1 tokens = %d, want 1000", snap.Top5ExpensiveTools[0].TokenCount)
	}
	if snap.Top5ExpensiveTools[4].TokenCount != 600 {
		t.Errorf("top5 tokens = %d, want 600", snap.Top5ExpensiveTools[4].TokenCount)
	}
}

func TestSnapshotConcurrency(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			reporter.TrackCost("tool", "server", int64(n))
		}(i)
	}
	wg.Wait()

	snap := Snapshot()
	if snap.TotalSpend == 0 {
		t.Error("expected non-zero total after concurrent tracking")
	}
}
