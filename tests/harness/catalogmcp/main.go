// Command catalogmcp is the stdio MCP server the end-to-end harness
// (tests/harness, issue #301) proxies through leanproxy-mcp. It serves one
// server of the embedded 118-tool catalog (testdata/catalog.json).
//
// Every tools/call on a catalog tool answers with a text block echoing the
// tool name and arguments, plus a "fixtureHits" field counting how many of
// the harness's fake credentials arrived in the request params: the harness
// uses it to prove request-direction redaction without trusting the
// (response-redacted) echo.
//
// The hidden tool "ask_client" (not listed) sends a roots/list request to
// the client and answers with {"clientReplied": bool} once the reply arrives
// or after 5 s, so the harness can check server-to-client requests never
// hang the proxy.
//
// Flags:
//
//	--server NAME          catalog server to serve (required)
//	--delay-ms N           sleep N ms before answering each tools/call
//	--response-bytes N     pad each tools/call text to at least N bytes
//	--secrets              embed fake credentials in every tools/call result
//	                       and in the description of the first tool
//	--concurrent           handle requests concurrently; answers go out in
//	                       completion order, not request order
//	--resources            speak the 2025 revisions (#307): answer initialize
//	                       with the requested protocolVersion, advertise
//	                       resources and prompts, and serve one resource
//	                       (catalog://NAME/readme), one template and one
//	                       prompt ("brief")
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mmornati/leanproxy-mcp/tests/harness"
)

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type server struct {
	name          string
	tools         map[string]bool
	toolsList     json.RawMessage
	delay         time.Duration
	responseBytes int
	secrets       bool
	resources     bool

	outMu sync.Mutex
	out   *bufio.Writer

	nextClientReq atomic.Int64
	mu            sync.Mutex
	waiting       map[string]chan struct{}
}

func main() {
	name := flag.String("server", "", "catalog server to serve")
	delayMS := flag.Int("delay-ms", 0, "sleep this many ms before answering each tools/call")
	responseBytes := flag.Int("response-bytes", 0, "pad each tools/call text to at least this many bytes")
	secrets := flag.Bool("secrets", false, "embed fake credentials in tools/call results and in the first tool's description")
	concurrent := flag.Bool("concurrent", false, "handle requests concurrently, answering out of order")
	resources := flag.Bool("resources", false, "advertise and serve resources and prompts, echo the requested protocol version")
	flag.Parse()

	cat, err := harness.LoadCatalog()
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalogmcp:", err)
		os.Exit(1)
	}
	srv := cat.Server(*name)
	if srv == nil {
		fmt.Fprintf(os.Stderr, "catalogmcp: unknown --server %q\n", *name)
		os.Exit(2)
	}
	tools := append([]harness.Tool(nil), srv.Tools...)
	if *secrets && len(tools) > 0 {
		// A leaky upstream tool description: search_tools and list_tools
		// output must redact it.
		tools[0].Description += " Example config: " + strings.Join(harness.FakeSecrets(), " ")
	}
	toolsList, err := json.Marshal(map[string]interface{}{"tools": tools})
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalogmcp:", err)
		os.Exit(1)
	}
	s := &server{
		name:          srv.Name,
		tools:         make(map[string]bool, len(srv.Tools)),
		toolsList:     toolsList,
		delay:         time.Duration(*delayMS) * time.Millisecond,
		responseBytes: *responseBytes,
		secrets:       *secrets,
		resources:     *resources,
		out:           bufio.NewWriterSize(os.Stdout, 64*1024),
		waiting:       make(map[string]chan struct{}),
	}
	for _, t := range srv.Tools {
		s.tools[t.Name] = true
	}

	reader := bufio.NewReaderSize(os.Stdin, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var msg message
			if json.Unmarshal(line, &msg) == nil {
				s.dispatch(msg, *concurrent)
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *server) dispatch(msg message, concurrent bool) {
	hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
	switch {
	case msg.Method == "" && hasID:
		// The client's reply to one of our server-to-client requests.
		s.mu.Lock()
		ch, ok := s.waiting[string(msg.ID)]
		delete(s.waiting, string(msg.ID))
		s.mu.Unlock()
		if ok {
			close(ch)
		}
	case !hasID:
		// Notification (notifications/initialized, cancellations): ignore.
	case toolName(msg) == "ask_client":
		// Always asynchronous: its answer depends on a message this loop
		// has yet to read.
		go s.askClient(msg)
	case concurrent:
		go s.handle(msg)
	default:
		s.handle(msg)
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

func (s *server) handle(msg message) {
	switch msg.Method {
	case "initialize":
		if s.resources {
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			s.reply(msg.ID, map[string]interface{}{
				"protocolVersion": p.ProtocolVersion,
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{}, "resources": map[string]interface{}{}, "prompts": map[string]interface{}{},
				},
				"serverInfo": map[string]string{"name": s.name, "version": "1.0.0"},
			})
			return
		}
		s.reply(msg.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]string{"name": s.name, "version": "1.0.0"},
		})
	case "resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get":
		if !s.resources {
			s.replyError(msg.ID, -32601, "method not found: "+msg.Method)
			return
		}
		s.resourceMethod(msg)
	case "tools/list":
		s.reply(msg.ID, s.toolsList)
	case "tools/call":
		s.toolCall(msg)
	case "ping":
		s.reply(msg.ID, map[string]interface{}{})
	default:
		s.replyError(msg.ID, -32601, "method not found: "+msg.Method)
	}
}

