package injection

import (
	"fmt"
	"log/slog"
	"regexp"
	"sync/atomic"
)

type InjectionPattern struct {
	Name        string
	Pattern     *regexp.Regexp
	Weight      int
	Enabled     atomic.Bool
	Description string
}

type PatternDef struct {
	Name        string `yaml:"name"`
	Pattern     string `yaml:"pattern"`
	Weight      int    `yaml:"weight"`
	Enabled     bool   `yaml:"enabled"`
	Description string `yaml:"description"`
}

type PatternConfig struct {
	CustomPatterns []PatternDef `yaml:"custom_patterns"`
}

func (p PatternDef) Compile() (*InjectionPattern, error) {
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", p.Name, err)
	}
	weight := p.Weight
	if weight < 0 {
		slog.Warn("injection: negative weight defaulting to 50",
			"name", p.Name,
			"weight", weight)
		weight = 50
	} else if weight == 0 {
		slog.Debug("injection: zero-weight pattern will not contribute to risk score",
			"name", p.Name)
	}
	ip := &InjectionPattern{
		Name:        p.Name,
		Pattern:     re,
		Weight:      weight,
		Description: p.Description,
	}
	ip.Enabled.Store(p.Enabled)
	return ip, nil
}

var DefaultPatternDefs = []PatternDef{
	{
		Name:        "ignore-previous-instructions",
		Pattern:     `(?i)(ignore|disregard|forget|override)\s+(all\s+)?(previous|prior|above)\s+(instructions|commands|directions|prompts)`,
		Weight:      90,
		Enabled:     true,
		Description: "Attempts to override system instructions",
	},
	{
		Name:        "new-instruction-override",
		Pattern:     `(?i)(here\s+are\s+(your\s+)?new\s+(instructions|prompt)|you\s+(are\s+)?now\s+|you\s+will\s+(now\s+)?act\s+as|your\s+(new\s+)?(role|mission|task)\s+(is|will\s+be))`,
		Weight:      85,
		Enabled:     true,
		Description: "Attempts to redefine the assistant role",
	},
	{
		Name:        "system-prompt-extraction",
		Pattern:     `(?i)(output|print|display|show|reveal|leak|dump)\s+((your|all)\s+)?(system\s+)?(prompt|instructions|commands|directives|initial\s+prompt)`,
		Weight:      80,
		Enabled:     true,
		Description: "Attempts to extract the system prompt",
	},
	{
		Name:        "dan-jailbreak",
		Pattern:     `(?i)(dan\s*(\d+(\.\d+)?)?|do\s+anything\s+now|jail\s*(break|free)|unfiltered\s+mode|no\s+(rules|restrictions|limits|boundaries|filter))`,
		Weight:      75,
		Enabled:     true,
		Description: "Jailbreak and DAN-style attacks",
	},
	{
		Name:        "role-impersonation",
		Pattern:     `(?i)act\s+as\s+(if\s+you\s+are|though\s+you\s+are|an?\s+(ai\s+)?(without|with(out)?\s+(no\s+)?)?(restrictions|limits|filter|rules|boundaries|ethics|safety|guidelines))`,
		Weight:      70,
		Enabled:     true,
		Description: "Role impersonation and boundary removal attempts",
	},
	{
		Name:        "repeat-everything",
		Pattern:     `(?i)(repeat|echo|mirror|copy)\s+(everything|this\s+entire|the\s+(full|complete|entire)\s+(prompt|text|message|conversation|chat))`,
		Weight:      70,
		Enabled:     true,
		Description: "Attempts to make the model repeat the entire conversation",
	},
	{
		Name:        "token-smuggling",
		Pattern:     `(?i)(base64|hex|binary|rot13|rot47|cipher|encod(?:e|ed))\s*(?:convert|transform|decode|interpret|treat|process)\s*(?:the\s+)?(following|above|below|text|content|payload)`,
		Weight:      65,
		Enabled:     true,
		Description: "Token smuggling via encoded payloads",
	},
	{
		Name:        "forget-everything",
		Pattern:     `(?i)(forget|erase|clear|reset|remove)\s+(everything(\s+you\s+(know|have))?|(all\s+)?(your\s+)?(memory|training|knowledge|context|instructions)|this\s+(conversation|session|chat))`,
		Weight:      75,
		Enabled:     true,
		Description: "Attempts to reset model context",
	},
	{
		Name:        "inject-command",
		Pattern:     `(?i)(new\s+(prompt|command|instruction)|prompt\s+(injection|override|hack)|injected|malicious\s+(prompt|input))`,
		Weight:      80,
		Enabled:     true,
		Description: "Explicit prompt injection markers",
	},
	{
		Name:        "important-override",
		Pattern:     `(?i)^(important|critical|urgent|imperative|mandatory)\s*(:|\n)`,
		Weight:      30,
		Enabled:     true,
		Description: "Urgency-based override attempts",
	},
	{
		Name:        "roleplay-context-switch",
		Pattern:     `(?i)(let\s+us\s+roleplay|we\s+are\s+(now\s+)?playing|imagine\s+(you\s+are|we\s+are)|pretend\s+(you\s+are|that)|from\s+now\s+on\s+(you\s+are|i\s+want))`,
		Weight:      40,
		Enabled:     true,
		Description: "Context-switching roleplay attempts",
	},
	{
		Name:        "hypothetical-override",
		Pattern:     `(?i)(in\s+this\s+(hypothetical|thought\s+experiment|fictional\s+scenario|simulation)|for\s+(the\s+)?purpose\s+of\s+this\s+(exercise|scenario|simulation))`,
		Weight:      25,
		Enabled:     true,
		Description: "Hypothetical scenario overrides",
	},
	{
		Name:        "ignore-above",
		Pattern:     `(?i)(ignore|disregard|skip|forget)\s+(everything\s+)?(above|below|the\s+(previous|above|following))`,
		Weight:      50,
		Enabled:     true,
		Description: "Selective instruction ignoring",
	},
	{
		Name:        "separator-injection",
		Pattern:     `(?m)^[-=*]{3,}\n*(?i)(ignore|new\s+instructions|override|system|user\s+said)`,
		Weight:      85,
		Enabled:     true,
		Description: "Delimiter-based instruction injection",
	},
}

var defaultPatterns []*InjectionPattern

func init() {
	defaultPatterns = make([]*InjectionPattern, 0, len(DefaultPatternDefs))
	for _, def := range DefaultPatternDefs {
		p, err := def.Compile()
		if err != nil {
			panic(fmt.Sprintf("injection: failed to compile default pattern %q: %v", def.Name, err))
		}
		defaultPatterns = append(defaultPatterns, p)
	}
}
