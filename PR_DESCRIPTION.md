# Fix: per-server `timeout` in `leanproxy_servers.yaml` was silently ignored (always 30s)

## Summary

A `timeout: 60s` entry on a `stdio` server stanza (e.g. `garmin` returning a
large FIT-decoded payload) was completely ignored end-to-end. Every request
was dispatched with a hardcoded **30s** timeout regardless of the YAML, so
slow/large responses always failed with `request timeout after 30s`. This
PR makes the per-server `timeout` value the source of truth, end to end.

Three stacked bugs were hiding behind the visible symptom. The fix touches
all three so the contract documented in `docs/configuration.md` (`server.timeout` /
`servers[].timeout`) finally holds.

---

## Why the user's symptom (`request timeout after 30s`, garmin)

The user's stack:

```
opencode  →  leanproxy-mcp (server run --stdio)  →  garmin-mcp stdio  →  Garmin API
```

1. `cmd/server.go:runServerRun` constructs the handler via
   `mcp.NewHandlerWithToolStore(unifiedPool, slog.Default(), cache)`. Both
   constructors in `pkg/mcp/handlers.go` hardcoded `timeout: 30 * time.Second`
   with no setter and no map.
2. `pkg/mcp/handlers.go:handleToolsCall` calls
   `h.pool.SendRequestToServer(ctx, serverName, MethodToolsCall, paramsBytes, h.timeout)`
   — so every tool call inherited the 30s default.
3. The pool goroutine in `pkg/pool/server.go:sendRequest` then enforced
   that 30s with `time.NewTimer(timeout)`, finally returning
   `pool: request timeout after 30s ...` (or `request timeout after 30s`
   from `pkg/pool/pool.go:548` depending on which branch fired first).

The per-server `ServerConfig.TimeoutValue` (parsed from `timeout:` in YAML
at `pkg/migrate/config.go:241-249`, defaulting to 30s) was correctly
propagated into the per-server pool config (`pkg/pool/pool.go:198`) and
the worker's own `requestTimeout` field (`pkg/pool/server.go:160-163`).
But the handler never knew about it — it always passed the hardcoded 30s
as `req.Timeout`, and `sendRequest` had a second bug that **overrode** the
worker's own `s.requestTimeout` with whatever `req.Timeout` was:

```go
timeout := s.requestTimeout      // 60s for garmin — correct
if req.Timeout > 0 {
    timeout = req.Timeout        // 30s from handler — overrides per-server!
}
```

Even if the handler had been fixed, this override would have masked the
fix for the same reason. So both layers had to change.

Finally, in the unrelated `cmd serve` proxy path, the global
`ServeConfig.RequestTimeout` was dead code: `SetConfig` was only ever
called from tests, and `runServe` never wired the loaded YAML into it.
The four `handle*Request*` handlers all read `GetConfig().RequestTimeout`
which was hard-locked at 30s. The proxy had no way to express different
timeouts per server. Removed entirely in this PR.

---

## Changes

### Production

- **`pkg/mcp/handlers.go`** — `Handler` now carries a `timeouts map[string]time.Duration`
  + a `defaultTimeout` (was: a single `timeout` field). New API:
  - `SetTimeout(serverName string, timeout time.Duration)` — register / clear
    a per-server timeout (zero clears).
  - `SetDefaultTimeout(timeout time.Duration)` — override the fallback
    (non-positive values are ignored).
  - `timeoutFor(serverName string) time.Duration` — internal helper that
    returns the per-server value or the default.
  - Both `tools/call` paths (the regular `handleToolsCall` at line 327
    and `handleInvokeTool` at line 783) now dispatch with
    `h.timeoutFor(serverName)`.

- **`cmd/server.go`** — `runServerRun` populates the handler with the
  per-server timeouts from the loaded YAML right after constructing it:

  ```go
  for _, srv := range cfg.Servers {
      if srv.TimeoutValue > 0 {
          handler.SetTimeout(srv.Name, srv.TimeoutValue)
      }
  }
  ```

- **`pkg/pool/server.go`** — `sendRequest` now uses the documented
  `min(per-server, caller)` policy:

  ```go
  timeout := s.requestTimeout
  if req.Timeout > 0 && req.Timeout < timeout {
      timeout = req.Timeout
  }
  ```

  This preserves the contract that a per-server `timeout:` is honored even
  when the handler still passes a (now correct) global default, while
  letting a caller pass a tighter cap if it ever needs to.

- **`pkg/registry/registry.go`** — added `Timeout time.Duration` to
  `ServerEntry` with a doc comment explaining it is sourced from the
  `timeout` field of the server's stanza and consumed by the request
  layer.

- **`cmd/serve.go`** —
  - `runServe` now calls `serverReg.Register(...)` per server with the
    `Timeout` field populated (previously the registry was created but
    never populated; the router could not have resolved anything).
  - The four `handleSingleRequest*` / `handleBatchRequest*` handlers all
    read `serverTimeout(server)` instead of `GetConfig().RequestTimeout`.
  - `ServeConfig`, `serveConfig`, `GetConfig`, `SetConfig` deleted.
    `MaxBatchSize` is now the const `maxBatchSize = 100` (the only other
    knob on the old struct, never changed by anything except tests).

