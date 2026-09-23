// Command protomcp is a stdio MCP server speaking the 2025 protocol
// revisions, for the end-to-end tests of issues #307 (version negotiation,
// tool metadata / rich result passthrough, resources and prompts
// aggregation) and #308 (server-to-client requests, progress,
// cancellation, resource updates). It answers initialize with the revision the client asked
// for and advertises tools, resources and prompts.
//
// Tools:
//
//	delete_repo       annotated destructiveHint:true, with an outputSchema
//	get_repo          annotated readOnlyHint:true
//	rich              answers with structuredContent (a >2^53 integer), a
//	                  resource_link and a text block leaking a fake secret
//	echo              answers with its arguments, verbatim, in a text block
//	notify_resources  sends notifications/resources/list_changed, then answers
//
// Server-to-client tools (issue #308), unlisted unless --list-relay-tools. ask_user, sample
// and roots answer with the client's raw reply in a text block and
// "sawSecret" (whether the reply carried the fake AWS key, to prove the
// proxy redacts the client's answers):
//
//	ask_user          sends elicitation/create and waits for the answer
//	sample            sends sampling/createMessage and waits for the answer
//	roots             sends roots/list and waits for the answer
//	ask_then_cancel   sends elicitation/create, cancels it 300 ms later
//	slow              sends 4 notifications/progress 500 ms apart (when the
//	                  call carries _meta.progressToken), then answers with
//	                  the token it received
//	hang              never answers; records the cancel notification
//	                  that names it
//	last_cancel       answers with the request id of the last hang call
//	                  that was canceled (waiting up to 5 s for one)
//	notify_updated    sends notifications/resources/updated for notes.txt
//	client_caps       answers with the capabilities of initialize
//
// Resources: file:///<name>/notes.txt (its text leaks a fake secret) and
// file:///<name>/big.txt; template file:///<name>/{path}; prompt "greet".
// Lists are served one item per page, so clients must follow nextCursor.
// Requests are handled concurrently.
//
// Usage: protomcp NAME [--list-relay-tools]. Fake credentials are built at runtime so no
// secret-looking literal is committed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var awsKey = "AKIA" + "IOSFODNN7EXAMPLE"

