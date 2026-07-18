# Blind Hunter — Adversarial Review
## Target: pkg/bouncer/injection/ — Story 13-1

Review the following files for logical errors, security flaws, correctness issues, and design weaknesses in the local prompt-injection classifier.

### classifier.go
- NewClassifier() deep-copies default patterns
- Classify() is RLock-protected, copies patterns before iteration
- SetPatterns() replaces the entire pattern slice (mutation without deep-copy)
- AddPattern/RemovePattern with mutex, but AddPattern skips compile validation on update path
- IsInjection() uses >= threshold (not >)
- Classify: empty payload early return; riskScore capped at 100

### patterns.go
- 14 default PatternDefs compiled in init()
- separator-injection pattern uses (?m) multiline + ^ anchor — may be fragile
- Compile() sets default weight 50 when weight <= 0
- PatternDef.Pattern is raw regex string — no validation at parse time

### config.go
- LoadConfig: threshold clamped [0→70, >100→100]
- BuildClassifier: logs but continues on invalid custom patterns
- Enabled field is read but classifier is always built (even when disabled)

### patterns_default.yaml
- Mirror of DefaultPatternDefs in YAML
- Escaped regex strings

### Test files
- Thread safety test (TestClassifier_ThreadSafePatterns) — uses 3 goroutines, 100 iterations each, no race detection
- Multiple pattern tests assert >= weight but don't check exact score
