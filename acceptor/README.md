# acceptor

The **Acceptor** component category and its `Factory` — the query-side analogue
of the collector's `receiver`.

An Acceptor accepts queries from clients over some transport and feeds them to
the pipeline (its "next consumer"), then serializes the result back.

- `Acceptor` — a `component.Component`.
- `Factory` — `component.Factory` + `CreateAcceptor(ctx, settings, cfg, next)`.
- `NewFactory` / `MakeFactoryMap` — build and index factories.

## Implementations

| Module | Type | Description |
| --- | --- | --- |
| [otqp](./otqp) | `otqp` | OpenTelemetry Query Protocol over gRPC + HTTP (default). |
