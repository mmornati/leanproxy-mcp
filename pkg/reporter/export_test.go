package reporter

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestExportCSV(t *testing.T) {
	entries := []CallLogEntry{
		{ServerName: "s1", ToolName: "tool-a", TokenCount: 100, Timestamp: time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)},
		{ServerName: "s1", ToolName: "tool-b", TokenCount: 200, Timestamp: time.Date(2026, 7, 21, 11, 0, 0, 0, time.UTC)},
		{ServerName: "s2", ToolName: "tool-a", TokenCount: 50, Timestamp: time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)},
	}

	var buf bytes.Buffer
	if err := ExportCSV(&buf, entries, nil); err != nil {
		t.Fatalf("ExportCSV: %v", err)
	}

	r := csv.NewReader(&buf)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}

	if len(records) != 4 {
		t.Fatalf("got %d rows, want 4 (header + 3 data)", len(records))
	}

	header := records[0]
	expectedHeader := []string{"timestamp", "team", "project", "server", "tool", "tokens", "estimated_cost"}
	for i, h := range expectedHeader {
		if header[i] != h {
			t.Errorf("header[%d] = %q, want %q", i, header[i], h)
		}
	}

	if records[1][3] != "s1" || records[1][4] != "tool-a" || records[1][5] != "100" {
		t.Errorf("row1 = %v, want server=s1 tool=tool-a tokens=100", records[1])
	}

	if records[3][3] != "s2" || records[3][4] != "tool-a" || records[3][5] != "50" {
		t.Errorf("row3 = %v, want server=s2 tool=tool-a tokens=50", records[3])
	}

	_ = time.RFC3339
	if !strings.HasPrefix(records[1][0], "2026-07-21T10:00:00") {
		t.Errorf("row1 timestamp = %q, want 2026-07-21T10:00:00Z", records[1][0])
	}

	if records[1][1] != "" || records[1][2] != "" {
		t.Errorf("expected empty team/project fields, got %q / %q", records[1][1], records[1][2])
	}
}

func TestExportCSVEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportCSV(&buf, nil, nil); err != nil {
		t.Fatalf("ExportCSV empty: %v", err)
	}

	r := csv.NewReader(&buf)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d rows, want 1 (header only)", len(records))
	}
}

func TestExportCSVProgress(t *testing.T) {
	entries := []CallLogEntry{
		{ServerName: "s1", ToolName: "t1", TokenCount: 10, Timestamp: time.Now()},
		{ServerName: "s1", ToolName: "t2", TokenCount: 20, Timestamp: time.Now()},
	}

	var calls []int
	progress := func(current, total int) {
		calls = append(calls, current)
	}

	var buf bytes.Buffer
	if err := ExportCSV(&buf, entries, progress); err != nil {
		t.Fatalf("ExportCSV with progress: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("progress called %d times, want 1 (batched, last row only)", len(calls))
	}
	if calls[0] != 2 {
		t.Errorf("progress calls = %v, want [2]", calls)
	}
}

func TestExportJSON(t *testing.T) {
	ts := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	entries := []CallLogEntry{
		{ServerName: "s1", ToolName: "tool-a", TokenCount: 100, Timestamp: ts},
		{ServerName: "s2", ToolName: "tool-b", TokenCount: 200, Timestamp: ts.Add(time.Hour)},
	}

	var buf bytes.Buffer
	if err := ExportJSON(&buf, entries, nil); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	output := buf.Bytes()
	if len(output) < 2 || output[0] != '[' || output[len(output)-1] != ']' {
		t.Errorf("expected JSON array, got: %s", string(output))
	}

	var decoded []ExportRow
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	if len(decoded) != 2 {
		t.Fatalf("got %d rows, want 2", len(decoded))
	}

	if decoded[0].Server != "s1" || decoded[0].Tool != "tool-a" || decoded[0].Tokens != 100 {
		t.Errorf("row0 = %+v, want server=s1 tool=tool-a tokens=100", decoded[0])
	}

	if decoded[1].Server != "s2" || decoded[1].Tool != "tool-b" || decoded[1].Tokens != 200 {
		t.Errorf("row1 = %+v, want server=s2 tool=tool-b tokens=200", decoded[1])
	}

	expectedCost := float64(100) * defaultCostPerToken
	if decoded[0].EstimatedCost != expectedCost {
		t.Errorf("estimated_cost = %f, want %f", decoded[0].EstimatedCost, expectedCost)
	}

	if !decoded[0].Timestamp.Equal(ts) {
		t.Errorf("timestamp = %v, want %v", decoded[0].Timestamp, ts)
	}

	if decoded[0].Team != "" || decoded[0].Project != "" {
		t.Errorf("expected empty team/project in JSON export")
	}
}

func TestExportJSONEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := ExportJSON(&buf, nil, nil); err != nil {
		t.Fatalf("ExportJSON empty: %v", err)
	}

	output := buf.String()
	if output != "[]" {
		t.Errorf("expected empty array, got %q", output)
	}
}

func TestExportJSONNoPII(t *testing.T) {
	entries := []CallLogEntry{
		{ServerName: "s1", ToolName: "t1", TokenCount: 50, Timestamp: time.Now()},
	}

	var buf bytes.Buffer
	if err := ExportJSON(&buf, entries, nil); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	output := buf.String()

	if strings.Contains(output, "prompt") || strings.Contains(output, "secret") || strings.Contains(output, "password") {
		t.Error("export should not contain PII or prompt content (NFR4)")
	}

	var rows []ExportRow
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, r := range rows {
		if r.Team != "" {
			t.Error("team field should be empty (no PII)")
		}
	}
}

func TestExportJSONProgress(t *testing.T) {
	entries := []CallLogEntry{
		{ServerName: "s1", ToolName: "t1", TokenCount: 10, Timestamp: time.Now()},
		{ServerName: "s2", ToolName: "t2", TokenCount: 20, Timestamp: time.Now()},
	}

	var calls []int
	progress := func(current, total int) {
		calls = append(calls, current)
	}

	var buf bytes.Buffer
	if err := ExportJSON(&buf, entries, progress); err != nil {
		t.Fatalf("ExportJSON with progress: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("progress called %d times, want 1 (batched, last row only)", len(calls))
	}
	if calls[0] != 2 {
		t.Errorf("progress calls = %v, want [2]", calls)
	}
}

func TestExportJSONLarge(t *testing.T) {
	n := 10000
	entries := make([]CallLogEntry, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, CallLogEntry{
			ServerName: "s1",
			ToolName:   "t1",
			TokenCount: int64(i),
			Timestamp:  time.Now(),
		})
	}

	var buf bytes.Buffer
	if err := ExportJSON(&buf, entries, nil); err != nil {
		t.Fatalf("ExportJSON large: %v", err)
	}

	var decoded []ExportRow
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal large json: %v", err)
	}

	if len(decoded) != n {
		t.Errorf("decoded %d rows, want %d", len(decoded), n)
	}
}
