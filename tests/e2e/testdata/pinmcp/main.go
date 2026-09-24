// Command pinmcp is a stdio MCP server for the tool pinning end-to-end
// tests (#310). Its serverInfo name and tool list are read from the JSON
// state file named by its first argument on every initialize and
// tools/list, so a test can change a tool description between two runs of
// the proxy (a "rug pull") or ship a poisoned one. Every tools/call it
// receives is appended to the file named by its second argument, so a test
// can assert that a blocked tool never reached the upstream.
//
// State file: {"server": "name", "tools": [{"name": "...", "description": "...",
// "annotations": {...}}]} (annotations optional; the policy tests of #314
// use them). The exposure tests of #322 also use, per tool: "extra" (more
// members of the tools/list entry: title, outputSchema, icons, _meta),
// "result" (the text tools/call returns instead of "called <name>"; the
// special value "{{arguments}}" returns the arguments as received,
// "{{redaction-probe}}" whether they arrived redacted) and
// "notify" (after answering a call to it, the server sends
// notifications/tools/list_changed).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type state struct {
	Server string `json:"server"`
	Tools  []struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Annotations map[string]interface{} `json:"annotations,omitempty"`
		Extra       map[string]interface{} `json:"extra,omitempty"`
		Result      string                 `json:"result,omitempty"`
		Notify      bool                   `json:"notify,omitempty"`
	} `json:"tools"`
}

// Special tool results: echoArguments returns the call's arguments;
// redactionProbe says whether they arrived redacted (without echoing them,
// so the answer itself is never redacted on the way back).
const (
	echoArguments  = "{{arguments}}"
	redactionProbe = "{{redaction-probe}}"
)

type request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     interface{}     `json:"id"`
}

func load(path string) state {
	var st state
	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path from argv
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.Server == "" {
		st.Server = "pinmcp"
	}
	return st
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: pinmcp <state.json> <calls.log>")
		os.Exit(2)
	}
	statePath, callsPath := os.Args[1], os.Args[2]
	out := bufio.NewWriter(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var req request
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result interface{}
		notify := false
		switch req.Method {
		case "initialize":
			result = map[string]interface{}{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": true}},
				"serverInfo":      map[string]string{"name": load(statePath).Server, "version": "1.0.0"},
			}
		case "tools/list":
			st := load(statePath)
			tools := make([]map[string]interface{}, 0, len(st.Tools))
			for _, t := range st.Tools {
				tool := map[string]interface{}{
					"name":        t.Name,
					"description": t.Description,
					"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"text": map[string]string{"type": "string"}}},
				}
				if t.Annotations != nil {
					tool["annotations"] = t.Annotations
				}
				for k, v := range t.Extra {
					tool[k] = v
				}
				tools = append(tools, tool)
			}
			result = map[string]interface{}{"tools": tools}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if f, err := os.OpenFile(callsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil { // #nosec G304 -- test fixture path from argv
				_, _ = f.WriteString(p.Name + "\n")
				_ = f.Close()
			}
			text := "called " + p.Name
			for _, t := range load(statePath).Tools {
				if t.Name == p.Name && t.Result != "" {
					text = t.Result
					switch text {
					case echoArguments:
						text = string(p.Arguments)
					case redactionProbe:
						text = "upstream received: raw"
						if strings.Contains(string(p.Arguments), "[SECRET_REDACTED]") {
							text = "upstream received: redacted"
						}
					}
				}
				if t.Name == p.Name && t.Notify {
					notify = true
				}
			}
			result = map[string]interface{}{"content": []map[string]string{{"type": "text", "text": text}}}
		default:
			result = map[string]interface{}{}
		}
		data, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
		_, _ = out.Write(append(data, '\n'))
		if notify {
			_, _ = out.WriteString(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` + "\n")
		}
		_ = out.Flush()
	}
}
