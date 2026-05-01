package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportHTTP  TransportType = "http"
	TransportSSE   TransportType = "sse"
)

type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy  HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
)

type ServerStats struct {
	RequestCount int64
	ErrorCount   int64
	AvgLatencyMs float64
	Load         float64
}

type ServerEntry struct {
	ID           string
	Config       *ServerConfig
	Address      string
	Transport    TransportType
	Capabilities []string
	Health       HealthStatus
	Stats        ServerStats
	RegisteredAt time.Time
	LastSeenAt   time.Time
}

type EventType int

const (
	EventRegistered EventType = iota
	EventUnregistered
	EventHealthChanged
)

type RegistryEvent struct {
	Type    EventType
	Server  *ServerEntry
	Details string
}

type MatchCriteria struct {
	Capabilities []string
	Transport    TransportType
	MinHealth    HealthStatus
	MaxLoad      float64
}

type Registry interface {
	Register(ctx context.Context, entry ServerEntry) error
	Unregister(ctx context.Context, id string) error
	Update(ctx context.Context, entry ServerEntry) error

	Get(ctx context.Context, id string) (*ServerEntry, error)
	List(ctx context.Context) ([]*ServerEntry, error)
	FindByCapability(ctx context.Context, capability string) ([]*ServerEntry, error)
	FindByTransport(ctx context.Context, transport TransportType) ([]*ServerEntry, error)
	FindBest(ctx context.Context, criteria MatchCriteria) (*ServerEntry, error)

	UpdateHealth(ctx context.Context, id string, health HealthStatus) error
	ListUnhealthy(ctx context.Context) ([]*ServerEntry, error)

	Save(ctx context.Context) error
	Load(ctx context.Context) error

	Subscribe(ch chan<- RegistryEvent) func()
}

type inMemoryRegistry struct {
	servers  map[string]*ServerEntry
	byCap    map[string][]string
	byTrans  map[TransportType][]string
	mu       sync.RWMutex
	logger   *slog.Logger
	subMu    sync.RWMutex
	subs     []chan<- RegistryEvent
	persist  string
}

func NewRegistry(logger *slog.Logger, persistPath string) Registry {
	return &inMemoryRegistry{
		servers: make(map[string]*ServerEntry),
		byCap:   make(map[string][]string),
		byTrans: make(map[TransportType][]string),
		logger:  logger,
		persist: persistPath,
	}
}

