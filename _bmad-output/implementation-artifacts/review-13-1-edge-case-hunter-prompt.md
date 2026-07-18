# Edge Case Hunter — Boundary Analysis
## Target: pkg/bouncer/injection/ — Story 13-1

Analyze all boundary conditions and edge cases:

1. Empty/malformed inputs: "", very long payloads (>1MB), unicode/punycode, null bytes
2. Regex edge cases: catastrophic backtracking, overlapping matches, RE2 compatibility
3. Thread safety: concurrent Classify + SetPatterns, concurrent RemovePattern + Classify with race detection
4. Pattern lifecycle: AddPattern with name collision (already handled), RemovePattern on already-removed, Disable+Remove
5. Config: threshold=0, threshold negative, threshold >100, YAML with unexpected fields
6. Score accumulation: overflow on totalWeight (int on 64-bit is safe, but 32-bit?), exact boundary at 100
7. YAML: empty file, no custom_patterns key, all fields null
8. separator-injection: multiline regex with (?m) — does multiline flag work as expected with \n at end?
