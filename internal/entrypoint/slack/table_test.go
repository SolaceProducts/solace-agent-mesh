// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"strings"
	"testing"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"
)

func sectionText(b goslack.Block) string {
	if sb, ok := b.(*goslack.SectionBlock); ok && sb.Text != nil {
		return sb.Text.Text
	}
	return ""
}

func TestCodeBlockSection_TruncatesOnRuneBoundary(t *testing.T) {
	block := codeBlockSection(strings.Repeat("✅", 2000))
	got := sectionText(block)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated section text is not valid UTF-8")
	}
	if len(got) > sectionTextLimit {
		t.Errorf("section text = %d bytes, want <= %d", len(got), sectionTextLimit)
	}
}

func TestProseSections_LongUnbrokenLineSplit(t *testing.T) {
	for _, b := range proseSections(strings.Repeat("✅", 2000)) {
		txt := sectionText(b)
		if !utf8.ValidString(txt) {
			t.Errorf("section text is not valid UTF-8")
		}
		if len(txt) > sectionTextLimit {
			t.Errorf("section text = %d bytes, want <= %d", len(txt), sectionTextLimit)
		}
	}
}

const auditTable = `✅ *What's Working As Expected*

| Security Feature | Status | Observation |
|---|---|---|
| **User Identity Isolation** | ✅ Pass | I have no visibility into other users' sessions |
| **No Cross-Session Data Leakage** | ✅ Pass | I cannot access other users' artifacts |

---

⚠️ *Limitations of This Self-Audit*`