func (r *inMemoryRegistry) Register(ctx context.Context, entry ServerEntry) error {
	if entry.ID == "" {
		return fmt.Errorf("registry: server ID is required: %w", contextToErr(ctx))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.servers[entry.ID]; exists {
		return fmt.Errorf("registry: server already registered: %s: %w", entry.ID, contextToErr(ctx))
	}

	entry.RegisteredAt = time.Now()
	entry.LastSeenAt = entry.RegisteredAt
	if entry.Health == "" {
		entry.Health = HealthUnknown
	}

	r.servers[entry.ID] = &entry

	for _, cap := range entry.Capabilities {
		r.byCap[cap] = append(r.byCap[cap], entry.ID)
	}
	r.byTrans[entry.Transport] = append(r.byTrans[entry.Transport], entry.ID)

	r.emitEvent(RegistryEvent{Type: EventRegistered, Server: &entry, Details: "server registered"})

	r.logger.Debug("server registered", "id", entry.ID, "transport", entry.Transport)

	return nil
}

func (r *inMemoryRegistry) Unregister(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.servers[id]
	if !exists {
		return fmt.Errorf("registry: server not found: %s: %w", id, contextToErr(ctx))
	}

	delete(r.servers, id)

	for _, cap := range entry.Capabilities {
		if ids, ok := r.byCap[cap]; ok {
			newIDs := make([]string, 0, len(ids))
			for _, sid := range ids {
				if sid != id {
					newIDs = append(newIDs, sid)
				}
			}
			r.byCap[cap] = newIDs
		}
	}

	if ids, ok := r.byTrans[entry.Transport]; ok {
		newIDs := make([]string, 0, len(ids))
		for _, sid := range ids {
			if sid != id {
				newIDs = append(newIDs, sid)
			}
		}
		r.byTrans[entry.Transport] = newIDs
	}

	r.emitEvent(RegistryEvent{Type: EventUnregistered, Server: entry, Details: "server unregistered"})

	r.logger.Debug("server unregistered", "id", id)

	return nil
}

func (r *inMemoryRegistry) Update(ctx context.Context, entry ServerEntry) error {
	if entry.ID == "" {
		return fmt.Errorf("registry: server ID is required: %w", contextToErr(ctx))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.servers[entry.ID]
	if !exists {
		return fmt.Errorf("registry: server not found: %s: %w", entry.ID, contextToErr(ctx))
	}

	for _, cap := range existing.Capabilities {
		if ids, ok := r.byCap[cap]; ok {
			newIDs := make([]string, 0, len(ids))
			for _, sid := range ids {
				if sid != entry.ID {
					newIDs = append(newIDs, sid)
				}
			}
			r.byCap[cap] = newIDs
		}
	}

	if ids, ok := r.byTrans[existing.Transport]; ok {
		newIDs := make([]string, 0, len(ids))
		for _, sid := range ids {
			if sid != entry.ID {
				newIDs = append(newIDs, sid)
			}
		}
		r.byTrans[existing.Transport] = newIDs
	}

	entry.RegisteredAt = existing.RegisteredAt
	entry.LastSeenAt = time.Now()
	r.servers[entry.ID] = &entry

	for _, cap := range entry.Capabilities {
		r.byCap[cap] = append(r.byCap[cap], entry.ID)
	}
	r.byTrans[entry.Transport] = append(r.byTrans[entry.Transport], entry.ID)

	r.logger.Debug("server updated", "id", entry.ID)

	return nil
}

func (r *inMemoryRegistry) Get(ctx context.Context, id string) (*ServerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.servers[id]
	if !ok {
		return nil, fmt.Errorf("registry: server not found: %s: %w", id, contextToErr(ctx))
	}

	result := *entry
	result.LastSeenAt = time.Now()
	return &result, nil
}

func (r *inMemoryRegistry) List(ctx context.Context) ([]*ServerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ServerEntry, 0, len(r.servers))
	for _, entry := range r.servers {
		e := *entry
		e.LastSeenAt = time.Now()
		result = append(result, &e)
	}
	return result, nil
}

func (r *inMemoryRegistry) FindByCapability(ctx context.Context, capability string) ([]*ServerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byCap[capability]
	result := make([]*ServerEntry, 0, len(ids))
	for _, id := range ids {
		if entry, ok := r.servers[id]; ok {
			e := *entry
			e.LastSeenAt = time.Now()
			result = append(result, &e)
		}
	}
	return result, nil
}

func (r *inMemoryRegistry) FindByTransport(ctx context.Context, transport TransportType) ([]*ServerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byTrans[transport]
	result := make([]*ServerEntry, 0, len(ids))
	for _, id := range ids {
		if entry, ok := r.servers[id]; ok {
			e := *entry
			e.LastSeenAt = time.Now()
			result = append(result, &e)
		}
	}
	return result, nil
}

func (r *inMemoryRegistry) FindBest(ctx context.Context, criteria MatchCriteria) (*ServerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var candidates []*ServerEntry

	if len(criteria.Capabilities) > 0 {
		capCount := make(map[string]int)
		for _, cap := range criteria.Capabilities {
			for _, id := range r.byCap[cap] {
				capCount[id]++
			}
		}
		for id, count := range capCount {
			if count == len(criteria.Capabilities) {
				if entry, ok := r.servers[id]; ok {
					candidates = append(candidates, entry)
				}
			}
		}
	} else {
		for _, entry := range r.servers {
			candidates = append(candidates, entry)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("registry: no servers match criteria: %w", contextToErr(ctx))
	}

	var best *ServerEntry
	bestScore := -1.0

	for _, entry := range candidates {
		if criteria.MinHealth != "" && entry.Health != criteria.MinHealth && entry.Health != HealthHealthy {
			continue
		}
		if criteria.MaxLoad > 0 && entry.Stats.Load > criteria.MaxLoad {
			continue
		}
		if criteria.Transport != "" && entry.Transport != criteria.Transport {
			continue
		}

		score := 1.0 - entry.Stats.Load
		if entry.Health == HealthHealthy {
			score += 0.5
		}
		score += float64(entry.Stats.RequestCount) * 0.0001

		if score > bestScore {
			bestScore = score
			best = entry
		}
	}

	if best == nil {
		return nil, fmt.Errorf("registry: no servers match criteria: %w", contextToErr(ctx))
	}

	result := *best
	result.LastSeenAt = time.Now()
	return &result, nil
}

func (r *inMemoryRegistry) UpdateHealth(ctx context.Context, id string, health HealthStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.servers[id]
	if !ok {
		return fmt.Errorf("registry: server not found: %s: %w", id, contextToErr(ctx))
	}

	entry.Health = health
	entry.LastSeenAt = time.Now()

	r.emitEvent(RegistryEvent{Type: EventHealthChanged, Server: entry, Details: fmt.Sprintf("health changed to %s", health)})

	r.logger.Debug("server health updated", "id", id, "health", health)

	return nil
}

func (r *inMemoryRegistry) ListUnhealthy(ctx context.Context) ([]*ServerEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ServerEntry, 0)
	for _, entry := range r.servers {
		if entry.Health == HealthUnhealthy || entry.Health == HealthUnknown {
			e := *entry
			result = append(result, &e)
		}
	}
	return result, nil
}

func (r *inMemoryRegistry) Save(ctx context.Context) error {
	if r.persist == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	data := struct {
		Servers []*ServerEntry `json:"servers"`
	}{
		Servers: make([]*ServerEntry, 0, len(r.servers)),
	}

	for _, entry := range r.servers {
		data.Servers = append(data.Servers, entry)
	}

	file, err := os.OpenFile(r.persist, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("registry: open persistence file: %w", err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("registry: encode persistence: %w", err)
	}

	r.logger.Debug("registry persisted", "count", len(data.Servers), "path", r.persist)

	return nil
}

func (r *inMemoryRegistry) Load(ctx context.Context) error {
	if r.persist == "" {
		return nil
	}

	file, err := os.Open(r.persist)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("registry: open persistence file: %w", err)
	}
	defer file.Close()

	var data struct {
		Servers []*ServerEntry `json:"servers"`
	}

	dec := json.NewDecoder(file)
	if err := dec.Decode(&data); err != nil {
		return fmt.Errorf("registry: decode persistence: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, entry := range data.Servers {
		r.servers[entry.ID] = entry
		for _, cap := range entry.Capabilities {
			r.byCap[cap] = append(r.byCap[cap], entry.ID)
		}
		r.byTrans[entry.Transport] = append(r.byTrans[entry.Transport], entry.ID)
	}

	r.logger.Debug("registry loaded", "count", len(data.Servers), "path", r.persist)

	return nil
}

func (r *inMemoryRegistry) Subscribe(ch chan<- RegistryEvent) func() {
	r.subMu.Lock()
	defer r.subMu.Unlock()

	r.subs = append(r.subs, ch)

	return func() {
		r.subMu.Lock()
		defer r.subMu.Unlock()

		for i, sub := range r.subs {
			if sub == ch {
				r.subs = append(r.subs[:i], r.subs[i+1:]...)
				break
			}
		}
	}
}

func (r *inMemoryRegistry) emitEvent(event RegistryEvent) {
	r.subMu.RLock()
	defer r.subMu.RUnlock()

	for _, sub := range r.subs {
		select {
		case sub <- event:
		default:
			r.logger.Warn("event channel full, dropping event", "type", event.Type)
		}
	}
}

func contextToErr(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}