// Package policy is the per-tool policy layer (issue #314, audit S14,
// OWASP MCP02 / MCP07): which upstream tools a client may call, which ones
// need a human's confirmation first, and which ones are refused.
//
// A policy is the `policy:` config block:
//
//	policy:
//	  default: allow          # allow | deny
//	  unknown_tools: deny     # deny | allow
//	  rules:                  # first match wins; globs on "server.tool"
//	    - match: "postgres.pg_execute"
//	      action: confirm     # allow | deny | confirm
//	    - match: "github.delete_*"
//	      action: deny
//	    - match: "*"
//	      annotations: { destructiveHint: true }
//	      action: confirm
//
// A call is decided in this order, and the first step that decides wins:
//
//  1. unknown_tools: a tool the server does not list in its tools/list is
//     refused (deny, the default) or evaluated like any other tool (allow),
//     without annotations;
//  2. rules, top to bottom: the first rule whose match glob AND
//     annotations (all of them) match decides;
//  3. default: allow (the default) or deny.
//
// There is no "most specific rule" logic: order the rules from the most
// specific to the most general.
//
// Config holds the YAML, Compile turns it into an Engine whose globs are
// precompiled; Engine.Evaluate is safe for concurrent use and does not
// allocate.
package policy

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

// Action is what a policy decides for a tool call.
type Action string

const (
	// ActionAllow lets the call through.
	ActionAllow Action = "allow"
	// ActionDeny refuses the call; the upstream is never called.
	ActionDeny Action = "deny"
	// ActionConfirm asks the user (MCP elicitation) before the call; a
	// client that cannot be asked gets the call refused.
	ActionConfirm Action = "confirm"
)

// DefaultConfirmTimeout bounds how long a confirmation waits for the user.
const DefaultConfirmTimeout = 5 * time.Minute

// Config is the `policy:` block. A nil *Config means the defaults: every
// advertised tool is allowed, tools a server does not advertise are
// refused.
type Config struct {
	// Default is the action when no rule matches: allow (default) or deny.
	Default Action `yaml:"default,omitempty"`
	// UnknownTools is what happens to a call to a tool that is not in the
	// server's current tools/list: deny (default) or allow.
	UnknownTools Action `yaml:"unknown_tools,omitempty"`
	// Rules are evaluated top to bottom; the first match wins.
	Rules []Rule `yaml:"rules,omitempty"`
	// ConfirmTimeout bounds how long a confirm rule waits for the user's
	// answer (Go duration, default 5m). No answer in time refuses the call.
	ConfirmTimeout string `yaml:"confirm_timeout,omitempty"`
}

// Rule is one entry of policy.rules.
type Rule struct {
	// Match is a glob on "server.tool": `*` matches any run of characters
	// (dots included), `?` exactly one character; everything else is
	// literal and case-sensitive.
	Match string `yaml:"match"`
	// Annotations, when set, also require the tool to declare each of
	// these hints (readOnlyHint, destructiveHint, idempotentHint,
	// openWorldHint) with exactly this value. A hint the tool does not
	// declare never matches.
	Annotations map[string]bool `yaml:"annotations,omitempty"`
	// Action is allow, deny or confirm.
	Action Action `yaml:"action"`
	// Injection overrides the prompt-injection guard's policies for the
	// calls this rule matches (and lets through).
	Injection *InjectionOverride `yaml:"injection,omitempty"`
}

// InjectionOverride replaces the injection guard's risk bands for the
// tools of one rule. A list that is not set keeps the global one.
type InjectionOverride struct {
	// RequestPolicies replace injection.request_policies for the call's
	// arguments (actions: block, quarantine, redact, log).
	RequestPolicies []injection.Rule `yaml:"request_policies,omitempty"`
	// ResponsePolicies replace injection.response_policies for the tool's
	// result (actions: annotate, redact, block, log).
	ResponsePolicies []injection.Rule `yaml:"response_policies,omitempty"`
}

