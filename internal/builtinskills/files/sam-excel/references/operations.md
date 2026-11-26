# `sam-excel__xlsx_write` — operation reference

Every operation is an object in the `operations` array with an `op` discriminator. Only the fields relevant to that `op` are read. `sheet` defaults to the first sheet when omitted; a sheet named in any op is created automatically if it doesn't exist.

All operations in one `xlsx_write` call are applied to a single in-memory workbook and saved as **one** new artifact version at the end — there is no intermediate version per operation. When modifying, leave `output_name` unset so the result versions the same `source` workbook; set `output_name` only to fork a new file.

## Structure

### `create_sheet`
- `name` (required) — new sheet name.
- `index` (optional) — 0-based position; best-effort placement.

### `delete_sheet`
- `name` (required) — sheet to remove.

### `rename_sheet`
- `sheet` (required) — current name.
- `new_name` (required) — new name.

## Data

### `set_cell`
- `sheet`, `cell` (required) — e.g. `B5`.
- `value` — literal value: string, number, or boolean. Pass numbers as JSON numbers. A string starting with `=` is written as a formula.
- `formula` — Excel formula, e.g. `=SUM(A1:A9)` (leading `=` optional). Provide `value` **or** `formula`.

### `set_values`
- `sheet`, `anchor` (required) — top-left cell, e.g. `A1`.
- `values` (required) — 2D array written row-major from the anchor. Cells may be strings/numbers/booleans. A string starting with `=` is written as a formula (e.g. `"='Sales Data'!B6"` computes; quote sheet names with spaces), so you can mix labels, values, and formulas in one block.

Use this for small literal blocks. For bulk data that already exists as an artifact, use `fill_from_data` instead. (Note: `fill_from_data` writes its source **literally** — a leading `=` there stays text, not a formula.)

### `fill_from_data`
Stamp a data artifact into the sheet — the way to load bulk rows without re-emitting them inline.
- `sheet`.
- `anchor` — top-left cell where the data starts. Required unless `range` is given.
- `range` (optional) — a **bounded target** instead of an open-ended anchor, e.g. `A1:C50`. Data that overflows the range (more rows or more columns than it allows) is an **error** and the workbook is left unchanged; cells inside the range not covered by the data are **cleared** (so a bounded fill into an existing sheet reads as exactly the new data). Use `range` when the destination has a fixed size you want to enforce; use `anchor` when the data should define its own extent.
- `source` (required) — 0-based index (`"0"`) or filename of an artifact in the tool call's `data_sources`.
- `format` (optional) — `auto` (default), `csv`, `tsv`, `json`. `auto` detects from filename/MIME — give JSON artifacts a `.json` name or pass `format: "json"` so they aren't misread as CSV.
- `include_header` (optional, default `true`) — write a header row. For CSV/TSV the first record is treated as the header; for JSON arrays-of-objects a header is synthesized.
- `columns` (optional) — explicit column order for JSON arrays-of-objects. When omitted, the sorted union of object keys is used. Listing fewer columns than the objects' keys projects to just those columns; listing a key that isn't present yields a blank column.

Ragged sources (rows of differing lengths) are written as-is in anchor mode: short rows leave trailing cells blank, long rows extend past the header.

JSON sources may be an **array of arrays** (`[["a","b"],[1,2]]` → rows verbatim) or an **array of objects** (`[{"a":1,"b":2}]` → header + one row per object). CSV numeric/boolean-looking cells are coerced to real numbers/booleans.

## Formulas

### `set_formula_range`
Stamp one formula across a whole range — never write a column of formulas cell by cell.
- `sheet`, `range` (required) — e.g. `D2:D100`.
- `formula` (required) — e.g. `=C2/B2`.
- `mode` (optional) — `shared` (default) or `template`.
  - **`shared`** — writes the formula into every cell with relative references auto-adjusted (`=A2*B2` over `C2:C100` becomes `=A3*B3`, `=A4*B4`, …); absolute references (`$A$1`) and quoted sheet names (`'Personal Info'!`) are preserved. This is what you want almost always.
  - **`template`** — expands `{row}` (1-based row number) and `{col}` (column letter) tokens per cell. Use when shared-formula relative adjustment isn't the behavior you need.

## Styling

### `set_style`
- `sheet`, `range` (required) — a cell (`A1`) or range (`A1:D1`).
- `style` (required) — the style object below.

**`style` object** (all fields optional):

| Field | Type | Notes |
|---|---|---|
| `bold` | bool | |
| `italic` | bool | |
| `underline` | bool | |
| `font` | string | Font family, e.g. `Calibri`. |
| `size` | number | Font point size. |
| `font_color` | string | Hex without `#`, e.g. `FFFFFF`. |
| `fill` | string | Background hex without `#`, e.g. `4472C4`. |
| `number_format` | string | Excel format code (see below). |
| `align` | string | `left` \| `center` \| `right`. |
| `valign` | string | `top` \| `center` \| `bottom`. |
| `wrap_text` | bool | |
| `border` | string | `thin` \| `medium` \| `thick` \| `dashed` \| `dotted` \| `double` (applied to all four sides). |
| `border_color` | string | Hex without `#`. Default `000000`. |

**Common `number_format` codes:** `#,##0` (thousands), `#,##0.00` (2 decimals), `0%` / `0.0%` (percent), `$#,##0.00` (currency), `yyyy-mm-dd` (date), `[h]:mm:ss` (duration), `@` (text).

### `merge` / `unmerge`
- `sheet`, `range` (required) — e.g. `A1:D1`.

## Layout

### `set_col_width`
- `sheet`, `cols` (required) — a column (`A`) or range (`A:C`).
- `width` (required) — width in character units.

### `set_row_height`
- `sheet`, `row` (required) — 1-based row number.
- `height` (required) — height in points.

### `freeze_panes`
- `sheet`, `cell` (required) — the top-left **unfrozen** cell. `B2` freezes row 1 and column A; `A2` freezes just the header row.

### `auto_filter`
- `sheet`, `range` (required) — the data range including the header row, e.g. `A1:D41`.

## Advanced

### `add_chart`
- `sheet`, `anchor` (required) — top-left cell where the chart is placed, e.g. `F2`.
- `chart` (required):
  - `type` — `col` \| `bar` \| `line` \| `pie` \| `doughnut` \| `area` \| `scatter` \| `radar`.
  - `title` (optional).
  - `series` — array of `{ name, categories, values }`, all **cell/range references** (not literals):
    - `name` — a reference to the series label cell, usually the header, e.g. `Q3!$B$1`. A literal string is rejected (Excel renders the series name from the referenced cell).
    - `categories` (optional) — range ref for the category axis, e.g. `Q3!$A$2:$A$13`.
    - `values` (required) — range ref for the data, e.g. `Q3!$B$2:$B$13`.
  - Sheet names in series refs are auto-quoted, so `Dept Summary!$B$2:$B$7` and `'Dept Summary'!$B$2:$B$7` both work. (Without quoting, Excel cannot resolve a sheet name that contains a space, contains punctuation, or parses as a cell reference such as `Q3`, and the chart renders blank — the tool handles this for you.)

### `set_defined_name`
- `name` (required) — the defined name.
- `refers_to` (required) — the range it points to, e.g. `Q3!$A$1:$A$50`.
- `sheet` (optional) — scope the name to a single sheet (omit for workbook scope).
