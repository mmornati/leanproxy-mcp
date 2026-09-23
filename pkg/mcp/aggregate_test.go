package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceURI_RoundTrip(t *testing.T) {
	cases := []struct{ server, uri string }{
		{"github", "repo://octo/demo/README.md"},
		{"fs", "file:///home/me/notes.txt"},
		{"fs", "file:///{path}"},
		{"odd/name", "x://y?q=1#frag"},
		{"srv", "leanproxy://nested/uri"},
		{"srv", ""},
	}
	for _, c := range cases {
		u := ResourceURI(c.server, c.uri)
		assert.True(t, strings.HasPrefix(u, "leanproxy://"), u)
		server, uri, ok := ParseResourceURI(u)
		require.True(t, ok, u)
		assert.Equal(t, c.server, server, u)
		assert.Equal(t, c.uri, uri, u)
	}
	for _, bad := range []string{"file:///x", "leanproxy://", "leanproxy:///x", "leanproxy://%zz/x", "leanproxy:srv/x"} {
		_, _, ok := ParseResourceURI(bad)
		assert.False(t, ok, bad)
	}
	// A client expanding a namespaced template keeps the prefix.
	expanded := strings.Replace(ResourceURI("fs", "file:///{path}"), "{path}", "etc/hosts", 1)
	server, uri, ok := ParseResourceURI(expanded)
	require.True(t, ok)
	assert.Equal(t, "fs", server)
	assert.Equal(t, "file:///etc/hosts", uri)
}

// twoUpstreams returns a pool with two servers that both expose resources,
// templates and prompts ("alpha" paginates one item per page).
func twoUpstreams() *fakeUpstreamPool {
	return newFakeUpstreamPool(map[string]*fakeUpstream{
		"alpha": {
			caps:     `{"tools":{},"resources":{"subscribe":true},"prompts":{}}`,
			pageSize: 1,
			resources: []json.RawMessage{
				json.RawMessage(`{"uri":"file:///a/one.txt","name":"one","title":"One","mimeType":"text/plain","size":12345678901234567}`),
				json.RawMessage(`{"uri":"file:///a/two.txt","name":"two"}`),
			},
			templates: []json.RawMessage{json.RawMessage(`{"uriTemplate":"file:///a/{path}","name":"files"}`)},
			prompts:   []json.RawMessage{json.RawMessage(`{"name":"review","description":"Review code","arguments":[{"name":"lang","required":true}]}`)},
			results: map[string]json.RawMessage{
				MethodResourcesRead:      json.RawMessage(`{"contents":[{"uri":"file:///a/one.txt","mimeType":"text/plain","text":"alpha one"}]}`),
				MethodPromptsGet:         json.RawMessage(`{"description":"Review code","messages":[{"role":"user","content":{"type":"text","text":"review this"}}]}`),
				MethodResourcesSubscribe: json.RawMessage(`{}`),
			},
		},
		"beta": {
			caps: `{"resources":{},"prompts":{"listChanged":true}}`,
			resources: []json.RawMessage{
				json.RawMessage(`{"uri":"db://beta/table","name":"table"}`),
			},
			templates: []json.RawMessage{json.RawMessage(`{"uriTemplate":"db://beta/{table}","name":"tables"}`)},
			prompts:   []json.RawMessage{json.RawMessage(`{"name":"review","description":"Beta review"}`)},
			results: map[string]json.RawMessage{
				MethodResourcesRead: json.RawMessage(`{"contents":[{"uri":"db://beta/table","blob":"AAEC","mimeType":"application/octet-stream"}]}`),
				MethodPromptsGet:    json.RawMessage(`{"messages":[]}`),
			},
		},
		"toolsonly": {caps: `{"tools":{}}`},
	})
}

func decodeList(t *testing.T, resp *Response, key string) []map[string]json.RawMessage {
	t.Helper()
	require.Nil(t, resp.Error, "%+v", resp.Error)
	var body map[string][]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(resp.Result, &body))
	return body[key]
}

func str(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	require.NoError(t, json.Unmarshal(raw, &s))
	return s
}

