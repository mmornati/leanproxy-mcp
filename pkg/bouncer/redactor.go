package bouncer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"sync"
	"time"
)

const SecretRedacted = "[SECRET_REDACTED]"

// defaultMaxOverlap is the number of trailing bytes held back between
// streaming scans so that a secret straddling a read boundary is still seen
// as a whole. It must be at least as long as the longest single-token
// span any built-in pattern can match (JWTs are routinely several hundred
// bytes; multi-line patterns like pem-private-key and pem-certificate are
// handled by the no-match hold-back path below, not by this constant).
const defaultMaxOverlap = 1024

// maxPendingMatch bounds how long a match touching the end of the buffer is
// held back waiting for more input before it is emitted as-is. When the
// regex cannot yet match a secret prefix we hold this much back anyway, so
// a long-prefix secret that needs more bytes to complete the regex does not
// have its prefix emitted unredacted before the tail arrives.
const maxPendingMatch = 64 * 1024

// maxCarryWithoutMatch caps how much we will buffer when the regex has not
// found any spans in carry. Without a cap, a long clean stream delivered in
// small chunks would grow carry without bound (the redactor never emits
// because the regex cannot match what is not there). With the cap, anything
// beyond this size is emitted on a "no match" scan under the assumption that
// repeated scans of the same bytes are overwhelmingly likely to indicate
// clean data; a true secret longer than the cap is beyond the regex layer's
// threat model anyway and is the documented limit.
const maxCarryWithoutMatch = 4 * maxPendingMatch

// rescanInterval is how many new bytes of carry may arrive before we
// re-run findSpans. The interval is the maximum length any built-in
// pattern can match; scanning more often than that would re-do work
// without changing the result, scanning less often risks missing a
// secret whose regex only completes after a long prefix has accumulated.
const rescanInterval = maxPendingMatch

// emptyReadBackoffThreshold is how many consecutive (0, nil) reads we
// tolerate from a misbehaving io.Reader before sleeping briefly to yield
// CPU. Without this, a reader that returns (0, nil) instead of blocking
// spins the redactor at 100% CPU.
const emptyReadBackoffThreshold = 1024

// emptyReadBackoff is the duration we sleep after every
// emptyReadBackoffThreshold consecutive empty reads. Kept small so a real
// misbehaving reader cannot stall the proxy for long.
const emptyReadBackoff = time.Millisecond

type RedactionMeta struct {
	MessageID string
	Method    string
}

type Redactor struct {
	patterns     []*regexp.Regexp
	alertManager *AlertManager
	bufferSize   int
	maxOverlap   int
}

func NewRedactor(patterns []*regexp.Regexp) *Redactor {
	return &Redactor{
		patterns:   patterns,
		bufferSize: 4096,
		maxOverlap: defaultMaxOverlap,
	}
}

func NewRedactorWithAlerts(patterns []*regexp.Regexp, alertManager *AlertManager) *Redactor {
	return &Redactor{
		patterns:     patterns,
		alertManager: alertManager,
		bufferSize:   4096,
		maxOverlap:   defaultMaxOverlap,
	}
}

// span is a half-open [start, end) byte range of a pattern match.
type span struct{ start, end int }

// scanSpansPool amortizes the per-scan []span allocation across calls to
// RedactStream. findSpansInto appends into a pooled buffer instead of
// allocating a fresh slice, and RedactStream reuses the same buffer across
// every scan inside one call — pre-fix, a 1 MB stream triggered ~190 scans
// and ~190 []span allocations. redactChunkWithCount still allocates a fresh
// span slice per call (its output escapes to the caller, so the per-message
// hot path is a separate design). Tracked separately from carry/out buffers
// because spans contain only integer offsets, no sensitive data, and never
// need zeroing.
//
// Initial cap of 64 covers a 4 KB scan chunk dense with matches (e.g. an
// array of short API keys) without an immediate append-growth reallocation.
// Larger match counts grow the underlying array in place via append and the
// larger buffer stays associated with the slot.
var scanSpansPool = sync.Pool{
	New: func() interface{} {
		s := make([]span, 0, 64)
		return &s
	},
}

