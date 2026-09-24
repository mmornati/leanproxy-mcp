//go:build harness

package harness

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

// Response token governor (issue #319): a "large results" session (a file
// read and the list/search endpoints of LargeResultTools) replayed through
// the real binary with the governor off (the default) and on, plus checks
// that normal-size results are untouched and that read_result pages the
// whole original back.

// governorBlock turns the governor on with the documented defaults.
const governorBlock = `response:
  enabled: true
  max_tokens: 4000
`

// largeCall is one large-results call measured with the governor off and on.
type largeCall struct {
	server, tool string
	off, on      int // tokens of the JSON-RPC response line
	offMS, onMS  float64
}

type governorResults struct {
	calls []largeCall
	// Session model: each result enters the context once at 1× and is
	// re-read on every later turn at the cache-read rate.
	sessionOff, sessionOn int
	// Normal-size results (the replayed sessions' calls): identical with
	// the governor on?
	normalCalls, normalIdentical int
	// Paced normal-size invoke_tool latency, governor off vs on.
	normalOffMS, normalOnMS []float64
	// read_result: pages to read get_file_contents back, and whether the
	// concatenation equals the original.
	pages     int
	reassembl bool
	readErr   string
}

func measureGovernor(t *testing.T, bins binaries, cat *Catalog) governorResults {
	t.Helper()
	var res governorResults

	largeOff := newEnv(t, bins, catalogServers(cat, "--large-results"), "")
	largeOn := newEnv(t, bins, catalogServers(cat, "--large-results"), governorBlock)
	off, on := largeOff.startProxy(t, bins), largeOn.startProxy(t, bins)
	for _, lt := range LargeResultTools {
		// Warm both (first call starts the upstream).
		off.mustCall("tools/call", invokeParams(lt.Server, lt.Tool, map[string]interface{}{"warm": true}))
		on.mustCall("tools/call", invokeParams(lt.Server, lt.Tool, map[string]interface{}{"warm": true}))
	}
	var carriedOff, carriedOn float64
	var sessionOff, sessionOn float64
	for i, lt := range LargeResultTools {
		ro, do, err := off.call("tools/call", invokeParams(lt.Server, lt.Tool, map[string]interface{}{}), callTimeout)
		if err != nil || ro.msg.Error != nil {
			t.Fatalf("governor off %s/%s: %v %.300s", lt.Server, lt.Tool, err, ro.line)
		}
		rn, dn, err := on.call("tools/call", invokeParams(lt.Server, lt.Tool, map[string]interface{}{}), callTimeout)
		if err != nil || rn.msg.Error != nil {
			t.Fatalf("governor on %s/%s: %v %.300s", lt.Server, lt.Tool, err, rn.line)
		}
		c := largeCall{server: lt.Server, tool: lt.Tool, off: tokens(ro.line), on: tokens(rn.line),
			offMS: float64(do.Microseconds()) / 1000, onMS: float64(dn.Microseconds()) / 1000}
		res.calls = append(res.calls, c)
		if i > 0 {
			sessionOff += cacheReadMultiplier * carriedOff
			sessionOn += cacheReadMultiplier * carriedOn
		}
		sessionOff += float64(c.off)
		sessionOn += float64(c.on)
		carriedOff += float64(c.off)
		carriedOn += float64(c.on)

		if lt.Tool == "get_file_contents" {
			res.pages, res.reassembl, res.readErr = readBack(t, on, toolText(rn), toolText(ro))
		}
	}
	res.sessionOff, res.sessionOn = int(math.Round(sessionOff)), int(math.Round(sessionOn))
	off.stop()
	on.stop()

	// Normal-size results: every replayed-session call, off vs on.
	normOff := newEnv(t, bins, catalogServers(cat), "")
	normOn := newEnv(t, bins, catalogServers(cat), governorBlock)
	no, nn := normOff.startProxy(t, bins), normOn.startProxy(t, bins)
	for _, s := range sessions {
		for i, pr := range s.prompts {
			args := map[string]interface{}{"prompt": i}
			a := no.mustCall("tools/call", invokeParams(pr.server, pr.tool, args))
			b := nn.mustCall("tools/call", invokeParams(pr.server, pr.tool, args))
			res.normalCalls++
			if string(a.msg.Result) == string(b.msg.Result) {
				res.normalIdentical++
			}
		}
	}
	params := func(k int) map[string]interface{} {
		return invokeParams("slack", "slack_post_message", map[string]interface{}{"channel_id": "c", "text": fmt.Sprintf("msg %d", k)})
	}
	paced(t, no, sequentialWarmup, params)
	paced(t, nn, sequentialWarmup, params)
	res.normalOffMS = paced(t, no, sequentialCalls/2, params)
	res.normalOnMS = paced(t, nn, sequentialCalls/2, params)
	no.stop()
	nn.stop()
	return res
}

