# Copyright Solace Corporation
#
# Use of this software is governed by the Business Source License (BUSL-1.1)
# included in the LICENSE file.

"""
PDF → Markdown converter tool for STR execution.

Wraps Microsoft `markitdown` so PDF text extraction picks up real layout
reconstruction — spaces between words, paragraph breaks, column awareness —
that the pure-Go content-stream extractor in `convert_to_markdown` cannot do.

Bordered tables are additionally read from the page's ruled-cell grid and
rendered as Markdown tables at the top of the output, under a `## Tables`
heading, ahead of the document text. Prefer that section when a question turns
on which value sits in which column: the table-like text further down is
reconstructed from text positions and its cells can be shifted. The result
reports `table_extraction_status` and `table_count` so a caller can tell a
table-free PDF from one whose tables could not be read. Borderless tables are
not found this way and appear only in the document text.

This tool is intentionally PDF-only. Non-PDF formats stay on the Go
`convert_file_to_markdown` tool where the per-format handlers are mature
and avoid the Python venv cold-start cost.
"""

import io
import logging
import os
import time
from datetime import datetime, timezone

try:
    from markitdown import MarkItDown

    MARKITDOWN_AVAILABLE = True
except ImportError:
    MARKITDOWN_AVAILABLE = False

try:
    import pdfplumber

    PDFPLUMBER_AVAILABLE = True
except ImportError:
    PDFPLUMBER_AVAILABLE = False

from sam_tool_sdk import (
    Artifact,
    DataDisposition,
    DataObject,
    ToolResult,
    tool_cli,
)

log = logging.getLogger(__name__)

PREVIEW_BYTE_LIMIT = 500

MAX_TABLE_PAGES = 100
TABLE_BUDGET_SECONDS = 30

def _render_markdown_table(rows: list[list]) -> str:
    """Empty when no cell carries text: pdfplumber reports a ruled box around a figure or
    banner as a table of all-None cells, and emitting it would bury the document text under
    blank rows.

    Trailing textless columns are dropped because stray or header-only ruling lines add grid
    columns the document does not have. A column whose marks are drawn rather than typed
    (ticks, signatures) is textless too and is lost with them.
    """
    cleaned = [["" if c is None else " ".join(str(c).split()) for c in row]
               for row in rows if row]
    if not cleaned or not any(c for r in cleaned for c in r):
        return ""
    width = max(len(r) for r in cleaned)
    cleaned = [r + [""] * (width - len(r)) for r in cleaned]
    while width > 1 and all(r[width - 1] == "" for r in cleaned):
        width -= 1
        cleaned = [r[:width] for r in cleaned]

    def line(cells: list[str]) -> str:
        escaped = (c.replace("\\", "\\\\").replace("|", "\\|") for c in cells)
        return "| " + " | ".join(escaped) + " |"

    out = [line(cleaned[0]), "| " + " | ".join("---" for _ in range(width)) + " |"]
    out.extend(line(r) for r in cleaned[1:])
    return "\n".join(out)

