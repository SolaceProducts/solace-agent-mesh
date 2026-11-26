# Copyright Solace Corporation
#
# Use of this software is governed by the Business Source License (BUSL-1.1)
# included in the LICENSE file.

"""Tests for the structural table path in convert_pdf_to_markdown.

Regression origin: markitdown's PDF converter clusters pdfplumber word boxes into columns with
a coarse tolerance, which collapses a narrow column into its neighbour. On a hotel-ratings PDF
it emitted

    | Neptune's Palace |     | 5 Yes | Yes | A hotel fit for a king. |

so an agent comparing ratings read them out of the Vacancy column. extract_tables() reads the
ruled-cell grid instead.

testdata/ruled_table.pdf is the regression shape — a narrow numeric column between wide text
columns — committed as bytes so these tests need no PDF writer.

Run:
    uv run --python 3.13 --with pytest --with pdfplumber --with "markitdown[pdf]" \
        --with ./python/sam-tool-sdk python -m pytest tools/python/pdf_to_markdown/ -q
"""

import importlib.util
import logging
import pathlib
import sys

import pytest

_HERE = pathlib.Path(__file__).parent
_SPEC = importlib.util.spec_from_file_location(
    "pdf_to_markdown_tool", _HERE / "pdf_to_markdown_tool.py"
)
_MOD = importlib.util.module_from_spec(_SPEC)
sys.modules["pdf_to_markdown_tool"] = _MOD
try:
    _SPEC.loader.exec_module(_MOD)
except ImportError as exc:
    pytest.skip(f"tool dependency missing: {exc}", allow_module_level=True)

render = _MOD._render_markdown_table
extract = _MOD._extract_tables

RULED_TABLE_PDF = (_HERE / "testdata" / "ruled_table.pdf").read_bytes()
PROSE_PDF = (_HERE / "testdata" / "no_table.pdf").read_bytes()

needs_pdfplumber = pytest.mark.skipif(
    not _MOD.PDFPLUMBER_AVAILABLE, reason="pdfplumber not installed"
)
needs_markitdown = pytest.mark.skipif(
    not _MOD.MARKITDOWN_AVAILABLE, reason="markitdown not installed"
)

@needs_pdfplumber
def test_narrow_column_keeps_its_own_cell_end_to_end():
    """The regression. Rating must hold "5" rather than merging into Vacancy."""
    md, status, count = extract(RULED_TABLE_PDF, "[test]")
    assert status == "ok"
    assert count == 1
    assert "| Neptune's Palace | 5 | Yes | A hotel fit for a king. |" in md

@needs_pdfplumber
def test_extraction_emits_one_tables_section_with_a_header_row():
    md, _, _ = extract(RULED_TABLE_PDF, "[test]")
    lines = md.splitlines()
    assert lines[0] == "## Tables"
    assert "| Name | Rating | Vacancy | Sample Review |" in lines
    separators = [l for l in lines if set(l.replace("|", "").replace(" ", "")) == {"-"}]
    assert len(separators) == 1

@needs_pdfplumber
def test_rows_are_not_transposed():
    """A transposed grid would put the header down the side and still look table-shaped."""
    md, _, _ = extract(RULED_TABLE_PDF, "[test]")
    assert "| Name | Neptune's Palace" not in md

@needs_pdfplumber
def test_a_pdf_with_no_ruled_tables_reports_none_found():
    """Distinguishes a table-free PDF from one whose tables were dropped."""
    assert extract(PROSE_PDF, "[test]") == ("", "none_found", 0)

@needs_pdfplumber
def test_open_failure_reports_failed_not_none_found():
    md, status, count = extract(b"not a pdf at all", "[test]")
    assert (md, status, count) == ("", "failed", 0)

@needs_pdfplumber
def test_a_failing_page_does_not_discard_earlier_pages(monkeypatch):
    """One bad page used to throw away every table already extracted."""
    real_open = _MOD.pdfplumber.open

    class ExplodingPage:
        def extract_tables(self):
            raise ValueError("bounding box is not fully within parent page")

        def flush_cache(self):
            pass

    class SecondPageFails:
        """pdfplumber's pages attribute is read-only, so the document is wrapped rather than
        mutated."""

        def __init__(self, inner):
            self.inner = inner
            self.pages = [inner.pages[0], ExplodingPage()]

        def __enter__(self):
            return self

        def __exit__(self, *exc_info):
            self.inner.close()
            return False

    monkeypatch.setattr(_MOD.pdfplumber, "open", lambda stream: SecondPageFails(real_open(stream)))
    md, status, count = extract(RULED_TABLE_PDF, "[test]")
    assert count == 1, "the good page's table must survive"
    assert status == "partial"
    assert "page(s) 2" in md

def test_extract_returns_unavailable_without_pdfplumber(monkeypatch):
    monkeypatch.setattr(_MOD, "PDFPLUMBER_AVAILABLE", False)
    assert extract(b"%PDF-1.4", "[test]") == ("", "unavailable", 0)

