package governor

import (
	"strings"
	"testing"
	"time"
)

func intp(v int) *int { return &v }

func TestConfig_Validate(t *testing.T) {
	var nilCfg *Config
	if err := nilCfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		cfg     Config
		wantErr string
	}{
		"defaults":           {Config{Enabled: true}, ""},
		"zero budget":        {Config{MaxTokens: intp(0)}, ""},
		"negative budget":    {Config{MaxTokens: intp(-1)}, "must be >= 0"},
		"tiny budget":        {Config{MaxTokens: intp(10)}, "or >= 100"},
		"rule without match": {Config{Tools: []ToolRule{{MaxTokens: intp(500)}}}, "match is required"},
		"bad glob":           {Config{Tools: []ToolRule{{Match: "a[", MaxTokens: intp(500)}}}, "invalid match glob"},
		"rule budget":        {Config{Tools: []ToolRule{{Match: "a.*", MaxTokens: intp(5)}}}, "tools[0].max_tokens"},
		"both":               {Config{Tools: []ToolRule{{Match: "a.*", MaxTokens: intp(500), Passthrough: true}}}, "mutually exclusive"},
		"bad ttl":            {Config{Spill: SpillConfig{TTL: "soon"}}, "spill.ttl"},
		"zero ttl":           {Config{Spill: SpillConfig{TTL: "0s"}}, "must be > 0"},
		"negative bytes":     {Config{Spill: SpillConfig{MaxBytes: -1}}, "max_bytes"},
		"relative dir":       {Config{Spill: SpillConfig{Dir: "results"}}, "absolute path"},
		"home dir":           {Config{Spill: SpillConfig{Dir: "~/r"}}, ""},
	} {
		err := tc.cfg.Validate()
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: error %v, want %q", name, err, tc.wantErr)
		}
	}
}

func TestConfig_BudgetFor(t *testing.T) {
	cfg := &Config{
		MaxTokens: intp(2000),
		Tools: []ToolRule{
			{Match: "db.*", Passthrough: true},
			{Match: "fs.read_file", MaxTokens: intp(8000)},
			{Match: "fs.*", MaxTokens: intp(0)},
			{Match: "github.*"},
		},
	}
	for id, want := range map[string]Budget{
		"db.query":        {Passthrough: true},
		"fs.read_file":    {MaxTokens: 8000},
		"fs.list":         {MaxTokens: 0},
		"github.get_me":   {MaxTokens: 2000},
		"slack.post":      {MaxTokens: 2000},
		"unknown_no_dots": {MaxTokens: 2000},
	} {
		if got := cfg.BudgetFor(id); got != want {
			t.Errorf("BudgetFor(%s) = %+v, want %+v", id, got, want)
		}
	}
	if !(Budget{MaxTokens: 1}).Limited() || (Budget{}).Limited() || (Budget{MaxTokens: 5, Passthrough: true}).Limited() {
		t.Error("Limited")
	}
	var nilCfg *Config
	if nilCfg.BudgetFor("x.y").MaxTokens != DefaultMaxTokens {
		t.Error("nil config budget")
	}
}

func TestConfig_Defaults(t *testing.T) {
	var c *Config
	if c.TTLValue() != DefaultTTL || c.MaxBytesValue() != DefaultMaxBytes || c.GlobalMaxTokens() != DefaultMaxTokens {
		t.Error("defaults")
	}
	c = &Config{Spill: SpillConfig{TTL: "5m", MaxBytes: 42, Dir: "~/x/y"}}
	if c.TTLValue() != 5*time.Minute || c.MaxBytesValue() != 42 || c.DirValue("/home/u") != "/home/u/x/y" {
		t.Error("explicit values")
	}
	if (&Config{}).DirValue("/h") != "/h/.leanproxy/results" {
		t.Error("default dir")
	}
	if DefaultEnabled {
		t.Error("the governor must be off by default in v1.0-rc1")
	}
}
