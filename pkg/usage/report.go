// This file (issue #324) rebuilds an auditable savings breakdown purely
// from counters the proxy actually recorded: the response governor's
// GovernorStats (#319-#321), the schema/discovery counters (#324) and
// telemetry's plain counters (#317), all read from this package's JSONL
// store (Store.Load). Every number is either measured (an exact count or a
// size the pipeline computed from a real payload) or explicitly labelled
// estimated, and the estimator is always named: the whole proxy, the
// response governor and the benchmark harness all use the same chars/4
// heuristic (reporter.DefaultCharsPerToken), so runtime accounting and
// harness numbers are directly comparable. Nothing here invents or
// extrapolates a number that was not measured.
package usage

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

// MechanismSavings is one row of the report's breakdown by mechanism: a
// single savings (or cost) category, always labelled measured or
// estimated.
type MechanismSavings struct {
	Mechanism string `json:"mechanism"`
	// Measured is true when every field below comes directly from a
	// counter the pipeline incremented from a real payload; it is false
	// only for derived rows that combine measured counters with a stated
	// assumption (currently none do — kept for forward compatibility with
	// e.g. a future price-derived row).
	Measured bool `json:"measured"`
	// IsCost marks a row that is a token cost, not a saving (discovery):
	// it is never added into TotalSavedTokens.
	IsCost bool `json:"is_cost,omitempty"`
	// Estimator names the token-counting heuristic (empty when the row
	// carries no *_tokens field, e.g. a pure call count).
	Estimator       string `json:"estimator,omitempty"`
	OriginalTokens  int64  `json:"original_tokens"`
	ResultingTokens int64  `json:"resulting_tokens"`
	SavedTokens     int64  `json:"saved_tokens"`
	Calls           int64  `json:"calls"`
	Notes           string `json:"notes,omitempty"`
}

// SavedPercent returns the row's saving as a percentage of OriginalTokens
// (0 when OriginalTokens is 0).
func (m MechanismSavings) SavedPercent() float64 {
	if m.OriginalTokens <= 0 {
		return 0
	}
	return float64(m.SavedTokens) / float64(m.OriginalTokens) * 100
}

// ToolSize is one row of "top tools by response size" — a hint for writing
// projection rules (#320).
type ToolSize struct {
	Tool           string `json:"tool"`
	OriginalTokens int64  `json:"original_tokens"`
	Calls          int64  `json:"calls"`
}

// SessionSummary is one contributing session's totals (--by session).
type SessionSummary struct {
	SessionID           string    `json:"session_id"`
	Timestamp           time.Time `json:"timestamp"`
	TotalOriginalTokens int64     `json:"total_original_tokens"`
	TotalSavedTokens    int64     `json:"total_saved_tokens"`
}

// SavingsReport is the full auditable savings report (issue #324).
type SavingsReport struct {
	GeneratedAt time.Time `json:"generated_at"`
	// Since is the --since cutoff used to select usage records (zero: no
	// cutoff, every retained record).
	Since time.Time `json:"since,omitempty"`
	// SessionCount is the number of distinct front-end processes
	// (session ids) whose latest snapshot contributed to this report.
	SessionCount int `json:"session_count"`
	// Estimator names the single token-counting heuristic used
	// everywhere in this report (see DefaultCharsPerToken): the runtime
	// pipeline, the response governor and tests/bench all use it, so this
	// report's numbers and the harness's are directly comparable.
	Estimator string `json:"estimator"`

	Mechanisms []MechanismSavings `json:"mechanisms"`

	TotalOriginalTokens int64   `json:"total_original_tokens"`
	TotalSavedTokens    int64   `json:"total_saved_tokens"`
	TotalSavedPercent   float64 `json:"total_saved_percent"`

	// ExtraTurnsEstimate is the number of discovery calls (search_tools,
	// list_tools, list_servers): each implies roughly one extra LLM turn.
	// The call count itself is measured; converting it into a number of
	// "extra turns" is the estimate, and it is never folded into a token
	// number (per the issue: "do not convert it into tokens silently").
	ExtraTurnsEstimate int64 `json:"extra_turns_estimate"`

	TopToolsByResponseSize []ToolSize `json:"top_tools_by_response_size,omitempty"`
	// TopServersByResponseSize is the same breakdown grouped by upstream
	// server (the part of the tool identity before the first '.'), for
	// --by server.
	TopServersByResponseSize []ToolSize `json:"top_servers_by_response_size,omitempty"`

	// PricePerMTok and EstimatedCostSaved are only present when --price-per-mtok
	// was given: there is no built-in price table (see the issue), so this
	// is always the caller's own number, clearly derived (never presented
	// as a "measured" cost).
	PricePerMTok       *float64 `json:"price_per_mtok,omitempty"`
	EstimatedCostSaved *float64 `json:"estimated_cost_saved,omitempty"`

	// Methodology is a short, human-readable statement of how the numbers
	// above were produced, repeated in every output format so a report
	// read outside its original context is still self-explanatory. See
	// docs/savings-report.md for the full methodology.
	Methodology string `json:"methodology"`

	// PerSession is one row per contributing session (--by session), each
	// a total across every mechanism, from that session's own latest
	// snapshot only.
	PerSession []SessionSummary `json:"per_session,omitempty"`
}

