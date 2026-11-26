# pdfcpu recipes

Reference for the `pdf_ops` tool, which wraps the pure-Go [pdfcpu](https://github.com/pdfcpu/pdfcpu) library.

## Page range syntax

Used by the `extract_pages` and (optionally) `rotate` operations.

| Expression | Effect |
|---|---|
| `1` | Page 1 only. |
| `1-3` | Pages 1, 2, 3. |
| `1-3,5,7-` | Pages 1–3, page 5, and pages 7 through the end. |
| `-3` | Pages 1 through 3. |
| `3-` | Page 3 through the end. |
| `even` | Even-numbered pages. |
| `odd` | Odd-numbered pages. |
| `l-5` | Last page minus 5 (supports `l` for last). |

Leave `page_range` unset on `rotate` to rotate every page.

## Watermark option strings

`watermark_opts` is a comma-separated list of `key:value` pairs. pdfcpu accepts these keys (non-exhaustive — see upstream docs for the full list):

| Key | Value | Notes |
|---|---|---|
| `font` | font name (e.g. `Helvetica`, `Times-Roman`, `Courier`, `Helvetica-Bold`) | 14 standard PDF fonts work without external font files. |
| `points` | integer point size | Size of the text. `24` is a reasonable large stamp. |
| `scale` | `<n>` or `<n> rel` or `<n> abs` | Relative (`rel`) scales relative to the page; absolute (`abs`) is in user units. |
| `opacity` | `0.0`–`1.0` | `0.3` is a subtle draft stamp. |
| `rotation` | degrees (e.g. `0`, `45`, `-45`, `90`) | `45` gives the classic diagonal DRAFT look. |
| `color` | hex `#rrggbb` or named | `#808080` grey, `#ff0000` red. |
| `pos` | position (`tl`, `tc`, `tr`, `l`, `c`, `r`, `bl`, `bc`, `br`) | Defaults to `c` (center). |
| `offset` | `<dx> <dy>` | Pixel offset from `pos`. |

### Default used by `pdf_ops`

If `watermark_opts` is omitted, the tool uses:
```
font:Helvetica,points:24,opacity:0.3,rotation:45
```
Which is a centered, grey-ish 45° diagonal stamp at 30 % opacity.

### Recipes

```yaml
# Classic DRAFT stamp (default).
watermark_text: "DRAFT"
# watermark_opts omitted — uses default

# Big horizontal red "CONFIDENTIAL" near top.
watermark_text: "CONFIDENTIAL"
watermark_opts: "font:Helvetica-Bold,points:48,color:#ff0000,opacity:0.4,rotation:0,pos:tc"

# Subtle footer.
watermark_text: "INTERNAL USE ONLY"
watermark_opts: "font:Helvetica,points:10,opacity:0.5,rotation:0,pos:bc"
```

## Encryption

The `encrypt` and `decrypt` operations both use the same `password` parameter as the user password *and* the owner password. pdfcpu applies AES-256 encryption by default.

To decrypt, you need the original password — there is no recovery path.

## Operation quick reference

| Operation | Inputs | Output |
|---|---|---|
| `merge` | `input_pdf` + one or more `additional_pdfs` | One merged PDF (named `merged.pdf` by default). |
| `split` | `input_pdf` | N PDFs named `page_1.pdf`, `page_2.pdf`, ... |
| `extract_pages` | `input_pdf` + `page_range` | One PDF containing only the selected pages. |
| `rotate` | `input_pdf` + `rotation` (90 / 180 / 270) + optional `page_range` | One PDF with pages rotated. |
| `watermark` | `input_pdf` + `watermark_text` + optional `watermark_opts` | One PDF with a text stamp. |
| `encrypt` | `input_pdf` + `password` | One encrypted PDF. |
| `decrypt` | `input_pdf` + `password` | One decrypted PDF. |
| `info` | `input_pdf` | Text result + `Data` map with page count, version, encryption status, metadata. No artifact. |

## Limits

- Maximum output file size per artifact: 200 MiB (applies to every `page_N.pdf` in split too).
- `pdf_ops` operates on PDFs only — to turn office documents or images into PDFs first, use `libreoffice_convert` or `image_magick`.
- Pure-Go implementation — no external binaries, so it runs identically on macOS, Linux, and inside the container sandbox.
