# responsefilter processor

Response transformation. It runs on the way out and reshapes a `qdata.Result`:
dropping internal attributes, masking sensitive values, and (for cumulative
counters returned without a rate function) attaching a feedback notification per
the QLSWG side-channel guidance.

Applies to every signal's attributes (metrics, logs, spans).

## Config

| Key | Default | Description |
| --- | --- | --- |
| `drop_labels` | `[]` | Attribute keys removed from every series/record. |
| `mask_labels` | `[]` | Attribute keys whose values are replaced with `mask_with`. |
| `mask_with` | `***` | Replacement value for masked attributes. |
| `warn_counter_without_rate` | `false` | Emit a feedback warning when a raw cumulative counter is returned. |

```yaml
processors:
  responsefilter:
    drop_labels: ["__internal__"]
    mask_labels: ["user_email"]
    warn_counter_without_rate: true
```
