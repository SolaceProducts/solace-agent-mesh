# `sam-excel__xlsx_read` — selective reading playbook

The goal of every read is to get exactly the cells you need into context and nothing more. A workbook can hold hundreds of thousands of cells; reading it whole would blow the context window and bury the relevant data. Follow the describe → narrow → search loop.

## 1. Always describe first

```json
{ "source": "report.xlsx", "mode": "describe" }
```

Returns, per sheet:
- dimensions and row × column counts,
- the header row (row 1),
- merged ranges,
- workbook defined names.

This is cheap and tells you where the data is and how big it is — enough to plan a targeted read without loading any cell data.

## 2. Read a bounded window

Once you know the layout, pull only the relevant block.

- **Explicit range** — preferred when you know the cells:
  ```json
  { "source": "report.xlsx", "mode": "values", "sheet": "Q3", "range": "A1:E25" }
  ```
- **Row window** — preferred for paging through rows; spans all used columns:
  ```json
  { "source": "report.xlsx", "mode": "values", "sheet": "Q3", "rows": "1-50" }
  ```
  Page with `51-100`, `101-150`, … as needed.

`format` controls rendering:
- `markdown` (default) — a pipe table, easy to read.
- `csv` — compact; good for re-feeding into a data tool.
- `json_rows` — a JSON 2D array; good for programmatic downstream use.

## 3. The cell cap

Whole-sheet `values`/`formulas` reads are capped at `max_cells` (default 2000). Over the cap, the tool returns the sheet size and tells you to narrow — it does **not** return the data. When you see that, respond by:
- adding a `range` or `rows` window, or
- switching to `search` to locate specific cells.

You can raise `max_cells` for a genuinely small-but-wide sheet, but prefer narrowing the area.

## 4. Search instead of scanning

To find where a value lives without reading the sheet:

```json
{ "source": "report.xlsx", "mode": "search", "query": "Total Revenue" }
```

- Searches all sheets unless you set `sheet`.
- Set `"regex": true` to treat `query` as a regular expression (e.g. `"^Q[1-4] Total$"`).
- Returns matching `Sheet!Cell = value` lines, capped at 100 matches (refine the query if you hit the cap).

Then do a small targeted `values` read around the coordinates you found.

## 5. Read formulas, not just values

```json
{ "source": "model.xlsx", "mode": "formulas", "sheet": "Calc", "range": "F2:F40" }
```

Returns the formula text (`=C2/B2`) for each cell instead of the computed value — use this to understand or audit a model before modifying it.

## Typical loops

- **"What's in this file?"** → `describe`, then a `range` read of the interesting sheet.
- **"Find the row for customer X."** → `search` for X, then a `range` read around the hit.
- **"Summarize a large sheet."** → `describe` for size, then page with `rows` windows, or read a representative `range`.
- **"How is this total computed?"** → `formulas` read of the total cell and its inputs.
