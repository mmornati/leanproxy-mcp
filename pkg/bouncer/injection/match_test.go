package injection

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The trigger prefilter and the windows must never change what a pattern
// matches: compare with a plain regex run over the whole normalized text,
// for every corpus entry, alone and buried in long filler text.
func TestWindowedMatching_EquivalentToFullRegex(t *testing.T) {
	c := NewClassifier()
	filler := strings.Repeat("The proxy relays each call to the upstream server and returns its answer.\n", 12)
	rng := rand.New(rand.NewSource(1))
	corpus := loadCorpus(t)
	texts := make([]string, 0, 4*len(corpus))
	for _, e := range corpus {
		texts = append(texts, e.Payload)
		cut := rng.Intn(len(filler))
		texts = append(texts, filler[:cut]+e.Payload+filler[cut:], filler+e.Payload, e.Payload+" "+filler)
	}
	for _, text := range texts {
		norm := []byte(Normalize(text))
		sc := acquireScratch(len(c.patterns))
		c.index.scan(norm, sc.occ)
		for i, p := range c.patterns {
			full := p.Pattern.Match(norm)
			windowed := p.matches(norm, sc.occ[i])
			if full != windowed {
				t.Errorf("pattern %s: full regex %v, windowed %v, on %q", p.Name, full, windowed, trim(text))
			}
			if full && len(sc.occ[i]) == 0 {
				t.Errorf("pattern %s matched without any trigger (triggers %v) on %q", p.Name, p.Triggers, trim(text))
			}
		}
		releaseScratch(sc)
	}
}

func trim(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// patterns_default.yaml documents the built-in set: keep it in sync.
func TestPatternsDefaultYAML_InSync(t *testing.T) {
	data, err := os.ReadFile("patterns_default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc PatternConfig
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.CustomPatterns) != len(DefaultPatternDefs) {
		t.Fatalf("yaml has %d patterns, code %d", len(doc.CustomPatterns), len(DefaultPatternDefs))
	}
	for i, want := range DefaultPatternDefs {
		got := doc.CustomPatterns[i]
		if got.Name != want.Name || got.Pattern != want.Pattern || got.Weight != want.Weight ||
			got.Enabled != want.Enabled || strings.Join(got.Triggers, "|") != strings.Join(want.Triggers, "|") || strings.Join(got.Requires, "|") != strings.Join(want.Requires, "|") {
			t.Errorf("pattern %d (%s) differs between patterns_default.yaml and DefaultPatternDefs", i, want.Name)
		}
	}
}

// Custom patterns without triggers run over the whole text; with triggers
// they are prefiltered.
func TestCustomPatternTriggers(t *testing.T) {
	c := NewClassifier()
	if err := c.AddPattern(PatternDef{Name: "acme", Pattern: `acme\s+override`, Weight: 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPattern(PatternDef{Name: "acme2", Pattern: `zeta\s+omega`, Weight: 20, Enabled: true, Triggers: []string{"ZETA"}}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("filler text ", 200)
	if got := c.Classify(long + "ACME   override" + long).RiskScore; got != 60 {
		t.Fatalf("custom pattern without triggers: risk %d", got)
	}
	if got := c.Classify(long + "Zeta omega" + long).RiskScore; got != 20 {
		t.Fatalf("custom pattern with triggers: risk %d", got)
	}
	if !c.RemovePattern("acme") || c.Classify("acme override").RiskScore != 0 {
		t.Fatal("removed pattern still matches")
	}
}

// Real documents that are not attacks: the Go standard library sources
// shipped with the toolchain running the test (code and doc comments). None
// may reach the default response threshold.
func TestClassify_GoSourceHasNoFalsePositives(t *testing.T) {
	if testing.Short() {
		t.Skip("scans the Go standard library")
	}
	root := filepath.Join(goroot(t), "src")
	c := NewClassifier()
	var files, flaggedAny int
	for _, dir := range []string{"net/http", "encoding/json", "os", "fmt", "strings", "io", "text/template", "crypto/tls"} {
		matches, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
		for _, f := range matches {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			files++
			res := c.Classify(string(data))
			if res.RiskScore > 0 {
				flaggedAny++
			}
			if res.RiskScore >= DefaultThreshold {
				t.Errorf("%s scored %d %s", f, res.RiskScore, matchNames(res))
			}
		}
	}
	if files == 0 {
		t.Skip("Go sources not available")
	}
	t.Logf("Go standard library: %d files, %d with any match, 0 at or above %d", files, flaggedAny, DefaultThreshold)
}

func goroot(t *testing.T) string {
	t.Helper()
	if g := os.Getenv("GOROOT"); g != "" {
		return g
	}
	return runtimeGOROOT()
}

// The built-ins are compiled without (?i) (foldFree): on normalized text
// they must match exactly what the documented (?i) patterns match.
func TestFoldFreeDefaults_Equivalent(t *testing.T) {
	texts := []string{strings.Repeat("filler ", 50)}
	for _, e := range loadCorpus(t) {
		texts = append(texts, e.Payload)
	}
	for _, def := range DefaultPatternDefs {
		if strings.ContainsAny(strings.NewReplacer(`\S`, "", `\W`, "", `\B`, "", `\D`, "").Replace(def.Pattern[5:]), "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("%s has an upper-case literal: it cannot drop (?i)", def.Name)
		}
		withFold := regexpMustCompile(t, def.Pattern)
		without := regexpMustCompile(t, foldFree(def.Pattern))
		for _, text := range texts {
			n := []byte(Normalize(text))
			if withFold.Match(n) != without.Match(n) {
				t.Errorf("%s: (?i) changes the result on %q", def.Name, trim(text))
			}
		}
	}
}
