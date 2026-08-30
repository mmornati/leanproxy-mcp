package bouncer

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestRedactStreamPoolScrubsCarryBetweenRequests verifies acceptance
// criterion #1 from issue #277: a carry buffer that holds raw secret bytes
// must be zeroed before it returns to the pool, so a subsequent request
// that pulls the same buffer from the pool never sees the previous
// request's secret.
//
// The test writes a recognisable secret into a pooled carry buffer via the
// release helper, then pulls a fresh carry buffer from the pool and asserts
// the secret bytes are not visible. This exercises the scrub directly
// rather than relying on a side-channel observable through RedactStream's
// public API.
func TestRedactStreamPoolScrubsCarryBetweenRequests(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE"
	secretBytes := []byte(secret)

	// Acquire a carry buffer, write the secret into it, and release. If
	// releaseCarryBuf were a no-op, the next acquire would hand back the
	// same backing array with the secret still in it.
	b1 := acquireCarryBuf()
	if len(b1) != 0 {
		t.Fatalf("acquired carry buffer must have len 0, got %d", len(b1))
	}
	b1 = append(b1, secretBytes...)
	releaseCarryBuf(b1)

	// Pull a fresh carry buffer from the pool. There is no guarantee
	// sync.Pool returns the same backing array — entries can be
	// discarded between Get and Put — but in practice under a steady
	// test load the same slot is reused, so we drain a handful and
	// inspect each for stray secret bytes.
	for i := 0; i < 16; i++ {
		b := acquireCarryBuf()
		if bytes.Contains(b[:cap(b)], secretBytes) {
			t.Fatalf("carry buffer slot %d still contains secret bytes after release", i)
		}
		releaseCarryBuf(b)
	}

	// End-to-end check that a request whose output *would* expose a
	// stale carry never leaks: redacting an empty payload after a
	// secret-bearing one must produce empty output.
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	var sink1 bytes.Buffer
	if err := redactor.RedactStream(bytes.NewReader(append(bytes.Repeat([]byte("x"), 100), secretBytes...)), &sink1); err != nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	if !strings.Contains(sink1.String(), SecretRedacted) {
		t.Fatalf("request 1 should have produced a redaction marker, got %q", sink1.String())
	}
	var sink2 bytes.Buffer
	if err := redactor.RedactStream(bytes.NewReader(nil), &sink2); err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	if sink2.Len() != 0 {
		t.Fatalf("empty request 2 produced %d output bytes, want 0", sink2.Len())
	}
}

// TestRedactStreamPoolBuffersReused verifies that the carry / out / spans
// buffers are pooled and reused rather than freshly allocated for each
// call. The test calls RedactStream many times and checks that
// allocation counters stay flat (covered by the benchmark) — here we just
// make sure the reuse path is exercised and the output is still correct
// when the pool returns the same backing array to multiple calls.
func TestRedactStreamPoolBuffersReused(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))

	// Alternate two distinct payloads to force the carry / spans pool to
	// hand back the same buffer with different contents. Any state leak
	// from one call to the next would surface as a wrong-length output
	// or a leaked substring.
	short := []byte(`safe text`)
	longWithSecret := append(bytes.Repeat([]byte("a"), 8000), []byte("AKIAIOSFODNN7EXAMPLE")...)
	longWithSecret = append(longWithSecret, bytes.Repeat([]byte("a"), 8000)...)

	for i := 0; i < 50; i++ {
		var (
			input  []byte
			expect string
		)
		if i%2 == 0 {
			input = short
			expect = "safe text"
		} else {
			input = longWithSecret
			expect = SecretRedacted
		}
		var sink bytes.Buffer
		if err := redactor.RedactStream(bytes.NewReader(input), &sink); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !strings.Contains(sink.String(), expect) {
			t.Fatalf("iteration %d: missing %q in output %q", i, expect, sink.String())
		}
		// Ensure no stale substring from the other payload survived.
		if i%2 == 0 && strings.Contains(sink.String(), "AKIAIOSFODNN7EXAMPLE") {
			t.Fatalf("iteration %d: stale secret from previous long run leaked into short output: %q", i, sink.String())
		}
	}
}

// TestRedactStreamPoolConcurrentNoLeak exercises the pooled buffers from
// many goroutines simultaneously. The test is a check that
// releaseCarryBuf / releaseSpansBuf are race-free and that the zeroing
// step does not race with a concurrent reader pulling the same buffer.
//
// Without -race this test is unlikely to fail; with -race (which is part
// of the project's CI test invocation) it surfaces any data race in the
// pool plumbing.
func TestRedactStreamPoolConcurrentNoLeak(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	pattern := []byte("AKIAIOSFODNN7EXAMPLE")

	const goroutines = 32
	const calls = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines*calls)

	for g := 0; g < goroutines; g++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < calls; i++ {
				// Each goroutine emits a payload with a secret on
				// alternating iterations. The pool must hand out
				// distinct buffers to each goroutine (sync.Pool's
				// per-P shard) and the zeroing on release must
				// not race with the next Get. Use a wide printable
				// character range so concurrent goroutines do not
				// collide on identical fill bytes.
				fill := byte('a' + byte(seed%26))
				payload := bytes.Repeat([]byte{fill}, 100+i)
				if i%2 == 0 {
					payload = append(payload, pattern...)
				}
				payload = append(payload, bytes.Repeat([]byte{fill}, 100+i)...)
				var sink bytes.Buffer
				if err := redactor.RedactStream(bytes.NewReader(payload), &sink); err != nil {
					errs <- err
					return
				}
				if strings.Contains(sink.String(), string(pattern)) {
					errs <- io.ErrUnexpectedEOF
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent redaction failure: %v", err)
		}
	}
}

