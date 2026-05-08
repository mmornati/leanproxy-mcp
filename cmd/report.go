package cmd

import (
	"fmt"
	"os"
	"time"

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

Output is written to stdout by default. Use --output to write to a file.`,
	Run: runReport,
}

var reportFlags struct {
	sessionID  string
	outputPath string
	jsonOutput bool
	noSecurity bool
}

func init() {
	reportCmd.Flags().StringVar(&reportFlags.sessionID, "session-id", "", "Generate report for specific session (default: current)")
	reportCmd.Flags().StringVar(&reportFlags.outputPath, "output", "", "Output file path (default: stdout)")
	reportCmd.Flags().BoolVar(&reportFlags.jsonOutput, "json", false, "Output JSON instead of Markdown")
	reportCmd.Flags().BoolVar(&reportFlags.noSecurity, "no-security", false, "Exclude security events from report")
	RootCmd.AddCommand(reportCmd)
}

var globalReportGenerator = utils.NewReportGenerator()

func runReport(cmd *cobra.Command, args []string) {
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
		err := os.WriteFile(reportFlags.outputPath, []byte(output), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing report: %v\n", fmt.Errorf("report output: context: %w", err))
			os.Exit(1)
		}
		fmt.Printf("Report written to %s\n", reportFlags.outputPath)
	} else {
		fmt.Println(output)
	}
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
