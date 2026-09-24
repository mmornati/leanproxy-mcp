//go:build harness

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Code mode spike (issue #325): the same multi-call tasks done the plain
// way (one invoke_tool per call, every result into the model's context)
// and with one execute_code call whose program filters the results in the
// sandbox, through a proxy built with -tags codemode. It measures tokens
// crossing the pipe and wall-clock latency; it does not measure a model's
// success rate (that needs a real model, see docs/design/code-mode.md).

const codeModeBlock = "code_mode:\n  enabled: true\n"

// codeModeRepos are the five repositories of the "top issues" task. The
// catalog mock ignores the repository and returns the same 300 issues:
// the results are the same size a real list_issues page is.
var codeModeRepos = []string{"api", "web", "cli", "docs", "infra"}

// topIssuesCode is what a model would write for "find the 3 open issues
// with the most comments across these 5 repos".
const topIssuesCode = `const repos = ["api", "web", "cli", "docs", "infra"];
const lists = await Promise.all(repos.map(repo => tools.github.list_issues({owner: "octo", repo, state: "open"})));
return lists.flatMap((issues, i) => issues.map(x => ({repo: repos[i], number: x.number, title: x.title, comments: x.comments})))
  .sort((a, b) => b.comments - a.comments).slice(0, 3);`

// codeModeCall is one plain tool call of a scenario.
type codeModeCall struct {
	server, tool string
	args         map[string]interface{}
}

// codeModeScenario is one task done both ways.
type codeModeScenario struct {
	name  string
	calls []codeModeCall
	code  string
	// governed runs both paths with the response governor on.
	governed bool
}

func codeModeScenarios() []codeModeScenario {
	top := codeModeScenario{name: "Top 3 issues by comments across 5 repos (5 × list_issues, 300 issues each)", code: topIssuesCode}
	for _, repo := range codeModeRepos {
		top.calls = append(top.calls, codeModeCall{"github", "list_issues", map[string]interface{}{"owner": "octo", "repo": repo, "state": "open"}})
	}
	governed := top
	governed.name = "Same task, response governor on (max_tokens 4000)"
	governed.governed = true
	return []codeModeScenario{
		top,
		governed,
		{
			name:  "One small call (create_issue)",
			calls: []codeModeCall{{"github", "create_issue", map[string]interface{}{"owner": "octo", "repo": "demo", "title": "Login crash"}}},
			code:  `return await tools.github.create_issue({owner: "octo", repo: "demo", title: "Login crash"});`,
		},
		{
			name: "Two small calls, both results needed (sleep + readiness)",
			calls: []codeModeCall{
				{"garmin", "get_sleep_data", map[string]interface{}{"date": "2026-09-23"}},
				{"garmin", "get_training_readiness", map[string]interface{}{"date": "2026-09-23"}},
			},
			code: `const [sleep, readiness] = await Promise.all([tools.garmin.get_sleep_data({date: "2026-09-23"}), tools.garmin.get_training_readiness({date: "2026-09-23"})]);
return {sleep, readiness};`,
		},
	}
}

type codeModeResult struct {
	name string
	// Tokens of the JSON-RPC lines: requests plus responses.
	plainTokens, codeTokens int
	// Latency medians (ms): plain calls one after another (one model turn
	// each), plain calls all at once (parallel tool calls in one turn),
	// and the execute_code call.
	plainSeqMS, plainParMS, codeMS float64
	// Model turns: sequential plain / parallel plain / code mode.
	turnsSeq, turnsPar, turnsCode int
	// codeOK: the program answered; codeCorrect: its answer matches the
	// one computed from the plain results (checked for the top-issues
	// task only).
	codeOK, codeCorrect bool
	checked             bool
	codeErr             string
}

type codeModeResults struct {
	scenarios []codeModeResult
	// execute_code's definition in tools/list (enabled minus disabled).
	listOverhead int
	// Floor: execute_code(`return 1`) vs one small invoke_tool, medians.
	floorCodeMS, floorPlainMS float64
	// Binary sizes (bytes).
	defaultBytes, codeModeBytes int64
}

const codeModeReps = 7

func measureCodeMode(t *testing.T, bins binaries, cat *Catalog) codeModeResults {
	t.Helper()
	var res codeModeResults
	if fi, err := os.Stat(bins.proxy); err == nil {
		res.defaultBytes = fi.Size()
	}
	if fi, err := os.Stat(bins.codemode); err == nil {
		res.codeModeBytes = fi.Size()
	}

	plain := newEnv(t, bins, catalogServers(cat, "--large-results"), codeModeBlock)
	governed := newEnv(t, bins, catalogServers(cat, "--large-results"), codeModeBlock+governorBlock)
	off := newEnv(t, bins, catalogServers(cat, "--large-results"), "")
	p := plain.startProxyBin(t, bins.codemode)
	g := governed.startProxyBin(t, bins.codemode)
	o := off.startProxyBin(t, bins.codemode)
	defer p.stop()
	defer g.stop()
	defer o.stop()

	res.listOverhead = tokens(p.mustCall("tools/list", map[string]interface{}{}).line) - tokens(o.mustCall("tools/list", map[string]interface{}{}).line)

	// The right answer of the top-issues task, from the ungoverned plain
	// results of the first scenario; the governed run is checked against
	// it too.
	var truth string
	for _, sc := range codeModeScenarios() {
		proc := p
		if sc.governed {
			proc = g
		}
		r, plainReplies, codeOut := runCodeModeScenario(t, proc, sc)
		if sc.code == topIssuesCode {
			if truth == "" {
				truth = topIssues(t, plainReplies)
			}
			r.checked = true
			r.codeCorrect = r.codeOK && truth != "" && sameJSON(truth, codeOut)
			if !r.codeCorrect {
				t.Logf("code mode: %s: program answered %.400s; the right answer is %.400s", sc.name, codeOut, truth)
			}
		}
		res.scenarios = append(res.scenarios, r)
	}

	// The floor: what one sandbox costs on its own.
	small := invokeParams("slack", "slack_post_message", map[string]interface{}{"channel_id": "c", "text": "hi"})
	codeMS := make([]float64, 0, 3*codeModeReps)
	plainMS := make([]float64, 0, 3*codeModeReps)
	for i := 0; i < 3*codeModeReps; i++ {
		_, d, err := p.call("tools/call", routerCall("execute_code", map[string]interface{}{"code": "return 1"}), callTimeout)
		if err != nil {
			t.Fatalf("execute_code floor: %v", err)
		}
		codeMS = append(codeMS, ms(d))
		_, d, err = p.call("tools/call", small, callTimeout)
		if err != nil {
			t.Fatalf("invoke_tool floor: %v", err)
		}
		plainMS = append(plainMS, ms(d))
	}
	res.floorCodeMS, res.floorPlainMS = percentile(codeMS, 0.5), percentile(plainMS, 0.5)
	return res
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// runCodeModeScenario runs one task both ways and returns the measures,
// the plain replies and the program's output (of the last repetition).
func runCodeModeScenario(t *testing.T, p *proc, sc codeModeScenario) (codeModeResult, []reply, string) {
	t.Helper()
	r := codeModeResult{name: sc.name, turnsSeq: len(sc.calls) + 1, turnsPar: 2, turnsCode: 2}

	// Warm the upstreams.
	for _, c := range sc.calls {
		p.mustCall("tools/call", invokeParams(c.server, c.tool, c.args))
	}

	seq := make([]float64, 0, codeModeReps)
	par := make([]float64, 0, codeModeReps)
	code := make([]float64, 0, codeModeReps)
	var plainReplies []reply
	var codeOut string
	for rep := 0; rep < codeModeReps; rep++ {
		// Plain, one after another.
		var total time.Duration
		plainReplies = plainReplies[:0]
		tokensPlain := 0
		for _, c := range sc.calls {
			params := invokeParams(c.server, c.tool, c.args)
			rp, d, err := p.call("tools/call", params, callTimeout)
			if err != nil || rp.msg.Error != nil {
				t.Fatalf("%s: plain %s.%s: %v %.300s", sc.name, c.server, c.tool, err, rp.line)
			}
			req, _ := jsonLine(t, 0, "tools/call", params)
			tokensPlain += tokens(req) + tokens(rp.line)
			plainReplies = append(plainReplies, rp)
			total += d
		}
		seq = append(seq, ms(total))
		r.plainTokens = tokensPlain

		// Plain, all at once.
		start := time.Now()
		pending := make([]*pendingCall, 0, len(sc.calls))
		for _, c := range sc.calls {
			pc, err := p.send("tools/call", invokeParams(c.server, c.tool, c.args))
			if err != nil {
				t.Fatal(err)
			}
			pending = append(pending, pc)
		}
		for _, pc := range pending {
			if rp, err := pc.wait(p, callTimeout); err != nil || rp.msg.Error != nil {
				t.Fatalf("%s: parallel call: %v %.300s", sc.name, err, rp.line)
			}
		}
		par = append(par, ms(time.Since(start)))

		// Code mode.
		params := routerCall("execute_code", map[string]interface{}{"code": sc.code})
		rc, d, err := p.call("tools/call", params, callTimeout)
		if err != nil || rc.msg.Error != nil {
			t.Fatalf("%s: execute_code: %v %.300s", sc.name, err, rc.line)
		}
		code = append(code, ms(d))
		req, _ := jsonLine(t, 0, "tools/call", params)
		r.codeTokens = tokens(req) + tokens(rc.line)
		var body struct {
			IsError bool `json:"isError"`
		}
		_ = json.Unmarshal(rc.msg.Result, &body)
		r.codeOK = !body.IsError
		codeOut = toolText(rc)
		if body.IsError {
			r.codeErr = codeOut
		}
	}
	r.plainSeqMS, r.plainParMS, r.codeMS = percentile(seq, 0.5), percentile(par, 0.5), percentile(code, 0.5)
	return r, plainReplies, codeOut
}

// topIssues computes the top-issues answer from the plain results ("" when
// they are not whole JSON listings).
func topIssues(t *testing.T, plain []reply) string {
	t.Helper()
	type issue struct {
		Repo     string `json:"repo"`
		Number   int    `json:"number"`
		Title    string `json:"title"`
		Comments int    `json:"comments"`
	}
	var all []issue
	for i, rp := range plain {
		var issues []issue
		if err := json.Unmarshal([]byte(toolText(rp)), &issues); err != nil {
			return ""
		}
		for _, x := range issues {
			x.Repo = codeModeRepos[i]
			all = append(all, x)
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].Comments > all[b].Comments })
	if len(all) > 3 {
		all = all[:3]
	}
	out, _ := json.Marshal(all)
	return string(out)
}

// sameJSON reports whether two JSON texts hold the same value.
func sameJSON(a, b string) bool {
	var va, vb interface{}
	if json.Unmarshal([]byte(a), &va) != nil || json.Unmarshal([]byte(b), &vb) != nil {
		return false
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}

func codeModeAssertions(c codeModeResults) []assertion {
	top := c.scenarios[0]
	saving := 0.0
	if top.plainTokens > 0 {
		saving = 100 * (1 - float64(top.codeTokens)/float64(top.plainTokens))
	}
	return []assertion{
		{
			name:     "Code mode (spike): the multi-call program gets the same answer as the plain calls",
			measured: fmt.Sprintf("correct: %v", top.codeCorrect),
			pass:     top.codeCorrect,
			detail:   top.codeErr,
		},
		{
			name:     "Code mode (spike): the multi-call task saves ≥ 90% of the tokens",
			measured: fmt.Sprintf("%d → %d tokens (−%.1f%%)", top.plainTokens, top.codeTokens, saving),
			pass:     saving >= 90,
			detail:   fmt.Sprintf("%d → %d", top.plainTokens, top.codeTokens),
		},
		{
			name:     "Code mode (spike): the default binary does not contain it",
			measured: fmt.Sprintf("%.1f MiB default, %.1f MiB with -tags codemode", float64(c.defaultBytes)/(1<<20), float64(c.codeModeBytes)/(1<<20)),
			pass:     c.codeModeBytes > c.defaultBytes+(1<<20),
			detail:   fmt.Sprintf("%d vs %d bytes", c.defaultBytes, c.codeModeBytes),
		},
	}
}

func renderCodeMode(c codeModeResults) string {
	var b strings.Builder
	w := func(format string, args ...interface{}) { fmt.Fprintf(&b, format, args...) }
	w("\n## Code mode spike (#325, experimental, `-tags codemode`)\n\n")
	w("Each task done the plain way (one `invoke_tool` per call, every result into the context) and as one `execute_code` program, through a proxy built with `-tags codemode` over `catalogmcp --large-results`. Tokens are those of the JSON-RPC request and response lines (the code the model writes included); latency is the median of %d runs. Model turns: plain one-call-per-turn / plain with parallel tool calls / code mode. No model is involved: the programs are fixed, so this measures cost, not whether a model writes them correctly.\n\n", codeModeReps)
	w("| Task | Plain tokens | Code mode tokens | Change | Code mode + its definition (one-task session) | Plain latency seq / parallel | Code mode latency | Turns | Code mode answer |\n|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, s := range c.scenarios {
		answer := "ok"
		switch {
		case !s.codeOK:
			answer = "**failed**: " + strings.ReplaceAll(firstLine(s.codeErr), "|", "\\|")
		case s.checked && s.codeCorrect:
			answer = "correct"
		case s.checked:
			answer = "**wrong** (the program saw governed, shortened results)"
		}
		withDef := s.codeTokens + c.listOverhead
		w("| %s | %d | %d | %s | %d · %s | %.1f / %.1f ms | %.1f ms | %d / %d / %d | %s |\n", s.name, s.plainTokens, s.codeTokens, pct(s.codeTokens, s.plainTokens), withDef, pct(withDef, s.plainTokens), s.plainSeqMS, s.plainParMS, s.codeMS, s.turnsSeq, s.turnsPar, s.turnsCode, answer)
	}
	w("\n| Fixed cost | Value |\n|---|---:|\n")
	w("| `execute_code` definition in `tools/list` (every session) | %d tokens |\n", c.listOverhead)
	w("| Sandbox floor: `execute_code(\"return 1\")` vs one small `invoke_tool`, p50 | %.1f ms vs %.1f ms |\n", c.floorCodeMS, c.floorPlainMS)
	w("| Binary size, default vs `-tags codemode` (`-s -w`) | %.1f MiB vs %.1f MiB (+%.1f MiB) |\n", float64(c.defaultBytes)/(1<<20), float64(c.codeModeBytes)/(1<<20), float64(c.codeModeBytes-c.defaultBytes)/(1<<20))
	return b.String()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 160 {
		line = line[:157] + "..."
	}
	return line
}
