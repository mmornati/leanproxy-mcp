package injection

import (
	"fmt"
	"log/slog"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync/atomic"
)

type InjectionPattern struct {
	Name        string
	Pattern     *regexp.Regexp
	Weight      int
	Enabled     atomic.Bool
	Description string
	// Triggers are lower-case literals one of which every match contains;
	// empty means "always run the regex".
	Triggers []string
	// Requires are lower-case literals one of which every match also
	// contains; a window without any is skipped (empty: no second check).
	Requires [][]byte
	// window is the longest input the regexp engine still matches with its
	// bounded backtracker for this pattern (see eachWindow).
	window int
}

// defaultWindow is the window used when a pattern's program size is not
// known.
const defaultWindow = 512

// backtrackWindow returns the input length under which Go's regexp uses its
// bit-state backtracker for pattern (256 Kib of state / program size),
// between 256 bytes (a window always holds a phrase) and maxWindow.
func backtrackWindow(pattern string) int {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return defaultWindow
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil || len(prog.Inst) == 0 {
		return defaultWindow
	}
	w := 256*1024/len(prog.Inst) - 8
	switch {
	case w < 256:
		w = 256
	case w > maxWindow:
		w = maxWindow
	}
	return w
}

// maxWindow caps a window: the patterns match phrases of a few dozen words,
// and the backtracker's cost grows with the window.
const maxWindow = 768

type PatternDef struct {
	Name        string `yaml:"name"`
	Pattern     string `yaml:"pattern"`
	Weight      int    `yaml:"weight"`
	Enabled     bool   `yaml:"enabled"`
	Description string `yaml:"description"`
	// Triggers is an optional prefilter: lower-case literal substrings one
	// of which every match of Pattern contains (in normalized text). When
	// none is present the regex is skipped. Leave it empty to always run
	// the regex.
	Triggers []string `yaml:"triggers,omitempty"`
	// Requires is an optional second prefilter: lower-case literals one of
	// which every match also contains. The regex skips a window around a
	// trigger that holds none of them.
	Requires []string `yaml:"requires,omitempty"`
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
		Triggers:    lowerAll(p.Triggers),
		Requires:    literals(lowerAll(p.Requires)),
		window:      backtrackWindow(p.Pattern),
	}
	ip.Enabled.Store(p.Enabled)
	return ip, nil
}

