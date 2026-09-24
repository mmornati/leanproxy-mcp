package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// LargeResultTools are the catalog tools that answer with a large result
// when catalogmcp runs with --large-results (the response governor
// measurements, issue #319): a file read and the list/search endpoints an
// agent session typically calls.
var LargeResultTools = []struct{ Server, Tool string }{
	{"github", "get_file_contents"},
	{"github", "list_issues"},
	{"github", "search_code"},
	{"jira", "jira_search"},
	{"postgres", "pg_query"},
}

// LargeResult returns the large text result of tool, deterministic, and
// whether the tool has one.
func LargeResult(tool string) (string, bool) {
	switch tool {
	case "get_file_contents":
		return largeFile(), true
	case "list_issues":
		return largeJSON(largeIssues(300)), true
	case "search_code":
		return largeJSON(largeCodeSearch(400)), true
	case "jira_search":
		return largeJSON(largeJiraSearch(250)), true
	case "pg_query":
		return largeJSON(largeRows(1500)), true
	}
	return "", false
}

func largeJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// largeFile is a ~200 KB Go source file.
func largeFile() string {
	var b strings.Builder
	b.WriteString("package server\n\nimport (\n\t\"encoding/json\"\n\t\"net/http\"\n)\n\n")
	for i := 1; b.Len() < 200*1024; i++ {
		fmt.Fprintf(&b, "// handler%04d serves /api/v1/resource%d: it decodes the request, validates it and writes the answer.\n", i, i)
		fmt.Fprintf(&b, "func handler%04d(w http.ResponseWriter, r *http.Request) {\n", i)
		fmt.Fprintf(&b, "\tvar req struct{ ID int `json:\"id\"`; Name string `json:\"name\"` }\n")
		fmt.Fprintf(&b, "\tif err := json.NewDecoder(r.Body).Decode(&req); err != nil {\n\t\thttp.Error(w, err.Error(), http.StatusBadRequest)\n\t\treturn\n\t}\n")
		fmt.Fprintf(&b, "\t_ = json.NewEncoder(w).Encode(map[string]interface{}{\"id\": req.ID, \"resource\": %d})\n}\n\n", i)
	}
	return b.String()
}

func largeIssues(n int) []map[string]interface{} {
	out := make([]map[string]interface{}, n)
	for i := range out {
		num := 1000 + i
		out[i] = map[string]interface{}{
			"url":        fmt.Sprintf("https://api.github.com/repos/octo/demo/issues/%d", num),
			"html_url":   fmt.Sprintf("https://github.com/octo/demo/issues/%d", num),
			"id":         900000000 + i,
			"node_id":    fmt.Sprintf("I_kwDOAbCdEf%08d", i),
			"number":     num,
			"title":      fmt.Sprintf("Crash when saving settings with a long name (#%d)", num),
			"state":      "open",
			"user":       map[string]interface{}{"login": fmt.Sprintf("dev%d", i%17), "id": 10000 + i%17, "avatar_url": fmt.Sprintf("https://avatars.githubusercontent.com/u/%d?v=4", 10000+i%17), "type": "User"},
			"labels":     []map[string]interface{}{{"name": "bug", "color": "d73a4a"}, {"name": "p2", "color": "fbca04"}},
			"comments":   i % 9,
			"created_at": "2026-08-01T10:00:00Z",
			"updated_at": "2026-09-20T12:34:56Z",
			"body":       "Steps to reproduce: open settings, type a name longer than 64 characters, press save. Expected: saved. Actual: the app crashes with a nil pointer in settings.Save.",
		}
	}
	return out
}

func largeCodeSearch(n int) map[string]interface{} {
	items := make([]map[string]interface{}, n)
	for i := range items {
		items[i] = map[string]interface{}{
			"name":       fmt.Sprintf("config_%d.go", i),
			"path":       fmt.Sprintf("internal/pkg%d/config_%d.go", i%23, i),
			"sha":        fmt.Sprintf("%040x", i*7919),
			"html_url":   fmt.Sprintf("https://github.com/octo/demo/blob/main/internal/pkg%d/config_%d.go", i%23, i),
			"repository": map[string]interface{}{"full_name": "octo/demo", "private": false},
			"text_matches": []map[string]interface{}{{
				"fragment": fmt.Sprintf("cfg, err := parseConfig(path%d)\nif err != nil {\n\treturn fmt.Errorf(\"load config: %%w\", err)\n}", i),
			}},
		}
	}
	return map[string]interface{}{"total_count": n, "incomplete_results": false, "items": items}
}

func largeJiraSearch(n int) map[string]interface{} {
	issues := make([]map[string]interface{}, n)
	for i := range issues {
		issues[i] = map[string]interface{}{
			"key":  fmt.Sprintf("PROJ-%d", 100+i),
			"self": fmt.Sprintf("https://jira.example.com/rest/api/2/issue/%d", 10100+i),
			"fields": map[string]interface{}{
				"summary":           fmt.Sprintf("Migrate service %d to the new config loader", i),
				"status":            map[string]interface{}{"name": "In Progress", "id": "3"},
				"assignee":          map[string]interface{}{"displayName": "Dev User", "accountId": fmt.Sprintf("5b10ac8d82e05b22cc7d%04d", i%50)},
				"priority":          map[string]interface{}{"name": "Medium"},
				"customfield_10020": []map[string]interface{}{{"name": "Sprint 42", "state": "active"}},
				"updated":           "2026-09-20T12:34:56.000+0000",
				"description":       "The old loader is deprecated. Move every call site to config.Load and delete the legacy flags.",
			},
		}
	}
	return map[string]interface{}{"startAt": 0, "maxResults": n, "total": n, "issues": issues}
}

func largeRows(n int) map[string]interface{} {
	rows := make([]map[string]interface{}, n)
	for i := range rows {
		rows[i] = map[string]interface{}{"id": i + 1, "email": fmt.Sprintf("user%d@example.com", i), "plan": []string{"free", "pro", "team"}[i%3], "created_at": "2026-01-02 03:04:05", "active": i%4 != 0}
	}
	return map[string]interface{}{"command": "SELECT", "rowCount": n, "rows": rows}
}
