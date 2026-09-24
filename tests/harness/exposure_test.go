//go:build harness

package harness

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
)

// Exposure modes (issue #322): what a client with native tool search
// (Claude Code, clientInfo.name "claude-code") pays in passthrough mode —
// every upstream tool listed, namespaced — against the router's
// search_tools flow, for the same replayed sessions.
//
// The session model for passthrough follows what such a client does with a
// large tool list (Claude Code's MCP tool search, on by default): only the
// tool names enter the model context at session start; a tool's full
// definition is loaded the first time it is needed (one client-side
// search round trip, the same extra turn search_tools costs). The client's
// own search tool definition and its search request are not counted (they
// exist with or without LeanProxy); neither are tool results (identical on
// every path).

// exposureClient is the clientInfo.name the built-in table maps to
// passthrough.
const exposureClient = "claude-code"

var exposedToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type exposureSession struct {
	name string
	// Router (search_tools flow) and native, from the token replay.
	router, native int
	// Passthrough, client-side deferred loading.
	deferred, deferredTurns int
	// Passthrough, a client that loads every definition up front.
	eager int
}

type exposureResults struct {
	tools int // tools passthrough lists
	// tools/list line tokens.
	routerList, passthroughList, hybridList, nativeTotal int
	// Names-only context of a deferring client (the tool names, one per
	// line), and the average full definition of one tool.
	namesOnly     int
	avgDefinition float64
	// Request line of the same call: invoke_tool vs the namespaced name.
	invokeRequest, passthroughRequest int
	sessions                          []exposureSession
	invalidNames                      []string
	routed, routedOK                  int
}

// startProxyAs starts `server run --stdio` and initializes it as client,
// negotiating version.
func (e *env) startProxyAs(t testing.TB, bins binaries, client, version string) *proc {
	t.Helper()
	p := startProc(t, e.vars, bins.proxy, "server", "run", "--stdio", "--config", e.cfg)
	p.mustCall("initialize", map[string]interface{}{
		"protocolVersion": version,
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": client, "version": "1"},
	})
	if err := p.write(map[string]string{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		t.Fatal(err)
	}
	return p
}

func measureExposure(t *testing.T, bins binaries, cat *Catalog, tr tokenResults) exposureResults {
	t.Helper()
	res := exposureResults{nativeTotal: tr.nativeTotal, routerList: tr.router}

	e := newEnv(t, bins, catalogServers(cat), "")
	p := e.startProxyAs(t, bins, exposureClient, "2025-06-18")
	defer p.stop()

	// The first tools/list may run before every server answered; list
	// until the whole catalog is there (the steady state, which
	// tools/list_changed brings a client to).
	var list reply
	var entries []json.RawMessage
	for attempt := 0; attempt < 100; attempt++ {
		list = p.mustCall("tools/list", map[string]interface{}{})
		var body struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(list.msg.Result, &body); err != nil {
			t.Fatalf("passthrough tools/list: %v %.300s", err, list.line)
		}
		entries = body.Tools
		if len(entries) >= cat.ToolCount() {
			break
		}
	}
	res.tools = len(entries)
	res.passthroughList = tokens(list.line)

	defs := make(map[string]int, len(entries))
	names := make([]string, 0, len(entries))
	sum := 0
	for _, raw := range entries {
		var probe struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &probe)
		if !exposedToolName.MatchString(probe.Name) {
			res.invalidNames = append(res.invalidNames, probe.Name)
		}
		names = append(names, probe.Name)
		n := tokens(raw)
		defs[probe.Name] = n
		sum += n
	}
	res.namesOnly = tokens([]byte(strings.Join(names, "\n")))
	if len(entries) > 0 {
		res.avgDefinition = float64(sum) / float64(len(entries))
	}

	args := map[string]interface{}{"owner": "octo", "repo": "demo", "title": "Login crash"}
	req, _ := jsonLine(t, 0, "tools/call", invokeParams("github", "create_issue", args))
	res.invokeRequest = tokens(req)
	req, _ = jsonLine(t, 0, "tools/call", routerCall("github__create_issue", args))
	res.passthroughRequest = tokens(req)

	// Hybrid: the same list plus search_tools (and read_result while the
	// governor is on; it is off here).
	he := newEnv(t, bins, catalogServers(cat), "exposure:\n  mode: hybrid\n")
	hp := he.startProxyAs(t, bins, "leanproxy-harness", "2025-06-18")
	for attempt := 0; attempt < 100; attempt++ {
		r := hp.mustCall("tools/list", map[string]interface{}{})
		res.hybridList = tokens(r.line)
		if strings.Count(string(r.msg.Result), `"name":`) > cat.ToolCount() {
			break
		}
	}
	hp.stop()

	// Session replay: every call really runs through the namespaced name.
	for i, s := range sessions {
		sr := exposureSession{name: s.name}
		if i < len(tr.sessionResults) {
			sr.router, sr.native = tr.sessionResults[i].searchLean, tr.sessionResults[i].native
		}
		carried := float64(res.namesOnly)
		loaded := map[string]bool{}
		var deferred, eager float64
		for k, pr := range s.prompts {
			rate := 1.0
			if k > 0 {
				rate = cacheReadMultiplier
			}
			eager += rate * float64(res.passthroughList)
			name := pr.server + "__" + pr.tool
			turn := rate * carried
			if !loaded[name] {
				loaded[name] = true
				turn += float64(defs[name])
				carried += float64(defs[name])
				sr.deferredTurns++
			}
			deferred += turn

			res.routed++
			r, _, err := p.call("tools/call", routerCall(name, map[string]interface{}{"prompt": k}), callTimeout)
			if err == nil && r.msg.Error == nil && strings.Contains(toolText(r), `"called":"`+pr.tool+`"`) {
				res.routedOK++
			} else {
				t.Logf("passthrough call %s: %v %.300s", name, err, r.line)
			}
		}
		sr.deferred = int(math.Round(deferred))
		sr.eager = int(math.Round(eager))
		res.sessions = append(res.sessions, sr)
	}
	return res
}

