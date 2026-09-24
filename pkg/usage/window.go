// This file turns the usage store into the today / week-to-date windows the
// dashboard and /metrics serve (metrics.UsageSummary). Each Record is a
// session's cumulative total, so a window is, per session, the session's
// latest record minus its last record before the window started: a proxy
// that has been running since yesterday only contributes today what it
// recorded today. The totals are then built with BuildSavingsReport, so
// they are exactly `report`'s numbers for the same deltas.
package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

// DayStart returns 00:00 UTC of t's day: the start of the "today" window,
// and the same day boundary the store's per-day files use.
func DayStart(t time.Time) time.Time { return truncateToDay(t) }

// WeekStart returns 00:00 UTC of the Monday of t's ISO week: the start of
// the week-to-date window.
func WeekStart(t time.Time) time.Time {
	day := truncateToDay(t)
	offset := (int(day.Weekday()) + 6) % 7 // Monday = 0 ... Sunday = 6
	return day.AddDate(0, 0, -offset)
}

// Summarize computes the today / week-to-date windows at now from records
// (in any order). Records before the week's start are only used as
// baselines.
func Summarize(records []Record, now time.Time) metrics.UsageSummary {
	day, week := DayStart(now), WeekStart(now)
	sessions := map[string]*sessionView{}
	for _, r := range records {
		v, ok := sessions[r.SessionID]
		if !ok {
			v = &sessionView{}
			sessions[r.SessionID] = v
		}
		rec := r
		v.observe(r.Timestamp, day, week, func() (Record, error) { return rec, nil })
	}
	return buildSummary(sessions, day, week)
}

// sessionView keeps, for one session, the three records a window needs:
// its latest record and its last record before each window's start. They
// are loaded lazily (load), so a reader that sees thousands of records per
// session only decodes the few it keeps.
type sessionView struct {
	latest, beforeDay, beforeWeek *lazyRecord
}

type lazyRecord struct {
	ts   time.Time
	load func() (Record, error)
}

func (v *sessionView) observe(ts, day, week time.Time, load func() (Record, error)) {
	lr := &lazyRecord{ts: ts, load: load}
	if v.latest == nil || ts.After(v.latest.ts) {
		v.latest = lr
	}
	if ts.Before(day) && (v.beforeDay == nil || ts.After(v.beforeDay.ts)) {
		v.beforeDay = lr
	}
	if ts.Before(week) && (v.beforeWeek == nil || ts.After(v.beforeWeek.ts)) {
		v.beforeWeek = lr
	}
}

func buildSummary(sessions map[string]*sessionView, day, week time.Time) metrics.UsageSummary {
	return metrics.UsageSummary{
		Estimator: fmt.Sprintf("chars/%d", int(reporter.DefaultCharsPerToken)),
		Today:     buildWindow(sessions, day, func(v *sessionView) *lazyRecord { return v.beforeDay }),
		Week:      buildWindow(sessions, week, func(v *sessionView) *lazyRecord { return v.beforeWeek }),
	}
}

