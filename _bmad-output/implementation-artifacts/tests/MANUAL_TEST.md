# Manual Test Plan — IDE Plugins (Stories 14-2 & 14-3)

These two stories ship IDE plugins that **cannot be exercised by the headless Go E2E suite** — both require a real IDE host (VS Code or JetBrains IDE) to drive activation, the status bar widget, the webview/tool-window panel, and the marketplace install flow.

Use this checklist when validating a release candidate that includes the `14-2` (VS Code) and/or `14-3` (JetBrains) story changes.

---

## Shared pre-requisites

Before either story is tested, prepare the local environment:

1. Build the proxy binary (or download a release artifact):
   ```bash
   make build
   cp dist/leanproxy-mcp tests/e2e/leanproxy-mcp   # if testing alongside the E2E suite
   ```
2. Start a minimal proxy with the metrics endpoint bound to a known port and one or two registered servers:
   ```yaml
   # leanproxy_servers.yaml
   servers: []
   metrics:
     bind: "127.0.0.1:9090"
   ```
   ```bash
   ./leanproxy serve --config leanproxy_servers.yaml
   ```
3. Confirm `/metrics` is reachable and returns the expected shape:
   ```bash
   curl -s http://127.0.0.1:9090/metrics | jq
   # → {"by_tool":[],"by_server":[],"total_spend":0,"top_5_expensive_tools":[]}
   ```
4. To exercise **disconnected** state for either story, stop the proxy with `Ctrl-C` (or `pkill leanproxy`) and observe the IDE behaviour.

---

## Story 14-2 — VS Code extension (TypeScript)

**Source:** [`_bmad-output/implementation-artifacts/14-2-vscode-extension.md`](14-2-vscode-extension.md)
**Extension path:** `extensions/vscode/`

### Acceptance criteria (recap)

| # | Criterion | How to verify manually |
|---|-----------|------------------------|
| AC1 | Status bar shows session cost, updates within a polling interval after each call. | Open VS Code → confirm a status bar item on the right (`$(pulse)` or `$(error)` icon) with text like `≈ $0.0000`. Drive a traffic-bearing server (or hand-edit `~/.leanproxy/metrics.json`) and confirm the value changes within 2 polls (default 2s). |
| AC2 | Click status bar → webview panel with per-tool breakdown. | Click the status item. The panel **LeanProxy** should open in a new editor tab. Confirm three tables are visible: **By Server**, **By Tool**, **Top 5 Most Expensive Tools**. |
| AC3 | Webview polls `/metrics` every 2s. | Open the webview's DevTools (`Help → Toggle Developer Tools → pick the webview process`). In the **Network** tab, verify requests to `http://127.0.0.1:9090/metrics` arrive at ≈2s intervals. |
| AC4 | LeanProxy offline → status bar shows disconnected icon + tooltip; webview shows "Proxy Offline". | Stop the proxy. Within one poll cycle the status icon should switch to `$(error)` with tooltip `disconnected`. Reopen the webview and confirm an empty/error card with text **Proxy Offline**. |
| AC5 | Installable from VS Code marketplace. | Run `npm run package` inside `extensions/vscode/`. (Known issue: see [Review Patch] missing `icon.png` below — packaging will fail until it is provided.) Successful packaging produces a `.vsix` that installs cleanly via `Extensions → ... → Install from VSIX`. |
| AC6 | First-run experience <60s. | Cold-start VS Code, install the `.vsix`, activate on a workspace. The status bar item should appear within 60s and the first `/metrics` fetch should complete. |

### Manual test steps

1. **Install dependencies**
   ```bash
   cd extensions/vscode
   npm ci
   ```
2. **Compile (TypeScript)**
   ```bash
   npm run compile
   npm run compile:webview   # webview must be built separately
   ```
   Expectation: 0 errors. (Debug Log: initial compile had 7 errors; current build should be clean.)
3. **Run unit tests**
   ```bash
   npm test
   ```
   Expectation: all Mocha tests pass — covers cost calculation, `MetricsSnapshot` parsing, config defaults, top-5 limiting.
4. **Launch in dev host**
   - Open `extensions/vscode/` in VS Code itself.
   - Press `F5` → "Run Extension" → a new VS Code window opens with the extension activated.
