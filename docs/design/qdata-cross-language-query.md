# `qdata.Query` as a cross-language query format: support & limitations

Design note for [#10](https://github.com/minuk-dev/opentelemetry-querier/issues/10).
Status: **analysis / tracking**. Phases 0–1 are actionable; Phases 2–3 are tracked
design, not committed work.

## Question

Can one `qdata.Query` be the neutral carrier for Lucene / LogQL / PromQL / SQL?

## Two layers (QLSWG spec)

The spec splits this into two layers; the querier sits firmly on the first.

| Layer | Mechanism in code | Status |
| --- | --- | --- |
| **DSL text transport** (§4.1 best-effort proxy) | `Query.expr` (raw text) + `Query.dialect` (language tag) | ✅ carries any DSL opaquely |
| **Structured IR / plan** (§4.2–4.3) | — none — | ❌ no language-neutral AST |

The one place a query is *understood* rather than *carried* is
`processor/queryrewrite`: it parses PromQL with the upstream Prometheus parser and
injects `enforced_matchers` into every `VectorSelector`. `promdispatcher` then ships
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
  see `processor/queryrewrite/queryrewrite.go` dialect guard).

Implemented: the canonical tags and the contract live in `qdata`
(`Dialect{PromQL,LogQL,Lucene,SQL}`, `QueryDialect`, `KnownDialect`; see the doc
comment there). `promdispatcher` now enforces the dispatcher half — it rejects any
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

### Phase 2 — Generalize enforcement representation

Keep the flat `enforced_matchers` as the 90% isolation path; add an *optional*
recursive predicate for the rest:

```proto
message Predicate {
  oneof node {
    LabelMatcher leaf = 1;
    BoolExpr     bool = 2;   // AND/OR/NOT over child Predicates
  }
}
// Query gains: repeated Predicate enforced_predicates = 11;
```

Only dialects whose injector supports it consume it; PromQL keeps using the flat list.

### Phase 3 — Cross-signal (heaviest, out of scope now)

Two coupled changes:

1. `Query.signal` → a signal *set* (or `SIGNAL_UNSPECIFIED` = "the dialect decides");
2. a new relational `Result` payload (`Table` = repeated rows of `KeyValueList`) so join
   output is representable.

This is where a real §4.3 IR (Substrait-style plan) would land. Only justified once
there is a concrete cross-signal backend (e.g. SQL over a unified store); otherwise it
is speculative weight.

## Recommendation

The issue's "not actionable yet" verdict holds for the **IR** (Phase 3). But
**Phases 0–1 are actionable and low-risk**, and they are the honest generalization of
code that already exists — they turn "queryrewrite happens to parse PromQL" into
"dialects register comprehension." Scope the issue down to that; leave Phases 2–3 as
this tracked design note.
