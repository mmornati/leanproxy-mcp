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
}

func (m *mockEmbedder) Embed(_ context.Context, req EmbedRequest) (Embedding, error) {
	m.mu.Lock()
	m.inputs = append(m.inputs, req.Input())
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
	// Must be sorted: a, m, z
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
			cfg:  Config{Provider: ProviderOllama, Ollama: &OllamaConfig{URL: "http://localhost:11434"}},
		},
		{
			name:    "invalid ollama url",
			cfg:     Config{Provider: ProviderOllama, Ollama: &OllamaConfig{URL: "://invalid"}},
			wantErr: "invalid url",
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

	resultCh, errCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "test"})

	select {
	case emb := <-resultCh:
		if len(emb.Vector) != 3 {
			t.Errorf("expected 3 dims, got %d", len(emb.Vector))
		}
		if emb.Model != "test" {
			t.Errorf("model = %q, want %q", emb.Model, "test")
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
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

	_, errCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "test"})

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "embed failed") {
			t.Errorf("expected 'embed failed', got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for error")
	}
}

func TestPoolFullQueue(t *testing.T) {
	blocker := &blockingEmbedder{block: make(chan struct{})}
	pool := NewPool(blocker, PoolConfig{Size: 1, Queue: 1}, nil)
	defer pool.Close()

	// Fill the pool
	_, _ = pool.Embed(context.Background(), EmbedRequest{ToolName: "a"})
	_, _ = pool.Embed(context.Background(), EmbedRequest{ToolName: "b"})

	// Next submit should fail with pool full
	_, errCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "c"})
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrPoolFull) {
			t.Errorf("expected ErrPoolFull, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	close(blocker.block)
}

type blockingEmbedder struct {
	block chan struct{}
}

func (b *blockingEmbedder) Embed(ctx context.Context, _ EmbedRequest) (Embedding, error) {
	<-b.block
	return Embedding{}, nil
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
}

func TestOllamaConfigValidate(t *testing.T) {
	cfg := &OllamaConfig{}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should validate: %v", err)
	}
	if cfg.URL != defaultOllamaURL {
		t.Errorf("default url = %q, want %q", cfg.URL, defaultOllamaURL)
	}
	if cfg.Model != defaultOllamaModel {
		t.Errorf("default model = %q, want %q", cfg.Model, defaultOllamaModel)
	}

	cfg2 := &OllamaConfig{URL: "http://myhost:8080", Model: "my-model"}
	if err := cfg2.Validate(); err != nil {
		t.Errorf("valid custom config: %v", err)
	}
}

func TestOllamaConfigValidateBadURL(t *testing.T) {
	cfg := &OllamaConfig{URL: "://bad"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid url") {
		t.Errorf("expected invalid url error, got: %v", err)
	}
}

func TestOpenAIConfigValidate(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	// No API key set should fail
	cfg := &OpenAIConfig{}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("expected OPENAI_API_KEY error, got: %v", err)
	}

	// API key in config should pass
	cfg2 := &OpenAIConfig{APIKey: "sk-test"}
	if err := cfg2.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
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
	if e.cfg.apiKey() != "sk-test" {
		t.Errorf("apiKey() = %q, want %q", e.cfg.apiKey(), "sk-test")
	}
	e.Close()
}

func TestPoolConcurrentEmbed(t *testing.T) {
	mock := &mockEmbedder{
		emb: Embedding{Vector: []float32{0.5}, Model: "test"},
	}
	pool := NewPool(mock, PoolConfig{Size: 4, Queue: 100}, nil)
	defer pool.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			resultCh, errCh := pool.Embed(context.Background(), EmbedRequest{ToolName: "tool"})
			select {
			case <-resultCh:
			case <-errCh:
				t.Errorf("goroutine %d got error", n)
			case <-time.After(time.Second):
				t.Errorf("goroutine %d timeout", n)
			}
		}(i)
	}
	wg.Wait()
}

func TestNewPoolClose(t *testing.T) {
	mock := &mockEmbedder{emb: Embedding{Vector: []float32{0.1}, Model: "t"}}
	pool := NewPool(mock, PoolConfig{Size: 2}, nil)
	pool.Close()
	// Close twice should not panic
	pool.Close()
}
