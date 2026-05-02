package bouncer

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestAlertManagerRecordsRedactions(t *testing.T) {
	am := NewAlertManager(false)

	am.RecordRedaction(RedactionEvent{
		PatternName: "aws-access-key",
		Count:       1,
		Timestamp:   time.Now(),
	})

	counts := am.GetCounts()
	if counts["aws-access-key"] != 1 {
		t.Errorf("expected count 1, got %d", counts["aws-access-key"])
	}
}

func TestAlertManagerMultipleRedactions(t *testing.T) {
	am := NewAlertManager(false)

	am.RecordRedaction(RedactionEvent{
		PatternName: "aws-access-key",
		Count:       2,
		Timestamp:   time.Now(),
	})
	am.RecordRedaction(RedactionEvent{
		PatternName: "github-classic-pat",
		Count:       3,
		Timestamp:   time.Now(),
	})

	counts := am.GetCounts()
	if counts["aws-access-key"] != 2 {
		t.Errorf("expected aws count 2, got %d", counts["aws-access-key"])
	}
	if counts["github-classic-pat"] != 3 {
		t.Errorf("expected github count 3, got %d", counts["github-classic-pat"])
	}
}

func TestAlertManagerDisabled(t *testing.T) {
	am := NewAlertManager(false)
	am.SetEnabled(false)

	am.RecordRedaction(RedactionEvent{
		PatternName: "aws-access-key",
		Count:       1,
		Timestamp:   time.Now(),
	})

	counts := am.GetCounts()
	if len(counts) != 0 {
		t.Error("disabled alert manager should not record")
	}
}

func TestAlertManagerSetVerbose(t *testing.T) {
	am := NewAlertManager(false)
	if am.verbose {
		t.Error("expected verbose to be false initially")
	}

	am.SetVerbose(true)
	if !am.verbose {
		t.Error("expected verbose to be true after SetVerbose")
	}
}

func TestDetectSecretFields(t *testing.T) {
	fields := detectSecretFields("tools/call")
	if len(fields) != 2 || fields[0] != "arguments" {
		t.Errorf("expected [arguments, input], got %v", fields)
	}

	fields = detectSecretFields("resources/read")
	if len(fields) != 2 || fields[0] != "uri" {
		t.Errorf("expected [uri, contents], got %v", fields)
	}

	fields = detectSecretFields("unknown/method")
	if len(fields) != 1 || fields[0] != "payload" {
		t.Errorf("expected [payload], got %v", fields)
	}
}

func TestEmitSummaryEmpty(t *testing.T) {
	am := NewAlertManager(false)
	am.EmitSummary("msg-123", "tools/call")
}

func TestAlertManagerVerboseMode(t *testing.T) {
	am := NewAlertManager(true)

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	am.RecordRedaction(RedactionEvent{
		PatternName: "aws-access-key",
		Count:       1,
		Timestamp:   time.Now(),
		MessageID:   "msg-123",
		Method:      "tools/call",
	})

	output := buf.String()
	if !strings.Contains(output, "aws-access-key") {
		t.Error("expected pattern name in output")
	}
	if strings.Contains(output, "AKIA") {
		t.Error("secret value should not appear in alert output")
	}
}

func TestNoSecretsInAlerts(t *testing.T) {
	am := NewAlertManager(false)

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	am.RecordRedaction(RedactionEvent{
		PatternName: "aws-access-key",
		Count:       1,
		Timestamp:   time.Now(),
	})

	am.EmitSummary("msg-123", "tools/call")

	output := buf.String()
	if strings.Contains(output, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("secret value should not appear in alert output")
	}
	if strings.Contains(output, "secret") && !strings.Contains(output, "redaction_event") {
		t.Error("alert should not contain raw secret data")
	}
	if !strings.Contains(output, "aws-access-key") {
		t.Error("pattern name should appear in output")
	}
}

func TestAlertManagerEmitSummary(t *testing.T) {
	am := NewAlertManager(false)

	am.RecordRedaction(RedactionEvent{
		PatternName: "aws-access-key",
		Count:       2,
		Timestamp:   time.Now(),
	})
	am.RecordRedaction(RedactionEvent{
		PatternName: "github-classic-pat",
		Count:       1,
		Timestamp:   time.Now(),
	})

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	am.EmitSummary("msg-123", "tools/call")

	output := buf.String()
	if !strings.Contains(output, "redaction_summary") {
		t.Error("expected summary in output")
	}
}

func TestAlertManagerEmitSummaryNoRedactions(t *testing.T) {
	am := NewAlertManager(false)

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	am.EmitSummary("msg-123", "tools/call")

	output := buf.String()
	if strings.Contains(output, "redaction") {
		t.Error("no redaction summary should be emitted when no redactions occurred")
	}
}

func TestRedactionEventStruct(t *testing.T) {
	event := RedactionEvent{
		PatternName: "test-pattern",
		Count:       5,
		Timestamp:   time.Now(),
		MessageID:   "test-id",
		Method:      "test/method",
	}

	if event.PatternName != "test-pattern" {
		t.Errorf("expected pattern name test-pattern, got %s", event.PatternName)
	}
	if event.Count != 5 {
		t.Errorf("expected count 5, got %d", event.Count)
	}
	if event.MessageID != "test-id" {
		t.Errorf("expected message id test-id, got %s", event.MessageID)
	}
	if event.Method != "test/method" {
		t.Errorf("expected method test/method, got %s", event.Method)
	}
}

func TestAlertManagerResetAfterSummary(t *testing.T) {
	am := NewAlertManager(false)

	am.RecordRedaction(RedactionEvent{
		PatternName: "aws-access-key",
		Count:       1,
		Timestamp:   time.Now(),
	})

	counts := am.GetCounts()
	if counts["aws-access-key"] != 1 {
		t.Error("expected count 1 before summary")
	}

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	am.EmitSummary("msg-123", "tools/call")

	counts = am.GetCounts()
	if len(counts) != 0 {
		t.Error("expected counts to be reset after summary")
	}
}