# `qdata.Query` as a cross-language query format: support & limitations

Design note for [#10](https://github.com/minuk-dev/opentelemetry-querier/issues/10).

> **Update — the structured IR shipped.** The `(expr, dialect)` DSL-text transport
> described below has been **removed**. `Query` now carries a language-neutral
> `QueryPlan` (a tree of `Select`/`TimeAgg`/`Aggregate`/`Function`/`BinaryOp`/
> `Literal` nodes reusing the `Predicate` filter): acceptors parse their native
> query into it (PromQL via the upstream parser; LogQL/Lucene via self-contained
> subset parsers), and dispatchers render it back to their backend (PromQL/LogQL
> text, Elasticsearch query DSL). Enforcement is now language-neutral — the
> query-rewrite processor AND-s the enforced predicates into every `Select`
> filter, so boolean (OR/NOT) enforcement composes and a backend that cannot
> render a shape fails closed at render time. This realizes the Phase 3 §4.3 IR
> the sections below deferred. The historical analysis is kept for context.

Status: **Phases 0–3 implemented.** Phases 0–2 (IR + language-neutral enforcement)
shipped first; Phase 3 (cross-signal) then landed in
[#24](https://github.com/minuk-dev/opentelemetry-querier/issues/24) — the `Query.signals`
set, the relational `Result.Table` payload, and `crosssignaldispatcher`, which executes
a cross-signal plan by fan-out to per-signal backends plus an in-process join rather than
requiring a unified store.

## Question

Can one `qdata.Query` be the neutral carrier for Lucene / LogQL / PromQL / SQL?

## Two layers (QLSWG spec)

The spec splits this into two layers; the querier sits firmly on the first.

| Layer | Mechanism in code | Status |
| --- | --- | --- |
| **DSL text transport** (§4.1 best-effort proxy) | `Query.expr` (raw text) + `Query.dialect` (language tag) | ✅ carries any DSL opaquely |
| **Structured IR / plan** (§4.2–4.3) | — none — | ❌ no language-neutral AST |

The one place a query is *understood* rather than *carried* is
`processor/queryrewriteprocessor`: it parses PromQL with the upstream Prometheus parser and
injects `enforced_matchers` into every `VectorSelector`. `prometheusdispatcher` then ships
`expr` verbatim as the Prometheus API `query` form field.

So today: **transport is universal; comprehension is PromQL-only and hard-wired.**

## The three real gaps (and a correction)

**Gap A — comprehension isn't pluggable.** `queryrewrite` *is* a PromQL parser +
injector, but that shape isn't abstracted. Adding LogQL/SQL understanding means
copying its structure, not registering into it.

**Gap B — `enforced_matchers` is a flat label conjunction.**
`[]LabelMatcher{name, op, value}` is actually reasonably language-neutral (attribute
predicates, implicitly AND-ed). The real limitation is (1) no injectors exist for
non-PromQL dialects, and (2) it can't express boolean composition (OR/NOT/nesting) —
fine for tenant isolation, insufficient for richer enforcement.

**Gap C — `signal` is a single enum.** A SQL query joining metrics + logs has no
single signal.

**Correction to the issue's premise.** The issue says "the result model is already
multi-signal… the query side is the gap." That is only half true: `Result.data` is a
`oneof {Metrics, Logs, Spans}` — a *single* payload. A cross-signal **join** yields
rows like `(metric_value, log_message)` that are neither Metrics nor Logs. So true
cross-signal is **also a result-side gap**: it needs a relational/tabular payload.
This changes the scope of Gap C.

## Design — four phases, increasing cost

### Phase 0 — Pin the `dialect` contract ✅ done

Register the canonical dialect tags (`promql`, `logql`, `lucene`, `sql`) and state the
invariant:

- a **dispatcher** must reject or pass-through a `dialect` it doesn't understand;
- a **processor** must no-op on dialects it can't parse (queryrewrite already does this,
  see `processor/queryrewriteprocessor/queryrewriteprocessor.go` dialect guard).

Implemented: the canonical tags and the contract live in `qdata`
(`Dialect{PromQL,LogQL,Lucene,SQL}`, `QueryDialect`, `KnownDialect`; see the doc
comment there). `prometheusdispatcher` now enforces the dispatcher half — it rejects any
non-PromQL dialect with `CodeInvalidArgument` instead of shipping the text to the
Prometheus API. Removes ambiguity about who may touch `expr`.

### Phase 1 — Make comprehension pluggable (high-value step)

Extract `queryrewrite`'s shape into an interface and a registry:

```go
// One implementation today (PromQL); LogQL/SQL/Lucene register later.
type DialectRewriter interface {
    Dialect() string
    Enforce(expr string, preds []*qdata.LabelMatcher) (string, error)
}
```

`queryrewrite` becomes the PromQL registrant; the processor just looks up
`query.dialect` in the registry. This is the natural generalization of code that
already exists, and unlocks per-dialect injectors without touching proto.

### Phase 2 — Generalize enforcement representation ✅ done

Keep the flat `enforced_matchers` as the 90% isolation path; add an *optional*
recursive predicate for the rest:

```proto
message Predicate {
  oneof node {
    LabelMatcher leaf = 1;
    BoolExpr     bool_expr = 2;   // AND/OR/NOT over child Predicates
  }
}
// Query gains: repeated Predicate enforced_predicates = 11;
```

Only dialects whose injector supports it consume it; PromQL keeps using the flat list.

Implemented (representation layer): `Predicate` / `BoolExpr` / `BoolOp` and
`Query.enforced_predicates` are in the proto and re-exported from `qdata`, with
constructors (`LeafPredicate`, `BoolPredicate`), a `ValidatePredicate`
well-formedness check, and `FlattenConjunction` — which reduces a pure
AND-of-leaves forest to a flat matcher list so a label-oriented injector can
consume the common case and fail closed on real boolean composition (OR/NOT).
Consumption is wired in `processor/queryrewriteprocessor`: `ProcessQuery` folds
`enforced_predicates` into the flat matcher list via `FlattenConjunction` before
delegating to the dialect rewriter, and fails closed when the tree needs OR/NOT
that a label-selector injector can't apply. A dialect whose injector natively
understands boolean composition (SQL `WHERE`, Lucene) can later consume the tree
directly instead of flattening.

### Phase 3 — Cross-signal (implemented via in-process join, #24)

Two coupled changes, both shipped:

1. `Query.signals` (repeated `Signal`) generalizes the single `signal` field; the plan
   is authoritative and `qdata.PlanSignals` / `qdata.QuerySignals` read the set.
2. a new relational `Result` payload — `Table` = repeated `Row` (`KeyValueList`) — added
   to the `Result.data` oneof so cross-signal join output is representable.

This is where a real §4.3 IR (Substrait-style plan) lands. Rather than wait for a
unified metrics+logs store, `crosssignaldispatcher` executes a cross-signal plan by
**fan-out + in-process join**: a top-level `BinaryOp` whose two operands are
single-signal subtrees is dispatched to each signal's own backend (Prometheus, Loki),
each result is normalized to a relational table (metric series → rows, log records →
rows), and the two are inner-joined on the `BinaryOp`'s matching labels (or the tables'
shared columns) into a `Result.Table`. A single-signal plan is delegated whole, so the
dispatcher also works as a signal-aware router. Anything it cannot express — a
multi-signal plan that is not a top-level join, an operand spanning more than one
signal, a join with no usable key — fails closed. A native cross-signal backend (SQL
over a unified store) can later replace the in-process join without changing the
`Query`/`Result` shape.

## Recommendation

The issue's "not actionable yet" verdict held only for the **IR** (Phase 3).
**Phases 0–2 were actionable and low-risk** — the honest generalization of code that
already existed, turning "queryrewrite happens to parse PromQL" into "dialects
register comprehension" (Phase 1) and "enforcement can express boolean composition"
(Phase 2). They have shipped, and #10 was scoped down to them and closed.

Phase 3 (cross-signal IR) is **implemented** in
[#24](https://github.com/minuk-dev/opentelemetry-querier/issues/24): the `Query.signals`
set + relational `Result.Table` payload, plus `crosssignaldispatcher`, which executes a
cross-signal plan end-to-end by fan-out to per-signal backends and an in-process join —
so it no longer waits on a unified backend. A native cross-signal store can later slot in
behind the same `Query`/`Result` shape.
