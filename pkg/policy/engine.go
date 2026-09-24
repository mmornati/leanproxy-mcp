package policy

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

// Rule indexes of the decisions that do not come from policy.rules.
const (
	// RuleDefault marks a decision taken by policy.default.
	RuleDefault = -1
	// RuleUnknownTools marks a decision taken by policy.unknown_tools.
	RuleUnknownTools = -2
)

// Hints are the behavior annotations a tool declares (nil: not declared).
type Hints struct {
	ReadOnly, Destructive, Idempotent, OpenWorld *bool
}

func (h Hints) get(i int) *bool {
	switch i {
	case 0:
		return h.ReadOnly
	case 1:
		return h.Destructive
	case 2:
		return h.Idempotent
	default:
		return h.OpenWorld
	}
}

// Injection is a rule's compiled injection override: a nil dispatcher
// keeps the guard's global one.
type Injection struct {
	Requests  *injection.Dispatcher
	Responses *injection.Dispatcher
}

// Decision is the outcome of evaluating one tool call.
type Decision struct {
	Action Action
	// Rule is the index in policy.rules of the deciding rule, or
	// RuleDefault / RuleUnknownTools.
	Rule int
	// Label names what decided, for errors and logs: `rules[1] (match
	// "github.delete_*")`, "policy.default" or "policy.unknown_tools".
	Label string
	// Injection is the deciding rule's injection override (nil: none).
	Injection *Injection
}

// Engine is a compiled policy. It is immutable and safe for concurrent use.
type Engine struct {
	def            Action
	unknown        Action
	confirmTimeout time.Duration
	rules          []compiledRule
	// exact indexes the rules whose glob has no wildcard by name, and
	// globs lists the others (indexes into rules, in order), so a call
	// only walks the wildcard rules above the first exact match.
	exact       map[string][]int
	globs       []int
	defDecision Decision
	unkDecision Decision
	src         Config
}

type compiledRule struct {
	match    glob
	hints    [4]int8 // -1: not required; 0: must be false; 1: must be true
	hasHints bool
	decision Decision
}

// Compile validates cfg and precompiles its globs and injection overrides.
// A nil cfg yields the default policy.
func Compile(cfg *Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	e := &Engine{
		def:            cfg.EffectiveDefault(),
		unknown:        cfg.EffectiveUnknownTools(),
		confirmTimeout: cfg.EffectiveConfirmTimeout(),
	}
	if cfg != nil {
		e.src = *cfg
		e.src.Rules = append([]Rule(nil), cfg.Rules...)
	}
	e.defDecision = Decision{Action: e.def, Rule: RuleDefault, Label: "policy.default"}
	e.unkDecision = Decision{Action: e.unknown, Rule: RuleUnknownTools, Label: "policy.unknown_tools"}
	for i, r := range e.src.Rules {
		cr := compiledRule{
			match:    compileGlob(r.Match),
			hints:    [4]int8{-1, -1, -1, -1},
			decision: Decision{Action: normAction(r.Action), Rule: i, Label: RuleLabel(i, r.Match)},
		}
		for name, want := range r.Annotations {
			cr.hasHints = true
			cr.hints[hintIndex(name)] = 0
			if want {
				cr.hints[hintIndex(name)] = 1
			}
		}
		if o := r.Injection; o != nil {
			inj := &Injection{}
			if o.RequestPolicies != nil {
				inj.Requests = injection.NewDispatcher(o.RequestPolicies)
			}
			if o.ResponsePolicies != nil {
				inj.Responses = injection.NewDispatcher(o.ResponsePolicies)
			}
			cr.decision.Injection = inj
		}
		if cr.match.kind == globExact {
			if e.exact == nil {
				e.exact = make(map[string][]int)
			}
			e.exact[r.Match] = append(e.exact[r.Match], i)
		} else {
			e.globs = append(e.globs, i)
		}
		e.rules = append(e.rules, cr)
	}
	return e, nil
}

// MustCompile is Compile for configs known to be valid (tests).
func MustCompile(cfg *Config) *Engine {
	e, err := Compile(cfg)
	if err != nil {
		panic(err)
	}
	return e
}

// RuleLabel is the label of policy.rules[i].
func RuleLabel(i int, match string) string {
	return "rules[" + strconv.Itoa(i) + "] (match " + strconv.Quote(match) + ")"
}

// Default returns policy.default.
func (e *Engine) Default() Action { return e.def }

// UnknownTools returns policy.unknown_tools.
func (e *Engine) UnknownTools() Action { return e.unknown }

// ConfirmTimeout returns how long a confirmation waits for the user.
func (e *Engine) ConfirmTimeout() time.Duration { return e.confirmTimeout }

// Rules returns a copy of the configured rules.
func (e *Engine) Rules() []Rule { return append([]Rule(nil), e.src.Rules...) }

