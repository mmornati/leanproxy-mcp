package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/usage"
	"github.com/spf13/cobra"
)

// reportCmd is the auditable savings report (issue #324): it reads the
// real counters every front end records (pkg/usage's JSONL store, fed
// from pkg/metrics.Snapshot -- the response governor's GovernorStats and
// the schema/discovery telemetry counters), never an estimated or modeled
// number. See docs/savings-report.md for the full methodology.
var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Auditable savings report built from real, measured counters",
	Long: `Generate a savings report from the counters the proxy actually recorded:
schema savings (router vs. passthrough tools/list size), discovery cost
(search_tools/list_tools/list_servers), and the response governor's
truncation/projection/dedup/summarization savings (issues #319-#321).

Every figure is labelled measured or estimated, with the estimator named
(chars/4). There is no simulated or modeled "native cost" anywhere in this
report -- see docs/savings-report.md for the exact methodology, and
docs/benchmark-results.md for how these numbers compare with the benchmark
harness.

Output is a human-readable summary by default. Use --export to write
csv, json or md instead, and --output to write it to a file (mode 0600)
rather than stdout.`,
	RunE: runReport,
}

var reportFlags struct {
	since        string
	by           string
	export       string
	outputPath   string
	jsonOutput   bool
	pricePerMTok string
}

func init() {
	reportCmd.Flags().StringVar(&reportFlags.since, "since", "", `Only include usage since this time (e.g. "7d", "24h", or "2026-01-01"); default: every retained record`)
	reportCmd.Flags().StringVar(&reportFlags.by, "by", "tool", "Extra breakdown to show: tool, server or session")
	reportCmd.Flags().StringVar(&reportFlags.export, "export", "", "Export format: csv, json or md (default: human-readable text)")
	reportCmd.Flags().StringVar(&reportFlags.outputPath, "output", "", "Output file path (default: stdout), written with mode 0600")
	reportCmd.Flags().BoolVar(&reportFlags.jsonOutput, "json", false, "Shorthand for --export json")
	reportCmd.Flags().StringVar(&reportFlags.pricePerMTok, "price-per-mtok", "", "Optional: compute an estimated cost saved at this price per million tokens (your own number; there is no built-in price table)")
	RootCmd.AddCommand(reportCmd)
}

func runReport(cmd *cobra.Command, args []string) error {
	since, err := parseSinceFlag(reportFlags.since)
	if err != nil {
		return fmt.Errorf("invalid --since %q: %w", reportFlags.since, err)
	}

	var price *float64
	if reportFlags.pricePerMTok != "" {
		p, err := strconv.ParseFloat(reportFlags.pricePerMTok, 64)
		if err != nil || p < 0 {
			return fmt.Errorf("invalid --price-per-mtok %q: must be a non-negative number", reportFlags.pricePerMTok)
		}
		price = &p
	}

	by := strings.ToLower(strings.TrimSpace(reportFlags.by))
	switch by {
	case "", "tool", "server", "session":
		if by == "" {
			by = "tool"
		}
	default:
		return fmt.Errorf("invalid --by %q (use tool, server or session)", reportFlags.by)
	}

	export := reportFlags.export
	if export == "" && reportFlags.jsonOutput {
		export = "json"
	}
	switch export {
	case "", "csv", "json", "md":
	default:
		return fmt.Errorf("unsupported export format %q (use csv, json or md)", export)
	}

	store, err := usage.NewStore()
	if err != nil {
		return fmt.Errorf("opening usage store: %w", err)
	}
	records, err := store.Load(since)
	if err != nil {
		return fmt.Errorf("reading usage store: %w", err)
	}

	report := usage.BuildSavingsReport(records, since, price)

	var output string
	switch export {
	case "csv":
		var b strings.Builder
		if err := report.CSV(&b); err != nil {
			return fmt.Errorf("rendering csv: %w", err)
		}
		output = b.String()
	case "json":
		data, err := report.JSON()
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		output = string(data)
	case "md":
		output = report.Markdown(by)
	default:
		output = report.Text(by)
	}

	if reportFlags.outputPath != "" {
		// Mode 0600, not the 0666-before-umask of os.Create (see the
		// issue's "Minor" fix).
		if err := os.WriteFile(reportFlags.outputPath, []byte(output), 0600); err != nil {
			return fmt.Errorf("report output: %w", err)
		}
		fmt.Printf("Report written to %s\n", reportFlags.outputPath)
		return nil
	}

	fmt.Println(output)
	return nil
}

// parseSinceFlag parses --since as a relative duration extended with a "d"
// (day) unit on top of what time.ParseDuration understands (e.g. "7d",
// "36h", "90m"), or an absolute "YYYY-MM-DD" date. An empty string means no
// cutoff (every retained record).
func parseSinceFlag(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := parseFlexibleDuration(v); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf(`use a duration like "7d"/"24h" or a date like "2026-01-02"`)
}

// parseFlexibleDuration extends time.ParseDuration with a "d" (24h) unit,
// e.g. "7d" or "1d12h".
func parseFlexibleDuration(v string) (time.Duration, error) {
	if !strings.ContainsAny(v, "dD") {
		return time.ParseDuration(v)
	}
	var total time.Duration
	var numStart int
	for i, r := range v {
		if r == 'd' || r == 'D' {
			n, err := strconv.Atoi(v[numStart:i])
			if err != nil {
				return 0, fmt.Errorf("invalid day component in %q: %w", v, err)
			}
			total += time.Duration(n) * 24 * time.Hour
			numStart = i + 1
		}
	}
	if numStart < len(v) {
		rest, err := time.ParseDuration(v[numStart:])
		if err != nil {
			return 0, fmt.Errorf("invalid duration suffix in %q: %w", v, err)
		}
		total += rest
	}
	if total <= 0 {
		return 0, fmt.Errorf("empty or non-positive duration %q", v)
	}
	return total, nil
}
