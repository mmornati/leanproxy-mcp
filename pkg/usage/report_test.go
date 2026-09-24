package usage

import (
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
)

func sampleRecord(sessionID string, ts time.Time) Record {
	return Record{
		Timestamp: ts,
		SessionID: sessionID,
		Estimator: "chars/4",
		Snapshot: metrics.MetricsSnapshot{
			ResponseGovernor: &mcp.GovernorStats{
				Enabled:               true,
				Results:               10,
				Truncated:             3,
				OriginalTokens:        10000,
				ReturnedTokens:        4000,
				SavedTokens:           6000,
				Projected:             2,
				ProjectionSavedTokens: 1000,
				DedupHits:             1,
				DedupSavedTokens:      500,
				Summarized:            1,
				SummarizeSavedTokens:  500,
				SummarizeFallbacks:    1,
				ByTool: []mcp.GovernorToolStats{
					{Tool: "github.get_file_contents", Results: 4, OriginalTokens: 8000},
					{Tool: "github.search_code", Results: 6, OriginalTokens: 2000},
				},
			},
			Telemetry: mcp.TelemetryCounters{
				SchemaListings:     1,
				SchemaNativeTokens: 5000,
				SchemaSentTokens:   500,
				DiscoveryCalls:     2,
				DiscoveryTokens:    300,
			},
		},
	}
}

func TestBuildSavingsReportSingleSession(t *testing.T) {
	now := time.Now()
	rep := BuildSavingsReport([]Record{sampleRecord("s1", now)}, time.Time{}, nil)

	if rep.SessionCount != 1 {
		t.Fatalf("SessionCount = %d, want 1", rep.SessionCount)
	}
	if rep.Estimator != "chars/4" {
		t.Fatalf("Estimator = %q, want chars/4", rep.Estimator)
	}

	// schema saved = 5000 - 500 = 4500
	// governor saved = 6000 (SavedTokens already the total)
	// total = 4500 + 6000 = 10500
	if rep.TotalSavedTokens != 10500 {
		t.Fatalf("TotalSavedTokens = %d, want 10500", rep.TotalSavedTokens)
	}

	var sum int64
	var truncation, projection, dedup, summarization MechanismSavings
	for _, m := range rep.Mechanisms {
		if m.IsCost {
			continue
		}
		sum += m.SavedTokens
		switch m.Mechanism {
		case "response_truncation":
			truncation = m
		case "response_projection":
			projection = m
		case "response_dedup":
			dedup = m
		case "response_summarization":
			summarization = m
		}
	}
	if sum != rep.TotalSavedTokens {
		t.Fatalf("sum of mechanism SavedTokens = %d, want %d (total)", sum, rep.TotalSavedTokens)
	}
	if projection.SavedTokens != 1000 {
		t.Fatalf("projection saved = %d, want 1000", projection.SavedTokens)
	}
	if dedup.SavedTokens != 500 {
		t.Fatalf("dedup saved = %d, want 500", dedup.SavedTokens)
	}
	if summarization.SavedTokens != 500 {
		t.Fatalf("summarization saved = %d, want 500", summarization.SavedTokens)
	}
	// truncation = total governor saved (6000) - projection(1000) - dedup(500) - summarization(500) = 4000
	if truncation.SavedTokens != 4000 {
		t.Fatalf("truncation saved = %d, want 4000", truncation.SavedTokens)
	}

	if rep.ExtraTurnsEstimate != 2 {
		t.Fatalf("ExtraTurnsEstimate = %d, want 2", rep.ExtraTurnsEstimate)
	}

	if len(rep.TopToolsByResponseSize) != 2 || rep.TopToolsByResponseSize[0].Tool != "github.get_file_contents" {
		t.Fatalf("TopToolsByResponseSize = %+v, want get_file_contents first", rep.TopToolsByResponseSize)
	}
}

func TestBuildSavingsReportMultipleSessionsAggregate(t *testing.T) {
	now := time.Now()
	records := []Record{
		sampleRecord("s1", now),
		sampleRecord("s2", now.Add(time.Minute)),
	}
	rep := BuildSavingsReport(records, time.Time{}, nil)
	if rep.SessionCount != 2 {
		t.Fatalf("SessionCount = %d, want 2", rep.SessionCount)
	}
	// Each session contributes 10500; two sessions -> 21000.
	if rep.TotalSavedTokens != 21000 {
		t.Fatalf("TotalSavedTokens = %d, want 21000", rep.TotalSavedTokens)
	}
}

