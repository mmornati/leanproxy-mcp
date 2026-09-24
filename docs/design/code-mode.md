# Code mode: design spike and go / no-go

!!! warning "Experimental spike, not a shipped feature"
    This is the write-up of the spike for [#325](https://github.com/mmornati/leanproxy-mcp/issues/325)
    (audit roadmap item 23). The prototype is compiled only with `go build -tags codemode`, and even
    then it stays off until `code_mode.enabled: true`. The release binary does not contain it:
    `CGO_ENABLED=0` builds of this branch and of `main` are byte-for-byte the same size.

## TL;DR

- **What was built.** One gateway tool, `execute_code {language: "js", code}`. The model sends a
  short JavaScript program. The program calls upstream tools as `await tools.<server>.<tool>(args)`
  and filters their results. Only the value it returns goes back into the context window.
- **Runtime.** The program runs in [goja](https://github.com/dop251/goja), a pure-Go JavaScript
  interpreter, inside a **separate child process**. That process is the same binary running a
  hidden `codemode-worker` command. It has kernel resource limits and an empty environment, and
  its only channel back to the proxy is a pipe.
- **Security.** Every tool call a program makes goes through the proxy's **whole** middleware
  pipeline, exactly like an `invoke_tool` call. That covers per-tool policy (deny, confirm and
  unknown tools), tool pinning, redaction in both directions, the injection guard, the response
  governor and OTel. End-to-end tests with the real binary prove this. Code mode adds a way to
  call tools. It does not add a way around any check.
- **Measured (harness, see [Measurements](#measurements)).**
    - On the task it is built for, 5 large listings reduced to a top 3, code mode cut the tokens
      from **289,982 to 230 (−99.9%)** and returned the correct answer.
    - It **loses** on small tasks: +100–200% tokens for a one-task session, because the tool's
      definition costs 213 tokens in every `tools/list`. Each call also has an 18 ms sandbox floor,
      against 0.7 ms for a plain call.
    - With the response governor on, the program **silently returned a wrong answer**. It computed
      its result from shortened listings.
    - The binary grows by 6.1 MiB (+31%).
- **Recommendation: no-go for v1.0.** Keep the spike behind the build tag, or delete it. Revisit
  only if the [conditions below](#go-no-go) are met. The token numbers are real, but they come from
  one task shape. We have not measured how reliably a model writes the code. The feature also opens
  a code-execution surface that is hard to keep safe on macOS and Windows, and the market already
  offers it.

## Why code mode, and why it is optional

The "code mode" pattern is described in Anthropic's "code execution with MCP", Cloudflare MCP
Portals' Code Mode and Docker MCP Gateway's experimental code-mode. It saves tokens on
**intermediate results**, which neither tool search (#305) nor response truncation (#319) can
touch. A task like "find the 3 open issues with the most comments across 5 repos" normally pulls
five full listings into the context so the model can read them once. A program can read them
outside the context and return three lines.

The audit (§5 item 23) rated it P3 for four reasons: the market already has it, it needs a real
sandbox, it needs a runtime that grows the binary, and its results depend on how well the model
writes code. This spike measures the first three. The fourth needs a real-model evaluation (see
[Evaluation plan](#evaluation-plan)).

## Runtime options

| | goja **in the proxy process** | goja **in a child process** (prototype) | wazero + QuickJS/Javy (WASM) | Container sandbox (#312, `pkg/pool/sandbox.go`) | Client's own code-execution tool |
|---|---|---|---|---|---|
| Binary size | +6.1 MiB, measured (goja, regexp2, sourcemap) | same +6.1 MiB, measured: the child is the same binary | about +4–6 MiB (wazero plus an embedded QuickJS `.wasm`), estimated, not built | ~0 in the binary, but needs docker or podman plus a JS image (≥ 50 MB) | 0 |
| Start-up per call | 0.05 ms, measured (`goja.New()` + a small program) | **18 ms p50, measured** (fork/exec + Go runtime + goja) | ms per instance once compiled; compiling the QuickJS module is 100s of ms (cacheable), estimated | 300 ms–1 s+ (`docker run`), estimated | none in LeanProxy |
| Stop an infinite loop | `vm.Interrupt` works in JS code but **not inside native built-ins** | Interrupt, plus `RLIMIT_CPU` (SIGXCPU, then SIGKILL) and a wall-clock kill | Context cancellation works everywhere, because all code is WASM | `docker kill` on a timeout | client's problem |
| Memory limit | **none**: goja has no heap cap, and one `"x".repeat(2**30)` can OOM the proxy and every session with it | heap watchdog plus `RLIMIT_AS` (Linux), and a crash only kills the sandbox | exact: the WASM linear-memory max | cgroup `--memory` | client's problem |
| Deterministic time-out | wall clock only | wall clock plus a CPU-time cap from the kernel | wall clock (no fuel metering in wazero) | wall clock | – |
| No filesystem or network | yes (goja exposes nothing) | yes, plus an empty env, `RLIMIT_FSIZE 0` and `RLIMIT_NOFILE 16` | yes (no WASI imports given) | `--network none`, read-only mounts | depends on the client |
| Tool calls through the pipeline | direct function call | JSON lines over the child's stdio | host functions | stdio or a socket into the container | the client calls the proxy's tools, so they already pass through it |
| Portability of the hard limits | – | Linux only (rlimits). macOS and Windows get soft limits only | all platforms | Linux/macOS with a container runtime | – |
| Maturity / risk | goja is mature and pure Go | same | QuickJS is C compiled to WASM, and WASM contains any bug in it | strong isolation, heavy dependency | out of our control |

**Why a child process.**

- **Why not in-process goja.** It is the cheapest option, but it fails the security bar. goja cannot
  interrupt its native built-ins and has no memory cap, so one allocation in a program could kill
  the proxy for every connected client. The memory tests reproduce this: `"x".repeat(2 ** 30)`
  allocates 1 GiB in one native call.
- **Why not a container.** The container sandbox from #312 gives strong isolation, but a
  300 ms–1 s start per call and a docker or podman dependency are too much for a token-saving
  feature.
- **Why not WASM.** wazero + QuickJS is the best long-term sandbox: exact memory limits, and
  cancellation that works everywhere on every platform. It was not built for this spike. Its
  numbers above are estimates.
- **What the child process gives.** Most of the WASM safety on Linux, with the runtime the issue
  asked for. The kernel enforces the limits, and the worst a program can do is crash its own
  sandbox.

**Delegating to the client's code-execution tool** needs no runtime in LeanProxy at all, and every
call already comes back through the proxy. It is the right answer for clients that have one, such
as Claude Code and the Anthropic API code-execution tool. It does nothing for the open and local
models LeanProxy targets, and it needs the client's sandbox to be able to reach the proxy.

## API surface

- **The tool.** `execute_code` is listed next to the gateway tools, in every exposure mode, only
  while code mode is on. Its input schema is `{code: string, language?: "js"}`. The description
  (213 tokens) names the configured servers and the limits.
- **Calling tools.** `tools.<server>.<tool>(args)` returns a `Promise`. Use
  `tools["my-server"].tool` for names with dashes. The program is the body of an `async` function,
  so it can use `await`, `Promise.all` and `return`.
- **Stubs.** They come from the server list only, through a dynamic object. `Object.keys(tools)`
  lists the servers. **Tools are never enumerated**: which tools exist, and which ones the policy
  hides, is decided by the proxy on each call and never disclosed to the sandbox. A model discovers
  tools with `search_tools` or `list_tools`, then calls them from code under the same server and
  tool names.
- **What a call returns.** The result's `structuredContent` if it has one. Otherwise its text,
  parsed as JSON when it is a JSON object or array, or else the text as a string. A result with
  `isError: true`, a policy refusal, a pinning block or an upstream error **rejects** the promise
  with the message the model would have seen. The program can `catch` it.
- **What the model gets back.** The return value, as a string or as JSON. `console.log` output is
  capped at 4 KiB and appended under `[console]`. A failure comes back as an `isError` result naming
  its kind (`error`, `timeout`, `cpu`, `memory`, `output`, `calls` or `crash`).
- **Not built.** The issue asked for **typed stubs** generated from the tool schemas (TypeScript
  declarations). Generating them for every tool would bring back the schema tax that the router
  removes. The better follow-up is a one-line signature per hit in `search_tools` results.

## Security model

Code mode runs **model-written code**. Treat that code as untrusted: a prompt injection in any tool
result can steer what the model writes next.

| Threat | Control in the prototype | Tested by |
|---|---|---|
| Ambient authority (fs, network, env, processes) | goja exposes only the ECMAScript built-ins plus `tools` and `console`: no `require`, modules, `process`, `fetch`, timers, `WebAssembly` or `SharedArrayBuffer`. The child runs with an **empty environment** (no upstream credentials reachable), in the temp directory, with `RLIMIT_FSIZE 0`, `RLIMIT_NOFILE 16` and `RLIMIT_CORE 0`. | `TestRun_NoAmbientAuthority`, e2e `no require` |
| Bypassing policy | Each call becomes a new `tools/call invoke_tool` request sent through `Handler.HandleRequest`, the **whole** chain from the outermost stage: exposure, telemetry, governor, pinning, policy, cache, redaction and the injection guard. The request carries a context mark: `execute_code` cannot run from code (no nested sandboxes), and a server that is not configured is refused before the pipeline. | `TestCodeMode_CallsGoThroughTheWholePipeline`, e2e policy deny / confirm / unknown-tool, pinning block |
| Leaking secrets | The program only ever sees **redacted** results, because redaction runs on each nested response. Its arguments are redacted before they reach an upstream. Its final answer goes out through redaction and the injection guard a second time. | e2e: the program checks it never saw the raw key; the upstream reports the arguments arrived redacted; the injection warning appears on the nested and the final result |
| Infinite loop / CPU burn | Wall-clock `Interrupt` at `timeout`, and a kill at `timeout + 1 s`. `RLIMIT_CPU`: SIGXCPU interrupts the VM at `cpu_time`, and the kernel sends SIGKILL one second later, which also stops native code. | `TestRun_InfiniteLoopKilledByCPULimit`, `TestRun_WallClockTimeout`, e2e |
| Memory exhaustion | A heap watchdog (runtime/metrics, every 5 ms) interrupts above `max_memory_mb`. `RLIMIT_AS`, set to current mappings + 2 × the limit + 256 MiB, makes a huge native allocation abort **the sandbox process only**. (`RLIMIT_DATA` was tried first and does not work for Go: the kernel checks it when the address space grows, and the Go heap grows by remapping space it reserved earlier.) | `TestRun_MemoryLimits` (1 GiB string, growing heap, 2²⁸-element array), e2e |
| Huge output / flooding | Output cap (64 KiB by default), checked in the sandbox and again in the proxy, with a line cap on the pipe. Caps on `console`, the source size, the argument size, each tool result handed to the program, call count (32) and concurrency (4). | `TestRun_OutputLimit`, `TestRun_CallLimit`, e2e |
| A crashed or hostile sandbox | Separate process; `Pdeathsig: SIGKILL` (it dies with the proxy); its own process group. The proxy parses only JSON lines from it, and any malformed line kills it. In-flight tool calls are cancelled when it ends. | e2e: the proxy answers `ping` and runs the next program after every limit |
| Audit | One log line per call (server, tool, duration, outcome) and one per program (outcome, calls, duration, output size). Nested calls are OTel child spans of the `execute_code` span. | e2e checks the log line |

**Residual risks (why this is not a go).**

1. **Client-side approval is collapsed.** Clients such as Claude Code ask the user per MCP tool.
   With `execute_code`, one approval covers every call the program makes. LeanProxy's own policy
   still applies per call, including a per-call `confirm` through elicitation. The tool is
   annotated `destructiveHint: true` so a careful client prompts for it. Still, the user sees
   less than before.
2. **Exfiltration gets cheaper and less visible.** A program can read from one tool and write to
   another (the "lethal trifecta") without the intermediate data ever appearing in the transcript.
   Redaction, the injection guard and `confirm` rules on write or open-world tools are the
   mitigations, and they are the same ones as for plain calls. But nobody reads the intermediate
   data anymore, so nobody can spot a problem in it.
3. **Hard limits are Linux-only.** On macOS and Windows the prototype has only the watchdogs and
   the wall-clock kill. The watchdogs cannot stop a runaway native built-in, and a memory spike
   there can reach the machine's limit (though only the sandbox process dies).
4. **No syscall filter.** The sandbox process is a full Go binary. goja being pure Go and
   memory-safe is the isolation boundary. A seccomp or Landlock profile, or WASM, would add a
   second one.
5. **Side effects of cancelled calls.** A call already sent upstream when the program ends or is
   killed may still take effect.

## Measurements

From `make harness` (`tests/harness/codemode_test.go`, linux/amd64). A proxy built with
`-tags codemode` runs over `catalogmcp --large-results`, where `list_issues` returns a
300-issue page. Tokens are those of the JSON-RPC request and response lines, including the code
the model writes. Latency is the median of 7 runs, proxy side only. **No model is involved**: the
programs are fixed. These numbers measure cost, not whether a model writes the program correctly.

| Task | Plain tokens | Code mode tokens | Change | Code mode + its definition (one-task session) | Plain latency seq / parallel | Code mode latency | Model turns (seq / parallel / code) | Code mode answer |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| Top 3 issues by comments across 5 repos (5 × `list_issues`) | 289,982 | 230 | −99.9% | 443 · −99.8% | 70.2 / 35.8 ms | 108.4 ms | 6 / 2 / 2 | correct |
| Same task, response governor on (`max_tokens: 4000`) | 18,977 | 206 | −98.9% | 419 · −97.8% | 73.5 / 31.8 ms | 62.7 ms | 6 / 2 / 2 | **wrong** |
| One small call (`create_issue`) | 104 | 101 | −2.9% | 314 · **+201.9%** | 0.6 / 0.6 ms | 19.2 ms | 2 / 2 / 2 | ok |
| Two small calls, both results needed | 178 | 145 | −18.5% | 358 · **+101.1%** | 1.1 / 0.5 ms | 19.5 ms | 3 / 2 / 2 | ok |

| Fixed cost | Value |
|---|---:|
| `execute_code` definition in `tools/list` (every session, re-read every turn) | 213 tokens |
| Sandbox floor, p50: `execute_code("return 1")` vs one small `invoke_tool` | 17.6 ms vs 0.7 ms |
| Binary size, `-s -w`: default vs `-tags codemode` | 19.9 MiB vs 26.0 MiB (+6.1 MiB, +31%) |
| Default binary, this branch vs `main` (`CGO_ENABLED=0`, as released) | 20,816,056 vs 20,816,056 bytes |

**Reading the numbers honestly:**

- **Where code mode wins.** Large intermediate results that the answer does not need.
  Proxy-side latency is slightly *worse* (108 ms vs 36 ms when the plain calls run in parallel):
  the program's calls queue behind a concurrency of 4, and the sandbox costs 18 ms. It saves
  model turns only against a client that makes one tool call per turn (6 → 2). A client that makes
  parallel calls needs 2 turns either way. In a real session the token saving compounds, because
  plain results stay in the context and are re-read on every later turn.
- **Against the response governor.** The governor, which already ships, cuts the same plain task
  by 93% (289,982 → 18,977). Code mode still takes it to 206. But **the governor also governs the
  calls a program makes**, as the security requirement demands, so the program worked on
  truncated, projected listings. It then returned a plausible top 3 with one entry missing its
  fields, and it raised no error. A model would pass that answer on. Using both features together
  needs the follow-up listed below, or the answer is silently wrong.
- **Where code mode loses.**
    - **Small or single calls.** The program costs as much as the call, the definition adds
      213 tokens to every session, and the sandbox adds 18 ms.
    - **Results the model needs verbatim** (a file to edit, a diff to review). There is nothing to
      filter, and the output cap (64 KiB) is below what the governor would page through
      `read_result`.
    - **Tasks where the next step depends on judging a result.** The model has to see the
      intermediate data anyway.
    - **Failing programs.** A buggy program costs a failed call plus a retry, which is not measured
      here.

## Evaluation plan

1. **Harness (done).** The scenario above runs in every `make harness`. Its assertions:
    - the program's answer equals the one computed from the plain calls;
    - the multi-call task saves at least 90% of the tokens;
    - the default binary does not contain code mode.
2. **Real model (manual, not run in this spike).** This environment has no model access. The plan:
    - **Tasks, 20 runs each.**
        - "Top 3 issues by comments across 5 repos".
        - "Which of these 10 Jira tickets have no linked PR".
        - "Sum the rows of 3 SQL queries".
        - Two control tasks where code mode should not help: one small call, and "read this file and
          fix the bug".
    - **Arms.** Router + `invoke_tool`, router + `execute_code`, and passthrough + the client's
      native tool search.
    - **Models.** One frontier model and one local model through Ollama or llama.cpp. LeanProxy's
      audience includes local models, and small models write worse code.
    - **Metrics.** Task success (checked answer), total billed tokens including retries (from the
      client's usage), model turns, and wall time.
    - **Go bar.** Success rate within 5 points of the plain flow on the multi-call tasks, for both
      models.

## Go / no-go

**No-go for v1.0.** The prototype stays behind `-tags codemode`, off by default, as a reference.
Deleting it is also fine.

The reasons, with the numbers above:

1. **The win is narrow.** On the task it is built for, it saves −99.9% of tokens. But it costs +100%
   to +200% on small tasks once its definition is counted, and it is **wrong** next to the response
   governor. The governor already takes 93% off that same task, safely, without asking the model to
   write code.
2. **The security cost is real.** A code-execution surface whose hard limits exist on Linux only.
   It also removes per-tool approval in the client, and it makes chained exfiltration invisible.
   The prototype contains all of this, but each point is a reason to go slowly.
3. **It depends on the model, and that is not measured.** The audience that benefits most from
   LeanProxy (open and local models, clients without native tool search) is the one most likely to
   write broken programs.
4. **The market already has it.** Anthropic, Cloudflare and Docker ship code mode, and for clients
   with a code-execution tool, delegating to it gives the same saving with no runtime in LeanProxy.
   The +6.1 MiB (+31%) binary would buy a me-too feature, not a differentiator.

**Conditions to revisit** (all of them):

- A real-model evaluation that meets the go bar above.
- Calls made from code are exempt from the governor's truncation, projection and dedup, but still
  redacted and injection-scanned. Their results never enter the context, so shortening them only
  corrupts answers.
- A second isolation boundary: WASM (wazero + QuickJS), or seccomp and Landlock on Linux, plus a
  macOS story.
- Per-call signatures in `search_tools` results instead of stubs for every tool, and the
  `execute_code` definition only in exposure modes where it pays off.

## Try the prototype

```bash
go build -tags codemode -o leanproxy-mcp-codemode .
```

```yaml
code_mode:
  enabled: true        # off by default; ignored (with a warning) by a default build
  timeout: 30s         # wall clock per execute_code, tool calls included
  cpu_time: 5s         # sandbox CPU time (RLIMIT_CPU, Linux)
  max_memory_mb: 256   # heap watchdog + RLIMIT_AS (Linux)
  max_calls: 32        # tool calls per program
  max_concurrent_calls: 4
  max_output_bytes: 65536
```

Only `server run` (stdio and Streamable HTTP) has code mode. The deprecated `serve` front end does
not. See [configuration](../configuration.md#code-mode-code_mode-experimental).

**Dependency.** `github.com/dop251/goja` is pinned to `v0.0.0-20260917113740-793a2a65c13b`. goja has
no tagged releases, so the pseudo-version pins an exact commit, which `go.sum` hashes.

- **Licenses.** goja is MIT. Its dependencies are `dlclark/regexp2` (MIT), `go-sourcemap/sourcemap`
  (BSD-2-Clause) and `google/pprof` (Apache-2.0), all compatible with this MIT project.
- **Linking.** Everything is pure Go, with no cgo. It is linked only with `-tags codemode`.
- **Vulnerability checks.** CI's security job now also runs `govulncheck -tags codemode ./...`,
  because the default run never sees these packages.

**Code.**

- `pkg/codemode/`: config, sandbox and worker.
- `pkg/mcp/middleware_codemode.go`: the pipeline stage.
- `cmd/codemode_on.go` and `cmd/codemode_off.go`.
- Tests: `pkg/codemode/*_test.go` and `pkg/mcp/middleware_codemode_test.go` (`make test-codemode`),
  `tests/e2e/codemode_test.go` (default `go test`), and `tests/harness/codemode_test.go`.
