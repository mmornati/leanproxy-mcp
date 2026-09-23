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
//	big         {"bytes":N,"id_last":B} answer with a text of N bytes (default
//	            --response-bytes); id_last writes the "id" as the last member
//	stderr      {"bytes":N,"text":T} write one stderr line (T, or N bytes) then
//	            answer
//	stop_reading  answer, then never read stdin again
//	close_stdout  answer, then close stdout and keep running
//	list_changed  send notifications/tools/list_changed, then answer
//	add_tool      {"tag":T} add a tool named T to tools/list, then answer
//
// Session checks (#297): "received" counts requests other than initialize;
// "stats" also reports how many initialize requests and
// notifications/initialized were seen and how many other requests arrived
// before the session was initialized (each of those is answered with an
// error, like a strict MCP server).
//
// Flags (#295):
//
//	--response-bytes N     default size of the "big" tool's text
//	--stderr-bytes N       write one N-byte stderr line at startup
//	--spawn-child FILE     start a "sleep 1000" grandchild, write its PID to FILE
//	--ignore-sigterm       ignore SIGTERM (stop must escalate to SIGKILL)
//	--instructions S       instructions returned by initialize
//	--init-delay-ms N      answer initialize after N ms
//
// Every other method (initialize, tools/list, ping, ...) gets a minimal
// successful result.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	responseBytes = flag.Int("response-bytes", 1<<20, "default size of the big tool's text")
	instructions  = flag.String("instructions", "", "instructions returned by initialize")
	initDelayMS   = flag.Int("init-delay-ms", 0, "answer initialize after this many ms")
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
	extra     []string

	initializes    atomic.Int64
	initializedN   atomic.Int64
	earlyRequests  atomic.Int64
	sessionStarted atomic.Bool
}

func main() {
	stderrBytes := flag.Int("stderr-bytes", 0, "write one stderr line of this size at startup")
	spawnChild := flag.String("spawn-child", "", "start a sleep grandchild and write its PID to this file")
	ignoreSIGTERM := flag.Bool("ignore-sigterm", false, "ignore SIGTERM")
	flag.Parse()

	if *ignoreSIGTERM {
		signal.Ignore(syscall.SIGTERM)
	}
	if *stderrBytes > 0 {
		_, _ = os.Stderr.Write(append(bytes.Repeat([]byte("e"), *stderrBytes), '\n'))
	}
	if *spawnChild != "" {
		child := exec.Command("sleep", "1000")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "spawn child:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*spawnChild, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(1)
		}
	}

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
		if toolName(msg) == "stop_reading" {
			// Synchronous so no further line is read: answer, then never
			// read stdin again (the pipe fills up behind us).
			s.reply(msg.ID, toolResult("stopped"))
			for {
				time.Sleep(time.Hour)
			}
		}
		s.dispatch(msg, line)
	}
}

func toolName(msg message) string {
	if msg.Method != "tools/call" {
		return ""
	}
	var p struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	return p.Name
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
	case msg.Method == "notifications/initialized":
		s.initializedN.Add(1)
		s.sessionStarted.Store(true)
	case !hasID:
		// Any other notification: ignore.
	case msg.Method == "initialize":
		s.initializes.Add(1)
		go s.handle(msg)
	case !s.sessionStarted.Load():
		s.earlyRequests.Add(1)
		s.write(map[string]interface{}{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]interface{}{"code": -32002, "message": "session not initialized"}})
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
		time.Sleep(time.Duration(*initDelayMS) * time.Millisecond)
		result := map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": true}},
			"serverInfo":      map[string]interface{}{"name": "concurrentmcp", "version": "1.0.0"},
		}
		if *instructions != "" {
			result["instructions"] = *instructions
		}
		s.reply(msg.ID, result)
	case "tools/list":
		tools := []interface{}{
			map[string]interface{}{"name": "sleep", "description": "sleep ms milliseconds", "inputSchema": map[string]interface{}{"type": "object"}},
			map[string]interface{}{"name": "echo", "description": "echo tag", "inputSchema": map[string]interface{}{"type": "object"}},
			map[string]interface{}{"name": "big", "description": "answer with a text of bytes bytes", "inputSchema": map[string]interface{}{"type": "object"}},
		}
		s.mu.Lock()
		for _, name := range s.extra {
			tools = append(tools, map[string]interface{}{"name": name, "description": name, "inputSchema": map[string]interface{}{"type": "object"}})
		}
		s.mu.Unlock()
		s.reply(msg.ID, map[string]interface{}{"tools": tools})
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
			MS     int    `json:"ms"`
			Tag    string `json:"tag"`
			Code   int    `json:"code"`
			Bytes  int    `json:"bytes"`
			IDLast bool   `json:"id_last"`
			Text   string `json:"text"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(msg.Params, &p)

	n := s.active.Add(1)
	// Leave the active count before answering: once the proxy reads the
	// answer it may write the next request, which must not be counted
	// as overlapping this one.
	release := sync.OnceFunc(func() { s.active.Add(-1) })
	defer release()
	for {
		m := s.maxActive.Load()
		if n <= m || s.maxActive.CompareAndSwap(m, n) {
			break
		}
	}

	switch p.Name {
	case "sleep":
		time.Sleep(time.Duration(p.Arguments.MS) * time.Millisecond)
		release()
		s.reply(msg.ID, toolResult(p.Arguments.Tag))
	case "echo":
		release()
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
	case "big":
		n := p.Arguments.Bytes
		if n <= 0 {
			n = *responseBytes
		}
		text := strings.Repeat("x", n)
		if !p.Arguments.IDLast {
			s.reply(msg.ID, toolResult(text))
			return
		}
		result, _ := json.Marshal(toolResult(text))
		line := fmt.Sprintf(`{"jsonrpc":"2.0","result":%s,"id":%s}`, result, msg.ID)
		s.outMu.Lock()
		_, _ = s.out.WriteString(line + "\n")
		_ = s.out.Flush()
		s.outMu.Unlock()
	case "stderr":
		text := p.Arguments.Text
		if text == "" {
			text = strings.Repeat("e", p.Arguments.Bytes)
		}
		_, _ = os.Stderr.WriteString(text + "\n")
		s.reply(msg.ID, toolResult("ok"))
	case "close_stdout":
		s.outMu.Lock()
		_ = s.out.Flush()
		_ = os.Stdout.Close()
		s.outMu.Unlock()
	case "stats":
		s.mu.Lock()
		cancelled := append([]cancelRecord(nil), s.cancelled...)
		s.mu.Unlock()
		s.reply(msg.ID, map[string]interface{}{
			"received":      s.received.Load(),
			"maxActive":     s.maxActive.Load(),
			"cancellations": cancelled,
			"initializes":   s.initializes.Load(),
			"initialized":   s.initializedN.Load(),
			"earlyRequests": s.earlyRequests.Load(),
			"pid":           os.Getpid(),
		})
	case "list_changed":
		s.write(map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
		s.reply(msg.ID, toolResult("notified"))
	case "add_tool":
		s.mu.Lock()
		s.extra = append(s.extra, p.Arguments.Tag)
		s.mu.Unlock()
		s.reply(msg.ID, toolResult("added"))
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
