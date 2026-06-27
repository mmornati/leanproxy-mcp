package bouncer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
)

func TestEmbedToolCallNoPool(t *testing.T) {
	SetGlobalEmbedPool(nil)

	_, errCh := EmbedToolCall(context.Background(), EmbedRequest{ToolName: "test"})
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "no global embed pool") {
			t.Errorf("expected 'no global embed pool' error, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEmbedToolCallWithPool(t *testing.T) {
	mock := &testEmbedder{emb: embedder.Embedding{Vector: []float32{0.1, 0.2}, Model: "test-model"}}
	pool := embedder.NewPool(mock, embedder.PoolConfig{Size: 1}, slog.Default())
	defer pool.Close()

	SetGlobalEmbedPool(pool)
	defer SetGlobalEmbedPool(nil)

	resultCh, errCh := EmbedToolCall(context.Background(), EmbedRequest{ToolName: "test"})
	select {
	case emb := <-resultCh:
		if len(emb.Vector) != 2 {
			t.Errorf("expected 2 dims, got %d", len(emb.Vector))
		}
		if emb.Model != "test-model" {
			t.Errorf("model = %q, want %q", emb.Model, "test-model")
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestNewEmbedderFromConfigOllama(t *testing.T) {
	cfg := embedder.Config{
		Provider: embedder.ProviderOllama,
		Ollama:   &embedder.OllamaConfig{URL: "http://localhost:11434"},
	}
	eng, err := newEmbedderFromConfig(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng.Provider() != embedder.ProviderOllama {
		t.Errorf("provider = %q, want %q", eng.Provider(), embedder.ProviderOllama)
	}
	eng.Close()
}

func TestNewEmbedderFromConfigOpenAI(t *testing.T) {
	cfg := embedder.Config{
		Provider: embedder.ProviderOpenAI,
		OpenAI:   &embedder.OpenAIConfig{APIKey: "sk-test"},
	}
	eng, err := newEmbedderFromConfig(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng.Provider() != embedder.ProviderOpenAI {
		t.Errorf("provider = %q, want %q", eng.Provider(), embedder.ProviderOpenAI)
	}
	eng.Close()
}

func TestNewEmbedderFromConfigUnknown(t *testing.T) {
	cfg := embedder.Config{Provider: "unknown"}
	_, err := newEmbedderFromConfig(cfg, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "unsupported embedder provider") {
		t.Errorf("expected error about unsupported provider, got: %v", err)
	}
}

func TestMustSetupEmbedder(t *testing.T) {
	// Should not panic with valid config
	cfg := embedder.Config{
		Provider: embedder.ProviderOllama,
		Ollama:   &embedder.OllamaConfig{URL: "http://localhost:11434"},
	}
	MustSetupEmbedder(cfg, embedder.PoolConfig{Size: 1})
	pool := GlobalEmbedPool()
	if pool == nil {
		t.Fatal("expected non-nil global pool after MustSetupEmbedder")
	}
	pool.Close()
	SetGlobalEmbedPool(nil)
}

type testEmbedder struct {
	emb embedder.Embedding
	err error
}

func (t *testEmbedder) Embed(_ context.Context, _ embedder.EmbedRequest) (embedder.Embedding, error) {
	if t.err != nil {
		return embedder.Embedding{}, t.err
	}
	return t.emb, nil
}

func (t *testEmbedder) Provider() embedder.Provider { return embedder.ProviderOllama }
func (t *testEmbedder) Close() error                { return nil }

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

func TestEmbedResultType(t *testing.T) {
	r := EmbedResult{Vector: []float32{0.1, 0.2}, Model: "test"}
	if len(r.Vector) != 2 {
		t.Errorf("expected 2 elements, got %d", len(r.Vector))
	}
}

func TestEmbedToolCallPoolError(t *testing.T) {
	mock := &testEmbedder{err: errors.New("embed failed")}
	pool := embedder.NewPool(mock, embedder.PoolConfig{Size: 1}, slog.Default())
	defer pool.Close()

	SetGlobalEmbedPool(pool)
	defer SetGlobalEmbedPool(nil)

	_, errCh := EmbedToolCall(context.Background(), EmbedRequest{ToolName: "test"})
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "embed failed") {
			t.Errorf("expected 'embed failed' error, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
