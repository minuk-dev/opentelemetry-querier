# pipeline

Wires an acceptor to a dispatcher through an ordered chain of processors —
the query-side analogue of the collector's pipeline.

```
Acceptor → [Processors] → Dispatcher → storage
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

## Verified identity

Configure `identity.gateway.authority` and `identity.gateway.secret_file` on
a pipeline to require a gateway assertion. Mount a dedicated, randomly
generated secret of at least 32 bytes and use TLS between the gateway and
Querier. The gateway strips inbound `X-Querier-*` headers and sets
`X-Querier-Gateway-Secret`, `X-Querier-Principal` and at least one
`X-Querier-Allowed-Tenant` value. It can also assert `X-Querier-Group` and
`X-Querier-Role` values. Deploy the listener on a private interface.
Set `identity.tenant_header` to select a tenant header. The default is
`X-Scope-OrgID`. A single allowed tenant can be selected implicitly.

For existing header-based policies, configure the gateway to assert the old
subject and allowed tenant, then enable identity on the protected pipeline.
Tenant and authorization processors use the verified identity when available.
Authenticators can implement `pipeline.Authenticator`. Components can read
the identity through `qdata.IdentityFromContext`, including in connector leaves.

### Basic credentials

Use `identity.basic` on a pipeline and configure its accounts with mounted
bcrypt hash files. Each file holds a single hash with cost 10–16. Configure
TLS at the listener or at a trusted TLS terminator, including protection of
the connection to Querier. OTQP gRPC clients send the same Authorization
value as request metadata.

```yaml
identity:
  basic:
    authority: platform
    realm: Querier
    accounts:
      alice:
        hash_file: /run/secrets/querier-alice-bcrypt
        tenants: [team-a]
```
