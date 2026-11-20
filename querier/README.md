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

## Query result caching

Set `cache` under a leaf pipeline to enable process-local result reuse and
coalescing. This example assumes an authenticated gateway supplies the tenant
identity and the named processors are configured.

```yaml
service:
  pipelines:
    metrics/default:
      acceptors: [otqp]
      processors: [tenant, authratelimit, simpleauthz, queryrewrite, responsefilter]
      dispatchers: [prometheus]
      cache:
        ttl: 30s
        max_bytes: 16777216
```

See the [pipeline cache documentation](../pipeline#result-cache) for eligibility,
capacity, and cancellation behavior.
