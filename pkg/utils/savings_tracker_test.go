package utils

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSavingsTrackerRecordRequest(t *testing.T) {
	tracker := NewSavingsTracker()

	err := tracker.RecordRequest("server1", "abcdefghijklmnop", "ab")
	if err != nil {
		t.Fatalf("RecordRequest() error = %v", err)
	}

	cumulative := tracker.GetCumulativeSavings()
	if cumulative.TotalOriginal != 4 {
		t.Errorf("TotalOriginal = %d, want 4", cumulative.TotalOriginal)
	}
	if cumulative.TotalOptimized != 1 {
		t.Errorf("TotalOptimized = %d, want 1", cumulative.TotalOptimized)
	}
	if cumulative.TotalSaved != 3 {
		t.Errorf("TotalSaved = %d, want 3", cumulative.TotalSaved)
	}
}

func TestSavingsTrackerCumulativeCalculations(t *testing.T) {
	tracker := NewSavingsTracker()

	tracker.RecordRequest("server1", "abcdefghijklmnop", "ab")
	tracker.RecordRequest("server1", "ijklmnop", "ij")

	cumulative := tracker.GetCumulativeSavings()
	if cumulative.TotalOriginal != 6 {
		t.Errorf("TotalOriginal = %d, want 6", cumulative.TotalOriginal)
	}
	if cumulative.TotalOptimized != 2 {
		t.Errorf("TotalOptimized = %d, want 2", cumulative.TotalOptimized)
	}
}

func TestSavingsTrackerServerBreakdown(t *testing.T) {
	tracker := NewSavingsTracker()

	tracker.RecordRequest("server1", "abcdefghijklmnop", "ab")
	tracker.RecordRequest("server2", "ijklmnop", "ij")

	breakdown := tracker.GetServerBreakdown()

	if len(breakdown) != 2 {
		t.Errorf("Server breakdown length = %d, want 2", len(breakdown))
	}

	if ss, ok := breakdown["server1"]; !ok {
		t.Error("server1 not found in breakdown")
	} else if ss.SavedTokens != 3 {
		t.Errorf("server1 SavedTokens = %d, want 3", ss.SavedTokens)
	}

	if ss, ok := breakdown["server2"]; !ok {
		t.Error("server2 not found in breakdown")
	} else if ss.SavedTokens != 1 {
		t.Errorf("server2 SavedTokens = %d, want 1", ss.SavedTokens)
	}
}

func TestSavingsTrackerReset(t *testing.T) {
	tracker := NewSavingsTracker()

	tracker.RecordRequest("server1", "abcdefghijklmnop", "ab")
	tracker.Reset()

	cumulative := tracker.GetCumulativeSavings()
	if cumulative.TotalOriginal != 0 {
		t.Errorf("After Reset: TotalOriginal = %d, want 0", cumulative.TotalOriginal)
	}
	if cumulative.TotalOptimized != 0 {
		t.Errorf("After Reset: TotalOptimized = %d, want 0", cumulative.TotalOptimized)
	}

	breakdown := tracker.GetServerBreakdown()
	if len(breakdown) != 0 {
		t.Errorf("After Reset: breakdown length = %d, want 0", len(breakdown))
	}
}

func TestSavingsTrackerThreadSafety(t *testing.T) {
	tracker := NewSavingsTracker()

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			serverName := "server"
			if id%2 == 0 {
				serverName = "server1"
			} else {
				serverName = "server2"
			}
			tracker.RecordRequest(serverName, "abcdefghijklmnop", "ab")
		}(i)
	}

	wg.Wait()

	cumulative := tracker.GetCumulativeSavings()
	expectedOriginal := int64(numGoroutines * 4)
	if cumulative.TotalOriginal != expectedOriginal {
		t.Errorf("TotalOriginal = %d, want %d (thread safety)", cumulative.TotalOriginal, expectedOriginal)
	}
}

func TestSavingsTrackerSessionDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode - timing dependent test")
	}

	tracker := NewSavingsTracker()

	require.Eventually(t, func() bool {
		cumulative := tracker.GetCumulativeSavings()
		return cumulative.SessionDuration >= 10*time.Millisecond
	}, 100*time.Millisecond, 10*time.Millisecond)
}
