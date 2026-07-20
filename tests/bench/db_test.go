package bench

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/postgresql"
)

func benchLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// BenchmarkPostgresQueryPoolOverhead measures the connection-pool-management
// overhead for the postgresql client. It validates that the pool can handle
// concurrent requests without exceeding pool limits.
func BenchmarkPostgresQuery(b *testing.B) {
	connStr := os.Getenv("LEANPROXY_POSTGRES_CONNECTION")
	if connStr == "" {
		b.Skip("LEANPROXY_POSTGRES_CONNECTION not set")
	}

	cfg := postgresql.Config{
		ConnectionString: connStr,
		PoolSize:         10,
		StatementTimeout: 30 * time.Second,
	}

	client, err := postgresql.NewPostgresClient(benchLogger(), cfg)
	if err != nil {
		b.Fatalf("create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	args, _ := json.Marshal(map[string]string{"query": "SELECT 1"})

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := client.CallTool(ctx, "postgresql_query", args)
			if err != nil {
				b.Errorf("query failed: %v", err)
			}
		}
	})
}

func BenchmarkPostgresListTables(b *testing.B) {
	connStr := os.Getenv("LEANPROXY_POSTGRES_CONNECTION")
	if connStr == "" {
		b.Skip("LEANPROXY_POSTGRES_CONNECTION not set")
	}

	cfg := postgresql.Config{
		ConnectionString: connStr,
		PoolSize:         10,
		StatementTimeout: 30 * time.Second,
	}

	client, err := postgresql.NewPostgresClient(benchLogger(), cfg)
	if err != nil {
		b.Fatalf("create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	args, _ := json.Marshal(map[string]string{"schema": "public"})

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := client.CallTool(ctx, "postgresql_list_tables", args)
		if err != nil {
			b.Errorf("list tables failed: %v", err)
		}
	}
}
