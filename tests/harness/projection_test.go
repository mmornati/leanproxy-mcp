//go:build harness

package harness

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Field projection (issue #320) on the large-results session: the same
// calls through the governor with truncation only (#319), projection only
// (max_tokens: 0) and projection then truncation, plus one call with the
// model's fields argument. Tokens are those of the whole JSON-RPC line;
// "items" counts the elements of the result's main list the model sees
// within the budget (the omission object excluded).

// projectionRules are the projection rules measured: the issue's github.*
// drop pack, and the built-in default pack for the other servers.
const projectionRules = `  default_projections: true
  projections:
    - match: "github.*"
      drop: ["**.node_id", "**.*_url", "**.url", "**.reactions", "**.avatar_url", "**.gravatar_id"]
`

// projectionFields is the fields argument of the model-override call.
var projectionFields = []string{"[].number", "[].title", "[].state", "[].labels[].name", "[].user.login", "[].updated_at"}

type projCall struct {
	server, tool string
	// Tokens of the response line: truncation only, projection only,
	// projection then truncation.
	trunc, proj, both int
	// Items of the main list visible: truncation only, both.
	truncItems, bothItems int
	projMS, bothMS        float64
}

type projectionResults struct {
	calls []projCall
	// The fields call (github.list_issues): tokens and items, projection
	// then truncation.
	fieldsTokens, fieldsItems int
	// Every response line of the projection+truncation proxy within
	// max_tokens?
	maxBoth int
	// read_result of a projection id returns the full original.
	fullBack bool
	fullErr  string
}

func measureProjection(t *testing.T, bins binaries, cat *Catalog) projectionResults {
	t.Helper()
	var res projectionResults
	trunc := newEnv(t, bins, catalogServers(cat, "--large-results"), governorBlock)
	projOnly := newEnv(t, bins, catalogServers(cat, "--large-results"), "response:\n  enabled: true\n  max_tokens: 0\n"+projectionRules)
	both := newEnv(t, bins, catalogServers(cat, "--large-results"), governorBlock+projectionRules)
	pt, pp, pb := trunc.startProxy(t, bins), projOnly.startProxy(t, bins), both.startProxy(t, bins)
	defer pt.stop()
	defer pp.stop()
	defer pb.stop()
	for _, lt := range LargeResultTools {
		for _, p := range []*proc{pt, pp, pb} {
			p.mustCall("tools/call", invokeParams(lt.Server, lt.Tool, map[string]interface{}{"warm": true}))
		}
	}
	for _, lt := range LargeResultTools {
		c := projCall{server: lt.Server, tool: lt.Tool}
		args := map[string]interface{}{}
		rt, _, err := pt.call("tools/call", invokeParams(lt.Server, lt.Tool, args), callTimeout)
		if err != nil || rt.msg.Error != nil {
			t.Fatalf("truncation %s/%s: %v %.300s", lt.Server, lt.Tool, err, rt.line)
		}
		rp, dp, err := pp.call("tools/call", invokeParams(lt.Server, lt.Tool, args), callTimeout)
		if err != nil || rp.msg.Error != nil {
			t.Fatalf("projection %s/%s: %v %.300s", lt.Server, lt.Tool, err, rp.line)
		}
		rb, db, err := pb.call("tools/call", invokeParams(lt.Server, lt.Tool, args), callTimeout)
		if err != nil || rb.msg.Error != nil {
			t.Fatalf("projection+truncation %s/%s: %v %.300s", lt.Server, lt.Tool, err, rb.line)
		}
		c.trunc, c.proj, c.both = tokens(rt.line), tokens(rp.line), tokens(rb.line)
		c.truncItems, c.bothItems = visibleItems(firstText(rt)), visibleItems(firstText(rb))
		c.projMS, c.bothMS = float64(dp.Microseconds())/1000, float64(db.Microseconds())/1000
		res.maxBoth = max(res.maxBoth, c.both)
		res.calls = append(res.calls, c)

		if lt.Tool == "list_issues" {
			res.fullBack, res.fullErr = projectionFullBack(t, pp, rp)
		}
	}

	params := invokeParams("github", "list_issues", map[string]interface{}{})
	params["arguments"].(map[string]interface{})["fields"] = projectionFields
	rf, _, err := pb.call("tools/call", params, callTimeout)
	if err != nil || rf.msg.Error != nil {
		t.Fatalf("fields call: %v %.300s", err, rf.line)
	}
	res.fieldsTokens, res.fieldsItems = tokens(rf.line), visibleItems(firstText(rf))
	res.maxBoth = max(res.maxBoth, res.fieldsTokens)
	return res
}

// projectionFullBack reads the full copy of a projected result back
// (jsonpath $ on the projection id) and checks it has the dropped fields.
func projectionFullBack(t *testing.T, p *proc, r reply) (bool, string) {
	t.Helper()
	text := toolText(r)
	i := strings.Index(text, "result_id=r_")
	if i < 0 {
		return false, "no projection note"
	}
	id := text[i+10 : i+10+28]
	back, _, err := p.call("tools/call", routerCall("read_result", map[string]interface{}{"result_id": id, "jsonpath": "$[0]"}), callTimeout)
	if err != nil || back.msg.Error != nil {
		return false, fmt.Sprintf("read_result: %v %.200s", err, back.line)
	}
	first := firstText(back)
	if !strings.Contains(first, `"node_id"`) || !strings.Contains(first, `"html_url"`) {
		return false, "the full copy lacks the dropped fields: " + first
	}
	return true, ""
}

// firstText is the first text item of a tools/call result.
func firstText(r reply) string {
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(r.msg.Result, &res) != nil || len(res.Content) == 0 {
		return ""
	}
	return res.Content[0].Text
}

// visibleItems counts the elements of a JSON result's main list (the root
// array, or the largest array member of the root object) that are not the
// governor's omission object; -1 for a result that is not JSON.
func visibleItems(text string) int {
	var root interface{}
	if json.Unmarshal([]byte(text), &root) != nil {
		return -1
	}
	var list []interface{}
	switch v := root.(type) {
	case []interface{}:
		list = v
	case map[string]interface{}:
		for _, m := range v {
			if a, ok := m.([]interface{}); ok && len(a) > len(list) {
				list = a
			}
		}
	default:
		return -1
	}
	n := 0
	for _, el := range list {
		if o, ok := el.(map[string]interface{}); ok {
			if _, omitted := o["__leanproxy_omitted"]; omitted {
				continue
			}
		}
		n++
	}
	return n
}

// noisyListings are the large-results tools whose fixtures carry API noise
// the measured rules drop; the others (a source file, SQL rows) have none
// and must come back unchanged.
var noisyListings = map[string]bool{"list_issues": true, "search_code": true, "jira_search": true}

// projectionAssertions are field projection's harness assertions.
func projectionAssertions(p projectionResults, g governorResults) []assertion {
	noisyOff, noisyProj := 0, 0
	savesNoisy, keepsOthers, moreItems := true, true, true
	for i, c := range p.calls {
		off := g.calls[i].off
		if noisyListings[c.tool] {
			noisyOff += off
			noisyProj += c.proj
			if c.proj >= off {
				savesNoisy = false
			}
		} else if c.proj != off {
			keepsOthers = false
		}
		if c.bothItems < c.truncItems {
			moreItems = false
		}
	}
	return []assertion{
		{
			name:     "Field projection: projection alone saves tokens on every noisy listing and leaves the others unchanged",
			measured: fmt.Sprintf("listings %d → %d tokens (%s), others unchanged: %v", noisyOff, noisyProj, pct(noisyProj, noisyOff), keepsOthers),
			pass:     savesNoisy && keepsOthers,
			detail:   fmt.Sprintf("savesNoisy=%v keepsOthers=%v", savesNoisy, keepsOthers),
		},
		{
			name:     "Field projection: on top of truncation, at least as many list items within the same budget",
			measured: fmt.Sprintf("never fewer: %v", moreItems),
			pass:     moreItems,
			detail:   fmt.Sprintf("moreItems=%v", moreItems),
		},
		{
			name:     "Field projection: every projected+truncated response line ≤ max_tokens (4,000)",
			measured: fmt.Sprintf("max %d tokens", p.maxBoth),
			pass:     p.maxBoth <= 4000,
			detail:   fmt.Sprintf("max %d", p.maxBoth),
		},
		{
			name:     "Field projection: read_result returns the dropped fields from the full copy",
			measured: fmt.Sprintf("%v", p.fullBack),
			pass:     p.fullBack,
			detail:   p.fullErr,
		},
	}
}

// renderProjection is the field projection section of
// bench-results/harness.md.
func renderProjection(p projectionResults, g governorResults) string {
	var b strings.Builder
	w := func(format string, args ...interface{}) { fmt.Fprintf(&b, format, args...) }
	items := func(n int) string {
		if n < 0 {
			return "—"
		}
		return fmt.Sprintf("%d", n)
	}
	w("\n## Field projection (large results, #320)\n\n")
	w("The large-results calls through three more proxies: projection only (`max_tokens: 0`), truncation only (the #319 row above) and projection then truncation (`max_tokens: 4000`). Rules: the `github.*` drop pack of the issue and `default_projections: true`. \"Items\" is the number of list elements visible within the budget.\n\n")
	w("| Call | Off | Projection only | Truncation only (items) | Projection + truncation (items) | Latency proj / both |\n|---|---:|---:|---:|---:|---:|\n")
	off, proj, trunc, both := 0, 0, 0, 0
	for i, c := range p.calls {
		o := g.calls[i].off
		off += o
		proj += c.proj
		trunc += c.trunc
		both += c.both
		w("| `%s.%s` | %d | %d (%s) | %d (%s) | %d (%s) | %.1f / %.1f ms |\n", c.server, c.tool, o, c.proj, pct(c.proj, o), c.trunc, items(c.truncItems), c.both, items(c.bothItems), c.projMS, c.bothMS)
	}
	w("| **Total** | **%d** | **%d (%s)** | **%d** | **%d** | |\n", off, proj, pct(proj, off), trunc, both)
	for _, c := range p.calls {
		if c.tool != "list_issues" {
			continue
		}
		w("\n| Model override | Value |\n|---|---:|\n")
		w("| `github.list_issues` with `fields: %s` | %d tokens, %d items (truncation only: %d tokens, %d items) |\n",
			strings.Join(projectionFields, ", "), p.fieldsTokens, p.fieldsItems, c.trunc, c.truncItems)
	}
	return b.String()
}
