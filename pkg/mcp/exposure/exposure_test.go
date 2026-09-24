package exposure

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func boolPtr(b bool) *bool { return &b }

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"router": ModeRouter, " Passthrough ": ModePassthrough, "HYBRID": ModeHybrid} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "direct", "pass-through"} {
		if _, err := ParseMode(in); err == nil {
			t.Errorf("ParseMode(%q) accepted", in)
		}
	}
	if ModeRouter.ListsUpstreamTools() || !ModePassthrough.ListsUpstreamTools() || !ModeHybrid.ListsUpstreamTools() {
		t.Error("ListsUpstreamTools")
	}
}

func TestConfigValidate(t *testing.T) {
	var nilCfg *Config
	if err := nilCfg.Validate(); err != nil {
		t.Fatalf("nil config: %v", err)
	}
	bad := map[string]*Config{
		"exposure.mode":                          {Mode: "direct"},
		"exposure.clients[0]: match is required": {Clients: []ClientRule{{Match: " ", Mode: ModeRouter}}},
		"exposure.clients[0]: invalid match":     {Clients: []ClientRule{{Match: "[", Mode: ModeRouter}}},
		"exposure.clients[1].mode":               {Clients: []ClientRule{{Match: "a", Mode: ModeRouter}, {Match: "b", Mode: "x"}}},
		"exposure.always_load[0]: invalid glob":  {AlwaysLoad: []string{"["}},
		"exposure.always_load[0]: empty glob":    {AlwaysLoad: []string{""}},
		"exposure.max_name_length":               {MaxNameLength: 65},
	}
	for want, cfg := range bad {
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Validate(%+v) = %v; want an error containing %q", cfg, err, want)
		}
	}
	if err := (&Config{MaxNameLength: 10}).Validate(); err == nil {
		t.Error("max_name_length 10 accepted")
	}
	good := &Config{Mode: ModeHybrid, BuiltinClients: boolPtr(false), Clients: []ClientRule{{Match: "my-*", Mode: ModePassthrough}},
		AlwaysLoad: []string{"github.search_*"}, MaxNameLength: 48}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
}