// MayDeny reports whether some advertised tool can be denied (a deny rule
// or default deny): discovery only needs filtering then.
func (e *Engine) MayDeny() bool {
	if e.def == ActionDeny {
		return true
	}
	for _, r := range e.rules {
		if r.decision.Action == ActionDeny {
			return true
		}
	}
	return false
}

// Trivial reports whether every advertised tool is allowed (no rules,
// default allow).
func (e *Engine) Trivial() bool { return len(e.rules) == 0 && e.def == ActionAllow }

// UnknownDecision is the decision for a tool the server does not list,
// when unknown_tools is deny.
func (e *Engine) UnknownDecision() Decision { return e.unkDecision }

// Evaluate decides a call to tool on server (an advertised tool, or an
// unknown one under unknown_tools: allow, with zero Hints).
func (e *Engine) Evaluate(server, tool string, h Hints) Decision {
	return e.EvaluateID(server+"."+tool, h)
}

// EvaluateID is Evaluate for an already joined "server.tool" identity. It
// does not allocate.
func (e *Engine) EvaluateID(id string, h Hints) Decision {
	// The first exact rule for id whose annotations match bounds the
	// wildcard rules that can still win (first match wins).
	first := len(e.rules)
	for _, i := range e.exact[id] {
		if e.rules[i].hintsMatch(h) {
			first = i
			break
		}
	}
	for _, i := range e.globs {
		if i > first {
			break
		}
		if r := &e.rules[i]; r.matches(id, h) {
			return r.decision
		}
	}
	if first < len(e.rules) {
		return e.rules[first].decision
	}
	return e.defDecision
}

func (r *compiledRule) matches(id string, h Hints) bool {
	return r.match.match(id) && r.hintsMatch(h)
}

// hintsMatch reports whether h satisfies every annotation the rule
// requires (true when it requires none).
func (r *compiledRule) hintsMatch(h Hints) bool {
	if !r.hasHints {
		return true
	}
	for i, want := range r.hints {
		if want < 0 {
			continue
		}
		v := h.get(i)
		if v == nil || *v != (want == 1) {
			return false
		}
	}
	return true
}

// Step is one line of an explanation: how one rule (or the default)
// compared with the call.
type Step struct {
	Label string
	// Glob and Hints report whether the match glob and the annotations
	// matched (Hints is true when the rule requires none).
	Glob, Hints bool
	Action      Action
}

// Explanation is the decision for a call and how every rule compared with
// it, top to bottom, up to the deciding one.
type Explanation struct {
	Decision Decision
	// Listed is whether the tool is in the server's tools/list (false
	// also when that is not known).
	Listed bool
	Steps  []Step
}

// Explain evaluates a call like the middleware does, recording why:
// listed is whether the server advertises the tool.
func (e *Engine) Explain(server, tool string, h Hints, listed bool) Explanation {
	ex := Explanation{Listed: listed}
	if !listed && e.unknown == ActionDeny {
		ex.Decision = e.unkDecision
		return ex
	}
	id := server + "." + tool
	for i := range e.rules {
		r := &e.rules[i]
		st := Step{Label: r.decision.Label, Glob: r.match.match(id), Hints: r.hintsMatch(h), Action: r.decision.Action}
		ex.Steps = append(ex.Steps, st)
		if st.Glob && st.Hints {
			ex.Decision = r.decision
			return ex
		}
	}
	ex.Decision = e.defDecision
	return ex
}

// Summary is the one-line description of the policy for startup logs and
// `doctor security`.
func (e *Engine) Summary() string {
	confirm, deny := 0, 0
	for _, r := range e.rules {
		switch r.decision.Action {
		case ActionConfirm:
			confirm++
		case ActionDeny:
			deny++
		}
	}
	return fmt.Sprintf("policy: default %s, unknown_tools %s, %d rule(s) (%d deny, %d confirm)",
		e.def, e.unknown, len(e.rules), deny, confirm)
}

// Describe renders rule i as it is configured (for `doctor security`).
func (e *Engine) Describe(i int) string {
	r := e.src.Rules[i]
	var sb strings.Builder
	sb.WriteString(RuleLabel(i, r.Match))
	if len(r.Annotations) > 0 {
		sb.WriteString(" annotations {")
		first := true
		for _, name := range hintNames {
			if v, ok := r.Annotations[name]; ok {
				if !first {
					sb.WriteString(", ")
				}
				first = false
				sb.WriteString(name + ": " + strconv.FormatBool(v))
			}
		}
		sb.WriteString("}")
	}
	sb.WriteString(" -> " + string(normAction(r.Action)))
	if r.Injection != nil {
		sb.WriteString(" (injection override:")
		if r.Injection.RequestPolicies != nil {
			sb.WriteString(" request_policies")
		}
		if r.Injection.ResponsePolicies != nil {
			sb.WriteString(" response_policies")
		}
		sb.WriteString(")")
	}
	return sb.String()
}
