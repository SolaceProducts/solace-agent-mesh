---
name: sam-pptx
description: Build PowerPoint (.pptx) decks that read well and finish inside the task's call budget, with pptx_create_presentation from Document Tools. Covers planning the story, gathering the figures in few calls, choosing layouts, native charts and tables, a compact slide spec, one visual check, and applying a brand from another skill. Load this skill for any "make a deck / presentation / slides / PowerPoint" task.
tags:
  - powerpoint
  - pptx
  - presentation
  - slides
  - deck
  - office
---

# PowerPoint Deck Skill

You build decks with **`pptx_create_presentation`** from Document Tools. Its own
instructions list every layout and field; this skill is about the judgment around
it: what to put in the deck, how to get there without running out of model calls,
and how to check the result.

## Workflow

1. **Plan the story first.** Decide the 5–8 slides before gathering anything: a
   title, the headline result, one slide per question the user asked, and a
   closing takeaway. Each slide makes one point; its title states that point
   ("Electronics led revenue at $15.7M"), not just the topic ("Revenue").
2. **Gather the figures in as few calls as possible.** The task has a limited
   number of model calls (30 by default), and the deck is the last step, so data
   gathering must leave calls in hand.
   - Compute each slide's figures in one query, or several slides' figures in
     one query, with common table expressions, window functions, or a
     `UNION ALL` of labelled rows. A dozen metrics is three or four queries.
   - Make independent tool calls in the same response.
   - Don't render chart images: the deck draws its own charts (below).
3. **Save the slide spec as one compact artifact.** Use a
   `«««save_artifact: filename="deck-spec.yaml" mime_type="application/yaml" tags="__working" description="..."»»»`
   block, tagged `__working` because it is an intermediate. The whole spec is
   written in one response, so keep it small: round figures (`$15.7M`, not
   `$15,712,384.27`), at most 8 table rows, at most 12 chart categories, and short
   bullets. A spec cut off by the output limit produces no deck at all.
4. **Call `pptx_create_presentation`** with `slides` set to that filename and a
   `title`. Pass image artifacts in `images`, and reference each from an image
   layout slide with `image_ref`.
5. **Check it once.** Skip this for a simple deck, or when few calls remain.
   Otherwise, if `render_document_to_images` is available, render the deck
   (its `pages` option limits which pages), load the page images with
   `load_artifact`, and look for overflowing text, medallions with only a letter
   or the wrong glyph, unreadable charts and repeated layouts. Fix the spec artifact in place (with
   `artifact_search_and_replace_regex` if you have it) and rebuild, once. Don't
   iterate on cosmetics.
6. **Reply briefly.** Give the deck and two or three sentences on what it shows.
   Don't repeat the whole deck in chat.

## Choosing layouts

Vary the layouts. A deck of ten `bullet_list` slides reads as a document. Match
the layout to the kind of point:

| The point is… | Use |
|---|---|
| One headline number | `big_number` (`big_number: "$32.2M"`, `big_label`, short `body`) |
| A trend over time | `chart` with `chart_type: line` (or `area`) |
| A ranking or comparison across categories | `chart` with `chart_type: bar` |
| Shares of a whole (up to about six parts) | `chart` with `chart_type: pie` or `doughnut` |
| Exact figures someone will read across | `table` (at most 8 rows; put "Other" last if you cut) |
| Two things side by side | `comparison` |
| A few findings or recommendations | `icon_grid` or `bullet_list` |
| One thing that stands out | `callout`: the lead-in in `title` (the tool highlights it), the sentence in `body` |
| Phases or next steps | `timeline` or `process` |

Set an `icon` on every `icon_grid`, `timeline` and `process` item and every
`callout`. Without one, an item gets a glyph guessed from its words or just a
letter, and a callout gets no icon at all.

## Charts and tables

Charts are native PowerPoint charts: the viewer can restyle them and read the
values, and they cost nothing extra to make. Put the numbers straight into
`chart_data`:

```yaml
- layout: chart
  title: "Revenue peaked in November"
  chart_data:
    chart_type: line
    show_legend: false
    series:
      - name: "Net revenue ($K)"
        categories: ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]
        values: [1910, 2050, 2380, 2440, 2510, 2490, 2560, 2620, 2700, 2930, 3610, 3790]
```

- Use one unit per chart and say it in the series name ("Net revenue ($K)",
  "Orders (units)", "Margin (%)").
- Several series share one set of categories; give each a `name`.
- Sort bar categories by value unless the order means something (months, sizes).
- A table's cells are strings: format them (`"$1.36M"`, `"8.1%"`) before writing.

## Brands

Two things shape a deck's look, and they control different parts:

- **The template profile** decides the slide backgrounds, title and body
  colours, any logo and decorations, and the footer. The agent's tool
  configuration names it (`template_profile`).
- **The `theme` parameter** recolours what the profile leaves open: the heading
  and body fonts, and the accent colours used by charts and cards.

When the user asks for a brand and a loaded skill defines it (colours, fonts, a
logo), pass its fonts and colours as `theme`, and don't invent brand colours.
Otherwise leave `theme` out. `theme` does not
change slide backgrounds or a profile's logo, so tell the user if the result
doesn't look like the brand. To show the brand's logo, put it in `images` and
use an `image_center`, `image_left` or `image_right` slide with `image_ref`; the
`title` layout cannot place one. Don't set `style` unless the user asks for a
particular look.

## Common failures

- **Running out of calls before the deck.** One query per metric, or a chart
  image per figure, spends the budget on gathering. Batch queries and use native
  charts.
- **A spec too large to write in one response.** Raw unrounded figures, long
  tables and paragraph bullets. Round, cut, and summarize.
- **Every slide the same layout.** See the table above.
- **Titles that name a topic instead of making a point.**
