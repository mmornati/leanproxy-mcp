package registry

import (
	"context"
	"fmt"
	"sync"
)

type Server struct {
	Name    string
	URL     string
	Version string
}

type Registry interface {
	Register(ctx context.Context, server Server) error
	Unregister(ctx context.Context, name string) error
	List(ctx context.Context) ([]Server, error)
	Get(ctx context.Context, name string) (Server, error)
}

type registry struct {
	servers map[string]Server
	mu      sync.RWMutex
}

func New() Registry {
	return &registry{
		servers: make(map[string]Server),
	}
}

func (r *registry) Register(ctx context.Context, server Server) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if server.Name == "" {
		return fmt.Errorf("server name is required")
	}
	r.servers[server.Name] = server
	return nil
}

func (r *registry) Unregister(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.servers, name)
	return nil
}

func (r *registry) List(ctx context.Context) ([]Server, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Server, 0, len(r.servers))
	for _, s := range r.servers {
		result = append(result, s)
	}
	return result, nil
}

func (r *registry) Get(ctx context.Context, name string) (Server, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	server, ok := r.servers[name]
	if !ok {
		return Server{}, fmt.Errorf("server not found: %s", name)
	}
	return server, nil
}
