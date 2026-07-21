package reporter

import (
	"sort"
	"sync"
	"testing"
	"time"
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

func TestCostTrackerTrackAt(t *testing.T) {
	clock := &mockClock{now: time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)}
	tracker := newCostTracker(clock)

	tracker.TrackAt("tool1", "server1", 100, clock.Now())
	tracker.TrackAt("tool1", "server1", 50, clock.Now().Add(time.Hour))
	tracker.TrackAt("tool2", "server1", 200, clock.Now().Add(2*time.Hour))

	stats := tracker.GetServerToolStats("server1")
	if len(stats) != 2 {
		t.Fatalf("GetServerToolStats length = %d, want 2", len(stats))
	}

	for _, s := range stats {
		if s.ToolName == "tool1" {
			if s.CallCount != 2 {
				t.Errorf("tool1 call count = %d, want 2", s.CallCount)
			}
			if s.TokenCount != 150 {
				t.Errorf("tool1 token count = %d, want 150", s.TokenCount)
			}
			if !s.LastInvoked.Equal(clock.Now().Add(time.Hour)) {
				t.Errorf("tool1 last invoked = %v, want %v", s.LastInvoked, clock.Now().Add(time.Hour))
			}
		}
		if s.ToolName == "tool2" {
			if s.CallCount != 1 {
				t.Errorf("tool2 call count = %d, want 1", s.CallCount)
			}
			if s.TokenCount != 200 {
				t.Errorf("tool2 token count = %d, want 200", s.TokenCount)
			}
		}
	}
}

func TestCostTrackerTrackWithPromptHash(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	tracker := newCostTracker(clock)

	tracker.TrackWithPromptHash("tool1", "server1", 100, "abc123", clock)
	tracker.TrackWithPromptHash("tool1", "server1", 50, "abc123", clock)
	tracker.TrackWithPromptHash("tool2", "server2", 200, "def456", clock)

	stats := tracker.GetServerToolStats("server1")
	if len(stats) != 1 {
		t.Fatalf("GetServerToolStats length = %d, want 1", len(stats))
	}
	if stats[0].CallCount != 2 {
		t.Errorf("call count = %d, want 2", stats[0].CallCount)
	}
	if stats[0].TokenCount != 150 {
		t.Errorf("token count = %d, want 150", stats[0].TokenCount)
	}

	hashes := tracker.GetPromptHashes()
	if len(hashes) != 2 {
		t.Fatalf("GetPromptHashes length = %d, want 2", len(hashes))
	}
	if hashes["abc123"] != 150 {
		t.Errorf("abc123 cost = %d, want 150", hashes["abc123"])
	}
	if hashes["def456"] != 200 {
		t.Errorf("def456 cost = %d, want 200", hashes["def456"])
	}
}

func TestCostTrackerGetToolServerStats(t *testing.T) {
	tracker := NewCostTracker()
	tracker.Track("tool1", "server1", 100)
	tracker.Track("tool1", "server2", 200)
	tracker.Track("tool2", "server1", 50)

	stats := tracker.GetToolServerStats("tool1")
	if len(stats) != 2 {
		t.Fatalf("GetToolServerStats length = %d, want 2", len(stats))
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].ServerName < stats[j].ServerName
	})
	if stats[0].ServerName != "server1" || stats[0].TokenCount != 100 {
		t.Errorf("server1 = %+v, want TokenCount=100", stats[0])
	}
	if stats[1].ServerName != "server2" || stats[1].TokenCount != 200 {
		t.Errorf("server2 = %+v, want TokenCount=200", stats[1])
	}
}

func TestCostTrackerGetServerToolStatsEmpty(t *testing.T) {
	tracker := NewCostTracker()
	stats := tracker.GetServerToolStats("nonexistent")
	if len(stats) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(stats))
	}
}

func TestCostTrackerPromptHash(t *testing.T) {
	h1 := promptHash(`{"model":"gpt-4"}`, `{"content":"hello"}`)
	h2 := promptHash(`{"model":"gpt-4"}`, `{"content":"hello"}`)
	h3 := promptHash(`{"model":"gpt-4"}`, `{"content":"world"}`)

	if h1 == "" {
		t.Error("expected non-empty hash")
	}
	if h1 != h2 {
		t.Error("same inputs should produce same hash")
	}
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestCostTrackerGetPromptHashesForServerTool(t *testing.T) {
	tracker := NewCostTracker()
	tracker.TrackWithPromptHash("tool1", "server1", 100, "hash1", defaultClock)
	tracker.TrackWithPromptHash("tool1", "server1", 50, "hash2", defaultClock)

	hashes := tracker.GetPromptHashesForServerTool("server1", "tool1")
	if len(hashes) != 2 {
		t.Fatalf("expected 2 hashes, got %d", len(hashes))
	}
	if hashes["hash1"] != 100 {
		t.Errorf("hash1 = %d, want 100", hashes["hash1"])
	}
}

type mockClock struct {
	now time.Time
}

func (m *mockClock) Now() time.Time                  { return m.now }
func (m *mockClock) Since(t time.Time) time.Duration { return m.now.Sub(t) }

func TestCostTrackerGetEntries(t *testing.T) {
	base := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	clock := &mockClock{now: base}
	tracker := newCostTracker(clock)

	tracker.TrackAt("tool1", "server1", 100, base)
	tracker.TrackAt("tool2", "server1", 200, base.Add(-12*time.Hour))
	tracker.TrackAt("tool3", "server2", 50, base.Add(-48*time.Hour))

	since := base.Add(-24 * time.Hour)
	entries := tracker.GetEntries(since)
	if len(entries) != 2 {
		t.Fatalf("GetEntries(since %v) = %d, want 2", since, len(entries))
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
	if entries[0].ToolName != "tool2" {
		t.Errorf("first entry tool = %s, want tool2", entries[0].ToolName)
	}
	if entries[1].ToolName != "tool1" {
		t.Errorf("second entry tool = %s, want tool1", entries[1].ToolName)
	}
}

func TestCostTrackerGetEntriesZeroTime(t *testing.T) {
	clock := &mockClock{now: time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)}
	tracker := newCostTracker(clock)

	tracker.TrackAt("tool1", "server1", 100, clock.Now())
	tracker.TrackAt("tool2", "server2", 200, clock.Now().Add(-48*time.Hour))

	entries := tracker.GetEntries(time.Time{})
	if len(entries) != 2 {
		t.Fatalf("GetEntries(zero) = %d, want 2", len(entries))
	}
}

func TestCostTrackerGetEntriesEmpty(t *testing.T) {
	tracker := NewCostTracker()
	entries := tracker.GetEntries(time.Now())
	if len(entries) != 0 {
		t.Errorf("expected empty, got %d", len(entries))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (s[:len(substr)] == substr || contains(s[1:], substr)))
}
