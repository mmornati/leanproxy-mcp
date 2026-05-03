package toolstore

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

const CacheValidDuration = 24 * time.Hour

type CachedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Cache interface {
	GetTools(serverName string) ([]CachedTool, error)
	SetTools(serverName string, tools []CachedTool) error
	Invalidate(serverName string) error
	GetCacheDir() string
}

type FileCache struct {
	cacheDir string
	logger   *slog.Logger
	mu       sync.RWMutex
	inMemory map[string]*cachedToolsEntry
}

type cachedToolsEntry struct {
	Tools      []CachedTool
	CachedAt   time.Time
	ValidUntil time.Time
}

type cachedToolsFile struct {
	Tools    []CachedTool `json:"tools"`
	CachedAt time.Time    `json:"cached_at"`
}

func NewFileCache(logger *slog.Logger) (*FileCache, error) {
	return newFileCacheWithDir(logger, "")
}

func newFileCacheWithDir(logger *slog.Logger, cacheDir string) (*FileCache, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if cacheDir == "" {
		usr, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("toolstore: get user home dir: %w", err)
		}
		cacheDir = filepath.Join(usr.HomeDir, ".config", "leanproxy", "toolcache")
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("toolstore: create cache dir: %w", err)
	}

	return &FileCache{
		cacheDir: cacheDir,
		logger:   logger,
		inMemory: make(map[string]*cachedToolsEntry),
	}, nil
}

func (c *FileCache) GetCacheDir() string {
	return c.cacheDir
}

func (c *FileCache) filePath(serverName string) string {
	return filepath.Join(c.cacheDir, sanitizeFilename(serverName)+".json")
}

func (c *FileCache) GetTools(serverName string) ([]CachedTool, error) {
	c.mu.RLock()
	if cached, ok := c.inMemory[serverName]; ok {
		if time.Now().Before(cached.ValidUntil) {
			c.mu.RUnlock()
			c.logger.Debug("toolstore hit (memory)", "server", serverName)
			return cached.Tools, nil
		}
	}
	c.mu.RUnlock()

	filePath := c.filePath(serverName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.logger.Debug("toolstore miss", "server", serverName)
			return nil, nil
		}
		return nil, fmt.Errorf("toolstore: read cache file: %w", err)
	}

	var cached cachedToolsFile
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, fmt.Errorf("toolstore: unmarshal cached tools: %w", err)
	}

	if time.Since(cached.CachedAt) > CacheValidDuration {
		c.logger.Debug("toolstore expired", "server", serverName)
		return nil, nil
	}

	tools := make([]CachedTool, len(cached.Tools))
	copy(tools, cached.Tools)

	c.mu.Lock()
	c.inMemory[serverName] = &cachedToolsEntry{
		Tools:      tools,
		CachedAt:   cached.CachedAt,
		ValidUntil: cached.CachedAt.Add(CacheValidDuration),
	}
	c.mu.Unlock()

	c.logger.Debug("toolstore hit (disk)", "server", serverName, "count", len(tools))
	return tools, nil
}

func (c *FileCache) SetTools(serverName string, tools []CachedTool) error {
	c.mu.Lock()
	c.inMemory[serverName] = &cachedToolsEntry{
		Tools:      tools,
		CachedAt:   time.Now(),
		ValidUntil: time.Now().Add(CacheValidDuration),
	}
	c.mu.Unlock()

	filePath := c.filePath(serverName)
	cachedFile := cachedToolsFile{
		Tools:    tools,
		CachedAt: time.Now(),
	}

	data, err := json.MarshalIndent(cachedFile, "", "  ")
	if err != nil {
		return fmt.Errorf("toolstore: marshal tools for cache: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("toolstore: write cache file: %w", err)
	}

	c.logger.Debug("toolstore saved", "server", serverName, "count", len(tools), "path", filePath)
	return nil
}

func (c *FileCache) Invalidate(serverName string) error {
	c.mu.Lock()
	delete(c.inMemory, serverName)
	c.mu.Unlock()

	filePath := c.filePath(serverName)
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("toolstore: remove cache file: %w", err)
	}

	c.logger.Debug("toolstore invalidated", "server", serverName)
	return nil
}

func (c *FileCache) ListCachedServers() ([]string, error) {
	entries, err := os.ReadDir(c.cacheDir)
	if err != nil {
		return nil, fmt.Errorf("toolstore: read cache dir: %w", err)
	}

	var servers []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) > 5 && name[len(name)-5:] == ".json" {
			servers = append(servers, name[:len(name)-5])
		}
	}
	return servers, nil
}

type NoOpCache struct{}

func NewNoOpCache() *NoOpCache {
	return &NoOpCache{}
}

func (c *NoOpCache) GetTools(serverName string) ([]CachedTool, error) {
	return nil, nil
}

func (c *NoOpCache) SetTools(serverName string, tools []CachedTool) error {
	return nil
}

func (c *NoOpCache) Invalidate(serverName string) error {
	return nil
}

func (c *NoOpCache) GetCacheDir() string {
	return ""
}

var _ Cache = (*FileCache)(nil)
var _ Cache = (*NoOpCache)(nil)

func sanitizeFilename(name string) string {
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}
