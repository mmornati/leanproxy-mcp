// Command fakemcp is a minimal stdio MCP server used by the end-to-end
// firewall tests. It appends every raw request line it receives to the file
// named by its first argument (when given), and deliberately leaks fake
// credentials in its tool descriptions, tool results and error responses so
// the tests can assert that leanproxy-mcp redacts them in both directions.
//
// Fake credentials are assembled at runtime so no secret-looking literal is
// committed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var (
	awsKey    = "AKIA" + "IOSFODNN7EXAMPLE"
	ghToken   = "ghp_" + strings.Repeat("a1B2", 9)
	stripeKey = "sk_live_" + strings.Repeat("x9Y8", 6)
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

func main() {
	var logFile *os.File
	if len(os.Args) > 1 {
		f, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakemcp: open log:", err)
			os.Exit(1)
		}
		defer f.Close()
		logFile = f
	}

	out := bufio.NewWriter(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if logFile != nil {
			_, _ = logFile.Write(append(append([]byte(nil), line...), '\n'))
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue // notification
		}
		resp := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		result, rpcErr := handle(req)
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		data, _ := json.Marshal(resp)
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
}

func handle(req request) (interface{}, map[string]interface{}) {
	switch req.Method {
	case "initialize":
		return map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]string{"name": "fakemcp", "version": "0.0.1"},
		}, nil
	case "tools/list":
		return map[string]interface{}{"tools": []map[string]interface{}{
			{
				"name":        "echo",
				"description": "Echoes its arguments. Default key " + awsKey,
				"inputSchema": map[string]interface{}{"type": "object"},
			},
			{
				"name":        "fail",
				"description": "Always fails.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"token": map[string]string{"type": "string", "description": "e.g. " + ghToken},
					},
				},
			},
			{
				"name":        "json_doc",
				"description": "Returns a JSON document (with a nested secret, a large integer and HTML characters) serialized in content[0].text, for lossless-redaction tests (#306).",
				"inputSchema": map[string]interface{}{"type": "object"},
			},
			{
				"name":        "missing_owner",
				"description": "Always fails with a -32602 Invalid params error, for error-fidelity tests (#296).",
				"inputSchema": map[string]interface{}{"type": "object"},
			},
			{
				"name":        "env",
				"description": "Returns this process's environment (os.Environ()), one KEY=VALUE per line, for least-privilege child environment tests (#311).",
				"inputSchema": map[string]interface{}{"type": "object"},
			},
		}}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.Name == "fail" {
			return nil, map[string]interface{}{
				"code":    -32000,
				"message": "upstream auth failed for " + awsKey,
				"data":    map[string]string{"token": ghToken},
			}
		}
		if p.Name == "missing_owner" {
			// #296 acceptance criterion: this exact code/message must reach
			// the client unchanged through both `server run --stdio` and
			// `serve`.
			return nil, map[string]interface{}{
				"code":    -32602,
				"message": "missing owner",
			}
		}
		if p.Name == "json_doc" {
			return map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": JSONDocText()}},
			}, nil
		}
		if p.Name == "env" {
			return map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": strings.Join(os.Environ(), "\n")}},
			}, nil
		}
		text := fmt.Sprintf("echo %s | leaked %s %s %s", p.Arguments, awsKey, ghToken, stripeKey)
		return map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": text}},
		}, nil
	default:
		return nil, map[string]interface{}{"code": -32601, "message": "method not found: " + req.Method}
	}
}

// JSONDocText is the text the json_doc tool returns: a JSON document whose
// layout (spacing, key order, number literals, unescaped <>&) must reach the
// client byte for byte except for the two secrets. Keep it in sync with
// tests/e2e/stdio_firewall_test.go.
func JSONDocText() string {
	return `{"rows": [{"id": 12345678901234567, "html": "<b>Ann & Bob</b>", "price": 1.50,` +
		` "config": {"db": {"password": "hunter2-nested", "host": "db.internal"}},` +
		` "note": "deploy key ` + ghToken + ` rotated"}], "total": 1e3}`
}
