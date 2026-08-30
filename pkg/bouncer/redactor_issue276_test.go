package bouncer

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRedactStreamLongSecretPrefixNoLeak verifies acceptance criterion #1
// from issue #276: a secret longer than maxPendingMatch (64 KB) whose prefix
// appears before the regex can complete must not leak any of its bytes.
//
// The custom pattern requires 200+ alphanumeric characters followed by an
// "END" terminator, so a prefix that arrives without enough body and no
// terminator produces zero spans. The redactor must keep enough hold-back
// in front of the partial secret that nothing is emitted unredacted before
// the full secret arrives.
func TestRedactStreamLongSecretPrefixNoLeak(t *testing.T) {
	// Pattern intentionally requires a long body and a fixed terminator so
	// the prefix alone does not match.
	pattern := regexp.MustCompile(`SECRET_[A-Za-z0-9]{200,}END`)
	redactor := NewRedactor([]*regexp.Regexp{pattern})

	// Build a secret whose total length comfortably exceeds maxPendingMatch.
	// Keep it just over the boundary so the test runs in well under a
	// second per readSize; the no-leak contract is the same at 65 KB and
	// 700 KB, only the worst-case scan cost differs.
	const bodyLen = 66_000 // > maxPendingMatch (64 KB)
	body := strings.Repeat("a", bodyLen)
	secret := []byte("SECRET_" + body + "END")
	if len(secret) <= maxPendingMatch {
		t.Fatalf("test secret length %d must exceed maxPendingMatch=%d", len(secret), maxPendingMatch)
	}

	// Filler must not match `[A-Za-z0-9]` so the secret boundary stays
	// detectable and the partial scan cannot accidentally absorb filler.
	const fillerLen = 8 * 1024
	filler := bytes.Repeat([]byte(" "), fillerLen)

	payload := append(append([]byte{}, filler...), secret...)
	payload = append(payload, bytes.Repeat([]byte(" "), 256)...)

	// readSize=1 is exercised too: the rescan-throttle added in this fix
	// bounds the regex calls to one per rescanInterval of new carry, so a
	// 65 KB payload at readSize=1 only triggers a handful of scans. The
	// boundary contract is the same at every read size; bytewise reads are
	// the hardest case for any per-iteration optimization.
	for _, readSize := range []int{4096, 512, 1} {
		t.Run("", func(t *testing.T) {
			var out bytes.Buffer
			if err := redactor.RedactStream(&chunkedReader{data: append([]byte{}, payload...), n: readSize}, &out); err != nil {
				t.Fatalf("readSize=%d: %v", readSize, err)
			}
			got := out.String()

			// Full secret must not appear anywhere in the output.
			if strings.Contains(got, string(secret)) {
				t.Fatalf("readSize=%d: full secret leaked", readSize)
			}
			// The body bytes are the part that the prior code emitted
			// unredacted. The redaction marker "[SECRET_REDACTED]" itself
			// contains the literal substring "SECRET_", so checking the
			// body bytes is the actual leak signal.
			if strings.Contains(got, body[:1024]) {
				t.Fatalf("readSize=%d: secret body prefix leaked", readSize)
			}
			if !strings.Contains(got, SecretRedacted) {
				t.Fatalf("readSize=%d: redaction marker missing", readSize)
			}
			wantLen := fillerLen + len(SecretRedacted) + 256
			if len(got) != wantLen {
				t.Fatalf("readSize=%d: output length %d, want %d", readSize, len(got), wantLen)
			}
		})
	}
}

// TestRedactStreamLongSecretNoLeakAtEOF mirrors the previous test but
// delivers the full secret in a single Read, then EOF. The hold-back logic
// must still redact the whole secret even when no further input arrives
// after the match.
func TestRedactStreamLongSecretNoLeakAtEOF(t *testing.T) {
	pattern := regexp.MustCompile(`SECRET_[A-Za-z0-9]{200,}END`)
	redactor := NewRedactor([]*regexp.Regexp{pattern})

	body := strings.Repeat("b", 66_000)
	secret := []byte("SECRET_" + body + "END")
	if len(secret) <= maxPendingMatch {
		t.Fatalf("test secret length %d must exceed maxPendingMatch=%d", len(secret), maxPendingMatch)
	}

	var out bytes.Buffer
	if err := redactor.RedactStream(bytes.NewReader(secret), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	// The redaction marker is "[SECRET_REDACTED]" which itself contains
	// the literal substring "SECRET_" — check the body bytes instead of
	// the marker, which is the part the redactor would have to hide.
	if strings.Contains(got, body[:1024]) {
		t.Fatalf("secret body leaked: %q", got)
	}
	if !strings.Contains(got, SecretRedacted) {
		t.Fatalf("expected redaction marker, got %q", got)
	}
	if got != SecretRedacted {
		t.Fatalf("expected only the redaction marker, got %q", got)
	}
}

// TestApplySpansPanicsOnSpanPastLimit verifies acceptance criterion #2:
// applySpans must fail loudly (panic) if a span crosses the limit, instead
// of silently emitting raw secret bytes via a slice-bounds-violated append.
func TestApplySpansPanicsOnSpanPastLimit(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("applySpans did not panic for span.end > limit")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value type %T, want string", r)
		}
		if !strings.Contains(msg, "span end") || !strings.Contains(msg, "exceeds limit") {
			t.Fatalf("unexpected panic message: %q", msg)
		}
	}()

	data := []byte("AAA BBB CCC")
	// Span ends past the limit — would cause data[pos:s.start] to read
	// past limit, exposing raw bytes.
	spans := []span{{start: 4, end: 11}}
	applySpans(nil, data, spans, 8)
}