// carryBufPool provides a reusable carry buffer for RedactStream. Pre-#277
// each call allocated a ~9 KB carry slice per request; with 1 000 concurrent
// streams that was ~9 MB of short-lived heap that the GC had to churn
// through. Pooling keeps it flat.
//
// The carry buffer may contain raw secret bytes from upstream before the
// regex layer scrubs them, so releaseCarryBuf MUST zero the underlying array
// before returning the slice to the pool. sync.Pool hands buffers out to any
// goroutine; a single unscrubbed reuse would leak a previous request's
// secret. The initial capacity matches the default steady-state size
// (bufferSize + maxOverlap + one more bufferSize ~= 9 KB) so most streams
// never trigger an append-growth reallocation.
var carryBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, defaultMaxOverlap+2*defaultBufferSize)
		return &b
	},
}

// outBufPool provides a reusable redaction-output buffer for RedactStream.
// The output buffer holds redacted data (non-secret slices of carry plus
// SecretRedacted markers), never raw secrets, so releaseOutBuf does not
// need to scrub. The initial capacity matches the default scanThreshold
// (bufferSize + maxOverlap ~= 5 KB) plus slack for the redaction marker
// expansion so most streams never trigger an append-growth reallocation.
var outBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, defaultMaxOverlap+defaultBufferSize+len(SecretRedacted)*2)
		return &b
	},
}

// acquireSpansBuf returns a fresh length-0 spans slice from the pool.
// Callers append into it via findSpansInto and must release it with
// releaseSpansBuf exactly once, regardless of path.
func acquireSpansBuf() []span {
	s := scanSpansPool.Get().(*[]span)
	return (*s)[:0]
}

func releaseSpansBuf(s []span) {
	if cap(s) == 0 {
		// A zero-cap slice (e.g. from a default-valued struct field) is
		// unusable: putting it back would let the next caller pull a
		// stale array that doesn't match the pooled shape.
		return
	}
	// Truncate to length 0. span entries contain only integer offsets, no
	// sensitive data, so we do not zero the backing array.
	s = s[:0]
	scanSpansPool.Put(&s)
}

// acquireCarryBuf returns a length-0 carry buffer from the pool. The
// returned slice's underlying array may contain non-zero bytes from a
// previous request; callers must overwrite (via append) before reading.
func acquireCarryBuf() []byte {
	b := carryBufPool.Get().(*[]byte)
	return (*b)[:0]
}

// releaseCarryBuf zeroes the underlying array of b and returns it to the
// pool. The carry buffer holds raw secret bytes between the moment they
// are read and the moment findSpans/applySpans process them; zeroing here
// is what stops a previous request's secrets from being reused by another
// goroutine that pulls the same buffer. Uses constantTimeZero (the same
// primitive buffer_pool.go uses for the read buffer) so the scrub matches
// the existing constant-time pattern.
func releaseCarryBuf(b []byte) {
	if cap(b) == 0 {
		return
	}
	constantTimeZero(b[:cap(b)])
	b = b[:0]
	carryBufPool.Put(&b)
}

// acquireOutBuf returns a length-0 redaction-output buffer from the pool.
func acquireOutBuf() []byte {
	b := outBufPool.Get().(*[]byte)
	return (*b)[:0]
}

// releaseOutBuf returns b to the pool without scrubbing. The output buffer
// holds redacted data only (non-secret slices of carry plus SecretRedacted
// markers), so leaving its backing array intact is safe and saves the O(cap)
// zero pass on every release.
func releaseOutBuf(b []byte) {
	if cap(b) == 0 {
		return
	}
	b = b[:0]
	outBufPool.Put(&b)
}

// findSpans returns the merged, ordered set of byte ranges matched by any
// pattern. Overlapping or adjacent matches from different patterns are
// coalesced so each byte of input is redacted at most once.
//
// Allocates a fresh slice; callers that operate in a hot loop (RedactStream)
// should use findSpansInto with a pooled buffer instead.
func (r *Redactor) findSpans(data []byte) []span {
	return r.findSpansInto(data, nil)
}

