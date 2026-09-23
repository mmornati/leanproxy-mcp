package pool

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// BenchmarkRelayLargeResponse measures the cost of relaying one large
// (≈840 KB) response from a spawned server's stdout back to the caller, at
// the default (non-debug) log level. It guards the fix for issue #304: the
// reader used to log every stdout line (`"line", string(line)`) and the
// send path formatted responses with `fmt.Sprintf("%+v", resp)`, both paid
// even when nothing was going to be logged.
//
// The fake server is a shell one-liner that reads each request line, pulls
// its numeric "id" out with sed, and streams back a canned response whose
// result payload is read from a file (so the ~840 KB payload never has to
// be embedded in the script's argv).
func BenchmarkRelayLargeResponse(b *testing.B) {
	const payloadSize = 840 * 1024

	tmp := b.TempDir()
	payloadFile := filepath.Join(tmp, "payload.txt")
	if err := os.WriteFile(payloadFile, []byte(strings.Repeat("x", payloadSize)), 0o600); err != nil {
		b.Fatalf("write payload fixture: %v", err)
	}

	script := `while read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  printf '{"jsonrpc":"2.0","id":%s,"result":{"data":"' "$id"
  cat ` + payloadFile + `
  printf '"}}\n'
done`

	config := StdioServerConfig{
		Name:    "bench-relay",
		Command: "sh",
		Args:    []string{"-c", script},
	}

	// A discarding handler at the default level: Debug records are never
	// built, matching "with the default log level" in the acceptance
	// criteria.
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	server := newServerV2("bench-relay", config, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := server.spawn(ctx); err != nil {
		b.Fatalf("spawn failed: %v", err)
	}
	defer server.stop()

	// Let the reader goroutines start before timing begins.
	time.Sleep(100 * time.Millisecond)

	b.ReportAllocs()
	b.SetBytes(payloadSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := server.sendRequest(ctx, Request{
			Method: "bench/relay",
			Params: json.RawMessage(`{}`),
		})
		if err != nil {
			b.Fatalf("request %d failed: %v", i, err)
		}
		if len(result) < payloadSize {
			b.Fatalf("request %d: short response (%d bytes)", i, len(result))
		}
	}
}