@needs_pdfplumber
def test_a_bound_that_cuts_in_before_any_table_is_not_reported_as_none_found(monkeypatch):
    """Truncating to zero tables must not look like a table-free document: none_found would hide
    the fact that pages went unscanned."""
    monkeypatch.setattr(_MOD, "MAX_TABLE_PAGES", 0)
    md, status, count = extract(RULED_TABLE_PDF, "[test]")
    assert count == 0
    assert status == "partial", "a truncated scan is not a table-free PDF"
    assert "were scanned for tables" in md, "the truncation must be stated, not just counted"

@needs_pdfplumber
def test_a_blank_ruled_box_does_not_downgrade_a_good_extraction(monkeypatch):
    """pdfplumber reports a ruled box around a figure as an all-None table, so it is routine.
    Letting it force "partial" would tell the caller a correct extraction is untrustworthy on
    most real documents."""
    blank_box = [[None, None], [None, None]]
    real_table = [["Name", "Rating"], ["Neptune's Palace", "5"]]

    class PageWithABlankBox:
        def extract_tables(self):
            return [blank_box, real_table]

        def flush_cache(self):
            pass

    class OnePage:
        pages = [PageWithABlankBox()]

        def __enter__(self):
            return self

        def __exit__(self, *exc_info):
            return False

    monkeypatch.setattr(_MOD.pdfplumber, "open", lambda stream: OnePage())
    md, status, count = extract(RULED_TABLE_PDF, "[test]")
    assert count == 1
    assert status == "ok", "a textless ruled box is not a degradation"
    assert "held no text" in md, "it is still worth stating"

@needs_pdfplumber
def test_a_broken_page_tree_degrades_to_text_only_rather_than_raising(monkeypatch):
    """pdf.pages is a lazy property that re-raises parse failures. If it escapes, the caller
    loses text markitdown already produced."""
    class NoPages:
        @property
        def pages(self):
            raise ValueError("corrupt page tree")

        def __enter__(self):
            return self

        def __exit__(self, *exc_info):
            return False

    monkeypatch.setattr(_MOD.pdfplumber, "open", lambda stream: NoPages())
    md, status, count = extract(RULED_TABLE_PDF, "[test]")
    assert count == 0
    assert status == "partial", "the scan stopped; nothing says the document has no tables"
    assert "scan stopped early" in md

@needs_pdfplumber
def test_an_abort_after_a_good_page_keeps_it_and_does_not_blame_it(monkeypatch):
    """The scan stopping must not accuse a page whose table is in the output, and must say the
    later pages went unscanned rather than letting their absence read as "no tables there"."""
    real_open = _MOD.pdfplumber.open

    class ExitFails:
        def __init__(self, inner):
            self.inner = inner
            self.pages = [inner.pages[0]]

        def __enter__(self):
            return self

        def __exit__(self, *exc_info):
            raise OSError("closing the document failed")

    monkeypatch.setattr(_MOD.pdfplumber, "open", lambda stream: ExitFails(real_open(stream)))
    md, status, count = extract(RULED_TABLE_PDF, "[test]")
    assert count == 1, "the table that was read must survive the abort"
    assert status == "partial"
    assert "scan stopped early" in md
    assert "could not be read on page(s) 1" not in md, "page 1 succeeded; its table is right there"

@needs_pdfplumber
def test_notes_precede_the_first_table(monkeypatch):
    """Position, not just presence: a note after the blocks is outside the byte-capped preview."""
    monkeypatch.setattr(_MOD, "MAX_TABLE_PAGES", 1)
    md, _, _ = extract(RULED_TABLE_PDF, "[test]")
    if "### Table" in md and "were scanned" in md:
        assert md.index("were scanned") < md.index("### Table")

@needs_pdfplumber
def test_a_long_failed_page_list_does_not_crowd_out_the_tables(monkeypatch):
    """An unbounded page enumeration ahead of the tables evicted them from the preview."""
    real_open = _MOD.pdfplumber.open

    class ManyBadPages:
        def __init__(self, inner):
            self.inner = inner

            class Bad:
                def extract_tables(self):
                    raise ValueError("unreadable")

                def flush_cache(self):
                    pass

            self.pages = [Bad() for _ in range(40)] + [inner.pages[0]]

        def __enter__(self):
            return self

        def __exit__(self, *exc_info):
            self.inner.close()
            return False

    monkeypatch.setattr(_MOD.pdfplumber, "open", lambda stream: ManyBadPages(real_open(stream)))
    md, status, count = extract(RULED_TABLE_PDF, "[test]")
    assert count == 1
    assert "and 30 more" in md, "the page list must be capped"
    assert md.index("### Table") < _MOD.PREVIEW_BYTE_LIMIT, "the table must reach the preview"

