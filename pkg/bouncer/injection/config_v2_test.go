package injection

import (
	"strings"
	"testing"
)

func TestConfig_ResponsePolicyDefaults(t *testing.T) {
	cfg := &Config{Enabled: true}
	d := cfg.BuildResponseDispatcher()
	if d == nil {
		t.Fatal("responses are scanned by default")
	}
	rules := d.Rules()
	if len(rules) != 2 || rules[0] != (Rule{MinRisk: 70, MaxRisk: 100, Action: ActionAnnotate}) || rules[1].Action != ActionLog {
		t.Fatalf("default response rules = %+v", rules)
	}
	if got := d.Dispatch(Result{RiskScore: 90}); got.Action != ActionAnnotate {
		t.Fatalf("90 -> %s", got.Action)
	}
	if got := d.Dispatch(Result{RiskScore: 40}); got.Action != ActionLog {
		t.Fatalf("40 -> %s", got.Action)
	}

	cfg.Threshold = 50
	if r := cfg.BuildResponseDispatcher().Rules(); r[0].MinRisk != 50 {
		t.Fatalf("threshold not used: %+v", r)
	}
	off := false
	cfg.ScanResponses = &off
	if cfg.BuildResponseDispatcher() != nil {
		t.Fatal("scan_responses: false must disable response classification")
	}

	cfg = &Config{Enabled: true, ResponsePolicies: []Rule{{MinRisk: 1, MaxRisk: 100, Action: ActionBlock}}}
	if r := cfg.BuildResponseDispatcher().Rules(); len(r) != 1 || r[0].Action != ActionBlock {
		t.Fatalf("response_policies ignored: %+v", r)
	}
}

func TestConfig_RequestPoliciesPrecedence(t *testing.T) {
	cfg := &Config{
		Enabled:         true,
		Action:          "block",
		Policies:        []Rule{{MinRisk: 1, MaxRisk: 100, Action: ActionLog}},
		RequestPolicies: []Rule{{MinRisk: 1, MaxRisk: 100, Action: ActionRedact}},
	}
	if r := cfg.BuildDispatcher().Rules(); len(r) != 1 || r[0].Action != ActionRedact {
		t.Fatalf("request_policies must win: %+v", r)
	}
	cfg.RequestPolicies = nil
	if r := cfg.BuildDispatcher().Rules(); r[0].Action != ActionLog {
		t.Fatalf("policies is the fallback: %+v", r)
	}
}

func TestConfig_Validate(t *testing.T) {
	valid := []*Config{
		nil,
		{Enabled: false, Action: "nonsense"},
		{Enabled: true},
		{Enabled: true, Threshold: 70, Action: "quarantine",
			RequestPolicies:  []Rule{{MinRisk: 80, MaxRisk: 100, Action: ActionBlock}, {MinRisk: 1, MaxRisk: 79, Action: ActionRedact}},
			ResponsePolicies: []Rule{{MinRisk: 80, MaxRisk: 100, Action: ActionBlock}, {MinRisk: 50, MaxRisk: 79, Action: ActionAnnotate}, {MinRisk: 1, MaxRisk: 49, Action: ActionLog}},
			Judge:            &JudgeConfig{Provider: "ollama", Model: "llama3.1:8b", Threshold: 60, Timeout: "1500ms"}},
	}
	for i, c := range valid {
		if err := c.Validate(); err != nil {
			t.Errorf("valid[%d]: %v", i, err)
		}
	}
	invalid := map[string]*Config{
		"threshold":                {Enabled: true, Threshold: 101},
		"action":                   {Enabled: true, Action: "annotate"},
		"annotate on requests":     {Enabled: true, RequestPolicies: []Rule{{MinRisk: 1, MaxRisk: 100, Action: ActionAnnotate}}},
		"quarantine on responses":  {Enabled: true, ResponsePolicies: []Rule{{MinRisk: 1, MaxRisk: 100, Action: ActionQuarantine}}},
		"unknown action":           {Enabled: true, Policies: []Rule{{MinRisk: 1, MaxRisk: 100, Action: "drop"}}},
		"inverted band":            {Enabled: true, ResponsePolicies: []Rule{{MinRisk: 80, MaxRisk: 20, Action: ActionLog}}},
		"band out of range":        {Enabled: true, RequestPolicies: []Rule{{MinRisk: 1, MaxRisk: 120, Action: ActionLog}}},
		"max_scan_bytes":           {Enabled: true, MaxScanBytes: -1},
		"bad custom pattern":       {Enabled: true, CustomPatterns: []PatternDef{{Name: "x", Pattern: "(", Weight: 10}}},
		"judge provider":           {Enabled: true, Judge: &JudgeConfig{Provider: "openai", Model: "m"}},
		"judge model":              {Enabled: true, Judge: &JudgeConfig{Provider: "ollama"}},
		"judge url":                {Enabled: true, Judge: &JudgeConfig{Provider: "ollama", Model: "m", URL: "localhost:11434"}},
		"judge timeout":            {Enabled: true, Judge: &JudgeConfig{Provider: "ollama", Model: "m", Timeout: "soon"}},
		"judge threshold":          {Enabled: true, Judge: &JudgeConfig{Provider: "ollama", Model: "m", Threshold: 150}},
		"judge band":               {Enabled: true, Judge: &JudgeConfig{Provider: "ollama", Model: "m", MinRisk: 90, MaxRisk: 40}},
		"judge band out of bounds": {Enabled: true, Judge: &JudgeConfig{Provider: "ollama", Model: "m", MaxRisk: 101}},
	}
	for name, c := range invalid {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), "injection") {
			t.Errorf("%s: error %q lacks context", name, err)
		}
	}
}

func TestLoadConfig_V2Keys(t *testing.T) {
	cfg, err := LoadConfig(strings.NewReader(`
enabled: true
threshold: 60
scan_responses: true
max_scan_bytes: 65536
request_policies:
  - {min_risk: 80, max_risk: 100, action: block}
response_policies:
  - {min_risk: 60, max_risk: 100, action: annotate}
judge:
  provider: ollama
  model: llama3.1:8b
  threshold: 50
  timeout: 2s
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.EffectiveMaxScanBytes() != 65536 || !cfg.ScansResponses() || cfg.Judge.Model != "llama3.1:8b" || len(cfg.ResponsePolicies) != 1 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// The quarantine action reports its ID and no longer builds a bogus
// "[CONTENT_REDACTED]" payload for redact.
func TestDispatch_V2Actions(t *testing.T) {
	d := NewDispatcherWithQuarantineDir([]Rule{{MinRisk: 1, MaxRisk: 100, Action: ActionQuarantine}}, t.TempDir())
	got := d.Dispatch(Result{RiskScore: 60, Payload: "p"})
	if got.Action != ActionQuarantine || got.QuarantineID == "" || !strings.Contains(got.Message, got.QuarantineID) {
		t.Fatalf("quarantine = %+v", got)
	}
	d = NewDispatcher([]Rule{{MinRisk: 1, MaxRisk: 100, Action: ActionAnnotate}})
	if got := d.Dispatch(Result{RiskScore: 60}); got.Action != ActionAnnotate {
		t.Fatalf("annotate = %+v", got)
	}
}
