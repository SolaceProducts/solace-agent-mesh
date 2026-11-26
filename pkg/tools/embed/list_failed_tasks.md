{%- if windowActivityCount == 0 -%}
## 🟦 No activity — {{ windowLabel }}

No tasks were submitted or completed in this window.
{%- elsif count == 0 -%}
## ✅ No failures — {{ windowLabel }}

{{ windowActivityCount }} tasks ran, none failed.
{%- elsif first > count -%}
## ⚠️ Failed tasks — {{ windowLabel }}

Page {{ pageNumber }} is past the end: {{ count }} failures fit on {{ totalPages }} page(s).
{%- else -%}
## ⚠️ Failed tasks — {{ windowLabel }} — showing {{ first }}–{{ last }} of {{ count }} (page {{ pageNumber }} of {{ totalPages }})

| When | Agent | Status | Duration | Task ID |
|---|---|---|---|---|
{% for r in rows -%}
| {{ r.when }} | `{{ r.agent }}` | {{ r.statusEmoji }} {{ r.status }} | {{ r.duration }} | `{{ r.id }}` |
{% endfor %}
{% if hasMore %}Ask for the next page for more. {% endif %}Ask me to explain a task by its ID for a full trace.
{%- endif -%}
