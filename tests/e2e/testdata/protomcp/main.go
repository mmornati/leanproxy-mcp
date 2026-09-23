// Command protomcp is a stdio MCP server speaking the 2025 protocol
// revisions, for the end-to-end tests of issue #307 (version negotiation,
// tool metadata / rich result passthrough, resources and prompts
// aggregation). It answers initialize with the revision the client asked
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
// Resources: file:///<name>/notes.txt (its text leaks a fake secret) and
// file:///<name>/big.txt; template file:///<name>/{path}; prompt "greet".
// Lists are served one item per page, so clients must follow nextCursor.
//
// Usage: protomcp NAME. Fake credentials are built at runtime so no
// secret-looking literal is committed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var awsKey = "AKIA" + "IOSFODNN7EXAMPLE"

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

func main() {
	name := "proto"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	out := bufio.NewWriter(os.Stdout)
	write := func(v interface{}) {
		data, _ := json.Marshal(v)
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var msg message
		if json.Unmarshal(sc.Bytes(), &msg) != nil || len(msg.ID) == 0 {
			continue // notification
		}
		result, rpcErr := handle(name, msg, write)
		resp := map[string]interface{}{"jsonrpc": "2.0", "id": msg.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		write(resp)
	}
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

func handle(name string, msg message, write func(interface{})) (interface{}, map[string]interface{}) {
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		return map[string]interface{}{
			"protocolVersion": p.ProtocolVersion,
			"capabilities": map[string]interface{}{
				"tools":     map[string]interface{}{},
				"resources": map[string]interface{}{"listChanged": true},
				"prompts":   map[string]interface{}{},
			},
			"serverInfo": map[string]string{"name": "protomcp-" + name, "version": "1.0.0"},
		}, nil
	case "ping":
		return map[string]interface{}{}, nil
	case "tools/list":
		return page(msg.Params, "tools", []json.RawMessage{
			raw(`{"name":"delete_repo","title":"Delete repository","description":"Delete a repository","inputSchema":{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"]},"outputSchema":{"type":"object","properties":{"deleted":{"type":"boolean"}}},"annotations":{"destructiveHint":true},"_meta":{"ui/resourceUri":"ui://` + name + `/delete"}}`),
			raw(`{"name":"get_repo","description":"Read repository metadata","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true}}`),
			raw(`{"name":"rich","description":"Returns structured content and a resource link","inputSchema":{"type":"object"}}`),
			raw(`{"name":"echo","description":"Echoes its arguments","inputSchema":{"type":"object"}}`),
			raw(`{"name":"notify_resources","description":"Announces a resource list change","inputSchema":{"type":"object"}}`),
		}), nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		switch p.Name {
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
