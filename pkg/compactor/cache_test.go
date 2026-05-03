package compactor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileCache_SetAndGet(t *testing.T) {
	tmpDir := t.TempDir()

	cache, err := NewFileCache(tmpDir, nil)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	manifest := &DistilledManifest{
		ServerName:   "test-server",
		OriginalHash: "abc123",
		Tools: []DistilledTool{
			{Name: "tool1", Description: "Test tool", Parameters: json.RawMessage("{}")},
		},
		DistilledAt: time.Now(),
	}

	ctx := context.Background()

	if err := cache.Set(ctx, "test-server", manifest); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	retrieved, err := cache.Get(ctx, "test-server", "abc123")
	if err != nil {
		t.Fatalf("failed to get cache: %v", err)
	}

	if retrieved == nil {
		t.Fatal("expected retrieved manifest, got nil")
	}

	if retrieved.ServerName != "test-server" {
		t.Errorf("expected server_name 'test-server', got %s", retrieved.ServerName)
	}

	if len(retrieved.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(retrieved.Tools))
	}
}

func TestFileCache_Get_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	cache, err := NewFileCache(tmpDir, nil)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	ctx := context.Background()

	retrieved, err := cache.Get(ctx, "nonexistent", "hash123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Error("expected nil for nonexistent key")
	}
}

func TestFileCache_Invalidate(t *testing.T) {
	tmpDir := t.TempDir()

	cache, err := NewFileCache(tmpDir, nil)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	manifest := &DistilledManifest{
		ServerName:   "test-server",
		OriginalHash: "abc123",
		Tools:        []DistilledTool{},
		DistilledAt:  time.Now(),
	}

	ctx := context.Background()

	if err := cache.Set(ctx, "test-server", manifest); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	if err := cache.Invalidate(ctx, "test-server"); err != nil {
		t.Fatalf("failed to invalidate cache: %v", err)
	}

	retrieved, err := cache.Get(ctx, "test-server", "abc123")
	if err != nil {
		t.Fatalf("unexpected error after invalidation: %v", err)
	}

	if retrieved != nil {
		t.Error("expected nil after invalidation")
	}
}

func TestNoOpCache(t *testing.T) {
	cache := NewNoOpCache()

	ctx := context.Background()

	retrieved, err := cache.Get(ctx, "test", "hash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if retrieved != nil {
		t.Error("expected nil from NoOpCache.Get")
	}

	if err := cache.Set(ctx, "test", &DistilledManifest{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := cache.Invalidate(ctx, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFileCache_DirectoryCreation(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, "nested", "path", "to", "cache")

	cache, err := NewFileCache(cacheDir, nil)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	ctx := context.Background()

	manifest := &DistilledManifest{
		ServerName:   "test",
		OriginalHash: "hash",
		Tools:        []DistilledTool{},
		DistilledAt:  time.Now(),
	}

	if err := cache.Set(ctx, "test", manifest); err != nil {
		t.Fatalf("failed to set cache: %v", err)
	}

	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		t.Error("expected cache directory to be created")
	}
}