func TestBuildSavingsReportLatestPerSessionOnly(t *testing.T) {
	now := time.Now()
	early := sampleRecord("s1", now)
	// A later snapshot of the SAME session with higher cumulative counters
	// (as a real process would produce); only this one should count.
	late := sampleRecord("s1", now.Add(time.Hour))
	late.Snapshot.ResponseGovernor.SavedTokens = 60000
	late.Snapshot.ResponseGovernor.OriginalTokens = 100000
	late.Snapshot.ResponseGovernor.ReturnedTokens = 40000
	late.Snapshot.ResponseGovernor.ProjectionSavedTokens = 1000
	late.Snapshot.ResponseGovernor.DedupSavedTokens = 500
	late.Snapshot.ResponseGovernor.SummarizeSavedTokens = 500

	rep := BuildSavingsReport([]Record{early, late}, time.Time{}, nil)
	if rep.SessionCount != 1 {
		t.Fatalf("SessionCount = %d, want 1 (dedup by session)", rep.SessionCount)
	}
	// Governor saved should reflect only `late` (60000), not early+late.
	var govSaved int64
	for _, m := range rep.Mechanisms {
		if m.Mechanism == "response_truncation" || m.Mechanism == "response_projection" ||
			m.Mechanism == "response_dedup" || m.Mechanism == "response_summarization" {
			govSaved += m.SavedTokens
		}
	}
	if govSaved != 60000 {
		t.Fatalf("governor saved = %d, want 60000 (from latest snapshot only)", govSaved)
	}
}

func TestBuildSavingsReportPricing(t *testing.T) {
	now := time.Now()
	price := 3.0
	rep := BuildSavingsReport([]Record{sampleRecord("s1", now)}, time.Time{}, &price)
	if rep.EstimatedCostSaved == nil {
		t.Fatal("EstimatedCostSaved is nil, want a value when --price-per-mtok is set")
	}
	want := float64(rep.TotalSavedTokens) / 1_000_000 * price
	if *rep.EstimatedCostSaved != want {
		t.Fatalf("EstimatedCostSaved = %v, want %v", *rep.EstimatedCostSaved, want)
	}
}

func TestBuildSavingsReportNoPricingByDefault(t *testing.T) {
	rep := BuildSavingsReport([]Record{sampleRecord("s1", time.Now())}, time.Time{}, nil)
	if rep.PricePerMTok != nil || rep.EstimatedCostSaved != nil {
		t.Fatal("pricing fields set without --price-per-mtok")
	}
}

func TestBuildSavingsReportEmpty(t *testing.T) {
	rep := BuildSavingsReport(nil, time.Time{}, nil)
	if rep.SessionCount != 0 || rep.TotalSavedTokens != 0 {
		t.Fatalf("empty report should be all zero, got %+v", rep)
	}
}

// TestSavingsReportOutputsLabelMeasuredVsEstimated is an offline/privacy
// check for the acceptance criteria "Measured and estimated labels are
// present in every output format", and that no payload/argument content
// ever appears in an output (only names, numbers and timestamps).
func TestSavingsReportOutputsLabelMeasuredVsEstimated(t *testing.T) {
	rep := BuildSavingsReport([]Record{sampleRecord("s1", time.Now())}, time.Time{}, nil)

	text := rep.Text("tool")
	md := rep.Markdown("tool")
	jsonBytes, err := rep.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	jsonStr := string(jsonBytes)

	for _, out := range []string{text, md, jsonStr} {
		if !strings.Contains(out, "measured") {
			t.Errorf("output missing 'measured' label:\n%s", out)
		}
	}
	if !strings.Contains(text, "estimated") && !strings.Contains(text, "extra_turns") && !strings.Contains(strings.ToLower(text), "estimated") {
		t.Errorf("text output missing an 'estimated' label:\n%s", text)
	}

	// Never a payload: the report only ever carries tool/server identifiers
	// and numbers, so a canary substring standing in for a secret/argument
	// value must never appear.
	canary := "sk-ThisIsNotARealSecretCanaryValue123"
	for _, out := range []string{text, md, jsonStr} {
		if strings.Contains(out, canary) {
			t.Fatalf("output leaked a payload-shaped value: %s", canary)
		}
	}
}

func TestSavingsReportCSVHasTotalRow(t *testing.T) {
	rep := BuildSavingsReport([]Record{sampleRecord("s1", time.Now())}, time.Time{}, nil)
	var b strings.Builder
	if err := rep.CSV(&b); err != nil {
		t.Fatalf("CSV: %v", err)
	}
	if !strings.Contains(b.String(), "TOTAL") {
		t.Fatalf("CSV output missing TOTAL row:\n%s", b.String())
	}
}