// findSpansInto appends the merged, ordered set of byte ranges matched by
// any pattern into dst. The returned slice aliases into dst, so callers
// must not retain the result past the next findSpansInto call. The result
// is always dst (with len 0 when no spans were found) so the caller can
// safely re-pass the same dst on the next call without first reassigning
// it from the return value.
//
// Passing a fresh dst from the scanSpansPool keeps the per-scan allocation
// flat: the same backing array is reused across all scans inside one
// RedactStream invocation and across calls.
func (r *Redactor) findSpansInto(data []byte, dst []span) []span {
	dst = dst[:0]
	for _, pattern := range r.patterns {
		for _, loc := range pattern.FindAllIndex(data, -1) {
			if loc[1] > loc[0] {
				dst = append(dst, span{loc[0], loc[1]})
			}
		}
	}
	if len(dst) == 0 {
		return dst
	}
	return mergeSpans(dst)
}

// mergeSpans coalesces overlapping or adjacent spans in place. The input
// may be in any order; the result is sorted by start and any spans that
// touch or overlap are merged into one.
func mergeSpans(spans []span) []span {
	if len(spans) <= 1 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})
	merged := spans[:1]
	for _, s := range spans[1:] {
		last := &merged[len(merged)-1]
		if s.start < last.end {
			if s.end > last.end {
				last.end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// applySpans writes data[:limit] to out with every span replaced by the
// redaction marker. All spans must end at or before limit; a span that
// crosses the limit would silently emit raw secret bytes to the writer, so
// we panic instead of letting that happen. The invariant is enforced by
// the hold-back logic in RedactStream and the tests in
// redactor_boundary_test.go; the runtime check is defense in depth so a
// future change to either side that breaks the contract fails loudly rather
// than leaking.
func applySpans(out []byte, data []byte, spans []span, limit int) []byte {
	pos := 0
	for _, s := range spans {
		if s.start < pos {
			panic(fmt.Sprintf("applySpans: span start %d rewinds current pos %d (limit=%d, data_len=%d)", s.start, pos, limit, len(data)))
		}
		if s.end > limit {
			panic(fmt.Sprintf("applySpans: span end %d exceeds limit %d (data_len=%d)", s.end, limit, len(data)))
		}
		out = append(out, data[pos:s.start]...)
		out = append(out, SecretRedacted...)
		pos = s.end
	}
	if pos > limit {
		panic(fmt.Sprintf("applySpans: trailing pos %d exceeds limit %d (data_len=%d)", pos, limit, len(data)))
	}
	return append(out, data[pos:limit]...)
}

// RedactStream copies reader to writer, replacing secrets as they pass
// through. Input is accumulated before scanning and a tail of up to
// maxOverlap bytes after the last match is held back until more input (or
// EOF) arrives, so a secret split across arbitrary read boundaries — including
// very small reads from a pipe — is still redacted as a whole.
//
// All per-request scratch buffers (carry, output, scan spans) come from
// package-level sync.Pools; the defer block returns each one with its
// backing array zeroed (where it holds sensitive bytes) before this
// function returns. See issue #277 for the allocation profile this is
// meant to flatten.
func (r *Redactor) RedactStream(reader io.Reader, writer io.Writer, meta ...*RedactionMeta) error {
	readerBuf := bufio.NewReaderSize(reader, r.bufferSize)
	writerBuf := bufio.NewWriterSize(writer, r.bufferSize)
	defer writerBuf.Flush()

	maxOverlap := r.maxOverlap
	if maxOverlap <= 0 {
		maxOverlap = defaultMaxOverlap
	}
	// Scan once we hold a full buffer beyond the hold-back window, so steady
	// state emits bufferSize bytes per scan rather than stalling.
	scanThreshold := r.bufferSize + maxOverlap

	var totalRead, totalWritten int64
	matchCount := 0

	// Pooled scratch buffers; each is zeroed (or, for spans, truncated) on
	// release so secrets do not survive across requests. The spans buffer
	// is reused across every scan inside this call — the central win of
	// issue #277: pre-fix, findSpans allocated a fresh slice every scan
	// (≈190 scans for a 1 MB stream at default thresholds).
	carry := acquireCarryBuf()
	defer releaseCarryBuf(carry)
	out := acquireOutBuf()
	defer releaseOutBuf(out)
	spansBuf := acquireSpansBuf()
	defer releaseSpansBuf(spansBuf)

	buf := GetBuffer()
	defer ReturnBuffer(buf)

	// Tracks consecutive (0, nil) reads so we can yield CPU instead of
	// spinning when an io.Reader returns no bytes and no error.
	emptyReads := 0

	// Tracks the length of carry at the most recent scan, so we can avoid
	// re-running findSpans on every iteration when the regex has nothing
	// new to evaluate. The full carry is re-scanned only after it grows
	// by rescanInterval bytes (or at EOF). Without this throttle, a slow
	// trickle of small reads would re-scan the entire carry on every
	// iteration and turn the loop into O(N²) on the regex engine — which
	// is exactly the regression the no-span hold-back introduces for
	// long clean streams.
	lastScanCarryLen := 0

	for {
		n, err := readerBuf.Read(buf)
		if n > 0 {
			carry = append(carry, buf[:n]...)
			totalRead += int64(n)
			emptyReads = 0
		} else if err == nil {
			// (0, nil) is permitted by io.Reader but means "no data right
			// now, try again". A misbehaving reader that loops on this
			// would otherwise spin RedactStream at 100% CPU. Yield
			// periodically so the goroutine cooperates with the scheduler.
			emptyReads++
			if emptyReads >= emptyReadBackoffThreshold {
				time.Sleep(emptyReadBackoff)
				emptyReads = 0
			}
			continue
		} else {
			emptyReads = 0
		}

		atEOF := err == io.EOF
		if err != nil && !atEOF {
			// Flush whatever we accumulated before bailing. A partial read
			// followed by an error used to silently discard the carry,
			// leaving the downstream client with a truncated stream and no
			// signal that something went wrong. Best-effort flush: a write
			// error here does not mask the original read error, but is
			// surfaced through the warning log so an operator can spot a
			// downstream that is failing while we are reading. We flush
			// explicitly (the deferred Flush swallows its error) and only
			// count bytes that actually reached the underlying writer.
			if len(carry) > 0 {
				flushSpans := r.findSpansInto(carry, spansBuf)
				flushOut := applySpans(out[:0], carry, flushSpans, len(carry))
				if _, writeErr := writerBuf.Write(flushOut); writeErr != nil {
					slog.Warn("bouncer redact: final flush after read error failed", "write_error", writeErr, "carry_len", len(carry))
				} else {
					totalWritten += int64(len(flushOut))
					matchCount += len(flushSpans)
				}
			}
			if flushErr := writerBuf.Flush(); flushErr != nil {
				slog.Warn("bouncer redact: flush after read error failed", "write_error", flushErr)
			}
			return fmt.Errorf("bouncer redact: %w", err)
		}

		if !atEOF && len(carry) < scanThreshold {
			continue
		}

		// Re-scan the regex only when carry has grown enough to make the
		// cost worthwhile. A scan at lastScanCarryLen + rescanInterval
		// is sufficient to catch any span that starts at most
		// rescanInterval bytes back from the current end of carry, which
		// is the maximum range any of the built-in patterns can produce.
		// At EOF we always do a final scan so nothing is left unredacted.
		var spans []span
		if atEOF || lastScanCarryLen == 0 || len(carry)-lastScanCarryLen >= rescanInterval {
			spans = r.findSpansInto(carry, spansBuf)
			lastScanCarryLen = len(carry)
		}
		hold := 0
		if !atEOF {
			// A match that runs right up to the end of what we have read may be
			// a truncated prefix of a longer secret (open-ended patterns such as
			// github_pat_…{22,} match partial tokens). Hold it back from its
			// start and rescan once more input arrives, unless it has grown
			// beyond any plausible secret length.
			if n := len(spans); n > 0 && spans[n-1].end == len(carry) && len(carry)-spans[n-1].start <= maxPendingMatch {
				hold = len(carry) - spans[n-1].start
				spans = spans[:n-1]
			}
			lastEnd := 0
			if len(spans) > 0 {
				lastEnd = spans[len(spans)-1].end
			}
			if h := min(maxOverlap, len(carry)-lastEnd); h > hold {
				hold = h
			}
			// If the regex returned no spans and we are still streaming, the
			// carry may contain the prefix of a long secret whose terminator
			// or required length has not arrived yet. The match-touching-end
			// branch above only fires when the regex already found something
			// — without a span it never engages and hold reverts to
			// maxOverlap, which is far smaller than a legitimate secret.
			// Hold the entire carry so the prefix is not emitted unredacted
			// before its tail arrives, and only relax the hold once carry
			// exceeds maxCarryWithoutMatch so a long clean stream does not
			// grow carry without bound.
			if len(spans) == 0 {
				if len(carry) > maxCarryWithoutMatch {
					hold = len(carry) - maxCarryWithoutMatch
				} else {
					hold = len(carry)
				}
			}
		}
		emitEnd := len(carry) - hold

		out = applySpans(out[:0], carry, spans, emitEnd)
		if _, writeErr := writerBuf.Write(out); writeErr != nil {
			return fmt.Errorf("bouncer redact: %w", writeErr)
		}
		totalWritten += int64(len(out))
		matchCount += len(spans)
		slog.Debug("processing chunk", "size", emitEnd, "held", hold)

		carry = append(carry[:0], carry[emitEnd:]...)

		if atEOF {
			slog.Debug("streaming redaction complete", "bytes_read", totalRead, "bytes_written", totalWritten, "secrets_found", matchCount)
			break
		}
	}

	if r.alertManager != nil && matchCount > 0 && len(meta) > 0 && meta[0] != nil {
		r.alertManager.RecordRedaction(RedactionEvent{
			PatternName: "streaming_redaction",
			Count:       matchCount,
			Timestamp:   time.Now(),
			MessageID:   meta[0].MessageID,
			Method:      meta[0].Method,
		})
		r.alertManager.EmitSummary(meta[0].MessageID, meta[0].Method)
	}

	return nil
}

// redactChunkWithCount redacts a self-contained byte slice and reports how
// many distinct secret spans were replaced.
func (r *Redactor) redactChunkWithCount(chunk []byte) ([]byte, int) {
	spans := r.findSpans(chunk)
	if len(spans) == 0 {
		out := make([]byte, len(chunk))
		copy(out, chunk)
		return out, 0
	}
	return applySpans(make([]byte, 0, len(chunk)), chunk, spans, len(chunk)), len(spans)
}

func (r *Redactor) redactChunk(chunk []byte) []byte {
	out, _ := r.redactChunkWithCount(chunk)
	return out
}

// RedactJSON redacts every string value in a JSON document. If the input is
// not valid JSON it falls back to a byte-level scan of the raw input rather
// than passing it through unchanged, so a malformed or truncated payload can
// never be used to smuggle a secret past the redactor.
func (r *Redactor) RedactJSON(data []byte) ([]byte, int, error) {
	slog.Debug("redacting message", "size", len(data))

	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Warn("invalid JSON input, falling back to byte-level redaction", "error", err)
		redacted, count := r.redactChunkWithCount(data)
		if count > 0 {
			slog.Info("redaction complete", "secrets_found", count, "mode", "bytes")
		}
		return redacted, count, nil
	}

	redactedRaw, count := r.redactInterface(raw)
	redacted, err := json.Marshal(redactedRaw)
	if err != nil {
		return nil, 0, fmt.Errorf("bouncer redact: marshal: %w", err)
	}

	if count > 0 {
		slog.Info("redaction complete", "secrets_found", count)
	}

	return redacted, count, nil
}

func (r *Redactor) redactInterface(val interface{}) (interface{}, int) {
	switch v := val.(type) {
	case string:
		return r.redactString(v)
	case map[string]interface{}:
		totalCount := 0
		for k, val := range v {
			// Sensitive-field-name pass: a value sitting behind a key
			// in SensitiveJSONFieldNames (e.g. "api_key", "private_key",
			// "client_secret") is always treated as a credential,
			// regardless of whether the value matches any built-in
			// regex. This catches tokens whose value the operator has
			// not enumerated, and is the documented behavior for #279
			// acceptance criterion (sensitive JSON fields at any depth).
			// The field-name pass is JSON-aware, so it preserves the
			// surrounding key/value framing: only the value is redacted.
			if _, ok := val.(string); ok && sensitiveJSONFieldLookup(k) {
				v[k] = SecretRedacted
				totalCount++
				continue
			}
			newVal, count := r.redactInterface(val)
			v[k] = newVal
			totalCount += count
		}
		return v, totalCount
	case []interface{}:
		totalCount := 0
		for i, val := range v {
			newVal, count := r.redactInterface(val)
			v[i] = newVal
			totalCount += count
		}
		return v, totalCount
	default:
		return v, 0
	}
}

func (r *Redactor) redactString(data string) (string, int) {
	result := data
	totalCount := 0
	for _, pattern := range r.patterns {
		matches := pattern.FindAllString(result, -1)
		if len(matches) > 0 {
			totalCount += len(matches)
			result = pattern.ReplaceAllString(result, SecretRedacted)
		}
	}
	return result, totalCount
}

func NewRedactorFromLoaded(loaded *LoadedPatterns) *Redactor {
	return NewRedactor(loaded.All)
}

type SidecarClient interface {
	Redact(ctx context.Context, content string) string
	FallbackCount() int64
	Provider() string
	Model() string
	Healthy(ctx context.Context) bool
}

// SidecarFallback is the sentinel string the sidecar LLM emits when its
// primary path fails (Ollama down, decode error, empty response). The
// redactor treats it as "I could not redact anything; do whatever you did
// without me" rather than as a hard error, so a single Ollama hiccup does
// not reject every request. See pkg/sidecar.Client.aggressiveRedact.
const SidecarFallback = "[VALUE_REDACTED]"

// RedactJSONWithSidecar applies the regex redactor first and then, if a
// sidecar is configured and either the regex matched nothing or
// alwaysCallSidecar is true, hands the already-redacted content to the
// sidecar for a second pass. The sidecar therefore never sees a secret the
// regex layer could catch (the regex pass runs first regardless), and its
// output is accepted only if it is valid JSON or matches the sidecar's
// documented fallback sentinel.
func RedactJSONWithSidecar(ctx context.Context, data []byte, r *Redactor, sidecar SidecarClient, alwaysCallSidecar bool) ([]byte, error) {
	if r != nil {
		redacted, count, err := r.RedactJSON(data)
		if err != nil {
			return nil, err
		}
		data = redacted
		if count > 0 && !alwaysCallSidecar {
			// Regex caught something and the operator did not opt in to
			// the per-request sidecar path: skip the LLM round-trip and
			// forward the regex-cleaned payload.
			return redacted, nil
		}
	}
	if sidecar != nil {
		sidecarResult := []byte(sidecar.Redact(ctx, string(data)))
		if isSidecarFallback(sidecarResult) {
			slog.Warn("sidecar: redaction fallback in use; relying on regex layer",
				"output_len", len(sidecarResult))
			return data, nil
		}
		if !json.Valid(sidecarResult) {
			return nil, fmt.Errorf("bouncer redact: sidecar returned invalid JSON")
		}
		return sidecarResult, nil
	}
	return data, nil
}

// isSidecarFallback reports whether the sidecar output is one of the
// documented fallback forms (empty string, or the SidecarFallback sentinel)
// rather than a real redaction.
func isSidecarFallback(out []byte) bool {
	if len(out) == 0 {
		return true
	}
	return bytes.Equal(out, []byte(SidecarFallback))
}

// Patterns returns the compiled patterns this redactor applies.
func (r *Redactor) Patterns() []*regexp.Regexp {
	return r.patterns
}
