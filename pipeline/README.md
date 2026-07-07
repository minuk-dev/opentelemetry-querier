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
  `dispatcher.Dispatcher`. `Handle` runs the request path (processors in order),
  dispatches to storage, then the response path (processors in reverse). A
  processor error on the request path short-circuits before the dispatcher is
  reached.
