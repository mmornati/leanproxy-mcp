package cache

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestNewProviderDetectorDefaults(t *testing.T) {
	d := NewProviderDetector()
	if d == nil {
		t.Fatal("expected non-nil detector")
	}
	tests := []struct {
		url      string
		expected Provider
	}{
		{"https://api.anthropic.com/v1/messages", ProviderAnthropic},
		{"https://api.anthropic.com/v1/complete", ProviderAnthropic},
		{"http://api.anthropic.com/v1/messages", ProviderAnthropic},
		{"https://api.anthropic.com", ProviderAnthropic},
		{"https://openai.com/v1/chat", ProviderOther},
		{"https://example.com/api", ProviderOther},
		{"", ProviderOther},
		{"http://localhost:8080/mcp", ProviderOther},
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

func TestConcurrentAccess(t *testing.T) {
	d := NewProviderDetector()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Detect("https://api.anthropic.com/v1/messages")
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			d.LoadReader(strings.NewReader(`providers:
  - name: test
    patterns:
      - "https://test.com"
`))
		}
	}()
	wg.Wait()
}

func TestDetectCaseSensitivity(t *testing.T) {
	d := NewProviderDetector()
	d.LoadReader(strings.NewReader(`providers:
  - name: MyCustom
    patterns:
      - "https://custom.api.com"
`))
	if got := d.Detect("https://CUSTOM.API.COM/v1"); got != ProviderOther {
		t.Errorf("expected case-sensitive match to fail, got %q", got)
	}
	if got := d.Detect("https://custom.api.com/v1"); got != Provider("mycustom") {
		t.Errorf("expected lowercase provider name, got %q", got)
	}
}

func TestWithLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewProviderDetector(WithLogger(logger))
	d.Reload()
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
	for i := 0; i < b.N; i++ {
		_ = d.Detect(url)
	}
}
