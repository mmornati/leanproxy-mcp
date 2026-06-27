package bouncer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
)

func TestEmbedToolCallNoPool(t *testing.T) {
	SetGlobalEmbedPool(nil)
	EmbedToolCall(context.Background(), EmbedRequest{ToolName: "test"})
}

func TestEmbedToolCallWithPoolSuccess(t *testing.T) {
	mock := &testEmbedder{emb: embedder.Embedding{Vector: []float32{0.1, 0.2}, Model: "test-model"}}
	pool := embedder.NewPool(mock, embedder.PoolConfig{Size: 1}, slog.Default())
	defer pool.Close()

	SetGlobalEmbedPool(pool)
	defer SetGlobalEmbedPool(nil)

	done := make(chan struct{})
	var captured EmbedOutcome
	prev := EmbedResultHandler
	EmbedResultHandler = func(o EmbedOutcome) {
		captured = o
		close(done)
	}
	defer func() { EmbedResultHandler = prev }()

	EmbedToolCall(context.Background(), EmbedRequest{ToolName: "test"})

	select {
	case <-done:
		if len(captured.Vector) != 2 {
			t.Errorf("expected 2 dims, got %d", len(captured.Vector))
		}
		if captured.Model != "test-model" {
			t.Errorf("model = %q, want %q", captured.Model, "test-model")
		}
		if captured.Err != "" {
			t.Errorf("unexpected error: %s", captured.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for embed result")
	}
}

func TestEmbedToolCallPoolError(t *testing.T) {
	mock := &testEmbedder{err: errors.New("embed failed")}
	pool := embedder.NewPool(mock, embedder.PoolConfig{Size: 1}, slog.Default())
	defer pool.Close()

	SetGlobalEmbedPool(pool)
	defer SetGlobalEmbedPool(nil)

	done := make(chan struct{})
	var captured EmbedOutcome
	prev := EmbedResultHandler
	EmbedResultHandler = func(o EmbedOutcome) {
		captured = o
		close(done)
	}
	defer func() { EmbedResultHandler = prev }()

	EmbedToolCall(context.Background(), EmbedRequest{ToolName: "test"})

	select {
	case <-done:
		if captured.Err == "" {
			t.Error("expected error captured")
		}
		if EmbedFailureCount() == 0 {
			t.Error("expected EmbedFailureCount > 0")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestEmbedToolCallPoolFull(t *testing.T) {
	blocker := &blockingEmbedder{block: make(chan struct{})}
	pool := embedder.NewPool(blocker, embedder.PoolConfig{Size: 1, Queue: 1}, slog.Default())

	SetGlobalEmbedPool(pool)
	defer func() {
		close(blocker.block)
		pool.Close()
		SetGlobalEmbedPool(nil)
	}()

	_, _ = pool.Embed(context.Background(), embedder.EmbedRequest{ToolName: "filler"})
	_, _ = pool.Embed(context.Background(), embedder.EmbedRequest{ToolName: "filler2"})

	done := make(chan struct{})
	prev := EmbedResultHandler
	EmbedResultHandler = func(o EmbedOutcome) {
		if o.Err != "" && strings.Contains(o.Err, "queue full") {
			close(done)
		}
	}
	defer func() { EmbedResultHandler = prev }()

	EmbedToolCall(context.Background(), EmbedRequest{ToolName: "overflow"})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for pool-full error")
	}
}

func TestEmbedToolCallEmptyToolName(t *testing.T) {
	embedSuccessCount.Store(0)
	embedFailureCount.Store(0)

	mock := &testEmbedder{emb: embedder.Embedding{Vector: []float32{1}}}
	pool := embedder.NewPool(mock, embedder.PoolConfig{Size: 1}, slog.Default())
	defer pool.Close()

	SetGlobalEmbedPool(pool)
	defer SetGlobalEmbedPool(nil)

	EmbedToolCall(context.Background(), EmbedRequest{ToolName: ""})
	time.Sleep(100 * time.Millisecond)

	if embedSuccessCount.Load() > 0 {
		t.Error("empty tool name should not embed")
	}
}

func TestEmbedToolCallPayloadTooLarge(t *testing.T) {
	mock := &testEmbedder{emb: embedder.Embedding{Vector: []float32{1}}}
	pool := embedder.NewPool(mock, embedder.PoolConfig{Size: 1}, slog.Default())
	defer pool.Close()

	SetGlobalEmbedPool(pool)
	defer SetGlobalEmbedPool(nil)

	bigArgs := make([]byte, embedder.MaxPayloadBytes+10)
	for i := range bigArgs {
		bigArgs[i] = 'a'
	}
	EmbedToolCall(context.Background(), EmbedRequest{ToolName: "test", Args: bigArgs})
	time.Sleep(50 * time.Millisecond)
}

func TestSetupEmbedderOllama(t *testing.T) {
	cfg := embedder.Config{
		Provider: embedder.ProviderOllama,
		Ollama:   &embedder.OllamaConfig{URL: "http://localhost:11434"},
	}
	if err := SetupEmbedder(cfg, embedder.PoolConfig{Size: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pool := GlobalEmbedPool()
	if pool == nil {
		t.Fatal("expected non-nil global pool after SetupEmbedder")
	}
	pool.Close()
	SetGlobalEmbedPool(nil)
}

func TestSetupEmbedderOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := embedder.Config{
		Provider: embedder.ProviderOpenAI,
		OpenAI:   &embedder.OpenAIConfig{APIKey: "sk-test"},
	}
	if err := SetupEmbedder(cfg, embedder.PoolConfig{Size: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pool := GlobalEmbedPool()
	if pool == nil {
		t.Fatal("expected non-nil global pool after SetupEmbedder")
	}
	pool.Close()
	SetGlobalEmbedPool(nil)
}

func TestSetupEmbedderMissingOpenAIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := embedder.Config{
		Provider: embedder.ProviderOpenAI,
		OpenAI:   &embedder.OpenAIConfig{},
	}
	err := SetupEmbedder(cfg, embedder.PoolConfig{Size: 1})
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("expected OPENAI_API_KEY in error, got: %v", err)
	}
}

func TestSetupEmbedderInvalidURL(t *testing.T) {
	cfg := embedder.Config{
		Provider: embedder.ProviderOllama,
		Ollama:   &embedder.OllamaConfig{URL: "://bad"},
	}
	err := SetupEmbedder(cfg, embedder.PoolConfig{Size: 1})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestSetupEmbedderReplacesExistingPool(t *testing.T) {
	mock1 := &testEmbedder{emb: embedder.Embedding{Vector: []float32{1}}}
	pool1 := embedder.NewPool(mock1, embedder.PoolConfig{Size: 1}, nil)
	SetGlobalEmbedPool(pool1)

	cfg := embedder.Config{
		Provider: embedder.ProviderOllama,
		Ollama:   &embedder.OllamaConfig{URL: "http://localhost:11434"},
	}
	if err := SetupEmbedder(cfg, embedder.PoolConfig{Size: 2}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pool2 := GlobalEmbedPool()
	if pool2 == pool1 {
		t.Error("expected pool to be replaced")
	}
	pool2.Close()
	SetGlobalEmbedPool(nil)
}

func TestNewEmbedderFromConfigUnknown(t *testing.T) {
	cfg := embedder.Config{Provider: "unknown"}
	_, err := newEmbedderFromConfig(cfg, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "unsupported embedder provider") {
		t.Errorf("expected error about unsupported provider, got: %v", err)
	}
}

type testEmbedder struct {
	mu  sync.Mutex
	emb embedder.Embedding
	err error
}

func (t *testEmbedder) Embed(_ context.Context, _ embedder.EmbedRequest) (embedder.Embedding, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return embedder.Embedding{}, t.err
	}
	return t.emb, nil
}

func (t *testEmbedder) Provider() embedder.Provider { return embedder.ProviderOllama }
func (t *testEmbedder) Close() error                { return nil }

type blockingEmbedder struct {
	block chan struct{}
}

func (b *blockingEmbedder) Embed(_ context.Context, _ embedder.EmbedRequest) (embedder.Embedding, error) {
	<-b.block
	return embedder.Embedding{}, nil
}
func (b *blockingEmbedder) Provider() embedder.Provider { return embedder.ProviderOllama }
func (b *blockingEmbedder) Close() error                { return nil }

func TestGlobalEmbedPoolAccessors(t *testing.T) {
	if GlobalEmbedPool() != nil {
		t.Error("expected nil default global pool")
	}

	mock := &testEmbedder{emb: embedder.Embedding{Vector: []float32{1.0}, Model: "m"}}
	pool := embedder.NewPool(mock, embedder.PoolConfig{Size: 1}, nil)
	SetGlobalEmbedPool(pool)

	if GlobalEmbedPool() != pool {
		t.Error("global embed pool mismatch")
	}

	pool.Close()
	SetGlobalEmbedPool(nil)
}

func TestEmbedOutcomeFields(t *testing.T) {
	o := EmbedOutcome{
		Request:  EmbedRequest{ToolName: "t"},
		Provider: "ollama",
		Model:    "m",
		Vector:   []float32{0.1},
	}
	if o.Provider != "ollama" {
		t.Errorf("provider = %q, want %q", o.Provider, "ollama")
	}
}

func TestEmbedCounters(t *testing.T) {
	embedSuccessCount.Store(0)
	embedFailureCount.Store(0)

	if EmbedSuccessCount() != 0 || EmbedFailureCount() != 0 {
		t.Error("counters should reset")
	}
}
