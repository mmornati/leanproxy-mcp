# Blind Hunter — Cynical Review Prompt

Invoke the `bmad-review-adversarial-general` skill on this diff for story 18-2 (Per-server / per-tool drill-down).

## Diff (working tree changes against HEAD 02acf6f)

```
_bmad-output/implementation-artifacts/18-2-drill-down.md      |  52 +++-
_bmad-output/implementation-artifacts/sprint-status.yaml      |   2 +-
pkg/dashboard/server.go                                        | 141 ++++++++++++++
pkg/dashboard/server_test.go                                   |  91 +++++++++
pkg/reporter/cost.go                                           | 209 +++++++++++++++++++--
pkg/reporter/cost_test.go                                      | 183 ++++++++++++++++++
6 files changed, 664 insertions(+), 14 deletions(-)
```

### pkg/reporter/cost.go changes:
- Added `import "crypto/sha256"`
- Added `GetEntries(since time.Time) []CallLogEntry` package-level function
- Modified `TrackCostFromStrings` to call `TrackWithPromptHash` instead of `Track`
- Added `promptHash()` helper (SHA-256 first 8 bytes hex)
- Added `ServerToolKey`, `ServerToolStat`, `CallLogEntry` structs
- Added `serverTool map[ServerToolKey]*ServerToolStat` and `promptHashes map[string]int64` and `callLog []CallLogEntry` fields to `CostTracker`
- Updated `NewCostTracker`/`newCostTracker` to init new fields
- Modified `Track()` to delegate to `TrackAt()`
- Added `TrackAt()` method (duplicates tracking logic with explicit timestamp)
- Added `TrackWithPromptHash()` method (duplicates tracking logic with hash storage)
- Added `NamedServerToolStat` struct
- Added `GetServerToolStats()`, `GetToolServerStats()`, `GetPromptHashes()`, `GetEntries()`, `GetPromptHashesForServerTool()` methods
- Updated `Reset()` to clear new fields

### pkg/reporter/cost_test.go changes:
- Added `TestCostTrackerTrackAt`, `TestCostTrackerTrackWithPromptHash`, `TestCostTrackerGetToolServerStats`, `TestCostTrackerGetServerToolStatsEmpty`, `TestCostTrackerPromptHash`, `TestCostTrackerGetPromptHashesForServerTool`, `TestCostTrackerGetEntries`, `TestCostTrackerGetEntriesZeroTime`, `TestCostTrackerGetEntriesEmpty`
- Added `contains` helper, `mockClock` struct

### pkg/dashboard/server.go changes:
- Added `"net/url"` import, `"github.com/mmornati/leanproxy-mcp/pkg/reporter"` import
- Added `//go:embed views/* var viewsFS embed.FS`
- Added `ServerRow` struct, `Servers []ServerRow` field to `DashboardData`
- Added drill-down CSS styles to index template
- Added server table section and drill-down container to index template
- Added `drilldownTemplates` variable (ParseFS views/drilldown.html)
- Added 3 route registrations: `GET /api/dashboard/servers`, `GET /api/dashboard/servers/{server}`, `GET /api/dashboard/servers/{server}/tools/{tool}/prompts`
- Added `tracker := reporter.GlobalCostTracker()` in `collectDashboardData`
- Added server row building in collectDashboardData
- Added `handleServerTable()`, `handleServerDrilldown()`, `handleToolPrompts()`, `parseSinceParam()` functions

### pkg/dashboard/server_test.go changes:
- Added `TestDashboardServerTableEndpoint`, `TestDashboardServerDrilldownEndpoint`, `TestDashboardServerDrilldownEndpointInvalid`, `TestDashboardToolPromptsEndpoint`

### Additional changed files (sprint status + story spec):
- Updated sprint status from "ready-for-dev" to "review"
- Updated story spec with completed tasks, debug log, file list, change log

## Context: Spec file

The spec is at `_bmad-output/implementation-artifacts/18-2-drill-down.md`. Acceptance criteria: Click server row → drill-down with tool name, call count, token count, avg tokens/call, last invoked; sorted by tokens desc. Date filter (last 7 days). 'Show prompts' (opt-in) → list of prompt hashes + cost; no prompt content, only hashes for privacy.
