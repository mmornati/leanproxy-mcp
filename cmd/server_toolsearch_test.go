package cmd

import (
	"context"
	"log/slog"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/toolsearch"
)

func TestConfigureToolSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	newHandler := func() *mcp.Handler {
		return mcp.NewHandler(pool.NewUnifiedPool(nil, nil, nil, slog.Default()), slog.Default())
	}

	h := newHandler()
	configureToolSearch(ctx, h, nil)
	if h.SearchIndex().HybridEnabled() {
		t.Fatal("hybrid tool search enabled without a tool_search block")
	}

	h = newHandler()
	configureToolSearch(ctx, h, &toolsearch.Config{Hybrid: &toolsearch.HybridConfig{
		Enabled:  true,
		Embedder: embedder.Config{Provider: embedder.ProviderOllama, Ollama: &embedder.OllamaConfig{URL: "http://127.0.0.1:1", Model: "m"}},
	}})
	if !h.SearchIndex().HybridEnabled() {
		t.Fatal("hybrid tool search not enabled")
	}

	h = newHandler()
	configureToolSearch(ctx, h, &toolsearch.Config{Hybrid: &toolsearch.HybridConfig{Enabled: true}})
	if h.SearchIndex().HybridEnabled() {
		t.Fatal("hybrid enabled with an invalid embedder")
	}
}
