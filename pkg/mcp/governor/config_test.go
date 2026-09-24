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
		"defaults":                 {Config{Enabled: true}, ""},
		"zero budget":              {Config{MaxTokens: intp(0)}, ""},
		"negative budget":          {Config{MaxTokens: intp(-1)}, "must be >= 0"},
		"tiny budget":              {Config{MaxTokens: intp(10)}, "or >= 100"},
		"rule without match":       {Config{Tools: []ToolRule{{MaxTokens: intp(500)}}}, "match is required"},
		"bad glob":                 {Config{Tools: []ToolRule{{Match: "a[", MaxTokens: intp(500)}}}, "invalid match glob"},
		"rule budget":              {Config{Tools: []ToolRule{{Match: "a.*", MaxTokens: intp(5)}}}, "tools[0].max_tokens"},
		"both":                     {Config{Tools: []ToolRule{{Match: "a.*", MaxTokens: intp(500), Passthrough: true}}}, "mutually exclusive"},
		"bad ttl":                  {Config{Spill: SpillConfig{TTL: "soon"}}, "spill.ttl"},
		"zero ttl":                 {Config{Spill: SpillConfig{TTL: "0s"}}, "must be > 0"},
		"negative bytes":           {Config{Spill: SpillConfig{MaxBytes: -1}}, "max_bytes"},
		"relative dir":             {Config{Spill: SpillConfig{Dir: "results"}}, "absolute path"},
		"home dir":                 {Config{Spill: SpillConfig{Dir: "~/r"}}, ""},
		"projection ok":            {Config{Projections: []ProjectionRule{{Match: "gh.*", Drop: []string{"**.url"}}}}, ""},
		"projection exempt":        {Config{Projections: []ProjectionRule{{Match: "gh.get_me"}}}, ""},
		"projection match":         {Config{Projections: []ProjectionRule{{Drop: []string{"a"}}}}, "projections[0]: match is required"},
		"projection glob":          {Config{Projections: []ProjectionRule{{Match: "a[", Drop: []string{"a"}}}}, "invalid match glob"},
		"keep and drop":            {Config{Projections: []ProjectionRule{{Match: "a.*", Keep: []string{"a"}, Drop: []string{"b"}}}}, "mutually exclusive"},
		"bad path":                 {Config{Projections: []ProjectionRule{{Match: "a.*", Keep: []string{"items[0]"}}}}, "projections[0]: path"},
		"drop ends in []":          {Config{Projections: []ProjectionRule{{Match: "a.*", Drop: []string{"items[]"}}}}, "must end with a key name"},
		"dedup off":                {Config{Dedup: "off"}, ""},
		"dedup on":                 {Config{Dedup: "ON"}, ""},
		"dedup empty":              {Config{Dedup: ""}, ""},
		"dedup bad":                {Config{Dedup: "sometimes"}, "response.dedup must be"},
		"summarize off":            {Config{Summarize: &SummarizeConfig{Enabled: false}}, ""},
		"summarize no tools":       {Config{Summarize: &SummarizeConfig{Enabled: true, URL: "http://127.0.0.1:11434"}}, "tools is required"},
		"summarize ok":             {Config{Summarize: &SummarizeConfig{Enabled: true, URL: "http://127.0.0.1:11434", Tools: []string{"fs.*"}}}, ""},
		"summarize bad provider":   {Config{Summarize: &SummarizeConfig{Enabled: true, Provider: "openai", Tools: []string{"fs.*"}}}, "only \"ollama\" is supported"},
		"summarize bad tool glob":  {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a["}}}, "invalid glob"},
		"summarize empty tool":     {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{""}}}, "is empty"},
		"summarize bad threshold":  {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a.*"}, ThresholdTokens: intp(0)}}, "threshold_tokens must be > 0"},
		"summarize bad max":        {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a.*"}, MaxSummaryTokens: intp(-1)}}, "max_summary_tokens must be > 0"},
		"summarize bad timeout":    {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a.*"}, Timeout: "nope"}}, "timeout"},
		"summarize zero timeout":   {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a.*"}, Timeout: "0s"}}, "timeout must be > 0"},
		"summarize remote refused": {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a.*"}, URL: "http://example.com:11434"}}, "loopback address"},
		"summarize remote allowed": {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a.*"}, URL: "http://example.com:11434", AllowRemote: true}}, ""},
		"summarize bad url":        {Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"a.*"}, URL: "://bad"}}, "invalid URL"},
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

func TestConfig_DedupSummarizeDefaults(t *testing.T) {
	var nilCfg *Config
	if nilCfg.DedupEnabled() || nilCfg.SummarizeEnabled() {
		t.Error("nil config: dedup and summarize must default off")
	}
	if (&Config{}).DedupEnabled() {
		t.Error("dedup must default off")
	}
	if !(&Config{Dedup: "on"}).DedupEnabled() {
		t.Error("dedup: on")
	}
	c := &Config{Summarize: &SummarizeConfig{Enabled: true, Tools: []string{"fs.*", "github.list_*"}}}
	if !c.SummarizeEnabled() {
		t.Error("summarize enabled")
	}
	if c.ThresholdTokensValue() != DefaultSummarizeThresholdTokens {
		t.Error("threshold default")
	}
	if c.MaxSummaryTokensValue() != DefaultSummarizeMaxTokens {
		t.Error("max summary default")
	}
	if c.SummarizeTimeoutValue() != DefaultSummarizeTimeout {
		t.Error("timeout default")
	}
	if c.Summarize.ProviderValue() != DefaultSummarizeProvider {
		t.Error("provider default")
	}
	if c.Summarize.URLValue() != DefaultSummarizeURL {
		t.Error("url default")
	}
	for id, want := range map[string]bool{
		"fs.read_file":      true,
		"github.list_repos": true,
		"github.get_repo":   false,
		"other.tool":        false,
	} {
		if got := c.MatchesSummarizeTool(id); got != want {
			t.Errorf("MatchesSummarizeTool(%s) = %v, want %v", id, got, want)
		}
	}
	off := &Config{}
	if off.MatchesSummarizeTool("fs.read_file") {
		t.Error("summarize off: nothing matches")
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