const savingsMethodology = "Every *_tokens number is measured from a real payload the proxy handled " +
	"(the marshaled tools/list response, a discovery-tool result, or a response-governor result), " +
	"counted with the chars/4 heuristic (reporter.DefaultCharsPerToken) -- the same estimator the " +
	"response governor and the benchmark harness (tests/bench) use, so these numbers and " +
	"`make harness` output are directly comparable. Schema savings compare the router's compacted " +
	"tools/list against the same real passthrough tool set (zero in passthrough/hybrid exposure mode, " +
	"where no compaction happens). Discovery is a token cost, not a saving. extra_turns_estimate is the " +
	"only estimated (non-measured) figure in this report: it reads the measured discovery-call count as " +
	"roughly one extra LLM turn per call. See docs/savings-report.md."

// BuildSavingsReport aggregates records (already filtered to --since by the
// caller) into a SavingsReport. Records are grouped by SessionID and only
// the latest (by Timestamp) record of each session is used, since each
// Record.Snapshot is a cumulative total for its own process -- summing
// every snapshot of the same session would double count.
func BuildSavingsReport(records []Record, since time.Time, pricePerMTok *float64) SavingsReport {
	latest := latestPerSession(records)

	var agg metrics.MetricsSnapshot
	var govOriginal, govReturned int64
	toolTotals := map[string]*ToolSize{}
	perSession := make([]SessionSummary, 0, len(latest))
	for _, rec := range latest {
		s := rec.Snapshot
		agg.Telemetry.SchemaListings += s.Telemetry.SchemaListings
		agg.Telemetry.SchemaNativeTokens += s.Telemetry.SchemaNativeTokens
		agg.Telemetry.SchemaSentTokens += s.Telemetry.SchemaSentTokens
		agg.Telemetry.DiscoveryCalls += s.Telemetry.DiscoveryCalls
		agg.Telemetry.DiscoveryTokens += s.Telemetry.DiscoveryTokens

		sessionSchemaSaved := s.Telemetry.SchemaNativeTokens - s.Telemetry.SchemaSentTokens
		sessionSummary := SessionSummary{
			SessionID:           rec.SessionID,
			Timestamp:           rec.Timestamp,
			TotalOriginalTokens: s.Telemetry.SchemaNativeTokens,
			TotalSavedTokens:    sessionSchemaSaved,
		}

		if gov := s.ResponseGovernor; gov != nil {
			for _, t := range gov.ByTool {
				e, ok := toolTotals[t.Tool]
				if !ok {
					e = &ToolSize{Tool: t.Tool}
					toolTotals[t.Tool] = e
				}
				e.OriginalTokens += t.OriginalTokens
				e.Calls += t.Results
			}
			agg.Telemetry.GovernorTruncations += gov.Truncated
			agg.Telemetry.GovernedResults += gov.Results
			agg.Telemetry.GovernorTokensSaved += gov.SavedTokens
			agg.Telemetry.GovernorProjections += gov.Projected
			agg.Telemetry.GovernorProjectionTokensSaved += gov.ProjectionSavedTokens
			agg.Telemetry.GovernorDedupHits += gov.DedupHits
			agg.Telemetry.GovernorDedupTokensSaved += gov.DedupSavedTokens
			agg.Telemetry.GovernorSummarizations += gov.Summarized
			agg.Telemetry.GovernorSummarizationTokensSaved += gov.SummarizeSavedTokens
			agg.Telemetry.GovernorSummarizationFallbacks += gov.SummarizeFallbacks
			govOriginal += gov.OriginalTokens
			govReturned += gov.ReturnedTokens
			sessionSummary.TotalOriginalTokens += gov.OriginalTokens
			sessionSummary.TotalSavedTokens += gov.SavedTokens
		}
		perSession = append(perSession, sessionSummary)
	}
	sort.Slice(perSession, func(i, j int) bool { return perSession[i].SessionID < perSession[j].SessionID })

	rep := SavingsReport{
		GeneratedAt:  time.Now().UTC(),
		Since:        since,
		SessionCount: len(latest),
		PerSession:   perSession,
		Estimator:    fmt.Sprintf("chars/%d", int(reporter.DefaultCharsPerToken)),
		Methodology:  savingsMethodology,
	}

	t := agg.Telemetry
	schemaSaved := t.SchemaNativeTokens - t.SchemaSentTokens
	rep.Mechanisms = append(rep.Mechanisms, MechanismSavings{
		Mechanism:       "schema",
		Measured:        true,
		Estimator:       rep.Estimator,
		OriginalTokens:  t.SchemaNativeTokens,
		ResultingTokens: t.SchemaSentTokens,
		SavedTokens:     schemaSaved,
		Calls:           t.SchemaListings,
		Notes:           "router's compacted tools/list vs. the same real passthrough tool set; zero in passthrough/hybrid mode",
	})

	// Response governor: truncation's own contribution is the remainder
	// after the sub-mechanisms (projection/dedup/summarization) are taken
	// out, so the four governor rows plus schema sum to TotalSavedTokens.
	truncationSaved := t.GovernorTokensSaved - t.GovernorProjectionTokensSaved - t.GovernorDedupTokensSaved - t.GovernorSummarizationTokensSaved
	rep.Mechanisms = append(rep.Mechanisms,
		MechanismSavings{
			Mechanism:       "response_truncation",
			Measured:        true,
			Estimator:       rep.Estimator,
			OriginalTokens:  govOriginal,
			ResultingTokens: govReturned,
			SavedTokens:     truncationSaved,
			Calls:           t.GovernorTruncations,
			Notes:           "structural truncation + spill-to-read_result (#319); the governor's residual saving after projection/dedup/summarization",
		},
		MechanismSavings{
			Mechanism:   "response_projection",
			Measured:    true,
			Estimator:   rep.Estimator,
			SavedTokens: t.GovernorProjectionTokensSaved,
			Calls:       t.GovernorProjections,
			Notes:       "field projection (#320): removed fields, before truncation; before/after size of only the projected part is not retained separately, so only the saved delta is shown",
		},
		MechanismSavings{
			Mechanism:   "response_dedup",
			Measured:    true,
			Estimator:   rep.Estimator,
			SavedTokens: t.GovernorDedupTokensSaved,
			Calls:       t.GovernorDedupHits,
			Notes:       "in-session dedup (#321): identical results replaced by a short stub",
		},
		MechanismSavings{
			Mechanism:   "response_summarization",
			Measured:    true,
			Estimator:   rep.Estimator,
			SavedTokens: t.GovernorSummarizationTokensSaved,
			Calls:       t.GovernorSummarizations,
			Notes:       fmt.Sprintf("local-LLM summarization (#321); %d attempt(s) fell back to truncation", t.GovernorSummarizationFallbacks),
		},
		MechanismSavings{
			Mechanism:       "discovery",
			Measured:        true,
			IsCost:          true,
			Estimator:       rep.Estimator,
			ResultingTokens: t.DiscoveryTokens,
			SavedTokens:     -t.DiscoveryTokens,
			Calls:           t.DiscoveryCalls,
			Notes:           "search_tools/list_tools/list_servers result size: a token cost, not a saving; not counted in the total",
		},
	)

	for _, m := range rep.Mechanisms {
		if m.IsCost {
			continue
		}
		rep.TotalSavedTokens += m.SavedTokens
	}
	rep.TotalOriginalTokens = t.SchemaNativeTokens + govOriginal
	if rep.TotalOriginalTokens > 0 {
		rep.TotalSavedPercent = float64(rep.TotalSavedTokens) / float64(rep.TotalOriginalTokens) * 100
	}
	rep.ExtraTurnsEstimate = t.DiscoveryCalls

	tools := make([]ToolSize, 0, len(toolTotals))
	serverTotals := map[string]*ToolSize{}
	for _, v := range toolTotals {
		tools = append(tools, *v)
		// GovernorToolStats.Tool is cacheIdentity's "server.tool" (or just
		// "tool" when server is empty); server names never contain a dot,
		// so splitting on the first one recovers the server.
		server := v.Tool
		if i := strings.Index(v.Tool, "."); i > 0 {
			server = v.Tool[:i]
		}
		se, ok := serverTotals[server]
		if !ok {
			se = &ToolSize{Tool: server}
			serverTotals[server] = se
		}
		se.OriginalTokens += v.OriginalTokens
		se.Calls += v.Calls
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].OriginalTokens > tools[j].OriginalTokens })
	if len(tools) > topToolsLimit {
		tools = tools[:topToolsLimit]
	}
	rep.TopToolsByResponseSize = tools

	servers := make([]ToolSize, 0, len(serverTotals))
	for _, v := range serverTotals {
		servers = append(servers, *v)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].OriginalTokens > servers[j].OriginalTokens })
	if len(servers) > topToolsLimit {
		servers = servers[:topToolsLimit]
	}
	rep.TopServersByResponseSize = servers

	if pricePerMTok != nil {
		rep.PricePerMTok = pricePerMTok
		cost := float64(rep.TotalSavedTokens) / 1_000_000 * *pricePerMTok
		rep.EstimatedCostSaved = &cost
	}

	return rep
}