// DefaultPatternDefs is the built-in pattern set. Patterns are matched
// against normalized text (see Normalize: lower-cased, escapes decoded,
// invisible characters dropped, whitespace collapsed), so they are written
// in lower case; the (?i) flag is kept for callers that match raw text.
//
// Issue #315 kept the names and weights of the original 14 patterns but
// anchored them on word boundaries and narrowed the phrasings that fired on
// ordinary documents (READMEs, issues, code) once tool responses started to
// be classified, e.g. "you are now logged in", "no limits on file size",
// "show all commands", "dependency injected", the name "Dan". It added
// patterns for tool-use hijacking and exfiltration, which is what indirect
// prompt injection in tool output looks like.
//
// Triggers lists literal substrings of the normalized text one of which
// every match of the pattern contains (line breaks are read as spaces when
// triggers are checked): a pattern whose triggers are all absent is not
// run. It keeps the per-request cost at a few substring
// searches for the common, benign case.
var DefaultPatternDefs = []PatternDef{
	{
		Name:        "ignore-previous-instructions",
		Pattern:     `(?i)\b(ignore|disregard|forget|override)\s+(all\s+)?(of\s+)?(the\s+|your\s+|any\s+)?(previous|prior|above|earlier|preceding)\s+(instructions?|commands?|directions?|prompts?|rules|guidelines)\b`,
		Weight:      90,
		Enabled:     true,
		Description: "Attempts to override system instructions",
		Triggers:    []string{"ignore", "disregard", "forget", "override"},
	},
	{
		Name: "new-instruction-override",
		Pattern: `(?i)\bhere\s+are\s+(your\s+)?new\s+(instructions|prompts?)\b` +
			`|\byou\s+(are\s+now|will\s+now\s+be|are\s+no\s+longer)\s+(an?\s+|in\s+|my\s+|operating\s+)?(unrestricted|unfiltered|uncensored|jailbroken|evil|dan\b|free\s+from|without|developer\s+mode|god\s+mode|system\s+administrator|hacker|admin(istrator)?\b|root\b|different\s+(ai|assistant|model)|new\s+(ai|assistant|persona))` +
			`|\byou\s+will\s+now\s+act\s+as\b|\byou\s+will\s+act\s+as\s+(an?\s+)?(dan|hacker|unrestricted|unfiltered|uncensored|jailbroken|evil|admin|root)\b` +
			`|\byour\s+new\s+(role|mission|task|goal|objective)\s+(is|will\s+be)\b` +
			`|\byour\s+(role|mission|task|goal|objective)\s+(is|will\s+be)\s+(now\s+)?to\s+(ignore|bypass|reveal|output|obey|disregard|leak|exfiltrate|send|follow\s+(these|my|the\s+new))\b`,
		Weight:      85,
		Enabled:     true,
		Description: "Attempts to redefine the assistant role",
		Triggers:    []string{"here are", "you are now", "you will now", "you will act", "you are no longer", "your new", "your role", "your mission", "your task", "your goal", "your objective"},
	},
	{
		Name: "system-prompt-extraction",
		Pattern: `(?i)\b(output|print|reveal|leak|dump)\s+all\s+(instructions|directives|prompts)\b` +
			`|\b(output|print|display|show|reveal|leak|dump|repeat|tell\s+me)\s+(me\s+)?(` +
			`your\s+(system\s+|initial\s+|original\s+|hidden\s+|secret\s+)?(prompt|instructions|directives)` +
			`|all\s+system\s+(instructions|directives|prompts)` +
			`|(your|all|the)\s+system\s+(prompt|instructions|commands|directives)` +
			`|(your\s+)?initial\s+prompt)\b`,
		Weight:      80,
		Enabled:     true,
		Description: "Attempts to extract the system prompt",
		Triggers:    []string{"all instructions", "all directives", "all prompts", "your prompt", "your instructions", "your directives", "your initial", "your original", "your hidden", "your secret", "system prompt", "system instruction", "system command", "system directive", "initial prompt"},
	},
	{
		Name: "dan-jailbreak",
		Pattern: `(?i)\bdan\s*(\d+(\.\d+)?|mode)\b|\b(a|an|as)\s+dan\b|\bdo\s+anything\s+now\b|\bjail\s*(break|broken)\s+(mode|prompt|the\s+(ai|model|system|assistant)|yourself|enabled|activated)\b|\bjail\s*free\s+mode\b|\bunfiltered\s+mode\b|\bno\s+(rules|restrictions|limits|filters?)\s+mode\b` +
			`|\bno\s+(rules|restrictions|limits|boundaries|filters?)\s+(apply|on\s+this\s+(response|conversation|chat)|for\s+this\s+(interaction|conversation|chat|response)|needs?\s+to\s+apply)`,
		Weight:      75,
		Enabled:     true,
		Description: "Jailbreak and DAN-style attacks",
		Triggers:    []string{"dan ", "dan1", "dan2", "dan3", "dan4", "dan5", "dan6", "dan7", "dan8", "dan9", "a dan", "an dan", "as dan", "anything now", "jailbreak", "jail break", "jailbroken", "jail broken", "jail free", "jailfree", "unfiltered mode", "no rules", "no restrictions", "no limits", "no boundaries", "no filter"},
	},
	{
		Name:        "role-impersonation",
		Pattern:     `(?i)\bact\s+as\s+((if|though)\s+you\s+(are|have|had|were)\b|an?\s+(ai\s+)?(without|with(out)?\s+(no\s+)?)?(restrictions|limits|filter|rules|boundaries|ethics|safety|guidelines))`,
		Weight:      70,
		Enabled:     true,
		Description: "Role impersonation and boundary removal attempts",
		Triggers:    []string{"act as"},
	},
	{
		Name: "repeat-everything",
		Pattern: `(?i)\b(repeat|echo|mirror)\s+(back\s+)?everything\s+(you\s+(know|were\s+told|have\s+been\s+told|see)|i\s+say|from\s+(the\s+start|above|before)|above)\b` +
			`|\b(repeat|echo|mirror|copy)\s+(back\s+)?(this\s+entire|the\s+(full|complete|entire))\s+((prompt|conversation|chat|context)\b|(text|message)\s+(above|back|so\s+far|verbatim|from)\b)`,
		Weight:      70,
		Enabled:     true,
		Description: "Attempts to make the model repeat the entire conversation",
		Triggers:    []string{"everything", "this entire", "the full", "the complete", "the entire"},
	},
	{
		Name:        "token-smuggling",
		Pattern:     `(?i)\b(base64|hex|binary|rot13|rot47|cipher|encod(?:e|ed))\s*(?:convert|transform|decode|interpret|treat|process)\s*(?:the\s+)?(following|above|below|text|content|payload)`,
		Weight:      65,
		Enabled:     true,
		Description: "Token smuggling via encoded payloads",
		Triggers:    []string{"base64", "hex", "binary", "rot13", "rot47", "cipher", "encode"},
	},
	{
		Name: "forget-everything",
		Pattern: `(?im)\b(forget|erase)\s+everything(\s+(you\s+(know|have\s+learned|were\s+told)|i\s+(said|told\s+you)))?\s*([.,;:!]|$|\s(and|above|before|so\s+far|now|then)\b)` +
			`|\b(forget|erase|clear|reset|remove|wipe)\s+(all\s+)?(of\s+)?your\s+(memory|training|knowledge|context|instructions)` +
			`|\breset\s+this\s+(conversation|chat)\b`,
		Weight:      75,
		Enabled:     true,
		Description: "Attempts to reset model context",
		Triggers:    []string{"everything", "your memory", "your training", "your knowledge", "your context", "your instructions", "this conversation", "this chat"},
	},
	{
		Name: "inject-command",
		Pattern: `(?i)\bnew\s+(system\s+)?(prompt|command|instructions?)\s*:` +
			`|\bnew\s+(prompt|instructions?)\s+(for\s+(you|the\s+(ai|assistant|model|agent))|below|follow)` +
			`|\bas\s+new\s+(instructions|commands)\b` +
			`|\bprompt\s+(override|hack)\b|\bprompt\s+injection\s+(attempt|successful|payload)` +
			`|\binjected\s+(command|prompt|instruction)s?\b|\bmalicious\s+(prompt|input)\b`,
		Weight:      80,
		Enabled:     true,
		Description: "Explicit prompt injection markers",
		Triggers:    []string{"new prompt", "new command", "new instruction", "new system", "as new", "prompt override", "prompt hack", "prompt injection", "injected", "malicious"},
	},
	{
		Name:        "important-override",
		Pattern:     `(?im)^(important|critical|urgent|imperative|mandatory)\s*(:|\n)`,
		Weight:      30,
		Enabled:     true,
		Description: "Urgency-based override attempts",
		Triggers:    []string{"important", "critical", "urgent", "imperative", "mandatory"},
	},
	{
		Name:        "roleplay-context-switch",
		Pattern:     `(?i)\b(let\s+us\s+roleplay|we\s+are\s+(now\s+)?playing|imagine\s+(you\s+are|we\s+are)|pretend\s+(you\s+are|that)|from\s+now\s+on\s+(you\s+are|i\s+want))`,
		Weight:      40,
		Enabled:     true,
		Description: "Context-switching roleplay attempts",
		Triggers:    []string{"roleplay", "playing", "imagine", "pretend", "from now on"},
	},
	{
		Name:        "hypothetical-override",
		Pattern:     `(?i)\b(in\s+this\s+(hypothetical|thought\s+experiment|fictional\s+scenario|simulation)|for\s+(the\s+)?purpose\s+of\s+this\s+(exercise|scenario|simulation))`,
		Weight:      25,
		Enabled:     true,
		Description: "Hypothetical scenario overrides",
		Triggers:    []string{"hypothetical", "thought experiment", "fictional scenario", "simulation", "purpose of this"},
	},
	{
		Name:        "ignore-above",
		Pattern:     `(?i)\b(ignore|disregard|skip|forget)\s+(everything\s+)?(above|below|the\s+(previous|above|following))\b`,
		Weight:      50,
		Enabled:     true,
		Description: "Selective instruction ignoring",
		Triggers:    []string{"ignore", "disregard", "skip", "forget"},
	},
	{
		Name:        "separator-injection",
		Pattern:     `(?m)^[-=*]{3,}\n*(?i)(ignore|new\s+instructions|override|system|user\s+said)`,
		Weight:      85,
		Enabled:     true,
		Description: "Delimiter-based instruction injection",
		Triggers:    []string{"---", "===", "***"},
	},
	{
		Name:        "tool-call-hijack",
		Pattern:     `(?i)\b(call|invoke|execute|run|use)\s+the\s+[\w.:/-]+\s+(tool|function)\s+(to|with|and|on)\b|\b(call|invoke)\s+the\s+tool\b`,
		Weight:      45,
		Enabled:     true,
		Description: "Tells the model to call a tool (tool-use hijacking)",
		Triggers:    []string{"tool to", "tool with", "tool and", "tool on", "function to", "function with", "function and", "function on", "the tool"},
	},
	{
		Name: "ai-directive",
		Pattern: `(?i)\b(ai|assistant|model|agent|llm|chatbot)s?\b[^.\n]{0,40}?\byou\s+(must|should|need\s+to|have\s+to|are\s+required\s+to)\s+(now\s+|immediately\s+|also\s+|first\s+)?(call|invoke|execute|run|use|send|read|open|fetch|delete|write|forward|email|post|upload)\b` +
			`|\b(when|if|once|after)\s+(you|(the|an?|any)\s+(ai|assistant|model|agent|llm))\s+(read|reads|see|sees|process|processes|summari[sz]es?)\s+(this|these)\b`,
		Weight:      50,
		Enabled:     true,
		Description: "Instructions addressed to the AI reading the text",
		Triggers:    []string{"you must", "you should", "you need to", "you have to", "you are required", "read this", "reads this", "see this", "sees this", "process this", "processes this", "summari", "read these", "see these", "process these"},
	},
	{
		Name:        "exfiltrate-secrets",
		Pattern:     `(?i)\b(send|post|upload|forward|transmit|email|exfiltrate|leak)\s+(\S+\s+){0,6}?(~/\.ssh\S*|\S*id_rsa\S*|\S*id_ed25519\S*|\S*\.env\b|\S*\.aws/credentials|\S*\.netrc|private\s+keys?|api[\s_-]?keys?|access\s+tokens?|passwords?|credentials|secrets?|session\s+cookies?|ssh\s+keys?|environment\s+variables)\S*\s+(\S+\s+){0,4}?(to|at|into)\s+(https?://|ftp://|\S+@\S+\.\w|the\s+attacker|this\s+(url|address|endpoint|server|webhook))`,
		Weight:      70,
		Enabled:     true,
		Description: "Asks for secrets or files to be sent to an external destination",
		Triggers:    []string{".ssh", "id_rsa", "id_ed25519", ".env", ".aws", ".netrc", "private key", "api key", "api_key", "api-key", "apikey", "access token", "password", "secret", "credentials", "session cookie", "ssh key", "environment variables"},
		Requires:    []string{"://", "@", "the attacker", "this url", "this address", "this endpoint", "this server", "this webhook"},
	},
	{
		Name:        "send-to-url",
		Pattern:     `(?i)\b(send|upload|forward|transmit|exfiltrate)\s+(\S+\s+){0,8}?to\s+(https?|ftp)://`,
		Weight:      30,
		Enabled:     true,
		Description: "Asks for data to be sent to a URL",
		Triggers:    []string{"send", "upload", "forward", "transmit", "exfiltrate"},
		Requires:    []string{"://"},
	},
	{
		Name:        "exfiltrate-verb",
		Pattern:     `(?i)\bexfiltrat(e|es|ing)\b`,
		Weight:      40,
		Enabled:     true,
		Description: "Explicit data exfiltration wording",
		Triggers:    []string{"exfiltrat"},
	},
	{
		Name: "markdown-image-beacon",
		Pattern: `(?i)(!\[[^\]\n]{0,40}\]|\[\])\(\s*https?://[^)\s]+[?&](data|payload|secrets?|leak|exfil|history|conversation|chat|prompt|context|memory)=` +
			`|\]\(\s*https?://[^)\s]*[?&][\w-]+=(\{\s*[\w.$-]+\s*\}|\$\{[\w.-]+\}|<[\w .-]+>|%7b[\w.-]+%7d|\[[\w .-]+\])`,
		Weight:      70,
		Enabled:     true,
		Description: "Markdown image or link that would leak data through its URL",
		Triggers:    []string{"]("},
	},
	{
		Name:        "hidden-instruction-tag",
		Pattern:     `(?i)<(important|system|instructions?|admin|secret)>`,
		Weight:      50,
		Enabled:     true,
		Description: "Pseudo-tags used to smuggle instructions to the model",
		Triggers:    []string{"<important>", "<system>", "<instruction", "<admin>", "<secret>"},
	},
	{
		Name:        "chat-template-token",
		Pattern:     `(?i)<\|im_start\|>|<\|(system|assistant|user)\|>|\[/?inst\]|<<sys>>`,
		Weight:      70,
		Enabled:     true,
		Description: "Chat-template control tokens that fake a new conversation turn",
		Triggers:    []string{"<|", "[inst]", "[/inst]", "<<sys>>"},
	},
}

var defaultPatterns []*InjectionPattern

// foldFree drops the case-insensitive flag from a built-in pattern: they
// are written in lower case and run on normalized (lower-cased) text, where
// (?i) changes nothing but costs a case fold per rune.
func foldFree(pattern string) string {
	switch {
	case strings.HasPrefix(pattern, "(?i)"):
		return pattern[len("(?i)"):]
	case strings.HasPrefix(pattern, "(?im)"):
		return "(?m)" + pattern[len("(?im)"):]
	}
	return pattern
}

func init() {
	defaultPatterns = make([]*InjectionPattern, 0, len(DefaultPatternDefs))
	for _, def := range DefaultPatternDefs {
		def.Pattern = foldFree(def.Pattern)
		p, err := def.Compile()
		if err != nil {
			panic(fmt.Sprintf("injection: failed to compile default pattern %q: %v", def.Name, err))
		}
		defaultPatterns = append(defaultPatterns, p)
	}
}

func literals(in []string) [][]byte {
	if len(in) == 0 {
		return nil
	}
	out := make([][]byte, len(in))
	for i, s := range in {
		out[i] = []byte(s)
	}
	return out
}

func lowerAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.ToLower(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