### Tests

- **`pkg/mcp/handlers_test.go`** —
  - `TestHandlerTimeoutFor_PerServer` — default fallback, per-server
    override wins, zero clears.
  - `TestHandlerSetDefaultTimeout` — default override, non-positive
    ignored, per-server still wins.
  - `TestHandlerToolsCallUsesPerServerTimeout` — end-to-end regression:
    drives `HandleRequest` and asserts the mock pool was called with
    `60 * time.Second` when garmin is configured with `60s`. **This is
    the regression test that would have caught the user's bug.**
  - `mockPool` gained a `sendRequestFunc` hook so tests can capture the
    `timeout` argument.

- **`pkg/pool/server_stdio_test.go`** — `TestSendRequestTimeout_MinOfServerAndCaller`
  covers the four branches of the `min()` policy (server-only, caller
  smaller wins, server smaller wins, equal).

- **`cmd/server_test.go`** — `TestServeConfig_GetSet` and
  `TestServeConfig_SetInvalid` removed (deleted API).

### Docs

- **`docs/configuration.md`** — the table entry for `timeout` was
  rewritten to clarify it is **per-server** (`servers[].timeout`) and that
  the value is honored end-to-end through the handler and the pool worker.

---

## Behavior change

For every configured server, the **default remains 30s** if the user did
not set `timeout:` (matches the old behavior). For every server that does
set `timeout:`, the new value flows through to both the handler dispatch
and the worker selection. The only observable change is that requests
that were timing out at 30s while the per-server value was higher now get
the full per-server budget.

---

## Verification

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `golangci-lint run ./...` — clean.
- `go test -race ./pkg/mcp/ ./pkg/pool/ ./cmd/ ./pkg/registry/` — pass.
- `go test ./...` (full suite) — pass.
- Manual smoke test against a fake slow stdio subprocess (see PR comments) —
  the request now dispatches with the configured `60s` and the worker
  honors it.

---

## Risk

Low. The change is:
- additive in `mcp.Handler` (new field + new setters; default keeps the
  30s behavior);
- a pure deletion + replace in `cmd/serve.go` for the dead global config;
- a tighter guard in `pkg/pool/server.go:sendRequest` that actually fixes
  a latent bug (worker was being silently overridden by the caller).

The only behavior the user should observe is that slow servers configured
with `timeout > 30s` now get the budget they asked for.

---

## Test report (self-test run on this branch)

A manual end-to-end smoketest (`tests/manual/slowstdio_smoketest.go`)
spawns the freshly-built `leanproxy-mcp server run --stdio` against a
fake stdio child that sleeps 45s on `tools/call`, configured with
`timeout: 60s`. The smoketest reads the JSON-RPC response and asserts
the request succeeded.

Run with `go run ./tests/manual/slowstdio_smoketest.go <binary-path>`.
Set `SMOKETEST_KEEP_TMPDIR=1` to inspect the temp dir on failure.

### Result against the user's existing binary (v0.9.0, pre-fix):

```
=== smoke test report ===
  t=   0.0s  initialize -> {"jsonrpc":"2.0","result":{"protocolVersion":"2024-11-05",...
  t=   0.0s  initialized
  t=   0.0s  tools/call sent — expecting fake to sleep 45s, leanproxy should NOT timeout at 30s
  t=  30.2s  tools/call response at t=30.2s (got=true)
  t=  30.2s  FAIL: leanproxy returned a timeout error: tool call failed: request timeout after 30s
  t=  30.3s  leanproxy exited
```

**This reproduces the user's exact symptom** (`request timeout after 30s`)
at the exact moment (t=30s).

### Result against the binary built from this PR:

```
=== smoke test report ===
  t=   0.0s  initialize -> {"jsonrpc":"2.0","result":{"protocolVersion":"2024-11-05",...
  t=   0.0s  initialized
  t=   0.0s  tools/call sent — expecting fake to sleep 45s, leanproxy should NOT timeout at 30s
  t=  45.1s  tools/call response at t=45.1s (got=true)
  t=  45.1s  OK: leanproxy returned a successful reply: {"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"slow reply"}]},"id":3}
  t=  45.1s  leanproxy exited

PASS: leanproxy-mcp honored the per-server 60s timeout.
The old 30s hardcoded default is gone.
```

The request waited the full 45s (the fake's sleep) and got a successful
reply — proving the per-server `60s` timeout is now honored end-to-end.

The user can re-run this test against their installation at any time:

```bash
go run ./tests/manual/slowstdio_smoketest.go /Users/mmornati/go/bin/leanproxy-mcp
```

### Deploy notes

Built and deployed to `/Users/mmornati/go/bin/leanproxy-mcp` (the
binary opencode invokes). The previous binary is at
`/Users/mmornati/go/bin/leanproxy-mcp.bak.<timestamp>`. The
old `leanproxy-mcp` process was confirmed exited before the rebuild —
no restart required from the operator side beyond the standard
opencode-managed lifecycle.

