package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestHandleConnection_ConcurrentGatewayWritesAreSerialized: gateway-tool
// responses on the async path share the connection's bufio.Writer with every
// other in-flight request and must be written under writerMu. Run with -race.
func TestHandleConnection_ConcurrentGatewayWritesAreSerialized(t *testing.T) {
	const n = 300
	var sb strings.Builder
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			fmt.Fprintf(&sb, `{"jsonrpc":"2.0","id":%d,"method":"list_servers"}`+"\n", i)
		} else {
			fmt.Fprintf(&sb, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"k":"v"}}`+"\n", i)
		}
	}
	out := &bytes.Buffer{}
	handleConnection(&mockReadWriter{reader: strings.NewReader(sb.String()), writer: out}, &mockRouter{}, &mockGatewayTools{}, &mockPool{})

	lines := bytes.Split(bytes.TrimRight(out.Bytes(), "\n"), []byte("\n"))
	if len(lines) != n {
		t.Fatalf("expected %d response lines, got %d", n, len(lines))
	}
	for i, l := range lines {
		if !bytes.HasPrefix(l, []byte(`{"jsonrpc":"2.0"`)) || !bytes.HasSuffix(l, []byte("}")) {
			t.Fatalf("line %d is not a complete JSON-RPC response (interleaved write?): %q", i, l)
		}
	}
}
