# responsefilter processor

Response transformation. It runs on the way out and reshapes a `qdata.Result`:
dropping internal attributes, masking sensitive values, and (for cumulative
counters returned without a rate function) attaching a feedback notification per
the QLSWG side-channel guidance.

Applies to every payload's attributes: metrics series, log records, spans, and
the rows of a relational cross-signal `Table`. A join copies each side's
attributes into its rows, so scrubbing the table is what keeps a dropped or
masked label from being laundered through a cross-signal query; a dropped key
also leaves the table's declared `columns` schema. A payload this processor does
not know how to scrub fails the query rather than being returned unfiltered.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `drop_labels` | `[]` | Attribute keys removed from every series/record/span/table row (and from the table schema). |
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
