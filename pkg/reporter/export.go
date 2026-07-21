package reporter

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const defaultCostPerToken = 0.000002
const progressBatchInterval = 1000

type ExportRow struct {
	Timestamp     time.Time `json:"timestamp"`
	Team          string    `json:"team"`
	Project       string    `json:"project"`
	Server        string    `json:"server"`
	Tool          string    `json:"tool"`
	Tokens        int64     `json:"tokens"`
	EstimatedCost float64   `json:"estimated_cost"`
}

func ExportCSV(w io.Writer, entries []CallLogEntry, progress func(current, total int)) error {
	enc := csv.NewWriter(w)
	defer enc.Flush()

	if err := enc.Write([]string{"timestamp", "team", "project", "server", "tool", "tokens", "estimated_cost"}); err != nil {
		return fmt.Errorf("csv header: %w", err)
	}

	for i, e := range entries {
		row := []string{
			e.Timestamp.Format(time.RFC3339),
			"",
			"",
			e.ServerName,
			e.ToolName,
			fmt.Sprintf("%d", e.TokenCount),
			fmt.Sprintf("%.6f", float64(e.TokenCount)*defaultCostPerToken),
		}
		if err := enc.Write(row); err != nil {
			return fmt.Errorf("csv row %d: %w", i, err)
		}
		if progress != nil && ((i+1)%progressBatchInterval == 0 || i+1 == len(entries)) {
			progress(i+1, len(entries))
		}
	}
	return nil
}

func ExportJSON(w io.Writer, entries []CallLogEntry, progress func(current, total int)) error {
	if _, err := io.WriteString(w, "["); err != nil {
		return fmt.Errorf("json open: %w", err)
	}

	enc := json.NewEncoder(w)

	for i, e := range entries {
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return fmt.Errorf("json sep: %w", err)
			}
		}
		row := ExportRow{
			Timestamp:     e.Timestamp,
			Server:        e.ServerName,
			Tool:          e.ToolName,
			Tokens:        e.TokenCount,
			EstimatedCost: float64(e.TokenCount) * defaultCostPerToken,
		}
		if err := enc.Encode(row); err != nil {
			return fmt.Errorf("json row %d: %w", i, err)
		}
		if progress != nil && ((i+1)%progressBatchInterval == 0 || i+1 == len(entries)) {
			progress(i+1, len(entries))
		}
	}

	if _, err := io.WriteString(w, "]"); err != nil {
		return fmt.Errorf("json close: %w", err)
	}
	return nil
}
