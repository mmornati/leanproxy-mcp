// Package logx provides lazy, redacted, truncated slog attributes for
// hot-path logging. A raw request or response payload must never reach a
// log file unredacted, and formatting it (converting bytes to a string,
// scanning for secrets) must never cost anything unless the record is
// actually going to be emitted.
package logx

import (
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
)

// MaxPayloadLogBytes is the length a payload's logged text is truncated to.
const MaxPayloadLogBytes = 512

// truncatedSuffix is appended when a payload was cut down to
// MaxPayloadLogBytes.
const truncatedSuffix = "...(truncated)"

// builtinPatterns backs Payload's redaction. It is the same built-in
// pattern set bouncer.NewRedaction falls back to when no `bouncer:` config
// is supplied, compiled once so every log call reuses it. Payload does not
// take a *bouncer.Redactor because it is used from places with no access to
// one (a request's firewall redactor is configured per Handler, not
// available to every logging call site, and some logging happens outside
// the firewall's request path entirely); it redacts independently as a
// defense-in-depth measure, on top of whatever redaction the pipeline
// already applied.
var builtinPatterns = bouncer.PatternsToRegexps(bouncer.BuiltInPatterns)

// payloadValue defers formatting a payload until slog actually resolves it,
// which only happens when the handler is enabled for the record's level.
type payloadValue struct {
	b []byte
}

// Payload returns a slog.Attr for a raw byte payload (a request's params, a
// response's result, an upstream line) whose value is redacted and
// truncated, and only when the log record is actually emitted:
//
//	logger.Debug("handleToolsCall called", logx.Payload("params", req.Params))
//
// Building the Attr itself does no work beyond holding a reference to b, so
// it is safe to call regardless of the configured log level.
func Payload(key string, b []byte) slog.Attr {
	return slog.Attr{Key: key, Value: slog.AnyValue(payloadValue{b: b})}
}

// LogValue implements slog.LogValuer.
func (p payloadValue) LogValue() slog.Value {
	return slog.StringValue(p.String())
}

// String redacts and truncates the payload. Exported so callers that build
// their own log line (rather than passing a raw slog.Attr) can reuse it.
func (p payloadValue) String() string {
	if len(p.b) == 0 {
		return ""
	}
	s := bouncer.RedactWithPatterns(string(p.b), builtinPatterns)
	if len(s) > MaxPayloadLogBytes {
		s = s[:MaxPayloadLogBytes] + truncatedSuffix
	}
	return s
}
