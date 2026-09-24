package usage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
)

// windowNow is a Wednesday: the week started on Monday 2026-09-21.
var windowNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

// cumulative builds a record whose counters are a session's cumulative
// totals: schema tokens (native, sent) and one governed tool.
func cumulative(session string, ts time.Time, native, sent int64, tool string, results, orig, ret int64) Record {
	return Record{
		Timestamp: ts,
		SessionID: session,
		Estimator: "chars/4",
		Snapshot: metrics.MetricsSnapshot{
			Telemetry: mcp.TelemetryCounters{SchemaListings: 1, SchemaNativeTokens: native, SchemaSentTokens: sent},
			ResponseGovernor: &mcp.GovernorStats{
				Enabled: true, Results: results, OriginalTokens: orig, ReturnedTokens: ret, SavedTokens: orig - ret,
				ByTool: []mcp.GovernorToolStats{{Tool: tool, Results: results, OriginalTokens: orig, ReturnedTokens: ret}},
			},
		},
	}
}

func TestWeekStart(t *testing.T) {
	cases := map[string]string{
		"2026-09-21T00:00:00Z": "2026-09-21", // Monday
		"2026-09-23T12:00:00Z": "2026-09-21", // Wednesday
		"2026-09-27T23:59:59Z": "2026-09-21", // Sunday
		"2026-09-28T00:00:01Z": "2026-09-28", // next Monday
	}
	for in, want := range cases {
		ts, _ := time.Parse(time.RFC3339, in)
		if got := WeekStart(ts).Format(dayLayout); got != want {
			t.Errorf("WeekStart(%s) = %s, want %s", in, got, want)
		}
	}
	if got := DayStart(windowNow); !got.Equal(time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("DayStart = %s", got)
	}
}

// windowRecords: session "long" has run since last week; "new" started
// today; "old" only ran last week.
func windowRecords() []Record {
	return []Record{
		cumulative("long", time.Date(2026, 9, 20, 23, 59, 55, 0, time.UTC), 1000, 100, "github.search", 1, 400, 400),
		cumulative("long", time.Date(2026, 9, 22, 23, 59, 55, 0, time.UTC), 3000, 300, "github.search", 3, 1400, 900),
		cumulative("long", time.Date(2026, 9, 23, 11, 0, 0, 0, time.UTC), 4000, 400, "github.search", 5, 3400, 1400),
		cumulative("new", time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC), 500, 50, "fs.read_file", 2, 10000, 2000),
		cumulative("old", time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC), 9000, 900, "fs.read_file", 9, 90000, 9000),
	}
}

