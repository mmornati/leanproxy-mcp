package dryrun

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestNewDryRunner(t *testing.T) {
	dr := NewDryRunner(true)
	if dr == nil {
		t.Fatal("expected non-nil DryRunner")
	}
	if !dr.Enabled() {
		t.Error("expected Enabled() to return true")
	}
}

func TestNewDryRunnerDisabled(t *testing.T) {
	dr := NewDryRunner(false)
	if dr == nil {
		t.Fatal("expected non-nil DryRunner")
	}
	if dr.Enabled() {
		t.Error("expected Enabled() to return false")
	}
}

func TestShouldSkipWhenEnabled(t *testing.T) {
	dr := NewDryRunner(true)
	if !dr.ShouldSkip() {
		t.Error("expected ShouldSkip() to return true when enabled")
	}
}

func TestShouldSkipWhenDisabled(t *testing.T) {
	dr := NewDryRunner(false)
	if dr.ShouldSkip() {
		t.Error("expected ShouldSkip() to return false when disabled")
	}
}

func TestPreviewWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	dr := NewDryRunner(false)
	dr.Preview("test_action", map[string]interface{}{"key": "value"})

	if buf.Len() != 0 {
		t.Errorf("expected empty buffer, got %d bytes", buf.Len())
	}
}

func TestPreviewWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	dr := NewDryRunner(true)
	dr.Preview("proxy_start", map[string]interface{}{
		"listen": "127.0.0.1:8080",
	})

	output := buf.String()
	if output == "" {
		t.Fatal("expected non-empty output")
	}
	if !bytes.Contains([]byte(output), []byte("[DRY-RUN]")) {
		t.Errorf("expected [DRY-RUN] in output, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("proxy_start")) {
		t.Errorf("expected proxy_start in output, got: %s", output)
	}
}

func TestEnabled(t *testing.T) {
	drEnabled := NewDryRunner(true)
	if !drEnabled.Enabled() {
		t.Error("expected Enabled() to return true")
	}

	drDisabled := NewDryRunner(false)
	if drDisabled.Enabled() {
		t.Error("expected Enabled() to return false")
	}
}