const topToolsLimit = 10

// latestPerSession keeps only the latest (by Timestamp) record per
// SessionID, sorted by SessionID for deterministic output.
func latestPerSession(records []Record) []Record {
	bySession := map[string]Record{}
	for _, r := range records {
		cur, ok := bySession[r.SessionID]
		if !ok || r.Timestamp.After(cur.Timestamp) {
			bySession[r.SessionID] = r
		}
	}
	out := make([]Record, 0, len(bySession))
	for _, r := range bySession {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// Text renders the report as a human-readable, fixed-width summary. by
// selects the extra breakdown table ("tool" (default), "server" or
// "session"); an unknown value falls back to "tool".
func (r SavingsReport) Text(by string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "LeanProxy Savings Report (generated %s)\n", r.GeneratedAt.Format(time.RFC3339))
	if !r.Since.IsZero() {
		fmt.Fprintf(&b, "Since: %s\n", r.Since.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "Sessions: %d | Estimator: %s (measured unless noted)\n\n", r.SessionCount, r.Estimator)

	fmt.Fprintf(&b, "%-24s %10s %12s %12s %12s %8s %s\n", "MECHANISM", "MEASURED", "ORIGINAL", "RESULTING", "SAVED", "CALLS", "NOTES")
	for _, m := range r.Mechanisms {
		label := "measured"
		if !m.Measured {
			label = "estimated"
		}
		if m.IsCost {
			label += "/cost"
		}
		fmt.Fprintf(&b, "%-24s %10s %12d %12d %12d %8d %s\n", m.Mechanism, label, m.OriginalTokens, m.ResultingTokens, m.SavedTokens, m.Calls, m.Notes)
	}

	fmt.Fprintf(&b, "\nTotal saved (excludes discovery cost): %d tokens of %d (%.1f%%)\n", r.TotalSavedTokens, r.TotalOriginalTokens, r.TotalSavedPercent)
	fmt.Fprintf(&b, "Extra turns (estimated from %d discovery call(s), NOT a token figure): %d\n", r.ExtraTurnsEstimate, r.ExtraTurnsEstimate)

	switch by {
	case "server":
		if len(r.TopServersByResponseSize) > 0 {
			fmt.Fprintf(&b, "\nTop servers by response size:\n")
			for _, t := range r.TopServersByResponseSize {
				fmt.Fprintf(&b, "  %-40s %10d tokens over %d call(s)\n", t.Tool, t.OriginalTokens, t.Calls)
			}
		}
	case "session":
		if len(r.PerSession) > 0 {
			fmt.Fprintf(&b, "\nBy session:\n")
			for _, s := range r.PerSession {
				fmt.Fprintf(&b, "  %-40s %s  saved %d of %d tokens\n", s.SessionID, s.Timestamp.Format(time.RFC3339), s.TotalSavedTokens, s.TotalOriginalTokens)
			}
		}
	default:
		if len(r.TopToolsByResponseSize) > 0 {
			fmt.Fprintf(&b, "\nTop tools by response size (hint for projection rules):\n")
			for _, t := range r.TopToolsByResponseSize {
				fmt.Fprintf(&b, "  %-40s %10d tokens over %d call(s)\n", t.Tool, t.OriginalTokens, t.Calls)
			}
		}
	}

	if r.PricePerMTok != nil {
		fmt.Fprintf(&b, "\nEstimated cost saved at $%.2f/MTok (your price, not built in): $%.4f\n", *r.PricePerMTok, *r.EstimatedCostSaved)
	}

	fmt.Fprintf(&b, "\n%s\n", r.Methodology)
	return b.String()
}

// Markdown renders the report as a Markdown document. by selects the extra
// breakdown table, as Text does.
func (r SavingsReport) Markdown(by string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# LeanProxy Savings Report\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", r.GeneratedAt.Format(time.RFC3339))
	if !r.Since.IsZero() {
		fmt.Fprintf(&b, "- Since: %s\n", r.Since.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "- Sessions: %d\n- Estimator: `%s`\n\n", r.SessionCount, r.Estimator)

	fmt.Fprintf(&b, "| Mechanism | Label | Original | Resulting | Saved | Calls | Notes |\n")
	fmt.Fprintf(&b, "|---|---|---:|---:|---:|---:|---|\n")
	for _, m := range r.Mechanisms {
		label := "measured"
		if !m.Measured {
			label = "estimated"
		}
		if m.IsCost {
			label += "/cost"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %d | %s |\n", m.Mechanism, label, m.OriginalTokens, m.ResultingTokens, m.SavedTokens, m.Calls, m.Notes)
	}

	fmt.Fprintf(&b, "\n**Total saved (excludes discovery cost): %d / %d tokens (%.1f%%)**\n\n", r.TotalSavedTokens, r.TotalOriginalTokens, r.TotalSavedPercent)
	fmt.Fprintf(&b, "Extra turns (estimated, not a token figure): %d\n\n", r.ExtraTurnsEstimate)

	switch by {
	case "server":
		if len(r.TopServersByResponseSize) > 0 {
			fmt.Fprintf(&b, "## Top servers by response size\n\n| Server | Original tokens | Calls |\n|---|---:|---:|\n")
			for _, t := range r.TopServersByResponseSize {
				fmt.Fprintf(&b, "| %s | %d | %d |\n", t.Tool, t.OriginalTokens, t.Calls)
			}
			fmt.Fprintf(&b, "\n")
		}
	case "session":
		if len(r.PerSession) > 0 {
			fmt.Fprintf(&b, "## By session\n\n| Session | Timestamp | Saved | Original |\n|---|---|---:|---:|\n")
			for _, s := range r.PerSession {
				fmt.Fprintf(&b, "| %s | %s | %d | %d |\n", s.SessionID, s.Timestamp.Format(time.RFC3339), s.TotalSavedTokens, s.TotalOriginalTokens)
			}
			fmt.Fprintf(&b, "\n")
		}
	default:
		if len(r.TopToolsByResponseSize) > 0 {
			fmt.Fprintf(&b, "## Top tools by response size\n\n| Tool | Original tokens | Calls |\n|---|---:|---:|\n")
			for _, t := range r.TopToolsByResponseSize {
				fmt.Fprintf(&b, "| %s | %d | %d |\n", t.Tool, t.OriginalTokens, t.Calls)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	if r.PricePerMTok != nil {
		fmt.Fprintf(&b, "Estimated cost saved at $%.2f/MTok (your price, not built in): **$%.4f**\n\n", *r.PricePerMTok, *r.EstimatedCostSaved)
	}

	fmt.Fprintf(&b, "> %s\n", r.Methodology)
	return b.String()
}

// JSON renders the report as indented JSON: the documented, stable schema
// for --export json (see docs/savings-report.md).
func (r SavingsReport) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// CSV renders the mechanism breakdown as CSV (one row per mechanism, plus a
// trailing TOTAL row), for finance/spreadsheet consumption.
func (r SavingsReport) CSV(w io.Writer) error {
	enc := csv.NewWriter(w)
	defer enc.Flush()
	if err := enc.Write([]string{"mechanism", "measured", "is_cost", "original_tokens", "resulting_tokens", "saved_tokens", "calls", "notes"}); err != nil {
		return fmt.Errorf("csv header: %w", err)
	}
	for _, m := range r.Mechanisms {
		row := []string{
			m.Mechanism,
			strconv.FormatBool(m.Measured),
			strconv.FormatBool(m.IsCost),
			strconv.FormatInt(m.OriginalTokens, 10),
			strconv.FormatInt(m.ResultingTokens, 10),
			strconv.FormatInt(m.SavedTokens, 10),
			strconv.FormatInt(m.Calls, 10),
			m.Notes,
		}
		if err := enc.Write(row); err != nil {
			return fmt.Errorf("csv row %s: %w", m.Mechanism, err)
		}
	}
	total := []string{"TOTAL", "true", "false", strconv.FormatInt(r.TotalOriginalTokens, 10), "", strconv.FormatInt(r.TotalSavedTokens, 10), "", fmt.Sprintf("%.1f%% saved", r.TotalSavedPercent)}
	if err := enc.Write(total); err != nil {
		return fmt.Errorf("csv total row: %w", err)
	}
	return nil
}