def test_the_unavailable_path_warns(monkeypatch, caplog):
    """The only signal that a deployment silently strips tables from every PDF."""
    monkeypatch.setattr(_MOD, "PDFPLUMBER_AVAILABLE", False)
    with caplog.at_level(logging.WARNING):
        extract(b"%PDF-1.4", "[test]")
    assert any("pdfplumber unavailable" in r.message for r in caplog.records)

@needs_pdfplumber
def test_a_degraded_scan_warns(monkeypatch, caplog):
    monkeypatch.setattr(_MOD, "MAX_TABLE_PAGES", 0)
    with caplog.at_level(logging.WARNING):
        extract(RULED_TABLE_PDF, "[test]")
    assert any("table scan degraded" in r.message for r in caplog.records)

def _convert(pdf_bytes: bytes):
    import asyncio

    from sam_tool_sdk import Artifact

    artifact = Artifact(content=pdf_bytes, filename="ratings.pdf", version=0,
                        mime_type="application/pdf", metadata={})
    return asyncio.run(_MOD.convert_pdf_to_markdown(artifact))

@needs_pdfplumber
@needs_markitdown
def test_tables_precede_the_document_text():
    """Ordering is load-bearing: the result carries only a PREVIEW_BYTE_LIMIT preview, so
    tables placed after the prose would fall outside what most callers ever read."""
    result = _convert(RULED_TABLE_PDF)
    body = result.data_objects[0].content
    assert body.index("## Tables") < body.index("## Document text")
    assert result.data["result_preview"].startswith("## Tables")

@needs_pdfplumber
@needs_markitdown
def test_status_and_count_are_always_reported():
    """A successful exit discards the worker's stderr, so the result payload is the only place
    a dropped-tables degradation can surface."""
    result = _convert(RULED_TABLE_PDF)
    assert result.data["table_extraction_status"] == "ok"
    assert result.data["table_count"] == 1
    assert "ruled table(s)" in result.message
    assert "All " not in result.message, "ok says nothing about borderless tables"
    assert "borderless" in result.message

@needs_pdfplumber
@needs_markitdown
def test_a_prose_pdf_converts_with_no_tables_section():
    """The common case. A document with no tables must not gain an empty Tables heading, and
    must still report a status so a caller can tell it from a failure."""
    result = _convert(PROSE_PDF)
    assert result.data["table_extraction_status"] == "none_found"
    assert result.data["table_count"] == 0
    body = result.data_objects[0].content
    assert "## Tables" not in body
    assert "may have shifted cells" in body, "the only table present is the shifted one"
    assert "ruled table(s)" not in result.message

def test_exactly_one_separator_row():
    """markitdown emitted a separator per detected table region, splitting rows apart."""
    out = render([["a", "b"], ["1", "2"], ["3", "4"]]).splitlines()
    separators = [l for l in out if set(l.replace("|", "").replace(" ", "")) == {"-"}]
    assert len(separators) == 1
    assert out[1] == separators[0]

def test_none_cells_become_empty_strings():
    assert render([["a", None], [None, "b"]]).splitlines()[0] == "| a |  |"

def test_newlines_inside_cells_are_flattened():
    """A cell containing a newline would otherwise break out of its table row."""
    out = render([["h1", "h2"], ["line1\nline2", "x"]])
    assert "line1 line2" in out
    assert len(out.splitlines()) == 3

def test_carriage_returns_are_flattened_too():
    """CommonMark ends a line on a bare \\r, so it breaks the row exactly as \\n does."""
    assert "\r" not in render([["h"], ["a\rb"]])

def test_pipes_in_cells_are_escaped():
    assert r"a \| b" in render([["h"], ["a | b"]])

def test_backslashes_are_escaped_before_pipes():
    """Without escaping the backslash first, a cell of 'a\\|b' renders as an escaped pipe and
    the literal text is lost."""
    out = render([["h"], [r"a\|b"]])
    assert r"a\\\|b" in out

def test_ragged_rows_are_padded_to_the_widest():
    assert render([["a", "b", "c"], ["1"]]).splitlines()[2] == "| 1 |  |  |"

def test_trailing_empty_columns_are_dropped():
    """Stray or header-only ruling lines add grid columns the document does not have."""
    out = render([["Name", "Rating", "", ""], ["Neptune's", "5", "", ""]]).splitlines()
    assert out[0] == "| Name | Rating |"
    assert out[2] == "| Neptune's | 5 |"

def test_an_all_empty_column_still_renders_a_valid_table():
    """The trim stops at one column. Trimming to zero degenerates the separator to '|  |',
    which is no longer a table."""
    out = render([[""], [""]])
    assert out == ""

def test_a_table_with_no_text_anywhere_is_dropped():
    """pdfplumber reports a ruled box around a figure as an all-None table; emitting it would
    bury the document text under blank rows and eat the preview."""
    assert render([[None, None], ["", ""]]) == ""

def test_a_wholly_empty_table_renders_nothing():
    assert render([]) == ""
    assert render([[]]) == ""