func buildWindow(sessions map[string]*sessionView, since time.Time, baseline func(*sessionView) *lazyRecord) metrics.UsageWindow {
	deltas := make([]Record, 0, len(sessions))
	for id, v := range sessions {
		if v.latest == nil || v.latest.ts.Before(since) {
			continue // nothing recorded in this window
		}
		latest, err := v.latest.load()
		if err != nil {
			continue
		}
		var base metrics.MetricsSnapshot
		if b := baseline(v); b != nil {
			if rec, err := b.load(); err == nil {
				base = rec.Snapshot
			}
		}
		deltas = append(deltas, Record{
			Timestamp: latest.Timestamp,
			SessionID: id,
			Snapshot:  diffSnapshot(latest.Snapshot, base),
		})
	}

	rep := BuildSavingsReport(deltas, since, nil)
	w := metrics.UsageWindow{
		Since:          since,
		Sessions:       rep.SessionCount,
		OriginalTokens: rep.TotalOriginalTokens,
		SavedTokens:    rep.TotalSavedTokens,
		SavedPercent:   rep.TotalSavedPercent,
	}
	for _, m := range rep.Mechanisms {
		if m.Mechanism == "discovery" {
			w.DiscoveryCalls, w.DiscoveryTokens = m.Calls, m.ResultingTokens
		}
	}

	byTool := map[string]*metrics.UsageTool{}
	for _, d := range deltas {
		gov := d.Snapshot.ResponseGovernor
		if gov == nil {
			continue
		}
		w.ToolCalls += gov.Results
		for _, t := range gov.ByTool {
			e, ok := byTool[t.Tool]
			if !ok {
				server, tool := splitIdentity(t.Tool)
				e = &metrics.UsageTool{Server: server, Tool: tool}
				byTool[t.Tool] = e
			}
			e.Calls += t.Results
			e.OriginalTokens += t.OriginalTokens
			e.ReturnedTokens += t.ReturnedTokens
			e.SavedTokens += t.OriginalTokens - t.ReturnedTokens
		}
	}
	tools := make([]metrics.UsageTool, 0, len(byTool))
	for _, t := range byTool {
		tools = append(tools, *t)
	}
	w.SetTools(tools)
	return w
}

// splitIdentity splits the governor's "server.tool" identity
// (pkg/mcp.cacheIdentity; server names never contain a dot). An identity
// without a dot has no server.
func splitIdentity(identity string) (server, tool string) {
	if i := strings.Index(identity, "."); i > 0 {
		return identity[:i], identity[i+1:]
	}
	return "", identity
}

// diffSnapshot returns the counters BuildSavingsReport and buildWindow read
// (schema, discovery, the response governor's totals and per-tool rows),
// accumulated between base and latest. Counters never go down within a
// session; a negative difference (which would mean a corrupt record) is
// clamped to zero.
func diffSnapshot(latest, base metrics.MetricsSnapshot) metrics.MetricsSnapshot {
	lt, bt := latest.Telemetry, base.Telemetry
	out := metrics.MetricsSnapshot{Telemetry: mcp.TelemetryCounters{
		SchemaListings:     sub(lt.SchemaListings, bt.SchemaListings),
		SchemaNativeTokens: sub(lt.SchemaNativeTokens, bt.SchemaNativeTokens),
		SchemaSentTokens:   sub(lt.SchemaSentTokens, bt.SchemaSentTokens),
		DiscoveryCalls:     sub(lt.DiscoveryCalls, bt.DiscoveryCalls),
		DiscoveryTokens:    sub(lt.DiscoveryTokens, bt.DiscoveryTokens),
	}}
	if latest.ResponseGovernor == nil {
		return out
	}
	lg := *latest.ResponseGovernor
	var bg mcp.GovernorStats
	if base.ResponseGovernor != nil {
		bg = *base.ResponseGovernor
	}
	baseTools := make(map[string]mcp.GovernorToolStats, len(bg.ByTool))
	for _, t := range bg.ByTool {
		baseTools[t.Tool] = t
	}
	g := &mcp.GovernorStats{
		Enabled:               lg.Enabled,
		MaxTokens:             lg.MaxTokens,
		Results:               sub(lg.Results, bg.Results),
		Truncated:             sub(lg.Truncated, bg.Truncated),
		OriginalTokens:        sub(lg.OriginalTokens, bg.OriginalTokens),
		ReturnedTokens:        sub(lg.ReturnedTokens, bg.ReturnedTokens),
		Projected:             sub(lg.Projected, bg.Projected),
		ProjectionSavedTokens: sub(lg.ProjectionSavedTokens, bg.ProjectionSavedTokens),
		DedupHits:             sub(lg.DedupHits, bg.DedupHits),
		DedupSavedTokens:      sub(lg.DedupSavedTokens, bg.DedupSavedTokens),
		Summarized:            sub(lg.Summarized, bg.Summarized),
		SummarizeSavedTokens:  sub(lg.SummarizeSavedTokens, bg.SummarizeSavedTokens),
		SummarizeFallbacks:    sub(lg.SummarizeFallbacks, bg.SummarizeFallbacks),
	}
	g.SavedTokens = sub(g.OriginalTokens, g.ReturnedTokens)
	for _, t := range lg.ByTool {
		b := baseTools[t.Tool]
		d := mcp.GovernorToolStats{
			Tool:           t.Tool,
			Results:        sub(t.Results, b.Results),
			Truncated:      sub(t.Truncated, b.Truncated),
			OriginalTokens: sub(t.OriginalTokens, b.OriginalTokens),
			ReturnedTokens: sub(t.ReturnedTokens, b.ReturnedTokens),
		}
		if d.Results > 0 || d.OriginalTokens > 0 {
			g.ByTool = append(g.ByTool, d)
		}
	}
	out.ResponseGovernor = g
	return out
}

