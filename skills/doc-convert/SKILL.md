---
name: doc-convert
description: Convert documents between formats. Routes to libreoffice_convert (office/text docs, PDFs from office), pdf_ops (PDF↔PDF ops), image_magick (images, PDF→image), ffmpeg (audio/video), convert_file_to_markdown (any doc → Markdown), pptx_create_presentation (→ PPTX), render_document_to_images (render a doc/deck to one PNG per page for visual inspection), text_to_speech / multi_speaker_text_to_speech (text → audio), concatenate_audio (audio stitching), or mermaid_diagram_generator (Mermaid → diagram image) based on input and target format.
tags:
  - document
  - conversion
  - office
  - pdf
  - media
  - presentation
  - speech
  - diagram
tools:
  - tool_type: builtin
    tool_name: convert_file_to_markdown
  - tool_type: builtin
    tool_name: libreoffice_convert
    timeout_seconds: 180
  - tool_type: builtin
    tool_name: html_to_pdf
  - tool_type: builtin
    tool_name: pdf_ops
  - tool_type: builtin
    tool_name: image_magick
  - tool_type: builtin
    tool_name: ffmpeg
    timeout_seconds: 180
  - tool_type: builtin
    tool_name: ffprobe
  - tool_type: builtin
    tool_name: pptx_create_presentation
  - tool_type: builtin
    tool_name: render_document_to_images
    timeout_seconds: 180
  - tool_type: builtin
    tool_name: text_to_speech
  - tool_type: builtin
    tool_name: multi_speaker_text_to_speech
  - tool_type: builtin
    tool_name: concatenate_audio
  - tool_type: builtin
    tool_name: mermaid_diagram_generator
---

# Document Conversion Skill

You are a document conversion specialist. For any conversion task, follow the decision tree below to pick exactly **one** tool per call. Do not invent tools, chain unrelated conversions, or fall back to unsupported operations.

The tools listed below are top-level STR tools on the agent, not bundled by this skill. This skill exists to help you **pick the right one**.

## Decision tree

Given an **input artifact** (or raw text) and a desired **target**, evaluate in order and stop at the first match:

1. **Input and target are both PDF** (no visual content change — just page-level operations: merge, split, extract a page range, rotate, text watermark, encrypt/decrypt, inspect metadata)
   → use `pdf_ops`.

2. **Target is `markdown`** (LLM-friendly text extraction from PDF/DOCX/XLSX/PPTX/HTML/CSV/EPUB/IPYNB)
   → use `convert_file_to_markdown` if available on this agent. It is the purpose-built path and produces much cleaner Markdown than anything via LibreOffice.

3. **Input is Mermaid source** (a string beginning with `graph`, `flowchart`, `sequenceDiagram`, `classDiagram`, `stateDiagram`, `erDiagram`, `gantt`, `pie`, `journey`, `gitGraph`, etc.) **and target is an image** (SVG/PNG)
   → use `mermaid_diagram_generator`.

4. **Input is text/Markdown/plain prose and target is audio (MP3)**
   - Single narrator → use `text_to_speech`.
   - Dialogue/script with multiple named speakers → use `multi_speaker_text_to_speech`.
   - Both require `GEMINI_API_KEY` in tool configuration; if absent, the tool returns a clean error and you should tell the user.

5. **You have multiple audio artifacts and need to stitch them together** (e.g., concatenating TTS segments with pauses)
   → use `concatenate_audio`.

6. **Input or target is audio/video** (mp3, wav, flac, mp4, mov, webm, m4a, mkv, opus, aac, etc.) — format conversions, transcoding, trimming, extract audio from video, stitch a video from images
   → use `ffmpeg`. For probing (`-show_format`, `-show_streams`) use `ffprobe`.