// readBack pages the governed get_file_contents result with read_result
// and compares the concatenation with the ungoverned text.
func readBack(t *testing.T, p *proc, governed, original string) (int, bool, string) {
	t.Helper()
	i := strings.Index(governed, "id=r_")
	if i < 0 {
		return 0, false, "no result id in the governed result"
	}
	id := governed[i+3 : i+3+28]
	var b strings.Builder
	offset, pages := 0, 0
	for ; pages < 500; pages++ {
		r, _, err := p.call("tools/call", routerCall("read_result", map[string]interface{}{"result_id": id, "offset": offset}), callTimeout)
		if err != nil || r.msg.Error != nil {
			return pages, false, fmt.Sprintf("read_result: %v %.200s", err, r.line)
		}
		var res struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(r.msg.Result, &res); err != nil || res.IsError || len(res.Content) != 2 {
			return pages, false, fmt.Sprintf("read_result page: %.200s", r.line)
		}
		b.WriteString(res.Content[0].Text)
		nav := res.Content[1].Text
		if strings.Contains(nav, "end of result") {
			pages++
			break
		}
		j := strings.Index(nav, "offset=")
		if j < 0 {
			return pages, false, "no next offset: " + nav
		}
		if _, err := fmt.Sscanf(nav[j:], "offset=%d]", &offset); err != nil {
			return pages, false, err.Error()
		}
	}
	return pages, b.String() == original, ""
}

// governorAssertions are the governor's harness assertions.
func governorAssertions(g governorResults) []assertion {
	var out []assertion
	off, on := 0, 0
	for _, c := range g.calls {
		off += c.off
		on += c.on
	}
	saving := 0.0
	if off > 0 {
		saving = 100 * (1 - float64(on)/float64(off))
	}
	out = append(out, assertion{
		name:     "Response governor: large-results session saves ≥ 50% of result tokens",
		measured: fmt.Sprintf("%d → %d tokens (−%.1f%%)", off, on, saving),
		pass:     saving >= 50,
		detail:   fmt.Sprintf("%d → %d", off, on),
	})
	within := true
	for _, c := range g.calls {
		// The whole JSON-RPC line, envelope and escaping included.
		if c.on > 4000 {
			within = false
		}
	}
	out = append(out, assertion{
		name:     "Response governor: every governed response line ≤ max_tokens (4,000)",
		measured: fmt.Sprintf("max %d tokens", maxOn(g.calls)),
		pass:     within,
		detail:   fmt.Sprintf("max %d", maxOn(g.calls)),
	})
	out = append(out, assertion{
		name:     "Response governor: read_result pages rebuild the original exactly",
		measured: fmt.Sprintf("%d pages, identical: %v", g.pages, g.reassembl),
		pass:     g.reassembl && g.readErr == "",
		detail:   g.readErr,
	})
	out = append(out, assertion{
		name:     "Response governor: normal-size results unchanged",
		measured: fmt.Sprintf("%d/%d identical", g.normalIdentical, g.normalCalls),
		pass:     g.normalCalls > 0 && g.normalIdentical == g.normalCalls,
		detail:   fmt.Sprintf("%d/%d", g.normalIdentical, g.normalCalls),
	})
	return out
}

func maxOn(calls []largeCall) int {
	m := 0
	for _, c := range calls {
		m = max(m, c.on)
	}
	return m
}

// renderGovernor is the governor section of bench-results/harness.md.
func renderGovernor(g governorResults) string {
	var b strings.Builder
	w := func(format string, args ...interface{}) { fmt.Fprintf(&b, format, args...) }
	w("\n## Response governor (large results, #319)\n\n")
	w("The same calls through two proxies over `catalogmcp --large-results`: the default config (governor off) and `response: {enabled: true, max_tokens: 4000}`. Tokens are those of the whole JSON-RPC response line.\n\n")
	w("| Call | Governor off | Governor on | Savings | Latency off / on |\n|---|---:|---:|---:|---:|\n")
	off, on := 0, 0
	for _, c := range g.calls {
		off += c.off
		on += c.on
		w("| `%s.%s` | %d | %d | %s | %.1f / %.1f ms |\n", c.server, c.tool, c.off, c.on, pct(c.on, c.off), c.offMS, c.onMS)
	}
	w("| **Total (each result once)** | **%d** | **%d** | **%s** | |\n", off, on, pct(on, off))
	w("| **Session (%d turns, earlier results re-read at 0.25×)** | **%d** | **%d** | **%s** | |\n", len(g.calls), g.sessionOff, g.sessionOn, pct(g.sessionOn, g.sessionOff))
	w("\n| Check | Value |\n|---|---:|\n")
	w("| `read_result` pages to read the file back (4,000 tokens per page) / identical to the original | %d / %v |\n", g.pages, g.reassembl)
	w("| Normal-size results identical with the governor on | %d/%d |\n", g.normalIdentical, g.normalCalls)
	w("| Paced normal-size `invoke_tool`, p50 / p95: governor off | %.2f / %.2f ms |\n", percentile(g.normalOffMS, 0.5), percentile(g.normalOffMS, 0.95))
	w("| Same, governor on | %.2f / %.2f ms |\n", percentile(g.normalOnMS, 0.5), percentile(g.normalOnMS, 0.95))
	return b.String()
}