5. **Verify AC1 — status bar**
   - Look at the bottom-right status bar: a `$(pulse) ≈ $0.0000` item is visible.
   - Tail the proxy stderr; trigger a few MCP calls (or `curl` the proxy); confirm the cost value updates within ~2s.
6. **Verify AC2 — webview**
   - Click the status bar item. The webview opens.
   - Confirm three tables: **By Server**, **By Tool**, **Top 5 Most Expensive Tools**. If `/metrics` returned zero rows, the tables render with empty-state copy.
7. **Verify AC3 — polling cadence**
   - Webview DevTools → Network → filter by `metrics` → confirm 2s spacing.
8. **Verify AC4 — disconnected state**
   - `pkill leanproxy` in the host terminal. Status icon should flip to `$(error)`. Tooltip should read `disconnected`. Reopen the webview → it should display a **Proxy Offline** card.
9. **Verify AC6 — first-run**
   - Quit VS Code, delete `~/.config/Code/User/globalStorage/leanproxy*` (if present), relaunch, observe cold-start time.

### Known issues to expect during manual test

- **[Review][Patch] Missing `icon.png`** — `vsce package` will fail until an icon is added or the `icon` field is removed from `package.json`. **AC5 cannot be passed** without resolving this.
- **[Review][Patch] NaN crash in `updateStatusBar`** — if `total_spend` is negative, `toFixed(4)` throws `RangeError`. Manually inject `{"total_spend":-1}` into the metrics response and watch the status bar widget go blank.
- **[Review][Patch] XSS via `innerHTML`** — a server/tool name containing `<script>` will execute in the webview. Test by adding a server named `<img src=x onerror=alert(1)>` and confirm whether the image fires. **This is a security finding** — flag it on the review report.
- **[Review][Decision] <1s update latency vs 2s poll default** — current default exceeds the AC's <1s latency. Either reduce `leanproxy.pollInterval` to 500ms in settings before testing, or note the deviation in the report.
- **[Review][Defer] "Calls" column absent from webview** — `/metrics` doesn't expose call counts; belongs to story 14-1. Do not fail AC on missing this column.

### Pass/fail recording

Tick each AC above and capture in the test report:

- ✅ / ❌ per AC
- VS Code version, OS, and `leanproxy` commit hash
- Screenshots of the status bar (online + offline) and the webview tables
- Output of `npm test`
- Log of any review-finding reproductions

---

## Story 14-3 — JetBrains plugin (Kotlin)

**Source:** [`_bmad-output/implementation-artifacts/14-3-jetbrains-plugin.md`](14-3-jetbrains-plugin.md)
**Plugin path:** `extensions/jetbrains/`

### Acceptance criteria (recap)

| # | Criterion | How to verify manually |
|---|-----------|------------------------|
| AC1 | Status-bar widget shows session cost. | Open IntelliJ → bottom-right shows a widget reading `LeanProxy: $0.0000` (or similar). |
| AC2 | Click widget → opens **LeanProxy** tool window with Total Spend, By Server, By Tool, Top 5 tables. | Click the widget → the **LeanProxy** tool window opens in the editor area. |
| AC3 | Tool window polls `/metrics` at configurable interval. | Configure interval in `Settings → Tools → LeanProxy`. Watch the Network tab in the IDE's HTTP client logs (or use a local proxy like mitmproxy) to confirm cadence. |
| AC4 | LeanProxy offline → status bar shows disconnected state, tool window shows loading/error state. | Stop the proxy → widget text reverts to `disconnected`; tool window shows an error message. |
| AC5 | Configurable refresh interval and currency. | `Settings → Tools → LeanProxy` → change poll interval and currency symbol → values persist across restart (verify the **Persistence** caveat below). |
| AC6 | Published to JetBrains Marketplace. | Build with `./gradlew buildPlugin` → produces a zip. Upload to JetBrains Marketplace. Cannot be fully validated without a publisher token. |

### Manual test steps

1. **Install the Gradle IntelliJ Plugin toolchain**
   - JDK 17+
   - IntelliJ IDEA 2023.3+ (Community or Ultimate)
2. **Build the plugin**
   ```bash
   cd extensions/jetbrains
   ./gradlew buildPlugin
   ```
   Expectation: produces `build/distributions/leanproxy-jetbrains-*.zip`.
   (Known issue: see Review Patch — no `gradlew` is committed. If `./gradlew` is missing, install Gradle 8+ and run `gradle wrapper` first.)
