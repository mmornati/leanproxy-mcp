package injection

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

type Action string

const (
	ActionBlock      Action = "block"
	ActionQuarantine Action = "quarantine"
	ActionRedact     Action = "redact"
	ActionLog        Action = "log"
)

type Rule struct {
	MinRisk int    `yaml:"min_risk"`
	MaxRisk int    `yaml:"max_risk"`
	Action  Action `yaml:"action"`
}

type ActionResult struct {
	Action             Action `json:"action"`
	Message            string `json:"message,omitempty"`
	RiskScore          int    `json:"risk_score"`
	QuarantineID       string `json:"quarantine_id,omitempty"`
	QuarantineDir      string `json:"-"`
	TransformedPayload string `json:"-"`
}

type Dispatcher struct {
	rules         []Rule
	quarantineDir string
}

func DefaultRules() []Rule {
	return []Rule{
		{MinRisk: 80, MaxRisk: 100, Action: ActionBlock},
		{MinRisk: 50, MaxRisk: 79, Action: ActionQuarantine},
		{MinRisk: 1, MaxRisk: 49, Action: ActionLog},
	}
}

func NewDispatcher(rules []Rule) *Dispatcher {
	if rules == nil {
		rules = DefaultRules()
	}
	qDir := defaultQuarantineDir()
	return &Dispatcher{
		rules:         rules,
		quarantineDir: qDir,
	}
}

func NewDispatcherWithQuarantineDir(rules []Rule, quarantineDir string) *Dispatcher {
	if rules == nil {
		rules = DefaultRules()
	}
	return &Dispatcher{
		rules:         rules,
		quarantineDir: quarantineDir,
	}
}

func defaultQuarantineDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("injection: cannot determine home dir, using temp", "error", err)
		return os.TempDir()
	}
	return filepath.Join(home, ".leanproxy", "quarantine")
}

func (d *Dispatcher) Rules() []Rule {
	result := make([]Rule, len(d.rules))
	copy(result, d.rules)
	return result
}

func (d *Dispatcher) Dispatch(result Result) ActionResult {
	risk := result.RiskScore
	for _, rule := range d.rules {
		if risk >= rule.MinRisk && (rule.MaxRisk == 0 || risk <= rule.MaxRisk) {
			return d.applyAction(rule.Action, result)
		}
	}
	return ActionResult{
		Action:    ActionLog,
		Message:   "no matching rule, logging",
		RiskScore: risk,
	}
}

func (d *Dispatcher) applyAction(action Action, result Result) ActionResult {
	switch action {
	case ActionBlock:
		return d.block(result)
	case ActionQuarantine:
		return d.quarantine(result)
	case ActionRedact:
		return d.redact(result)
	case ActionLog:
		return d.logOnly(result)
	default:
		return d.logOnly(result)
	}
}

func (d *Dispatcher) block(result Result) ActionResult {
	slog.Error("injection: blocked high-risk payload",
		"risk_score", result.RiskScore,
		"matches", len(result.Matches),
	)
	return ActionResult{
		Action:    ActionBlock,
		Message:   fmt.Sprintf("BLOCKED: payload risk score %d exceeds threshold", result.RiskScore),
		RiskScore: result.RiskScore,
	}
}

func (d *Dispatcher) quarantine(result Result) ActionResult {
	id := uuid.New().String()
	qDir := d.quarantineDir
	if err := os.MkdirAll(qDir, 0700); err != nil {
		slog.Error("injection: cannot create quarantine dir", "path", qDir, "error", err)
		return d.block(result)
	}

	qPath := filepath.Join(qDir, id+".json")
	qEntry := struct {
		ID        string  `json:"id"`
		RiskScore int     `json:"risk_score"`
		Payload   string  `json:"payload"`
		Matches   []Match `json:"matches"`
	}{
		ID:        id,
		RiskScore: result.RiskScore,
		Payload:   result.Payload,
		Matches:   result.Matches,
	}

	data, err := json.MarshalIndent(qEntry, "", "  ")
	if err != nil {
		slog.Error("injection: failed to marshal quarantine entry", "error", err)
		return d.block(result)
	}

	if err := os.WriteFile(qPath, data, 0600); err != nil {
		slog.Error("injection: failed to write quarantine file", "path", qPath, "error", err)
		return d.block(result)
	}

	slog.Warn("injection: payload quarantined",
		"id", id,
		"risk_score", result.RiskScore,
		"path", qPath,
	)

	return ActionResult{
		Action:        ActionQuarantine,
		Message:       fmt.Sprintf("[CONTENT_QUARANTINED - review at %s]", qPath),
		RiskScore:     result.RiskScore,
		QuarantineID:  id,
		QuarantineDir: qDir,
	}
}

func (d *Dispatcher) redact(result Result) ActionResult {
	redacted := "[CONTENT_REDACTED]"
	slog.Warn("injection: redacting payload",
		"risk_score", result.RiskScore,
		"matches", len(result.Matches),
	)
	return ActionResult{
		Action:             ActionRedact,
		Message:            "[CONTENT_REDACTED]",
		RiskScore:          result.RiskScore,
		TransformedPayload: redacted,
	}
}

func (d *Dispatcher) logOnly(result Result) ActionResult {
	slog.Debug("injection: low-risk payload logged",
		"risk_score", result.RiskScore,
		"matches", len(result.Matches),
	)
	return ActionResult{
		Action:    ActionLog,
		Message:   "forwarded",
		RiskScore: result.RiskScore,
	}
}

func (d *Dispatcher) QuarantineDir() string {
	return d.quarantineDir
}

type ActionCounts struct {
	Block      int `json:"block"`
	Quarantine int `json:"quarantine"`
	Redact     int `json:"redact"`
	Log        int `json:"log"`
	Total      int `json:"total"`
}