func (s *server) toolCall(msg message) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		s.replyError(msg.ID, -32602, "invalid params")
		return
	}
	if !s.tools[p.Name] {
		s.replyError(msg.ID, -32602, "unknown tool "+p.Name)
		return
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	payload := map[string]interface{}{"server": s.name, "called": p.Name, "args": p.Arguments}
	if s.secrets {
		payload["note"] = "config dump: " + strings.Join(harness.FakeSecrets(), " ")
	}
	text, _ := json.Marshal(payload)
	if pad := s.responseBytes - len(text); pad > 0 {
		text = append(text, ' ')
		text = append(text, filler(pad)...)
	}
	s.reply(msg.ID, map[string]interface{}{
		"content":     []map[string]string{{"type": "text", "text": string(text)}},
		"fixtureHits": harness.CountFakeSecrets(string(msg.Params)),
	})
}

// resourceMethod serves the --resources resources and prompts.
func (s *server) resourceMethod(msg message) {
	uri := "catalog://" + s.name + "/readme"
	switch msg.Method {
	case "resources/list":
		s.reply(msg.ID, map[string]interface{}{"resources": []map[string]string{{"uri": uri, "name": "readme", "mimeType": "text/plain"}}})
	case "resources/templates/list":
		s.reply(msg.ID, map[string]interface{}{"resourceTemplates": []map[string]string{{"uriTemplate": "catalog://" + s.name + "/{doc}", "name": "docs"}}})
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		s.reply(msg.ID, map[string]interface{}{"contents": []map[string]string{{"uri": p.URI, "mimeType": "text/plain", "text": "readme of " + s.name}}})
	case "prompts/list":
		s.reply(msg.ID, map[string]interface{}{"prompts": []map[string]string{{"name": "brief", "description": "Brief about " + s.name}}})
	case "prompts/get":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		s.reply(msg.ID, map[string]interface{}{"messages": []map[string]interface{}{{"role": "user", "content": map[string]string{"type": "text", "text": p.Name + " from " + s.name}}}})
	}
}

// filler returns n bytes of plain prose, so a large response looks like
// text rather than one repeated byte.
func filler(n int) []byte {
	const chunk = "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore. "
	b := make([]byte, 0, n)
	for len(b) < n {
		b = append(b, chunk[:min(len(chunk), n-len(b))]...)
	}
	return b
}

func (s *server) askClient(msg message) {
	id := fmt.Sprintf("%q", fmt.Sprintf("catalogmcp-%d", s.nextClientReq.Add(1)))
	ch := make(chan struct{})
	s.mu.Lock()
	s.waiting[id] = ch
	s.mu.Unlock()
	s.write(map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": "roots/list"})
	replied := false
	select {
	case <-ch:
		replied = true
	case <-time.After(5 * time.Second):
	}
	s.reply(msg.ID, map[string]interface{}{
		"content":       []map[string]string{{"type": "text", "text": fmt.Sprintf("client replied: %v", replied)}},
		"clientReplied": replied,
	})
}

func (s *server) reply(id json.RawMessage, result interface{}) {
	s.write(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *server) replyError(id json.RawMessage, code int, message string) {
	s.write(map[string]interface{}{"jsonrpc": "2.0", "id": id, "error": map[string]interface{}{"code": code, "message": message}})
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
