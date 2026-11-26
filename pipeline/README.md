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

## Query deadline

Set `query_timeout: 10s` under a service pipeline to bound query processing
through response handling. The default is zero, which uses any caller
deadline. Connector operands share the parent deadline, and a leaf can
shorten it. Expiration returns HTTP 504 or gRPC DEADLINE_EXCEEDED.

## Backend retries

Configure `service.backend_retries` by dispatcher ID, for example
`prometheus/primary: {max_attempts: 3, backoff: 100ms}`. One attempt
is the default. A retry is eligible for HTTP 502, 503 and 504 upstream
failures or a classified transient transport error. Validation, access
denials, other statuses, streaming queries and canceled requests are
excluded. The fixed wait and attempts share the original query context.
Executor slots should enclose the full retry loop. Debug logs report
attempt counts and final success without query data.

## Backend circuit breaker

Set `service.backend_circuit_breakers` by dispatcher ID, for example
`prometheus/primary: {failure_threshold: 3, open_for: 20s, probe_limit: 1}`.
The default is disabled. A successful backend call resets the failure count.
Backend unavailable and timeout outcomes count while the query is active.
Admission, authorization and caller cancellation do not count. The breaker
counts the final logical outcome after retries. Open circuits return HTTP 502
or gRPC UNAVAILABLE while other dispatcher IDs remain independent. After the
open interval, the first conclusive recovery probe wins. Debug logs describe
transitions and admitted probes.
