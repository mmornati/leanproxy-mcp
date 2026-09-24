// Package usage is the auditable savings record store (issue #324): an
// append-only, offline, per-day JSONL log of the real counters the proxy
// already records (pkg/metrics.Snapshot: the response governor's
// GovernorStats, the telemetry counters, and the cost tracker's
// breakdown). It never stores a payload, argument or secret — only
// numbers, server/tool names and timestamps — so `leanproxy-mcp report`
// can rebuild an auditable savings report without a live process and
// without re-estimating anything that was not actually measured.
package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

// DefaultRetentionDays is how long usage files are kept before Prune
// removes them, matching the acceptance criteria's default of 90 days.
const DefaultRetentionDays = 90

// dayLayout names one JSONL file per calendar day (UTC), so rotation and
// pruning are just filename comparisons.
const dayLayout = "2006-01-02"

const filePrefix = "usage-"
const fileSuffix = ".jsonl"

// Record is one point-in-time snapshot of the real counters the proxy has
// recorded, appended to the usage store. Estimator names the token-counting
// heuristic every *_tokens field in Snapshot was computed with
// (reporter.DefaultCharsPerToken, "chars/4"): the same primitive the
// runtime cost tracker, the response governor and the benchmark harness
// all use, so runtime accounting and harness numbers are directly
// comparable.
type Record struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	PID       int       `json:"pid"`
	Estimator string    `json:"estimator"`

	Snapshot metrics.MetricsSnapshot `json:"snapshot"`
}

// NewRecord builds a Record from the process-global counters
// (metrics.Snapshot) at the moment it is called. sessionID identifies the
// front-end run (e.g. the status file's PID-derived id); it is never a
// payload or a secret.
func NewRecord(sessionID string) Record {
	return Record{
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		PID:       os.Getpid(),
		Estimator: fmt.Sprintf("chars/%d", int(reporter.DefaultCharsPerToken)),
		Snapshot:  metrics.Snapshot(),
	}
}

// Store is the JSONL usage log: one file per UTC day under its directory,
// mode 0600, append-only.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at ~/.leanproxy/usage (creating it if
// needed), matching HOME the same way pkg/statusfile does: os.UserHomeDir
// first, falling back to the OS user database, so a sandboxed HOME (tests,
// the benchmark harness) never touches the real one.
func NewStore() (*Store, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	return NewStoreWithDir(filepath.Join(home, ".leanproxy", "usage"))
}

// NewStoreWithDir returns a Store rooted at dir (creating it if needed).
// Tests and --usage-dir use this to avoid touching the real home directory.
func NewStoreWithDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("usage: create usage dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Dir returns the store's root directory.
func (s *Store) Dir() string { return s.dir }

// Append writes one record to the day's file (UTC), creating it at mode
// 0600 if needed. Concurrent-safe across processes: each write is a single
// O_APPEND write of one JSON line, which is atomic for lines under the
// platform pipe buffer size on POSIX systems.
func (s *Store) Append(rec Record) error {
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	path := s.pathForDay(rec.Timestamp)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("usage: open %s: %w", path, err)
	}
	defer f.Close()

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("usage: marshal record: %w", err)
	}
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("usage: write %s: %w", path, err)
	}
	return nil
}

func (s *Store) pathForDay(t time.Time) string {
	return filepath.Join(s.dir, filePrefix+t.UTC().Format(dayLayout)+fileSuffix)
}

// Load returns every record with Timestamp >= since (UTC), sorted oldest
// first. A zero since loads every retained record. Malformed lines are
// skipped rather than failing the whole load (one truncated write, e.g.
// from a crash mid-append, should not lose the rest of the day).
func (s *Store) Load(since time.Time) ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("usage: read usage dir: %w", err)
	}

	sinceUTC := since.UTC()
	var records []Record
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		day, ok := parseDayFile(e.Name())
		if !ok {
			continue
		}
		// A whole day is skippable only when it ends before since's day
		// (the file holds no timestamp finer than the day in its name).
		if !sinceUTC.IsZero() && day.Add(24*time.Hour).Before(truncateToDay(sinceUTC)) {
			continue
		}
		recs, err := readRecords(filepath.Join(s.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if sinceUTC.IsZero() || !r.Timestamp.Before(sinceUTC) {
				records = append(records, r)
			}
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Timestamp.Before(records[j].Timestamp) })
	return records, nil
}

func readRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("usage: open %s: %w", path, err)
	}
	defer f.Close()

	records := make([]Record, 0, 16) // a day's file is usually a handful of records
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // skip a malformed/truncated line rather than fail the load
		}
		records = append(records, rec)
	}
	return records, scanner.Err()
}

// Prune removes usage files whose whole day is older than retention
// (default DefaultRetentionDays), relative to now (UTC). It returns the
// number of files removed.
func (s *Store) Prune(retention time.Duration) (int, error) {
	if retention <= 0 {
		retention = DefaultRetentionDays * 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-retention)

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("usage: read usage dir: %w", err)
	}

	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		day, ok := parseDayFile(e.Name())
		if !ok {
			continue
		}
		if day.Add(24 * time.Hour).Before(cutoff) {
			if err := os.Remove(filepath.Join(s.dir, e.Name())); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("usage: prune %s: %w", e.Name(), err)
			}
			removed++
		}
	}
	return removed, nil
}

func parseDayFile(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
		return time.Time{}, false
	}
	dayStr := strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileSuffix)
	day, err := time.Parse(dayLayout, dayStr)
	if err != nil {
		return time.Time{}, false
	}
	return day.UTC(), true
}

func truncateToDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// homeDir mirrors pkg/statusfile's homeDir: os.UserHomeDir (respects a
// sandboxed HOME, e.g. in tests and the benchmark harness) falling back to
// the OS user database.
func homeDir() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home, nil
	}
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("usage: get user home dir: %w", err)
	}
	return usr.HomeDir, nil
}
