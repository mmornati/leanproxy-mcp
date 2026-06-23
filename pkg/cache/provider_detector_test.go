package cache

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewProviderDetectorDefaults(t *testing.T) {
	d := NewProviderDetector()
	if d == nil {
		t.Fatal("expected non-nil detector")
	}
	tests := []struct {
		name     string
		url      string
		expected Provider
	}{
		{"anthropic https messages", "https://api.anthropic.com/v1/messages", ProviderAnthropic},
		{"anthropic https complete", "https://api.anthropic.com/v1/complete", ProviderAnthropic},
		{"anthropic http messages", "http://api.anthropic.com/v1/messages", ProviderAnthropic},
		{"anthropic bare host", "https://api.anthropic.com", ProviderAnthropic},
		{"anthropic with port", "https://api.anthropic.com:8443/v1/messages", ProviderAnthropic},
		{"anthropic subdomain matches", "https://internal.api.anthropic.com/v1", ProviderAnthropic},
		{"anthropic different host prefix", "https://api.anthropic.com.foo.example/v1", ProviderOther},
		{"anthropic exact attack domain", "https://api.anthropic.com.evil.com/v1", ProviderOther},
		{"anthropic exact attack no path", "https://api.anthropic.com.evil.com", ProviderOther},
		{"anthropic similar prefix", "https://api.anthropic.comX", ProviderOther},
		{"openai non-anthropic", "https://openai.com/v1/chat", ProviderOther},
		{"example non-anthropic", "https://example.com/api", ProviderOther},
		{"empty url", "", ProviderOther},
		{"localhost", "http://localhost:8080/mcp", ProviderOther},
		{"whitespace url", "   ", ProviderOther},
		{"url with spaces inside", "https://api.anthropic.com /v1", ProviderOther},
		{"case-insensitive host", "HTTPS://API.ANTHROPIC.COM/v1", ProviderAnthropic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.Detect(tt.url)
			if got != tt.expected {
				t.Errorf("Detect(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

func TestDetectCustomProvider(t *testing.T) {
	d := NewProviderDetector()
	cfg := `providers:
  - name: openai
    patterns:
      - "https://api.openai.com"
      - "https://oapi.openai.com"
  - name: google
    patterns:
      - "https://generativelanguage.googleapis.com"
`
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	tests := []struct {
		url      string
		expected Provider
	}{
		{"https://api.openai.com/v1/chat/completions", Provider("openai")},
		{"https://oapi.openai.com/v1/embeddings", Provider("openai")},
		{"https://generativelanguage.googleapis.com/v1/models", Provider("google")},
		{"https://api.anthropic.com/v1/messages", ProviderAnthropic},
		{"https://example.com", ProviderOther},
		{"https://api.openai.com.evil.com/v1", ProviderOther},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := d.Detect(tt.url)
			if got != tt.expected {
				t.Errorf("Detect(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	d := NewProviderDetector()
	err := d.LoadReader(strings.NewReader("{{invalid yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadEmptyConfig(t *testing.T) {
	d := NewProviderDetector()
	if err := d.LoadReader(strings.NewReader("providers: []")); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	if got := d.Detect("https://api.anthropic.com/v1/messages"); got != ProviderAnthropic {
		t.Errorf("after empty config, Detect = %q, want %q", got, ProviderAnthropic)
	}
}

func TestLoadEmptyPatternEntriesSkipped(t *testing.T) {
	d := NewProviderDetector()
	cfg := `providers:
  - name: empty_pat
    patterns:
      - ""
      - "   "
  - name: valid
    patterns:
      - "https://example.test"
`
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	if got := d.Detect("https://api.anthropic.com/v1/messages"); got != ProviderAnthropic {
		t.Errorf("anthropic should still match, got %q", got)
	}
	if got := d.Detect("https://example.test/v1"); got != Provider("valid") {
		t.Errorf("valid provider should match, got %q", got)
	}
}

func TestLoadInvalidProviderNameSkipped(t *testing.T) {
	d := NewProviderDetector()
	cfg := `providers:
  - name: "Bad Name With Spaces"
    patterns:
      - "https://example.test"
  - name: "ok_name"
    patterns:
      - "https://ok.test"
`
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	if got := d.Detect("https://example.test/v1"); got != ProviderOther {
		t.Errorf("invalid provider name should be skipped, got %q", got)
	}
	if got := d.Detect("https://ok.test/v1"); got != Provider("ok_name") {
		t.Errorf("valid provider should match, got %q", got)
	}
}

func TestLoadRejectsOtherProviderName(t *testing.T) {
	d := NewProviderDetector()
	cfg := `providers:
  - name: other
    patterns:
      - "https://anywhere.test"
`
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	if got := d.Detect("https://anywhere.test/v1"); got != ProviderOther {
		t.Errorf("'other' name should be rejected (reserved), got %q", got)
	}
}

func TestLoadRejectsDuplicateProviderName(t *testing.T) {
	d := NewProviderDetector()
	cfg := `providers:
  - name: dup
    patterns:
      - "https://first.test"
  - name: dup
    patterns:
      - "https://second.test"
`
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	if got := d.Detect("https://first.test/v1"); got != Provider("dup") {
		t.Errorf("first provider should match, got %q", got)
	}
	if got := d.Detect("https://second.test/v1"); got != ProviderOther {
		t.Errorf("second provider's patterns should be ignored after duplicate-name rejection, got %q", got)
	}
}

func TestLoadRejectsCustomAnthropicOverride(t *testing.T) {
	d := NewProviderDetector()
	cfg := `providers:
  - name: anthropic
    patterns:
      - "https://malicious.example.test"
`
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	if got := d.Detect("https://malicious.example.test/v1"); got != ProviderOther {
		t.Errorf("malicious URL should not be misclassified as anthropic (override rejected), got %q", got)
	}
	if got := d.Detect("https://api.anthropic.com/v1"); got != ProviderAnthropic {
		t.Errorf("real Anthropic URL must still match built-in, got %q", got)
	}
}

func TestLoadReaderHugeConfigRejected(t *testing.T) {
	d := NewProviderDetector()
	big := strings.Repeat("x", maxConfigBytes+10)
	if err := d.LoadReader(strings.NewReader(big)); err == nil {
		t.Fatal("expected error for oversize config")
	}
}

func TestConcurrentAccess(t *testing.T) {
	d := NewProviderDetector()
	var wg sync.WaitGroup
	var detected atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if d.Detect("https://api.anthropic.com/v1/messages") == ProviderAnthropic {
					detected.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "swap"
			_ = i
			_ = d.LoadReader(strings.NewReader(`providers:
  - name: ` + name + `
    patterns:
      - "https://swap.test"
`))
		}(i)
	}
	wg.Wait()
	if detected.Load() == 0 {
		t.Fatal("expected at least some anthropic detections during concurrent reloads")
	}
}

func TestReloadErrorKeepsOldPatterns(t *testing.T) {
	d := NewProviderDetector()
	good := `providers:
  - name: keep
    patterns:
      - "https://keep.test"
`
	if err := d.LoadReader(strings.NewReader(good)); err != nil {
		t.Fatalf("LoadReader failed: %v", err)
	}
	if got := d.Detect("https://keep.test/v1"); got != Provider("keep") {
		t.Fatalf("expected keep provider, got %q", got)
	}
	if err := d.LoadReader(strings.NewReader("not yaml at all {")); err == nil {
		t.Fatal("expected error for invalid yaml")
	}
	if got := d.Detect("https://keep.test/v1"); got != Provider("keep") {
		t.Errorf("patterns should be retained after failed reload, got %q", got)
	}
}

func TestReloadReturnsError(t *testing.T) {
	d := NewProviderDetector(WithConfigPath("/does/not/exist.yaml"))
	if err := d.Reload(); err == nil {
		t.Fatal("expected error when config path is missing")
	}
}

func TestReloadSurvivesNilReader(t *testing.T) {
	d := NewProviderDetector(WithConfigPath("/marker/path.yaml"))
	d.configFunc = func(string) (io.ReadCloser, error) {
		return nil, nil
	}
	if err := d.Reload(); err == nil {
		t.Fatal("expected error from nil reader")
	}
	if d.Detect("https://api.anthropic.com/v1/messages") != ProviderAnthropic {
		t.Fatal("default patterns should still be active after failed reload")
	}
}

func TestDetectCaseInsensitiveHost(t *testing.T) {
	d := NewProviderDetector()
	if got := d.Detect("HTTPS://API.ANTHROPIC.COM/v1/messages"); got != ProviderAnthropic {
		t.Errorf("expected case-insensitive host match, got %q", got)
	}
}

func TestWithLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewProviderDetector(WithLogger(logger))
	if err := d.Reload(); err != nil {
		t.Fatalf("unexpected reload error: %v", err)
	}
	if !strings.Contains(buf.String(), "no config path set") {
		t.Errorf("expected log message about missing config path, got: %s", buf.String())
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestLoadReaderReadError(t *testing.T) {
	d := NewProviderDetector()
	err := d.LoadReader(errorReader{})
	if err == nil {
		t.Fatal("expected error from failed read")
	}
}

func TestNewProviderDetectorWithOptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewProviderDetector(
		WithLogger(logger),
		WithConfigPath("/nonexistent/config.yaml"),
	)
	if d.logger != logger {
		t.Error("logger option not applied")
	}
	if d.configPath != "/nonexistent/config.yaml" {
		t.Error("config path option not applied")
	}
}

func TestWithConfigReader(t *testing.T) {
	tmp, err := os.CreateTemp("", "providers-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("providers:\n  - name: injected\n    patterns:\n      - \"https://injected.test\"\n"); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	var probedPath atomic.Value
	d := NewProviderDetector(
		WithConfigPath("/some/marker/path.yaml"),
		WithConfigReader(func(path string) (io.ReadCloser, error) {
			probedPath.Store(path)
			return os.Open(tmp.Name())
		}),
	)
	if err := d.Load(); err != nil {
		t.Fatalf("Load via WithConfigReader failed: %v", err)
	}
	if got, _ := probedPath.Load().(string); got != "/some/marker/path.yaml" {
		t.Errorf("expected WithConfigReader to receive configured path, got %q", got)
	}
	if got := d.Detect("https://injected.test/v1"); got != Provider("injected") {
		t.Errorf("expected injected provider, got %q", got)
	}
}

func TestWithConfigReaderNilIgnored(t *testing.T) {
	d := NewProviderDetector(WithConfigReader(nil))
	if d.configFunc == nil {
		t.Fatal("default configFunc should remain when nil is passed")
	}
	if _, err := d.configFunc("/dev/null"); err != nil {
		t.Fatalf("default configFunc should still work, got %v", err)
	}
}

func TestLoadViaTempFile(t *testing.T) {
	tmp, err := os.CreateTemp("", "providers-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("providers:\n  - name: file_loaded\n    patterns:\n      - \"https://file.test\"\n"); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	d := NewProviderDetector(WithConfigPath(tmp.Name()))
	if got := d.Detect("https://file.test/v1"); got != Provider("file_loaded") {
		t.Errorf("expected file_loaded provider, got %q", got)
	}
}

func TestLoadMissingFileWarns(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewProviderDetector(
		WithLogger(logger),
		WithConfigPath("/does/not/exist.yaml"),
	)
	if got := d.Detect("https://api.anthropic.com/v1"); got != ProviderAnthropic {
		t.Errorf("default anthropic pattern should still match, got %q", got)
	}
	if !strings.Contains(buf.String(), "failed to load config") {
		t.Errorf("expected warn about failed load, got: %s", buf.String())
	}
}

func TestLoadHandlesNilReader(t *testing.T) {
	d := NewProviderDetector()
	d.configFunc = func(string) (io.ReadCloser, error) {
		return nil, errors.New("boom")
	}
	if err := d.Load(); err == nil {
		t.Fatal("expected error from nil reader")
	}
}

func TestReloadWithoutConfigPathIsNoop(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewProviderDetector(WithLogger(logger))
	if err := d.Reload(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "no config path set") {
		t.Errorf("expected warn about no config path, got: %s", buf.String())
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		in       string
		expected string
		ok       bool
	}{
		{"https://api.anthropic.com/v1/messages", "api.anthropic.com", true},
		{"https://api.anthropic.com:8443/v1", "api.anthropic.com", true},
		{"HTTPS://API.ANTHROPIC.COM/v1", "api.anthropic.com", true},
		{"  https://api.anthropic.com  ", "api.anthropic.com", true},
		{"", "", false},
		{"   ", "", false},
		{"not a url", "", false},
		{"https://api.anthropic.com spaces/x", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			host, ok := extractHost(tt.in)
			if host != tt.expected || ok != tt.ok {
				t.Errorf("extractHost(%q) = (%q, %v), want (%q, %v)", tt.in, host, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestPatternsCopiedAfterLoad(t *testing.T) {
	d := NewProviderDetector()
	cfg := `providers:
  - name: foo
    patterns:
      - "https://foo.test"
`
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatal(err)
	}
	d.mu.RLock()
	first := d.patterns[len(d.patterns)-1].prefixes[0]
	d.mu.RUnlock()
	if err := d.LoadReader(strings.NewReader(cfg)); err != nil {
		t.Fatal(err)
	}
	d.mu.RLock()
	second := d.patterns[len(d.patterns)-1].prefixes[0]
	d.mu.RUnlock()
	if first != second {
		t.Errorf("expected stable pattern after reload, got %q vs %q", first, second)
	}
}

func TestParseURLDefensive(t *testing.T) {
	if _, err := url.Parse("https://api.anthropic.com/v1"); err != nil {
		t.Fatalf("baseline url.Parse failed: %v", err)
	}
}

func BenchmarkDetectAnthropic(b *testing.B) {
	d := NewProviderDetector()
	url := "https://api.anthropic.com/v1/messages"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Detect(url)
	}
}

func BenchmarkDetectOther(b *testing.B) {
	d := NewProviderDetector()
	url := "https://openai.com/v1/chat/completions"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Detect(url)
	}
}

func BenchmarkDetectWithCustomProviders(b *testing.B) {
	d := NewProviderDetector()
	d.LoadReader(strings.NewReader(`providers:
  - name: openai
    patterns:
      - "https://api.openai.com"
      - "https://oapi.openai.com"
      - "https://api.openai.com/v1"
  - name: google
    patterns:
      - "https://generativelanguage.googleapis.com"
  - name: azure
    patterns:
      - "https://.openai.azure.com"
`))
	url := "https://api.anthropic.com/v1/messages"
	b.ResetTimer()
	for i := 0; b.N > i; i++ {
		_ = d.Detect(url)
	}
}
