package injection

import (
	"sort"
	"strings"
	"testing"
)

// defaultResponseThreshold is the risk from which the default response
// policy annotates a tool output (DefaultConfig().Threshold).
var defaultResponseThreshold = DefaultConfig().Threshold

// TestClassify_ResponseCorpus measures the classifier on the response side
// of the corpus (#315): indirect prompt injection planted in web pages,
// issues, e-mails, READMEs and code, against benign documents of the same
// kinds. It reports precision / recall at the default response threshold
// (the risk from which the default policy annotates) and at any score, and
// gates on: every malicious sample scores at least its expected_risk_min,
// no benign sample reaches the threshold.
func TestClassify_ResponseCorpus(t *testing.T) {
	corpus := corpusByDirection(t, "response")
	if len(corpus) < 100 {
		t.Fatalf("response corpus too small: %d entries", len(corpus))
	}
	c := NewClassifier()

	type counts struct{ tp, fn, fp, tn int }
	var atThreshold, atAny counts
	var belowMin, fps, lowFPs []string
	bySource := map[string]*counts{}
	for _, e := range corpus {
		res := c.Classify(e.Payload)
		src := bySource[e.Source]
		if src == nil {
			src = &counts{}
			bySource[e.Source] = src
		}
		hit := res.RiskScore >= defaultResponseThreshold
		any := res.RiskScore > 0
		label := e.Notes + " (" + e.Source + ")"
		if e.ShouldDetect {
			if res.RiskScore < e.ExpectedRiskMin {
				belowMin = append(belowMin, label+": risk "+itoa(res.RiskScore)+" < "+itoa(e.ExpectedRiskMin)+" "+matchNames(res))
			}
			if hit {
				atThreshold.tp++
				src.tp++
			} else {
				atThreshold.fn++
				src.fn++
			}
			if any {
				atAny.tp++
			} else {
				atAny.fn++
			}
			continue
		}
		if hit {
			atThreshold.fp++
			src.fp++
			fps = append(fps, label+": risk "+itoa(res.RiskScore)+" "+matchNames(res))
		} else {
			atThreshold.tn++
			src.tn++
		}
		if any {
			atAny.fp++
			if !hit {
				lowFPs = append(lowFPs, label+": risk "+itoa(res.RiskScore)+" "+matchNames(res))
			}
		} else {
			atAny.tn++
		}
	}

	report := func(name string, k counts) {
		precision, recall := 100.0, 100.0
		if k.tp+k.fp > 0 {
			precision = 100 * float64(k.tp) / float64(k.tp+k.fp)
		}
		if k.tp+k.fn > 0 {
			recall = 100 * float64(k.tp) / float64(k.tp+k.fn)
		}
		fpr := 0.0
		if k.fp+k.tn > 0 {
			fpr = 100 * float64(k.fp) / float64(k.fp+k.tn)
		}
		t.Logf("response corpus %-22s precision %5.1f%%  recall %5.1f%%  false-positive rate %4.1f%%  (tp %d fn %d fp %d tn %d)",
			name, precision, recall, fpr, k.tp, k.fn, k.fp, k.tn)
	}
	t.Logf("response corpus: %d malicious, %d benign samples", atAny.tp+atAny.fn, atAny.fp+atAny.tn)
	report("at risk >= "+itoa(defaultResponseThreshold)+":", atThreshold)
	report("at any risk > 0:", atAny)
	sources := make([]string, 0, len(bySource))
	for s := range bySource {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	for _, s := range sources {
		k := bySource[s]
		t.Logf("  %-10s malicious detected %d/%d, benign flagged %d/%d", s, k.tp, k.tp+k.fn, k.fp, k.fp+k.tn)
	}
	for _, l := range lowFPs {
		t.Logf("  benign sample scored below the threshold (logged only): %s", l)
	}
	for _, l := range belowMin {
		t.Errorf("malicious sample under-scored: %s", l)
	}
	for _, l := range fps {
		t.Errorf("benign sample reaches the default threshold: %s", l)
	}
}

func matchNames(r Result) string {
	names := make([]string, len(r.Matches))
	for i, m := range r.Matches {
		names[i] = m.PatternName
	}
	return "[" + strings.Join(names, ",") + "]"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
