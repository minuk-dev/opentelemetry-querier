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
| [authratelimitprocessor](./authratelimitprocessor) | `authratelimit` | Bearer-token auth + per-tenant rate limiting. |
| [tenantprocessor](./tenantprocessor) | `tenant` | Resolve the tenant and register an isolation label matcher. |
| [simpleauthzprocessor](./simpleauthzprocessor) | `simpleauthz` | Per-subject authorization: reject or scope a user's query. |
| [queryrewriteprocessor](./queryrewriteprocessor) | `queryrewrite` | Inject enforced label matchers into the PromQL AST. |
| [responsefilterprocessor](./responsefilterprocessor) | `responsefilter` | Drop/mask result attributes; emit feedback warnings. |

## Security dependencies

Configured tenant consumers require a preceding tenant resolver: per-tenant
rate limits, `simpleauthz.from_tenant`, and `from_tenant` enforcement labels.
Tenant isolation and authorization matchers require a subsequent `queryrewrite`.
For example: `tenant → authratelimit → simpleauthz → queryrewrite → responsefilter`.
`querier.Build`, `Pipeline.Start`, and `Pipeline.Handle` reject invalid ordering,
including named component instances. Tenant forwarding without isolation and
authorization rules without constraints do not require a rewrite.

Custom security processors should implement `SecurityProcessor` to declare their
configured `SecurityRequirements`. Query rewrite consumes pending
`EnforcedMatchers`/`EnforcedPredicates` after composing them into the plan; the
pipeline rejects any pending constraints left before dispatch, including ones
registered by custom processors without dependency declarations.
