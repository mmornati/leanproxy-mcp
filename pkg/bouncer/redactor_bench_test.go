package bouncer

import (
	"bytes"
	"io"
	"log/slog"
	"strconv"
	"testing"
)

// quietLogs silences the default logger for the duration of a benchmark so
// per-call "redaction complete" lines do not interleave with results.
func quietLogs(b *testing.B) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(prev) })
}

// buildStructuredPayload returns a JSON document of roughly size bytes that
// looks like a typical MCP tool result: an array of records with ids,
// strings, numbers (including integers beyond float64 precision), nested
// objects, unicode and HTML-significant characters. It contains no secrets.
func buildStructuredPayload(size int) []byte {
	var b bytes.Buffer
	b.Grow(size + 1024)
	b.WriteString(`{"items":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		n := strconv.Itoa(i)
		b.WriteString(`{"id":`)
		b.WriteString(n)
		b.WriteString(`,"uuid":"3f2b8c1e-9a4d-4e7b-8c21-`)
		b.WriteString(strconv.FormatInt(100000000000+int64(i), 10))
		b.WriteString(`","name":"Item `)
		b.WriteString(n)
		b.WriteString(` — café <b>&amp;</b>","big":12345678901234567,"price":19.990,"active":true,"owner":null,`)
		b.WriteString(`"tags":["alpha","beta","gamma"],"meta":{"created_at":"2024-01-02T03:04:05Z","sha":"9fceb02d0ae598e95dc970b74767f19372d61af8",`)
		b.WriteString(`"description":"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.",`)
		b.WriteString(`"path":"src/pkg/module_`)
		b.WriteString(n)
		b.WriteString(`/handler.go","score":0.7312}}`)
	}
	b.WriteString(`],"total":`)
	b.WriteString(strconv.Itoa(size))
	b.WriteString(`}`)
	return b.Bytes()
}

func BenchmarkRedactJSONStructured20MB(b *testing.B) {
	data := buildStructuredPayload(20 << 20)
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	quietLogs(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := r.RedactJSON(data); err != nil {
			b.Fatal(err)
		}
	}
}