func sub(a, b int64) int64 {
	if a < b {
		return 0
	}
	return a - b
}

// Live is an incrementally updated reader of a Store for Summary: it reads
// only the lines appended since its previous call (every front end appends
// a record every 5 seconds, so re-reading a week of files on each dashboard
// refresh would not scale) and decodes in full only the few records per
// session a window needs. It reads the files from the day before the
// week's start onwards, so a session running across the week boundary has
// its baseline; on a new day it starts over. Safe for concurrent use.
type Live struct {
	store *Store
	now   func() time.Time

	mu       sync.Mutex
	day      time.Time
	offsets  map[string]int64
	sessions map[string]*sessionView
}

// NewLive returns a Live reader over store.
func NewLive(store *Store) *Live {
	return &Live{store: store, now: time.Now}
}

// Summary reads what was appended to the store since the previous call and
// returns the windows at the current time.
func (l *Live) Summary() (metrics.UsageSummary, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now().UTC()
	day, week := DayStart(now), WeekStart(now)
	if !day.Equal(l.day) {
		l.day = day
		l.offsets = map[string]int64{}
		l.sessions = map[string]*sessionView{}
	}

	entries, err := os.ReadDir(l.store.dir)
	if err != nil && !os.IsNotExist(err) {
		return metrics.UsageSummary{}, fmt.Errorf("usage: read usage dir: %w", err)
	}
	firstFile := week.AddDate(0, 0, -1)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fileDay, ok := parseDayFile(e.Name())
		if !ok || fileDay.Before(firstFile) {
			continue
		}
		if err := l.tail(filepath.Join(l.store.dir, e.Name()), day, week); err != nil {
			return metrics.UsageSummary{}, err
		}
	}
	return buildSummary(l.sessions, day, week), nil
}

// recordKey is the part of a Record decoded for every line.
type recordKey struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
}

// tail reads path's complete lines from the last offset on. A trailing
// partial line (a write in progress) is left for the next call. Observing a
// line twice is harmless (views keep the latest timestamp), so a file that
// shrank is simply re-read from the start.
func (l *Live) tail(path string, day, week time.Time) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("usage: open %s: %w", path, err)
	}
	defer f.Close()

	offset := l.offsets[path]
	if fi, err := f.Stat(); err == nil && fi.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("usage: seek %s: %w", path, err)
	}

	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break // partial (or no) line: read it next time
		}
		if err != nil {
			return fmt.Errorf("usage: read %s: %w", path, err)
		}
		offset += int64(len(line))

		var key recordKey
		if json.Unmarshal(line, &key) != nil || (key.SessionID == "" && key.Timestamp.IsZero()) {
			continue // malformed line, as in Store.Load
		}
		v, ok := l.sessions[key.SessionID]
		if !ok {
			v = &sessionView{}
			l.sessions[key.SessionID] = v
		}
		v.observe(key.Timestamp, day, week, decodeOnce(line))
	}
	l.offsets[path] = offset
	return nil
}

// decodeOnce returns a loader that decodes line into a Record the first
// time it is called and returns the same result afterwards.
func decodeOnce(line []byte) func() (Record, error) {
	var (
		once sync.Once
		rec  Record
		err  error
	)
	return func() (Record, error) {
		once.Do(func() {
			err = json.Unmarshal(line, &rec)
			line = nil
		})
		return rec, err
	}
}