func TestResolverModeFor(t *testing.T) {
	r, err := NewResolver(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]Mode{
		"claude-code":                   ModePassthrough,
		"Claude-Code":                   ModePassthrough,
		"claude-ai":                     ModePassthrough,
		"cursor-vscode":                 ModePassthrough,
		"Visual Studio Code":            ModePassthrough,
		"Visual Studio Code - Insiders": ModePassthrough,
		"":                              ModeRouter,
		"leanproxy-harness":             ModeRouter,
		"claude-code-fork":              ModeRouter,
		"zed":                           ModeRouter,
	}
	for name, want := range cases {
		if got, _ := r.ModeFor(name); got != want {
			t.Errorf("default ModeFor(%q) = %q, want %q", name, got, want)
		}
	}
	if !r.MayListUpstreamTools() {
		t.Error("the built-in table lists upstream tools for some clients")
	}

	// Configured rules come first; the fallback applies to the rest.
	r, err = NewResolver(&Config{Mode: ModeHybrid, Clients: []ClientRule{{Match: "claude-code", Mode: ModeRouter}, {Match: "my-agent*", Mode: ModePassthrough}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]Mode{"claude-code": ModeRouter, "My-Agent 2": ModePassthrough, "cursor-vscode": ModePassthrough, "other": ModeHybrid} {
		if got, why := r.ModeFor(name); got != want {
			t.Errorf("configured ModeFor(%q) = %q (%s), want %q", name, got, why, want)
		}
	}

	// builtin_clients: false drops the table.
	r, _ = NewResolver(&Config{BuiltinClients: boolPtr(false)}, "")
	if got, _ := r.ModeFor("claude-code"); got != ModeRouter || r.MayListUpstreamTools() {
		t.Errorf("builtin_clients false: claude-code = %q, may list = %v", got, r.MayListUpstreamTools())
	}

	// --exposure wins over everything.
	r, _ = NewResolver(&Config{Clients: []ClientRule{{Match: "*", Mode: ModePassthrough}}}, ModeRouter)
	if got, why := r.ModeFor("claude-code"); got != ModeRouter || why != "--exposure" || r.MayListUpstreamTools() {
		t.Errorf("forced router: %q (%s)", got, why)
	}
	if _, err := NewResolver(nil, "bogus"); err == nil {
		t.Error("invalid forced mode accepted")
	}
	if _, err := NewResolver(&Config{Mode: "bogus"}, ""); err == nil {
		t.Error("invalid config accepted")
	}

	var none *Resolver
	if got, _ := none.ModeFor("claude-code"); got != ModeRouter || none.MayListUpstreamTools() || none.AlwaysLoad("a", "b") || none.MaxNameLength() != 64 {
		t.Error("a nil resolver is router for everyone")
	}
}

func TestResolverAlwaysLoadAndNameLength(t *testing.T) {
	r, _ := NewResolver(&Config{AlwaysLoad: []string{"github.search_*", "slack.post"}, MaxNameLength: 40}, "")
	for id, want := range map[[2]string]bool{{"github", "search_code"}: true, {"slack", "post"}: true, {"slack", "post2"}: false, {"github", "create"}: false} {
		if got := r.AlwaysLoad(id[0], id[1]); got != want {
			t.Errorf("AlwaysLoad(%v) = %v", id, got)
		}
	}
	if r.MaxNameLength() != 40 {
		t.Errorf("MaxNameLength = %d", r.MaxNameLength())
	}
}

func TestToolName(t *testing.T) {
	if got := ToolName("github", "create_issue", 64); got != "github__create_issue" {
		t.Fatalf("plain name = %q", got)
	}
	if s, tool, ok := SplitPlain("github__create_issue"); !ok || s != "github" || tool != "create_issue" {
		t.Fatalf("SplitPlain = %q %q %v", s, tool, ok)
	}
	if _, _, ok := SplitPlain("github_create"); ok {
		t.Fatal("SplitPlain without separator")
	}

	long := strings.Repeat("very_long_tool_name_", 5)
	cases := []struct{ server, tool string }{
		{"github", long},
		{"my.server", "tool"},                  // a '.' is not a valid name character
		{"srv", "tool with spaces/and:colons"}, // idem
		{"a", "b__c"},                          // a "__" in a part would make the split ambiguous
		{"a__b", "c"},
		{"srv", "outil_évènement"}, // multi-byte characters
		{"", "x"},
	}
	seen := map[string]string{}
	for _, c := range cases {
		name := ToolName(c.server, c.tool, 64)
		if !validName.MatchString(name) {
			t.Errorf("ToolName(%q, %q) = %q: not ^[a-zA-Z0-9_-]{1,64}$", c.server, c.tool, name)
		}
		if !Hashed(name) {
			t.Errorf("ToolName(%q, %q) = %q: expected the hash suffix", c.server, c.tool, name)
		}
		if again := ToolName(c.server, c.tool, 64); again != name {
			t.Errorf("not deterministic: %q vs %q", name, again)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("collision: %q for %v and %s", name, c, prev)
		}
		seen[name] = fmt.Sprint(c)
	}
	if ToolName("a", "b__c", 64) == ToolName("a__b", "c", 64) {
		t.Error("ambiguous split not disambiguated")
	}
	// The kept prefix stays readable.
	if name := ToolName("github", long, 64); !strings.HasPrefix(name, "github__very_long_tool_name_") || len(name) != 64 {
		t.Errorf("shortened name %q", name)
	}
	// A lower cap, and an out-of-range cap taken as 64.
	if name := ToolName("github", long, 32); len(name) != 32 || !validName.MatchString(name) {
		t.Errorf("cap 32: %q", name)
	}
	if name := ToolName("github", long, 1000); len(name) != 64 {
		t.Errorf("cap 1000: %q", name)
	}
	if ToolName("github", "create_issue", 16) != "github__create_issue" {
		t.Error("an out-of-range cap (16) is taken as 64")
	}
	if Hashed("github__create_issue") || Hashed("_0123456789") || !Hashed("x_0123456789") || Hashed("x_012345678G") {
		t.Error("Hashed shape")
	}
	if !ValidName("a-b_c", 64) || ValidName("a.b", 64) || ValidName("", 64) || ValidName("abc", 2) {
		t.Error("ValidName")
	}
}

// TestToolNameProperty: whatever the server and tool names, the namespaced
// name is valid, at most maxLen, and distinct inputs give distinct names.
func TestToolNameProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(322)) // #nosec G404 -- deterministic test input
	alphabet := []rune("abcXYZ019_-.: /é__")
	word := func(n int) string {
		r := make([]rune, n)
		for i := range r {
			r[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(r)
	}
	names := map[string][2]string{}
	for i := 0; i < 5000; i++ {
		server, tool := word(1+rng.Intn(20)), word(1+rng.Intn(90))
		maxLen := MinMaxNameLength + rng.Intn(DefaultMaxNameLength-MinMaxNameLength+1)
		name := ToolName(server, tool, maxLen)
		if !ValidName(name, maxLen) {
			t.Fatalf("ToolName(%q, %q, %d) = %q", server, tool, maxLen, name)
		}
		if maxLen != DefaultMaxNameLength {
			continue
		}
		if prev, ok := names[name]; ok && prev != [2]string{server, tool} {
			t.Fatalf("collision %q: %v and %q/%q", name, prev, server, tool)
		}
		names[name] = [2]string{server, tool}
		if !Hashed(name) {
			if s, tl, ok := SplitPlain(name); !ok || s != server || tl != tool {
				t.Fatalf("plain name %q does not split back to %q/%q", name, server, tool)
			}
		}
	}
}
