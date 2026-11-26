// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestConvertText(t *testing.T) {
	lines := make([]string, 120)
	for i := range lines {
		lines[i] = "line content here"
	}
	text := strings.Join(lines, "\n")

	result, err := ConvertText([]byte(text))
	if err != nil {
		t.Fatalf("ConvertText failed: %v", err)
	}
	if result.CitationType != "line_range" {
		t.Errorf("expected citation_type 'line_range', got %q", result.CitationType)
	}
	if result.Text != text {
		t.Error("text should be unchanged")
	}
	if len(result.Citations) != 3 {
		t.Fatalf("expected 3 citations, got %d", len(result.Citations))
	}

	c := result.Citations[0]
	if c.Location != "lines_1_50" {
		t.Errorf("citation 0 location: got %q, want 'lines_1_50'", c.Location)
	}
	if c.CharStart != 0 {
		t.Errorf("citation 0 char_start: got %d, want 0", c.CharStart)
	}

	c = result.Citations[1]
	if c.Location != "lines_51_100" {
		t.Errorf("citation 1 location: got %q, want 'lines_51_100'", c.Location)
	}

	c = result.Citations[2]
	if c.Location != "lines_101_120" {
		t.Errorf("citation 2 location: got %q, want 'lines_101_120'", c.Location)
	}
	if c.CharEnd != len(text) {
		t.Errorf("last citation char_end: got %d, want %d", c.CharEnd, len(text))
	}

	for i := 1; i < len(result.Citations); i++ {
		prev := result.Citations[i-1]
		curr := result.Citations[i]
		if prev.CharEnd+1 != curr.CharStart {
			t.Errorf("gap between citation %d (end %d) and %d (start %d)",
				i-1, prev.CharEnd, i, curr.CharStart)
		}
	}
}

func TestConvertTextEmpty(t *testing.T) {
	result, err := ConvertText([]byte(""))
	if err != nil {
		t.Fatalf("ConvertText failed: %v", err)
	}
	if result.Text != "" {
		t.Error("expected empty text")
	}
	if len(result.Citations) != 0 {
		t.Errorf("expected 0 citations, got %d", len(result.Citations))
	}
}

func TestConvertTextSingleLine(t *testing.T) {
	result, err := ConvertText([]byte("hello world"))
	if err != nil {
		t.Fatalf("ConvertText failed: %v", err)
	}
	if len(result.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(result.Citations))
	}
	c := result.Citations[0]
	if c.Location != "lines_1_1" {
		t.Errorf("location: got %q, want 'lines_1_1'", c.Location)
	}
	if c.CharStart != 0 || c.CharEnd != 11 {
		t.Errorf("positions: got %d-%d, want 0-11", c.CharStart, c.CharEnd)
	}
}

func TestConvertDOCX(t *testing.T) {
	docx := buildMinimalDOCX(t, []string{
		"First paragraph text.",
		"",
		"Third paragraph text.",
		"Fourth paragraph text.",
	})

	result, err := ConvertDOCX(docx)
	if err != nil {
		t.Fatalf("ConvertDOCX failed: %v", err)
	}
	if result.CitationType != "paragraph" {
		t.Errorf("expected citation_type 'paragraph', got %q", result.CitationType)
	}

	if len(result.Citations) != 3 {
		t.Fatalf("expected 3 citations, got %d", len(result.Citations))
	}

	if result.Citations[0].Location != "physical_paragraph_1" {
		t.Errorf("citation 0: got %q, want 'physical_paragraph_1'", result.Citations[0].Location)
	}

	if result.Citations[1].Location != "physical_paragraph_3" {
		t.Errorf("citation 1: got %q, want 'physical_paragraph_3'", result.Citations[1].Location)
	}

	expected := "First paragraph text.\nThird paragraph text.\nFourth paragraph text."
	if result.Text != expected {
		t.Errorf("text mismatch:\n  got:  %q\n  want: %q", result.Text, expected)
	}

	if result.Citations[2].CharEnd != len(result.Text) {
		t.Errorf("last CharEnd: got %d, want %d", result.Citations[2].CharEnd, len(result.Text))
	}
}

