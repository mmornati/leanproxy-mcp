package compactor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Cache interface {
	Get(ctx context.Context, serverName string, originalHash string) (*DistilledManifest, error)
	Set(ctx context.Context, serverName string, manifest *DistilledManifest) error
	Invalidate(ctx context.Context, serverName string) error
}

type FileCache struct {
	cacheDir string
	logger   *slog.Logger
	mu       sync.RWMutex
	inMemory map[string]*DistilledManifest
}

func NewFileCache(cacheDir string, logger *slog.Logger) (*FileCache, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if cacheDir == "" {
		usr, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("compactor: get user home dir: %w", err)
		}
		cacheDir = filepath.Join(usr.HomeDir, ".config", "leanproxy", "distilled")
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("compactor: create cache dir: %w", err)
	}

	return &FileCache{
		cacheDir: cacheDir,
		logger:   logger,
		inMemory: make(map[string]*DistilledManifest),
	}, nil
}

func (c *FileCache) cacheKey(serverName, originalHash string) string {
	hash := sha256.Sum256([]byte(serverName + originalHash))
	return fmt.Sprintf("%x", hash)
}

func (c *FileCache) filePath(serverName string, originalHash string) string {
	key := c.cacheKey(serverName, originalHash)
	return filepath.Join(c.cacheDir, serverName+"_"+key[:16]+".json")
}

func (c *FileCache) Get(ctx context.Context, serverName string, originalHash string) (*DistilledManifest, error) {
	c.mu.RLock()
	if cached, ok := c.inMemory[serverName+originalHash]; ok {
		c.mu.RUnlock()
		c.logger.Debug("cache hit (memory)", "server", serverName)
		return cached, nil
	}
	c.mu.RUnlock()

	filePath := c.filePath(serverName, originalHash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.logger.Debug("cache miss", "server", serverName)
			return nil, nil
		}
		return nil, fmt.Errorf("compactor: read cache file: %w", err)
	}

	var distilled DistilledManifest
	if err := json.Unmarshal(data, &distilled); err != nil {
		return nil, fmt.Errorf("compactor: unmarshal cached manifest: %w", err)
	}

	c.mu.Lock()
	c.inMemory[serverName+originalHash] = &distilled
	c.mu.Unlock()

	c.logger.Debug("cache hit (disk)", "server", serverName)
	return &distilled, nil
}

func (c *FileCache) Set(ctx context.Context, serverName string, manifest *DistilledManifest) error {
	c.mu.Lock()
	c.inMemory[serverName+manifest.OriginalHash] = manifest
	c.mu.Unlock()

	filePath := c.filePath(serverName, manifest.OriginalHash)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("compactor: marshal manifest for cache: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("compactor: write cache file: %w", err)
	}

	c.logger.Debug("cached distilled manifest", "server", serverName, "path", filePath)
	return nil
}

func (c *FileCache) Invalidate(ctx context.Context, serverName string) error {
	c.mu.Lock()
	inMemoryKeysToDelete := make([]string, 0)
	for key := range c.inMemory {
		if strings.HasPrefix(key, serverName) {
			inMemoryKeysToDelete = append(inMemoryKeysToDelete, key)
		}
	}
	for _, key := range inMemoryKeysToDelete {
		delete(c.inMemory, key)
	}
	c.mu.Unlock()

	pattern := filepath.Join(c.cacheDir, serverName+"_*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("compactor: glob cache files: %w", err)
	}

	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			c.logger.Warn("failed to remove cache file", "path", path, "error", err)
		}
	}

	c.logger.Debug("invalidated cache", "server", serverName, "files_removed", len(matches))
	return nil
}

type NoOpCache struct{}

func NewNoOpCache() *NoOpCache {
	return &NoOpCache{}
}

func (c *NoOpCache) Get(ctx context.Context, serverName string, originalHash string) (*DistilledManifest, error) {
	return nil, nil
}

func (c *NoOpCache) Set(ctx context.Context, serverName string, manifest *DistilledManifest) error {
	return nil
}

func (c *NoOpCache) Invalidate(ctx context.Context, serverName string) error {
	return nil
}

func hashForInvalidation(manifest RawManifest) string {
	data, _ := json.Marshal(manifest)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash)
}

var _ Cache = (*FileCache)(nil)
var _ Cache = (*NoOpCache)(nil)

func isCacheValid(cached *DistilledManifest, currentHash string) bool {
	if cached == nil {
		return false
	}
	if cached.OriginalHash != currentHash {
		return false
	}
	if time.Since(cached.DistilledAt) > 24*time.Hour {
		return false
	}
	return true
}
