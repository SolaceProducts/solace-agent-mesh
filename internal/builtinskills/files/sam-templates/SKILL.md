---
name: sam-templates
description: Author and use reusable report templates. AUTHOR — turn a finished report or artifact you just built into a template by capturing its unresolved embeds, parameterizing session-specific literals as @@KEY@@ substitutions, declaring its data as data_inputs, verifying it renders, and packaging it as a single downloadable .samt file to drop in a project or into a skill's assets/. USE — inspect a template's contract and render it from session data. Load this when the user wants to save a built report as a reusable template, or to generate a report from an existing template.
tags:
  - template
  - report
  - authoring
  - skill
---

# Templates Skill

This skill covers the full template lifecycle:

- **Authoring** (§ "What a template is" → "Package"): turn a one-off report you
  just built into a **reusable template** — `verify_template_candidate`, then
  `package_template`.
- **Using** (§ "Using a template"): render a template you already have (a `.samt`
  file — a session artifact, or one bundled in a skill's `assets/`) from session
  data — `read_template`, then `instantiate_template`.

If the user wants to *make* a report reusable, follow authoring. If they want to
*generate* a report from a template they already have, jump to "Using a template".

## What a template actually is

When you build a report, its embeds (`«artifact_content:...»`, Liquid blocks,
etc.) are resolved on the way to the user, but the artifact **stored in the
session keeps them unresolved** — that stored, pre-resolution form *is* the
template you want. Templatizing is the act of:

1. taking that raw form,
2. replacing the parts that were specific to *this* run with parameters, and
3. packaging it so it can be installed into a skill and re-run.

You read artifacts in their raw (unresolved) form, so the source material is
already in the right shape — the work is the parameterization, not recovery.

## Procedure

### 1. Read the source artifact raw

Call `load_artifact` on the report the user wants to templatize. You receive its
bytes **with embeds unresolved** — the `«artifact_content:…»` tokens come back
literally. Work from these exact bytes: the template body is this raw source with
`@@KEY@@` placeholders swapped in for session-specific literals, and the `«…»`
embed tokens preserved character-for-character.

### 2. Separate the reusable shape from the session-specific data

Walk the raw body and classify everything that was specific to this run. **Pick
the mechanism by the *shape* of the thing, not by whether you happen to know its
value.** This is the single most important authoring decision:

- **A scalar in a fixed slot** — a title, a quarter, an author, a date range, one
  headline number or threshold → replace with a `@@KEY@@` placeholder and declare
  it under `substitutions`. `@@KEY@@` is *only* for one value dropped into one
  spot.
- **A table, card grid, or list** — any repeating *markup* you produced by looping
  over data → do **not** make it a `@@KEY@@` fragment. Replace the whole structure
  with a `«««template_liquid:…»»»` block that loops a declared `data_input` and
  **emits the markup**, and declare that data as a `data_input`. See "Turning data
  into formatted output" and the anti-pattern example below. A `@@KEY@@` table
  forces whoever fills the template to hand-author the markup, guessing at your CSS
  classes and column order — the reliable way to make a rendered report look broken.
- **A chart is its own third type — not markup, and not a `@@KEY@@`.** For an HTML
  report a browser opens, a chart is a **Plotly (JS) graph**; a `template_liquid`
  block emits **only the JS data array** that feeds it, never the chart. The
  container `<div>` and the `Plotly.newPlot(...)` call are static HTML/JS you write
  once; the Liquid block produces just the `[{...}, …]` literal from a declared
  `data_input`. This is a *different shape* from a table (where Liquid emits the
  markup itself). (For a non-HTML target — PDF, PPTX, image, non-JS Markdown — a
  chart is instead a *rasterized image* you generate and embed.) See "Charts —
  emit data into JS" for the full pattern and both cases.
- **A non-chart image** (a logo, a photo, a pre-rendered diagram) → embed it with
  `«artifact_content:file >>> format:datauri»` and declare the file as a
  `data_input`. (A *chart* is not an image and is never rasterized in live HTML —
  render it with Plotly per the previous bullet.)
- **Embeds that point at this session's artifacts** — e.g.
  `«artifact_content:my_q3_sales.csv»` referencing a file that only exists in
  this conversation → **leave the embed exactly as it is** and *declare its
  target artifact as a `data_input`* in the sidecar, with a schema. The embed
  keeps naming the real artifact filename; the data_input makes that filename a
  named, schema-checked dependency of the template.

The rule in one line: **a table / card grid / list is a `template_liquid` block
that emits markup; a chart, in live HTML, is a Plotly JS graph whose data array a
`template_liquid` block emits (not the chart — rasterize it for PDF/PPTX/image
targets; see "Charts"); a scalar in a fixed slot is `@@KEY@@`; a non-chart image
is an `artifact_content … datauri` embed. Both data-bearing shapes read a declared
`data_input` — never `@@KEY@@`.**

Everything else (the prose, structure, styling, and embeds that are genuinely
generic) stays verbatim.

Two mechanics to get right:

- **The embed target is the artifact filename, never the data_input key.** An
  embed reads an artifact *by name* at serve time. `«artifact_content:data.csv»`
  resolves; `«artifact_content:employee_data»` (the binding key) does **not** —
  there is no artifact by that name, and verify will fail with
  "Artifact '…' not found". The binding key is only a label in the sidecar; it
  must not appear in the body.
- **The embed target and the data_input's `artifact:` field must be the same
  filename.** That match is what ties the body to the declared input.

A data_input's `description` is also the place to record **where the data came
from**. When you and the user obtained this run's data a particular way, fold
that origin into the description so a future fill has a head start finding
equivalent data. Describe the *origin and meaning* of the data, not the
procedure that produced it this run: say what the data is and the kind of source
it was drawn from (e.g. "closed-won opportunities for the quarter, one row per
month, from the CRM"), never a tool name, call signature, or query params. Tools
and agents change between the authoring run and a later fill — a semantic
description of the data outlives them and still points a future run at the right
data, whereas a "call tool X with params Y" runbook goes stale and can mislead.
It is a hint, never validated.

### 3. Author the body (as an artifact) and the `.template.yaml` sidecar

Produce two things:

- the **template body**, which you **save as a session artifact** with a
  `«««save_artifact: …»»»` block — the raw report with `@@KEY@@` placeholders and
  the embeds left intact (still naming their real artifact filenames). Saving
  through a save_artifact block preserves the late embeds (`«artifact_content:…»`,
  `«««template_liquid:…»»»`) **unresolved** in the stored artifact — that stored
  form *is* the template body. **Never pass the body text as a tool argument** —
  the tools take a `body_artifact` filename. A whole HTML report (with its CDN
  `<script>` tags) repeated in tool calls bloats the conversation and can wedge
  the model session; keeping it in an artifact avoids that entirely.
- the **sidecar** (`.template.yaml`) declaring the contract (small YAML, passed
  inline).

For example, save the body as an artifact named `report.html`:

```
«««save_artifact: filename="report.html" mime_type="text/html" description="Quarterly report template"
# @@report_title@@

Prepared by @@prepared_by@@.

«artifact_content:sales_data.json»
»»»
```

For that body — which contains, verbatim:

```
# @@report_title@@

Prepared by @@prepared_by@@.

«artifact_content:sales_data.json»
```

the matching sidecar is:

```yaml
template:
  file: report.html            # bare filename of the asset
  output_filename: report.html # optional: name when instantiated
  mime_type: text/html         # must be a text MIME — templates carry embeds
  description: Quarterly sales report
substitutions:
  report_title:
    description: H1 title
    default: "Quarterly Report"
  prepared_by:
    description: Report author
    required: true
data_inputs:
  sales_rows:                  # logical label — does NOT appear in the body
    description: Monthly closed-won sales for the quarter, one row per month, from the CRM
    artifact: sales_data.json  # MUST equal the embed target in the body
    mime_type: application/json
    required: true
    schema: { ... }            # JSON Schema, OR `columns:` for CSV — never both
```

Note the body embeds `sales_data.json` (the real artifact name), and the
`sales_rows` data_input's `artifact:` field is that same `sales_data.json`. The
binding key `sales_rows` never appears in the body.

### 4. Confirm the declared inputs with the user

The data-dependency contract is the easy thing to get subtly wrong. Before
verifying, tell the user explicitly which embeds you kept and which artifacts you
declared as `data_inputs`, e.g. "The body still embeds `sales_data.json`, which I
declared as the `sales_rows` data input (a JSON array, schema attached)." Let
them correct you. Confirm you did **not** inline any resolved data.

### 5. Verify before you package

Call **`verify_template_candidate`** with `body_artifact` (the filename you saved
the body under, e.g. `report.html`), the sidecar, and sample `substitutions`.
Point the declared `data_inputs` at real session artifacts so the data validation
and the serve-faithful embed resolution both run.

- `{ok:true, preview_artifact:"<name>"}` — the fully rendered report is **saved
  as that session artifact, not returned inline** (a resolved report is large,
  and pushing HTML back into the conversation is slow and can wedge the model
  session). To let the user eyeball it, `artifact_return` the `preview_artifact`
  — do not try to read the rendered HTML back into your own context.
- `{ok:false, errors:[...]}` — fix the body, the sidecar, or the session data
  per the echoed contract and retry. **Do not package on a failed verify.**

An `inlined_data` error means you declared a `data_input` whose artifact the body
never references. Usually the embed target is wrong — check it names the
artifact *filename* (`«artifact_content:data.csv»`), not the binding key — and
that it matches the `data_input`'s `artifact:` field.

### 6. Package

Once verify is clean, call **`package_template`** with the same `body_artifact`
and sidecar. It returns a **single downloadable `.samt` file** (mime
`application/vnd.sam-template+zip`) that bundles the template body and its
contract into one self-contained file. Its name keeps the render format ahead of
`.samt` (e.g. `report.html.samt`). It is binary, so the embeds inside survive
download unresolved — it stays a template, not a rendered report.

### 7. Hand it off

The single `.samt` file is the deliverable. It can be:

- **instantiated directly** later with `instantiate_template` (source_artifact) —
  see "Using a template" below;
- **dropped into a project** so any chat there can render it; or
- **committed into a skill's `assets/`** as that one `.samt` file (no unpacking —
  a skill ships the template as a single file, exactly like the artifact form).

## Using a template

You have a template already — a single **`.samt` file**, either in the session
(from `package_template`, a project, or a prior download) or **bundled in a
loaded skill's `assets/`** — and you want to render it from data.

### 1. Read the contract first

Templates declare `substitutions` (@@KEY@@ values you supply) and `data_inputs`
(session artifacts the body reads, by filename, with a schema). Read the contract
with **`read_template`** before instantiating — one tool reads either source:

- session `.samt` → `read_template` with `source_artifact`.
- skill-bundled `.samt` → `read_template` with `skill_name` + `asset` (the
  `.samt` path under the skill's `assets/`, e.g. `assets/report.html.samt`).

It returns the sidecar contract by default; the body can be large, so pass
`include_body=true` only if you actually need to see the body (a large body is
saved as an artifact and its name returned rather than dumped inline).

### 2. Make the data_inputs available

You have two ways to satisfy each declared `data_input`:

- **Bind by logical name (preferred).** Pass a `data_inputs` map to
  `instantiate_template` — `{ <binding name>: <an artifact in this session> }`,
  where the value is `filename` or `filename:version`. The tool rewrites the
  document's references from the declared filename to your bound artifact, so
  you never reproduce the sidecar's exact filenames. Bind "the artifact I
  already have" to "the input the template wants".
- **Save under the declared filename yourself.** For each declared `data_input`,
  ensure a session artifact **of that exact filename** exists, matching the
  declared columns/schema, then omit `data_inputs`. (The body reads
  `«artifact_content:<filename>»`, so the artifact name must match the sidecar's
  `artifact:` value.)

If you still need to *gather* the data, read each `data_input`'s `description`
first — it often records where the author's data came from. Treat that as a
hint toward equivalent data, not a fixed recipe: it describes the data's origin,
not a tool to call, and the tools available now may differ from the author's.

### 3. Instantiate

Call **`instantiate_template`** with the substitutions, your `data_inputs`
bindings (or the pre-saved data), and either `source_artifact` (the `.samt`) or
`skill_name` + `asset`. It validates your data against the contract, rewrites
the document's data references to the bound artifacts **pinned to the exact
version validated** (an omitted version resolves to the latest right now and is
still pinned), and checks the result resolves cleanly before saving; on a
contract failure it fails with the findings echoed — fix the data, bindings, or
substitutions and retry. The saved artifact keeps its embeds and re-renders live
on each download — so `artifact_return` it to the user. Because the data
references are version-pinned, later writes to those artifacts never change a
document already instantiated; to render newer data, instantiate again.

## Turning data into formatted output — embeds vs. Liquid

A template's output format is whatever its `mime_type` declares — **not just
HTML**. It can be Markdown, JSON, XML, YAML, CSV, SVG, an HTML fragment, or any
other text format. The two mechanisms below are format-agnostic: they inject or
render `data_input` values into *whatever* text shape you're producing. A common
non-HTML use is templating a **JSON** (or YAML/XML) file — dropping one data
structure into a field, or building an array from CSV rows — see the JSON example
below.

A template usually needs to *render* its data (a table, cards, a chart, a JSON
array), not just dump it. There are exactly two mechanisms. **There is no such
thing as a custom named formatter** — do not invent `>>> format:kpi_cards`,
`>>> format:table`, `>>> format:order_rows`, or similar. They silently fall back
to plain text, so the output looks broken with raw data pasted into your shell.

**1. `«artifact_content:file >>> …»` — inline the artifact, optionally reshaped.**
The `>>> format:` modifier only selects a *serialization*, and the ONLY valid
values are: `text`, `json`, `json_pretty`, `csv`, `datauri`, or a numeric spec
like `.2f`. Use it to drop raw/serialized content in:

```
<img src="«artifact_content:logo.png >>> format:datauri»">
<pre>«artifact_content:data.csv >>> format:csv»</pre>
```

**2. `«««template_liquid: …»»»` — render data into any text format.** This is what
you want for tables, KPI cards, lists, **and JSON/XML/YAML structures** — and, in a
distinct form, for a **chart's data array** (a chart is a Plotly graph; the Liquid
block emits only the JS data feeding it, *not* the chart — see "Charts"). It loads
a data artifact, parses it, and runs a Liquid template so you emit exactly the
text you want — HTML markup, Markdown, or the fields of a JSON/YAML/XML document.
Params are `key="value"` pairs on the first line (`data` required; optional
`jsonpath`, `limit`). For a **CSV** artifact the Liquid context is `headers`
(column names) and `data_rows` (each row is a positional array — `row[0]`,
`row[1]`, …):

```
«««template_liquid: data="data.csv"
<table>
  <thead><tr>{% for h in headers %}<th>{{ h }}</th>{% endfor %}</tr></thead>
  <tbody>
  {% for row in data_rows %}
    <tr>{% for cell in row %}<td>{{ cell }}</td>{% endfor %}</tr>
  {% endfor %}
  </tbody>
</table>
»»»
```

KPI cards and category rollups are the same idea — loop `data_rows`, accumulate
with Liquid, emit your `<div class="card">…` markup.

### The Liquid context depends on the artifact's shape

The parsed artifact is not handed to Liquid verbatim — it is structured into a
context whose variable names depend on what it parsed to. There are **five**
shapes. Reaching for the wrong one renders **blank output and no error**: the
artifact still satisfies its declared schema, so instantiation succeeds and only
the emitted text is empty.

| The `data=` artifact parses to | Liquid context | Access |
|---|---|---|
| CSV | `headers`, `data_rows` | `{% for row in data_rows %}{{ row[0] }}` — positional |
| JSON/YAML **array** | `items` | `{% for o in items %}{{ o.total }}` — named fields |
| JSON/YAML **object** | **its own keys, as top-level variables** | `{{ source_url }}` — no loop, no wrapper |
| JSON/YAML **scalar** | `value` | `{{ value }}` |
| any other MIME type | `text` (the raw string) | `{{ text }}` |

The shape comes from the artifact's MIME type, or from its file extension when the
stored type is generic (`text/plain`, empty, `application/octet-stream`): a `.csv`
saved as `text/plain` is still a CSV. Such a file also keeps `text` (the raw
string) next to the structured variables. If it is malformed, the template renders
from `text` alone when it can, and shows a parse error otherwise.

**The object case is the one most often gotten wrong.** A `data_input` whose
artifact is a JSON *object* rather than an array puts its keys straight into
scope — there is no `items` to loop and no wrapper to reach through:

```
«««template_liquid: data="cam_1_counts.json"
<p>Source: {{ source_url }} · {{ count }} detections</p>
»»»
```

**A nested map iterates as `[key, value]` pairs, not as objects.** When a field
inside that object is itself a map (`{"counts": {"car": 8, "truck": 2}}`), each
iteration yields a two-element array — index it positionally:

```
{% for pair in counts %}<tr><td>{{ pair[0] }}</td><td>{{ pair[1] }}</td></tr>{% endfor %}
```

`{% for c in counts %}{{ c.name }}{% endfor %}` is the natural guess and it emits
**empty cells** — `c` is an array, so `.name` resolves to nothing.

🛑 **Map iteration order is NOT stable.** Keys come back unsorted, so identical
data renders its rows in a different order from one render to the next — and
because embeds and Liquid resolve at *download* time, one saved report can reorder
between two views of it. When order matters, either emit the data as a JSON
**array** (which keeps its order) or drive the loop from a fixed key list:

```
{% assign classes = "car,truck,bus" | split: "," %}
{% for c in classes %}<tr><td>{{ c }}</td><td>{{ counts[c] }}</td></tr>{% endfor %}
```

⚠️ **`limit` only truncates `data_rows` and `items`.** On an object or scalar
context it is accepted and silently does nothing.

### The `@@KEY@@`-for-a-table anti-pattern (do not do this)

The most common — and most damaging — authoring mistake is turning a table, card
grid, or chart into a `@@KEY@@` substitution instead of a `template_liquid` block.
One placeholder *looks* convenient, but it moves the job of *writing the markup*
onto whoever fills the template later. They only see your substitution's
description, never your CSS, so they guess the class names and column order — and
guess wrong. The report renders with unstyled cells, wrong badge colors, and
misaligned columns even though instantiation "succeeded".

**Wrong** — a `@@KEY@@` the filler must hand-author as an HTML fragment:

```html
<table>
  <thead><tr><th>Agent</th><th>Invocations</th><th>Success</th></tr></thead>
  <tbody>@@agent_table_rows@@</tbody>   <!-- filler must hand-write every <tr> -->
</table>
```
```yaml
substitutions:
  agent_table_rows:
    description: HTML <tr> rows for the agent table   # forces the filler to author markup
    required: true
```

**Right** — the same table as a Liquid block over a declared `data_input`. The
row markup, *with your exact classes*, lives in the template; the filler supplies
**data**, not markup:

```html
<table>
  <thead><tr><th>Agent</th><th>Invocations</th><th>Success</th></tr></thead>
  <tbody>
«««template_liquid: data="agent_perf.csv"
{% for row in data_rows %}    <tr><td>{{ row[0] }}</td><td>{{ row[1] }}</td><td><span class="badge-green">{{ row[3] }}%</span></td></tr>
{% endfor %}»»»
  </tbody>
</table>
```
```yaml
data_inputs:
  agent_perf:
    description: Per-agent performance for the period, one row per agent
    artifact: agent_perf.csv
    columns: [ ... ]
```

The class names (`badge-green`) and column order are now fixed *in the template*,
so every render is correct and identical — the filler cannot drift from your CSS
because they never touch the markup. Every table, KPI-card grid, and list in your
report should follow this **Right** shape. The only things that stay `@@KEY@@` are
scalars in fixed slots (the title, the date range, one headline number).

**A chart is the same principle in a different shape.** The Liquid block does **not**
emit the chart markup — it emits **only the JS data array**, and static Plotly JS
renders the graph. Same discipline (data comes from a `data_input`, never
hand-typed or `@@KEY@@`), different output (a data literal, not `<tr>`s):

```html
<div id="agentChart"></div>            <!-- static container; you write it once -->
<script src="https://cdn.plot.ly/plotly-3.0.1.min.js"></script>
<script>
  const rows = [
«««template_liquid: data="agent_perf.csv"
{% for row in data_rows %}    { agent: {{ row[0] | json }}, invocations: {{ row[1] }} },
{% endfor %}»»»
  ];
  Plotly.newPlot("agentChart",                       // static trace/layout you write once
    [{ type: "bar", x: rows.map(r => r.agent), y: rows.map(r => r.invocations) }],
    { autosize: true }, { responsive: true });
</script>
```

The `agent_perf.csv` here is the **same** declared `data_input` the table used —
one data source can feed both a table and a chart. See "Charts — emit data into
JS" for container/layout details. Never make a chart a `@@KEY@@`, and never
hand-type its data array.

The identical mechanics build **non-HTML** output. A templated JSON file
(`mime_type: application/json`) that turns CSV rows into a JSON array, with a
`@@KEY@@` substitution and an `«artifact_content»` embed for a nested blob:

```
{
  "report": "@@report_name@@",
  "generatedFrom": «artifact_content:source_meta.json >>> format:json»,
  "rows": [
«««template_liquid: data="data.csv"
{% for row in data_rows %}    { "category": {{ row[0] | json }}, "revenue": {{ row[1] }} }{% unless forloop.last %},{% endunless %}
{% endfor %}»»»
  ]
}
```

Mind valid JSON when you emit into it: quote/escape string cells with `| json`,
leave numbers bare, and handle trailing commas (e.g. `{% unless forloop.last %},{% endunless %}`).
`verify_template_candidate` renders it against sample data, so a malformed result
shows up in the preview — eyeball that it parses.

**In a template, the data artifact a `template_liquid` block reads is still a
`data_input`** — declare it in the sidecar exactly as you would for an
`artifact_content` embed (the `data="…"` filename is the `artifact:` value), so
the data stays swappable and schema-checked. `verify_template_candidate` renders
the Liquid block, so a bad loop or column reference shows up in the preview.

## Charts — emit data into JS, don't rasterize

This section applies **only when the render target is HTML a browser will open**
(so it can run JS). For any other target — a PDF, a PPTX, an image deliverable, a
Markdown doc destined for a non-JS renderer — an **interactive** chart is not an
option, and a **rasterized** chart (or a static diagram) is the correct choice:
generate the image with the chart tool and embed/reference it
(`<img src="«artifact_content:chart.png >>> format:datauri»">` in HTML-for-PDF, or
`«artifact_return:chart.png»` / a Markdown image for other formats). The
"don't rasterize" rule below is about *live HTML*, not a blanket ban on images.

For an **HTML** report a browser will open, do **not** embed a chart as a static
image (`«artifact_content:chart.png >>> format:datauri»`). A rasterized chart is
fixed, low-fidelity, and can't be hovered, zoomed, or re-themed. Instead, pull in
an interactive charting library from a CDN and feed it the data as a JS literal
that a `template_liquid` block emits. The chart then renders in the browser and
stays live against the data_input like everything else.

**The chart's data array MUST come from a `template_liquid` block that reads a
declared `data_input` — never hand-write the numbers.** It is tempting, when you
already know the values, to just type the JS array literal
(`const rows = [{ label: "Electronics", value: 15230.5 }, …]`) straight into the
body. Don't: those inlined numbers freeze the chart to *this* run's data, so the
"template" is really a one-off snapshot — the whole point (re-rendering against
fresh `data_input` data on each download) is lost. The literals must be *emitted
by the Liquid loop*, not written by you.

Use **Plotly** (`https://cdn.plot.ly/plotly-3.0.1.min.js`). The clean pattern is
one Liquid block that emits *just the data* as a JS array, then plain static JS
that builds the trace from it — data and chart config stay separate.

**Container structure matters.** Give Plotly a **clean, padding-free target div**
and put any card styling (padding, border, shadow, rounded corners) on a
**separate wrapper** that also sets `overflow: hidden`. Plotly sizes the plot to
its target element's full width *including padding*, then draws the SVG inside the
content box — so padding or a border-radius directly on the target makes the chart
overflow and bulge past the corners. Keep them separate:

```html
<style>
  .chart-card { background:#fff; border-radius:8px; box-shadow:0 2px 8px rgba(0,0,0,.08);
                padding:16px; overflow:hidden; }   /* styling + clips to the rounded corners */
  .chart     { width:100%; }                        /* clean, padding-free Plotly target */
</style>

<div class="chart-card">
  <div id="revenueChart" class="chart"></div>
</div>
<script src="https://cdn.plot.ly/plotly-3.0.1.min.js"></script>
<script>
  const rows = [
«««template_liquid: data="chart_data.csv"
{% for row in data_rows %}    { label: {{ row[0] | json }}, value: {{ row[1] }} },
{% endfor %}»»»
  ];
  Plotly.newPlot("revenueChart",
    [{ type: "bar", x: rows.map(r => r.label), y: rows.map(r => r.value) }],
    { autosize: true, title: "Revenue by Category", margin: { t: 40 } },
    { responsive: true });
</script>
```

Points that keep it robust:

- **Never put `padding` or `border-radius` on the Plotly target div.** Style a
  wrapper (`.chart-card`) with `overflow: hidden`; keep the target (`.chart`) a
  bare full-width box. This is what stops the chart bulging past rounded corners.
- Pass `{ autosize: true }` in the layout and `{ responsive: true }` in the config
  so the plot fills the container on load and tracks it on resize.
- The Liquid block emits **only** the `rows` array literal; the trace/layout are
  static JS outside the block. This keeps the loop trivial and the chart config
  readable.
- Pipe text cells through `| json` (`{{ row[0] | json }}`) so labels are safely
  quoted and JS-escaped. Emit numeric cells bare (`{{ row[1] }}`).
- For a **JSON array** data_input the loop is `{% for o in items %}` with named
  fields: `{ label: {{ o.category | json }}, value: {{ o.total }} }`.
- `chart_data.csv` is a normal `data_input` — declare it in the sidecar exactly
  as any other. `verify_template_candidate` renders the Liquid block, so a bad
  column index surfaces in the preview (the `<script>` won't run there, but the
  emitted JS array is visible and must look right).

## Guardrails

- **Never invent a `>>> format:<name>`.** The only valid formats are `text`,
  `json`, `json_pretty`, `csv`, `datauri`, and numeric specs. To render data as
  HTML/Markdown, use a `«««template_liquid: data="…"»»»` block, not a made-up
  formatter — unknown formats silently degrade to plain text.
- **For charts in *live HTML*, don't rasterize.** When the target is HTML a
  browser opens, pull in a CDN charting library (Plotly) and feed it a JS data
  array emitted by a `template_liquid` block, not a static `format:datauri`
  image — see "Charts — emit data into JS". For non-HTML targets (PDF, PPTX,
  image, non-JS Markdown), a **rasterized** chart/diagram is the right choice —
  this rule does not apply.
- **Never hand-write a chart's data array.** The `[{...}, …]` the chart plots
  must be produced by a `template_liquid` loop over a declared `data_input`, not
  typed as literals — even when you know the values. Inlined chart numbers are
  the same failure as inlining any other data: the "template" becomes a static
  snapshot that can't re-render against new data.
- **Never make a table, card grid, list, or chart series a `@@KEY@@`
  substitution.** Repeating/tabular structures are `template_liquid` blocks over
  a `data_input`. `@@KEY@@` is only for a *scalar in a fixed slot* (title, date,
  one number); a non-chart image is an `artifact_content … datauri` embed. A
  `@@KEY@@` table forces the filler to hand-author markup, guessing at your CSS
  classes and column order — the exact failure that makes a "successfully"
  rendered report look broken. See "The `@@KEY@@`-for-a-table anti-pattern".
- **Give a chart library a clean, padding-free target div**; put card styling
  (padding, border-radius, shadow) on a wrapper with `overflow: hidden`. Padding
  or rounded corners on the plot target itself make the chart overflow its box.
- **Never inline resolved data into the body.** Keep the embed directive; do not
  paste the CSV rows / JSON / table it resolves to. Inlining is the failure mode
  that quietly produces a non-reusable "template".
- **An embed target is an artifact filename, never a data_input binding key.**
  `«artifact_content:data.csv»`, not `«artifact_content:employee_data»`. Each
  embed's target must equal the `artifact:` value of some declared `data_input`.
- **Match the Liquid loop to the artifact's actual shape.** A JSON *object*
  exposes its keys as top-level variables (no `items`); a nested map iterates as
  `[key, value]` pairs (`pair[0]`/`pair[1]`, not `.name`). Guessing wrong renders
  empty cells while instantiation reports success — see "The Liquid context
  depends on the artifact's shape". Verify the rendered preview, not just the
  exit status.
- Templates must be a **text** `mime_type`; the embeds resolve at serve time.
- `data_input` declares either a `schema` (JSON) or `columns` (CSV), never both.
- A `@@KEY@@` left in the body that you did not declare in `substitutions` is an
  error — declare every placeholder, or it will be flagged at verify.
- Never package a candidate you have not verified clean.
