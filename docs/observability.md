# Observability: OpenTelemetry traces and metrics

leanproxy-mcp can export OpenTelemetry traces and metrics over OTLP/HTTP —
off by default. See [Telemetry (OpenTelemetry)](configuration.md#telemetry-opentelemetry)
in the configuration reference for every option. This page shows the
exporter working end to end against Jaeger.

## Quickstart: Jaeger via docker-compose

Jaeger's all-in-one image accepts OTLP/HTTP directly on port 4318 and shows
traces in its UI on port 16686.

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

export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
leanproxy-mcp serve --listen 127.0.0.1:9090
```

Drive a couple of requests through the proxy (e.g. `invoke_tool` against any
configured server), then open http://localhost:16686, pick the
`leanproxy-mcp` service, and find a trace: it shows one `mcp.server` SERVER
span per request, with child spans for the cache and firewall stages
(`mcp.middleware.cache`, `mcp.middleware.redact_request`,
`mcp.middleware.injection`, `mcp.middleware.redact_response`) and, for a
`tools/call`, a `CLIENT` span (`tools/call <server>`) for the upstream call.

## Quickstart: OpenTelemetry Collector

To route into any other backend (Honeycomb, Datadog, Grafana Tempo, ...),
front it with an `otel-collector` instead of pointing leanproxy-mcp directly
at the backend:

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
exporters:
  debug:
    verbosity: detailed
  # add your real backend's exporter here (otlp, honeycomb, datadog, ...)
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
leanproxy-mcp serve --listen 127.0.0.1:9090
docker compose logs -f otel-collector   # traces/metrics land here via the debug exporter
```

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

A request an upstream sends to the client (`elicitation/create`,
`sampling/createMessage`, `roots/list`; #308) gets its own SERVER span,
`<method> <server>` (for example `elicitation/create github`), with
`mcp.method.name` and `mcp.server.name`, and an error status when it is
refused or fails. Its params and the client's answer are never recorded
either.

## Config file instead of environment variables

```yaml
telemetry:
  enabled: true
  otlp:
    endpoint: "http://localhost:4318"
```

## Existing `/metrics` JSON endpoint

`leanproxy-mcp serve --metrics-bind 127.0.0.1:9091` keeps working exactly as
before; it now has a `telemetry` section fed by the same counters
OpenTelemetry records (including `tool_pin_events_total`), useful when you want a quick number without standing
up a collector.