// TestApplySpansPanicsOnSpanRewind verifies the other half of the invariant:
// a span whose start rewinds the running cursor would skip bytes silently
// or panic with an unhelpful slice error. The explicit panic must fire.
func TestApplySpansPanicsOnSpanRewind(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("applySpans did not panic for span.start < pos")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value type %T, want string", r)
		}
		if !strings.Contains(msg, "rewinds current pos") {
			t.Fatalf("unexpected panic message: %q", msg)
		}
	}()

	data := []byte("AAA BBB CCC")
	// First span consumes [4,8). Second span starts at 2 — rewinds the
	// cursor and would either skip bytes or produce an out-of-order
	// redaction, neither of which is acceptable.
	spans := []span{{start: 4, end: 8}, {start: 2, end: 6}}
	applySpans(nil, data, spans, 11)
}

// TestApplySpansAcceptsWellFormedInput is the happy path: a well-formed
// span list must not panic and must produce the expected redacted output.
func TestApplySpansAcceptsWellFormedInput(t *testing.T) {
	data := []byte("AAA AKIAIOSFODNN7EXAMPLE BBB")
	spans := []span{{start: 4, end: 24}}
	got := applySpans(nil, data, spans, len(data))
	want := "AAA " + SecretRedacted + " BBB"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// spinningReader returns (0, nil) forever, then (0, io.EOF) after the
// configured number of empty reads. It is the canonical "misbehaving
// reader" that triggers the CPU-spin bug described in issue #276.
type spinningReader struct {
	emptyReadsLeft int64
	real           []byte
	off            int
}

func (s *spinningReader) Read(p []byte) (int, error) {
	if atomic.LoadInt64(&s.emptyReadsLeft) > 0 {
		atomic.AddInt64(&s.emptyReadsLeft, -1)
		return 0, nil
	}
	if s.off >= len(s.real) {
		return 0, io.EOF
	}
	n := copy(p, s.real[s.off:])
	s.off += n
	return n, nil
}

// TestRedactStreamYieldsOnMisbehavingReader verifies acceptance criterion
// #3: a reader that returns (0, nil) repeatedly must not pin a CPU core at
// 100%, AND once the reader finally produces real data the redactor must
// produce a correctly redacted output (the back-off must not corrupt the
// pipeline — for example by dropping the carry). We assert both: the
// redactor returns within a small multiple of the back-off window while
// the reader is still emitting empty reads, and the final output is
// byte-for-byte the expected redacted JSON.
func TestRedactStreamYieldsOnMisbehavingReader(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))

	const emptyReads = 5000
	reader := &spinningReader{
		emptyReadsLeft: emptyReads,
		real:           []byte(`{"api_key":"AKIAIOSFODNN7EXAMPLE"}`),
	}

	done := make(chan struct {
		out bytes.Buffer
		err error
	}, 1)
	start := time.Now()
	go func() {
		var out bytes.Buffer
		done <- struct {
			out bytes.Buffer
			err error
		}{out, redactor.RedactStream(reader, &out)}
	}()

	var result struct {
		out bytes.Buffer
		err error
	}
	select {
	case result = <-done:
		if result.err != nil {
			t.Fatalf("RedactStream returned error: %v", result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("RedactStream did not return within 5s — back-off not yielding (reader reported %d empty reads remaining)", atomic.LoadInt64(&reader.emptyReadsLeft))
	}
	elapsed := time.Since(start)

	// Backoff is emptyReadBackoff after every emptyReadBackoffThreshold
	// empty reads, so 5000 empty reads should yield ~4 sleeps of 1ms.
	// Generous upper bound to avoid flakes on busy CI runners.
	maxExpected := time.Duration(emptyReads/emptyReadBackoffThreshold+2) * emptyReadBackoff * 50
	if elapsed > maxExpected {
		t.Fatalf("RedactStream took %v, expected at most ~%v (back-off too coarse?)", elapsed, maxExpected)
	}

	// Output correctness: after the back-off window, the real payload
	// must be redacted as if the redactor had seen it cold. This catches
	// regressions where the back-off accidentally swallows bytes from
	// carry, returns early, or otherwise short-circuits the redaction.
	got := result.out.String()
	want := `{"api_key":"` + SecretRedacted + `"}`
	if got != want {
		t.Fatalf("RedactStream output %q, want %q", got, want)
	}
}

// partialErrorReader delivers some bytes, then returns the configured error
// alongside (or in place of) zero bytes. Issue #276 calls out that the
// carry must be flushed (after a final scan) before returning the error
// instead of being silently discarded.
type partialErrorReader struct {
	data      []byte
	readSize  int
	err       error
	errOnNext bool
}

func (p *partialErrorReader) Read(buf []byte) (int, error) {
	if p.errOnNext {
		p.errOnNext = false
		return 0, p.err
	}
	if len(p.data) == 0 {
		return 0, io.EOF
	}
	n := p.readSize
	if n > len(buf) {
		n = len(buf)
	}
	if n > len(p.data) {
		n = len(p.data)
	}
	copy(buf, p.data[:n])
	p.data = p.data[n:]
	if len(p.data) == 0 {
		p.errOnNext = true
	}
	return n, nil
}

// TestRedactStreamFlushesCarryOnPartialReadError verifies acceptance
// criterion #4: when a Read returns (n>0, error), the bytes appended to
// carry must be scanned and emitted (with any secrets redacted) before the
// error is returned to the caller.
func TestRedactStreamFlushesCarryOnPartialReadError(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))

	// Delivered chunk contains a secret; the reader then returns a
	// non-EOF error.
	payload := []byte(`prefix AKIAIOSFODNN7EXAMPLE suffix`)
	wantErr := errors.New("simulated read failure")

	reader := &partialErrorReader{
		data:     payload,
		readSize: len(payload),
		err:      wantErr,
	}

	var out bytes.Buffer
	err := redactor.RedactStream(reader, &out)
	if err == nil {
		t.Fatal("expected error to be returned")
	}
	if !errors.Is(err, wantErr) && !strings.Contains(err.Error(), "simulated read failure") {
		t.Fatalf("expected wrapped read error, got %v", err)
	}

	got := out.String()
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("secret leaked on partial-read error path: %q", got)
	}
	if !strings.Contains(got, SecretRedacted) {
		t.Fatalf("redaction marker missing on partial-read error path: %q", got)
	}
	if !strings.HasPrefix(got, "prefix ") || !strings.HasSuffix(got, " suffix") {
		t.Fatalf("flushed output lost surrounding bytes: %q", got)
	}
}

