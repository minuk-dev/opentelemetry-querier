# processor

The **Processor** component category and its `Factory` — the query-side analogue
of the collector's `processor`.

A Processor is a pipeline stage that may transform a query on the way in,
transform the result on the way out, or short-circuit the request (auth, rate
limit). Returning an error from `ProcessQuery` short-circuits before the
dispatcher is reached; return a `*qerror.Error` to control the transport status.

- `Processor` — `component.Component` + `ProcessQuery(ctx, *qdata.Query)` +
  `ProcessResult(ctx, *qdata.Query, *qdata.Result)`.
- `Base` — no-op implementations to embed and override selectively.
- `Factory` / `NewFactory` / `MakeFactoryMap`.

## Implementations

| Module | Type | Description |
| --- | --- | --- |
| [authratelimit](./authratelimit) | `authratelimit` | Bearer-token auth + per-tenant rate limiting. |
| [tenant](./tenant) | `tenant` | Resolve the tenant and register an isolation label matcher. |
| [queryrewrite](./queryrewrite) | `queryrewrite` | Inject enforced label matchers into the PromQL AST. |
| [responsefilter](./responsefilter) | `responsefilter` | Drop/mask result attributes; emit feedback warnings. |