3. **Install the plugin in a sandbox IDE**
   - `Settings → Plugins → ⚙ → Install Plugin from Disk…` → pick the zip → restart.
4. **Activate on a project** — open any project, the plugin auto-activates.
5. **Verify AC1 — status bar widget**
   - Bottom-right corner: a widget should be visible. **Note:** the review found a **HIGH** patch — `LeanProxyStatusBarWidgetFactory.createWidget()` does not call `start()`, so polling never begins. The widget likely displays `LeanProxy…` indefinitely. If you see this, record it as a reproduction and mark AC1 ❌.
6. **Verify AC2 — tool window**
   - Click the widget (or `View → Tool Windows → LeanProxy`). The tool window should open with four cards: **Total Spend**, **By Server**, **By Tool**, **Top 5 Most Expensive Tools**. The "Calls" column may be missing (deferred — see story 14-1).
7. **Verify AC3 — polling cadence**
   - With the tool window visible, watch for `/metrics` requests at the configured interval.
8. **Verify AC4 — disconnected state**
   - Stop the proxy. The widget should switch to a disconnected indicator. The tool window's status area should show an error.
9. **Verify AC5 — settings persistence**
   - Open `Settings → Tools → LeanProxy`. Change interval to 5s. Restart the IDE. **Note:** the review found a **HIGH** patch — `LeanProxySettings` is a volatile singleton, so values reset to defaults on every restart. If persistence fails, record and mark AC5 ❌.
   - Also verify the **Configurable UI** — the review notes no `Configurable` implementation; settings may not appear in the panel. If so, the AC is partially met.
10. **Verify AC6 — packaging**
    - `./gradlew buildPlugin` must succeed. Marketplace publish is out of scope for manual QA (deferred per review).

### Known issues to expect during manual test

- **HIGH — Status bar widget never starts polling** (`LeanProxyStatusBar.kt:18-20`). **AC1 will likely fail.** Reproduce by activating the plugin and waiting >30s; the widget will not update.
- **HIGH — Settings not persisted** (`Settings.kt:1-23`). **AC5 will fail** because there is no `PersistentStateComponent`. Every restart resets to defaults.
- **MEDIUM — No `Configurable` UI** (`Settings.kt`). The settings panel may not exist at all; if so, AC5 is unsatisfiable.
- **MEDIUM — Status bar click not wired** (`LeanProxyStatusBar.kt:49`). `getClickConsumer()` returns null; the widget does not respond to clicks. Use the menu (`View → Tool Windows → LeanProxy`) as a workaround for AC2.
- **MEDIUM — Lifecycle leaks** (`LeanProxyToolWindow.kt:156-158`). Polling continues after the panel is closed. Verify by closing the tool window and watching the network — requests should keep arriving.
- **MEDIUM — Misleading error messages** (`MetricsClient.kt:27,33`). All errors return "proxy offline" — both connection refused and HTTP 500. To distinguish, point the endpoint at an HTTP server that returns 500 and watch the message.
- **MEDIUM — Null JSON fields NPE** (`MetricsSnapshot.kt:3-18`). Send `{"by_tool":null,"by_server":null,"total_spend":null,"top_5_expensive_tools":null}` to the proxy and watch the tool window crash.
- **LOW — Unsafe cast** (`LeanProxyPlugin.kt:24`). May silently mis-render the widget on plugin update.
- **LOW — No `@Volatile` on mutable state** (`LeanProxyStatusBar.kt:30-33`). Race conditions possible under fast polling.
- **LOW — No input validation for cost values** (`Settings.kt:7`). A negative `tokenCostPer1000` will produce a negative cost display.

### Pass/fail recording

Tick each AC and capture:

- ✅ / ❌ per AC
- IntelliJ version, OS, Kotlin/Gradle version, `leanproxy` commit hash
- Screenshots of the status bar widget (online + offline) and the tool window tables
- Output of `./gradlew buildPlugin`
- Network capture of `/metrics` polling cadence
- Reproduction notes for every HIGH/MEDIUM review patch observed

---

## When to skip manual testing

Skip this checklist if the release notes do **not** include changes to `extensions/vscode/` or `extensions/jetbrains/`. The Go E2E suite already covers everything else.
