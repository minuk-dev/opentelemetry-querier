# pipeline

Wires an acceptor to a dispatcher through an ordered chain of processors —
the query-side analogue of the collector's pipeline.

```
Acceptor → [Authentication extension] → [Processors] → Dispatcher → storage
                          (results flow back out
                           through the processors
                           in reverse order)
```

- **`Handler`** — `Handle(ctx, *qdata.Query) (*qdata.Result, error)`. Acceptors
  depend on this interface so they can be tested with a stub.
- **`Pipeline`** — an ordered `[]processor.Processor` terminated by a
  `dispatcher.Dispatcher`. `Handle` validates the query plan, runs the request
  path (processors in order), dispatches to storage, then the response path
  (processors in reverse). A validation or processor error on the request path
  short-circuits before the dispatcher is reached.

## Plan validation

`Handle` runs `qdata.ValidatePlan` on the client's plan before the first
processor sees it (issue #67). Every acceptor funnels through the pipeline, so
this is the one place a plan is checked for shape, and a malformed one is
rejected as `qerror.CodeInvalidArgument` instead of reaching a dispatcher —
where a zero `time_agg` window became an upstream failure, an `aggregate`
setting both `by` and `without` silently lost its `without`, and an empty
function name rendered as `(...)`.

A query carrying no plan at all is left to the dispatcher, which fails closed on
it with its own message.

A [dispatch processor](../processor#dispatch-processors) can supply the result
at its configured position in the response chain.

## Query deadline

Set `query_timeout: 10s` under a service pipeline to bound query processing
through response handling. The default is zero, which uses any caller
deadline. Connector operands share the parent deadline, and a leaf can
shorten it. Expiration returns HTTP 504 or gRPC DEADLINE_EXCEEDED.

## Authentication and authorization

Declare authenticator instances under `extensions` and enable them in
`service.extensions`. Each acceptor selects its authenticator with
`auth.authenticator`. The acceptor verifies credentials from the transport
and exposes the verified principal and tenant to the pipeline through the
request context. An optional `auth.tenant_header` selects a tenant from the
verified scope. It defaults to `X-Scope-OrgID`. A credential with one allowed
tenant can omit the header.

Configure query authorization with processors such as
[`simpleauthz`](../processor/simpleauthzprocessor). Put policy processors before
`queryrewrite` so it applies the enforcement they register, and before any
cache that can return a result. An authenticated request shares its pipeline
deadline across authentication and query processing.

See [the default config](../config.yaml) for a complete example. A legacy
`authratelimit` config with `require_bearer` or `tokens` now fails validation.
Move the tokens into a `bearertokenauth` extension and specify the identity
and tenants the tokens represent. Leave rate settings under `processors`.

## Query concurrency

```yaml
service:
  concurrency:
    per_tenant: 8
    backends:
      prometheus/primary: 16
    queue_size: 32
```

Caps and queue capacity are shared across pipelines in one process.
A zero tenant cap disables tenant limiting. A zero queue rejects
immediately. Otherwise a full queue returns HTTP 429 or gRPC
RESOURCE_EXHAUSTED. Queued requests use their caller or query deadline.
The parent holds one verified-tenant slot through connector execution.
Each storage call takes its configured backend slot. Cache hits only
take a tenant slot. Slots release after active work ends. Waiters
compete for a freed slot.
