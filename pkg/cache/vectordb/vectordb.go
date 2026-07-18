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

	defaultDim = 1536
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
	if logger == nil {
		logger = slog.Default()
	}
	if cfg == nil {
		cfg = &migrate.VectorStoreConfig{Backend: string(BackendSQLite)}
	}
	dim := cfg.Dimension
	if dim <= 0 {
		dim = defaultDim
	}
	switch Backend(cfg.Backend) {
	case BackendSQLite:
		return newSQLiteStore(cfg.SQLite, dim, logger)
	case BackendQdrant:
		return newQdrantStore(cfg.Qdrant, dim, logger)
	case BackendPinecone:
		return newPineconeStore(cfg.Pinecone, logger)
	default:
		return nil, fmt.Errorf("vectordb: unknown backend %q", cfg.Backend)
	}
}
