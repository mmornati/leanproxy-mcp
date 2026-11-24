package pool

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	lperrors "github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

func newTestUnifiedPool(t *testing.T) *UnifiedPool {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	p := NewUnifiedPool(NewStdioPool(5, time.Minute, logger), NewHTTPClientPool(logger), NewSSEPool(logger), logger)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestCallRecorderCountsCallsAndErrors(t *testing.T) {
	var r callRecorder
	done := r.begin("remote")
	if got := r.get("remote").inFlight; got != 1 {
		t.Fatalf("inFlight = %d, want 1", got)
	}
	done(&Response{}, nil)
	r.begin("remote")(nil, errors.New("boom"))
	r.begin("remote")(&Response{Error: &lperrors.JSONRPCError{Code: -32000, Message: "upstream said no"}}, nil)

	st := r.get("remote")
	if st.requests != 3 || st.errors != 2 || st.inFlight != 0 {
		t.Fatalf("requests=%d errors=%d inFlight=%d, want 3/2/0", st.requests, st.errors, st.inFlight)
	}
	if st.lastErr != "upstream said no" || st.lastErrAt.IsZero() || st.lastRequestAt.IsZero() {
		t.Fatalf("last error not recorded: %+v", st)
	}
	r.forget("remote")
	if r.get("remote").requests != 0 {
		t.Fatal("forget kept the counters")
	}
}

func TestConnStatusKeepsLastConnectError(t *testing.T) {
	var c connStatus
	c.note(errors.New("authorization required"), 0)
	if msg, at, connected := c.snapshot(); msg != "authorization required" || at.IsZero() || !connected.IsZero() {
		t.Fatalf("after a failure: %q %v %v", msg, at, connected)
	}
	// A new connection clears it; later calls on the same connection do
	// not move connectedAt.
	c.note(nil, 1)
	_, _, first := c.snapshot()
	c.note(nil, 1)
	msg, at, second := c.snapshot()
	if msg != "" || !at.IsZero() || first.IsZero() || !second.Equal(first) {
		t.Fatalf("after connecting: %q %v %v %v", msg, at, first, second)
	}
}

func TestCapabilityNames(t *testing.T) {
	got := capabilityNames([]byte(`{"tools":{"listChanged":true},"prompts":{},"logging":{}}`))
	want := []string{"logging", "prompts", "tools"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if capabilityNames(nil) != nil || capabilityNames([]byte(`[]`)) != nil {
		t.Fatal("missing or malformed capabilities must give no names")
	}
}

// A remote server that cannot connect must say why in its snapshot, and a
// removed server must leave the pool.
func TestUnifiedPoolSnapshotStartRemoveRemote(t *testing.T) {
	p := newTestUnifiedPool(t)
	cfg := &migrate.ServerConfig{
		Name:      "remote",
		Transport: registry.TransportHTTP,
		// A closed port: the connection is refused at once.
		HTTP: &migrate.HTTPConfig{URL: "http://127.0.0.1:1/mcp"},
	}
	if err := p.StartServer(context.Background(), cfg); err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	if err := p.StartServer(context.Background(), cfg); err == nil {
		t.Fatal("starting the same name twice must fail")
	}

	deadline := time.Now().Add(10 * time.Second)
	var snap ServerSnapshot
	for {
		var ok bool
		snap, ok = p.Snapshot("remote")
		if !ok {
			t.Fatal("no snapshot for a server in the pool")
		}
		if snap.State == StateError || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if snap.Transport != "http" || snap.State != StateError {
		t.Fatalf("transport=%q state=%q, want http/error", snap.Transport, snap.State)
	}
	if snap.LastError == "" || snap.LastErrorAt.IsZero() || snap.Initialized {
		t.Fatalf("a failed connection must be explained: %+v", snap)
	}
	if got := p.Snapshots(); len(got) != 1 || got[0].Name != "remote" {
		t.Fatalf("Snapshots = %+v", got)
	}

	if err := p.RemoveServer("remote"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	if _, ok := p.Snapshot("remote"); ok {
		t.Fatal("a removed server still has a snapshot")
	}
	if err := p.RemoveServer("remote"); err == nil {
		t.Fatal("removing an unknown server must fail")
	}
}

func TestUnifiedPoolStartServerRejectsBadConfig(t *testing.T) {
	p := newTestUnifiedPool(t)
	ctx := context.Background()
	if err := p.StartServer(ctx, nil); err == nil {
		t.Fatal("nil config accepted")
	}
	if err := p.StartServer(ctx, &migrate.ServerConfig{Name: "x", Transport: "carrier-pigeon"}); err == nil {
		t.Fatal("unknown transport accepted")
	}
	if err := p.StartServer(ctx, &migrate.ServerConfig{Name: "x", Transport: registry.TransportStdio}); err == nil {
		t.Fatal("stdio server without a stdio block accepted")
	}
}
