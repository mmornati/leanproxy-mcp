package mcp

import (
	"fmt"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

// Firewall is the "Token Firewall": the security middlewares every front end
// (`server run --stdio` and `serve`) installs, built from one config.
type Firewall struct {
	Redaction *Redaction
	Injection *InjectionGuard
}

// NewFirewall builds the firewall from the `bouncer:` and `injection:` config
// blocks. Redaction is on by default (built-in patterns when bouncerCfg is
// nil); injection classification runs only when an `injection:` block
// enables it.
func NewFirewall(bouncerCfg *bouncer.Config, injectionCfg *injection.Config) *Firewall {
	return &Firewall{
		Redaction: NewRedaction(bouncerCfg),
		Injection: NewInjectionGuard(injectionCfg),
	}
}

// Middlewares returns the firewall stages in pipeline order, outermost
// first:
//
//	client → RedactResponse( RedactRequest → InjectionCheck → next ) → client
//
// The response redactor is outermost so it also covers responses produced
// by the other stages (e.g. a quarantine notice) and by any middleware added
// after the firewall. Request redaction runs before the injection classifier
// so a quarantined payload never lands on disk with a secret in it.
func (f *Firewall) Middlewares() []Middleware {
	if f == nil {
		return nil
	}
	return []Middleware{
		f.Redaction.ResponseMiddleware(),
		f.Redaction.RequestMiddleware(),
		f.Injection.Middleware(),
	}
}

// Summary is the one-line startup status of the firewall, e.g.
// "redaction enabled, 42 patterns; injection disabled".
func (f *Firewall) Summary() string {
	var red *Redaction
	var inj *InjectionGuard
	if f != nil {
		red, inj = f.Redaction, f.Injection
	}
	redaction := "redaction disabled"
	if red.Enabled() {
		redaction = fmt.Sprintf("redaction enabled, %d patterns", red.PatternCount())
	}
	inject := "injection disabled"
	if inj.Enabled() {
		inject = "injection enabled"
	}
	return redaction + "; " + inject
}
