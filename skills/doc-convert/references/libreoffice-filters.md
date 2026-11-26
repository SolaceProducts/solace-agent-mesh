# LibreOffice export filters

The `filter` parameter on `libreoffice_convert` maps directly to the `--convert-to <fmt>:<filter>` form of `soffice`. Use it when the default export for a target format isn't good enough.

The tool appends it as `target_format:filter`, so do NOT include the `target_format:` prefix yourself.

## Common filters

### PDF export (`target_format: "pdf"`)

Default is LibreOffice's generic PDF export. Override with `filter`:

| Filter string | Effect |
|---|---|
| `writer_pdf_Export` | Explicit Writer PDF export (usually chosen automatically). |
| `writer_pdf_Export:SelectPdfVersion=1` | PDF/A-1b — archive-quality, no external fonts/JS. |
| `writer_pdf_Export:SelectPdfVersion=2` | PDF/A-2b. |
| `impress_pdf_Export` | Explicit Impress (PPTX) PDF export. |
| `calc_pdf_Export` | Explicit Calc (XLSX) PDF export, one sheet per page by default. |

### Plain text (`target_format: "txt"`)

| Filter string | Effect |
|---|---|
| `Text (encoded):UTF8` | Force UTF-8 output (default on most locales, but worth being explicit). |
| `Text (encoded):UTF8,,,,,,,,,true` | UTF-8 with Unix line endings. |
| `Text (encoded):ISO8859-1` | Legacy Latin-1 output. |

### CSV from XLSX/ODS (`target_format: "csv"`)

The filter is a comma-separated string of token codes documented in LibreOffice's Calc text import dialog.

Format: `Text - txt - csv (StarCalc):<field_sep>,<text_sep>,<encoding>,<first_row>,<cell_fmt>,<lang>,<rem_quotes>,<quote_all>,<fixed>,<save_as_shown>`

| Filter string | Effect |
|---|---|
| `Text - txt - csv (StarCalc):44,34,76` | Comma-delimited, double-quoted strings, UTF-8 (encoding 76). |
| `Text - txt - csv (StarCalc):59,34,76` | Semicolon-delimited (common in Europe). |
| `Text - txt - csv (StarCalc):9,34,76` | Tab-delimited (TSV). |
| `Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,false,false` | Comma-delimited, quote every field. |

Encoding codes: `0` = system default, `76` = UTF-8, `11` = US-ASCII, `12` = ISO-8859-1. See `soffice --convert-to help` for the full list on your system.

### HTML (`target_format: "html"`)

| Filter string | Effect |
|---|---|
| `HTML (StarWriter)` | Default, compatible with most browsers. |
| `XHTML Writer File` | Stricter XHTML output. |

### Images from document pages (`target_format: "png"` / `"jpg"` / `"svg"`)

LibreOffice exports only the first page by default. If you need every page:

1. Convert to PDF first (`target_format: "pdf"`).
2. Pipe the PDF through `image_magick` with `{input[0]}` (all pages) and `output_filename: "page_%04d.png"`.

### RTF / DOC (legacy Word)

| Target | Filter | Effect |
|---|---|---|
| `rtf` | (none) | Default RTF export. |
| `doc` | `MS Word 97` | Legacy `.doc` format (Word 97–2003). |

### EPUB

| Filter string | Effect |
|---|---|
| `EPUB` | Default EPUB 3 export. |

## Examples

```yaml
# DOCX → PDF/A-2b (archive-grade)
operation: ignored-goes-to-libreoffice
tool: libreoffice_convert
args:
  input_document: <doc.docx>
  target_format: "pdf"
  filter: "writer_pdf_Export:SelectPdfVersion=2"

# XLSX → semicolon-delimited CSV (European locale)
tool: libreoffice_convert
args:
  input_document: <sheet.xlsx>
  target_format: "csv"
  filter: "Text - txt - csv (StarCalc):59,34,76"

# XLSX → TSV
tool: libreoffice_convert
args:
  input_document: <sheet.xlsx>
  target_format: "csv"
  filter: "Text - txt - csv (StarCalc):9,34,76"
  output_filename: "sheet.tsv"
```

## Discovering filters

If you need a filter that isn't listed here, ask the user to run on their host:

```
soffice --convert-to help
```

It prints the full filter list. Different LibreOffice versions ship different filters.

## Character allowlist

The tool rejects filter strings containing characters outside `[A-Za-z0-9_ .,=\-()]`. If a legitimate filter uses a character outside that set, it will be rejected — the allowlist is deliberately conservative to prevent command injection. Report the case to the maintainers with the filter string you need.