func TestConvertPPTX(t *testing.T) {
	pptx := buildMinimalPPTX(t, [][]string{
		{"Title Slide", "Subtitle here"},
		{"Content Slide", "Bullet point one"},
	})

	result, err := ConvertPPTX(pptx)
	if err != nil {
		t.Fatalf("ConvertPPTX failed: %v", err)
	}
	if result.CitationType != "slide" {
		t.Errorf("expected citation_type 'slide', got %q", result.CitationType)
	}
	if len(result.Citations) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(result.Citations))
	}

	if result.Citations[0].Location != "physical_slide_1" {
		t.Errorf("citation 0: got %q", result.Citations[0].Location)
	}
	if result.Citations[1].Location != "physical_slide_2" {
		t.Errorf("citation 1: got %q", result.Citations[1].Location)
	}

	if !strings.Contains(result.Text, "\n\n") {
		t.Error("expected double newline between slides")
	}

	if result.Citations[1].CharEnd != len(result.Text) {
		t.Errorf("last CharEnd: got %d, want %d", result.Citations[1].CharEnd, len(result.Text))
	}
}

func TestConvertDispatch(t *testing.T) {
	result, err := Convert("readme.txt", []byte("hello"), "text/plain")
	if err != nil {
		t.Fatalf("Convert text failed: %v", err)
	}
	if result.CitationType != "line_range" {
		t.Errorf("expected line_range, got %q", result.CitationType)
	}

	result, err = Convert("data.json", []byte(`{"key":"value"}`), "application/json")
	if err != nil {
		t.Fatalf("Convert json failed: %v", err)
	}
	if result.CitationType != "line_range" {
		t.Errorf("expected line_range for json, got %q", result.CitationType)
	}
}

func TestShouldConvert(t *testing.T) {
	tests := []struct {
		filename string
		mime     string
		want     bool
	}{
		{"report.pdf", "", true},
		{"doc.docx", "", true},
		{"pres.pptx", "", true},
		{"readme.txt", "", false},
		{"code.go", "", false},
		{"", "application/pdf", true},
		{"", "text/plain", false},
	}
	for _, tc := range tests {
		got := ShouldConvert(tc.filename, tc.mime)
		if got != tc.want {
			t.Errorf("ShouldConvert(%q, %q) = %v, want %v", tc.filename, tc.mime, got, tc.want)
		}
	}
}

func TestConvertedFilename(t *testing.T) {
	if got := ConvertedFilename("report.pdf"); got != "report.pdf.converted.txt" {
		t.Errorf("got %q", got)
	}
}

