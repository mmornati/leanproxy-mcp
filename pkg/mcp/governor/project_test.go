package governor

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProjPath(t *testing.T) {
	tests := []struct {
		path    string
		want    []projStep
		wantErr string
	}{
		{path: "title", want: []projStep{{kind: projName, glob: "title"}}},
		{path: "$.title", want: []projStep{{kind: projName, glob: "title"}}},
		{path: "[].number", want: []projStep{{kind: projElem}, {kind: projName, glob: "number"}}},
		{path: "[].labels[].name", want: []projStep{{kind: projElem}, {kind: projName, glob: "labels"}, {kind: projElem}, {kind: projName, glob: "name"}}},
		{path: "**.node_id", want: []projStep{{kind: projDeep}, {kind: projName, glob: "node_id"}}},
		{path: "**.*_url", want: []projStep{{kind: projDeep}, {kind: projName, glob: "*_url"}}},
		{path: "a.*.b", want: []projStep{{kind: projName, glob: "a"}, {kind: projName, glob: "*"}, {kind: projName, glob: "b"}}},
		{path: "m[][]", want: []projStep{{kind: projName, glob: "m"}, {kind: projElem}, {kind: projElem}}},
		{path: "", wantErr: "empty"},
		{path: "$", wantErr: "empty"},
		{path: "a..b", wantErr: "empty segment"},
		{path: "a.", wantErr: "empty segment"},
		{path: "items[0].name", wantErr: "only []"},
		{path: "a**.b", wantErr: "whole segment"},
		{path: "a.**", wantErr: "cannot end"},
		{path: strings.Repeat("a", MaxProjectionPathLen+1), wantErr: "longer than"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := parseProjPath(tt.path)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		glob, key string
		want      bool
	}{
		{"title", "title", true},
		{"title", "titles", false},
		{"*", "anything", true},
		{"*", "", true},
		{"*_url", "avatar_url", true},
		{"*_url", "url", false},
		{"*_url", "_url", true},
		{"html_*", "html_url", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXcYYb", false},
		{"a*a", "a", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, globMatch(tt.glob, tt.key), "%q vs %q", tt.glob, tt.key)
	}
}

func TestCompileProjection(t *testing.T) {
	_, err := CompileProjection([]string{"a"}, []string{"b"})
	assert.ErrorContains(t, err, "mutually exclusive")
	_, err = CompileProjection(nil, nil)
	assert.ErrorContains(t, err, "required")
	_, err = CompileProjection(nil, []string{"a[]"})
	assert.ErrorContains(t, err, "must end with a key name")
	_, err = CompileProjection(make([]string, MaxProjectionPaths+1), nil)
	assert.ErrorContains(t, err, "at most")
	p, err := CompileProjection([]string{"a[]"}, nil)
	require.NoError(t, err)
	assert.Equal(t, ProjectKeep, p.Mode)
}

func TestProjectionApply(t *testing.T) {
	tests := []struct {
		name  string
		keep  []string
		drop  []string
		doc   string
		want  string // "" = unchanged
		wantN int
	}{
		{
			name: "drop top-level key",
			drop: []string{"url"},
			doc:  `{"id":1,"url":"u","title":"t"}`,
			want: `{"id":1,"title":"t"}`, wantN: 1,
		},
		{
			name: "drop ** at any depth, including the root",
			drop: []string{"**.node_id"},
			doc:  `{"node_id":"a","user":{"node_id":"b","login":"x"},"labels":[{"node_id":"c","name":"bug"}]}`,
			want: `{"user":{"login":"x"},"labels":[{"name":"bug"}]}`, wantN: 3,
		},
		{
			name: "drop * glob inside a key",
			drop: []string{"**.*_url"},
			doc:  `[{"html_url":"h","url":"u","user":{"avatar_url":"a","login":"m"}}]`,
			want: `[{"url":"u","user":{"login":"m"}}]`, wantN: 2,
		},
		{
			name: "drop through [] and a lone *",
			drop: []string{"[].*.id"},
			doc:  `[{"id":1,"user":{"id":2,"login":"m"},"repo":{"id":3}}]`,
			want: `[{"id":1,"user":{"login":"m"},"repo":{}}]`, wantN: 2,
		},
		{
			name: "drop name across an array (implicit [])",
			drop: []string{"labels.color"},
			doc:  `{"labels":[{"name":"a","color":"f"},{"name":"b","color":"0"}]}`,
			want: `{"labels":[{"name":"a"},{"name":"b"}]}`, wantN: 2,
		},
		{
			name: "drop nothing matched: unchanged",
			drop: []string{"**.nope"},
			doc:  `{"a":{"b":[1,2]}}`,
		},
		{
			name: "drop keeps order, escapes and number text",
			drop: []string{"x"},
			doc:  `{"z":9007199254740993,"x":1,"a":"<b>&\u00e9","m":1.50e10}`,
			want: `{"z":9007199254740993,"a":"<b>&\u00e9","m":1.50e10}`, wantN: 1,
		},
		{
			name: "drop compacts the output",
			drop: []string{"x"},
			doc:  "{\n  \"x\": 1,\n  \"y\": [ 1, 2 ]\n}",
			want: `{"y":[1,2]}`, wantN: 1,
		},
		{
			name: "drop an escaped key",
			drop: []string{"a/b"},
			doc:  `{"a\/b":1,"c":2}`,
			want: `{"c":2}`, wantN: 1,
		},
		{
			name:  "keep list fields including nested arrays",
			keep:  []string{"[].number", "[].title", "[].labels[].name", "[].assignee.login"},
			doc:   `[{"number":1,"title":"a","body":"long","labels":[{"name":"bug","color":"f"}],"assignee":{"login":"m","id":7}},{"number":2,"title":"b","labels":[],"assignee":null}]`,
			want:  `[{"number":1,"title":"a","labels":[{"name":"bug"}],"assignee":{"login":"m"}},{"number":2,"title":"b","labels":[],"assignee":null}]`,
			wantN: 3,
		},
		{
			name: "keep a whole subtree",
			keep: []string{"user"},
			doc:  `{"id":1,"user":{"login":"m","id":2},"x":[1]}`,
			want: `{"user":{"login":"m","id":2}}`, wantN: 2,
		},
		{
			name: "keep with ** keeps the skeleton only",
			keep: []string{"**.name"},
			doc:  `{"a":{"name":"x","b":1},"tags":["t1","t2"],"c":[{"name":"y","z":2},{"z":3}],"d":5}`,
			want: `{"a":{"name":"x"},"c":[{"name":"y"}]}`, wantN: 5,
		},
		{
			name: "keep a * member",
			keep: []string{"items.*.id"},
			doc:  `{"items":[{"a":{"id":1,"x":2},"b":{"id":3}}],"total":1}`,
			want: `{"items":[{"a":{"id":1},"b":{"id":3}}]}`, wantN: 2,
		},
		{
			name: "keep a missing field: empty skeleton on direct paths",
			keep: []string{"[].missing"},
			doc:  `[{"a":1},{"b":2}]`,
			want: `[{},{}]`, wantN: 2,
		},
		{
			name: "keep everything already: unchanged",
			keep: []string{"a", "b"},
			doc:  `{"a":1,"b":2}`,
		},
		{
			name: "keep scalars of an array selected by []",
			keep: []string{"ids[]"},
			doc:  `{"ids":[1,2,3],"x":0}`,
			want: `{"ids":[1,2,3]}`, wantN: 1,
		},
		{
			name: "keep mixed paths: [] then * then **",
			keep: []string{"[].*.**.login"},
			doc:  `[{"user":{"login":"a","id":1},"pr":{"author":{"login":"b","id":2}},"n":1}]`,
			want: `[{"user":{"login":"a"},"pr":{"author":{"login":"b"}}}]`, wantN: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := CompileProjection(tt.keep, tt.drop)
			require.NoError(t, err)
			out, stats, changed, err := p.Apply([]byte(tt.doc))
			require.NoError(t, err)
			if tt.want == "" {
				assert.False(t, changed, "got %s", out)
				assert.Nil(t, out)
				return
			}
			require.True(t, changed)
			assert.Equal(t, tt.want, string(out))
			assert.Equal(t, tt.wantN, stats.Removed)
			assert.True(t, json.Valid(out))
		})
	}
}

func TestProjectionApplyNotJSON(t *testing.T) {
	p, err := CompileProjection(nil, []string{"x"})
	require.NoError(t, err)
	for _, doc := range []string{"plain text", `"a string"`, "42", `{"x":`, ""} {
		out, _, changed, err := p.Apply([]byte(doc))
		assert.Error(t, err, doc)
		assert.False(t, changed)
		assert.Nil(t, out)
	}
}

func loadIssues(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/github_list_issues.json")
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, json.Compact(&buf, raw))
	return buf.Bytes()
}