func TestResourcesAndPrompts_MergedAndRoundTrip(t *testing.T) {
	fp := twoUpstreams()
	h := NewHandler(fp, quietLogger())

	resources := decodeList(t, call(t, h, nil, MethodResourcesList, nil), "resources")
	require.Len(t, resources, 3)
	uris := []string{str(t, resources[0]["uri"]), str(t, resources[1]["uri"]), str(t, resources[2]["uri"])}
	assert.Equal(t, []string{"leanproxy://alpha/file:///a/one.txt", "leanproxy://alpha/file:///a/two.txt", "leanproxy://beta/db://beta/table"}, uris)
	assert.Equal(t, `12345678901234567`, string(resources[0]["size"]), "other members are kept verbatim")
	assert.Equal(t, "One", str(t, resources[0]["title"]))
	assert.Len(t, fp.requestsFor("alpha", MethodResourcesList), 2, "alpha's two pages were followed")
	assert.Empty(t, fp.requestsFor("toolsonly", MethodResourcesList), "servers without the capability are not asked")

	templates := decodeList(t, call(t, h, nil, MethodResourcesTemplatesList, nil), "resourceTemplates")
	require.Len(t, templates, 2)
	assert.Equal(t, "leanproxy://alpha/file:///a/{path}", str(t, templates[0]["uriTemplate"]))
	assert.Equal(t, "leanproxy://beta/db://beta/{table}", str(t, templates[1]["uriTemplate"]))

	prompts := decodeList(t, call(t, h, nil, MethodPromptsList, nil), "prompts")
	require.Len(t, prompts, 2)
	assert.Equal(t, "alpha.review", str(t, prompts[0]["name"]))
	assert.Equal(t, "beta.review", str(t, prompts[1]["name"]))
	assert.JSONEq(t, `[{"name":"lang","required":true}]`, string(prompts[0]["arguments"]))

	// resources/read round-trips: the upstream gets its own URI back and
	// the contents come back namespaced.
	read := call(t, h, nil, MethodResourcesRead, map[string]interface{}{"uri": uris[0], "_meta": map[string]string{"k": "v"}})
	require.Nil(t, read.Error)
	assert.JSONEq(t, `{"contents":[{"uri":"leanproxy://alpha/file:///a/one.txt","mimeType":"text/plain","text":"alpha one"}]}`, string(read.Result))
	sent := fp.requestsFor("alpha", MethodResourcesRead)
	require.Len(t, sent, 1)
	assert.JSONEq(t, `{"uri":"file:///a/one.txt","_meta":{"k":"v"}}`, string(sent[0].params))

	read = call(t, h, nil, MethodResourcesRead, map[string]string{"uri": uris[2]})
	require.Nil(t, read.Error)
	assert.Contains(t, string(read.Result), `"uri":"leanproxy://beta/db://beta/table"`)
	assert.Contains(t, string(read.Result), `"blob":"AAEC"`)

	// An upstream's own URI (e.g. from a resource_link) is routed through
	// the last listing.
	read = call(t, h, nil, MethodResourcesRead, map[string]string{"uri": "db://beta/table"})
	require.Nil(t, read.Error)
	assert.Len(t, fp.requestsFor("beta", MethodResourcesRead), 2)

	// A template expansion reaches the owner with the expanded URI.
	read = call(t, h, nil, MethodResourcesRead, map[string]string{"uri": "leanproxy://alpha/file:///a/deep/x.txt"})
	require.Nil(t, read.Error)
	sent = fp.requestsFor("alpha", MethodResourcesRead)
	assert.JSONEq(t, `{"uri":"file:///a/deep/x.txt"}`, string(sent[len(sent)-1].params))

	for _, unknown := range []string{"leanproxy://nosuch/file:///x", "file:///unlisted", "leanproxy://"} {
		resp := call(t, h, nil, MethodResourcesRead, map[string]string{"uri": unknown})
		require.NotNil(t, resp.Error, unknown)
		assert.Equal(t, ErrCodeResourceNotFound, resp.Error.Code, unknown)
	}
	resp := call(t, h, nil, MethodResourcesRead, map[string]string{})
	require.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeInvalidParams, resp.Error.Code)

	// prompts/get routes by prefix, with the upstream's own name and the
	// arguments untouched.
	got := call(t, h, nil, MethodPromptsGet, map[string]interface{}{"name": "alpha.review", "arguments": map[string]string{"lang": "go"}})
	require.Nil(t, got.Error)
	assert.Contains(t, string(got.Result), "review this")
	sent = fp.requestsFor("alpha", MethodPromptsGet)
	require.Len(t, sent, 1)
	assert.JSONEq(t, `{"name":"review","arguments":{"lang":"go"}}`, string(sent[0].params))
	got = call(t, h, nil, MethodPromptsGet, map[string]string{"name": "beta.review"})
	require.Nil(t, got.Error)
	assert.Len(t, fp.requestsFor("beta", MethodPromptsGet), 1)
	for _, bad := range []string{"review", "nosuch.review", "alpha."} {
		resp := call(t, h, nil, MethodPromptsGet, map[string]string{"name": bad})
		require.NotNil(t, resp.Error, bad)
		assert.Equal(t, ErrCodeInvalidParams, resp.Error.Code, bad)
	}

	// subscribe/unsubscribe go to the owner; an upstream error is relayed.
	sub := call(t, h, nil, MethodResourcesSubscribe, map[string]string{"uri": uris[0]})
	require.Nil(t, sub.Error)
	sentSub := fp.requestsFor("alpha", MethodResourcesSubscribe)
	require.Len(t, sentSub, 1)
	assert.JSONEq(t, `{"uri":"file:///a/one.txt"}`, string(sentSub[0].params))
	unsub := call(t, h, nil, MethodResourcesUnsubscribe, map[string]string{"uri": uris[2]})
	require.NotNil(t, unsub.Error)
	assert.Equal(t, ErrCodeMethodNotFound, unsub.Error.Code)
}

