---
name: sam-excel
description: Read, create, and modify Excel (.xlsx) workbooks. Provides sam-excel__xlsx_read (selective, context-safe reading — describe / range / row-window / cell-search) and sam-excel__xlsx_write (create or modify via an ordered list of openpyxl-style operations — set cells, fill ranges directly from CSV/JSON data artifacts, stamp formulas across whole ranges, style, merge, freeze panes, auto-filter, charts). Load this skill for any spreadsheet task.
tags:
  - excel
  - xlsx
  - spreadsheet
  - data
  - office
---

# Excel Skill

You work with Excel (`.xlsx`) workbooks. Two tools are bundled here:

- **`sam-excel__xlsx_read`** — read a workbook *selectively*: orient with `describe`, then pull only the range/rows you need, read formulas, or search for cells. Never dumps a whole workbook into context.
- **`sam-excel__xlsx_write`** — create a new workbook or modify an existing one by applying an ordered list of **openpyxl-style operations**.

The vocabulary mirrors **openpyxl + native Excel**: A1 notation everywhere (`A1`, `C2:C100`, `Sheet1!$A$2:$A$50`), real Excel formulas (`=SUM(B2:B40)`), and openpyxl-style styling (font, fill, alignment, number format). Use them the way you'd use openpyxl.

Two habits matter most:

1. **Fill from data artifacts — don't re-type data.** When the rows already exist as a CSV/JSON artifact (e.g. produced by another tool or a template render), use the `fill_from_data` operation to stamp them into a sheet. Do **not** read the data in and re-emit it as inline `values`.
2. **Read selectively.** Always `describe` first, then read a bounded `range` / `rows` window or `search` for what you need. A whole-sheet read over the cell cap returns guidance to narrow, not the data.

---

## Reading: `sam-excel__xlsx_read`

Start every read task with `describe`:

```json
{ "source": "report.xlsx", "mode": "describe" }
```

It returns, per sheet: dimensions, row/column counts, the header row, merged ranges, and defined names. Then pull only what you need:

- **A bounded block** — `{ "source": "report.xlsx", "mode": "values", "sheet": "Q3", "range": "A1:E25" }`
- **A row window** — `{ "source": "report.xlsx", "mode": "values", "rows": "1-50" }`
- **Find a cell without reading the sheet** — `{ "source": "report.xlsx", "mode": "search", "query": "Total Revenue" }` (set `"regex": true` for patterns)
- **Inspect formulas** — `{ "source": "report.xlsx", "mode": "formulas", "sheet": "Q3", "range": "F2:F40" }`

`format` controls rendering: `markdown` (default), `csv`, or `json_rows`. Whole-sheet `values` reads above `max_cells` (default 2000) return the sheet size and tell you to narrow — respond by scoping with `range`/`rows` or using `search`.

See `references/reading.md` for the full selective-read playbook.

---

## Writing: `sam-excel__xlsx_write`

Omit `source` to **create** a new workbook; pass an existing `.xlsx` artifact as `source` to **modify** it. Operations apply in order; the whole call produces **one** new artifact version at the end (not one per operation).

**Versioning & `output_name`:** when modifying, leave `output_name` unset — it defaults to the `source` filename, so the edit saves as the next version of the same workbook (artifacts are immutable and versioned; the prior version stays addressable as `name.xlsx:1`). Set `output_name` only when you want to fork a separate, differently-named copy. A fresh create with no `output_name` saves as `workbook.xlsx`.

### Filling a sheet from a data artifact (the common case)

When the data already lives in a CSV/JSON artifact, reference it in `data_sources` and stamp it with `fill_from_data` — no manual re-entry:

```json
{
  "output_name": "q3-sales",
  "data_sources": ["sales_rows.csv"],
  "operations": [
    { "op": "rename_sheet", "sheet": "Sheet1", "new_name": "Q3" },
    { "op": "fill_from_data", "sheet": "Q3", "anchor": "A1", "source": "0", "include_header": true },
    { "op": "set_cell", "sheet": "Q3", "cell": "D1", "value": "Margin" },
    { "op": "set_formula_range", "sheet": "Q3", "range": "D2:D41", "formula": "=C2/B2" },
    { "op": "set_cell", "sheet": "Q3", "cell": "B42", "formula": "=SUM(B2:B41)" },
    { "op": "set_style", "sheet": "Q3", "range": "A1:D1",
      "style": { "bold": true, "font_color": "FFFFFF", "fill": "4472C4" } },
    { "op": "set_style", "sheet": "Q3", "range": "D2:D41", "style": { "number_format": "0.0%" } },
    { "op": "freeze_panes", "sheet": "Q3", "cell": "A2" },
    { "op": "auto_filter", "sheet": "Q3", "range": "A1:D41" }
  ]
}
```

### Stamping formulas across a range

`set_formula_range` fills a formula across a whole range in one op — never write a column of formulas cell by cell. The default `shared` mode auto-adjusts relative references per cell (`=A2*B2` → `=A3*B3` …) while preserving absolute refs and quoted sheet names. Use `mode: "template"` with `{row}`/`{col}` tokens only when relative adjustment doesn't fit.

### Modifying an existing workbook

```json
{
  "source": "budget.xlsx",
  "operations": [
    { "op": "set_cell", "sheet": "FY26", "cell": "C7", "value": 125000 },
    { "op": "set_style", "sheet": "FY26", "range": "C7", "style": { "number_format": "$#,##0" } }
  ]
}
```

### Operation catalog (summary)

| Group | Operations |
|---|---|
| Structure | `create_sheet`, `delete_sheet`, `rename_sheet` |
| Data | `set_cell`, `set_values`, `fill_from_data` |
| Formulas | `set_formula_range` |
| Styling | `set_style`, `merge`, `unmerge` |
| Layout | `set_col_width`, `set_row_height`, `freeze_panes`, `auto_filter` |
| Advanced | `add_chart`, `set_defined_name` |

Full per-operation field reference and the complete `style` object (font, fill, border, alignment, number formats) are in `references/operations.md`. More worked examples (charts, multi-sheet, formatting recipes) are in `references/examples.md`.

### Inline operations vs an operations artifact

- **Inline `operations`** — use for small, targeted edits (a handful of ops). Most modify tasks.
- **`operations_artifact`** — for large or iterative builds, save the operations array as a YAML/JSON artifact and pass its filename. You can then edit it in place with `artifact_search_and_replace_regex` and re-run, rather than re-emitting the whole list. When set, it takes precedence over inline `operations`.

---

## Guidance

- **Numbers stay numbers.** Pass numeric cell values as JSON numbers (`125000`, not `"125000"`) so they're real numbers in the sheet. `fill_from_data` coerces clean numeric CSV cells automatically.
- **Format numbers with `number_format`, not by pre-rounding.** Apply `0.0%`, `$#,##0`, `#,##0.00`, `yyyy-mm-dd` etc. via `set_style` so the underlying value stays precise.
- **One header, then freeze it.** After a header row, `freeze_panes` at the cell below-left (e.g. `A2`) and add `auto_filter` over the data range for anything tabular.
- **Iterate cheaply.** To revise a generated workbook, modify the source artifact with targeted ops rather than regenerating from scratch.

## References

- `references/operations.md` — every operation, field by field, plus the full style object and number-format / color / border reference.
- `references/examples.md` — worked examples: charts, multi-sheet workbooks, JSON-object fills, formatting recipes.
- `references/reading.md` — the selective-read playbook (describe → narrow → search) and context-safety rules.
