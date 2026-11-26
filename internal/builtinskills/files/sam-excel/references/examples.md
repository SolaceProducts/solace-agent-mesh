# `sam-excel__xlsx_write` — worked examples

## 1. Report from a CSV data artifact, with totals and formatting

Assumes a `sales_rows.csv` artifact already exists (columns: `region, units, revenue`).

```json
{
  "output_name": "q3-sales",
  "data_sources": ["sales_rows.csv"],
  "operations": [
    { "op": "rename_sheet", "sheet": "Sheet1", "new_name": "Q3" },
    { "op": "fill_from_data", "sheet": "Q3", "anchor": "A1", "source": "0", "include_header": true },
    { "op": "set_cell", "sheet": "Q3", "cell": "D1", "value": "Avg Price" },
    { "op": "set_formula_range", "sheet": "Q3", "range": "D2:D4", "formula": "=C2/B2" },
    { "op": "set_cell", "sheet": "Q3", "cell": "B5", "formula": "=SUM(B2:B4)" },
    { "op": "set_cell", "sheet": "Q3", "cell": "C5", "formula": "=SUM(C2:C4)" },
    { "op": "set_style", "sheet": "Q3", "range": "A1:D1",
      "style": { "bold": true, "font_color": "FFFFFF", "fill": "4472C4", "align": "center" } },
    { "op": "set_style", "sheet": "Q3", "range": "C2:C5", "style": { "number_format": "$#,##0" } },
    { "op": "set_style", "sheet": "Q3", "range": "D2:D4", "style": { "number_format": "$#,##0.00" } },
    { "op": "set_col_width", "sheet": "Q3", "cols": "A:D", "width": 16 },
    { "op": "freeze_panes", "sheet": "Q3", "cell": "A2" },
    { "op": "auto_filter", "sheet": "Q3", "range": "A1:D4" }
  ]
}
```

## 2. Chart from a filled sheet

```json
{
  "source": "q3-sales.xlsx",
  "operations": [
    { "op": "add_chart", "sheet": "Q3", "anchor": "F2",
      "chart": {
        "type": "col",
        "title": "Revenue by Region",
        "series": [
          { "name": "Q3!$C$1", "categories": "Q3!$A$2:$A$4", "values": "Q3!$C$2:$C$4" }
        ]
      }
    }
  ]
}
```

## 3. Multi-sheet workbook

```json
{
  "output_name": "fy26-plan",
  "operations": [
    { "op": "rename_sheet", "sheet": "Sheet1", "new_name": "Summary" },
    { "op": "create_sheet", "name": "Q1" },
    { "op": "create_sheet", "name": "Q2" },
    { "op": "set_values", "sheet": "Summary", "anchor": "A1",
      "values": [["Quarter", "Revenue"], ["Q1", 0], ["Q2", 0]] },
    { "op": "set_cell", "sheet": "Summary", "cell": "B2", "formula": "=SUM(Q1!B:B)" },
    { "op": "set_cell", "sheet": "Summary", "cell": "B3", "formula": "=SUM(Q2!B:B)" }
  ]
}
```

## 4. Fill from a JSON array-of-objects with explicit column order

Assumes a `people.json` artifact: `[{"name":"Alice","dept":"Eng","level":5}, ...]`.

```json
{
  "output_name": "roster",
  "data_sources": ["people.json"],
  "operations": [
    { "op": "fill_from_data", "sheet": "Sheet1", "anchor": "A1", "source": "people.json",
      "format": "json", "include_header": true, "columns": ["name", "dept", "level"] },
    { "op": "set_style", "sheet": "Sheet1", "range": "A1:C1", "style": { "bold": true } }
  ]
}
```

Without `columns`, the column order is the sorted union of object keys (`dept, level, name`). Provide `columns` whenever order matters.

## 5. Templated formula (when shared adjustment isn't right)

Stamp a row-aware label that references a fixed config cell plus the current row:

```json
{
  "source": "data.xlsx",
  "operations": [
    { "op": "set_formula_range", "sheet": "Sheet1", "range": "E2:E100", "mode": "template",
      "formula": "=D{row}*$B$1" }
  ]
}
```

`{row}` expands to each cell's row number, so `E2` gets `=D2*$B$1`, `E3` gets `=D3*$B$1`, etc.

## 6. Iterative authoring via an operations artifact

For a large build, save the operations array as a YAML artifact, then invoke with `operations_artifact`:

```
«««save_artifact: filename="build.yaml" mime_type="application/yaml" description="workbook build ops"
- op: rename_sheet
  sheet: Sheet1
  new_name: Data
- op: fill_from_data
  sheet: Data
  anchor: A1
  source: "0"
- op: set_style
  sheet: Data
  range: A1:F1
  style: {bold: true, fill: "4472C4", font_color: "FFFFFF"}
»»»
```

```json
{ "output_name": "out", "data_sources": ["rows.csv"], "operations_artifact": "build.yaml" }
```

Revise by editing `build.yaml` in place with `artifact_search_and_replace_regex` and re-invoking — cheaper and less error-prone than re-emitting the whole list.

## 7. Bounded fill into a fixed region

Use `range` instead of `anchor` when the destination has a fixed size you want to enforce — e.g. refreshing a dashboard table that must stay within `A1:D20`. Data that overflows the range errors (nothing is written); the rest of the range is cleared so no stale rows linger from a previous, larger dataset.

```json
{
  "source": "dashboard.xlsx",
  "data_sources": ["latest_rows.csv"],
  "operations": [
    { "op": "fill_from_data", "sheet": "Summary", "range": "A1:D20", "source": "0" }
  ]
}
```

If `latest_rows.csv` has more than 20 rows or more than 4 columns, the op returns an error telling you to widen the range or trim the data, and the workbook is left unchanged.

## Formatting recipes

- **Currency column:** `set_style` over the column range with `number_format: "$#,##0.00"`.
- **Percent column:** `number_format: "0.0%"` — and keep the underlying value a fraction (`0.42`), not `42`.
- **Banded header:** `set_style` on the header range with `bold: true`, a `fill`, and contrasting `font_color`; then `freeze_panes` below it.
- **Boxed table:** `set_style` over the full table range with `border: "thin"`.
- **Wrapped notes column:** `set_style` with `wrap_text: true` and a wider `set_col_width`.
