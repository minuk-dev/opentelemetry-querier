# qdata

Ergonomic helpers over the generated [`gen/qdata/v1`](../gen) protobuf types —
the standardized query & result model that flows through the pipeline (the
query-side analogue of the collector's `pdata`).

`qdata` is defined in protobuf (see [`proto/qdata/v1`](../proto/qdata/v1)) and
follows the CNCF Observability TAG **Query Language Standardization Working
Group** draft: a standard data-type set, a flattened attribute map, a
multi-signal result model (metrics / logs / spans / profiles), and a feedback
side channel.

Because generated messages cannot carry hand-written methods, this package
exposes free functions and aliases:

- **Value constructors** — `Double`, `Int`, `Str`, `Timestamp`, `Array`, …
- **Attribute helpers** — `NewAttrs`, `AttrPut`, `AttrGet`, `AttrGetFold`,
  `Fingerprint`.
- **Feedback** — `Notify` / `Warn` append notifications to a `Result`.
- **Aliases** — `qdata.Query`, `qdata.Result`, `qdata.MetricSeries`, … plus enum
  re-exports (`SignalMetrics`, `ContextRange`, `MatchEqual`, …).
