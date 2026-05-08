package reporter

import (
	"sync"
	"testing"
)

func TestCostTrackerTrack(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool2", "server1", 200)
	tracker.Track("tool1", "server2", 150)

	breakdown := tracker.GetBreakdown()

	if breakdown.Total != 450 {
		t.Errorf("Total = %d, want 450", breakdown.Total)
	}

	byTool := breakdown.ByTool
	if len(byTool) != 2 {
		t.Errorf("ByTool length = %d, want 2", len(byTool))
	}

	byToolMap := make(map[string]int64)
	for _, tc := range byTool {
		byToolMap[tc.ToolName] = tc.TokenCount
	}

	if byToolMap["tool1"] != 250 {
		t.Errorf("tool1 tokens = %d, want 250", byToolMap["tool1"])
	}
	if byToolMap["tool2"] != 200 {
		t.Errorf("tool2 tokens = %d, want 200", byToolMap["tool2"])
	}

	byServer := breakdown.ByServer
	if len(byServer) != 2 {
		t.Errorf("ByServer length = %d, want 2", len(byServer))
	}

	byServerMap := make(map[string]int64)
	for _, sc := range byServer {
		byServerMap[sc.ServerName] = sc.TokenCount
	}

	if byServerMap["server1"] != 300 {
		t.Errorf("server1 tokens = %d, want 300", byServerMap["server1"])
	}
	if byServerMap["server2"] != 150 {
		t.Errorf("server2 tokens = %d, want 150", byServerMap["server2"])
	}
}

func TestCostTrackerGetBreakdown(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	breakdown := tracker.GetBreakdown()

	if len(breakdown.ByTool) != 1 {
		t.Errorf("ByTool length = %d, want 1", len(breakdown.ByTool))
	}
	if breakdown.ByTool[0].ToolName != "tool1" {
		t.Errorf("ByTool[0].ToolName = %s, want tool1", breakdown.ByTool[0].ToolName)
	}
	if breakdown.ByTool[0].TokenCount != 100 {
		t.Errorf("ByTool[0].TokenCount = %d, want 100", breakdown.ByTool[0].TokenCount)
	}
}

func TestCostTrackerFormatCLI(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool2", "server1", 200)

	output := tracker.FormatCLI(false, false)
	if output == "" {
		t.Error("Expected non-empty output")
	}

	if !contains(output, "Total Session Tokens:") {
		t.Error("Expected total tokens in output")
	}

	if !contains(output, "Token Cost by Tool") {
		t.Error("Expected by-tool section in output")
	}

	if !contains(output, "Token Cost by Server") {
		t.Error("Expected by-server section in output")
	}
}

func TestCostTrackerFormatCLIBYTool(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool2", "server1", 200)

	output := tracker.FormatCLI(true, false)
	if !contains(output, "Token Cost by Tool") {
		t.Error("Expected by-tool section in output")
	}
}

func TestCostTrackerFormatCLIBYServer(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool2", "server2", 200)

	output := tracker.FormatCLI(false, true)
	if !contains(output, "Token Cost by Server") {
		t.Error("Expected by-server section in output")
	}
}

func TestCostTrackerFormatJSON(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)

	output, err := tracker.FormatJSON()
	if err != nil {
		t.Errorf("FormatJSON() error = %v", err)
	}
	if output == "" {
		t.Error("Expected non-empty JSON output")
	}
}

func TestCostTrackerReset(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Reset()

	breakdown := tracker.GetBreakdown()
	if breakdown.Total != 0 {
		t.Errorf("After Reset: Total = %d, want 0", breakdown.Total)
	}
	if len(breakdown.ByTool) != 0 {
		t.Errorf("After Reset: ByTool length = %d, want 0", len(breakdown.ByTool))
	}
	if len(breakdown.ByServer) != 0 {
		t.Errorf("After Reset: ByServer length = %d, want 0", len(breakdown.ByServer))
	}
}

func TestCostTrackerGetByTool(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool2", "server1", 200)

	byTool := tracker.GetByTool()
	if len(byTool) != 2 {
		t.Errorf("GetByTool() length = %d, want 2", len(byTool))
	}
	if byTool["tool1"] != 100 {
		t.Errorf("tool1 = %d, want 100", byTool["tool1"])
	}
	if byTool["tool2"] != 200 {
		t.Errorf("tool2 = %d, want 200", byTool["tool2"])
	}
}

func TestCostTrackerGetByServer(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool2", "server2", 200)

	byServer := tracker.GetByServer()
	if len(byServer) != 2 {
		t.Errorf("GetByServer() length = %d, want 2", len(byServer))
	}
	if byServer["server1"] != 100 {
		t.Errorf("server1 = %d, want 100", byServer["server1"])
	}
	if byServer["server2"] != 200 {
		t.Errorf("server2 = %d, want 200", byServer["server2"])
	}
}

func TestCostTrackerGetTotal(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool2", "server1", 200)

	total := tracker.GetTotal()
	if total != 300 {
		t.Errorf("GetTotal() = %d, want 300", total)
	}
}

func TestCostTrackerThreadSafety(t *testing.T) {
	tracker := NewCostTracker()

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			toolName := "tool"
			serverName := "server"
			if id%2 == 0 {
				toolName = "tool1"
				serverName = "server1"
			} else {
				toolName = "tool2"
				serverName = "server2"
			}
			tracker.Track(toolName, serverName, 10)
		}(i)
	}

	wg.Wait()

	total := tracker.GetTotal()
	expectedTotal := int64(numGoroutines * 10)
	if total != expectedTotal {
		t.Errorf("Total = %d, want %d (thread safety)", total, expectedTotal)
	}
}

func TestCostTrackerSorting(t *testing.T) {
	tracker := NewCostTracker()

	tracker.Track("tool3", "server1", 100)
	tracker.Track("tool1", "server1", 300)
	tracker.Track("tool2", "server1", 200)

	breakdown := tracker.GetBreakdown()

	if len(breakdown.ByTool) != 3 {
		t.Fatalf("ByTool length = %d, want 3", len(breakdown.ByTool))
	}

	if breakdown.ByTool[0].ToolName != "tool1" {
		t.Errorf("First tool should be tool1 (highest), got %s", breakdown.ByTool[0].ToolName)
	}
	if breakdown.ByTool[0].TokenCount != 300 {
		t.Errorf("First tool tokens = %d, want 300", breakdown.ByTool[0].TokenCount)
	}

	if breakdown.ByTool[1].ToolName != "tool2" {
		t.Errorf("Second tool should be tool2, got %s", breakdown.ByTool[1].ToolName)
	}

	if breakdown.ByTool[2].ToolName != "tool3" {
		t.Errorf("Third tool should be tool3, got %s", breakdown.ByTool[2].ToolName)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (s[:len(substr)] == substr || contains(s[1:], substr)))
}
