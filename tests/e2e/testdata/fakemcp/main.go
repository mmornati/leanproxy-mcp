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
		text := fmt.Sprintf("echo %s | leaked %s %s %s", p.Arguments, awsKey, ghToken, stripeKey)
		return map[string]interface{}{
			"content": []map[string]string{{"type": "text", "text": text}},
		}, nil
	default:
		return nil, map[string]interface{}{"code": -32601, "message": "method not found: " + req.Method}
	}
}
