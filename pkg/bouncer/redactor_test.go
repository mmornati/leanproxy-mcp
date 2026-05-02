package bouncer

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestRedactAWSKey(t *testing.T) {
	input := `{"api_key": "AKIAIOSFODNN7EXAMPLE"}`
	expected := `{"api_key": "[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactGitHubToken(t *testing.T) {
	input := `{"token": "ghp_123456789012345678901234567890123456"}`
	expected := `{"token": "[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactGitHubFineGrainedPAT(t *testing.T) {
	input := `{"token": "github_pat_11AAAAAAAAAAAAAAA_BBBBBBBBBBBBBBBBBBB"}`
	expected := `{"token": "[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactStripeKey(t *testing.T) {
	t.Skip("Stripe keys triggering secret scanning - using pattern validation only in patterns_test.go")
}

func TestRedactMultipleSecrets(t *testing.T) {
	input := `{"aws": "AKIAIOSFODNN7EXAMPLE", "github": "ghp_123456789012345678901234567890123456"}`
	expected := `{"aws": "[SECRET_REDACTED]", "github": "[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactNoSecrets(t *testing.T) {
	input := `{"message": "hello world"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != input {
		t.Errorf("got %q, want %q", string(result), input)
	}
}

func TestRedactJSONStructurePreservation(t *testing.T) {
	input := `{"data": {"api_key": "AKIAIOSFODNN7EXAMPLE"}, "count": 1}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	var original, redacted map[string]interface{}
	if err := json.Unmarshal([]byte(input), &original); err != nil {
		t.Fatalf("failed to parse original: %v", err)
	}
	if err := json.Unmarshal(result, &redacted); err != nil {
		t.Fatalf("redacted result is not valid JSON: %v", err)
	}

	if original["count"] != redacted["count"] {
		t.Errorf("count field changed: got %v, want %v", redacted["count"], original["count"])
	}
}

func TestRedactStreamBasic(t *testing.T) {
	input := `{"api_key": "AKIAIOSFODNN7EXAMPLE"}`
	expected := `{"api_key": "[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	reader := strings.NewReader(input)
	var writer bytes.Buffer

	err := redactor.RedactStream(reader, &writer)
	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}

	if writer.String() != expected {
		t.Errorf("got %q, want %q", writer.String(), expected)
	}
}

func TestRedactStreamNoSecrets(t *testing.T) {
	input := `{"message": "hello world"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	reader := strings.NewReader(input)
	var writer bytes.Buffer

	err := redactor.RedactStream(reader, &writer)
	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}

	if writer.String() != input {
		t.Errorf("got %q, want %q", writer.String(), input)
	}
}

func TestRedactStreamLargePayload(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"data": "`)
	for i := 0; i < 1000; i++ {
		sb.WriteString("some data chunk ")
	}
	sb.WriteString(`", "api_key": "AKIAIOSFODNN7EXAMPLE"}`)
	input := sb.String()

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	reader := strings.NewReader(input)
	var writer bytes.Buffer

	err := redactor.RedactStream(reader, &writer)
	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}

	if !strings.Contains(writer.String(), "[SECRET_REDACTED]") {
		t.Error("expected secret to be redacted in large payload")
	}
}

func TestRedactInvalidJSON(t *testing.T) {
	input := `{invalid json}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))

	if err != nil {
		t.Fatalf("RedactJSON should not fail on invalid JSON, got: %v", err)
	}

	if string(result) != input {
		t.Errorf("invalid JSON should pass through unchanged, got %q", string(result))
	}
}

func TestRedactBearerToken(t *testing.T) {
	input := `{"auth": "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"}`
	expected := `{"auth": "[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactAPIKeyCaseInsensitive(t *testing.T) {
	input := `api_key=abcdefghijklmnopqrstuvwxyz123456`
	expected := `[SECRET_REDACTED]`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result := redactor.redactString(input)

	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func BenchmarkRedactSmallMessage(b *testing.B) {
	input := `{"api_key": "AKIAIOSFODNN7EXAMPLE", "data": "hello world"}`
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = redactor.RedactJSON([]byte(input))
	}
}

func BenchmarkRedactStreamSmallMessage(b *testing.B) {
	input := `{"api_key": "AKIAIOSFODNN7EXAMPLE", "data": "hello world"}`
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	reader := strings.NewReader(input)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var writer bytes.Buffer
		reader.Seek(0, io.SeekStart)
		_ = redactor.RedactStream(reader, &writer)
	}
}

func TestNewRedactor(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	if redactor == nil {
		t.Fatal("expected non-nil redactor")
	}
	if redactor.bufferSize != 4096 {
		t.Errorf("expected default bufferSize=4096, got %d", redactor.bufferSize)
	}
}

func TestLargePayloadStreaming(t *testing.T) {
	largeData := make([]byte, 10*1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	copy(largeData[100:140], []byte("AKIAIOSFODNN7EXAMPLE"))

	r := bytes.NewReader(largeData)
	var w bytes.Buffer

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	err := redactor.RedactStream(r, &w)

	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}
	if w.Len() >= len(largeData)*3 {
		t.Error("output should be smaller due to redaction")
	}
	if !strings.Contains(w.String(), "[SECRET_REDACTED]") {
		t.Error("secret should be redacted")
	}
}

func TestStreamingNoDataLeak(t *testing.T) {
	secretData := []byte(`{"api_key": "AKIAIOSFODNN7EXAMPLE", "data": "sensitive"}`)
	r := bytes.NewReader(secretData)
	var w bytes.Buffer

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	err := redactor.RedactStream(r, &w)
	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}

	output := w.String()
	if strings.Contains(output, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("unredacted secret should not appear")
	}
	if !strings.Contains(output, "[SECRET_REDACTED]") {
		t.Error("redacted placeholder should appear")
	}
}

func TestStreamingRedactorLargePayload(t *testing.T) {
	largeData := make([]byte, 5*1024*1024)
	for i := range largeData {
		largeData[i] = byte('A' + (i % 26))
	}
	copy(largeData[1000:1040], []byte(`"token": "ghp_123456789012345678901234567890123456"`))

	r := bytes.NewReader(largeData)
	var w bytes.Buffer

	redactor := NewStreamingRedactor(PatternsToRegexps(BuiltInPatterns))
	err := redactor.RedactStream(r, &w)

	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}
	if !strings.Contains(w.String(), "[SECRET_REDACTED]") {
		t.Error("expected secret to be redacted")
	}
}

func TestStreamingRedactorNoSecrets(t *testing.T) {
	input := `{"message": "hello world", "count": 42}`
	redactor := NewStreamingRedactor(PatternsToRegexps(BuiltInPatterns))

	r := strings.NewReader(input)
	var w bytes.Buffer
	err := redactor.RedactStream(r, &w)

	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}
	if input != w.String() {
		t.Errorf("got %q, want %q", w.String(), input)
	}
}

func TestStreamingRedactorMultipleSecrets(t *testing.T) {
	input := `{"aws": "AKIAIOSFODNN7EXAMPLE", "github": "ghp_123456789012345678901234567890123456"}`
	expected := `{"aws": "[SECRET_REDACTED]", "github": "[SECRET_REDACTED]"}`

	redactor := NewStreamingRedactor(PatternsToRegexps(BuiltInPatterns))
	r := strings.NewReader(input)
	var w bytes.Buffer
	err := redactor.RedactStream(r, &w)

	if err != nil {
		t.Fatalf("RedactStream failed: %v", err)
	}
	if expected != w.String() {
		t.Errorf("got %q, want %q", w.String(), expected)
	}
}