def _extract_tables(content_bytes: bytes, log_identifier: str) -> tuple[str, str, int]:
    """Return (markdown, status, table_count) for the PDF's ruled tables.

    markitdown's PDF converter clusters pdfplumber word boxes into columns using a 25-50pt
    tolerance and a 20pt boundary slack, which collapses a narrow column into its neighbour
    and shifts every column after it. `extract_tables()` reads the ruled-cell grid instead, so
    a bordered table's cells land where the document put them.

    Only ruled tables are found: pdfplumber's default line strategies return nothing for a
    borderless table, which markitdown's word clustering is what exists to handle. Status
    distinguishes that ("none_found") from a real failure ("failed"/"partial"), because the
    caller cannot otherwise tell a table-free PDF from one whose tables were dropped.

    The pass re-parses the PDF a second time, so it is bounded by MAX_TABLE_PAGES and
    TABLE_BUDGET_SECONDS: overrunning the STR's timeout would turn a PDF that converts today
    into a hard SIGKILL, which no handler here could soften.
    """
    if not PDFPLUMBER_AVAILABLE:
        log.warning("%s pdfplumber unavailable, tables not read", log_identifier)
        return "", "unavailable", 0

    blocks: list[str] = []
    failed_pages: list[int] = []
    skipped = 0
    stopped_at = 0
    aborted = False
    page_no = 0
    try:
        pdf = pdfplumber.open(io.BytesIO(content_bytes))
    except Exception as exc:  # noqa: BLE001 - text output must survive a table failure
        log.warning("%s pdf open failed for tables, text-only output: errorType=%s",
                    log_identifier, type(exc).__name__)
        return "", "failed", 0

    deadline = time.monotonic() + TABLE_BUDGET_SECONDS
    try:
        with pdf:
            for page_no, page in enumerate(pdf.pages, start=1):
                if page_no > MAX_TABLE_PAGES or time.monotonic() > deadline:
                    stopped_at = page_no
                    break
                try:
                    tables = page.extract_tables() or []
                except Exception as exc:  # noqa: BLE001 - one bad page must not lose the others
                    if page_no not in failed_pages:
                        failed_pages.append(page_no)
                    log.warning("%s table extraction failed on page %d: errorType=%s",
                                log_identifier, page_no, type(exc).__name__)
                    if isinstance(exc, MemoryError):
                        stopped_at = page_no + 1
                        break
                    continue
                finally:
                    page.flush_cache()
                for table_no, table in enumerate(tables, start=1):
                    rendered = _render_markdown_table(table)
                    if rendered:
                        blocks.append(f"### Table {page_no}.{table_no}\n\n{rendered}")
                    else:
                        skipped += 1
    except Exception as exc:  # noqa: BLE001 - text output must survive a table failure
        log.warning("%s table scan aborted after page %s, keeping %d table(s): errorType=%s",
                    log_identifier, page_no, len(blocks), type(exc).__name__)
        aborted = True
        stopped_at = (page_no or 0) + 1

    notes = []
    if failed_pages:
        shown = ", ".join(str(p) for p in failed_pages[:10])
        more = f" and {len(failed_pages) - 10} more" if len(failed_pages) > 10 else ""
        notes.append(f"Tables could not be read on page(s) {shown}{more}."
                     " Use the document text for those.")
    if stopped_at:
        notes.append(f"Only the first {stopped_at - 1} page(s) were scanned for tables"
                     f"{'; the scan stopped early' if aborted else ''}.")
    if skipped:
        notes.append(f"{skipped} ruled area(s) held no text and were omitted.")

    degraded = bool(failed_pages or stopped_at)
    if degraded:
        log.warning("%s table scan degraded: failedPages=%d stoppedAtPage=%d aborted=%s tables=%d",
                    log_identifier, len(failed_pages), stopped_at, aborted, len(blocks))
    elif skipped:
        log.warning("%s %d ruled area(s) held no text and were omitted",
                    log_identifier, skipped)

    if not blocks:
        if not (degraded or skipped):
            return "", "none_found", 0
        body = "## Tables\n\n" + "\n\n".join(notes) + "\n\nNo table could be read from this PDF."
        return body, ("failed" if failed_pages else "partial"), 0

    body = "## Tables\n\n"
    if notes:
        body += "\n\n".join(notes) + "\n\n"
    body += "\n\n".join(blocks)
    return body, ("partial" if degraded else "ok"), len(blocks)

def _truncate_utf8(text: str, max_bytes: int) -> tuple[str, bool]:
    """Return (truncated_text, was_truncated). Splits on a UTF-8 boundary."""
    encoded = text.encode("utf-8")
    if len(encoded) <= max_bytes:
        return text, False
    cut = max_bytes
    while cut > 0 and (encoded[cut] & 0xC0) == 0x80:
        cut -= 1
    return encoded[:cut].decode("utf-8", errors="ignore"), True