// TestFindSpansIntoReusesDestinationBuffer verifies the findSpansInto
// helper appends into the destination rather than allocating a fresh
// slice. The test passes the same dst into findSpansInto twice with
// different inputs and asserts both calls write into the same backing
// array.
//
// Slice headers are passed by value, so the test reassigns the local
// dst variable to the returned slice (mirroring how RedactStream uses
// the helper) before checking that the second scan overwrites — not
// appends to — the first.
func TestFindSpansIntoReusesDestinationBuffer(t *testing.T) {
	redactor := NewRedactor([]*regexp.Regexp{regexp.MustCompile(`SECRET_[A-Z0-9]+`)})

	dst := make([]span, 0, 4)
	dst = redactor.findSpansInto([]byte("hello SECRET_AAA world"), dst)
	if len(dst) != 1 {
		t.Fatalf("first scan: want 1 span, got %d", len(dst))
	}
	firstAddr := &dst[0]

	// Reusing dst for a second scan must truncate to length 0 and rewrite
	// the backing array — not append to the previous scan's result. The
	// capacity must stay within the initial 4 so the pool's amortisation
	// is observable in steady state.
	dst = redactor.findSpansInto([]byte("SECRET_BBB SECRET_CCC"), dst)
	if len(dst) != 2 {
		t.Fatalf("second scan: want 2 spans, got %d", len(dst))
	}
	if cap(dst) > 4 {
		t.Fatalf("dst grew past its initial cap: cap=%d (pool amortization broken)", cap(dst))
	}
	if &dst[0] != firstAddr {
		t.Fatalf("dst backing array changed across calls: %p -> %p (pool not reusing)", firstAddr, &dst[0])
	}
	want := []span{{start: 0, end: 10}, {start: 11, end: 21}}
	for i, s := range dst {
		if s != want[i] {
			t.Fatalf("spans[%d] = %+v, want %+v", i, s, want[i])
		}
	}
}

// BenchmarkRedactStreamLargePayload documents the post-fix allocation
// profile of RedactStream on the canonical large-payload workload (1 MB
// clean stream at default thresholds). Each iteration pays for a fresh
// read buffer (GetBuffer) plus append-growth events when the carry and
// output buffers outgrow their pooled capacities. sync.Pool discards
// entries between benchmark iterations, so each iteration re-grows from
// the initial pool caps; in production these large buffers stay in the
// pool across requests and serve future large streams at zero growth
// cost.
//
// Run with `go test -bench BenchmarkRedactStreamLargePayload -benchmem
// -benchtime=3s ./pkg/bouncer/`.
func BenchmarkRedactStreamLargePayload(b *testing.B) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	// 1 MB clean payload — exercises ~190 scans at default thresholds
	// (scanThreshold=5 KB), the workload the issue's allocation numbers
	// are calibrated against.
	payload := bytes.Repeat([]byte("a"), 1<<20)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sink bytes.Buffer
		if err := redactor.RedactStream(bytes.NewReader(payload), &sink); err != nil {
			b.Fatalf("RedactStream failed: %v", err)
		}
		if sink.Len() != len(payload) {
			b.Fatalf("output length mismatch: got %d, want %d", sink.Len(), len(payload))
		}
	}
}

// BenchmarkRedactStreamManyConcurrentStreams measures per-request
// allocation under concurrency, which is the workload the issue
// identifies as the headline motivator ("1000 concurrent streams → ~9 MB
// just for carry buffers, vs ~128 KB pre-#274").
//
// Run with `go test -bench BenchmarkRedactStreamManyConcurrentStreams
// -benchmem -benchtime=3s -cpu 1,4,16 ./pkg/bouncer/`.
func BenchmarkRedactStreamManyConcurrentStreams(b *testing.B) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	payload := bytes.Repeat([]byte("a"), 64*1024)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var sink bytes.Buffer
			if err := redactor.RedactStream(bytes.NewReader(payload), &sink); err != nil {
				b.Fatalf("RedactStream failed: %v", err)
			}
			if sink.Len() != len(payload) {
				b.Fatalf("output length mismatch: got %d, want %d", sink.Len(), len(payload))
			}
		}
	})
}