// exposureAssertions: passthrough lists the whole catalog under valid
// names and routes every replayed call. The token numbers are reported,
// not asserted: which mode costs less depends on the client.
func exposureAssertions(x exposureResults, cat *Catalog) []assertion {
	return []assertion{
		{
			name:     "Passthrough (claude-code): tools/list lists every catalog tool under a ^[a-zA-Z0-9_-]{1,64}$ name",
			measured: fmt.Sprintf("%d/%d tools, %d invalid names", x.tools, cat.ToolCount(), len(x.invalidNames)),
			pass:     x.tools == cat.ToolCount() && len(x.invalidNames) == 0,
			detail:   fmt.Sprintf("invalid: %v", x.invalidNames),
		},
		{
			name:     "Passthrough: every replayed session call routes by its namespaced name",
			measured: fmt.Sprintf("%d/%d", x.routedOK, x.routed),
			pass:     x.routed > 0 && x.routedOK == x.routed,
			detail:   fmt.Sprintf("%d/%d", x.routedOK, x.routed),
		},
	}
}

// renderExposure is the exposure section of bench-results/harness.md.
func renderExposure(x exposureResults) string {
	var b strings.Builder
	w := func(format string, args ...interface{}) { fmt.Fprintf(&b, format, args...) }
	w("\n## Exposure modes: router vs passthrough for a client with native tool search (#322)\n\n")
	w("The same %d-tool catalog, through `server run --stdio` initialized as `%s` (built-in table: passthrough) and as an unknown client (router). Tokens are those of the JSON-RPC line.\n\n", x.tools, exposureClient)
	w("| Payload | Tokens |\n|---|---:|\n")
	w("| Native: every server's own `tools/list`, summed | %d |\n", x.nativeTotal)
	w("| Router `tools/list` (4 gateway tools) | %d |\n", x.routerList)
	w("| Passthrough `tools/list` (every tool, namespaced, full metadata): the one-time transfer to the client | %d (%s vs native) |\n", x.passthroughList, pct(x.passthroughList, x.nativeTotal))
	w("| Hybrid `tools/list` (passthrough + `search_tools`) | %d |\n", x.hybridList)
	w("| Passthrough names only (what a deferring client keeps in context) | %d |\n", x.namesOnly)
	w("| One tool's full definition, loaded on first use: average | %.0f |\n", x.avgDefinition)
	w("| Call request line: `invoke_tool` envelope / namespaced passthrough name | %d / %d |\n", x.invokeRequest, x.passthroughRequest)
	w("\nSession model (same sessions and cache-read rate as the replay above; tool results excluded): **router** = the `search_tools` flow; **passthrough, deferred** = names in context from turn 1, a tool's definition loaded (1×, then carried) the first time it is used, one client-side search turn each; **passthrough, eager** = a client that loads the whole list every turn, like native.\n\n")
	w("| Session | Native | Router (`search_tools`) | Passthrough, deferred | vs router | Deferred loads (extra turns) | Passthrough, eager |\n|---|---:|---:|---:|---:|---:|---:|\n")
	for _, s := range x.sessions {
		w("| %s | %d | %d | %d | %s | +%d | %d (%s vs native) |\n", s.name, s.native, s.router, s.deferred, pct(s.deferred, s.router), s.deferredTurns, s.eager, pct(s.eager, s.native))
	}
	w("\nReading: passthrough never makes the wire payload smaller — its `tools/list` is the whole catalog, about the size of the native lists, once per session (and again after each `tools/list_changed`). It pays off only when the client defers definitions itself; a client that loads every tool up front pays native-like costs, which is why unknown clients stay on the router.\n")
	return b.String()
}