// Annotation hint names accepted in Rule.Annotations.
const (
	HintReadOnly    = "readOnlyHint"
	HintDestructive = "destructiveHint"
	HintIdempotent  = "idempotentHint"
	HintOpenWorld   = "openWorldHint"
)

var hintNames = []string{HintReadOnly, HintDestructive, HintIdempotent, HintOpenWorld}

func normAction(a Action) Action {
	return Action(strings.ToLower(strings.TrimSpace(string(a))))
}

// EffectiveDefault returns default, or allow when unset.
func (c *Config) EffectiveDefault() Action {
	if c == nil || c.Default == "" {
		return ActionAllow
	}
	return normAction(c.Default)
}

// EffectiveUnknownTools returns unknown_tools, or deny when unset.
func (c *Config) EffectiveUnknownTools() Action {
	if c == nil || c.UnknownTools == "" {
		return ActionDeny
	}
	return normAction(c.UnknownTools)
}

// EffectiveConfirmTimeout returns confirm_timeout, or its default. An
// invalid value (rejected by Validate) also yields the default.
func (c *Config) EffectiveConfirmTimeout() time.Duration {
	if c == nil || c.ConfirmTimeout == "" {
		return DefaultConfirmTimeout
	}
	d, err := time.ParseDuration(c.ConfirmTimeout)
	if err != nil || d <= 0 {
		return DefaultConfirmTimeout
	}
	return d
}

// Validate checks every action, glob, annotation name and injection
// override. A nil receiver is valid.
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	switch c.EffectiveDefault() {
	case ActionAllow, ActionDeny:
	default:
		return fmt.Errorf("policy.default: %q is not one of allow, deny", c.Default)
	}
	switch c.EffectiveUnknownTools() {
	case ActionAllow, ActionDeny:
	default:
		return fmt.Errorf("policy.unknown_tools: %q is not one of deny, allow", c.UnknownTools)
	}
	if c.ConfirmTimeout != "" {
		d, err := time.ParseDuration(c.ConfirmTimeout)
		if err != nil {
			return fmt.Errorf("policy.confirm_timeout: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("policy.confirm_timeout must be > 0, got %s", c.ConfirmTimeout)
		}
	}
	for i, r := range c.Rules {
		if err := r.validate(); err != nil {
			return fmt.Errorf("policy.rules[%d]: %w", i, err)
		}
	}
	return nil
}

func (r Rule) validate() error {
	if strings.TrimSpace(r.Match) == "" {
		return fmt.Errorf("match is required (a glob on \"server.tool\", e.g. \"github.delete_*\" or \"*\")")
	}
	if err := validGlob(r.Match); err != nil {
		return fmt.Errorf("match %q: %w", r.Match, err)
	}
	switch normAction(r.Action) {
	case ActionAllow, ActionDeny, ActionConfirm:
	case "":
		return fmt.Errorf("action is required (allow, deny or confirm)")
	default:
		return fmt.Errorf("action %q is not one of allow, deny, confirm", r.Action)
	}
	for name := range r.Annotations {
		if hintIndex(name) < 0 {
			return fmt.Errorf("annotations: unknown hint %q (known: %s)", name, strings.Join(hintNames, ", "))
		}
	}
	if o := r.Injection; o != nil {
		if o.RequestPolicies == nil && o.ResponsePolicies == nil {
			return fmt.Errorf("injection: set request_policies and/or response_policies")
		}
		probe := injection.Config{Enabled: true, RequestPolicies: o.RequestPolicies, ResponsePolicies: o.ResponsePolicies}
		if err := probe.Validate(); err != nil {
			return fmt.Errorf("injection: %s", strings.TrimPrefix(err.Error(), "injection: "))
		}
	}
	return nil
}

func hintIndex(name string) int {
	for i, n := range hintNames {
		if n == name {
			return i
		}
	}
	return -1
}
