package registry

import (
	"encoding/json"
	"sync"
	"time"
)

type ToolStub struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
}

type ToolSchema struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	ServerID     string          `json:"serverId"`
}

type LazySchemaCache struct {
	mu         sync.RWMutex
	cache      map[string]ToolSchema
	lastAccess map[string]time.Time
	ttl        time.Duration
}

func NewLazySchemaCache(ttl time.Duration) *LazySchemaCache {
	return &LazySchemaCache{
		cache:      make(map[string]ToolSchema),
		lastAccess: make(map[string]time.Time),
		ttl:        ttl,
	}
}

func (c *LazySchemaCache) GetStub(toolName string) (ToolStub, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	schema, exists := c.cache[toolName]
	if !exists {
		return ToolStub{}, false
	}

	return ToolStub{
		Name:        schema.Name,
		Description: schema.Description,
	}, true
}

func (c *LazySchemaCache) GetFullSchema(toolName string) (ToolSchema, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	schema, exists := c.cache[toolName]
	if !exists {
		return ToolSchema{}, false
	}

	lastAccess := c.lastAccess[toolName]
	isExpired := c.ttl > 0 && time.Since(lastAccess) > c.ttl
	if isExpired {
		delete(c.cache, toolName)
		delete(c.lastAccess, toolName)
		return ToolSchema{}, false
	}

	c.lastAccess[toolName] = time.Now()
	return schema, true
}

func (c *LazySchemaCache) SetFullSchema(toolName string, schema ToolSchema) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[toolName] = schema
	c.lastAccess[toolName] = time.Now()
}

func (c *LazySchemaCache) CacheWithTTL(toolName string, schema ToolSchema, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[toolName] = schema
	c.lastAccess[toolName] = time.Now()
	c.ttl = ttl
}

func (c *LazySchemaCache) Invalidate(toolName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, toolName)
	delete(c.lastAccess, toolName)
}

func (c *LazySchemaCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]ToolSchema)
	c.lastAccess = make(map[string]time.Time)
}

func (c *LazySchemaCache) Stats() (cached int, expired int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.ttl <= 0 {
		return len(c.cache), 0
	}

	expired = 0
	now := time.Now()
	for _, lastAccess := range c.lastAccess {
		if now.Sub(lastAccess) > c.ttl {
			expired++
		}
	}

	return len(c.cache), expired
}