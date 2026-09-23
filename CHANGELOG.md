# Changelog

## Removed in v0.10

Epic 19 (["Make the README true"](https://github.com/mmornati/leanproxy-mcp/issues/288)) audited every
package and README/docs claim against what is actually wired into a command. The following features were
never wired into any command — they existed as code and/or config keys with no non-test callers — and were
removed rather than left to bit-rot. See issue [#303](https://github.com/mmornati/leanproxy-mcp/issues/303)
for the full audit.

- **Budget Management** (`pkg/budget`, `pkg/webhook`, `docs/budget.md`) — per-team/project token budgets
  with hard/soft caps and webhook alerts. LeanProxy does not pursue team budgets; LiteLLM, Portkey and the
  enterprise gateways own that.
- **Federation** (`pkg/federation`, the `federation:` config block) — multi-org peer routing. LeanProxy
  never saw LLM traffic across peers and the feature was never wired in. Existing configs that still set
  `federation:` keep loading; a single startup warning notes the key is ignored.
- **Model Routing** (`pkg/modelrouter`, `--model-router` / `--model-router-config`) — per-tool LLM model
  selection by complexity tier. LeanProxy does not route LLM traffic and never pursued this; LiteLLM,
  Portkey and the enterprise gateways own it. The flags are still accepted for this one release and now
  print a deprecation warning instead of doing nothing silently; they will be removed in a future release.
- **Lazy tool-schema loading** (`optimization.lazy_loading`, `Handler.EnableLazyLoading`, the
  `get_tool_schema` RPC method) — `EnableLazyLoading` was never called from any command, so the feature was
  always off. Existing configs that still set `optimization.lazy_loading:` keep loading; a single startup
  warning notes the key is ignored. Tool discovery is unaffected — it goes through the 3-tool gateway
  (`list_servers` / `list_tools` / `invoke_tool`), which was always the live path.
- **MLX sidecar** (`pkg/sidecar/mlx.go`, build tag `mlx`) — an Apple Silicon placeholder that never
  performed real inference (`Redact` returned a constant placeholder string). The Ollama sidecar is
  unaffected.
- **Duplicate/dead connection-pooling code**: `pkg/connpool` (an unused duplicate of `pkg/pool`),
  `pkg/proxy/socket` (an unused Unix-socket transport with a non-constant-time token comparison and a
  chmod-after-listen race), `pkg/proxy/jit.go`, `pkg/proxy/session.go`, `pkg/proxy/http.go`,
  `pkg/proxy/health_monitor.go`'s `HealthMonitor`/`ManagedServer` machinery and `pkg/proxy/process_health.go`
  (all definitions with no non-test callers), and `concurrent.Batcher` plus the unused `WorkerPool` wiring
  inside `pkg/pool`.
- **`cmd/serve.go`'s `handleSingleRequest` / `handleBatchRequest`** — dead synchronous copies of the live
  `handleSingleRequestAsync` / `handleBatchRequestAsync` path, kept alive only by their own tests. The tests
  now exercise the async functions directly.
- **Shadow Manifesting** README/docs claim — `utils.ManifestMerger` was never called from any command, so
  the claim that LeanProxy "merges global and project-local MCP configurations" was removed. LeanProxy does
  not auto-load project-local `.mcp.json` without an explicit trust prompt.

None of the above had any effect on a running LeanProxy instance before this release; removing them only
shrinks the binary and the surface future changes have to reason about.