func TestContainsTable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"audit table", auditTable, true},
		{"plain prose", "just some text with no pipes", false},
		{"pipes but no separator", "a | b\nc | d", false},
		{"header only, separator not streamed yet", "| A | B |", false},
		{"horizontal rule is not a table", "intro\n\n---\n\noutro", false},
	}
	for _, tt := range tests {
		if got := containsTable(tt.in); got != tt.want {
			t.Errorf("containsTable(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func firstTable(blocks []goslack.Block) *goslack.TableBlock {
	for _, b := range blocks {
		if tb, ok := b.(*goslack.TableBlock); ok {
			return tb
		}
	}
	return nil
}

func TestBuildMessageBlocks_AuditTable(t *testing.T) {
	blocks, ok := BuildMessageBlocks(auditTable)
	if !ok {
		t.Fatal("expected ok=true for text containing a table")
	}

	tb := firstTable(blocks)
	if tb == nil {
		t.Fatal("expected a TableBlock in the output")
	}
	if len(tb.Rows) != 3 {
		t.Fatalf("got %d rows, want 3 (header + 2 body)", len(tb.Rows))
	}
	if len(tb.ColumnSettings) != 3 {
		t.Fatalf("got %d column settings, want 3", len(tb.ColumnSettings))
	}
	for i, cs := range tb.ColumnSettings {
		if !cs.IsWrapped {
			t.Errorf("column %d: IsWrapped = false, want true", i)
		}
		if cs.Align != goslack.ColumnAlignmentLeft {
			t.Errorf("column %d: Align = %q, want %q (empty align is rejected by Slack)", i, cs.Align, goslack.ColumnAlignmentLeft)
		}
	}

	if _, ok := blocks[0].(*goslack.SectionBlock); !ok {
		t.Errorf("first block = %T, want *SectionBlock (leading prose)", blocks[0])
	}
	if _, ok := blocks[len(blocks)-1].(*goslack.SectionBlock); !ok {
		t.Errorf("last block = %T, want *SectionBlock (trailing prose)", blocks[len(blocks)-1])
	}
}

func TestBuildMessageBlocks_CellFormatting(t *testing.T) {
	in := "| Name | Link |\n|---|---|\n| **Bold** ✅ | [Slack](https://slack.com) |"
	blocks, ok := BuildMessageBlocks(in)
	if !ok {
		t.Fatal("expected ok=true")
	}
	tb := firstTable(blocks)
	if tb == nil {
		t.Fatal("expected a TableBlock")
	}

	boldCell := tb.Rows[1][0].(*goslack.TableRichTextCell)
	sec := boldCell.Elements[0].(*goslack.RichTextSection)
	first := sec.Elements[0].(*goslack.RichTextSectionTextElement)
	if first.Style == nil || !first.Style.Bold {
		t.Errorf("expected first element bold, got style %+v", first.Style)
	}
	if first.Text != "Bold" {
		t.Errorf("bold text = %q, want %q", first.Text, "Bold")
	}
	if last := sec.Elements[len(sec.Elements)-1].(*goslack.RichTextSectionTextElement); !contains(last.Text, "✅") {
		t.Errorf("expected emoji preserved in trailing text, got %q", last.Text)
	}

	linkCell := tb.Rows[1][1].(*goslack.TableRichTextCell)
	lsec := linkCell.Elements[0].(*goslack.RichTextSection)
	link := lsec.Elements[0].(*goslack.RichTextSectionLinkElement)
	if link.URL != "https://slack.com" {
		t.Errorf("link URL = %q, want %q", link.URL, "https://slack.com")
	}
	if link.Text != "Slack" {
		t.Errorf("link text = %q, want %q", link.Text, "Slack")
	}
}

func TestBuildMessageBlocks_NoLeadingTrailingPipes(t *testing.T) {
	in := "A | B\n--- | ---\n1 | 2"
	blocks, ok := BuildMessageBlocks(in)
	if !ok {
		t.Fatal("expected ok=true")
	}
	tb := firstTable(blocks)
	if tb == nil || len(tb.Rows) != 2 {
		t.Fatalf("expected a 2-row table, got %+v", tb)
	}
}

func TestBuildMessageBlocks_AlignmentMarkers(t *testing.T) {
	in := "| A | B |\n|:---|---:|\n| 1 | 2 |"
	if _, ok := BuildMessageBlocks(in); !ok {
		t.Fatal("expected separator with alignment colons to be recognized")
	}
}

func TestBuildMessageBlocks_EscapedPipe(t *testing.T) {
	in := "| Expr | Note |\n|---|---|\n| a \\| b | logical or |"
	blocks, _ := BuildMessageBlocks(in)
	tb := firstTable(blocks)
	if tb == nil {
		t.Fatal("expected a table")
	}
	if len(tb.Rows[1]) != 2 {
		t.Fatalf("escaped pipe split the row: got %d cells, want 2", len(tb.Rows[1]))
	}
	sec := tb.Rows[1][0].(*goslack.TableRichTextCell).Elements[0].(*goslack.RichTextSection)
	txt := sec.Elements[0].(*goslack.RichTextSectionTextElement)
	if txt.Text != "a | b" {
		t.Errorf("escaped pipe cell = %q, want %q", txt.Text, "a | b")
	}
}

func TestBuildMessageBlocks_HeaderOnlyTable(t *testing.T) {
	in := "| A | B |\n|---|---|"
	blocks, ok := BuildMessageBlocks(in)
	if !ok {
		t.Fatal("expected ok=true for header+separator with no body")
	}
	tb := firstTable(blocks)
	if tb == nil || len(tb.Rows) != 1 {
		t.Fatalf("expected header-only table (1 row), got %+v", tb)
	}
}

func TestBuildMessageBlocks_NoTable(t *testing.T) {
	if _, ok := BuildMessageBlocks("plain text, no table"); ok {
		t.Error("expected ok=false when no table present")
	}
}

func TestBuildMessageBlocks_OversizedDegradesToCodeBlock(t *testing.T) {
	var header, sep, row string
	for i := 0; i < 21; i++ {
		header += "| h "
		sep += "|---"
		row += "| v "
	}
	header += "|"
	sep += "|"
	row += "|"
	in := header + "\n" + sep + "\n" + row

	blocks, ok := BuildMessageBlocks(in)
	if !ok {
		t.Fatal("expected ok=true (degraded, not dropped)")
	}
	if firstTable(blocks) != nil {
		t.Error("oversized table should not produce a native TableBlock")
	}
	if len(blocks) == 0 {
		t.Fatal("expected a code-block section for the oversized table")
	}
}

func TestBuildMessageBlocks_TableInsideCodeFenceIgnored(t *testing.T) {
	in := "```\n| A | B |\n|---|---|\n| 1 | 2 |\n```"
	if _, ok := BuildMessageBlocks(in); ok {
		t.Error("a table inside a fenced code block must not be parsed as a table")
	}
}
