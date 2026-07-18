package vectordb

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

type Backend string

const (
	BackendSQLite   Backend = "sqlite-vec"
	BackendQdrant   Backend = "qdrant"
	BackendPinecone Backend = "pinecone"
)

type VectorRecord struct {
	ID       string
	Vector   []float32
	Metadata map[string]string
}

type SearchResult struct {
	Record VectorRecord
	Score  float64
}

type Store interface {
	Upsert(ctx context.Context, records ...VectorRecord) error
	Search(ctx context.Context, vector []float32, k int) ([]SearchResult, error)
	Delete(ctx context.Context, ids ...string) error
	Close() error
}

func NewStore(cfg *migrate.VectorStoreConfig, logger *slog.Logger) (Store, error) {
	if cfg == nil {
		cfg = &migrate.VectorStoreConfig{Backend: string(BackendSQLite)}
	}
	switch Backend(cfg.Backend) {
	case BackendSQLite:
		return newSQLiteStore(cfg.SQLite, logger)
	case BackendQdrant:
		return newQdrantStore(cfg.Qdrant, logger)
	case BackendPinecone:
		return newPineconeStore(cfg.Pinecone, logger)
	default:
		return nil, fmt.Errorf("vectordb: unknown backend %q", cfg.Backend)
	}
}
