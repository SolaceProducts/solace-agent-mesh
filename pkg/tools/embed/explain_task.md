## {% if queried.statusEmoji != "" %}{{ queried.statusEmoji }} {% endif %}`{{ queried.name }}` — {{ queried.status }} at {{ queried.at }}

### Execution tree

```
{% for line in treeLines -%}
{{ line }}
{% endfor -%}
```

### Timeline

{% if timeline.size == 0 -%}
No events recorded.
{% endif -%}
{% for e in timeline -%}
- {% if e.when != "" %}**{{ e.when }}** — {% endif %}`{{ e.kind }}`{% if e.summary != "" %} `{{ e.summary }}`{% endif %}
{% endfor -%}
{% if rootCause != "" %}
### Root cause

```text
{{ rootCause }}
```
{% endif -%}
{% if related.size > 0 %}
### Related tasks

| Task | Task ID | Type | Status | Duration | Relation |
|---|---|---|---|---|---|
{% for r in related -%}
| `{{ r.name }}` | `{{ r.id }}` | {{ r.type }} | {{ r.statusEmoji }} {{ r.status }} | {{ r.duration }} | {{ r.relation }} |
{% endfor -%}
{% endif -%}
{% if treeCapped %}
> ⚠️ Partial tree — deeper sub-tasks and/or ancestors were omitted; the real failure may be in a child not shown here. Ask me to explain the lowest child task to drill down, and treat the top node as an ancestor, not necessarily the root.
{% endif -%}
{% if eventsSampled %}
> ℹ️ Some events were sampled.
{% endif -%}
