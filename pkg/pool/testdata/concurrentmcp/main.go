// Command concurrentmcp is a fake stdio MCP server used by the pkg/pool
// multiplexing tests (#294). Unlike a typical line-by-line fake it handles
// every request in its own goroutine, so responses come back in whatever
// order the requests finish, exactly like a real concurrent MCP server.
//
// Tools (tools/call "name" → behaviour):
//
//	sleep       {"ms":N,"tag":T} sleep N ms (ignoring cancellation) and answer {"tag":T}
//	echo        {"tag":T} answer {"tag":T} immediately
//	hang        never answer
//	exit        {"code":N} exit the process immediately with code N
//	ask_client  send a roots/list request to the client and answer with its reply
//	stats       answer counters: received requests, max concurrent tool calls,
//	            and the notifications/cancelled seen so far
//
// Every other method (initialize, tools/list, ping, ...) gets a minimal
// successful result.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type cancelRecord struct {
	RequestID json.RawMessage `json:"requestId"`
	Reason    string          `json:"reason"`
}

type server struct {
	outMu sync.Mutex
	out   *bufio.Writer

	received      atomic.Int64
	active        atomic.Int64
	maxActive     atomic.Int64
	nextClientReq atomic.Int64

	mu        sync.Mutex
	cancelled []cancelRecord
	waiting   map[string]chan json.RawMessage
}

func main() {
	s := &server{
		out:     bufio.NewWriter(os.Stdout),
		waiting: make(map[string]chan json.RawMessage),
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var msg message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		s.dispatch(msg, line)
	}
}

func (s *server) dispatch(msg message, raw []byte) {
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method == "" && hasID:
		// A reply to one of our server-to-client requests.
		s.mu.Lock()
		ch, ok := s.waiting[string(msg.ID)]
		delete(s.waiting, string(msg.ID))
		s.mu.Unlock()
		if ok {
			ch <- json.RawMessage(raw)
		}
	case msg.Method == "notifications/cancelled":
		var rec cancelRecord
		_ = json.Unmarshal(msg.Params, &rec)
		s.mu.Lock()
		s.cancelled = append(s.cancelled, rec)
		s.mu.Unlock()
	case !hasID:
		// Any other notification: ignore.
	default:
		s.received.Add(1)
		go s.handle(msg)
	}
}

func (s *server) write(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = s.out.Write(append(data, '\n'))
	_ = s.out.Flush()
}

func (s *server) reply(id json.RawMessage, result interface{}) {
	s.write(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *server) handle(msg message) {
	switch msg.Method {
	case "initialize":
		s.reply(msg.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "concurrentmcp", "version": "1.0.0"},
		})
	case "tools/list":
		s.reply(msg.ID, map[string]interface{}{"tools": []interface{}{
			map[string]interface{}{"name": "sleep", "description": "sleep ms milliseconds", "inputSchema": map[string]interface{}{"type": "object"}},
			map[string]interface{}{"name": "echo", "description": "echo tag", "inputSchema": map[string]interface{}{"type": "object"}},
		}})
	case "tools/call":
		s.toolCall(msg)
	default:
		s.reply(msg.ID, map[string]interface{}{})
	}
}

func (s *server) toolCall(msg message) {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			MS   int    `json:"ms"`
			Tag  string `json:"tag"`
			Code int    `json:"code"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(msg.Params, &p)

	n := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		m := s.maxActive.Load()
		if n <= m || s.maxActive.CompareAndSwap(m, n) {
			break
		}
	}

	switch p.Name {
	case "sleep":
		time.Sleep(time.Duration(p.Arguments.MS) * time.Millisecond)
		s.reply(msg.ID, toolResult(p.Arguments.Tag))
	case "echo":
		s.reply(msg.ID, toolResult(p.Arguments.Tag))
	case "hang":
		select {}
	case "exit":
		os.Exit(p.Arguments.Code)
	case "ask_client":
		id := fmt.Sprintf("%q", fmt.Sprintf("srv-%d", s.nextClientReq.Add(1)))
		ch := make(chan json.RawMessage, 1)
		s.mu.Lock()
		s.waiting[id] = ch
		s.mu.Unlock()
		s.write(map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": "roots/list"})
		select {
		case r := <-ch:
			s.reply(msg.ID, map[string]interface{}{"reply": r})
		case <-time.After(5 * time.Second):
			s.reply(msg.ID, map[string]interface{}{"reply": nil})
		}
	case "stats":
		s.mu.Lock()
		cancelled := append([]cancelRecord(nil), s.cancelled...)
		s.mu.Unlock()
		s.reply(msg.ID, map[string]interface{}{
			"received":      s.received.Load(),
			"maxActive":     s.maxActive.Load(),
			"cancellations": cancelled,
		})
	default:
		s.write(map[string]interface{}{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]interface{}{"code": -32602, "message": "unknown tool " + p.Name}})
	}
}

func toolResult(tag string) map[string]interface{} {
	return map[string]interface{}{
		"tag":     tag,
		"content": []interface{}{map[string]interface{}{"type": "text", "text": tag}},
	}
}
