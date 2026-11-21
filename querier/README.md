# querier

The assembly layer — the analogue of the collector's `otelcol`.

- **`Factories`** — the set of component factories compiled into a distribution
  (populated by a builder-generated `components.go`).
- **`Config`** / **`LoadConfig`** — the runtime configuration. YAML is parsed
  into an untyped tree and structurally decoded with `mapstructure` (snake_case
  keys, matching the collector's `confmap`). Components are declared under
  `acceptors` / `processors` / `dispatchers` keyed by component ID and wired into
  named pipelines under `service.pipelines`.
- **`Build`** / **`Service`** — assembles the configured pipelines and the
  acceptors that feed them, then manages their lifecycle (`Start` / `Shutdown`).

See the repository root [`config.yaml`](../config.yaml) for a fully-commented
example.
