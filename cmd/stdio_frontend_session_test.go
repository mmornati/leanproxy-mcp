package cmd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// notifyingSource is a pool.ServerSource whose one upstream serves
// resources, and an event source the test drives by hand.
type notifyingSource struct {
	resourceSource
	events chan pool.ServerEventHandler
}

func (s *notifyingSource) SetServerEventHandler(fn pool.ServerEventHandler) {
	if fn != nil {
		s.events <- fn
	}
}

// TestServeStdio_SessionAndListChangedNotification (#307): the stdio front
// end opens one MCP session for its client, the negotiated version is kept
// on it, and an upstream resource-list change reaches the client as a
// notifications/resources/list_changed line on stdout.
func TestServeStdio_SessionAndListChangedNotification(t *testing.T) {
	src := &notifyingSource{events: make(chan pool.ServerEventHandler, 1)}
	h := mcp.NewHandler(src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.StartBackgroundRefresh(ctx)
	emit := <-src.events

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- serveStdio(ctx, inR, outW, h, stdioFrontendOptions{}) }()
	lines := make(chan string, 16)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 4096)
		for {
			n, err := outR.Read(tmp)
			buf = append(buf, tmp[:n]...)
			for {
				i := strings.IndexByte(string(buf), '\n')
				if i < 0 {
					break
				}
				lines <- string(buf[:i])
				buf = buf[i+1:]
			}
			if err != nil {
				close(lines)
				return
			}
		}
	}()
	next := func() string {
		t.Helper()
		select {
		case l := <-lines:
			return l
		case <-time.After(frontendWait):
			t.Fatal("no output")
		}
		return ""
	}
	write := func(s string) {
		t.Helper()
		if _, err := io.WriteString(inW, s+"\n"); err != nil {
			t.Fatal(err)
		}
	}

	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	line := next()
	if !strings.Contains(line, `"protocolVersion":"2025-03-26"`) || !strings.Contains(line, `"resources":{"listChanged":true}`) {
		t.Fatalf("initialize = %s", line)
	}
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	emit(pool.ServerEvent{Server: "docs", Kind: pool.EventResourcesListChanged, Generation: 1})
	line = next()
	var note struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &note); err != nil || note.Method != mcp.NotificationResourcesListChanged || note.JSONRPC != "2.0" || note.ID != nil {
		t.Fatalf("notification = %s (%v)", line, err)
	}

	// The session's version gates the tools/list fields: 2025-03-26 has
	// annotations.
	write(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if line = next(); !strings.Contains(line, `"readOnlyHint":true`) {
		t.Fatalf("tools/list = %s", line)
	}

	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(frontendWait):
		t.Fatal("serveStdio did not return")
	}
}
