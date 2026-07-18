package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSemanticStatsSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats", "semantic-stats.json")

	sc := NewSemanticCache(nil, nil, time.Hour, WithStatsPersistPath(path))
	sc.Set(t.Context(), "p", []byte(`{"r":1}`), "tool", nil)
	sc.Get(t.Context(), "p", "tool", nil)

	sc.persistStats()

	snap, err := LoadSemanticStatsSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSemanticStatsSnapshot failed: %v", err)
	}
	if snap.Version != semanticStatsVersion {
		t.Errorf("Version = %d, want %d", snap.Version, semanticStatsVersion)
	}
	if snap.Stats.TotalRequests != 1 {
		t.Errorf("TotalRequests = %d, want 1", snap.Stats.TotalRequests)
	}
	if snap.Stats.ExactHits != 1 {
		t.Errorf("ExactHits = %d, want 1", snap.Stats.ExactHits)
	}
	if snap.UpdatedAt.IsZero() {
		t.Error("UpdatedAt must be set")
	}
}

func TestLoadSemanticStatsSnapshot_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	if _, err := LoadSemanticStatsSnapshot(path); err == nil {
		t.Error("expected error for missing stats file")
	}
}

func TestSemanticCache_StopPersistsFinalSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "semantic-stats.json")

	sc := NewSemanticCache(nil, nil, time.Hour,
		WithStatsPersistPath(path),
		WithStatsPersistInterval(time.Hour), // ensure only Stop writes
	)
	sc.Start(t.Context())
	sc.Set(t.Context(), "p", []byte(`{"r":1}`), "tool", nil)
	sc.Get(t.Context(), "p", "tool", nil)
	sc.Stop()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stop() must write a final stats snapshot: %v", err)
	}
	snap, err := LoadSemanticStatsSnapshot(path)
	if err != nil {
		t.Fatalf("load after Stop failed: %v", err)
	}
	if snap.Stats.TotalRequests != 1 {
		t.Errorf("TotalRequests = %d, want 1", snap.Stats.TotalRequests)
	}
}
