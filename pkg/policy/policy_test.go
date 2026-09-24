package policy

import (
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

func bptr(b bool) *bool { return &b }

func TestGlob(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"github.create_issue", "github.create_issue", true},
		{"github.create_issue", "github.create_issues", false},
		{"github.create_issue", "GitHub.create_issue", false}, // case-sensitive
		{"*", "github.create_issue", true},
		{"**", "x.y", true},
		{"github.*", "github.create_issue", true},
		{"github.*", "github.", true},
		{"github.*", "githubx.create_issue", false},
		{"github.delete_*", "github.delete_file", true},
		{"github.delete_*", "github.create_file", false},
		{"*.delete_*", "gitlab.delete_branch", true},
		{"*.delete_*", "gitlab.undelete_branch", false},
		{"*delete*", "gitlab.undelete_branch", true},
		{"*_file", "fs.read_file", true},
		{"*_file", "fs.read_files", false},
		{"a*b*c", "abc", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "acb", false},
		{"ab*ba", "aba", false}, // prefix and suffix must not overlap
		{"pg.pg_?xecute", "pg.pg_execute", true},
		{"pg.pg_?xecute", "pg.pg_xecute", false},
		{"?.x", "é.x", true}, // ? is one character, not one byte
		{"*.?", "srv.ab", false},
		{"s*.t?ol", "srv.tool", true},
		{"s*.t?ol", "srv.tooool", false},
		{"*a?", "bbbaxa", false},
		{"*a?", "bbbaxab", true},
		{"*a?", "bbbaxba", false},
		{"*a?", "bbbax", true},
	}
	for _, tt := range tests {
		g := compileGlob(tt.pattern)
		if got := g.match(tt.s); got != tt.want {
			t.Errorf("glob %q on %q = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
		// Cross-check with path.Match for names without '/' (whose '*'
		// then has the same meaning).
		if !strings.Contains(tt.s, "/") {
			if want, err := path.Match(tt.pattern, tt.s); err == nil && want != tt.want {
				t.Errorf("glob %q on %q disagrees with path.Match (%v)", tt.pattern, tt.s, want)
			}
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string // "" = valid
	}{
		{"nil", nil, ""},
		{"empty", &Config{}, ""},
		{"full", &Config{Default: "Deny", UnknownTools: "allow", ConfirmTimeout: "30s", Rules: []Rule{
			{Match: "a.b", Action: "ALLOW"},
			{Match: "*", Annotations: map[string]bool{"destructiveHint": true}, Action: "confirm"},
		}}, ""},
		{"bad default", &Config{Default: "confirm"}, "policy.default"},
		{"bad unknown", &Config{UnknownTools: "confirm"}, "policy.unknown_tools"},
		{"bad timeout", &Config{ConfirmTimeout: "soon"}, "policy.confirm_timeout"},
		{"zero timeout", &Config{ConfirmTimeout: "0s"}, "policy.confirm_timeout must be > 0"},
		{"no match", &Config{Rules: []Rule{{Action: "deny"}}}, "policy.rules[0]: match is required"},
		{"class glob", &Config{Rules: []Rule{{Match: "a.[bc]", Action: "deny"}}}, "only the * and ? wildcards"},
		{"no action", &Config{Rules: []Rule{{Match: "a.b"}}}, "action is required"},
		{"bad action", &Config{Rules: []Rule{{Match: "a.b", Action: "block"}}}, `action "block" is not one of`},
		{"bad hint", &Config{Rules: []Rule{{Match: "*", Action: "deny", Annotations: map[string]bool{"dangerous": true}}}}, `unknown hint "dangerous"`},
		{"empty injection", &Config{Rules: []Rule{{Match: "*", Action: "allow", Injection: &InjectionOverride{}}}}, "set request_policies"},
		{"bad injection action", &Config{Rules: []Rule{{Match: "*", Action: "allow", Injection: &InjectionOverride{
			ResponsePolicies: []injection.Rule{{MinRisk: 1, MaxRisk: 100, Action: injection.ActionQuarantine}},
		}}}}, "policy.rules[0]: injection: response_policies[0]"},
		{"good injection", &Config{Rules: []Rule{{Match: "*", Action: "allow", Injection: &InjectionOverride{
			RequestPolicies: []injection.Rule{{MinRisk: 1, MaxRisk: 100, Action: injection.ActionBlock}},
		}}}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if _, err := Compile(tt.cfg); err != nil {
					t.Fatalf("compile: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %v, want it to contain %q", err, tt.want)
			}
			if _, cerr := Compile(tt.cfg); cerr == nil {
				t.Fatal("Compile accepted an invalid config")
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	e := MustCompile(nil)
	if e.Default() != ActionAllow || e.UnknownTools() != ActionDeny || e.ConfirmTimeout() != DefaultConfirmTimeout {
		t.Fatalf("defaults: %s %s %s", e.Default(), e.UnknownTools(), e.ConfirmTimeout())
	}
	if d := e.Evaluate("any", "tool", Hints{}); d.Action != ActionAllow || d.Rule != RuleDefault || d.Label != "policy.default" {
		t.Fatalf("default decision: %+v", d)
	}
	if d := e.UnknownDecision(); d.Action != ActionDeny || d.Rule != RuleUnknownTools {
		t.Fatalf("unknown decision: %+v", d)
	}
	if e := MustCompile(&Config{ConfirmTimeout: "45s"}); e.ConfirmTimeout() != 45*time.Second {
		t.Fatalf("confirm_timeout: %s", e.ConfirmTimeout())
	}
}

// issueExample is the policy of the issue (#314).
var issueExample = &Config{
	Default:      "allow",
	UnknownTools: "deny",
	Rules: []Rule{
		{Match: "postgres.pg_execute", Action: "confirm"},
		{Match: "github.delete_*", Action: "deny"},
		{Match: "*", Annotations: map[string]bool{"destructiveHint": true}, Action: "confirm"},
	},
}

func TestEvaluate_RulePrecedence(t *testing.T) {
	destructive := Hints{Destructive: bptr(true)}
	tests := []struct {
		name         string
		cfg          *Config
		server, tool string
		hints        Hints
		action       Action
		rule         int
	}{
		{"exact rule", issueExample, "postgres", "pg_execute", Hints{}, ActionConfirm, 0},
		{"exact rule wins over a later annotation rule", issueExample, "postgres", "pg_execute", destructive, ActionConfirm, 0},
		{"glob deny", issueExample, "github", "delete_file", Hints{}, ActionDeny, 1},
		{"first match wins: deny before destructive confirm", issueExample, "github", "delete_repo", destructive, ActionDeny, 1},
		{"annotation rule", issueExample, "fs", "remove", destructive, ActionConfirm, 2},
		{"annotation declared false does not match true", issueExample, "fs", "remove", Hints{Destructive: bptr(false)}, ActionAllow, RuleDefault},
		{"undeclared annotation never matches", issueExample, "fs", "remove", Hints{}, ActionAllow, RuleDefault},
		{"other hints do not matter", issueExample, "fs", "remove", Hints{ReadOnly: bptr(true), Destructive: bptr(true)}, ActionConfirm, 2},
		{"default allow", issueExample, "github", "create_issue", Hints{}, ActionAllow, RuleDefault},
		{"glob does not cross servers", issueExample, "gitlab", "delete_file", Hints{}, ActionAllow, RuleDefault},
		{"default deny", &Config{Default: "deny", Rules: []Rule{{Match: "github.get_*", Action: "allow"}}}, "github", "create_issue", Hints{}, ActionDeny, RuleDefault},
		{"allow-list under default deny", &Config{Default: "deny", Rules: []Rule{{Match: "github.get_*", Action: "allow"}}}, "github", "get_me", Hints{}, ActionAllow, 0},
		{"a broad rule first shadows a narrower one", &Config{Rules: []Rule{
			{Match: "github.*", Action: "allow"},
			{Match: "github.delete_*", Action: "deny"},
		}}, "github", "delete_file", Hints{}, ActionAllow, 0},
		{"an exact rule below a matching glob loses", &Config{Rules: []Rule{
			{Match: "github.*", Action: "deny"},
			{Match: "github.get_me", Action: "allow"},
		}}, "github", "get_me", Hints{}, ActionDeny, 0},
		{"an exact rule above a matching glob wins", &Config{Rules: []Rule{
			{Match: "github.get_me", Action: "allow"},
			{Match: "github.*", Action: "deny"},
		}}, "github", "get_me", Hints{}, ActionAllow, 0},
		{"an exact rule whose annotations do not match is skipped", &Config{Rules: []Rule{
			{Match: "fs.rm", Annotations: map[string]bool{"destructiveHint": true}, Action: "deny"},
			{Match: "fs.rm", Action: "confirm"},
			{Match: "*", Action: "allow"},
		}}, "fs", "rm", Hints{}, ActionConfirm, 1},
		{"duplicate exact rules: the first matching one wins", &Config{Rules: []Rule{
			{Match: "fs.rm", Action: "deny"},
			{Match: "fs.rm", Action: "allow"},
		}}, "fs", "rm", Hints{}, ActionDeny, 0},
		{"all annotations required", &Config{Rules: []Rule{
			{Match: "*", Annotations: map[string]bool{"readOnlyHint": true, "openWorldHint": false}, Action: "deny"},
		}}, "web", "fetch", Hints{ReadOnly: bptr(true), OpenWorld: bptr(true)}, ActionAllow, RuleDefault},
		{"all annotations present", &Config{Rules: []Rule{
			{Match: "*", Annotations: map[string]bool{"readOnlyHint": true, "openWorldHint": false}, Action: "deny"},
		}}, "db", "query", Hints{ReadOnly: bptr(true), OpenWorld: bptr(false)}, ActionDeny, 0},
		{"idempotent hint", &Config{Rules: []Rule{
			{Match: "*", Annotations: map[string]bool{"idempotentHint": true}, Action: "confirm"},
		}}, "db", "upsert", Hints{Idempotent: bptr(true)}, ActionConfirm, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := MustCompile(tt.cfg)
			d := e.Evaluate(tt.server, tt.tool, tt.hints)
			if d.Action != tt.action || d.Rule != tt.rule {
				t.Fatalf("Evaluate(%s.%s) = %s by %s (rule %d), want %s by rule %d", tt.server, tt.tool, d.Action, d.Label, d.Rule, tt.action, tt.rule)
			}
			if d.Rule >= 0 && !strings.HasPrefix(d.Label, fmt.Sprintf("rules[%d] (match ", d.Rule)) {
				t.Fatalf("label %q", d.Label)
			}
			ex := e.Explain(tt.server, tt.tool, tt.hints, true)
			if ex.Decision.Action != d.Action || ex.Decision.Rule != d.Rule {
				t.Fatalf("Explain disagrees: %+v", ex.Decision)
			}
		})
	}
}

func TestExplain(t *testing.T) {
	e := MustCompile(issueExample)
	ex := e.Explain("github", "delete_repo", Hints{}, false)
	if ex.Decision.Rule != RuleUnknownTools || len(ex.Steps) != 0 {
		t.Fatalf("an unlisted tool is decided by unknown_tools: %+v", ex)
	}
	ex = e.Explain("fs", "remove", Hints{Destructive: bptr(true)}, true)
	if len(ex.Steps) != 3 || ex.Steps[0].Glob || ex.Steps[1].Glob || !ex.Steps[2].Glob || !ex.Steps[2].Hints {
		t.Fatalf("steps: %+v", ex.Steps)
	}
	allowUnknown := MustCompile(&Config{UnknownTools: "allow", Rules: issueExample.Rules})
	if ex := allowUnknown.Explain("github", "delete_x", Hints{}, false); ex.Decision.Rule != 1 {
		t.Fatalf("unknown_tools allow evaluates the rules: %+v", ex.Decision)
	}
}

func TestInjectionOverrideCompiled(t *testing.T) {
	e := MustCompile(&Config{Rules: []Rule{
		{Match: "web.*", Action: "allow", Injection: &InjectionOverride{
			ResponsePolicies: []injection.Rule{{MinRisk: 1, MaxRisk: 100, Action: injection.ActionBlock}},
		}},
	}})
	d := e.Evaluate("web", "fetch", Hints{})
	if d.Injection == nil || d.Injection.Requests != nil || d.Injection.Responses == nil {
		t.Fatalf("injection override: %+v", d.Injection)
	}
	if got := d.Injection.Responses.Dispatch(injection.Result{RiskScore: 10}).Action; got != injection.ActionBlock {
		t.Fatalf("override dispatcher action %s", got)
	}
	if d := e.Evaluate("db", "query", Hints{}); d.Injection != nil {
		t.Fatal("a tool outside the rule must keep the global injection policy")
	}
}

func TestSummaryAndDescribe(t *testing.T) {
	e := MustCompile(issueExample)
	if s := e.Summary(); s != "policy: default allow, unknown_tools deny, 3 rule(s) (1 deny, 2 confirm)" {
		t.Fatalf("summary %q", s)
	}
	if s := e.Describe(2); s != `rules[2] (match "*") annotations {destructiveHint: true} -> confirm` {
		t.Fatalf("describe %q", s)
	}
}

func TestEvaluateIDDoesNotAllocate(t *testing.T) {
	e := MustCompile(benchPolicy(40))
	h := Hints{Destructive: bptr(true)}
	if n := testing.AllocsPerRun(100, func() { _ = e.EvaluateID("srv39.tool_x", h) }); n != 0 {
		t.Fatalf("EvaluateID allocates %.0f times", n)
	}
}

// benchPolicy is n exact and glob rules on other servers, then the issue's
// rules: the worst case walks every rule.
func benchPolicy(n int) *Config {
	cfg := &Config{}
	for i := 0; i < n; i++ {
		m := fmt.Sprintf("srv%d.tool_%d", i, i)
		if i%2 == 1 {
			m = fmt.Sprintf("srv%d.*_delete_*", i)
		}
		cfg.Rules = append(cfg.Rules, Rule{Match: m, Action: "deny"})
	}
	cfg.Rules = append(cfg.Rules, issueExample.Rules...)
	return cfg
}

func BenchmarkEvaluate(b *testing.B) {
	for _, n := range []int{0, 3, 50, 200} {
		e := MustCompile(benchPolicy(n))
		h := Hints{ReadOnly: bptr(true)}
		b.Run(fmt.Sprintf("rules=%d/no-match", n+len(issueExample.Rules)), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = e.EvaluateID("github.create_issue", h)
			}
		})
		b.Run(fmt.Sprintf("rules=%d/server+tool", n+len(issueExample.Rules)), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = e.Evaluate("github", "create_issue", h)
			}
		})
	}
}
