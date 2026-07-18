package injection

import (
	"fmt"
	"log/slog"
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
}

func NewClassifier() *Classifier {
	patterns := make([]*InjectionPattern, len(defaultPatterns))
	for i, p := range defaultPatterns {
		cp := *p
		patterns[i] = &cp
	}
	return &Classifier{
		patterns: patterns,
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
			return nil
		}
	}
	c.patterns = append(c.patterns, p)
	return nil
}

func (c *Classifier) RemovePattern(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.patterns {
		if p.Name == name {
			c.patterns = append(c.patterns[:i], c.patterns[i+1:]...)
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

func (c *Classifier) Classify(payload string) Result {
	if payload == "" {
		return Result{RiskScore: 0, Payload: payload}
	}

	c.mu.RLock()
	patterns := make([]*InjectionPattern, len(c.patterns))
	copy(patterns, c.patterns)
	c.mu.RUnlock()

	var matches []Match
	totalWeight := 0

	for _, p := range patterns {
		if !p.Enabled.Load() {
			continue
		}
		if p.Pattern.MatchString(payload) {
			matches = append(matches, Match{
				PatternName: p.Name,
				Weight:      p.Weight,
				Description: p.Description,
			})
			totalWeight += p.Weight
		}
	}

	if len(matches) == 0 {
		return Result{RiskScore: 0, Payload: payload}
	}

	riskScore := totalWeight
	if riskScore > 100 {
		riskScore = 100
	}

	return Result{
		RiskScore: riskScore,
		Matches:   matches,
		Payload:   payload,
	}
}

func (c *Classifier) IsInjection(payload string, threshold int) bool {
	result := c.Classify(payload)
	return result.RiskScore >= threshold
}

func (c *Classifier) SetPatterns(patterns []*InjectionPattern) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.patterns = patterns
}
