---
name: sam-docx
description: Create Word (.docx) documents. Provides sam-docx__docx_create_document — build a document from an ordered list of content blocks (headings, paragraphs with inline formatting, bullet/numbered lists, tables, images, page breaks, a real Word table-of-contents field). Load this skill for any "write this up as a Word doc" / report / memo task.
tags:
  - word
  - docx
  - document
  - report
  - office
---

# Word Document Skill

You create Word (`.docx`) documents with **`sam-docx__docx_create_document`**. It
builds a document from a **content-spec artifact**: a YAML or JSON array of
blocks, applied in order.

There is no read/edit tool here — for extracting or reasoning over an
*existing* `.docx`, use `convert_file_to_markdown` instead. This tool only
creates new documents.

## Workflow

1. **Save the content spec as an artifact.** Use the
   `«««save_artifact: filename="doc-spec.yaml" mime_type="application/yaml" tags="__working" description="..."»»»`
   fenced block. Tag it `__working` — it's an
   intermediate, not the deliverable.
2. **Call `docx_create_document`** with `content` set to that filename (and
   `title`, optionally `author`/`subject`/`output_name`/`images`).

Artifact-first, same reasoning as the pptx tool: documents rarely come out
right on the first pass, and editing the spec in place with
`artifact_search_and_replace_regex` is cheaper than re-emitting the whole
block array every revision.

## Block Types

| Type | Fields |
|---|---|
| `heading` | `level` (1-4, required), `text` (required) |
| `paragraph` | `text`, or `runs[]` for mixed formatting in one line; `align` |
| `bullet_list` | `items[]` |
| `numbered_list` | `items[]` |
| `table` | `headers[]`, `rows[][]`, `header_bold` (default true), `col_widths[]` (default: even split) |
| `image` | `image_ref` (required; quoted index into top-level `images[]`), `width_in` (default 4), `height_in` (default: preserves aspect ratio), `align` |
| `page_break` | *(no fields)* |
| `table_of_contents` | `toc_title` (default "Table of Contents"; `""` for no heading), `toc_levels` (default "1-3") |

Each block reads **only** the fields listed for its own type, and the same
applies to the fields inside a `runs` entry. A field on the wrong type, or
misspelled, is rejected rather than ignored — so a `heading` carrying `runs` is
an error, not an empty heading, and a `txt:` typo inside a run is an error
rather than a silently dropped span. Key names are matched case-insensitively.
Every problem in the spec is reported at once, so one revision fixes the whole
array.

`align` is `left` \| `center` \| `right` \| `justify` (paragraph and image
blocks); case and spacing don't matter, anything else is an error. A `runs`
entry is `{text, bold?, italic?, underline?, color?}` — `color` accepts
`C0392B`, `#C0392B`, or the `#abc` shorthand, but a colour *name* like `red` is
an error rather than a silent fallback to black.

`toc_levels` is a single digit or a hyphenated range (`"1-3"`); a comma-separated
list is an error. Note the default `"1-3"` omits level-4 headings.

Rows shorter than the widest row are padded. A table is limited to 63 columns
(Word's own maximum) — hitting that almost always means one row was written as a
flat list of every value instead of being nested as its own row.

Every table spans the full page width. With no `col_widths` the columns split
that width evenly, and readers are free to narrow a column to fit its content —
Word does, LibreOffice keeps the even split. Even columns are fine whenever the
columns hold comparable amounts of text.

Set `col_widths` when they don't, which in practice means a short label beside
long prose: an even split gives the labels far more room than they need and
wraps the prose column hard. A "Principle | Description" table reads better as
`col_widths: [1, 3]` — a quarter for the labels, three quarters for the prose —
and unlike the default this is exact in every reader. Shares are relative, so
`[1, 3]` and `[25, 75]` are the same table, and you need one per column: a count
that disagrees with the table's column count is an error rather than a guess.

Extreme ratios are bounded rather than obeyed: no column is ever narrower than
1/10 inch, so `[1, 10000]` gives a narrow first column rather than one collapsed
to nothing — a collapsed column clips its content instead of wrapping it.

If an image can't be rendered — bad `image_ref`, unreadable bytes, or oversized
— that block becomes a visible `[image unavailable: ...]` placeholder, the rest
of the document is still produced, and the result comes back `partial` naming
each degraded block. Pass that on to the user rather than reporting success.

## Table of Contents — real field, not a rendered table

`table_of_contents` inserts an actual Word `TOC` field. It shows placeholder
text until the reader opens the document in Word and updates the field (F9,
or right-click → Update Field) — Word then computes real entries and page
numbers from the document's `heading` blocks. This tool cannot compute page
numbers itself; that needs a full layout engine. Mention this to the user
when a TOC is part of the deliverable, so they know to refresh it once.

## Example

```yaml
- type: heading
  level: 1
  text: "Q3 Engineering Review"
- type: table_of_contents
  toc_levels: "1-2"
- type: page_break
- type: heading
  level: 1
  text: "1. Highlights"
- type: bullet_list
  items:
    - "Shipped agent-runtime v2 to production"
    - "Cut p99 task latency by 38%"
- type: paragraph
  runs:
    - text: "Uptime held at "
    - text: "99.94%"
      bold: true
    - text: " over the rolling 30-day window."
- type: heading
  level: 1
  text: "2. Metrics"
- type: table
  headers: ["Metric", "Q2", "Q3"]
  rows:
    - ["p99 latency", "1.4s", "0.9s"]
- type: image
  image_ref: "0"
  width_in: 5
```

Then:

```json
{
  "title": "Q3 Engineering Review",
  "content": "doc-spec.yaml",
  "images": ["architecture-diagram.png"],
  "output_name": "q3-review"
}
```

## Guidance

- Start with a level-1 heading for the document title.
- Prefer `bullet_list` over a `paragraph` with `\n`-separated lines for any
  list of points.
- Given raw Markdown to convert, map its structure onto these blocks
  (headings → `heading`, paragraphs → `paragraph`, bullet groups →
  `bullet_list`, tables → `table`) rather than dumping it verbatim into one
  text block.
- Images must be passed as artifacts in the top-level `images` array and
  referenced by 0-based index via `image_ref` — there is no inline base64
  option.

See `references/examples.md` for more worked examples (multi-section
reports, mixed-formatting paragraphs, ragged tables).
