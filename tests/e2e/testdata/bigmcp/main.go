// Command bigmcp is a stdio MCP server whose tools return very large
// results, for the response governor e2e tests (issue #319):
//
//   - big_text: ~200 KB of numbered lines, with a fake credential on line
//     1700 (the proxy must redact it before anything is spilled);
//   - big_json: a JSON array of 1000 issue objects, as a text item;
//   - big_error: a ~200 KB error result (isError: true);
//   - image: a ~300 KB image content item and a short caption;
//   - small: a few words;
//   - wide_issues / wide_huge: 30 / 600 GitHub-like issues with the usual
//     API noise (URLs, node ids, avatars, reactions) as a JSON text item,
//     for field projection (issue #320); issue 3's body holds the fake
//     credential;
//   - wide_structured / wide_strict: 20 of those issues as text and
//     structuredContent; wide_strict declares an outputSchema;
//   - echo_args: the tools/call params it received, as JSON text (the
//     e2e tests check that invoke_tool's fields never reach it).
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

// wideIssues renders n GitHub-like issues. ids are above 2^53 so a lossy
// float64 round-trip would show.
func wideIssues(n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		num := 4200 + i
		body := fmt.Sprintf("Steps to reproduce issue %d: open settings, press save.", num)
		if i == 3 {
			body = "token=" + ghToken
		}
		login := []string{"mona", "hubot", "octocat"}[i%3]
		fmt.Fprintf(&b, `{"url":"https://api.github.com/repos/octo/widget/issues/%[1]d","repository_url":"https://api.github.com/repos/octo/widget",`+
			`"labels_url":"https://api.github.com/repos/octo/widget/issues/%[1]d/labels{/name}","comments_url":"https://api.github.com/repos/octo/widget/issues/%[1]d/comments",`+
			`"events_url":"https://api.github.com/repos/octo/widget/issues/%[1]d/events","html_url":"https://github.com/octo/widget/issues/%[1]d",`+
			`"id":90071992547409%02[2]d,"node_id":"I_kwDOHx%08[2]d","number":%[1]d,"title":"Crash <%[1]d> & retry","state":"open",`+
			`"user":{"login":"%[3]s","id":%[4]d,"node_id":"MDQ6VXNlcj%[4]d","avatar_url":"https://avatars.githubusercontent.com/u/%[4]d?v=4","gravatar_id":"",`+
			`"url":"https://api.github.com/users/%[3]s","html_url":"https://github.com/%[3]s","followers_url":"https://api.github.com/users/%[3]s/followers",`+
			`"repos_url":"https://api.github.com/users/%[3]s/repos","type":"User","site_admin":false},`+
			`"labels":[{"id":51000,"node_id":"LA_kwDOHx01","url":"https://api.github.com/repos/octo/widget/labels/bug","name":"bug","color":"d73a4a","default":true}],`+
			`"assignee":null,"comments":%[5]d,"created_at":"2026-08-01T10:00:00Z","updated_at":"2026-09-20T12:34:56Z",`+
			`"reactions":{"url":"https://api.github.com/repos/octo/widget/issues/%[1]d/reactions","total_count":0,"+1":0,"-1":0},`+
			`"timeline_url":"https://api.github.com/repos/octo/widget/issues/%[1]d/timeline","body":%[6]q}`,
			num, i, login, 1000+i%3, i%7, body)
	}
	b.WriteByte(']')
	return b.String()
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
		for _, name := range []string{"big_text", "big_json", "big_error", "image", "small", "wide_issues", "wide_huge", "wide_structured", "wide_strict", "echo_args"} {
			tool := map[string]interface{}{"name": name, "description": "Returns a " + name + " result.", "inputSchema": map[string]interface{}{"type": "object"}}
			if name == "wide_strict" {
				tool["outputSchema"] = map[string]interface{}{"type": "object", "required": []string{"issues"}, "properties": map[string]interface{}{"issues": map[string]interface{}{"type": "array"}}}
			}
			tools = append(tools, tool)
		}
		return map[string]interface{}{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &p)
		switch p.Name {
		case "wide_issues":
			return text(wideIssues(30)), nil
		case "wide_huge":
			return text(wideIssues(600)), nil
		case "wide_structured", "wide_strict":
			doc := `{"issues":` + wideIssues(20) + `}`
			return map[string]interface{}{
				"content":           []map[string]string{{"type": "text", "text": doc}},
				"structuredContent": json.RawMessage(doc),
			}, nil
		case "echo_args":
			return text(string(req.Params)), nil
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
