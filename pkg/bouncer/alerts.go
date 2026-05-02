package bouncer

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

type AlertManager struct {
	verbose       bool
	enabled       bool
	mu            sync.Mutex
	currentCounts map[string]int
}

func NewAlertManager(verbose bool) *AlertManager {
	return &AlertManager{
		verbose:       verbose,
		enabled:       true,
		currentCounts: make(map[string]int),
	}
}

type RedactionEvent struct {
	PatternName string
	Count       int
	Timestamp   time.Time
	MessageID   string
	Method      string
}

func (am *AlertManager) RecordRedaction(event RedactionEvent) {
	if !am.enabled {
		return
	}

	am.mu.Lock()
	am.currentCounts[event.PatternName] += event.Count
	am.mu.Unlock()

	am.emitAlert(event)
}

func (am *AlertManager) emitAlert(event RedactionEvent) {
	attrs := []any{
		"pattern", event.PatternName,
		"count", event.Count,
		"timestamp", event.Timestamp.Format(time.RFC3339),
	}

	if am.verbose && event.MessageID != "" {
		attrs = append(attrs, "msg_id", event.MessageID, "method", event.Method)
	}

	if am.verbose {
		slog.Debug("redaction_match", attrs...)
	} else {
		slog.Info("redaction_event",
			slog.String("pattern", event.PatternName),
			slog.Int("count", event.Count))
	}
}

func (am *AlertManager) EmitSummary(messageID string, method string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if len(am.currentCounts) == 0 {
		return
	}

	total := 0
	for _, count := range am.currentCounts {
		total += count
	}

	if total == 0 {
		return
	}

	slog.Info("redaction_summary",
		slog.String("msg_id", messageID),
		slog.String("method", method),
		slog.Int("total_redactions", total),
		slog.Any("breakdown", am.currentCounts))

	if am.verbose {
		am.emitVerboseSummary(messageID, method)
	}

	am.currentCounts = make(map[string]int)
}

func (am *AlertManager) emitVerboseSummary(messageID, method string) {
	summary := map[string]interface{}{
		"event":       "redaction_summary",
		"message_id":  messageID,
		"method":      method,
		"patterns":    am.currentCounts,
		"has_secrets": true,
		"secret_fields": detectSecretFields(method),
	}

	data, _ := json.Marshal(summary)
	slog.Debug("redaction_detail", slog.String("detail", string(data)))
}

func detectSecretFields(method string) []string {
	knownMethods := map[string][]string{
		"tools/call":       {"arguments", "input"},
		"resources/read":  {"uri", "contents"},
	}
	if fields, ok := knownMethods[method]; ok {
		return fields
	}
	return []string{"payload"}
}

func (am *AlertManager) SetVerbose(verbose bool) {
	am.verbose = verbose
}

func (am *AlertManager) SetEnabled(enabled bool) {
	am.enabled = enabled
}

func (am *AlertManager) GetCounts() map[string]int {
	am.mu.Lock()
	defer am.mu.Unlock()
	result := make(map[string]int)
	for k, v := range am.currentCounts {
		result[k] = v
	}
	return result
}