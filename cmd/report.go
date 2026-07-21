package cmd

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
	"github.com/mmornati/leanproxy-mcp/pkg/utils"
	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate a Markdown-formatted report on tokens saved and risks intercepted",
	Long: `Generate a Markdown-formatted report summarizing token savings
and security risks intercepted during the LeanProxy session.

The report includes:
- Summary metrics (session ID, duration, total requests)
- Token savings breakdown by server
- Security events breakdown by redaction type
- Per-server detailed metrics

Output is written to stdout by default. Use --output to write to a file.

Use --export to export raw cost data as CSV or JSON for finance reporting.`,
	RunE: runReport,
}

var reportFlags struct {
	sessionID  string
	outputPath string
	jsonOutput bool
	noSecurity bool
	export     string
	since      string
}

func init() {
	reportCmd.Flags().StringVar(&reportFlags.sessionID, "session-id", "", "Generate report for specific session (default: current)")
	reportCmd.Flags().StringVar(&reportFlags.outputPath, "output", "", "Output file path (default: stdout)")
	reportCmd.Flags().BoolVar(&reportFlags.jsonOutput, "json", false, "Output JSON instead of Markdown")
	reportCmd.Flags().BoolVar(&reportFlags.noSecurity, "no-security", false, "Exclude security events from report")
	reportCmd.Flags().StringVar(&reportFlags.export, "export", "", "Export raw cost data format (csv, json)")
	reportCmd.Flags().StringVar(&reportFlags.since, "since", "", "Include entries since this date (YYYY-MM-DD)")
	RootCmd.AddCommand(reportCmd)
}

var globalReportGenerator = utils.NewReportGenerator()

func runReport(cmd *cobra.Command, args []string) error {
	if reportFlags.since != "" && reportFlags.export == "" {
		return fmt.Errorf("--since flag requires --export")
	}

	if reportFlags.export != "" {
		return runExport()
	}

	sessionData := buildSessionMetrics()

	if reportFlags.noSecurity {
		sessionData.SecurityEvents = []utils.SecurityEvent{}
	}

	var output string
	if reportFlags.jsonOutput {
		output = globalReportGenerator.GenerateJSONReport(sessionData)
	} else {
		output = globalReportGenerator.GenerateMarkdownReport(sessionData)
	}

	if reportFlags.outputPath != "" {
		if err := os.WriteFile(reportFlags.outputPath, []byte(output), 0600); err != nil {
			return fmt.Errorf("report output: %w", err)
		}
		fmt.Printf("Report written to %s\n", reportFlags.outputPath)
	} else {
		fmt.Println(output)
	}
	return nil
}

func runExport() error {
	var since time.Time
	if reportFlags.since != "" {
		var err error
		since, err = time.Parse("2006-01-02", reportFlags.since)
		if err != nil {
			return fmt.Errorf("invalid --since date format %q (use YYYY-MM-DD): %w", reportFlags.since, err)
		}
	}

	entries := reporter.GetEntries(since)

	out := io.Writer(os.Stdout)
	if reportFlags.outputPath != "" {
		f, err := os.Create(reportFlags.outputPath)
		if err != nil {
			return fmt.Errorf("creating output file %q: %w", reportFlags.outputPath, err)
		}
		defer f.Close()
		out = f
	}

	progress := func(current, total int) {
		if total > 0 {
			fmt.Fprintf(os.Stderr, "\rExporting: %d/%d rows", current, total)
		}
		if current == total {
			fmt.Fprintf(os.Stderr, "\n")
		}
	}

	switch reportFlags.export {
	case "csv":
		if err := reporter.ExportCSV(out, entries, progress); err != nil {
			return fmt.Errorf("exporting CSV: %w", err)
		}
	case "json":
		if err := reporter.ExportJSON(out, entries, progress); err != nil {
			return fmt.Errorf("exporting JSON: %w", err)
		}
	default:
		return fmt.Errorf("unsupported export format %q (use csv or json)", reportFlags.export)
	}

	if reportFlags.outputPath != "" {
		fmt.Printf("Export written to %s\n", reportFlags.outputPath)
	}
	return nil
}

func buildSessionMetrics() utils.SessionMetrics {
	cumulative := globalSavingsTracker.GetCumulativeSavings()
	breakdown := globalSavingsTracker.GetServerBreakdown()

	byServer := make(map[string]utils.ServerTokenSavings)
	for name, ss := range breakdown {
		byServer[name] = utils.ServerTokenSavings{
			ServerName:      ss.ServerName,
			OriginalTokens:  ss.OriginalTokens,
			OptimizedTokens: ss.OptimizedTokens,
			SavedTokens:     ss.SavedTokens,
		}
	}

	savingsPercentage := 0.0
	if cumulative.TotalOriginal > 0 {
		savingsPercentage = float64(cumulative.TotalSaved) / float64(cumulative.TotalOriginal) * 100
	}

	sessionID := reportFlags.sessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	}

	return utils.SessionMetrics{
		SessionID:     sessionID,
		SessionStart:  time.Now().Add(-cumulative.SessionDuration),
		SessionEnd:    time.Now(),
		TotalRequests: cumulative.RequestsProcessed,
		TokenSavings: utils.TokenSavingsSummary{
			OriginalTokens:    cumulative.TotalOriginal,
			OptimizedTokens:   cumulative.TotalOptimized,
			SavedTokens:       cumulative.TotalSaved,
			SavingsPercentage: savingsPercentage,
			ByServer:          byServer,
		},
		SecurityEvents: []utils.SecurityEvent{},
		ServerMetrics:  map[string]utils.ServerMetrics{},
	}
}