func TestSummarizeWindowsAreDeltas(t *testing.T) {
	s := Summarize(windowRecords(), windowNow)

	if s.Estimator != "chars/4" {
		t.Errorf("estimator = %q", s.Estimator)
	}
	if !s.Today.Since.Equal(DayStart(windowNow)) || !s.Week.Since.Equal(WeekStart(windowNow)) {
		t.Errorf("since = %s / %s", s.Today.Since, s.Week.Since)
	}

	// Today: long contributes (4000-3000, 400-300) schema and
	// (3400-1400, 1400-900) governor; new contributes all of its own.
	today := s.Today
	if today.Sessions != 2 {
		t.Errorf("today sessions = %d, want 2 (old ran last week only)", today.Sessions)
	}
	wantOriginal := int64((4000 - 3000) + (3400 - 1400) + 500 + 10000)
	wantSaved := int64((1000 - 100) + (2000 - 500) + (500 - 50) + (10000 - 2000))
	if today.OriginalTokens != wantOriginal || today.SavedTokens != wantSaved {
		t.Errorf("today original/saved = %d/%d, want %d/%d", today.OriginalTokens, today.SavedTokens, wantOriginal, wantSaved)
	}
	if today.ToolCalls != 2+2 {
		t.Errorf("today tool calls = %d, want 4", today.ToolCalls)
	}
	if today.TopServer != "fs" || today.TopTool != "fs.read_file" {
		t.Errorf("today top = %q / %q", today.TopServer, today.TopTool)
	}
	gh := today.ServerTools("github")
	if len(gh) != 1 || gh[0].Tool != "search" || gh[0].Calls != 2 || gh[0].OriginalTokens != 2000 || gh[0].ReturnedTokens != 500 || gh[0].SavedTokens != 1500 {
		t.Errorf("today github tools = %+v", gh)
	}

	// Week: long contributes everything after Sunday's last record.
	week := s.Week
	if week.Sessions != 2 {
		t.Errorf("week sessions = %d, want 2", week.Sessions)
	}
	wantWeekOriginal := int64((4000 - 1000) + (3400 - 400) + 500 + 10000)
	if week.OriginalTokens != wantWeekOriginal {
		t.Errorf("week original = %d, want %d", week.OriginalTokens, wantWeekOriginal)
	}
	if week.OriginalTokens <= today.OriginalTokens {
		t.Errorf("week-to-date (%d) should exceed today (%d) here", week.OriginalTokens, today.OriginalTokens)
	}
	if len(week.ByServer) != 2 || week.ByServer[0].Server != "fs" || week.ByServer[1].Server != "github" {
		t.Errorf("week by_server = %+v", week.ByServer)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	s := Summarize(nil, windowNow)
	if s.Today.Sessions != 0 || s.Today.OriginalTokens != 0 || s.Today.TopServer != "" || s.Today.TopTool != "" {
		t.Errorf("today = %+v", s.Today)
	}
	if s.Today.ByServer == nil || s.Today.ByTool == nil {
		t.Error("by_server / by_tool must be empty lists, not null")
	}
}

// A session without the governor still gets schema numbers, and no
// per-tool rows.
func TestSummarizeWithoutGovernor(t *testing.T) {
	rec := Record{Timestamp: windowNow.Add(-time.Hour), SessionID: "s", Snapshot: metrics.MetricsSnapshot{
		Telemetry: mcp.TelemetryCounters{SchemaListings: 1, SchemaNativeTokens: 2000, SchemaSentTokens: 200, DiscoveryCalls: 3, DiscoveryTokens: 90},
	}}
	s := Summarize([]Record{rec}, windowNow)
	if s.Today.SavedTokens != 1800 || s.Today.DiscoveryCalls != 3 || s.Today.DiscoveryTokens != 90 {
		t.Errorf("today = %+v", s.Today)
	}
	if len(s.Today.ByTool) != 0 || s.Today.ToolCalls != 0 {
		t.Errorf("per-tool rows without the governor: %+v", s.Today.ByTool)
	}
}

func newLiveAt(t *testing.T, now *time.Time) (*Store, *Live) {
	t.Helper()
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := NewLive(store)
	l.now = func() time.Time { return *now }
	return store, l
}

// Live, reading the store incrementally, gives exactly Summarize's answer
// over the same records, including records appended between calls.
func TestLiveMatchesSummarize(t *testing.T) {
	now := windowNow
	store, live := newLiveAt(t, &now)
	recs := windowRecords()

	for _, r := range recs[:3] {
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := live.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if want := Summarize(recs[:3], now); !reflect.DeepEqual(got, want) {
		t.Fatalf("first read:\n got %+v\nwant %+v", got, want)
	}

	for _, r := range recs[3:] {
		if err := store.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err = live.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if want := Summarize(recs, now); !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental read:\n got %+v\nwant %+v", got, want)
	}

	// Reading again with nothing new is stable.
	again, err := live.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatal("a read with nothing appended changed the summary")
	}
}

// A line still being written (no trailing newline) is not consumed until
// it is complete.
func TestLiveSkipsPartialLine(t *testing.T) {
	now := windowNow
	store, live := newLiveAt(t, &now)
	rec := cumulative("s", now.Add(-time.Hour), 2000, 200, "fs.read_file", 1, 100, 100)
	if err := store.Append(rec); err != nil {
		t.Fatal(err)
	}
	path := store.pathForDay(now)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"timestamp":"2026-09-23T11:30:00Z","session_id":"s2"`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s, err := live.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if s.Today.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1 (partial line ignored)", s.Today.Sessions)
	}
	if live.offsets[path] == 0 {
		t.Fatal("complete line not consumed")
	}
}

// On a new day the reader starts over, so yesterday's activity moves out
// of "today" but stays in the week.
func TestLiveDayRollover(t *testing.T) {
	now := windowNow
	store, live := newLiveAt(t, &now)
	if err := store.Append(cumulative("s", now.Add(-time.Hour), 2000, 200, "fs.read_file", 1, 100, 100)); err != nil {
		t.Fatal(err)
	}
	s, err := live.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if s.Today.Sessions != 1 {
		t.Fatalf("today sessions = %d, want 1", s.Today.Sessions)
	}

	now = now.Add(24 * time.Hour)
	s, err = live.Summary()
	if err != nil {
		t.Fatal(err)
	}
	if s.Today.Sessions != 0 || s.Week.Sessions != 1 {
		t.Fatalf("after rollover today/week sessions = %d/%d, want 0/1", s.Today.Sessions, s.Week.Sessions)
	}
}

// Files older than the day before the week's start are not read at all.
func TestLiveIgnoresOldFiles(t *testing.T) {
	now := windowNow
	store, live := newLiveAt(t, &now)
	old := cumulative("old", time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC), 9000, 900, "fs.read_file", 9, 90000, 9000)
	if err := store.Append(old); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Summary(); err != nil {
		t.Fatal(err)
	}
	if _, ok := live.offsets[filepath.Join(store.Dir(), "usage-2026-09-10.jsonl")]; ok {
		t.Fatal("a file from before the week was read")
	}
}
