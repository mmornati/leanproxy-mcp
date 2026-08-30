package bouncer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestRedactAWSKey(t *testing.T) {
	input := `{"api_key": "AKIAIOSFODNN7EXAMPLE"}`
	expected := `{"api_key":"[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, _, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactGitHubToken(t *testing.T) {
	input := `{"token": "ghp_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}`
	expected := `{"token":"[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, _, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactGitHubFineGrainedPAT(t *testing.T) {
	input := `{"token": "github_pat_11XXXXXXXXXXXXXXXX_XXXXXXXXXXXXXXXXXXXX"}`
	expected := `{"token":"[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, _, err := redactor.RedactJSON([]byte(input))
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
	input := `{"aws": "AKIAIOSFODNN7EXAMPLE", "github": "ghp_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}`
	expected := `{"aws":"[SECRET_REDACTED]","github":"[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, _, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactNoSecrets(t *testing.T) {
	input := `{"message": "hello world"}`
	expected := `{"message":"hello world"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, _, err := redactor.RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON failed: %v", err)
	}

	if string(result) != expected {
		t.Errorf("got %q, want %q", string(result), expected)
	}
}

func TestRedactJSONStructurePreservation(t *testing.T) {
	input := `{"data": {"api_key": "AKIAIOSFODNN7EXAMPLE"}, "count": 1}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, _, err := redactor.RedactJSON([]byte(input))
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
	// Malformed JSON must not be a bypass: the redactor falls back to a
	// byte-level scan instead of passing the input through unchanged.
	input := `{"a":"AKIAIOSFODNN7EXAMPLE"` // truncated, unparseable

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, count, err := redactor.RedactJSON([]byte(input))

	if err != nil {
		t.Fatalf("RedactJSON should not fail on invalid JSON, got: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 secret redacted in fallback mode, got %d", count)
	}
	if strings.Contains(string(result), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("secret leaked through invalid-JSON fallback: %q", string(result))
	}
	if !strings.Contains(string(result), SecretRedacted) {
		t.Errorf("expected redaction marker in output, got %q", string(result))
	}
}

func TestRedactBearerToken(t *testing.T) {
	input := `{"auth": "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"}`
	expected := `{"auth":"[SECRET_REDACTED]"}`

	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	result, _, err := redactor.RedactJSON([]byte(input))
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
	result, _ := redactor.redactString(input)

	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func BenchmarkRedactSmallMessage(b *testing.B) {
	input := `{"api_key": "AKIAIOSFODNN7EXAMPLE", "data": "hello world"}`
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = redactor.RedactJSON([]byte(input))
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
	copy(largeData[1000:1040], []byte(`"token": "ghp_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"`))

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
	input := `{"aws": "AKIAIOSFODNN7EXAMPLE", "github": "ghp_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}`
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

type mockSidecarClient struct {
	redactFunc func(ctx context.Context, content string) string
}

func (m *mockSidecarClient) Redact(ctx context.Context, content string) string {
	if m.redactFunc != nil {
		return m.redactFunc(ctx, content)
	}
	return content
}

func (m *mockSidecarClient) FallbackCount() int64             { return 0 }
func (m *mockSidecarClient) Provider() string                 { return "test" }
func (m *mockSidecarClient) Model() string                    { return "test" }
func (m *mockSidecarClient) Healthy(ctx context.Context) bool { return true }

func TestRedactJSONWithSidecar_RegexMatches(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	// The sidecar runs after the regex pass and must only ever see
	// already-redacted content.
	sidecar := &mockSidecarClient{
		redactFunc: func(ctx context.Context, content string) string {
			if strings.Contains(content, "AKIAIOSFODNN7EXAMPLE") {
				t.Error("sidecar received unredacted secret")
			}
			return content
		},
	}
	input := []byte(`{"key": "AKIAIOSFODNN7EXAMPLE"}`)
	result, err := RedactJSONWithSidecar(context.Background(), input, redactor, sidecar, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar failed: %v", err)
	}
	if !strings.Contains(string(result), SecretRedacted) {
		t.Errorf("expected regex-redacted result, got %q", string(result))
	}
}

func TestRedactJSONWithSidecar_RegexNoMatch_SidecarCalled(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	var sidecarCalled bool
	sidecar := &mockSidecarClient{
		redactFunc: func(ctx context.Context, content string) string {
			sidecarCalled = true
			return `{"key":"sidecar_redacted"}`
		},
	}
	input := []byte(`{"key": "safe_value"}`)
	result, err := RedactJSONWithSidecar(context.Background(), input, redactor, sidecar, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar failed: %v", err)
	}
	if !sidecarCalled {
		t.Error("expected sidecar to be called when regex finds no matches")
	}
	if string(result) != `{"key":"sidecar_redacted"}` {
		t.Errorf("expected sidecar redacted result, got %q", string(result))
	}
}

func TestRedactJSONWithSidecar_NilRedactor_CallsSidecar(t *testing.T) {
	var sidecarCalled bool
	sidecar := &mockSidecarClient{
		redactFunc: func(ctx context.Context, content string) string {
			sidecarCalled = true
			return `{"key":"sidecar_redacted"}`
		},
	}
	input := []byte(`{"key": "value"}`)
	result, err := RedactJSONWithSidecar(context.Background(), input, nil, sidecar, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar failed: %v", err)
	}
	if !sidecarCalled {
		t.Error("expected sidecar to be called when redactor is nil")
	}
	if string(result) != `{"key":"sidecar_redacted"}` {
		t.Errorf("expected sidecar redacted result, got %q", string(result))
	}
}

func TestRedactJSONWithSidecar_NilBoth(t *testing.T) {
	input := []byte(`{"key": "value"}`)
	result, err := RedactJSONWithSidecar(context.Background(), input, nil, nil, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar failed: %v", err)
	}
	if string(result) != `{"key": "value"}` {
		t.Errorf("expected passthrough, got %q", string(result))
	}
}

func TestRedactJSONWithSidecar_NoSidecar_Passthrough(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	input := []byte(`{"key": "safe_value"}`)
	result, err := RedactJSONWithSidecar(context.Background(), input, redactor, nil, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar failed: %v", err)
	}
	if !strings.Contains(string(result), `safe_value`) {
		t.Errorf("expected passthrough, got %q", string(result))
	}
}

func TestRedactJSONWithSidecar_CountTracking(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	sidecar := &mockSidecarClient{
		redactFunc: func(ctx context.Context, content string) string {
			if strings.Contains(content, "AKIA") || strings.Contains(content, "ghp_") {
				t.Error("sidecar received unredacted secret")
			}
			return content
		},
	}
	input := []byte(`{"aws": "AKIAIOSFODNN7EXAMPLE", "github": "ghp_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}`)
	result, err := RedactJSONWithSidecar(context.Background(), input, redactor, sidecar, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar failed: %v", err)
	}
	c := strings.Count(string(result), SecretRedacted)
	if c != 2 {
		t.Errorf("expected 2 redacted tokens, got %d", c)
	}
}

// TestRedactJSONWithSidecar_AcceptsValueRedactedSentinel: when the LLM is
// unreachable / errors out, the sidecar falls back to returning the literal
// "[VALUE_REDACTED]" sentinel. That is not valid JSON but it must NOT cause
// every request to be rejected — the regex layer has already cleaned the
// input by this point, so the regex-redacted payload is forwarded.
func TestRedactJSONWithSidecar_AcceptsValueRedactedSentinel(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	sidecar := &mockSidecarClient{
		redactFunc: func(ctx context.Context, content string) string {
			return SidecarFallback
		},
	}
	input := []byte(`{"key": "value"}`)
	out, err := RedactJSONWithSidecar(context.Background(), input, redactor, sidecar, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar should accept the fallback sentinel, got: %v", err)
	}
	if string(out) != `{"key":"value"}` {
		t.Errorf("expected regex-redacted passthrough, got %q", string(out))
	}
}

// TestRedactJSONWithSidecar_AcceptsEmptyOutput: a sidecar that returns an
// empty string (e.g. empty `response` field from Ollama) is treated the
// same as the sentinel fallback.
func TestRedactJSONWithSidecar_AcceptsEmptyOutput(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	sidecar := &mockSidecarClient{
		redactFunc: func(ctx context.Context, content string) string {
			return ""
		},
	}
	input := []byte(`{"key": "value"}`)
	out, err := RedactJSONWithSidecar(context.Background(), input, redactor, sidecar, false)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar should accept empty output, got: %v", err)
	}
	if string(out) != `{"key":"value"}` {
		t.Errorf("expected regex-redacted passthrough, got %q", string(out))
	}
}

// TestRedactJSONWithSidecar_AlwaysCallCallsSidecarOnRegexMatch: when the
// operator opts in via alwaysCallSidecar=true, the sidecar runs even
// though the regex layer matched.
func TestRedactJSONWithSidecar_AlwaysCallCallsSidecarOnRegexMatch(t *testing.T) {
	redactor := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	var sidecarCalled bool
	sidecar := &mockSidecarClient{
		redactFunc: func(ctx context.Context, content string) string {
			sidecarCalled = true
			if strings.Contains(content, "AKIAIOSFODNN7EXAMPLE") {
				t.Error("sidecar received unredacted secret in always-call mode")
			}
			return `{"key":"sidecar_value"}`
		},
	}
	input := []byte(`{"key": "AKIAIOSFODNN7EXAMPLE"}`)
	out, err := RedactJSONWithSidecar(context.Background(), input, redactor, sidecar, true)
	if err != nil {
		t.Fatalf("RedactJSONWithSidecar failed: %v", err)
	}
	if !sidecarCalled {
		t.Fatal("sidecar must be called when alwaysCallSidecar=true even after regex match")
	}
	if string(out) != `{"key":"sidecar_value"}` {
		t.Errorf("expected sidecar output, got %q", string(out))
	}
}