func TestIsTextFile(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"text/plain", true},
		{"text/markdown; charset=utf-8", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/yaml", true},
		{"application/vnd.api+json", true},
		{"image/svg+xml", true},
		{"text/csv", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"application/zip", false},
		{"audio/mpeg", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsTextFile(tc.mime); got != tc.want {
			t.Errorf("IsTextFile(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

func buildMinimalDOCX(t *testing.T, paragraphs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	var xml strings.Builder
	xml.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	xml.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	xml.WriteString(`<w:body>`)
	for _, p := range paragraphs {
		xml.WriteString(`<w:p><w:r><w:t>`)
		xml.WriteString(escapeXML(p))
		xml.WriteString(`</w:t></w:r></w:p>`)
	}
	xml.WriteString(`</w:body></w:document>`)

	f, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write([]byte(xml.String())); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func buildMinimalPPTX(t *testing.T, slides [][]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	for i, shapes := range slides {
		var xml strings.Builder
		xml.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		xml.WriteString(`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">`)
		xml.WriteString(`<p:cSld><p:spTree>`)
		for _, text := range shapes {
			xml.WriteString(`<p:sp><p:txBody><a:p><a:r><a:t>`)
			xml.WriteString(escapeXML(text))
			xml.WriteString(`</a:t></a:r></a:p></p:txBody></p:sp>`)
		}
		xml.WriteString(`</p:spTree></p:cSld></p:sld>`)

		name := "ppt/slides/slide" + itoa(i+1) + ".xml"
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := f.Write([]byte(xml.String())); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestConvertDOCXMultipleRuns(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	xmlContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body>` +
		`<w:p>` +
		`<w:r><w:t>Hello </w:t></w:r>` +
		`<w:r><w:rPr><w:b/></w:rPr><w:t>bold</w:t></w:r>` +
		`<w:r><w:t> world</w:t></w:r>` +
		`</w:p>` +
		`</w:body></w:document>`

	f, _ := w.Create("word/document.xml")
	_, _ = f.Write([]byte(xmlContent))
	_ = w.Close()

	result, err := ConvertDOCX(buf.Bytes())
	if err != nil {
		t.Fatalf("ConvertDOCX failed: %v", err)
	}
	if result.Text != "Hello bold world" {
		t.Errorf("expected 'Hello bold world', got %q", result.Text)
	}
	if len(result.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(result.Citations))
	}
	if result.Citations[0].Location != "physical_paragraph_1" {
		t.Errorf("expected physical_paragraph_1, got %q", result.Citations[0].Location)
	}
}

func TestConvertDOCXAllEmpty(t *testing.T) {
	docx := buildMinimalDOCX(t, []string{"", "", ""})
	result, err := ConvertDOCX(docx)
	if err != nil {
		t.Fatalf("ConvertDOCX failed: %v", err)
	}
	if result.Text != "" {
		t.Errorf("expected empty text, got %q", result.Text)
	}
	if len(result.Citations) != 0 {
		t.Errorf("expected 0 citations, got %d", len(result.Citations))
	}
}

func TestConvertDOCXMissingDocumentXML(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("word/other.xml")
	_, _ = f.Write([]byte("<data/>"))
	_ = w.Close()

	_, err := ConvertDOCX(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for missing document.xml")
	}
	if !strings.Contains(err.Error(), "document.xml not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConvertDOCXUnicode(t *testing.T) {
	docx := buildMinimalDOCX(t, []string{
		"日本語テスト",
		"Ñoño über café",
	})
	result, err := ConvertDOCX(docx)
	if err != nil {
		t.Fatalf("ConvertDOCX failed: %v", err)
	}
	expected := "日本語テスト\nÑoño über café"
	if result.Text != expected {
		t.Errorf("text mismatch:\n  got:  %q\n  want: %q", result.Text, expected)
	}
	if len(result.Citations) != 2 {
		t.Errorf("expected 2 citations, got %d", len(result.Citations))
	}
	if result.Citations[1].CharEnd != len(expected) {
		t.Errorf("last CharEnd: got %d, want %d", result.Citations[1].CharEnd, len(expected))
	}
}

func TestConvertPPTXEmptySlide(t *testing.T) {
	pptx := buildMinimalPPTX(t, [][]string{
		{"Content on slide 1"},
		{},
		{"Content on slide 3"},
	})
	result, err := ConvertPPTX(pptx)
	if err != nil {
		t.Fatalf("ConvertPPTX failed: %v", err)
	}
	if len(result.Citations) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(result.Citations))
	}
	if result.Citations[0].Location != "physical_slide_1" {
		t.Errorf("citation 0: got %q", result.Citations[0].Location)
	}
	if result.Citations[1].Location != "physical_slide_3" {
		t.Errorf("citation 1: got %q, want physical_slide_3", result.Citations[1].Location)
	}
}

func TestConvertPPTXSingleSlide(t *testing.T) {
	pptx := buildMinimalPPTX(t, [][]string{
		{"Only slide", "with two shapes"},
	})
	result, err := ConvertPPTX(pptx)
	if err != nil {
		t.Fatalf("ConvertPPTX failed: %v", err)
	}
	if len(result.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(result.Citations))
	}
	if !strings.Contains(result.Text, "Only slide") {
		t.Error("expected text to contain 'Only slide'")
	}
	if !strings.Contains(result.Text, "with two shapes") {
		t.Error("expected text to contain 'with two shapes'")
	}
	if !strings.Contains(result.Text, "\n") {
		t.Error("expected newline between shapes")
	}
}

func TestConvertPPTXSlideOrdering(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	writeSlide := func(num int, text string) {
		xmlContent := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
			`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` +
			escapeXML(text) +
			`</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
		f, _ := w.Create(fmt.Sprintf("ppt/slides/slide%d.xml", num))
		_, _ = f.Write([]byte(xmlContent))
	}

	writeSlide(3, "Third slide")
	writeSlide(1, "First slide")
	writeSlide(2, "Second slide")
	_ = w.Close()

	result, err := ConvertPPTX(buf.Bytes())
	if err != nil {
		t.Fatalf("ConvertPPTX failed: %v", err)
	}

	if len(result.Citations) != 3 {
		t.Fatalf("expected 3 citations, got %d", len(result.Citations))
	}
	if result.Citations[0].Location != "physical_slide_1" {
		t.Errorf("first citation: got %q", result.Citations[0].Location)
	}
	if result.Citations[2].Location != "physical_slide_3" {
		t.Errorf("third citation: got %q", result.Citations[2].Location)
	}
	if !strings.HasPrefix(result.Text, "First slide") {
		t.Errorf("expected text to start with 'First slide', got %q", result.Text[:20])
	}
}

func TestConvertTextUnicode(t *testing.T) {
	text := "日本語テスト line\nÑoño café"
	result, err := ConvertText([]byte(text))
	if err != nil {
		t.Fatalf("ConvertText failed: %v", err)
	}
	if result.Text != text {
		t.Error("text should be unchanged")
	}
	if len(result.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(result.Citations))
	}
	if result.Citations[0].CharEnd != len(text) {
		t.Errorf("CharEnd: got %d, want %d", result.Citations[0].CharEnd, len(text))
	}
}

func TestConvertTextExactBoundary(t *testing.T) {
	lines := make([]string, LinesPerCitationChunk)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	text := strings.Join(lines, "\n")

	result, err := ConvertText([]byte(text))
	if err != nil {
		t.Fatalf("ConvertText failed: %v", err)
	}
	if len(result.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(result.Citations))
	}
	if result.Citations[0].Location != "lines_1_50" {
		t.Errorf("location: got %q, want 'lines_1_50'", result.Citations[0].Location)
	}
}

func TestConvertTextExactBoundaryPlusOne(t *testing.T) {
	lines := make([]string, LinesPerCitationChunk+1)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	text := strings.Join(lines, "\n")

	result, err := ConvertText([]byte(text))
	if err != nil {
		t.Fatalf("ConvertText failed: %v", err)
	}
	if len(result.Citations) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(result.Citations))
	}
	if result.Citations[0].Location != "lines_1_50" {
		t.Errorf("citation 0: got %q", result.Citations[0].Location)
	}
	if result.Citations[1].Location != "lines_51_51" {
		t.Errorf("citation 1: got %q, want 'lines_51_51'", result.Citations[1].Location)
	}
}

func TestConvertDispatchDOCX(t *testing.T) {
	docx := buildMinimalDOCX(t, []string{"Hello from DOCX."})
	result, err := Convert("report.docx", docx, "")
	if err != nil {
		t.Fatalf("Convert DOCX dispatch failed: %v", err)
	}
	if result.CitationType != "paragraph" {
		t.Errorf("expected 'paragraph', got %q", result.CitationType)
	}
	if result.Text != "Hello from DOCX." {
		t.Errorf("text mismatch: got %q", result.Text)
	}
}

func TestConvertDispatchPPTX(t *testing.T) {
	pptx := buildMinimalPPTX(t, [][]string{{"Slide content."}})
	result, err := Convert("deck.pptx", pptx, "")
	if err != nil {
		t.Fatalf("Convert PPTX dispatch failed: %v", err)
	}
	if result.CitationType != "slide" {
		t.Errorf("expected 'slide', got %q", result.CitationType)
	}
}

func TestConvertDispatchByMIME(t *testing.T) {
	docx := buildMinimalDOCX(t, []string{"MIME dispatch test."})
	result, err := Convert("no-ext", docx, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err != nil {
		t.Fatalf("Convert MIME dispatch failed: %v", err)
	}
	if result.CitationType != "paragraph" {
		t.Errorf("expected 'paragraph', got %q", result.CitationType)
	}
}

func TestShouldConvertComprehensive(t *testing.T) {
	tests := []struct {
		filename string
		mime     string
		want     bool
	}{
		{"report.pdf", "", true},
		{"doc.docx", "", true},
		{"pres.pptx", "", true},
		{"REPORT.PDF", "", true},
		{"DOC.DOCX", "", true},
		{"readme.txt", "", false},
		{"code.go", "", false},
		{"image.png", "", false},
		{"data.csv", "", false},

		{"", "application/pdf", true},
		{"", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", true},
		{"", "application/vnd.openxmlformats-officedocument.presentationml.presentation", true},
		{"", "text/plain", false},
		{"", "application/json", false},
		{"", "image/png", false},
	}
	for _, tc := range tests {
		got := ShouldConvert(tc.filename, tc.mime)
		if got != tc.want {
			t.Errorf("ShouldConvert(%q, %q) = %v, want %v", tc.filename, tc.mime, got, tc.want)
		}
	}
}

func TestConvertedFilenameVariants(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"report.pdf", "report.pdf.converted.txt"},
		{"presentation.pptx", "presentation.pptx.converted.txt"},
		{"document.docx", "document.docx.converted.txt"},
		{"file with spaces.pdf", "file with spaces.pdf.converted.txt"},
	}
	for _, tc := range tests {
		got := ConvertedFilename(tc.input)
		if got != tc.want {
			t.Errorf("ConvertedFilename(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestConvertDOCXCitationCoverage(t *testing.T) {
	docx := buildMinimalDOCX(t, []string{
		"Paragraph one.",
		"Paragraph two.",
		"Paragraph three.",
	})
	result, err := ConvertDOCX(docx)
	if err != nil {
		t.Fatalf("ConvertDOCX failed: %v", err)
	}

	expectedTexts := []string{"Paragraph one.", "Paragraph two.", "Paragraph three."}
	for i, c := range result.Citations {
		substr := result.Text[c.CharStart:c.CharEnd]
		if substr != expectedTexts[i] {
			t.Errorf("citation %d: got %q, want %q", i, substr, expectedTexts[i])
		}
	}

	for i := 1; i < len(result.Citations); i++ {
		prev := result.Citations[i-1]
		curr := result.Citations[i]
		if prev.CharEnd+1 != curr.CharStart {
			t.Errorf("gap between citation %d (end %d) and %d (start %d)",
				i-1, prev.CharEnd, i, curr.CharStart)
		}
	}
}

func TestConvertPPTXCitationCoverage(t *testing.T) {
	pptx := buildMinimalPPTX(t, [][]string{
		{"Slide one text"},
		{"Slide two text"},
	})
	result, err := ConvertPPTX(pptx)
	if err != nil {
		t.Fatalf("ConvertPPTX failed: %v", err)
	}

	for _, c := range result.Citations {
		substr := result.Text[c.CharStart:c.CharEnd]
		if substr == "" {
			t.Errorf("empty substring for citation at %d-%d", c.CharStart, c.CharEnd)
		}
	}

	if len(result.Citations) == 2 {
		gap := result.Citations[1].CharStart - result.Citations[0].CharEnd
		if gap != 2 {
			t.Errorf("gap between slides: got %d, want 2", gap)
		}
	}
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
