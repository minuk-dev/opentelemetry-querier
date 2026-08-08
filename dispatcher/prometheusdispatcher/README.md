# prometheus dispatcher

Renders a `qdata.Query` to the **Prometheus HTTP query API**, executes it against
an upstream, and parses the JSON response back into a `qdata.Result`.

- Instant context → `POST /api/v1/query`; range context → `/api/v1/query_range`.
- The resolved tenant is forwarded via `tenant_header`.
- Series carry `METRIC_TYPE_UNSPECIFIED` (Prometheus is type-less; per the QLSWG
  spec this is UNKNOWN, not an assumed GAUGE).
- Upstream `warnings` are surfaced through the result's feedback channel.

## Rendering constraints

The plan is rendered to PromQL *text*, so every identifier it interpolates —
label names, `by` / `without` / `on` / `ignoring` / `group_left` label lists, and
function names — must be a bare PromQL identifier (`[a-zA-Z_][a-zA-Z0-9_]*`).
Anything else is rejected with `CodeInvalidArgument` before the upstream is
contacted, because an identifier carrying PromQL syntax could close the construct
it sits in and append a second selector that the enforced tenant matchers never
reach (issue #64). Matcher *values* are unaffected — they are quoted.

Note this rejects dotted OpenTelemetry attribute names (`http.status_code`),
which have no unquoted PromQL spelling. Supporting them means emitting
Prometheus 3.x UTF-8 quoted label names, which is not implemented.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `endpoint` | `http://localhost:9090` | Upstream base URL. |
| `tenant_header` | `X-Scope-OrgID` | Header used to forward the tenant id. |
| `timeout` | `30s` | Per-request timeout. |

```yaml
dispatchers:
  prometheus:
    endpoint: "http://localhost:9090"
    timeout: 30s
```
