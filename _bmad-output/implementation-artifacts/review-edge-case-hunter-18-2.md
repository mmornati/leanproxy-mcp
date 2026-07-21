# Edge Case Hunter — Prompt

Invoke the `bmad-review-edge-case-hunter` skill on this diff for story 18-2 (Per-server / per-tool drill-down).

Same diff content as the Blind Hunter prompt (see `review-blind-hunter-18-2.md` for full diff).

Key areas to examine:
1. `TrackAt()` vs `TrackWithPromptHash()` — code duplication, locking patterns, state consistency
2. `promptHash()` — collision risk with 8-byte truncation
3. `GetPromptHashesForServerTool()` — ignores both parameters, returns all hashes
4. `ServerToolPromptHashes()` — ignores serverName/toolName parameters, returns all hashes
5. `parseSinceParam()` — time.Now() dependency, DST handling
6. `handleToolPrompts()` — silently ignores query unescape errors (`_`)
7. Thread safety of `callLog` slice growth
8. Empty server name or tool name in URL path routing
9. `GetEntries` — nil vs empty slice semantics
10. Cron trigger every 5s for both cards and server table (duplicate polling)
