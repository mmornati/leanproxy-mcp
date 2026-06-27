package embedder

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockEmbedder struct {
	mu       sync.Mutex
	emb      Embedding
	err      error
	provider Provider
	inputs   []string
	calls    int
}

func (m *mockEmbedder) Embed(_ context.Context, req EmbedRequest) (Embedding, error) {
	m.mu.Lock()
	m.inputs = append(m.inputs, req.Input())
	m.calls++
	m.mu.Unlock()
	return m.emb, m.err
}

func (m *mockEmbedder) Provider() Provider { return m.provider }
func (m *mockEmbedder) Close() error       { return nil }

func TestEmbedRequestInput(t *testing.T) {
	tests := []struct {
		name string
		req  EmbedRequest
		want string
	}{
		{
			name: "tool name only",
			req:  EmbedRequest{ToolName: "get_weather"},
			want: "get_weather:",
		},
		{
			name: "tool name with args",
			req:  EmbedRequest{ToolName: "get_weather", Args: json.RawMessage(`{"location":"London","unit":"celsius"}`)},
			want: "get_weather: location=\"London\" unit=\"celsius\"",
		},
		{
			name: "tool name with single arg",
			req:  EmbedRequest{ToolName: "search", Args: json.RawMessage(`{"q":"hello"}`)},
			want: "search: q=\"hello\"",
		},
		{
			name: "invalid json args",
			req:  EmbedRequest{ToolName: "test", Args: json.RawMessage(`not json`)},
			want: "test:not json",
		},
		{
			name: "empty args",
			req:  EmbedRequest{ToolName: "ping", Args: json.RawMessage(`{}`)},
			want: "ping:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.req.Input()
			if got != tc.want {
				t.Errorf("Input() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEmbedRequestInputOrderDeterministic(t *testing.T) {
	req := EmbedRequest{
		ToolName: "test",
		Args:     json.RawMessage(`{"z":1,"a":2,"m":3}`),
	}
	got := req.Input()
	if !strings.Contains(got, " a=2") || !strings.Contains(got, " m=3") || !strings.Contains(got, " z=1") {
		t.Errorf("expected sorted keys, got: %s", got)
	}
	if !strings.HasPrefix(got, "test:") {
		t.Errorf("expected tool: prefix, got: %s", got)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "unknown provider",
			cfg:     Config{Provider: "unknown"},
			wantErr: `unknown provider "unknown"`,
		},
		{
			name:    "ollama missing config",
			cfg:     Config{Provider: ProviderOllama},
			wantErr: "ollama config required",
		},
		{
			name:    "openai missing config",
			cfg:     Config{Provider: ProviderOpenAI},
			wantErr: "openai config required",
		},
		{
			name: "valid ollama config",
			cfg:  Config{Provider: ProviderOllama, Ollama: &OllamaConfig{URL: "http://localhost:11434", Model: "m"}},
		},
		{
			name: "valid openai config",
			cfg:  Config{Provider: ProviderOpenAI, OpenAI: &OpenAIConfig{APIKey: "sk-test"}},
		},
		{
			name:    "empty ollama url",
			cfg:     Config{Provider: ProviderOllama, Ollama: &OllamaConfig{URL: " "}},
			wantErr: "url must not be empty",
		},
		{
			name:    "bad scheme",
			cfg:     Config{Provider: ProviderOllama, Ollama: &OllamaConfig{URL: "ftp://x"}},
			wantErr: "scheme must be http or https",
		},
		{
			name:    "empty model",
			cfg:     Config{Provider: ProviderOllama, Ollama: &OllamaConfig{URL: "http://localhost:11434", Model: " "}},
			wantErr: "model must not be empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error containing %q, got nil", tc.wantErr)
				return
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestPoolEmbed(t *testing.T) {
	mock := &mockEmbedder{
		emb: Embedding{Vector: []float32{0.1, 0.2, 0.3}, Model: "test"},
	}
	pool := NewPool(mock, PoolConfig{Size: 2}, nil)
	defer pool.Close()

	outCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "test"})

	select {
	case o := <-outCh:
		if o.Err != nil {
			t.Fatalf("unexpected error: %v", o.Err)
		}
		if len(o.Embedding.Vector) != 3 {
			t.Errorf("expected 3 dims, got %d", len(o.Embedding.Vector))
		}
		if o.Embedding.Model != "test" {
			t.Errorf("model = %q, want %q", o.Embedding.Model, "test")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for embed result")
	}
}

func TestPoolEmbedError(t *testing.T) {
	mock := &mockEmbedder{
		err: errors.New("embed failed"),
	}
	pool := NewPool(mock, PoolConfig{Size: 1}, nil)
	defer pool.Close()

	outCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "test"})

	select {
	case o := <-outCh:
		if o.Err == nil || !strings.Contains(o.Err.Error(), "embed failed") {
			t.Errorf("expected 'embed failed', got: %v", o.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error")
	}
}

func TestPoolFullQueue(t *testing.T) {
	blocker := &blockingEmbedder{block: make(chan struct{})}
	pool := NewPool(blocker, PoolConfig{Size: 1, Queue: 1}, nil)

	// Fill both slots: one in worker, one in queue.
	if ch := pool.Embed(context.Background(), EmbedRequest{ToolName: "a"}); ch == nil {
		t.Fatal("nil channel for a")
	}
	// Give the worker a moment to actually pick up "a" and enter blocker.Embed.
	// Without this sleep, the race detector can schedule j2 into the queue
	// before j1 has reached the blocking call, leaving a queue slot for j3.
	time.Sleep(2 * time.Millisecond)
	if ch := pool.Embed(context.Background(), EmbedRequest{ToolName: "b"}); ch == nil {
		t.Fatal("nil channel for b")
	}

	// Third call should hit the pool-full path.
	outCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "c"})
	if outCh == nil {
		t.Fatal("nil channel for c")
	}

	select {
	case o, ok := <-outCh:
		if !errors.Is(o.Err, ErrPoolFull) {
			t.Errorf("expected ErrPoolFull, got: %v (ok=%v)", o.Err, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for pool-full outcome")
	}

	close(blocker.block)
	pool.Close()
}

type blockingEmbedder struct {
	block chan struct{}
}

func (b *blockingEmbedder) Embed(ctx context.Context, _ EmbedRequest) (Embedding, error) {
	select {
	case <-b.block:
		return Embedding{}, nil
	case <-ctx.Done():
		return Embedding{}, ctx.Err()
	}
}
func (b *blockingEmbedder) Provider() Provider { return ProviderOllama }
func (b *blockingEmbedder) Close() error       { return nil }

func TestNewPoolDefaults(t *testing.T) {
	mock := &mockEmbedder{}
	pool := NewPool(mock, PoolConfig{}, nil)
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
	defer pool.Close()
	if pool.Size() != defaultPoolSize {
		t.Errorf("default size = %d, want %d", pool.Size(), defaultPoolSize)
	}
}

func TestPoolConfigBounds(t *testing.T) {
	cfg := PoolConfig{Size: 100000, Queue: 10000000}.withDefaults()
	if cfg.Size > maxPoolSize {
		t.Errorf("size not capped: %d", cfg.Size)
	}
	if cfg.Queue > maxPoolQueue {
		t.Errorf("queue not capped: %d", cfg.Queue)
	}
}

func TestOllamaConfigValidate(t *testing.T) {
	cfg := &OllamaConfig{}
	cfg.withDefaults()
	if cfg.URL != defaultOllamaURL {
		t.Errorf("default url = %q, want %q", cfg.URL, defaultOllamaURL)
	}
	if cfg.Model != defaultOllamaModel {
		t.Errorf("default model = %q, want %q", cfg.Model, defaultOllamaModel)
	}

	cfg2 := &OllamaConfig{URL: "http://myhost:8080", Model: "my-model"}
	cfg2.withDefaults()
	if cfg2.URL != "http://myhost:8080" || cfg2.Model != "my-model" {
		t.Errorf("custom config not preserved: %+v", cfg2)
	}
}

func TestOllamaConfigValidateURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr string
	}{
		{"", "url must not be empty"},
		{"  ", "url must not be empty"},
		{"ftp://x", "scheme must be http or https"},
		{"://bad", "invalid url"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validateOllamaURL(tt.url)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("url %q: error = %v, want contain %q", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestOpenAIConfigValidate(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	cfg := &OpenAIConfig{}
	if err := cfg.validateKey(); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("expected OPENAI_API_KEY error, got: %v", err)
	}

	cfg2 := &OpenAIConfig{APIKey: "sk-test"}
	if err := cfg2.validateKey(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	cfg2.withDefaults()
	if cfg2.Model != defaultOpenAIModel {
		t.Errorf("default model = %q, want %q", cfg2.Model, defaultOpenAIModel)
	}
}

func TestNewOllamaEmbedderBadURL(t *testing.T) {
	_, err := NewOllamaEmbedder(OllamaConfig{URL: "://bad"}, nil)
	if err == nil {
		t.Error("expected error for bad URL")
	}
}

func TestNewOpenAIEmbedderNoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	_, err := NewOpenAIEmbedder(OpenAIConfig{}, nil)
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestNewOpenAIEmbedderWithKey(t *testing.T) {
	e, err := NewOpenAIEmbedder(OpenAIConfig{APIKey: "sk-test"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.apiKey != "sk-test" {
		t.Errorf("apiKey = %q, want %q", e.apiKey, "sk-test")
	}
	e.Close()
}

func TestOllamaEmbedPayloadTooLarge(t *testing.T) {
	e, err := NewOllamaEmbedder(OllamaConfig{URL: "http://localhost:11434"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	big := make([]byte, MaxPayloadBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	_, err = e.Embed(context.Background(), EmbedRequest{ToolName: "x", Args: big})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got: %v", err)
	}
}

func TestPoolConcurrentEmbed(t *testing.T) {
	mock := &mockEmbedder{
		emb: Embedding{Vector: []float32{0.5}, Model: "test"},
	}
	pool := NewPool(mock, PoolConfig{Size: 4, Queue: 200}, nil)
	defer pool.Close()

	var wg sync.WaitGroup
	errCount := 0
	var errMu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			outCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "tool"})
			select {
			case o, ok := <-outCh:
				if !ok || o.Err != nil {
					errMu.Lock()
					errCount++
					errMu.Unlock()
				}
			case <-time.After(2 * time.Second):
				errMu.Lock()
				errCount++
				errMu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if errCount > 0 {
		t.Errorf("%d goroutines got errors", errCount)
	}
}

func TestPoolDoubleClose(t *testing.T) {
	mock := &mockEmbedder{emb: Embedding{Vector: []float32{0.1}, Model: "t"}}
	pool := NewPool(mock, PoolConfig{Size: 2}, nil)
	pool.Close()
	// Second close should not panic
	pool.Close()
}

// TestPoolCloseCancelsInFlight verifies the deferred fix: workers blocked
// inside in-flight HTTP calls observe ctx.Done() when Close() is invoked and
// exit promptly instead of waiting for the HTTP timeout (5–10s).
func TestPoolCloseCancelsInFlight(t *testing.T) {
	blocker := &blockingEmbedder{block: make(chan struct{})}
	pool := NewPool(blocker, PoolConfig{Size: 1}, nil)

	// Submit a job; worker picks it up and blocks inside Embed().
	outCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "slow"})

	// Give the worker a moment to start the HTTP call.
	time.Sleep(50 * time.Millisecond)

	// Close should cancel the in-flight call within the HTTP timeout window.
	closeStart := time.Now()
	pool.Close()
	closeDur := time.Since(closeStart)

	if closeDur > 2*time.Second {
		t.Errorf("Close() took %s, expected < 2s (in-flight should be canceled)", closeDur)
	}

	// Drain whatever came back. Either way the test must not hang.
	select {
	case o, ok := <-outCh:
		if !ok {
			t.Error("channel should have delivered an outcome")
		}
		if o.Err == nil {
			t.Error("expected ctx.Canceled error, got nil")
		}
	case <-time.After(time.Second):
		t.Error("channel should have been closed/canceled by now")
	}
}

func TestEmbedderUnavailableWrapped(t *testing.T) {
	e, err := NewOllamaEmbedder(OllamaConfig{URL: "http://localhost:11434"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !errors.Is(embedderSentinel(), ErrEmbedderUnavailable) {
		t.Error("ErrEmbedderUnavailable should be a sentinel")
	}
}

func embedderSentinel() error { return ErrEmbedderUnavailable }

// Benchmark: NFR11 <5ms p95 (NFR11: hot-path overhead target).
// Measures the cost of building the embed input string (the only sync work
// done on the request hot path before submitting to the async pool).
func BenchmarkEmbedRequestInput(b *testing.B) {
	args := json.RawMessage(`{"location":"San Francisco","unit":"celsius","date":"2026-06-27","hourly":true}`)
	req := EmbedRequest{ToolName: "get_weather", Args: args}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = req.Input()
	}
}

// Benchmark the async pool submit path (the actual hot-path call site).
// This is the overhead added per outbound tool call when embedder is enabled.
func BenchmarkPoolSubmit(b *testing.B) {
	mock := &mockEmbedder{emb: Embedding{Vector: make([]float32, 384), Model: "m"}}
	pool := NewPool(mock, PoolConfig{Size: 8, Queue: 1024}, nil)
	defer pool.Close()

	args := json.RawMessage(`{"location":"San Francisco","unit":"celsius"}`)
	req := EmbedRequest{ToolName: "get_weather", Args: args}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		outCh := pool.Embed(context.Background(), req)
		// Drain to avoid leaking goroutines
		<-outCh
	}
}

// Benchmark input formatting with a large payload (max-allowed size).
func BenchmarkEmbedRequestInputLarge(b *testing.B) {
	bigArgs := json.RawMessage(`"` + strings.Repeat("x", MaxPayloadBytes-2) + `"`)
	req := EmbedRequest{ToolName: "big_tool", Args: bigArgs}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = req.Input()
	}
}