func TestPromptNames_LongestServerPrefixWins(t *testing.T) {
	fp := newFakeUpstreamPool(map[string]*fakeUpstream{
		"git":     {caps: `{"prompts":{}}`, results: map[string]json.RawMessage{MethodPromptsGet: json.RawMessage(`{"messages":[],"from":"git"}`)}},
		"git.hub": {caps: `{"prompts":{}}`, results: map[string]json.RawMessage{MethodPromptsGet: json.RawMessage(`{"messages":[],"from":"git.hub"}`)}},
	})
	h := NewHandler(fp, quietLogger())
	resp := call(t, h, nil, MethodPromptsGet, map[string]string{"name": "git.hub.pr"})
	require.Nil(t, resp.Error)
	assert.Contains(t, string(resp.Result), `"from":"git.hub"`)
	resp = call(t, h, nil, MethodPromptsGet, map[string]string{"name": "git.commit"})
	require.Nil(t, resp.Error)
	assert.Contains(t, string(resp.Result), `"from":"git"`)
}

func TestAggregation_FailingUpstreamAndPageCap(t *testing.T) {
	many := make([]json.RawMessage, 0, 3*maxListPages)
	for i := 0; i < 3*maxListPages; i++ {
		many = append(many, json.RawMessage(fmt.Sprintf(`{"uri":"x://%d","name":"n%d"}`, i, i)))
	}
	fp := newFakeUpstreamPool(map[string]*fakeUpstream{
		"broken":  {caps: `{"resources":{},"prompts":{}}`, fail: map[string]bool{MethodResourcesList: true, MethodPromptsList: true}},
		"endless": {caps: `{"resources":{}}`, pageSize: 1, resources: many},
		"loop":    {caps: `{"prompts":{}}`, endless: true, prompts: []json.RawMessage{json.RawMessage(`{"name":"p"}`)}},
	})
	h := NewHandler(fp, quietLogger())

	resources := decodeList(t, call(t, h, nil, MethodResourcesList, nil), "resources")
	assert.Len(t, resources, maxListPages, "pagination stops at the cap")
	assert.Len(t, fp.requestsFor("endless", MethodResourcesList), maxListPages)

	prompts := decodeList(t, call(t, h, nil, MethodPromptsList, nil), "prompts")
	require.Len(t, prompts, 1, "the failing server contributes nothing")
	assert.Equal(t, "loop.p", str(t, prompts[0]["name"]))
	assert.Len(t, fp.requestsFor("loop", MethodPromptsList), maxListPages, "a server always answering a nextCursor is capped too")
}

func TestAggregation_NoUpstreams(t *testing.T) {
	h := NewHandler(newFakeUpstreamPool(map[string]*fakeUpstream{}), quietLogger())
	for method, key := range map[string]string{MethodResourcesList: "resources", MethodResourcesTemplatesList: "resourceTemplates", MethodPromptsList: "prompts"} {
		resp := call(t, h, nil, method, nil)
		require.Nil(t, resp.Error)
		assert.JSONEq(t, `{"`+key+`":[]}`, string(resp.Result), method)
	}
}

// TestAggregation_ResponsesAreRedacted drives resources/read, resources/list
// and prompts/get through the firewall middlewares: resource contents can
// carry secrets like any tool result.
func TestAggregation_ResponsesAreRedacted(t *testing.T) {
	awsKey := "AKIA" + "IOSFODNN7EXAMPLE"
	ghToken := "ghp_" + strings.Repeat("a1B2", 9)
	fp := newFakeUpstreamPool(map[string]*fakeUpstream{
		"leaky": {
			caps:      `{"resources":{},"prompts":{}}`,
			resources: []json.RawMessage{json.RawMessage(`{"uri":"file:///env","name":"env","description":"key ` + awsKey + `"}`)},
			prompts:   []json.RawMessage{json.RawMessage(`{"name":"p","description":"token ` + ghToken + `"}`)},
			results: map[string]json.RawMessage{
				MethodResourcesRead: json.RawMessage(`{"contents":[{"uri":"file:///env","text":"AWS_ACCESS_KEY_ID=` + awsKey + `"}]}`),
				MethodPromptsGet:    json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"use ` + ghToken + `"}}]}`),
			},
		},
	})
	h := NewHandler(fp, quietLogger())
	fw := NewFirewall(nil, nil) // built-in patterns
	h.Use(fw.Middlewares()...)

	for _, c := range []struct {
		method string
		params interface{}
	}{
		{MethodResourcesList, nil},
		{MethodPromptsList, nil},
		{MethodResourcesRead, map[string]string{"uri": "leanproxy://leaky/file:///env"}},
		{MethodPromptsGet, map[string]string{"name": "leaky.p"}},
	} {
		resp := call(t, h, nil, c.method, c.params)
		require.Nil(t, resp.Error, c.method)
		out := string(resp.Result)
		assert.NotContains(t, out, awsKey, c.method)
		assert.NotContains(t, out, ghToken, c.method)
	}
}
