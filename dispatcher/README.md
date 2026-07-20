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
