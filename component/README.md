# component

Core vocabulary shared by every querier component — the analogue of
`go.opentelemetry.io/collector/component`.

Provides:

- **`Type`** — a validated component type name (e.g. `otqp`).
- **`ID`** — a `type` or `type/name` instance identifier (decodes from text).
- **`Component`** — the `Start(ctx, Host)` / `Shutdown(ctx)` lifecycle contract.
- **`Config`** — the marker type for component configuration, plus an optional
  `Validator`.
- **`Factory`** — the base factory contract (`Type()`, `CreateDefaultConfig()`).
- **`Settings`** / **`BuildInfo`** — per-instance context handed to a factory's
  `Create*` method.

Every acceptor, processor and dispatcher builds on these, so a distribution can
be composed from independently-authored modules.
