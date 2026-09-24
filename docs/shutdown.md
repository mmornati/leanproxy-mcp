# Graceful Shutdown

This page describes what LeanProxy-MCP does when it stops: which requests
still get an answer, how upstream servers are stopped, and what is cleaned
up. The behaviour differs by front end.

## Summary

| | `server run --stdio` | `server run --http` | `serve` (deprecated) |
|---|---|---|---|
| Stopped by | `SIGINT`/`SIGTERM`, stdin EOF, a `shutdown` request | `SIGINT`/`SIGTERM` | `SIGINT`/`SIGTERM` |
| In-flight client requests | Signal: dropped. EOF or `shutdown`: up to **5 s** to finish, then canceled | Canceled at once; the HTTP server waits up to **5 s** for handlers to return | Dropped |
| Upstream stdio servers | `SIGTERM` to the process group, `SIGKILL` after **5 s** | same | same |
| Spilled results, status file | Removed | Removed | Removed |
| Exit status | `0` (`1` if reading stdin failed) | `0` | `0` |

## Stdio front end (`server run --stdio`)

This is the mode an IDE runs. It stops in three ways.

### The client closes stdin, or sends `shutdown`

This is the normal end of a session.

1. The proxy stops reading stdin.
2. Requests still running get up to **5 seconds** to finish. Their responses
   are written as usual.
3. Requests still running after 5 seconds are canceled. The proxy waits at
   most **2 more seconds** for them to return.
4. For a `shutdown` request, the proxy now answers it with
   `{"status": "shutdown"}`. The answer comes last, so the client knows every
   earlier request is done.
5. The upstream servers are stopped (see
   [Stopping upstream servers](#stopping-upstream-servers)), the usage data
   is flushed, and the status file is removed.
6. The process exits with status `0`.

`shutdown` is LeanProxy's own request, not part of the MCP specification.
Clients that never send it are fine: closing stdin has the same effect.

### `SIGINT` or `SIGTERM`

A signal does not wait for running requests. The proxy:

1. flushes the usage data and removes the status file;
2. stops the upstream servers;
3. deletes spilled results and flushes telemetry (at most 5 seconds);
4. exits with status `0`.

Requests that were running get no response.

## Streamable HTTP front end (`server run --http`)

The HTTP front end stops only on `SIGINT` or `SIGTERM`:

1. The usage data is flushed.
2. New sessions are refused. Every open session is ended: its running
   requests are canceled and its streams are closed.
3. The HTTP server stops accepting connections and waits up to **5
   seconds** for handlers to return, then closes what is left.
4. The status file is removed, the upstream servers are stopped, spilled
   results are deleted and telemetry is flushed.
5. The process exits with status `0`.

A client that sends `shutdown` over HTTP gets `{"status": "shutdown"}`, but
the proxy keeps running for its other clients. To end one session, the
client sends `DELETE` with its `Mcp-Session-Id`. Idle sessions end on their
own after `server.http.session_idle_timeout` (default `30m`).

## Deprecated line-TCP front end (`serve`)

On the first `SIGINT` or `SIGTERM`, `serve`:

1. flushes the usage data and stops the registry refresh and the health
   checker;
2. closes the dashboard and `/metrics` listeners and removes the status
   file;
3. stops the sidecar and closes the TCP listener;
4. stops the semantic cache, deletes spilled results and stops the upstream
   servers;
5. closes the vector store and flushes telemetry (at most 5 seconds);
6. exits with status `0`.

Open client connections are not drained. Later signals are ignored while it
shuts down. `SIGHUP` does not stop `serve`: it re-reads the
`--providers-config` file and rebuilds the redactor. `serve` does not stop
on a `shutdown` request.

## Stopping upstream servers

All front ends stop upstream servers the same way, in parallel:

- **Requests waiting on a stdio server fail at once** with
  `pool: server <name> is stopping`, instead of waiting for their timeout.
- **stdio servers** run in their own process group (Unix). The proxy sends
  `SIGTERM` to the whole group, so processes started by `npx`, `uvx`,
  `docker run` or `sh -c` stop too. A server that is still running after
  **5 seconds** gets `SIGKILL`. Any process left in its group at that
  point is killed as well.
- **Sandboxed servers**: the container is removed with `docker rm -f` (or
  `podman rm -f`), bounded to 5 seconds.
- **HTTP and SSE servers**: their connections are closed.
- The proxy waits at most **10 seconds** in total for all stdio servers to
  stop, then logs `timed out waiting for stdio servers to stop` and goes on.

On Windows, a server is killed at once; there is no process group or
`SIGTERM`.

The same stop sequence runs when a single server is stopped or restarted
while the proxy keeps running: after its `idle_timeout`, or after failed
health checks.

## What is cleaned up

| Item | Location | On shutdown |
|------|----------|-------------|
| Status file | `~/.config/leanproxy/status/current.json` | Removed. `status --running` then reports no running proxy |
| Spilled tool results (`response.spill`) | Memory, or `~/.leanproxy/results/<pid>-<id>/` with `spill.disk: true` | Deleted. If the process was killed, the directory is deleted by the next start once it is older than `spill.ttl` |
| Usage data for `report` | `~/.leanproxy/usage/` | Flushed to disk, kept |
| Tool cache, pins, quarantine | `~/.config/leanproxy/`, `~/.leanproxy/quarantine/` | Kept |
| Telemetry | OTLP collector | Pending spans and metrics are sent, at most 5 seconds |

`SIGKILL` on the proxy itself skips all of this. Its child servers get no
signal: most exit when their stdin closes. The status file stays until the
next start replaces it.

## For contributors

The shutdown order lives in `cmd/`:

- `cmd/server.go` (`runServerRun`) builds a `closePools` function that
  stops the background refresh and the health checker, then calls
  `StdioPool.Close()`, `HTTPClientPool.Close()`, `SSEPool.Close()`,
  `Governor.Close()` and the telemetry provider's `Shutdown`. It runs once,
  from the signal handler or after the front end returns.
- `cmd/stdio_frontend.go` (`serveStdio`) implements the drain:
  `defaultStdioShutdownGrace` (5 s), then cancel, then `stdioCancelWait`
  (2 s). The handler's own `shutdown` method (`pkg/mcp`) only acknowledges;
  the front end owns the order.
- `cmd/http_frontend.go` (`handleHTTP`) calls `streamhttp.Server.Shutdown`
  with `httpShutdownGrace` (5 s).
- `cmd/serve.go` has its own signal handler, which ends in `os.Exit(0)`.
- `pkg/pool`: `StdioPool.Close()` stops every server in parallel with
  `defaultStopGracePeriod` (5 s) and waits at most that plus
  `closeDeadlineMargin` (5 s). Rate limiters hold no goroutine and need no
  shutdown.
- `pkg/mcp/governor`: `Store.Close()` deletes the spill store, and
  `pruneStaleDirs` removes stale per-process directories at start.

## Next Steps

- [Configuration](./configuration.md) - Every key of `leanproxy_servers.yaml`
- [Architecture](./architecture.md) - System architecture
