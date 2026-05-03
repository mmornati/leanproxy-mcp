package compactor

import (
	"context"
	"encoding/json"
	"testing"
)

type mockLLMClient struct {
	distillFunc func(context.Context, RawManifest) (*DistilledManifest, error)
}

func (m *mockLLMClient) Distill(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
	if m.distillFunc != nil {
		return m.distillFunc(ctx, manifest)
	}
	return nil, nil
}

type mockCache struct {
	getFunc func(context.Context, string, string) (*DistilledManifest, error)
	setFunc func(context.Context, string, *DistilledManifest) error
}

func (m *mockCache) Get(ctx context.Context, serverName, originalHash string) (*DistilledManifest, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, serverName, originalHash)
	}
	return nil, nil
}

func (m *mockCache) Set(ctx context.Context, serverName string, manifest *DistilledManifest) error {
	if m.setFunc != nil {
		return m.setFunc(ctx, serverName, manifest)
	}
	return nil
}

func (m *mockCache) Invalidate(ctx context.Context, serverName string) error {
	return nil
}

func TestCompactor_Compact_UsesCache(t *testing.T) {
	cached := &DistilledManifest{
		ServerName:   "test-server",
		OriginalHash: "cached-hash",
		Tools: []DistilledTool{
			{Name: "cached-tool", Description: "Cached", Parameters: json.RawMessage("{}")},
		},
	}

	cache := &mockCache{
		getFunc: func(ctx context.Context, serverName, hash string) (*DistilledManifest, error) {
			return cached, nil
		},
	}

	client := &mockLLMClient{}

	compactor := NewCompactor(client, cache, CompactorConfig{Enabled: true}, nil)

	manifest := RawManifest{
		Name:        "test-server",
		Description: "Original",
		Tools: []RawTool{
			{Name: "original-tool", Description: "Original", Parameters: json.RawMessage("{}")},
		},
	}

	result, err := compactor.Compact(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != cached {
		t.Error("expected to get cached result")
	}
}

func TestCompactor_Compact_Disabled(t *testing.T) {
	cache := &mockCache{}
	client := &mockLLMClient{
		distillFunc: func(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
			t.Error("should not call LLM client when disabled")
			return nil, nil
		},
	}

	compactor := NewCompactor(client, cache, CompactorConfig{Enabled: false}, nil)

	manifest := RawManifest{Name: "test"}

	result, err := compactor.Compact(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != nil {
		t.Error("expected nil when disabled")
	}
}

func TestCompactor_Compact_CallsLLMOnCacheMiss(t *testing.T) {
	cache := &mockCache{
		getFunc: func(ctx context.Context, serverName, hash string) (*DistilledManifest, error) {
			return nil, nil
		},
	}

	called := false
	client := &mockLLMClient{
		distillFunc: func(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
			called = true
			return &DistilledManifest{
				ServerName:   manifest.Name,
				Tools:        []DistilledTool{},
				OriginalHash: manifest.Hash(),
			}, nil
		},
	}

	compactor := NewCompactor(client, cache, CompactorConfig{Enabled: true}, nil)

	manifest := RawManifest{Name: "test-server"}

	_, err := compactor.Compact(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called {
		t.Error("expected LLM client to be called")
	}
}

func TestCompactor_CompactWithFallback(t *testing.T) {
	cache := &mockCache{
		getFunc: func(ctx context.Context, serverName, hash string) (*DistilledManifest, error) {
			return nil, nil
		},
	}

	client := &mockLLMClient{
		distillFunc: func(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
			return nil, context.DeadlineExceeded
		},
	}

	compactor := NewCompactor(client, cache, CompactorConfig{Enabled: true}, nil)

	manifest := RawManifest{
		Name:        "test-server",
		Description: "Test",
		Tools: []RawTool{
			{Name: "tool1", Description: "Test tool", Parameters: json.RawMessage("{}")},
		},
	}

	result, err := compactor.CompactWithFallback(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Error("expected fallback result")
	}

	if len(result.Tools) != 1 {
		t.Errorf("expected 1 tool from fallback, got %d", len(result.Tools))
	}
}

func TestCompactor_InvalidateCache(t *testing.T) {
	cache := &mockCache{}
	client := &mockLLMClient{}

	compactor := NewCompactor(client, cache, CompactorConfig{Enabled: true}, nil)

	err := compactor.InvalidateCache(context.Background(), "test-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompactor_IsEnabled(t *testing.T) {
	compactor := NewCompactor(nil, nil, CompactorConfig{Enabled: true}, nil)
	if !compactor.IsEnabled() {
		t.Error("expected enabled")
	}

	compactor = NewCompactor(nil, nil, CompactorConfig{Enabled: false}, nil)
	if compactor.IsEnabled() {
		t.Error("expected disabled")
	}
}
