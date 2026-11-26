{%- if rows.size == 0 -%}
## 🟦 No feedback — {{ windowLabel }}

No feedback matched this filter in this window.
{%- else -%}
## 👍👎 Feedback — {{ windowLabel }}

| Timestamp | Agent / Workflow | User | Rating | Comment | Feedback ID |
|---|---|---|---|---|---|
{% for r in rows -%}
| {{ r.when }} | `{{ r.agent }}` | `{{ r.user }}` | {{ r.rating }} | {% if r.comment == "—" %}—{% else %}`{{ r.comment }}`{% endif %} | `{{ r.id }}` |
{% endfor %}
{%- if truncated %}
Showing the newest {{ rows.size }}; more exist. Narrow the window or add a rating filter to see the rest.
{% endif -%}
{%- endif -%}
{%- if unmatched != "" %}

No agent or workflow matched {{ unmatched }}.
{% endif -%}
