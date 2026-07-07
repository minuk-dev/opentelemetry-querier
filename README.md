This is not an opentelemetry project component.

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
                                     through the processors)
```

| Stage | Collector analogue | Role |
| --- | --- | --- |
| [**Acceptor**](./acceptor) | receiver | Accept client queries and feed them to the pipeline. |
| [**Processor**](./processor) | processor | Transform the query / result; short-circuit (auth, rate limit). |
| [**Dispatcher**](./dispatcher) | exporter | Execute against a storage backend and parse the response. |

Every stage operates on [**`qdata`**](./qdata) — a protobuf-defined, signal-agnostic
query & result model (the query-side analogue of the collector's `pdata`),
following the CNCF Observability TAG QLSWG draft. The default acceptor speaks
**OTQP** (OpenTelemetry Query Protocol) over gRPC and HTTP.

## Components

| Category | Component | Description |
| --- | --- | --- |
| Acceptor | [`otqp`](./acceptor/otqp) | OTQP over gRPC + HTTP (default). |
| Processor | [`authratelimit`](./processor/authratelimit) | Bearer auth + per-tenant rate limiting. |
| Processor | [`tenant`](./processor/tenant) | Tenant resolution + series isolation. |
| Processor | [`queryrewrite`](./processor/queryrewrite) | PromQL AST label injection. |
| Processor | [`responsefilter`](./processor/responsefilter) | Drop/mask result attributes; feedback warnings. |
| Dispatcher | [`prometheus`](./dispatcher/promdispatcher) | Upstream Prometheus HTTP query API. |

Each component is its own Go module exposing a `NewFactory()`, so new components
can be authored independently and selected via `builder.yaml`. See each
component's README for its config.

## Two-layer configuration

| File | When | Purpose |
| --- | --- | --- |
| [`builder.yaml`](./builder.yaml) | build time | Select which component *types* are compiled into a distribution (see [cmd/builder](./cmd/builder)). |
| [`config.yaml`](./config.yaml) | runtime | Declare component *instances* and wire them into named pipelines (see [querier](./querier)). |

## Quick start

Prerequisites: Go 1.25+, [`buf`](https://buf.build/docs/installation),
`protoc-gen-go`, `protoc-gen-go-grpc`.

```console
# 1. Generate code from protobuf, then the distribution from the manifest
$ buf generate
$ go run ./cmd/builder --config builder.yaml

# 2. Build and run
$ go build -o bin/querier ./cmd/querier
$ ./bin/querier --config config.yaml

# 3. Send a query over OTQP/HTTP (JSON)
$ curl -X POST http://localhost:4328/v1/query \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer dev-token" \
    -H "X-Scope-OrgID: acme" \
    -d '{"query":{"expr":"up","dialect":"promql"}}'
```

## Layout

This is a **multi-module workspace**: every component is its own Go module, tied
together by a root `go.work`. Because there is no single root module,
build/test/lint run per module:

```console
$ for m in $(find . -name go.mod -not -path './.git/*'); do (cd "$(dirname "$m")" && go build ./... && go test ./...); done
```

| Path | Module |
| --- | --- |
| [`proto/`](./proto) · [`gen/`](./gen) | protobuf sources and generated Go |
| [`component/`](./component) | Type, ID, Component, Factory, Settings |
| [`qdata/`](./qdata) · [`qerror/`](./qerror) | query/result model · coded errors |
| [`acceptor/`](./acceptor) · [`processor/`](./processor) · [`dispatcher/`](./dispatcher) | category interfaces + concrete components |
| [`pipeline/`](./pipeline) · [`querier/`](./querier) | pipeline wiring · assembly |
| [`cmd/builder/`](./cmd/builder) · [`cmd/querier/`](./cmd/querier) | ocb-style builder · generated distribution |

Config is decoded with `mapstructure` (snake_case keys); lint runs the full
golangci-lint set (see [`.golangci.yml`](./.golangci.yml)).

## Status

Early scaffold. The metrics signal is wired end to end against Prometheus; logs,
spans and profiles exist in `qdata` but are not yet dispatched. Streaming context
returns a single window for now.

## License

[Apache License 2.0](LICENSE).
