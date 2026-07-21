package metrics

import (
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

func TestServerDrilldown(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-b", "server-1", 200)
	reporter.TrackCost("tool-c", "server-2", 50)

	zero := time.Time{}

	dd := ServerDrilldown("server-1", zero)
	if dd.ServerName != "server-1" {
		t.Errorf("ServerName = %q, want server-1", dd.ServerName)
	}
	if len(dd.Tools) != 2 {
		t.Fatalf("len(Tools) = %d, want 2", len(dd.Tools))
	}

	if dd.Tools[0].ToolName != "tool-b" {
		t.Errorf("top tool = %q, want tool-b", dd.Tools[0].ToolName)
	}
	if dd.Tools[0].TokenCount != 200 {
		t.Errorf("top tool tokens = %d, want 200", dd.Tools[0].TokenCount)
	}
	if dd.Tools[0].CallCount != 1 {
		t.Errorf("top tool calls = %d, want 1", dd.Tools[0].CallCount)
	}
	if dd.Tools[0].AvgTokensCall != 200.0 {
		t.Errorf("avg tokens/call = %f, want 200.0", dd.Tools[0].AvgTokensCall)
	}

	if dd.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", dd.TotalTokens)
	}
}

func TestServerDrilldownEmpty(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	dd := ServerDrilldown("nonexistent", time.Time{})
	if dd.ServerName != "nonexistent" {
		t.Errorf("ServerName = %q", dd.ServerName)
	}
	if len(dd.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(dd.Tools))
	}
	if dd.TotalTokens != 0 {
		t.Errorf("expected 0 total, got %d", dd.TotalTokens)
	}
}

func TestToolDrilldown(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	reporter.TrackCost("tool-a", "server-1", 100)
	reporter.TrackCost("tool-a", "server-2", 200)
	reporter.TrackCost("tool-b", "server-1", 50)

	dd := ToolDrilldown("tool-a", time.Time{})
	if dd.ToolName != "tool-a" {
		t.Errorf("ToolName = %q, want tool-a", dd.ToolName)
	}
	if len(dd.Servers) != 2 {
		t.Fatalf("len(Servers) = %d, want 2", len(dd.Servers))
	}

	if dd.Servers[0].ToolName != "server-2" {
		t.Errorf("top server = %q, want server-2", dd.Servers[0].ToolName)
	}
	if dd.Servers[0].TokenCount != 200 {
		t.Errorf("top server tokens = %d, want 200", dd.Servers[0].TokenCount)
	}

	if dd.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", dd.TotalTokens)
	}
}

func TestToolDrilldownEmpty(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	dd := ToolDrilldown("nonexistent", time.Time{})
	if dd.ToolName != "nonexistent" {
		t.Errorf("ToolName = %q", dd.ToolName)
	}
	if len(dd.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(dd.Servers))
	}
}

func TestServerDrilldownSorting(t *testing.T) {
	reporter.GlobalCostTracker().Reset()
	t.Cleanup(func() { reporter.GlobalCostTracker().Reset() })

	reporter.TrackCost("tool-a", "server-1", 50)
	reporter.TrackCost("tool-b", "server-1", 300)
	reporter.TrackCost("tool-c", "server-1", 100)

	dd := ServerDrilldown("server-1", time.Time{})
	if len(dd.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(dd.Tools))
	}

	if dd.Tools[0].ToolName != "tool-b" || dd.Tools[0].TokenCount != 300 {
		t.Errorf("top = %+v, want tool-b/300", dd.Tools[0])
	}
	if dd.Tools[1].ToolName != "tool-c" || dd.Tools[1].TokenCount != 100 {
		t.Errorf("second = %+v, want tool-c/100", dd.Tools[1])
	}
	if dd.Tools[2].ToolName != "tool-a" || dd.Tools[2].TokenCount != 50 {
		t.Errorf("third = %+v, want tool-a/50", dd.Tools[2])
	}
}
