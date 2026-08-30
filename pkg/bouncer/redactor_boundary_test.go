package bouncer

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// chunkedReader delivers at most n bytes per Read, simulating a pipe or
// socket that hands data to the proxy in small pieces.
type chunkedReader struct {
	data []byte
	n    int
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.n
	if n > len(p) {
		n = len(p)
	}
	if n > len(c.data) {
		n = len(c.data)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

// boundarySecrets covers the structurally distinct pattern shapes: fixed
// length, open-ended ({22,}), long multi-segment (JWT), and delimiter-bounded.
var boundarySecrets = []string{
	"AKIAIOSFODNN7EXAMPLE",
	"github_pat_11abcdefghIJ9xsQ_xxxxxxxxxxxxxxxxx",
	"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
	"$API_KEY=secret123",
}

// TestRedactStreamSecretAtEveryOffset places each built-in example secret at
// every byte offset around the read and scan boundaries (and sparsely
// elsewhere) and feeds the stream through readers of various sizes. No
// offset/read-size combination may leak any part of the secret.
func TestRedactStreamSecretAtEveryOffset(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	// Filler must not be in any pattern's token charset, or open-ended
	// patterns (e.g. github_pat_…{22,}) would legitimately absorb it.
	filler := []byte(" ")
	boundaries := []int{4096, 4096 + defaultMaxOverlap, 8192}
	const span = 8192 + 600

	var offsets []int
	for offset := 0; offset < span; offset++ {
		dense := false
		for _, b := range boundaries {
			if offset > b-48 && offset < b+48 {
				dense = true
				break
			}
		}
		if dense || offset%211 == 0 {
			offsets = append(offsets, offset)
		}
	}

	for _, readSize := range []int{1, 7, 4096} {
		for _, secret := range boundarySecrets {
			for _, offset := range offsets {
				// 1-byte reads are the slow path; only exercise them where
				// the boundary logic can actually differ.
				if readSize == 1 && offset%211 == 0 {
					continue
				}
				payload := bytes.Repeat(filler, offset)
				payload = append(payload, secret...)
				payload = append(payload, bytes.Repeat(filler, 300)...)

				var out bytes.Buffer
				if err := redactor.RedactStream(&chunkedReader{data: payload, n: readSize}, &out); err != nil {
					t.Fatalf("readSize=%d secret=%q offset=%d: %v", readSize, secret, offset, err)
				}
				got := out.String()
				if strings.Contains(got, secret) {
					t.Fatalf("readSize=%d secret=%q offset=%d: full secret leaked", readSize, secret, offset)
				}
				// Partial leak check: the distinctive tail of the secret must not appear.
				if tail := secret[len(secret)-8:]; strings.Contains(got, tail) {
					t.Fatalf("readSize=%d secret=%q offset=%d: partial secret leaked (%q)", readSize, secret, offset, tail)
				}
				if !strings.Contains(got, SecretRedacted) {
					t.Fatalf("readSize=%d secret=%q offset=%d: no redaction marker", readSize, secret, offset)
				}
				if wantLen := offset + len(SecretRedacted) + 300; len(got) != wantLen {
					t.Fatalf("readSize=%d secret=%q offset=%d: output length %d, want %d (bytes dropped or duplicated)", readSize, secret, offset, len(got), wantLen)
				}
			}
		}
	}
}

// TestRedactStreamPreservesCleanBytes checks that a secret-free stream passes
// through byte-for-byte regardless of read size.
func TestRedactStreamPreservesCleanBytes(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	payload := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog\n", 700))
	for _, readSize := range []int{1, 13, 4096, 1 << 16} {
		var out bytes.Buffer
		if err := redactor.RedactStream(&chunkedReader{data: payload, n: readSize}, &out); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), payload) {
			t.Fatalf("readSize=%d: clean stream altered", readSize)
		}
	}
}

// TestRedactStreamAdjacentSecrets checks that secrets back-to-back and
// secrets matched by more than one pattern are each redacted exactly once.
func TestRedactStreamAdjacentSecrets(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	payload := []byte("AKIAIOSFODNN7EXAMPLEAKIAIOSFODNN7EXAMPLE ghp_abcdefghijklmnopqrstuvwxyz1234567890")
	var out bytes.Buffer
	if err := redactor.RedactStream(bytes.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	want := SecretRedacted + SecretRedacted + " " + SecretRedacted
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

type fakeSidecar struct {
	seen   string
	reply  string
	called bool
}

func (f *fakeSidecar) Redact(_ context.Context, content string) string {
	f.called = true
	f.seen = content
	if f.reply != "" {
		return f.reply
	}
	return content
}
func (f *fakeSidecar) FallbackCount() int64           { return 0 }
func (f *fakeSidecar) Provider() string               { return "fake" }
func (f *fakeSidecar) Model() string                  { return "fake" }
func (f *fakeSidecar) Healthy(_ context.Context) bool { return true }

// TestRedactJSONWithSidecarChainsRegexFirst: when the regex layer matches a
// secret the sidecar must be skipped by default (the regex-redacted payload
// is already safe), and what is forwarded must contain no secret bytes.
func TestRedactJSONWithSidecarChainsRegexFirst(t *testing.T) {
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	sc := &fakeSidecar{}
	out, err := RedactJSONWithSidecar(context.Background(), []byte(`{"k":"AKIAIOSFODNN7EXAMPLE"}`), r, sc, false)
	if err != nil {
		t.Fatal(err)
	}
	if sc.called {
		t.Fatal("sidecar must NOT be called when regex matched and alwaysCallSidecar=false")
	}
	if strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("output leaked secret: %s", out)
	}
}

// TestRedactJSONWithSidecarChainsRegexFirst_AlwaysCall: when the operator
// opts in to `bouncer.sidecar_always_call: true`, the sidecar runs even on
// regex matches. The regex pass still runs first, so the sidecar only sees
// redacted content.
func TestRedactJSONWithSidecarChainsRegexFirst_AlwaysCall(t *testing.T) {
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	sc := &fakeSidecar{}
	out, err := RedactJSONWithSidecar(context.Background(), []byte(`{"k":"AKIAIOSFODNN7EXAMPLE"}`), r, sc, true)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.called {
		t.Fatal("sidecar must be called when alwaysCallSidecar=true")
	}
	if strings.Contains(sc.seen, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("sidecar received unredacted secret: %s", sc.seen)
	}
	if strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("output leaked secret: %s", out)
	}
}

// TestRedactJSONWithSidecarRejectsNonJSON: a sidecar that returns prose (or
// an LLM that echoes garbage) must not become the request params.
func TestRedactJSONWithSidecarRejectsNonJSON(t *testing.T) {
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	sc := &fakeSidecar{reply: "Sure! Here is the redacted content."}
	_, err := RedactJSONWithSidecar(context.Background(), []byte(`{"k":"v"}`), r, sc, false)
	if err == nil {
		t.Fatal("expected error for non-JSON sidecar output")
	}
}
