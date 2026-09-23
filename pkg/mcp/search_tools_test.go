package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/toolsearch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func searchTestHandler(t *testing.T) (*Handler, *mockPool) {
	t.Helper()
	mp := newMockPool()
	mp.SetServerState("github", pool.StateIdle)
	mp.SetServerState("slack", pool.StateIdle)
	mp.SetTools("github", []Tool{
		{Name: "create_issue", Description: "Create a new issue in a GitHub repository.", InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"}},"required":["title","repo"]}`)},
		{Name: "list_pull_requests", Description: "List pull requests in a repository.", InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string"}}}`)},
		{Name: "get_job_logs", Description: "Download logs for a GitHub Actions workflow job.", InputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"number"}}}`)},
	})
	mp.SetTools("slack", []Tool{
		{Name: "slack_post_message", Description: "Post a new message to a Slack channel.", InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":{"type":"string"},"text":{"type":"string"}},"required":["channel_id","text"]}`)},
	})
	h := NewHandler(mp, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	return h, mp
}

func callSearch(t *testing.T, h *Handler, args string) (*Response, string) {
	t.Helper()
	resp, err := h.HandleRequest(context.Background(), &Request{JSONRPC: "2.0", Method: MethodToolsCall, ID: 1,
		Params: json.RawMessage(`{"name":"search_tools","arguments":` + args + `}`)})
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Error != nil {
		return resp, ""
	}
	var res ToolsCallResult
	require.NoError(t, json.Unmarshal(resp.Result, &res))
	require.Len(t, res.Content, 1)
	return resp, res.Content[0].Text
}

func TestSearchTools_RanksAcrossServers(t *testing.T) {
	h, _ := searchTestHandler(t)
	// Cold start: the cache is empty; search_tools fetches the unknown
	// servers' tool lists itself.
	_, text := callSearch(t, h, `{"query":"open a bug ticket about the crash"}`)
	lines := strings.Split(text, "\n")
	require.NotEmpty(t, lines)
	assert.Equal(t, "github_create_issue: Create a new issue in a GitHub repository. [repo: string, title: string] {body: string}", lines[0])

	_, text = callSearch(t, h, `{"query":"send a message to the team channel"}`)
	assert.True(t, strings.HasPrefix(text, "slack_slack_post_message: "), text)

	_, text = callSearch(t, h, `{"query":"CI logs of the failed job","k":1}`)
	assert.Equal(t, 1, len(strings.Split(text, "\n")), text)
	assert.True(t, strings.HasPrefix(text, "github_get_job_logs: "), text)
}

func TestSearchTools_ServerFilterAndErrors(t *testing.T) {
	h, _ := searchTestHandler(t)
	_, text := callSearch(t, h, `{"query":"message","server":"github"}`)
	assert.NotContains(t, text, "slack_", "server filter")

	_, text = callSearch(t, h, `{"query":"issue","server":"nope"}`)
	assert.Contains(t, text, "Server 'nope' not found. Available servers: github, slack.")

	resp, _ := callSearch(t, h, `{"query":"  "}`)
	require.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeInvalidParams, resp.Error.Code)

	resp, _ = callSearch(t, h, `{"query":1}`)
	require.NotNil(t, resp.Error)
	assert.Equal(t, ErrCodeInvalidParams, resp.Error.Code)

	_, text = callSearch(t, h, `{"query":"quantum teleportation"}`)
	assert.Contains(t, text, "No tools match")

	_, text = callSearch(t, h, `{"query":"repository","k":0}`)
	assert.Len(t, strings.Split(text, "\n"), 2, "k<1 falls back to the default, the corpus has 2 matches")
}

func TestSearchTools_TruncatesDescriptions(t *testing.T) {
	h, mp := searchTestHandler(t)
	mp.SetTools("slack", []Tool{{Name: "long", Description: "widget " + strings.Repeat("x", 400), InputSchema: json.RawMessage(`{}`)}})
	_, text := callSearch(t, h, `{"query":"widget"}`)
	assert.Equal(t, "slack_long: "+("widget " + strings.Repeat("x", 400))[:197]+"...", text)
}

func TestSearchTools_UnknownServersAreReported(t *testing.T) {
	h, mp := searchTestHandler(t)
	mp.SetServerState("down", pool.StateIdle)
	mp.sendRequestFunc = func(_ context.Context, name, _ string, _ json.RawMessage, _ time.Duration) (*pool.Response, error) {
		if name == "down" {
			return nil, fmt.Errorf("connection refused")
		}
		b, _ := json.Marshal(map[string]interface{}{"tools": mp.tools[name]})
		return &pool.Response{Result: b}, nil
	}
	_, text := callSearch(t, h, `{"query":"create issue"}`)
	assert.Contains(t, text, "github_create_issue")
	assert.Contains(t, text, "(tools of down not known yet")
}

func TestSearchTools_IndexFollowsToolCache(t *testing.T) {
	h, _ := searchTestHandler(t)
	h.setServerTools("github", []Tool{{Name: "get_me", Description: "The authenticated user."}})
	h.setServerTools("slack", []Tool{})
	assert.Equal(t, 1, h.SearchIndex().Len())
	_, text := callSearch(t, h, `{"query":"who am I, authenticated user"}`)
	assert.Equal(t, "github_get_me: The authenticated user.", text)

	// ConfigureToolSearch rebuilds from the cache with the new options.
	ix := h.ConfigureToolSearch(toolsearch.Options{Synonyms: map[string]string{"whoami": "authenticated user"}})
	assert.Equal(t, 1, ix.Len())
	_, text = callSearch(t, h, `{"query":"whoami"}`)
	assert.Equal(t, "github_get_me: The authenticated user.", text)
}

func TestSearchTools_OutputIsRedacted(t *testing.T) {
	h, mp := searchTestHandler(t)
	mp.SetTools("slack", []Tool{{Name: "leaky", Description: "widget example key " + fakeAWSKey, InputSchema: json.RawMessage(`{}`)}})
	h.Use(NewFirewall(nil, nil).Middlewares()...)
	resp, text := callSearch(t, h, `{"query":"widget"}`)
	assert.Contains(t, text, "slack_leaky")
	assertNoSecrets(t, "search_tools response", mustJSON(t, resp))
}

func TestParseInputSchemaDeterministicOrder(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"z":{"type":"string"},"a":{"type":"string"},"m":{"type":"number"},"b":{"type":"string"}},"required":["z","b"]}`)
	for i := 0; i < 20; i++ {
		assert.Equal(t, "s_t: d [b: string, z: string] {a: string, m: number}", formatTool(Tool{Name: "t", Description: "d", InputSchema: schema}, "s", 200))
	}
}
