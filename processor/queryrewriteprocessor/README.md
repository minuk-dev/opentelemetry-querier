# queryrewrite processor

Language-neutral query enforcement on the structured `qdata.QueryPlan`. It
ANDs registered matchers and predicates from the [tenant](../tenantprocessor)
and [simpleauthz](../simpleauthzprocessor) processors, plus static configuration,
into every `Select` node's filter. Each dispatcher renders the constrained plan
into its backend language; there is no PromQL-only parsing or dialect bypass.

User filters are preserved. For example, a user filter `tenant_id="other"` and
an enforced `tenant_id="acme"` both remain in the conjunction. The conflicting
conditions match no data; enforcement does not replace the user's matcher.

Run tenant resolution before consumers of `from_tenant`, and run queryrewrite
after every processor that registers constraints. Tenant and authorization
processors register constraints; queryrewrite is the stage that applies them to
the plan. Configure tenant resolution as required when it is an isolation input.

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
