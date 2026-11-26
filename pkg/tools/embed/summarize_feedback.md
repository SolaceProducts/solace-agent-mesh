{%- if rows.size == 0 -%}
## 🟦 No feedback — {{ windowLabel }}

No feedback in this window.
{%- else -%}
## 👍👎 Feedback summary — {{ windowLabel }}

| Agent | Type | Negative | Positive | Negative ratio | Activities |
|---|---|---|---|---|---|
{% for r in rows -%}
| `{{ r.agent }}` | {{ r.type }} | {{ r.negative }} | {{ r.positive }} | {{ r.negativeRatio }} | {{ r.activities }} |
{% endfor %}
{%- if truncated %}
Showing the first {{ rows.size }} by {{ sortLabel }}; narrow the window or name an agent to see the rest.
{% endif -%}
{%- if sample.size > 0 %}
### Recent negative comments

{% for s in sample -%}
- **{{ s.when }}** — `{{ s.agent }}` — `{{ s.user }}`: `{{ s.comment }}` (`{{ s.id }}`)
{% endfor -%}
{%- endif -%}
{%- endif -%}
{%- if unmatched != "" %}

No agent or workflow matched {{ unmatched }}.
{% endif -%}
