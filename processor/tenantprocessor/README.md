# tenant processor

Resolves the tenant for a query and, optionally, registers an enforced label
matcher so downstream [queryrewrite](../queryrewriteprocessor) isolates the tenant's
series.

The tenant is read from a request header (Cortex/Mimir-style `X-Scope-OrgID` by
default), then falls back to `default`. When no tenant resolves and `required`
is set, the query is rejected with an `Unauthenticated` error.

The header is the only source: a `tenant.id` already present in the query's
metadata is never read, and is overwritten (or, when nothing resolves, removed).
Metadata rides on the request message, so a client can set it over the wire — the
[acceptors](../../acceptor) strip it at ingress, and resolving from the transport
here keeps an acceptor that forgets to sanitize from silently reopening the spoof
(issue #65).

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
