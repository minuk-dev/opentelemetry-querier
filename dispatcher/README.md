# dispatcher

The **Dispatcher** component category and its `Factory` — the query-side analogue
of the collector's `exporter`.

A Dispatcher is the terminal pipeline stage: it renders a `qdata.Query` to a
concrete storage backend, executes it, and parses the backend response back into
a `qdata.Result`.

- `Dispatcher` — `component.Component` + `Dispatch(ctx, *qdata.Query)`.
- `Base` — no-op lifecycle to embed for dispatchers that need no setup.
- `Factory` / `NewFactory` / `MakeFactoryMap`.

## Implementations

| Module | Type | Description |
| --- | --- | --- |
| [prometheusdispatcher](./prometheusdispatcher) | `prometheus` | Executes against an upstream Prometheus HTTP query API. |
| [lokidispatcher](./lokidispatcher) | `loki` | Executes LogQL against an upstream Grafana Loki HTTP query API. |
| [elasticsearchdispatcher](./elasticsearchdispatcher) | `elasticsearch` | Executes Lucene queries against an upstream Elasticsearch `_search` API. |

The built-in HTTP dispatchers accept up to 16 MiB of decoded response data
per upstream request. This limit also applies to upstream error responses.
Larger responses fail with an upstream error.

### Configurable result limits

Set `max_response_bytes` up to 16 MiB on any built-in HTTP dispatcher to
lower the decoded body limit. `max_series` and `max_samples` apply to
Prometheus and Loki metric results. `max_records` applies to Loki streams
and Elasticsearch hits. Counts are checked before constructing qdata
records. Configure `max_rows` on a cross-signal connector to constrain
each operand before table conversion and cap joined output. The default
join output limit is 10,000 rows. Configured limits reject oversized
results with HTTP 429 or gRPC RESOURCE_EXHAUSTED. Elasticsearch hit totals
and partial flags retain their upstream meanings for permitted results.
