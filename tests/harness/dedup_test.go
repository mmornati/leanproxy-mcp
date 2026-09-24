//go:build harness

package harness

import (
	"fmt"
	"strings"
	"testing"
)

// In-session dedup (issue #321): a "repeated reads" session — an agent
// re-reading the same file three times in a row, as happens after a failed
// edit — replayed through the real binary with response.dedup off and on.

// dedupBlock turns the governor on with dedup, on top of the documented
// truncation defaults.
const dedupBlock = `response:
  enabled: true
  max_tokens: 4000
  dedup: on
`

// dedupRepeats is how many times the same large result is re-read in the
// simulated session (the first read plus this many repeats).
const dedupRepeats = 3

type dedupResults struct {
	// Tokens of one call, off (dedup off, truncation only) and on
	// (truncation + dedup), for the first read and for one repeat.
	firstOff, firstOn   int
	repeatOff, repeatOn int
	// Whole session (first read + dedupRepeats repeats).
	sessionOff, sessionOn int
}

// measureDedup replays get_file_contents dedupRepeats+1 times in one
// session, first with response.dedup off (truncation only, #319's
// baseline) and then with it on, and reports the savings of the repeat
// reads.
func measureDedup(t *testing.T, bins binaries, cat *Catalog) dedupResults {
	t.Helper()
	var res dedupResults
	tool := LargeResultTools[0] // github.get_file_contents

	run := func(extra string) (first int, repeats []int) {
		e := newEnv(t, bins, catalogServers(cat, "--large-results"), extra)
		p := e.startProxy(t, bins)
		defer p.stop()
		// No warm-up call here (unlike measureGovernor): with dedup on, a
		// throwaway call to the same tool would itself count as the
		// "first" occurrence, so the very first real call would already
		// look like a repeat.
		r0, _, err := p.call("tools/call", invokeParams(tool.Server, tool.Tool, map[string]interface{}{}), callTimeout)
		if err != nil || r0.msg.Error != nil {
			t.Fatalf("first read %s/%s: %v %.300s", tool.Server, tool.Tool, err, r0.line)
		}
		first = tokens(r0.line)
		for i := 0; i < dedupRepeats; i++ {
			r, _, err := p.call("tools/call", invokeParams(tool.Server, tool.Tool, map[string]interface{}{}), callTimeout)
			if err != nil || r.msg.Error != nil {
				t.Fatalf("repeat read %s/%s: %v %.300s", tool.Server, tool.Tool, err, r.line)
			}
			repeats = append(repeats, tokens(r.line))
		}
		return first, repeats
	}

	firstOff, repeatsOff := run(governorBlock) // #319 baseline: truncation, no dedup
	firstOn, repeatsOn := run(dedupBlock)

	res.firstOff, res.firstOn = firstOff, firstOn
	res.repeatOff, res.repeatOn = repeatsOff[0], repeatsOn[0]
	res.sessionOff = firstOff
	res.sessionOn = firstOn
	for _, r := range repeatsOff {
		res.sessionOff += r
	}
	for _, r := range repeatsOn {
		res.sessionOn += r
	}
	return res
}

// dedupAssertions are the dedup harness assertions (#321).
func dedupAssertions(d dedupResults) []assertion {
	var out []assertion
	saving := 0.0
	if d.sessionOff > 0 {
		saving = 100 * (1 - float64(d.sessionOn)/float64(d.sessionOff))
	}
	out = append(out, assertion{
		name:     "In-session dedup: repeated-reads session saves ≥ 30% of session tokens over truncation alone",
		measured: fmt.Sprintf("%d → %d tokens (−%.1f%%)", d.sessionOff, d.sessionOn, saving),
		pass:     saving >= 30,
		detail:   fmt.Sprintf("%d → %d", d.sessionOff, d.sessionOn),
	})
	repeatSaving := 0.0
	if d.repeatOff > 0 {
		repeatSaving = 100 * (1 - float64(d.repeatOn)/float64(d.repeatOff))
	}
	out = append(out, assertion{
		name:     "In-session dedup: a repeat of an already-seen result is mostly a stub",
		measured: fmt.Sprintf("%d → %d tokens (−%.1f%%)", d.repeatOff, d.repeatOn, repeatSaving),
		pass:     repeatSaving >= 90,
		detail:   fmt.Sprintf("%d → %d", d.repeatOff, d.repeatOn),
	})
	return out
}

// renderDedup is the dedup section of bench-results/harness.md.
func renderDedup(d dedupResults) string {
	var b strings.Builder
	w := func(format string, args ...interface{}) { fmt.Fprintf(&b, format, args...) }
	w("\n## In-session dedup (repeated reads, #321)\n\n")
	w("The same file (`github.get_file_contents`) read %d times in one session over `catalogmcp --large-results`: `response: {enabled: true, max_tokens: 4000}` (truncation only, #319's baseline) vs the same plus `dedup: on`. Tokens are those of the whole JSON-RPC response line.\n\n", dedupRepeats+1)
	w("| Read | Dedup off | Dedup on | Savings |\n|---|---:|---:|---:|\n")
	w("| First (not yet seen) | %d | %d | %s |\n", d.firstOff, d.firstOn, pct(d.firstOn, d.firstOff))
	w("| Repeat (already seen this session) | %d | %d | %s |\n", d.repeatOff, d.repeatOn, pct(d.repeatOn, d.repeatOff))
	w("| **Whole session (1 read + %d repeats)** | **%d** | **%d** | **%s** |\n", dedupRepeats, d.sessionOff, d.sessionOn, pct(d.sessionOn, d.sessionOff))
	return b.String()
}
