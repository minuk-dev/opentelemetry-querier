# OpenTelemetry Querier

A query proxy for observability backends, modeled on the
[OpenTelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector).
Where the Collector ingests telemetry through a *receiver → processor → exporter*
pipeline, the Querier proxies **queries** through an
**acceptor → processor → dispatcher** pipeline: it accepts a query, transforms it
and its results, controls tenancy, and dispatches it to storage.

```
Client ──▶ Acceptor ──▶ [ Processor chain ] ──▶ Dispatcher ──▶ Storage
                              │                                   │
   Result ◀───────────────────┘◀── (results flow back out ◀──────┘
                                     through the processors
                                     in reverse order)
```

## Concepts

### The pipeline

| Stage | Collector analogue | Role |
| --- | --- | --- |
| **Acceptor** | receiver | Accepts client queries over some transport and feeds them to the pipeline. |
| **Processor** | processor | Transforms the query on the way in and/or the result on the way out; may short-circuit (auth, rate limit). |
| **Dispatcher** | exporter | Renders the query to a storage backend, executes it, and parses the response back. |

### `qdata` — the standardized query & result model

Every stage operates on `qdata`, the signal-agnostic pivot model — the query-side
equivalent of the Collector's `pdata`. It is **defined in protobuf**
(`proto/qdata/v1`) and generated with [buf](https://buf.build), so it doubles as a
cross-language wire format / intermediate representation.

`qdata` follows the CNCF Observability TAG **Query Language Standardization Working
Group (QLSWG)** draft semantic specification:

- a standard **data-type set** (double, int, uint, string, bytes, bool,
  timestamp, duration, map, array, JSON);
- a **flattened attribute map** (OTel resource + scope + attributes merged);
- a **multi-signal result model** — metrics, logs/events, spans, profiles — with,
  for metrics, the QLSWG fields: `type` (GAUGE / CUMULATIVE_COUNTER / … /
  UNKNOWN), start/end timestamps, windowed array values, exemplars, temporal &
  group aggregation;
- a DSL-agnostic **query request** with the three evaluation contexts (instant,
  range, streaming), time range/step kept out of the DSL, and offset/lookback
  modifiers;
- a **feedback side channel** so a best-effort proxy can attach warnings
  (e.g. "raw cumulative counter returned without `rate()`") without failing the
  query.

### OTQP — the OpenTelemetry Query Protocol

The default acceptor speaks **OTQP**, the query-side analogue of OTLP. A
`QueryRequest` carries a `qdata` Query; a `QueryResponse` carries the `qdata`
Result (with its feedback). It is served over **gRPC** and **HTTP** (protobuf or
JSON body):

- gRPC: `QueryService/Query` and `QueryService/QueryStream` (default `:4327`)
- HTTP: `POST /v1/query` (default `:4328`)

## Two-layer configuration (like the Collector)

| File | When | Purpose |
| --- | --- | --- |
| `builder.yaml` | **build time** | Selects which component *types* are compiled into a distribution (analogue of `ocb`). |
| `config.yaml` | **runtime** | Declares component *instances* and wires them into named pipelines. |

### Build time — the builder

`cmd/builder` reads `builder.yaml` and generates the distribution's `main.go` and
`components.go` (factory registration) under `dist.output_path`:

```console
$ go run ./cmd/builder --config builder.yaml
builder: generated distribution "querier" in ./cmd/querier
```

### Runtime — the config

```yaml
acceptors:
  otqp:
    grpc_endpoint: "0.0.0.0:4327"
    http_endpoint: "0.0.0.0:4328"
processors:
  tenant: { header: "X-Scope-OrgID", enforce_label: "tenant_id" }
  queryrewrite:
    enforce_labels: [{ name: "tenant_id", from_tenant: true }]
dispatchers:
  prometheus: { endpoint: "http://localhost:9090" }
service:
  pipelines:
    query/default:
      acceptors: [otqp]
      processors: [tenant, queryrewrite]
      dispatchers: [prometheus]
```

Component instances are keyed by ID (`type` or `type/name`), so you can run
several instances of a type in different pipelines. See `config.yaml` for a fully
commented example.

## Built-in components

| Category | Type | Description |
| --- | --- | --- |
| Acceptor | `otqp` | OTQP over gRPC + HTTP (default acceptor). |
| Processor | `authratelimit` | Bearer-token auth + per-tenant token-bucket rate limiting. |
| Processor | `tenant` | Resolves the tenant (e.g. `X-Scope-OrgID`) and registers an isolation label matcher. |
| Processor | `queryrewrite` | Injects enforced label matchers into the PromQL AST (prom-label-proxy style). |
| Processor | `responsefilter` | Drops/masks result attributes; emits QLSWG feedback warnings. |
| Dispatcher | `prometheus` | Executes against an upstream Prometheus HTTP query API. |

Each component is its own Go package exposing a `NewFactory()` (`Type()`,
`CreateDefaultConfig()`, `CreateX()`), so new components can be authored
independently and selected via `builder.yaml`.

## Getting started

Prerequisites: Go 1.25+, [`buf`](https://buf.build/docs/installation),
`protoc-gen-go`, `protoc-gen-go-grpc`.

```console
# 1. Generate qdata / OTQP code from protobuf
$ buf generate

# 2. Generate the distribution from the builder manifest
$ go run ./cmd/builder --config builder.yaml

# 3. Build and run the distribution
$ go build -o bin/querier ./cmd/querier
$ ./bin/querier --config config.yaml

# 4. Send a query over OTQP/HTTP (JSON)
$ curl -X POST http://localhost:4328/v1/query \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer dev-token" \
    -H "X-Scope-OrgID: acme" \
    -d '{"query":{"expr":"up","dialect":"promql"}}'
```

## Project layout

```
proto/                 protobuf sources
  qdata/v1/            the standardized query & result model (QLSWG)
  otqp/v1/             the OTQP service
gen/                   generated Go (buf generate)
component/             component core: Type, ID, Component, Factory, Settings
acceptor/              acceptor category + factory; otqp/ the OTQP acceptor
processor/             processor category + factory; tenant/ queryrewrite/ ...
dispatcher/            dispatcher category + factory; promdispatcher/
pipeline/              acceptor→processors→dispatcher wiring (the Handler)
qdata/                 ergonomic helpers over the generated qdata types
qerror/                transport-agnostic coded errors
querier/               assembly: Factories, Config, multi-pipeline Service
cmd/builder/           the ocb-style builder (reads builder.yaml)
cmd/querier/           the generated default distribution
```

## Multi-module workspace

Like the opentelemetry-collector, **every component is its own Go module** — so a
`builder.yaml` can select components by `gomod` path and a distribution depends
only on what it uses. The core packages (`component`, `qdata`, `qerror`,
`pipeline`, the `acceptor` / `processor` / `dispatcher` category interfaces,
`querier`, `gen`) and each concrete component (`acceptor/otqp`,
`processor/tenant`, …) and the distribution (`cmd/querier`) are separate modules,
tied together for local development by a root `go.work`.

Because there is no single root module, build/test/lint run per module:

```console
$ for m in $(find . -name go.mod -not -path './.git/*'); do (cd "$(dirname "$m")" && go build ./... && go test ./...); done
```

Config is decoded with `mapstructure` (snake_case keys), matching the collector's
`confmap`; OTQP's JSON wire format stays camelCase via protojson.

Lint runs the full golangci-lint linter set (all non-deprecated linters,
`depguard` enabled) — see `.golangci.yml`.

## Status

Early scaffold. The metrics signal is wired end to end against Prometheus; logs,
spans and profiles exist in `qdata` but are not yet dispatched. Streaming context
returns a single window for now.

## License

[Apache License 2.0](LICENSE).