// TestRedactStreamFlushesCarryOnPartialReadErrorMultipleReads covers the
// case where several chunks land in carry before the error surfaces — the
// flush must still drain every byte that was successfully read.
func TestRedactStreamFlushesCarryOnPartialReadErrorMultipleReads(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))

	chunk1 := bytes.Repeat([]byte("a"), 100)
	chunk2 := []byte(` AKIAIOSFODNN7EXAMPLE `)
	chunk3 := bytes.Repeat([]byte("b"), 100)

	// A reader that delivers three chunks then a non-EOF error.
	seq := &sequenceReader{
		chunks: [][]byte{chunk1, chunk2, chunk3},
		err:    errors.New("disk on fire"),
	}

	var out bytes.Buffer
	err := redactor.RedactStream(seq, &out)
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("expected wrapped read error, got %v", err)
	}

	got := out.String()
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("secret leaked on partial-read error path: %q", got)
	}
	wantMarkerCount := 1
	if got := strings.Count(out.String(), SecretRedacted); got != wantMarkerCount {
		t.Fatalf("expected %d redaction marker, got %d in %q", wantMarkerCount, got, out.String())
	}
	// The a-run and b-run must both be present — the flush must drain all
	// three chunks, not just the last one.
	if !strings.Contains(got, string(chunk1)) || !strings.Contains(got, string(chunk3)) {
		t.Fatalf("flushed output missing one of the chunks: %q", got)
	}
}

type sequenceReader struct {
	chunks [][]byte
	off    int
	err    error
}

func (s *sequenceReader) Read(p []byte) (int, error) {
	if s.off >= len(s.chunks) {
		return 0, s.err
	}
	n := copy(p, s.chunks[s.off])
	s.off++
	return n, nil
}

// TestRedactStreamScanThrottle is a timing-based proxy for the rescan
// throttle: a clean ~65 KB stream delivered in 1-byte chunks must finish
// quickly. Without the throttle, every byte triggers a full regex scan
// and the test takes many seconds (with eight built-in patterns this is
// tens of thousands of full regex passes). With the throttle, only a
// handful of scans run and the test finishes well under the bound below.
// The bound is generous to accommodate slow CI runners; the un-throttled
// implementation would take >30s.
func TestRedactStreamScanThrottle(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))

	payload := bytes.Repeat([]byte("a"), 65_000)

	var out bytes.Buffer
	start := time.Now()
	if err := redactor.RedactStream(&chunkedReader{data: payload, n: 1}, &out); err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("rescan throttle ineffective: 65KB bytewise stream took %v (expected well under 10s on any reasonable runner)", elapsed)
	}
	if !bytes.Equal(out.Bytes(), payload) {
		t.Fatalf("clean stream altered: got %d bytes, want %d", out.Len(), len(payload))
	}
}
