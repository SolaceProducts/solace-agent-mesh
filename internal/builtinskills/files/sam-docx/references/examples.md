# docx_create_document — Worked Examples

## Multi-section report with numbered headings

```yaml
- type: heading
  level: 1
  text: "Incident Postmortem: 2026-08-12 Gateway Outage"
- type: paragraph
  text: "Prepared for the platform reliability review."
- type: table_of_contents
  toc_levels: "1-2"
- type: page_break
- type: heading
  level: 1
  text: "1. Summary"
- type: paragraph
  text: "A 14-minute gateway outage affected all HTTP entrypoints starting 03:12 UTC."
- type: heading
  level: 2
  text: "1.1 Impact"
- type: bullet_list
  items:
    - "100% of HTTP task submissions failed for 14 minutes"
    - "SSE streams for in-flight tasks disconnected"
    - "No data loss — tasks resumed from last checkpoint"
- type: heading
  level: 1
  text: "2. Timeline"
- type: table
  headers: ["Time (UTC)", "Event"]
  rows:
    - ["03:12", "Gateway pods begin crash-looping"]
    - ["03:19", "Alert fires, on-call paged"]
    - ["03:26", "Root cause identified: bad config rollout"]
    - ["03:26", "Rollback initiated"]
```

## Mixed-formatting paragraph

A paragraph combining plain and formatted spans uses `runs` instead of
`text`:

```yaml
- type: paragraph
  runs:
    - text: "Status: "
    - text: "DEGRADED"
      bold: true
      color: "C0392B"
    - text: " — see "
    - text: "section 3"
      italic: true
    - text: " for remediation steps."
```

## Table with a ragged row

Rows shorter than the widest row (or the header) are padded with empty
cells automatically — no need to pad manually:

```yaml
- type: table
  headers: ["Service", "Owner", "Status"]
  rows:
    - ["gateway", "platform-team", "healthy"]
    - ["notify", "platform-team"]        # missing Status — renders as an empty cell
```

## Table with a narrow label column

A short label beside a long description is the shape an even split handles
worst: the labels get far more room than they need and the prose column wraps
hard. `col_widths` pins the split — here a quarter to the labels, three quarters
to the prose, honoured exactly by every reader. Shares are relative, so `[1, 3]`
and `[25, 75]` produce the same table:

```yaml
- type: table
  headers: ["Principle", "Description"]
  col_widths: [1, 3]
  rows:
    - ["Event-Driven", "All inter-agent communication flows through the Solace event broker, enabling decoupling, resilience, and scalability."]
    - ["Composability", "Agents and workflows can be assembled like building blocks to address complex use cases."]
    - ["Observability", "Built-in telemetry, tracing, and health reporting give operators insight into the running mesh."]
```

Omit `col_widths` for tables whose columns hold comparable amounts of text (the
timeline and service tables above) — an even split already reads well, and the
table spans the full page width either way.

## Numbered list for a procedure

```yaml
- type: heading
  level: 2
  text: "Remediation Steps"
- type: numbered_list
  items:
    - "Roll back the config change"
    - "Scale gateway replicas to 2x baseline for 1 hour"
    - "Confirm SSE reconnection rate returns to baseline"
```

## Image sized to a fixed width, height auto-computed

Omit `height_in` to preserve the source image's aspect ratio at the given
width — a 1600x900 screenshot at `width_in: 6` renders at 6in x 3.375in:

```yaml
- type: image
  image_ref: "0"
  width_in: 6
```
