// Command bigmcp is a stdio MCP server whose tools return very large
// results, for the response governor e2e tests (issue #319):
//
//   - big_text: ~200 KB of numbered lines, with a fake credential on line
//     1700 (the proxy must redact it before anything is spilled);
//   - big_json: a JSON array of 1000 issue objects, as a text item;
//   - big_error: a ~200 KB error result (isError: true);
//   - image: a ~300 KB image content item and a short caption;
//   - small: a few words.
//
// The fake credential is assembled at runtime so no secret-looking literal
// is committed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var ghToken = "ghp_" + strings.Repeat("a1B2", 9)

type request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     interface{}     `json:"id"`
}

func main() {
	out := bufio.NewWriter(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil || req.ID == nil {
			continue
		}
		resp := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		if result, rpcErr := handle(req); rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		data, _ := json.Marshal(resp)
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
}

func text(s string) map[string]interface{} {
	return map[string]interface{}{"content": []map[string]string{{"type": "text", "text": s}}}
}

// BigText is the big_text result.
func BigText() string {
	var b strings.Builder
	for i := 1; i <= 3500; i++ {
		if i == 1700 {
			fmt.Fprintf(&b, "line %05d: token=%s\n", i, ghToken)
			continue
		}
		fmt.Fprintf(&b, "line %05d: the quick brown fox jumps over the lazy dog\n", i)
	}
	return b.String()
}

func bigJSON() string {
	items := make([]map[string]interface{}, 1000)
	for i := range items {
		items[i] = map[string]interface{}{"number": i, "title": fmt.Sprintf("Issue %d", i), "state": "open", "labels": []string{"bug"}}
	}
	data, _ := json.Marshal(items)
	return string(data)
}

func handle(req request) (interface{}, map[string]interface{}) {
	switch req.Method {
	case "initialize":
		return map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]string{"name": "bigmcp", "version": "0.0.1"},
		}, nil
	case "tools/list":
		tools := []map[string]interface{}{}
		for _, name := range []string{"big_text", "big_json", "big_error", "image", "small"} {
			tools = append(tools, map[string]interface{}{"name": name, "description": "Returns a " + name + " result.", "inputSchema": map[string]interface{}{"type": "object"}})
		}
		return map[string]interface{}{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &p)
		switch p.Name {
		case "big_text":
			return text(BigText()), nil
		case "big_json":
			return text(bigJSON()), nil
		case "big_error":
			r := text("upstream failure, full log follows:\n" + BigText())
			r["isError"] = true
			return r, nil
		case "image":
			return map[string]interface{}{"content": []map[string]string{
				{"type": "image", "mimeType": "image/png", "data": strings.Repeat("iVBORw0KGgoAAAANSUhEUgAA", 12500)},
				{"type": "text", "text": "a screenshot"},
			}}, nil
		case "small":
			return text("just a few words"), nil
		}
		return nil, map[string]interface{}{"code": -32602, "message": "unknown tool " + p.Name}
	case "ping":
		return map[string]interface{}{}, nil
	default:
		return nil, map[string]interface{}{"code": -32601, "message": "method not found: " + req.Method}
	}
}