type message struct {
	ID     json.RawMessage  `json:"id,omitempty"`
	Method string           `json:"method"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *json.RawMessage `json:"error,omitempty"`
}

// peer is the connection state shared by the concurrent handlers.
type peer struct {
	name  string
	outMu sync.Mutex
	out   *bufio.Writer

	mu         sync.Mutex
	nextReq    int
	waiting    map[string]chan message
	hanging    map[string]chan struct{}
	lastCancel string
	caps       json.RawMessage
	// listRelay lists the relay tools in tools/list (--list-relay-tools),
	// so a router-based front end (serve) can route them.
	listRelay bool
}

// relayTools are the server-to-client test tools, hidden from tools/list
// unless --list-relay-tools is given.
var relayTools = []string{"ask_user", "sample", "roots", "ask_then_cancel", "slow", "hang", "last_cancel", "notify_updated", "client_caps"}

func (p *peer) write(v interface{}) {
	data, _ := json.Marshal(v)
	p.outMu.Lock()
	defer p.outMu.Unlock()
	_, _ = p.out.Write(append(data, '\n'))
	_ = p.out.Flush()
}

// ask sends a request to the client and waits (10 s) for its answer.
func (p *peer) ask(method string, params interface{}) (message, bool) {
	p.mu.Lock()
	p.nextReq++
	id := fmt.Sprintf("%q", fmt.Sprintf("%s-req-%d", p.name, p.nextReq))
	ch := make(chan message, 1)
	p.waiting[id] = ch
	p.mu.Unlock()
	p.write(map[string]interface{}{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params})
	select {
	case m := <-ch:
		return m, true
	case <-time.After(10 * time.Second):
		return message{}, false
	}
}

func main() {
	name := "proto"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	p := &peer{name: name, out: bufio.NewWriter(os.Stdout), waiting: map[string]chan message{}, hanging: map[string]chan struct{}{}}
	for _, a := range os.Args[2:] {
		if a == "--list-relay-tools" {
			p.listRelay = true
		}
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var msg message
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch {
		case msg.Method == "" && len(msg.ID) > 0:
			// The client's answer to one of our requests.
			p.mu.Lock()
			ch := p.waiting[string(msg.ID)]
			delete(p.waiting, string(msg.ID))
			p.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		case len(msg.ID) == 0:
			if msg.Method == "notifications/cancelled" {
				var c struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				_ = json.Unmarshal(msg.Params, &c)
				p.mu.Lock()
				if ch, ok := p.hanging[string(c.RequestID)]; ok {
					close(ch)
					delete(p.hanging, string(c.RequestID))
				}
				p.mu.Unlock()
			}
		default:
			go p.serve(msg)
		}
	}
}

func (p *peer) serve(msg message) {
	result, rpcErr, answer := handle(p, msg)
	if !answer {
		return
	}
	resp := map[string]interface{}{"jsonrpc": "2.0", "id": msg.ID}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	p.write(resp)
}

// replyResult reports the client's answer to a server-to-client request.
func replyResult(m message, ok bool) json.RawMessage {
	if !ok {
		return raw(`{"content":[{"type":"text","text":"no reply"}],"sawSecret":false}`)
	}
	line, _ := json.Marshal(m)
	text, _ := json.Marshal(string(line))
	saw := strings.Contains(string(line), awsKey)
	return raw(`{"content":[{"type":"text","text":` + string(text) + `}],"sawSecret":` + strconv.FormatBool(saw) + `}`)
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// page returns items[cursor] with the next cursor, one item per page.
func page(params json.RawMessage, key string, items []json.RawMessage) map[string]interface{} {
	var p struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(params, &p)
	i, _ := strconv.Atoi(p.Cursor)
	body := map[string]interface{}{key: []json.RawMessage{}}
	if i < len(items) {
		body[key] = items[i : i+1]
	}
	if i+1 < len(items) {
		body["nextCursor"] = strconv.Itoa(i + 1)
	}
	return body
}

func handle(pr *peer, msg message) (interface{}, map[string]interface{}, bool) {
	result, rpcErr := handleMethod(pr, msg)
	if result == nil && rpcErr == nil {
		return nil, nil, false
	}
	return result, rpcErr, true
}

func handleMethod(pr *peer, msg message) (interface{}, map[string]interface{}) {
	name, write := pr.name, pr.write
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string          `json:"protocolVersion"`
			Capabilities    json.RawMessage `json:"capabilities"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		pr.mu.Lock()
		pr.caps = p.Capabilities
		pr.mu.Unlock()
		return map[string]interface{}{
			"protocolVersion": p.ProtocolVersion,
			"capabilities": map[string]interface{}{
				"tools":     map[string]interface{}{},
				"resources": map[string]interface{}{"listChanged": true, "subscribe": true},
				"prompts":   map[string]interface{}{},
			},
			"serverInfo": map[string]string{"name": "protomcp-" + name, "version": "1.0.0"},
		}, nil
	case "ping":
		return map[string]interface{}{}, nil
	case "tools/list":
		tools := []json.RawMessage{
			raw(`{"name":"delete_repo","title":"Delete repository","description":"Delete a repository","inputSchema":{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"]},"outputSchema":{"type":"object","properties":{"deleted":{"type":"boolean"}}},"annotations":{"destructiveHint":true},"_meta":{"ui/resourceUri":"ui://` + name + `/delete"}}`),
			raw(`{"name":"get_repo","description":"Read repository metadata","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}`),
			raw(`{"name":"rich","description":"Returns structured content and a resource link","inputSchema":{"type":"object"}}`),
			raw(`{"name":"echo","description":"Echoes its arguments","inputSchema":{"type":"object"}}`),
			raw(`{"name":"notify_resources","description":"Announces a resource list change","inputSchema":{"type":"object"}}`),
		}
		if pr.listRelay {
			for _, t := range relayTools {
				tools = append(tools, raw(`{"name":"`+t+`","description":"Relay test tool `+t+`","inputSchema":{"type":"object"}}`))
			}
		}
		return page(msg.Params, "tools", tools), nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      struct {
				ProgressToken json.RawMessage `json:"progressToken"`
			} `json:"_meta"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		switch p.Name {
		case "ask_user":
			return replyResult(pr.ask("elicitation/create", map[string]interface{}{
				"message":         "What is your name?",
				"requestedSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]string{"type": "string"}}},
			})), nil
		case "sample":
			return replyResult(pr.ask("sampling/createMessage", map[string]interface{}{
				"messages":  []map[string]interface{}{{"role": "user", "content": map[string]string{"type": "text", "text": "Summarize the repo"}}},
				"maxTokens": 16,
			})), nil
		case "roots":
			return replyResult(pr.ask("roots/list", nil)), nil
		case "ask_then_cancel":
			pr.mu.Lock()
			pr.nextReq++
			id := fmt.Sprintf("%s-req-%d", name, pr.nextReq)
			pr.mu.Unlock()
			write(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": "elicitation/create", "params": map[string]interface{}{
				"message": "Never mind", "requestedSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			}})
			time.Sleep(300 * time.Millisecond)
			write(map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]string{"requestId": id, "reason": "changed my mind"}})
			return raw(`{"content":[{"type":"text","text":"canceled ` + id + `"}]}`), nil
		case "slow":
			for i := 1; i <= 4; i++ {
				time.Sleep(500 * time.Millisecond)
				if len(p.Meta.ProgressToken) > 0 {
					write(map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]interface{}{
						"progressToken": p.Meta.ProgressToken, "progress": i, "total": 4, "message": fmt.Sprintf("step %d", i),
					}})
				}
			}
			text, _ := json.Marshal("done token=" + string(p.Meta.ProgressToken))
			return raw(`{"content":[{"type":"text","text":` + string(text) + `}]}`), nil
		case "hang":
			ch := make(chan struct{})
			pr.mu.Lock()
			pr.hanging[string(msg.ID)] = ch
			pr.mu.Unlock()
			select {
			case <-ch:
				pr.mu.Lock()
				pr.lastCancel = string(msg.ID)
				pr.mu.Unlock()
				return nil, nil // canceled: no answer
			case <-time.After(15 * time.Second):
				return raw(`{"content":[{"type":"text","text":"never canceled"}]}`), nil
			}
		case "last_cancel":
			deadline := time.Now().Add(5 * time.Second)
			for {
				pr.mu.Lock()
				last := pr.lastCancel
				pr.mu.Unlock()
				if last != "" || time.Now().After(deadline) {
					text, _ := json.Marshal("last canceled: " + last)
					return raw(`{"content":[{"type":"text","text":` + string(text) + `}]}`), nil
				}
				time.Sleep(50 * time.Millisecond)
			}
		case "notify_updated":
			write(map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/resources/updated", "params": map[string]string{"uri": "file:///" + name + "/notes.txt"}})
			return raw(`{"content":[{"type":"text","text":"updated"}]}`), nil
		case "client_caps":
			pr.mu.Lock()
			caps := pr.caps
			pr.mu.Unlock()
			text, _ := json.Marshal("caps " + string(caps))
			return raw(`{"content":[{"type":"text","text":` + string(text) + `}]}`), nil
		case "rich":
			return raw(`{"content":[{"type":"text","text":"key ` + awsKey + `"},{"type":"resource_link","uri":"file:///` + name + `/notes.txt","name":"notes","mimeType":"text/plain"}],"structuredContent":{"id":9007199254740993,"server":"` + name + `"},"isError":false}`), nil
		case "echo":
			text, _ := json.Marshal("args " + string(p.Arguments))
			return raw(`{"content":[{"type":"text","text":` + string(text) + `}]}`), nil
		case "notify_resources":
			write(map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/resources/list_changed"})
			return raw(`{"content":[{"type":"text","text":"notified"}]}`), nil
		default:
			return raw(`{"content":[{"type":"text","text":"called ` + p.Name + `"}]}`), nil
		}
	case "resources/list":
		return page(msg.Params, "resources", []json.RawMessage{
			raw(`{"uri":"file:///` + name + `/notes.txt","name":"notes","mimeType":"text/plain"}`),
			raw(`{"uri":"file:///` + name + `/big.txt","name":"big","size":12345678901234567}`),
		}), nil
	case "resources/templates/list":
		return page(msg.Params, "resourceTemplates", []json.RawMessage{
			raw(`{"uriTemplate":"file:///` + name + `/{path}","name":"files"}`),
		}), nil
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		if !strings.HasPrefix(p.URI, "file:///"+name+"/") {
			return nil, map[string]interface{}{"code": -32002, "message": "Resource not found", "data": map[string]string{"uri": p.URI}}
		}
		text, _ := json.Marshal(fmt.Sprintf("%s from %s: AWS_ACCESS_KEY_ID=%s", p.URI, name, awsKey))
		return raw(`{"contents":[{"uri":"` + p.URI + `","mimeType":"text/plain","text":` + string(text) + `}]}`), nil
	case "prompts/list":
		return page(msg.Params, "prompts", []json.RawMessage{
			raw(`{"name":"greet","description":"Greets someone","arguments":[{"name":"who","required":true}]}`),
		}), nil
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		if p.Name != "greet" {
			return nil, map[string]interface{}{"code": -32602, "message": "unknown prompt " + p.Name}
		}
		text, _ := json.Marshal(fmt.Sprintf("Hello %s from %s", p.Arguments["who"], name))
		return raw(`{"description":"Greets someone","messages":[{"role":"user","content":{"type":"text","text":` + string(text) + `}}]}`), nil
	case "resources/subscribe", "resources/unsubscribe":
		return map[string]interface{}{}, nil
	default:
		return nil, map[string]interface{}{"code": -32601, "message": "method not found: " + msg.Method}
	}
}
