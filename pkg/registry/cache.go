package registry

import (
	"container/list"
	"context"
	"encoding/json"
	"sync"
	"time"
)

type SchemaCache interface {
	Get(key string) (json.RawMessage, bool)
	Set(key string, schema json.RawMessage)
	Delete(key string)
	Clear()
}

type lruSchemaCache struct {
	mu       sync.Mutex
	maxSize  int
	maxAge   time.Duration
	cache    map[string]*list.Element
	lru      *list.List
	onEvict  func(key string, schema json.RawMessage)
}

type cacheEntry struct {
	key       string
	schema    json.RawMessage
	createdAt time.Time
}

func NewLRUSchemaCache(maxSize int, maxAge time.Duration, onEvict func(key string, schema json.RawMessage)) SchemaCache {
	if maxSize <= 0 {
		maxSize = 100
	}
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	return &lruSchemaCache{
		maxSize: maxSize,
		maxAge:  maxAge,
		cache:   make(map[string]*list.Element),
		lru:     list.New(),
		onEvict: onEvict,
	}
}

func (c *lruSchemaCache) Get(key string) (json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.cache[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)

	if time.Since(entry.createdAt) > c.maxAge {
		c.lru.Remove(elem)
		delete(c.cache, key)
		if c.onEvict != nil {
			c.onEvict(key, entry.schema)
		}
		return nil, false
	}

	c.lru.MoveToFront(elem)
	return entry.schema, true
}

func (c *lruSchemaCache) Set(key string, schema json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.cache[key]; ok {
		entry := elem.Value.(*cacheEntry)
		entry.schema = schema
		entry.createdAt = time.Now()
		c.lru.MoveToFront(elem)
		return
	}

	if c.lru.Len() >= c.maxSize {
		oldest := c.lru.Back()
		if oldest != nil {
			c.lru.Remove(oldest)
			entry := oldest.Value.(*cacheEntry)
			delete(c.cache, entry.key)
			if c.onEvict != nil {
				c.onEvict(entry.key, entry.schema)
			}
		}
	}

	entry := &cacheEntry{
		key:       key,
		schema:    schema,
		createdAt: time.Now(),
	}
	elem := c.lru.PushFront(entry)
	c.cache[key] = elem
}

func (c *lruSchemaCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.cache[key]
	if !ok {
		return
	}

	c.lru.Remove(elem)
	entry := elem.Value.(*cacheEntry)
	delete(c.cache, key)
	if c.onEvict != nil {
		c.onEvict(key, entry.schema)
	}
}

func (c *lruSchemaCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, elem := range c.cache {
		entry := elem.Value.(*cacheEntry)
		if c.onEvict != nil {
			c.onEvict(entry.key, entry.schema)
		}
	}
	c.cache = make(map[string]*list.Element)
	c.lru.Init()
}

type SchemaCacheManager struct {
	cache      SchemaCache
	logger     interface{ Debug(msg string, args ...any) }
	serverName string
}

func NewSchemaCacheManager(serverName string, maxSize int, maxAge time.Duration, logger interface{ Debug(msg string, args ...any) }) *SchemaCacheManager {
	return &SchemaCacheManager{
		cache:      NewLRUSchemaCache(maxSize, maxAge, nil),
		logger:     logger,
		serverName: serverName,
	}
}

func (m *SchemaCacheManager) GetFullSchema(ctx context.Context, toolName string) (json.RawMessage, error) {
	cacheKey := m.serverName + "/" + toolName

	if schema, ok := m.cache.Get(cacheKey); ok {
		if m.logger != nil {
			m.logger.Debug("schema cache hit", "tool", toolName, "server", m.serverName)
		}
		return schema, nil
	}

	if m.logger != nil {
		m.logger.Debug("schema cache miss", "tool", toolName, "server", m.serverName)
	}
	return nil, nil
}

func (m *SchemaCacheManager) SetFullSchema(ctx context.Context, toolName string, schema json.RawMessage) {
	cacheKey := m.serverName + "/" + toolName
	m.cache.Set(cacheKey, schema)
}

func (m *SchemaCacheManager) Invalidate(toolName string) {
	cacheKey := m.serverName + "/" + toolName
	m.cache.Delete(cacheKey)
}

func (m *SchemaCacheManager) InvalidateAll() {
	m.cache.Clear()
}