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
