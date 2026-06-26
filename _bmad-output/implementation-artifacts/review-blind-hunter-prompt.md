# Blind Hunter — Code Review

**Instructions:** You receive ONLY the code diff below. No spec, no project context, no access to source files. Review the code changes critically from first principles.

**Focus areas:**
- Logic errors, race conditions, nil pointer dereferences
- Incorrect error handling (unchecked errors, swallowed errors, wrong error wrapping)
- Resource leaks (file handles, goroutines, connections)
- Security issues (command injection, path traversal, hardcoded secrets)
- Incorrect use of Go stdlib patterns (context cancellation, sync primitives, interface contracts)
- Deadlocks, data races, unbounded goroutine launches
- Incorrect assumptions about data types (type confusion, overflow)
- Broken edge cases at boundaries (empty slices, nil maps, zero values)

**Output format:** Markdown list. Each finding: category tag, one-line title, file:line reference, evidence quote from the diff, and brief explanation.

```
- [bug] Title of the issue
  File: path/file.go:123
  Evidence: ```go
  suspect code line(s)
  ```
  Explanation: ...
```

---

**Diff file:** `_bmad-output/implementation-artifacts/story-11.2-review-diff.txt`

Paste the contents of that file below this line and run your review.