async def convert_pdf_to_markdown(
    input_filename: Artifact,
    tool_context: "SandboxToolContextFacade" = None,
) -> ToolResult:
    """
    Convert a PDF artifact to Markdown using markitdown / pdfminer.six.

    Args:
        input_filename: The PDF artifact to convert. The framework loads
            the artifact bytes before invoking this tool.
        tool_context: Injected by STR tool_runner (unused).

    Returns:
        ToolResult with a `<basename>_converted.md` Markdown artifact and
        a short preview in the data payload.
    """
    if not MARKITDOWN_AVAILABLE:
        return ToolResult.error(
            "The markitdown library is required for PDF conversion but is not installed."
        )

    if input_filename is None or not input_filename.content:
        return ToolResult.error("Missing or empty input PDF artifact.")

    src_name = input_filename.filename or "input.pdf"
    log_identifier = f"[pdf_to_markdown:nameLen={len(src_name)}]"

    content_bytes = input_filename.as_bytes()

    md = MarkItDown(enable_plugins=False)
    try:
        result = md.convert_stream(
            io.BytesIO(content_bytes),
            file_extension=".pdf",
        )
    except Exception as exc:
        log.warning("%s conversion failed: %s", log_identifier, exc)
        return ToolResult.error(f"PDF conversion error: {exc}")

    markdown_text = result.text_content or ""

    tables_md, table_status, table_count = _extract_tables(content_bytes, log_identifier)
    shift_caveat = ("Tables in this section are reconstructed from text positions and may have"
                    " shifted cells.")
    if tables_md:
        markdown_text = (
            f"{tables_md}\n\n## Document text\n\n"
            f"{shift_caveat} Prefer the Tables section above where a table appears in both.\n\n"
            f"{markdown_text}"
        )
    else:
        markdown_text = f"## Document text\n\n{shift_caveat}\n\n{markdown_text}"

    base = os.path.splitext(os.path.basename(src_name))[0]
    output_filename = f"{base}_converted.md"

    preview, truncated = _truncate_utf8(markdown_text, PREVIEW_BYTE_LIMIT)
    msg = "PDF converted to Markdown successfully."
    if table_count and table_status == "ok":
        msg += (f" {table_count} ruled table(s) appear first, read from the cell borders; prefer"
                " them over the same table in the document text. A borderless table appears only"
                " in the document text.")
    elif table_count:
        msg += (f" {table_count} ruled table(s) appear first, but the scan was incomplete: read"
                " the notes at the top of that section, and treat column alignment in the"
                " document text as unreliable.")
    elif table_status == "unavailable":
        msg += (" The structural table reader is not installed in this deployment, so any table"
                " below comes from text positions and its columns may be shifted.")
    elif table_status == "none_found":
        msg += (" No bordered table was found, so any table below comes from text positions and"
                " its columns may be shifted.")
    else:
        msg += (" No table could be read structurally, so any table below comes from text"
                " positions and its columns may be shifted.")
    if truncated:
        msg += " Preview shows first portion."

    metadata = {
        "description": f"Markdown conversion of '{src_name}'",
        "source_filename": src_name,
        "source_mime_type": "application/pdf",
        "generation_tool": "convert_pdf_to_markdown",
        "generation_timestamp": datetime.now(timezone.utc).isoformat(),
    }

    return ToolResult.ok(
        msg,
        data={
            "result_preview": preview,
            "result_truncated": truncated,
            "table_extraction_status": table_status,
            "table_count": table_count,
        },
        data_objects=[
            DataObject(
                name=output_filename,
                content=markdown_text,
                mime_type="text/markdown",
                disposition=DataDisposition.ARTIFACT,
                description=f"Markdown conversion of '{src_name}'",
                metadata=metadata,
            )
        ],
    )

cli = tool_cli(convert_pdf_to_markdown)
