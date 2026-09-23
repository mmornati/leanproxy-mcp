package toolsearch

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"getPullRequestFiles", []string{"pull", "request", "fil"}},
		{"slack_post_message", []string{"slack", "post", "message"}},
		{"messages", []string{"messag"}},
		{"List the open issues in my repo!", []string{"open", "issu", "repo"}},
		{"pullNumber workflow-runs", []string{"pull", "number", "workflow", "runs"}},
		{"stories categories", []string{"story", "category"}},
		{"running scheduled", []string{"runn", "schedul"}},
		{"VO2max SpO2 data", []string{"vo2max", "sp", "o2", "data"}},
		{"", nil},
	}
	for _, tt := range tests {
		if got := Tokenize(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Tokenize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStemShortWordsUnchanged(t *testing.T) {
	for _, w := range []string{"runs", "uses", "bed", "ring"} {
		if got := Stem(w); got != w {
			t.Errorf("Stem(%q) = %q, want unchanged", w, got)
		}
	}
}

func schema(props ...string) json.RawMessage {
	m := map[string]interface{}{}
	for _, p := range props {
		m[p] = map[string]string{"type": "string", "description": "The " + p + "."}
	}
	b, _ := json.Marshal(map[string]interface{}{"type": "object", "properties": m})
	return b
}

func smallIndex(opts Options) *Index {
	ix := New(opts)
	ix.SetServerTools("github", []Tool{
		{Name: "create_issue", Description: "Create a new issue in a repository.", InputSchema: schema("owner", "repo", "title")},
		{Name: "list_pull_requests", Description: "List pull requests in a repository.", InputSchema: schema("owner", "repo")},
		{Name: "run_workflow", Description: "Trigger a GitHub Actions workflow.", InputSchema: schema("workflow_id")},
	})
	ix.SetServerTools("jira", []Tool{
		{Name: "jira_create_issue", Description: "Create a Jira issue in a project.", InputSchema: schema("project_key", "summary")},
	})
	return ix
}

func names(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Tool.Server + "/" + h.Tool.Name
	}
	return out
}

func TestSearchRanksAndFilters(t *testing.T) {
	ix := smallIndex(Options{})
	ctx := context.Background()

	hits := ix.Search(ctx, Query{Text: "create a jira issue"})
	if len(hits) == 0 || names(hits)[0] != "jira/jira_create_issue" {
		t.Fatalf("top hit = %v, want jira/jira_create_issue first", names(hits))
	}
	hits = ix.Search(ctx, Query{Text: "create issue", Server: "github"})
	for _, h := range hits {
		if h.Tool.Server != "github" {
			t.Fatalf("server filter leaked %v", names(hits))
		}
	}
	if len(hits) == 0 || hits[0].Tool.Name != "create_issue" {
		t.Fatalf("filtered top hit = %v", names(hits))
	}
	if hits := ix.Search(ctx, Query{Text: "zzz nothing"}); len(hits) != 0 {
		t.Fatalf("no-match query returned %v", names(hits))
	}
	if hits := ix.Search(ctx, Query{Text: "the of and"}); len(hits) != 0 {
		t.Fatalf("stopword-only query returned %v", names(hits))
	}
	if got := ix.Search(ctx, Query{Text: "repository", K: 1}); len(got) != 1 {
		t.Fatalf("K=1 returned %d hits", len(got))
	}
}

func TestSearchSynonyms(t *testing.T) {
	ctx := context.Background()
	ix := smallIndex(Options{})
	hits := ix.Search(ctx, Query{Text: "open prs"})
	if len(hits) == 0 || hits[0].Tool.Name != "list_pull_requests" {
		t.Fatalf("pr synonym: %v", names(hits))
	}
	hits = ix.Search(ctx, Query{Text: "start ci"})
	if len(hits) == 0 || hits[0].Tool.Name != "run_workflow" {
		t.Fatalf("ci synonym: %v", names(hits))
	}

	none := New(Options{Synonyms: map[string]string{}})
	none.SetServerTools("github", []Tool{{Name: "list_pull_requests", Description: "List pull requests."}})
	if hits := none.Search(ctx, Query{Text: "prs"}); len(hits) != 0 {
		t.Fatalf("synonyms disabled but got %v", names(hits))
	}

	custom := New(Options{Synonyms: map[string]string{"k8s": "kubernetes cluster"}})
	custom.SetServerTools("ops", []Tool{{Name: "scale", Description: "Scale a Kubernetes deployment."}})
	if hits := custom.Search(ctx, Query{Text: "k8s"}); len(hits) != 1 {
		t.Fatalf("custom synonym: %v", names(hits))
	}
}

func TestSearchDeterministicTieBreak(t *testing.T) {
	ix := New(Options{})
	// Same length, same term frequencies: only the names differ.
	same := []Tool{{Name: "ping_y", Description: "Ping."}, {Name: "ping_x", Description: "Ping."}}
	ix.SetServerTools("zeta", same)
	ix.SetServerTools("alpha", same)
	want := []string{"alpha/ping_x", "alpha/ping_y", "zeta/ping_x", "zeta/ping_y"}
	for i := 0; i < 20; i++ {
		if got := names(ix.Search(context.Background(), Query{Text: "ping", K: 10})); !reflect.DeepEqual(got, want) {
			t.Fatalf("tie order = %v, want %v", got, want)
		}
	}
}

func TestSearchKLimits(t *testing.T) {
	ix := New(Options{})
	tools := make([]Tool, 30)
	for i := range tools {
		tools[i] = Tool{Name: "tool" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Description: "Common widget."}
	}
	ix.SetServerTools("s", tools)
	ctx := context.Background()
	if n := len(ix.Search(ctx, Query{Text: "widget"})); n != DefaultK {
		t.Errorf("default K: %d hits, want %d", n, DefaultK)
	}
	if n := len(ix.Search(ctx, Query{Text: "widget", K: 100})); n != MaxK {
		t.Errorf("K=100: %d hits, want capped at %d", n, MaxK)
	}
}

func TestSetServerToolsIncremental(t *testing.T) {
	ix := smallIndex(Options{})
	ctx := context.Background()
	if ix.Len() != 4 {
		t.Fatalf("Len = %d", ix.Len())
	}
	ix.SetServerTools("github", []Tool{{Name: "get_me", Description: "The authenticated user."}})
	if ix.Len() != 2 {
		t.Fatalf("after replace Len = %d, want 2", ix.Len())
	}
	if hits := ix.Search(ctx, Query{Text: "pull request"}); len(hits) != 0 {
		t.Fatalf("stale tools still indexed: %v", names(hits))
	}
	if hits := ix.Search(ctx, Query{Text: "authenticated user"}); len(hits) != 1 {
		t.Fatalf("new tool not indexed: %v", names(hits))
	}
	ix.RemoveServer("github")
	if !reflect.DeepEqual(ix.Servers(), []string{"jira"}) {
		t.Fatalf("Servers = %v", ix.Servers())
	}
}

func TestSearchUsesAllFields(t *testing.T) {
	ix := New(Options{})
	ix.SetServerTools("srv", []Tool{
		{Name: "a", Description: "x", InputSchema: json.RawMessage(`{"properties":{"flux":{"type":"string"},"b":true}}`)},
		{Name: "b", Description: "x", InputSchema: json.RawMessage(`{"properties":{"q":{"description":"capacitor value"}}}`)},
		{Name: "c", Description: "x", Title: "Warp drive"},
		{Name: "d", Description: "x", ReadOnlyHint: true},
	})
	ctx := context.Background()
	for query, want := range map[string]string{"flux": "a", "capacitor": "b", "warp": "c", "readonly only": "d", "srv": "a"} {
		hits := ix.Search(ctx, Query{Text: query})
		if len(hits) == 0 || hits[0].Tool.Name != want {
			t.Errorf("%q: %v, want %s first", query, names(hits), want)
		}
	}
}

func TestSearchConcurrentWithUpdates(t *testing.T) {
	ix := smallIndex(Options{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			ix.SetServerTools("jira", []Tool{{Name: "jira_create_issue", Description: "Create a Jira issue."}})
		}
	}()
	for i := 0; i < 200; i++ {
		ix.Search(context.Background(), Query{Text: "create issue"})
	}
	<-done
}

func TestConfig(t *testing.T) {
	var nilCfg *Config
	if err := nilCfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if nilCfg.HybridEnabled() {
		t.Fatal("hybrid on by default")
	}
	if got := nilCfg.Options().Synonyms; !reflect.DeepEqual(got, DefaultSynonyms()) {
		t.Fatalf("default synonyms = %v", got)
	}
	cfg := &Config{Synonyms: map[string]string{"k8s": "kubernetes", "pr": "merge request"}, DisableDefaultSynonyms: true}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Options().Synonyms; !reflect.DeepEqual(got, map[string]string{"k8s": "kubernetes", "pr": "merge request"}) {
		t.Fatalf("synonyms = %v", got)
	}
	for _, bad := range []map[string]string{{"two words": "x"}, {"the": "x"}, {"ok": "the of"}} {
		if err := (&Config{Synonyms: bad}).Validate(); err == nil {
			t.Errorf("Validate(%v) = nil, want error", bad)
		}
	}
	if err := (&Config{Hybrid: &HybridConfig{Enabled: true}}).Validate(); err == nil {
		t.Error("hybrid without embedder provider accepted")
	}
	if err := (&Config{Hybrid: &HybridConfig{Enabled: false}}).Validate(); err != nil {
		t.Errorf("disabled hybrid block rejected: %v", err)
	}
}