// The issue's github.* drop pack on 30 realistic list_issues items: at
// least 50% fewer estimated tokens, valid JSON, numbers preserved.
func TestProjection_GitHubDropPackOnListIssues(t *testing.T) {
	doc := loadIssues(t)
	p, err := CompileProjection(nil, []string{"**.node_id", "**.*_url", "**.url", "**.reactions", "**.avatar_url", "**.gravatar_id"})
	require.NoError(t, err)
	out, stats, changed, err := p.Apply(doc)
	require.NoError(t, err)
	require.True(t, changed)
	before, after := Tokens(len(doc)), Tokens(len(out))
	saving := 100 * (1 - float64(after)/float64(before))
	t.Logf("github.* drop pack: %d → %d tokens (−%.1f%%), %d members removed", before, after, saving, stats.Removed)
	assert.GreaterOrEqual(t, saving, 50.0)

	var orig, proj []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc, &orig))
	require.NoError(t, json.Unmarshal(out, &proj))
	require.Len(t, proj, 30)
	for i := range orig {
		for _, k := range []string{"id", "number", "title", "state", "comments", "updated_at", "body"} {
			assert.Equal(t, string(orig[i][k]), string(proj[i][k]), "issue %d %s", i, k)
		}
		for _, k := range []string{"url", "html_url", "node_id", "reactions", "comments_url"} {
			assert.NotContains(t, proj[i], k)
		}
	}
	assert.NotContains(t, string(out), "api.github.com")
}

