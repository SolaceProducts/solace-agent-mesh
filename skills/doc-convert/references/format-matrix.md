# Format matrix

Extended source/target lookup. The primary axis is the **input format**. For each input, the tool recommended for each target is listed with the output's default MIME type.

## Office / text documents

| Input | → PDF | → DOCX | → PPTX | → XLSX | → ODT | → HTML | → TXT | → CSV | → RTF | → EPUB | → PNG (page) |
|---|---|---|---|---|---|---|---|---|---|---|---|
| DOCX | libreoffice_convert | — | libreoffice_convert | — | libreoffice_convert | libreoffice_convert | libreoffice_convert | — | libreoffice_convert | libreoffice_convert | libreoffice_convert (`png`) |
| ODT | libreoffice_convert | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | — | libreoffice_convert | libreoffice_convert | libreoffice_convert (`png`) |
| PPTX | libreoffice_convert | libreoffice_convert | — | — | — | libreoffice_convert | libreoffice_convert | — | libreoffice_convert | — | libreoffice_convert (`png` — first slide only) |
| ODP | libreoffice_convert | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | — | libreoffice_convert | — | libreoffice_convert (`png`) |
| XLSX | libreoffice_convert | — | — | — | libreoffice_convert (as `ods`) | libreoffice_convert | — | libreoffice_convert | — | — | libreoffice_convert (`png`) |
| ODS | libreoffice_convert | — | — | libreoffice_convert | — | libreoffice_convert | — | libreoffice_convert | — | — | libreoffice_convert (`png`) |
| CSV | libreoffice_convert | — | — | libreoffice_convert | — | libreoffice_convert | libreoffice_convert | — | — | — | — |
| HTML | html_to_pdf | libreoffice_convert | — | — | libreoffice_convert | — | libreoffice_convert | — | libreoffice_convert | libreoffice_convert | — |
| TXT | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | — |
| RTF | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert (`png`) |
| EPUB | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | libreoffice_convert | — | — | — | — |
| MD | libreoffice_convert (→ HTML first) | libreoffice_convert (→ HTML first) | — | — | libreoffice_convert | libreoffice_convert | (passthrough) | — | — | libreoffice_convert | — |

## PDF inputs

| Target | Tool |
|---|---|
| PDF (subset of pages / merge / rotate / watermark / encrypt) | `pdf_ops` |
| DOCX, ODT, HTML, TXT, EPUB, RTF | `libreoffice_convert` |
| PPTX, ODP (one slide per page) | `libreoffice_convert` |
| PNG / JPG / TIFF / WebP (single page or all pages) | `image_magick` with `{input[0]}[<n>]` page index |
| Markdown (text extraction for LLM consumption) | `convert_file_to_markdown` |
| SVG | Two steps: `libreoffice_convert` or `image_magick` (quality varies) |

## Image inputs

| Target | Tool |
|---|---|
| Another raster/vector image format | `image_magick` |
| PDF | `image_magick` (write to `output.pdf`) |
| Animated GIF from frames / video from images | `ffmpeg` |

## Audio / video inputs

All go through `ffmpeg`. Common targets:

- MP3/OGG/OPUS/AAC/FLAC/WAV audio ↔ audio.
- MP4/WebM/MOV/MKV video ↔ video.
- Extract audio track from video (`-vn -acodec libmp3lame`).
- Extract single frame or frame range as image (`-vf fps=1 ...`).
- Trim, concat, overlay, subtitle burn-in.

`ffprobe` inspects metadata — call it before choosing an encoder if the source is unknown.

## Non-routes (not supported)

- **OCR**: neither `libreoffice_convert` nor any other tool here runs OCR. The multimodal LLM can usually describe scanned pages directly — rasterize with `image_magick` and view them inline.
- **Password-protected office documents**: LibreOffice cannot open encrypted DOCX/XLSX/PPTX without the password, and we do not prompt. Ask the user for the decrypted copy.
- **DRM-protected EPUB/PDF**: not handled. Report as unsupported.
- **SVG animations**: static SVG conversion only.
