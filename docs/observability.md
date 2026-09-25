# Observability: OpenTelemetry traces and metrics

leanproxy-mcp can export OpenTelemetry traces and metrics over OTLP/HTTP. It
is off by default and works with every front end: `server run --stdio`,
`server run --http` and the deprecated `serve`. See
[Telemetry (OpenTelemetry)](configuration.md#telemetry-opentelemetry) in the
configuration reference for every option. This page shows the exporter
working end to end.

## Turning it on

Telemetry is on as soon as an OTLP endpoint is set, either in the config file
or in the environment. Environment variables win over the config file.

```yaml
# ~/.config/leanproxy_servers.yaml
telemetry:
  enabled: true
  service_name: leanproxy-mcp        # the service.name resource attribute
  otlp:
    endpoint: "http://localhost:4318"
```

The config file is the simplest option with `server run --stdio`, because
the IDE starts that process and you would otherwise have to add the variables
to every IDE's MCP server entry.

| Environment variable | Effect |
|----------------------|--------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Base URL. `/v1/traces` and `/v1/metrics` are appended. Setting it turns telemetry on. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Full traces URL, used as given. |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Full metrics URL, used as given. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` (default) or `http/json`. Any other value, including `grpc`, disables telemetry with a warning. Both values currently send JSON bodies. |
| `OTEL_EXPORTER_OTLP_INSECURE` | Accepted, but has no effect: the endpoint's scheme (`http://` or `https://`) decides. |

Other standard variables are **not** read. In particular:

- `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`: set the service name
  with `telemetry.service_name` instead.
- `OTEL_EXPORTER_OTLP_HEADERS`: there is no way to send authentication
  headers. To export to a hosted backend that needs an API key, go through
  an OpenTelemetry Collector (see below).
- gRPC is not supported, only OTLP/HTTP.

Metrics are exported every 15 seconds. A traces endpoint is required: setting
only `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` disables telemetry with a warning.

## Quickstart: Jaeger via docker-compose

Jaeger's all-in-one image accepts OTLP/HTTP on port 4318 and shows traces in
its UI on port 16686.

```yaml
# docker-compose.yml
services:
  jaeger:
    image: jaegertracing/all-in-one:1.60
    ports:
      - "16686:16686"   # Jaeger UI
      - "4318:4318"     # OTLP/HTTP receiver
    environment:
      - COLLECTOR_OTLP_ENABLED=true
```

```bash
docker compose up -d

# A shared HTTP gateway; point an MCP client at http://127.0.0.1:8765/mcp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
leanproxy-mcp server run --http 127.0.0.1:8765
```

Or leave the environment alone, add the `telemetry:` block above to the
config file, and let your IDE start `leanproxy-mcp server run --stdio` as
usual. See the [Quickstart](quickstart.md#connect-your-client) for client
setup.

Make a few tool calls from the client, then open http://localhost:16686,
pick the `leanproxy-mcp` service and open a trace. Each request has:

- one `SERVER` span named after the method, for example `tools/list`, or
  `tools/call <tool>` for a tool call;
- one child span per pipeline stage that is active:
  `mcp.middleware.governor`, `mcp.middleware.tool_pinning`,
  `mcp.middleware.policy`, `mcp.middleware.cache`,
  `mcp.middleware.redact_response`, `mcp.middleware.redact_request`,
  `mcp.middleware.injection` (and `mcp.middleware.code_mode` in a code-mode
  build);
- for a request forwarded upstream, a `CLIENT` span named
  `<method> <server>`, for example `tools/call github`.

## Quickstart: OpenTelemetry Collector

To send data to any other backend (Honeycomb, Datadog, Grafana Tempo, ...),
put an OpenTelemetry Collector in front of it. The collector also adds the
authentication headers leanproxy-mcp cannot send.

```yaml
# docker-compose.yml
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.110.0
    command: ["--config=/etc/otel-collector-config.yaml"]
    volumes:
      - ./otel-collector-config.yaml:/etc/otel-collector-config.yaml
    ports:
      - "4318:4318"
```

```yaml
# otel-collector-config.yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318
exporters:
  debug:
    verbosity: detailed
  # add your real backend's exporter here (otlp, otlphttp, datadog, ...)
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [debug]
    metrics:
      receivers: [otlp]
      exporters: [debug]
```

```bash
docker compose up -d
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
leanproxy-mcp server run --http 127.0.0.1:8765
docker compose logs -f otel-collector   # traces and metrics appear here via the debug exporter
```

## Metrics

| Metric | Type | What it counts |
|--------|------|----------------|
| `mcp.server.requests` | counter | Requests handled |
| `mcp.server.errors` | counter | Requests that ended in an error |
| `mcp.server.request.duration` | histogram (ms) | Request duration |
| `mcp.server.response.size` | histogram (bytes) | Size of the result |
| `mcp.server.requests.in_flight` | up-down counter | Requests in progress |
| `leanproxy.redactions` | counter | Secrets redacted |
| `leanproxy.injection.detections` | counter | Injection findings |
| `leanproxy.cache.hits`, `leanproxy.cache.misses` | counter | Response cache lookups |
| `leanproxy.ratelimit.waits` | counter | Calls delayed by a per-server rate limit |
| `leanproxy.tool_pin.events` | counter | Tool pinning events |
| `leanproxy.policy.decisions` | counter | Policy decisions |
| `leanproxy.governor.*` | counters | Response governor accounting (see below) |

## What you will (and will not) see

Every span and metric carries method/tool/server names, sizes, counts and
status codes — `mcp.method.name`, `mcp.tool.name`, `mcp.server.name`,
`mcp.session.id`, `jsonrpc.request.id`, `error.type`, response size,
redaction counts, cache hit/miss, injection guard action. **Tool arguments
and results never appear on a span or a metric.**

Tool pinning (#310) adds the `leanproxy.tool_pin.events` counter, with the
attributes `event` (`server_pinned`, `tool_added`, `tool_changed`,
`tool_removed`, `tool_reverted`, `server_identity_changed`, `tool_flagged`,
`tool_shadowed`, and `call_blocked` for a call refused in `block` mode) and
`mcp.server.name`. A refused call's SERVER span carries
`leanproxy.tool_pin.status` (`new` / `changed`) and
`leanproxy.tool_pin.blocked: true`. Tool definitions and diffs are never
recorded, only logged.

The per-tool policy (#314) counts every decided tool call in
`leanproxy.policy.decisions`, with the attributes `outcome` (`allow`, `deny`,
`deny_unknown_tool`, `confirm_approved`, `confirm_approved_session`,
`confirm_cached`, `confirm_denied`, `confirm_unavailable`, `confirm_timeout`)
and `mcp.server.name`; the request's span carries
`leanproxy.policy.decision` (the same outcome) and `leanproxy.policy.rule`
(e.g. `rules[1] (match "github.delete_*")`). The policy stage has its own
child span, `mcp.middleware.policy`. The call's arguments are never recorded.

The response governor (#319) counts, per server, the estimated tokens of
the tool results it sees in `leanproxy.governor.tokens` (attribute
`direction`: `original` or `returned`) and the results it shortened in
`leanproxy.governor.truncations`; its stage has its own child span,
`mcp.middleware.governor`. Field projection (#320) adds
`leanproxy.governor.projections` (results projected, by server) and
`leanproxy.governor.projection.tokens` (estimated tokens of the projected
parts, attribute `direction`: `before` or `after`). In-session dedup and
summarization (#321) add `leanproxy.governor.dedup` (results replaced by a
dedup marker, by server) and `leanproxy.governor.dedup.tokens` (estimated
tokens saved), plus `leanproxy.governor.summarizations` (results replaced
by a summary), `leanproxy.governor.summarization.tokens` (estimated tokens
saved) and `leanproxy.governor.summarization.fallbacks` (summarization
attempts that fell back to truncation). Only token counts are recorded,
never a result, a content hash or a summary.

A request an upstream sends to the client (`elicitation/create`,
`sampling/createMessage`, `roots/list`; #308) gets its own SERVER span,
`<method> <server>` (for example `elicitation/create github`), with
`mcp.method.name` and `mcp.server.name`, and an error status when it is
refused or fails. Its params and the client's answer are never recorded
either.

## The `/metrics` JSON endpoint

The same counters are also kept in memory, whether or not an exporter is
configured. They are served as JSON on `/metrics` by `server run` and
`serve` with `--metrics-bind` (off by default), together with a `usage`
section: today's and week-to-date totals from the usage store, across every
proxy process on the machine. See
[Dashboard › Metrics Endpoint](dashboard.md#metrics-endpoint) for the schema.

With any front end, the counters are also written to the local usage store,
and [`leanproxy-mcp report`](savings-report.md) turns them into a savings
report.
