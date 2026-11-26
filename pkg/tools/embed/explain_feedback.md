## {{ rating }} on `{{ agent }}`{% if when != "" %} — {{ when }}{% endif %}
{% if comment != "" %}
Comment:

```text
{{ comment }}
```
{% endif -%}
{% if refused %}
> You can see this feedback, but not its chat session.
{% elsif tooDeep %}
> This chat session is nested too deeply to replay.
{% else -%}
{% if retentionGap %}
> The rated turn is no longer retained.
{% endif -%}
{% if turns.size == 0 %}
> No turns of this session are still retained.
{% endif -%}
{% endif -%}
{% if earlierOmitted %}
> Earlier turns not shown.
{% endif -%}
{% for t in turns %}
### {% if t.rated %}Rated turn{% else %}Turn {{ t.number }}{% endif %}{% if t.failed %} — failed{% endif %}

{% if t.request != "" -%}
Request: `{{ t.request }}`

{% endif -%}
Final status: {% if t.statusEmoji != "" %}{{ t.statusEmoji }} {% endif %}{{ t.status }}

```
{% for line in t.treeLines -%}
{{ line }}
{% endfor -%}
```
{% if t.eventsOmitted %}
Timeline left out to fit the size limit.
{% else %}
{% if t.timeline.size == 0 -%}
No events recorded.
{% endif -%}
{% for e in t.timeline -%}
- {% if e.when != "" %}**{{ e.when }}** — {% endif %}`{{ e.kind }}`{% if e.summary != "" %} `{{ e.summary }}`{% endif %}
{% endfor -%}
{% endif -%}
{% if t.rootCause != "" %}
Root cause:

```text
{{ t.rootCause }}
```
{% endif -%}
{% endfor -%}