func TestProjection_DefaultPackOnListIssues(t *testing.T) {
	doc := loadIssues(t)
	p, err := CompileProjection(nil, DefaultDropPaths)
	require.NoError(t, err)
	out, _, changed, err := p.Apply(doc)
	require.NoError(t, err)
	require.True(t, changed)
	t.Logf("default pack: %d → %d tokens", Tokens(len(doc)), Tokens(len(out)))
	assert.Less(t, len(out), len(doc)*3/4)
	// Conservative: "url" itself is not in the default pack.
	assert.Contains(t, string(out), `"url":`)
	assert.NotContains(t, string(out), `"node_id"`)
}

func TestProjection_KeepOnListIssues(t *testing.T) {
	doc := loadIssues(t)
	p, err := CompileProjection([]string{"[].number", "[].title", "[].state", "[].labels[].name", "[].assignee.login", "[].updated_at"}, nil)
	require.NoError(t, err)
	out, _, changed, err := p.Apply(doc)
	require.NoError(t, err)
	require.True(t, changed)
	var issues []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &issues))
	require.Len(t, issues, 30)
	for _, is := range issues {
		for k := range is {
			assert.Contains(t, []string{"number", "title", "state", "labels", "assignee", "updated_at"}, k)
		}
		var labels []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(is["labels"], &labels))
		require.NotEmpty(t, labels)
		for _, l := range labels {
			assert.Len(t, l, 1)
			assert.Contains(t, l, "name")
		}
		if string(is["assignee"]) != "null" {
			var a map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(is["assignee"], &a))
			assert.Len(t, a, 1)
		}
	}
	t.Logf("keep: %d → %d tokens", Tokens(len(doc)), Tokens(len(out)))
	assert.Less(t, len(out), len(doc)/10)
}

func TestProjections_For(t *testing.T) {
	cfg := &Config{
		Projections: []ProjectionRule{
			{Match: "github.list_issues", Keep: []string{"[].number"}},
			{Match: "github.get_me"}, // exemption
			{Match: "github.*", Drop: []string{"**.url"}},
		},
		DefaultProjections: true,
	}
	require.NoError(t, cfg.Validate())
	ps := cfg.CompileProjections()
	assert.Equal(t, 3, ps.Len())
	assert.True(t, ps.Default())

	rp, ok := ps.For("github.list_issues")
	require.True(t, ok)
	assert.Equal(t, ProjectKeep, rp.Mode)
	assert.Equal(t, "github.list_issues", rp.Rule)

	_, ok = ps.For("github.get_me")
	assert.False(t, ok, "an empty rule exempts the tool, default pack included")

	rp, ok = ps.For("github.search_code")
	require.True(t, ok)
	assert.Equal(t, "github.*", rp.Rule)

	rp, ok = ps.For("jira.search")
	require.True(t, ok)
	assert.Equal(t, "default_projections", rp.Rule)

	cfg.DefaultProjections = false
	_, ok = cfg.CompileProjections().For("jira.search")
	assert.False(t, ok)

	var nilCfg *Config
	_, ok = nilCfg.CompileProjections().For("x.y")
	assert.False(t, ok)
}

func BenchmarkProjection_DropPack(b *testing.B) {
	raw, err := os.ReadFile("testdata/github_list_issues.json")
	require.NoError(b, err)
	var buf bytes.Buffer
	require.NoError(b, json.Compact(&buf, raw))
	doc := buf.Bytes()
	p, err := CompileProjection(nil, []string{"**.node_id", "**.*_url", "**.url", "**.reactions", "**.avatar_url", "**.gravatar_id"})
	require.NoError(b, err)
	b.SetBytes(int64(len(doc)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, _, err := p.ApplyValid(doc); err != nil {
			b.Fatal(err)
		}
	}
}
