//go:build harness

package harness

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/usage"
)

// TestSavingsReportCrossCheck is issue #324's acceptance criterion: "After
// a harness session, report --export json totals match the harness's own
// accounting within 1%". It drives the real binary through a session that
// exercises every mechanism the savings report measures (schema, discovery,
// response truncation, response dedup), independently measures the same
// bytes the harness's own token unit (tokens(), pkg/reporter's chars/4
// estimator) would report, then runs `leanproxy-mcp report --export json`
// as a real subprocess against the same isolated HOME and checks the
// report's numbers against that independent measurement.
func TestSavingsReportCrossCheck(t *testing.T) {
	dir := t.TempDir()
	bins := buildBinaries(t, dir)
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	const reportGovernorBlock = `response:
  enabled: true
  max_tokens: 4000
  dedup: "on"
`
	e := newEnv(t, bins, catalogServers(cat, "--large-results"), reportGovernorBlock)
	p := e.startProxy(t, bins)

	// Schema accounting: one tools/list under router (default exposure)
	// mode.
	p.mustCall("tools/list", nil)

	// Discovery accounting: one list_tools call.
	p.mustCall("tools/call", routerCall("list_tools", map[string]interface{}{"server_name": "github"}))

	// Response governor: get_file_contents is large enough to truncate
	// (issue #319's LargeResultTools). Warm the upstream first (its first
	// call spawns the mock server and is not measured), then call it
	// twice with identical arguments -- the second call hits in-session
	// dedup (issue #321), the first hits plain truncation.
	// warm spawns the upstream (its first call is otherwise slow); the mock
	// tool ignores the "warm" argument and returns the same deterministic
	// large content as a normal call, so it is measured like any other
	// governed call below (the first of three identical large results:
	// truncated; the next two hit in-session dedup, issue #321).
	warmArgs := map[string]interface{}{"warm": true}
	warm := p.mustCall("tools/call", invokeParams("github", "get_file_contents", warmArgs))

	callArgs := map[string]interface{}{}
	first := p.mustCall("tools/call", invokeParams("github", "get_file_contents", callArgs))
	second := p.mustCall("tools/call", invokeParams("github", "get_file_contents", callArgs))

	// Independent baseline: the same call through a second, ungoverned
	// proxy in the same catalog gives the harness's own measurement of
	// the original (pre-governor) response size, exactly as
	// tests/harness/governor_test.go does.
	eOff := newEnv(t, bins, catalogServers(cat, "--large-results"), "")
	pOff := eOff.startProxy(t, bins)
	origReply := pOff.mustCall("tools/call", invokeParams("github", "get_file_contents", warmArgs))
	pOff.stop()

	origTokens := tokens(origReply.line)
	warmTokens := tokens(warm.line)
	firstTokens := tokens(first.line)
	secondTokens := tokens(second.line)
	// Harness-measured expected saving across the three governed calls
	// (warm + first + second): each started from the same original size;
	// the governor either truncated or deduped it down to
	// warmTokens/firstTokens/secondTokens.
	expectedGovernorSaved := int64(3*origTokens - warmTokens - firstTokens - secondTokens)
	if expectedGovernorSaved <= 0 {
		t.Fatalf("test setup: governor did not shrink the response (orig=%d warm=%d first=%d second=%d)", origTokens, warmTokens, firstTokens, secondTokens)
	}

	p.stop() // closes stdin: EOF triggers the same shutdown flush as SIGINT (cmd/server.go handleStdio).

	// Run the real `report --export json` CLI, in-process built binary,
	// against the same isolated HOME the proxy just wrote its usage
	// records to.
	cmd := exec.Command(bins.proxy, "report", "--export", "json")
	cmd.Env = e.vars
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("report --export json failed: %v\nstderr:\n%s", err, ee.Stderr)
		}
		t.Fatalf("report --export json failed: %v", err)
	}

	var rep usage.SavingsReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("unmarshal report JSON: %v\noutput:\n%s", err, out)
	}

	// The mechanism breakdown must sum to the total (acceptance
	// criterion: "the breakdown by mechanism sums to the total saved").
	var sum int64
	var governorSaved int64
	for _, m := range rep.Mechanisms {
		if !m.IsCost {
			sum += m.SavedTokens
		}
		switch m.Mechanism {
		case "response_truncation", "response_projection", "response_dedup", "response_summarization":
			governorSaved += m.SavedTokens
		}
	}
	if sum != rep.TotalSavedTokens {
		t.Errorf("mechanism SavedTokens sum to %d, want TotalSavedTokens %d", sum, rep.TotalSavedTokens)
	}

	// Cross-check against the harness's own independent measurement
	// (within 1%, per the acceptance criterion).
	assertWithinPercent(t, "response governor saved tokens", expectedGovernorSaved, governorSaved, 1)

	if rep.Estimator != "chars/4" {
		t.Errorf("Estimator = %q, want chars/4 (the same estimator tokens() uses)", rep.Estimator)
	}

	// Every output format must carry measured/estimated labels (a second,
	// full-process check on top of pkg/usage's unit test).
	textCmd := exec.Command(bins.proxy, "report")
	textCmd.Env = e.vars
	textOut, err := textCmd.Output()
	if err != nil {
		t.Fatalf("report (text): %v", err)
	}
	text := string(textOut)
	if !strings.Contains(text, "measured") || !strings.Contains(text, "estimated") {
		t.Errorf("text report missing measured/estimated labels:\n%s", text)
	}

	// Offline/privacy: nothing in any format ever carries a tool argument
	// or upstream payload value, only names/numbers/timestamps. The
	// large-file fixture (tests/harness/large.go) is deterministic; a
	// substring unique to its content must never appear in the report.
	if content, ok := LargeResult("get_file_contents"); ok && len(content) >= 64 {
		canary := content[:64]
		if strings.Contains(string(out), canary) || strings.Contains(text, canary) {
			t.Fatalf("report leaked payload content: found %q", canary)
		}
	}
}

// assertWithinPercent fails the test if got is more than pct% away from
// want (and, when want is 0, if got is nonzero).
func assertWithinPercent(t *testing.T, label string, want, got int64, pct float64) {
	t.Helper()
	if want == 0 {
		if got != 0 {
			t.Errorf("%s: got %d, want 0", label, got)
		}
		return
	}
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	allowed := float64(want) * pct / 100
	if float64(diff) > allowed {
		t.Errorf("%s: got %d, want %d (+/- %.0f%%, allowed delta %.1f, actual delta %d)", label, got, want, pct, allowed, diff)
	}
}
