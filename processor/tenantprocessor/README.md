# tenant processor

Resolves the tenant for a query and, optionally, registers an enforced label
matcher so downstream [queryrewrite](../queryrewriteprocessor) isolates the tenant's
series.

The tenant is read from a request header (Cortex/Mimir-style `X-Scope-OrgID` by
default), then falls back to `default`. When no tenant resolves and `required`
is set, the query is rejected with an `Unauthenticated` error.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `header` | `X-Scope-OrgID` | Request header carrying the tenant id. |
| `default` | `""` | Tenant used when the header is absent. |
| `required` | `false` | Reject queries with no resolvable tenant. |
| `enforce_label` | `""` | If set, register an equality matcher on this label with the resolved tenant. |

```yaml
processors:
  tenant:
    header: "X-Scope-OrgID"
    default: "anonymous"
    enforce_label: "tenant_id"
```
