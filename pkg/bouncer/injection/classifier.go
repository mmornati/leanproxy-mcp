package injection

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

type Match struct {
	PatternName string `json:"pattern_name"`
	Weight      int    `json:"weight"`
	Description string `json:"description,omitempty"`
}

type Result struct {
	RiskScore int     `json:"risk_score"`
	Matches   []Match `json:"matches,omitempty"`
	Payload   string  `json:"payload"`
}

type Classifier struct {
	mu       sync.RWMutex
	patterns []*InjectionPattern
	// index finds the trigger occurrences of every pattern in one pass;
	// rebuilt (under mu) whenever the pattern list changes.
	index *triggerIndex
}

func NewClassifier() *Classifier {
	patterns := make([]*InjectionPattern, len(defaultPatterns))
	for i, p := range defaultPatterns {
		np := &InjectionPattern{
			Name:        p.Name,
			Pattern:     p.Pattern,
			Weight:      p.Weight,
			Description: p.Description,
			Triggers:    p.Triggers,
			Requires:    p.Requires,
			window:      p.window,
		}
		np.Enabled.Store(p.Enabled.Load())
		patterns[i] = np
	}
	return &Classifier{
		patterns: patterns,
		index:    buildTriggerIndex(patterns),
	}
}

func NewClassifierWithCustom(defs []PatternDef) (*Classifier, error) {
	c := NewClassifier()
	for _, def := range defs {
		p, err := def.Compile()
		if err != nil {
			slog.Warn("injection: invalid custom pattern, skipping",
				"name", def.Name,
				"error", err)
			continue
		}
		c.patterns = append(c.patterns, p)
	}
	c.index = buildTriggerIndex(c.patterns)
	return c, nil
}

func (c *Classifier) Patterns() []*InjectionPattern {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*InjectionPattern, len(c.patterns))
	copy(result, c.patterns)
	return result
}

func (c *Classifier) AddPattern(def PatternDef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, err := def.Compile()
	if err != nil {
		return fmt.Errorf("add pattern: %w", err)
	}
	for i, existing := range c.patterns {
		if existing.Name == p.Name {
			c.patterns[i] = p
			c.index = buildTriggerIndex(c.patterns)
			return nil
		}
	}
	c.patterns = append(c.patterns, p)
	c.index = buildTriggerIndex(c.patterns)
	return nil
}

func (c *Classifier) RemovePattern(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.patterns {
		if p.Name == name {
			c.patterns = append(c.patterns[:i:i], c.patterns[i+1:]...)
			c.index = buildTriggerIndex(c.patterns)
			return true
		}
	}
	return false
}

func (c *Classifier) EnablePattern(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.patterns {
		if p.Name == name {
			p.Enabled.Store(true)
			return true
		}
	}
	return false
}

func (c *Classifier) DisablePattern(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.patterns {
		if p.Name == name {
			p.Enabled.Store(false)
			return true
		}
	}
	return false
}

// Classify normalizes payload (see Normalize) and scores it. The returned
// Result carries the original payload.
func (c *Classifier) Classify(payload string) Result {
	if payload == "" {
		return Result{RiskScore: 0, Payload: payload}
	}
	n := acquireNormalizer(false)
	n.addString(payload, 0)
	res := c.ClassifyNormalized(n.out)
	releaseNormalizer(n)
	res.Payload = payload
	return res
}

// ClassifyNormalized scores text that is already normalized (the output of
// Normalize or of a TextBuilder). Result.Payload is left empty.
func (c *Classifier) ClassifyNormalized(text []byte) Result {
	if len(text) == 0 {
		return Result{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	sc := acquireScratch(len(c.patterns))
	defer releaseScratch(sc)
	c.index.scan(text, sc.occ)

	for i, p := range c.patterns {
		if p.Enabled.Load() && p.matches(text, sc.occ[i]) {
			sc.hits = append(sc.hits, i)
		}
	}
	if len(sc.hits) == 0 {
		return Result{}
	}
	matches := make([]Match, len(sc.hits))
	totalWeight := 0
	for k, i := range sc.hits {
		p := c.patterns[i]
		matches[k] = Match{PatternName: p.Name, Weight: p.Weight, Description: p.Description}
		totalWeight += p.Weight
	}
	if totalWeight > 100 {
		totalWeight = 100
	}
	return Result{RiskScore: totalWeight, Matches: matches}
}

// MatchSpans appends to dst the byte ranges of normalized text matched by
// the enabled patterns that contribute to the risk score (weight > 0).
// Ranges are sorted by start and merged when they overlap.
func (c *Classifier) MatchSpans(text []byte, dst [][2]int) [][2]int {
	if len(text) == 0 {
		return dst
	}
	base := len(dst)
	c.mu.RLock()
	sc := acquireScratch(len(c.patterns))
	c.index.scan(text, sc.occ)
	for i, p := range c.patterns {
		if p.Weight <= 0 || !p.Enabled.Load() {
			continue
		}
		dst = p.appendMatches(text, sc.occ[i], dst)
	}
	releaseScratch(sc)
	c.mu.RUnlock()
	return mergeSpans(dst, base)
}

// mergeSpans sorts dst[base:] and merges overlapping or touching ranges.
func mergeSpans(dst [][2]int, base int) [][2]int {
	spans := dst[base:]
	if len(spans) < 2 {
		return dst
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	out := spans[:1]
	for _, sp := range spans[1:] {
		last := &out[len(out)-1]
		if sp[0] <= last[1] {
			if sp[1] > last[1] {
				last[1] = sp[1]
			}
			continue
		}
		out = append(out, sp)
	}
	return dst[:base+len(out)]
}

func (c *Classifier) IsInjection(payload string, threshold int) bool {
	result := c.Classify(payload)
	return result.RiskScore >= threshold
}

func (c *Classifier) SetPatterns(patterns []*InjectionPattern) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.patterns = patterns
	c.index = buildTriggerIndex(patterns)
}
