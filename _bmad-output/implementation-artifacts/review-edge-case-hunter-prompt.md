# Edge Case Hunter — Code Review

**Instructions:** You receive the code diff below AND read access to the project repository. Review the code changes exhaustively, walking every branching path and boundary condition. Find only **unhandled** edge cases — conditions where the code fails, panics, deadlocks, leaks resources, or produces incorrect output at boundaries.

**Focus areas:**
- Empty/missing inputs (empty strings, nil slices, nil maps, zero values)
- Boundary values (min/max ints, empty config, single-element slices, timeouts at boundaries)
- Error paths (every `if err != nil` — is the error handled or ignored? wrapped or bare?)
- Concurrency (shared state without synchronization, channel operations near selects, context cancellation propagation)
- I/O edge cases (partial writes, disk full, file already exists, temp file cleanup failures)
- Type coercion (string trimming/casing, transport type normalization, YAML round-trips)
- Atomicity (partial updates, rename vs. concurrent reads, temp file leaks on crash)
- Permission edge cases (umask variations, read-only config dir, file ownership)
- State machine edge cases (already installed + dry-run + force, stopped + already exited, etc.)
- Testing edge cases (test helpers that can themselves fail, temp dir cleanup, environment variable interference)

**Output format:** Markdown list. Each finding: category tag, one-line title, file:line reference, evidence quote from the diff, and brief explanation.

```
- [edge] Title of the issue
  File: path/file.go:123
  Evidence: ```go
  suspect code line(s)
  ```
  Explanation: ...
```

If no edge cases found, output: "No unhandled edge cases identified."

---

**Diff file:** `_bmad-output/implementation-artifacts/story-11.2-review-diff.txt`

Paste the contents of that file below this line and run your review. Use your project read access to examine the full source context of any suspicious lines.
