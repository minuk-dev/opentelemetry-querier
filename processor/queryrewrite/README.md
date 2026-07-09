# queryrewrite processor

Query transformation for the **PromQL** dialect. It weaves enforced label
matchers — from the [tenant](../tenant) processor and from static config — into
every vector/matrix selector of the PromQL AST, matching the prom-label-proxy
technique, so a query cannot escape its tenant or scope. Enforcement always wins:
a matching user matcher on the same label is replaced.

Non-PromQL dialects pass through untouched.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `enforce_labels` | `[]` | List of matchers to inject into every query. |

Each `enforce_labels` entry:

| Key | Description |
| --- | --- |
| `name` | Label name. |
| `value` | Static value (ignored when `from_tenant` is true). |
| `from_tenant` | Use the resolved tenant id as the value. |

```yaml
processors:
  queryrewrite:
    enforce_labels:
      - name: "tenant_id"
        from_tenant: true
```
