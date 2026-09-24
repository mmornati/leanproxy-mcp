package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
)

func TestStoreAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}

	now := time.Now().UTC()
	rec1 := Record{
		Timestamp: now.Add(-2 * time.Hour),
		SessionID: "s1",
		Estimator: "chars/4",
		Snapshot:  metrics.MetricsSnapshot{TotalSpend: 100},
	}
	rec2 := Record{
		Timestamp: now,
		SessionID: "s2",
		Estimator: "chars/4",
		Snapshot:  metrics.MetricsSnapshot{TotalSpend: 200},
	}
	if err := s.Append(rec1); err != nil {
		t.Fatalf("Append rec1: %v", err)
	}
	if err := s.Append(rec2); err != nil {
		t.Fatalf("Append rec2: %v", err)
	}

	all, err := s.Load(time.Time{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("Load: got %d records, want 2", len(all))
	}
	if all[0].SessionID != "s1" || all[1].SessionID != "s2" {
		t.Fatalf("Load: not sorted oldest first: %+v", all)
	}

	recent, err := s.Load(now.Add(-1 * time.Minute))
	if err != nil {
		t.Fatalf("Load since: %v", err)
	}
	if len(recent) != 1 || recent[0].SessionID != "s2" {
		t.Fatalf("Load since: got %+v, want only s2", recent)
	}
}

func TestStoreFilePermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	if err := s.Append(NewRecord("sess")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 usage file, got %d", len(entries))
	}
	info, err := os.Stat(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("usage file mode = %o, want 0600", perm)
	}
}

func TestStorePrune(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}

	old := time.Now().UTC().Add(-120 * 24 * time.Hour)
	recent := time.Now().UTC()
	if err := s.Append(Record{Timestamp: old, SessionID: "old"}); err != nil {
		t.Fatalf("Append old: %v", err)
	}
	if err := s.Append(Record{Timestamp: recent, SessionID: "recent"}); err != nil {
		t.Fatalf("Append recent: %v", err)
	}

	removed, err := s.Prune(DefaultRetentionDays * 24 * time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 1 {
		t.Fatalf("Prune: removed %d files, want 1", removed)
	}

	records, err := s.Load(time.Time{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 1 || records[0].SessionID != "recent" {
		t.Fatalf("Load after prune: got %+v, want only recent", records)
	}
}

func TestStoreLoadEmptyDir(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStoreWithDir(filepath.Join(dir, "usage"))
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	records, err := s.Load(time.Time{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("Load on empty store: got %d records, want 0", len(records))
	}
}

func TestStoreLoadSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	if err := s.Append(NewRecord("good")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	path := filepath.Join(dir, entries[0].Name())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("{not valid json\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	records, err := s.Load(time.Time{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("Load with malformed line: got %d records, want 1", len(records))
	}
}
