# acceptor

The **Acceptor** component category and its `Factory` — the query-side analogue
of the collector's `receiver`.

An Acceptor accepts queries from clients over some transport and feeds them to
the pipeline (its "next consumer"), then serializes the result back.

- `Acceptor` — a `component.Component`.
- `Factory` — `component.Factory` + `CreateAcceptor(ctx, settings, cfg, next)`.
- `NewFactory` / `MakeFactoryMap` — build and index factories.
- `PrepareIngress(query, header)` — the trust boundary; see below.

## The trust boundary

An acceptor is where client input becomes a pipeline query, so every acceptor
must call `PrepareIngress(query, header)` on a query before handing it to its
next consumer — including one it built itself from a native query, so the rules
hold for every transport and cannot drift per acceptor. It:

1. **Clears the pipeline-owned fields** `metadata`, `enforced_matchers` and
   `enforced_predicates`. They are fields of the request message, so a client can
   populate them, but they are the pipeline's state: `metadata` carries
   `tenant.id`, the trust anchor the tenant, query-rewrite, authz, rate-limit and
   dispatcher components all read, and the enforced fields carry the isolation
   the enforcing processors decided on. Trusting an inbound `tenant.id` let a
   client pick its own tenant and defeat header-based tenancy (issue #65).
2. **Injects the transport headers** onto `Query.header`, each overriding any
   value the body carried under the same name — case-insensitively, so no
   case-variant duplicate is left behind for a downstream (case-insensitive)
   lookup to pick between.

The header is therefore the only tenancy/auth input, which is what an upstream
gateway or authenticator actually controls.

## Implementations

| Module | Type | Description |
| --- | --- | --- |
| [otqpacceptor](./otqpacceptor) | `otqp` | OpenTelemetry Query Protocol over gRPC + HTTP (default). |
| [prometheusacceptor](./prometheusacceptor) | `prometheus` | Prometheus HTTP query API (`/api/v1/query`, `/api/v1/query_range`). |
| [lokiacceptor](./lokiacceptor) | `loki` | Grafana Loki HTTP query API (`/loki/api/v1/query`, `/loki/api/v1/query_range`). |
| [elasticsearchacceptor](./elasticsearchacceptor) | `elasticsearch` | Elasticsearch `_search` API (`/{index}/_search`). |