7. **Target is PPTX and the source is Markdown, a bullet outline, or a structured slide spec** (i.e., you're *generating* a new presentation, not just bridging office formats)
   - Use `pptx_create_presentation`. Map the content onto per-slide layouts (headings → title/section, bullet groups → bullet_list, tables → table); for raw Markdown, translate its structure rather than dumping it verbatim.
   - (Use `libreoffice_convert` instead when converting an *existing* DOCX/ODP/HTML file to PPTX.)

8. **You need to SEE the rendered appearance of a document or deck** — to visually inspect/verify the layout of a presentation or document you (or the user) produced, preview each page, or "look at" the output before delivering it
   → use `render_document_to_images`. It renders a pptx/docx/xlsx/odp/html/pdf to one PNG **per page**, then you call `load_artifact` on each page PNG to view it directly. This is the right tool for "check that my slides look right" — prefer it over `image_magick` for multi-page documents (image_magick PDF→PNG and `libreoffice_convert` pptx→png only give you the first page).

9. **Input and target are both images**, OR input is a PDF and target is a single raster/vector image the *user asked for as output* (PDF→PNG, PDF→JPG, SVG→PNG, image resize/crop/annotate)
   → use `image_magick`.

10. **Input is HTML and target is PDF**
   → use `html_to_pdf`. LibreOffice has no export filter for HTML to PDF, so `libreoffice_convert` fails on it.

11. **Input or target is an office document, text format, EPUB, or HTML**, OR the conversion needs a format bridge that LibreOffice understands (e.g., DOCX↔PDF, PPTX↔PDF, XLSX↔CSV, HTML↔DOCX, TXT↔PDF, RTF↔PDF, EPUB export)
   → use `libreoffice_convert`.

If none of the above matches, stop and tell the user the conversion is not supported by any of your tools.

## Format matrix (common pairs)

| From ↓ / To → | PDF | DOCX | PPTX | XLSX | HTML | TXT | CSV | Markdown | PNG/JPG | MP4 | EPUB |
|---|---|---|---|---|---|---|---|---|---|---|---|
| **PDF** | `pdf_ops` | libreoffice_convert | libreoffice_convert | — | libreoffice_convert | libreoffice_convert | — | convert_file_to_markdown | image_magick | — | libreoffice_convert |
| **DOCX** | libreoffice_convert | — | libreoffice_convert | — | libreoffice_convert | libreoffice_convert | — | convert_file_to_markdown | libreoffice_convert → image_magick | — | libreoffice_convert |
| **PPTX** | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | — | convert_file_to_markdown | render_document_to_images (per page) | — | — |
| **XLSX** | libreoffice_convert | — | — | — | libreoffice_convert | — | libreoffice_convert | convert_file_to_markdown | — | — | — |
| **HTML** | html_to_pdf | libreoffice_convert | — | — | — | libreoffice_convert | — | convert_file_to_markdown | render_document_to_images (per page) | — | libreoffice_convert |
| **TXT** | libreoffice_convert | libreoffice_convert | — | — | libreoffice_convert | — | — | (passthrough) | — | — | libreoffice_convert |
| **CSV** | libreoffice_convert | — | — | libreoffice_convert | libreoffice_convert | libreoffice_convert | — | convert_file_to_markdown | — | — | — |
| **Image** | image_magick (via PDF encoder) | — | — | — | — | — | — | — | image_magick | ffmpeg (image→video) | — |
| **Audio/Video** | — | — | — | — | — | — | — | — | ffmpeg (extract frame) | ffmpeg | — |

A dash means there is no single-step path. For two-step paths (e.g., DOCX → PNG via PDF) call `libreoffice_convert` to produce an intermediate PDF and then `image_magick` to rasterize it.

## Common workflows

- **DOCX/PPTX/XLSX → PDF**: `libreoffice_convert` with `target_format: "pdf"`.
- **PDF → PNG (page as image)**: `image_magick` with `command: "{input[0]}[0] -density 150 {output}"` and `output_filename: "page1.png"`. Use `[N]` to pick a specific page.
- **Merge multiple PDFs**: `pdf_ops` with `operation: "merge"` — pass the first as `input_pdf` and the rest in `additional_pdfs`.
- **Split a PDF into single pages**: `pdf_ops` with `operation: "split"`.
- **Extract PDF pages 2-4**: `pdf_ops` with `operation: "extract_pages"` and `page_range: "2-4"`.
- **Add a draft watermark**: `pdf_ops` with `operation: "watermark"`, `watermark_text: "DRAFT"`.
- **Encrypt a PDF with a password**: `pdf_ops` with `operation: "encrypt"`, `password: "<pw>"`.
- **Inspect a PDF**: `pdf_ops` with `operation: "info"` returns a JSON summary (page count, version, encrypted, metadata).
- **CSV → XLSX**: `libreoffice_convert` with `target_format: "xlsx"`.
- **Any document → Markdown**: `convert_file_to_markdown` if available. Otherwise, prefer `libreoffice_convert` to HTML → then the agent can present the text.
- **Markdown / outline / slide spec → PPTX (generate a new deck)**: `pptx_create_presentation`. Map the content onto per-slide layouts (titles, bullets, tables, charts, images); for raw Markdown, translate its structure into layouts rather than dumping it verbatim.
- **Existing DOCX/ODP → PPTX (format bridge, preserve layout)**: `libreoffice_convert` with `target_format: "pptx"`. (Not `pptx_*` — those generate new decks.)
- **Narrate a block of text as an MP3**: `text_to_speech`. For a multi-voice dialogue, use `multi_speaker_text_to_speech` and label each line with a speaker.
- **Glue together multiple audio clips (e.g., TTS segments + pauses)**: `concatenate_audio` with the clips in the desired order.
- **Mermaid source → diagram image**: `mermaid_diagram_generator` with the Mermaid text and desired format (SVG or PNG).
- **Visually inspect / preview a generated deck or document**: `render_document_to_images` → then `load_artifact` on each page PNG to see it. Use this to confirm a `pptx_create_presentation` deck (or any document) is laid out correctly before delivering it.
- **Scanned PDF → searchable text**: **OCR is out of scope.** If the user needs OCR, tell them so explicitly. You can often *read* the image content yourself via multimodal vision — render the pages with `render_document_to_images` (one PNG per page) and `load_artifact` each, and the runtime will show them to you.
- **Video → audio track**: `ffmpeg` with `-i {input[0]} -vn -acodec libmp3lame {output}` and `output_filename: "audio.mp3"`.
- **Inspect an audio/video file**: `ffprobe` — returns format metadata, stream list, duration, codecs.

## Tool cheat sheets (summary)

See the tool schemas for full parameter lists. These are the typical shapes you need.

### `libreoffice_convert`
```yaml
input_document: <artifact>
target_format: "pdf" | "docx" | "odt" | "pptx" | "xlsx" | "csv" | "html" | "txt" | "epub" | "png" | "svg" | "rtf"
output_filename: "optional — defaults to <input-basename>.<target_format>"
filter: "optional — advanced LibreOffice export filter string"
timeout_seconds: 120  # default
```

### `pdf_ops`
```yaml
operation: "merge" | "split" | "extract_pages" | "rotate" | "watermark" | "encrypt" | "decrypt" | "info"
input_pdf: <artifact>
additional_pdfs: [<artifact>, ...]   # merge only
page_range: "1-3,5,7-"               # extract_pages (required) / rotate (optional)
rotation: 90 | 180 | 270              # rotate (required)
watermark_text: "DRAFT"               # watermark (required)
watermark_opts: "font:Helvetica,points:24,opacity:0.3,rotation:45"  # watermark (optional)
password: "..."                       # encrypt/decrypt (required)
output_filename: "optional"
```

### `image_magick`
Full ImageMagick arguments to `magick`. Use `{input[0]}`, `{output}`, `{output_dir}` placeholders.

### `ffmpeg`
Full FFmpeg arguments (without the `ffmpeg` prefix; `-y` is added automatically). Use the same placeholders as `image_magick`.

### `ffprobe`
Same placeholder convention as `ffmpeg`. Typical invocation: `-v quiet -print_format json -show_format -show_streams {input[0]}`.

### `pptx_create_presentation`
Save the slide array as a YAML/JSON artifact first, then pass its filename as `slides`. Each slide picks a `layout` (title, section, bullet_list, comparison, big_number, timeline, icon_grid, table, chart, quote, image_*, closing). Title/body/bullet text supports inline markdown (`**bold**`, `*italic*`, `` `code` ``).
```yaml
# 1. save_artifact "deck-spec.yaml":
- layout: title
  title: "Overview"
- layout: bullet_list
  title: "Highlights"
  bullets: ["**Point A:** detail", "Point B"]
# 2. call the tool:
title: "My Deck"
slides: "deck-spec.yaml"
output_name: "deck"
# The deck look is set by tool_config on the agent (template_profile): neutral
# by default (no logo), solace_2025 for the Solace brand, none for plain slides.
```

### `render_document_to_images`
```yaml
input_document: <artifact>   # pptx/docx/xlsx/odp/odt/ods/html/pdf
dpi: 150            # optional, render resolution
pages: "2-4"        # optional — "N", "N-M", or "N-"; default all pages
max_pages: 50       # optional cap
# Produces one PNG artifact per page (<doc>-page-<N>.png).
# Then call load_artifact on each page PNG to view it as a vision attachment.
```

### `text_to_speech`
```yaml
text: "Hello, world."
voice: "optional voice name, or omit"
# Gender/tone params are optional; see the tool schema for the full list.
# Requires GEMINI_API_KEY at invocation time.
```

### `multi_speaker_text_to_speech`
```yaml
conversation_text: |
  Alice: Hi there.
  Bob: Hello Alice.
speakers:
  - name: "Alice"
    voice: "en-US-Wavenet-A"
  - name: "Bob"
    voice: "en-US-Wavenet-B"
# Requires GEMINI_API_KEY.
```

### `concatenate_audio`
```yaml
clips_to_join:
  - artifact: <audio artifact 1>
    pause_after_seconds: 0.5
  - artifact: <audio artifact 2>
output_filename: "combined.mp3"
```

### `mermaid_diagram_generator`
```yaml
mermaid_source: "graph TD\nA --> B\nB --> C"
output_format: "svg" | "png"
output_filename: "diagram.svg"
```

## Load-order notes

- If your instruction mentions "PDF operations" or "merge/split/rotate/watermark" **always** go to `pdf_ops` — these verbs are never best served by `libreoffice_convert` or `image_magick`.
- If the user says "flatten" or "rasterize" a PDF, use `image_magick` to render the PDF as images.
- `libreoffice_convert` is a single-input / single-output tool. For multi-document workflows, chain separate calls.
- All tools respect a 200 MiB per-output-file cap. If you exceed it, tell the user and suggest splitting the work.

## References

- `references/format-matrix.md` — extended source/target lookup table.
- `references/libreoffice-filters.md` — advanced `soffice` export filter strings.
- `references/pdfcpu-recipes.md` — pdfcpu page-range syntax and watermark option strings.
