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

## Result cache

A pipeline can opt in to a local result cache with both a positive `ttl` and
`max_bytes` configured. Each leaf pipeline owns its cache, including when a
connector routes work to it. Prometheus, Loki, and Elasticsearch dispatchers
support caching. Put cache configuration on the target pipelines when a
connector routes queries so each target still runs its own request processors.

The cache runs after request processors validate identity, apply policy, and
rewrite the query. Enable it with a tenant resolved from an authenticated or
trusted source, such as a gateway that controls the tenant header. The cache
bypasses unresolved tenants, instant queries evaluated at implicit current time,
and streaming queries. The key is a deterministic encoding of the rewritten
query, including its tenant and policy metadata, time, and options. Transport
headers do not affect cache keys. A dispatcher opting in must derive results from
the rewritten query and its metadata.

Entries hold dispatch results for up to the configured TTL. Result processors
run on a private response every time, including on hits. A successful response
path can publish an entry. Oversized results, encoding failures, and results
with a reported search timeout or failed shard skip cache storage. The capacity
counts encoded results and their keys. The least recently used entries are
evicted when space is needed.

Concurrent misses with the same key share one dispatch within a replica. Each
caller keeps its own cancellation and deadline. The last caller leaving
cancels unfinished dispatch. A failed dispatch completes its waiters and the
next request can try again. Each replica has its own cache and in-flight work.
