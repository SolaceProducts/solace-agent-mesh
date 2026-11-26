// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func buildDOCX(t *testing.T, bodyXML string) []byte {
	t.Helper()

	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>` + bodyXML + `</w:body>
</w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	addFile := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%s): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip.Write(%s): %v", name, err)
		}
	}

	addFile("[Content_Types].xml",
		`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`)
	addFile("_rels/.rels",
		`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`)
	addFile("word/document.xml", documentXML)

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestConvertDOCX_PlainParagraphs(t *testing.T) {
	body := `
		<w:p><w:r><w:t>Hello, world.</w:t></w:r></w:p>
		<w:p><w:r><w:t>Second paragraph.</w:t></w:r></w:p>
	`
	got, err := convertDOCXToMarkdown(buildDOCX(t, body))
	if err != nil {
		t.Fatalf("convertDOCX error: %v", err)
	}
	if !strings.Contains(got, "Hello, world.") {
		t.Errorf("missing first paragraph; got:\n%s", got)
	}
	if !strings.Contains(got, "Second paragraph.") {
		t.Errorf("missing second paragraph; got:\n%s", got)
	}
	if !strings.Contains(got, "Hello, world.\n\nSecond paragraph.") {
		t.Errorf("paragraphs not separated by blank line; got:\n%s", got)
	}
}

func TestConvertDOCX_Headings(t *testing.T) {
	body := `
		<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Title</w:t></w:r></w:p>
		<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Section</w:t></w:r></w:p>
		<w:p><w:pPr><w:pStyle w:val="Heading3"/></w:pPr><w:r><w:t>Subsection</w:t></w:r></w:p>
	`
	got, err := convertDOCXToMarkdown(buildDOCX(t, body))
	if err != nil {
		t.Fatalf("convertDOCX error: %v", err)
	}
	checks := []string{"# Title", "## Section", "### Subsection"}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestConvertDOCX_BoldItalicRuns(t *testing.T) {
	body := `
		<w:p>
			<w:r><w:rPr><w:b/></w:rPr><w:t>bold</w:t></w:r>
			<w:r><w:t> and </w:t></w:r>
			<w:r><w:rPr><w:i/></w:rPr><w:t>italic</w:t></w:r>
			<w:r><w:t> and </w:t></w:r>
			<w:r><w:rPr><w:b/><w:i/></w:rPr><w:t>both</w:t></w:r>
		</w:p>
	`
	got, err := convertDOCXToMarkdown(buildDOCX(t, body))
	if err != nil {
		t.Fatalf("convertDOCX error: %v", err)
	}
	if !strings.Contains(got, "**bold**") {
		t.Errorf("missing **bold**; got %q", got)
	}
	if !strings.Contains(got, "*italic*") {
		t.Errorf("missing *italic*; got %q", got)
	}
	if !strings.Contains(got, "***both***") {
		t.Errorf("missing ***both***; got %q", got)
	}
}

func TestConvertDOCX_ListItems(t *testing.T) {
	body := `
		<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>First item</w:t></w:r></w:p>
		<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>Second item</w:t></w:r></w:p>
	`
	got, err := convertDOCXToMarkdown(buildDOCX(t, body))
	if err != nil {
		t.Fatalf("convertDOCX error: %v", err)
	}
	if !strings.Contains(got, "- First item") || !strings.Contains(got, "- Second item") {
		t.Errorf("list items missing bullet prefix; got:\n%s", got)
	}
}

func TestConvertDOCX_Tables(t *testing.T) {
	body := `
		<w:tbl>
			<w:tr>
				<w:tc><w:p><w:r><w:t>Name</w:t></w:r></w:p></w:tc>
				<w:tc><w:p><w:r><w:t>Age</w:t></w:r></w:p></w:tc>
			</w:tr>
			<w:tr>
				<w:tc><w:p><w:r><w:t>Alice</w:t></w:r></w:p></w:tc>
				<w:tc><w:p><w:r><w:t>30</w:t></w:r></w:p></w:tc>
			</w:tr>
			<w:tr>
				<w:tc><w:p><w:r><w:t>Bob</w:t></w:r></w:p></w:tc>
				<w:tc><w:p><w:r><w:t>25</w:t></w:r></w:p></w:tc>
			</w:tr>
		</w:tbl>
	`
	got, err := convertDOCXToMarkdown(buildDOCX(t, body))
	if err != nil {
		t.Fatalf("convertDOCX error: %v", err)
	}
	for _, want := range []string{
		"| Name | Age |",
		"| --- | --- |",
		"| Alice | 30 |",
		"| Bob | 25 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing row %q; got:\n%s", want, got)
		}
	}
}

func TestConvertDOCX_MissingDocumentXML(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("other.xml")
	_, _ = w.Write([]byte("<x/>"))
	_ = zw.Close()

	_, err := convertDOCXToMarkdown(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for docx without document.xml")
	}
	if !strings.Contains(err.Error(), "word/document.xml") {
		t.Errorf("error should mention word/document.xml, got %v", err)
	}
}

func TestConvertDOCX_ParseHeadingLevel(t *testing.T) {
	cases := map[string]int{
		"Heading1": 1,
		"Heading2": 2,
		"Heading9": 9,
		"heading3": 3,
		"":         0,
		"Normal":   0,
		"Title":    0,
		"Heading0": 0,
	}
	for in, want := range cases {
		if got := parseHeadingLevel(in); got != want {
			t.Errorf("parseHeadingLevel(%q) = %d, want %d", in, got, want)
		}
	}
}

func buildXLSX(t *testing.T, sheets map[string][][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	_ = f.DeleteSheet("Sheet1")

	names := make([]string, 0, len(sheets))
	for name := range sheets {
		names = append(names, name)
	}
	for _, name := range names {
		_, err := f.NewSheet(name)
		if err != nil {
			t.Fatalf("new sheet %q: %v", name, err)
		}
		for rowIdx, row := range sheets[name] {
			for colIdx, cell := range row {
				cellRef, cerr := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
				if cerr != nil {
					t.Fatalf("coords: %v", cerr)
				}
				if err := f.SetCellValue(name, cellRef, cell); err != nil {
					t.Fatalf("set cell %s: %v", cellRef, err)
				}
			}
		}
	}
	if len(names) > 0 {
		f.SetActiveSheet(0)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func TestConvertXLSX_SingleSheetTable(t *testing.T) {
	data := buildXLSX(t, map[string][][]string{
		"Employees": {
			{"Name", "Role"},
			{"Alice", "Eng"},
			{"Bob", "PM"},
		},
	})
	got, err := convertXLSXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertXLSX error: %v", err)
	}
	for _, want := range []string{
		"## Employees",
		"| Name | Role |",
		"| --- | --- |",
		"| Alice | Eng |",
		"| Bob | PM |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
}

func TestConvertXLSX_EmptySheet(t *testing.T) {
	data := buildXLSX(t, map[string][][]string{"Blank": nil})
	got, err := convertXLSXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertXLSX error: %v", err)
	}
	if !strings.Contains(got, "## Blank") || !strings.Contains(got, "_(empty sheet)_") {
		t.Errorf("empty sheet handling wrong; got:\n%s", got)
	}
}

func TestConvertXLSX_RejectsNonXLSX(t *testing.T) {
	_, err := convertXLSXToMarkdown([]byte("not a real xlsx"))
	if err == nil {
		t.Fatal("expected error for invalid xlsx")
	}
}

func buildPPTX(t *testing.T, slides map[int]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	wrap := func(body string) string {
		return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld><p:spTree>` + body + `</p:spTree></p:cSld>
</p:sld>`
	}

	for num, body := range slides {
		name := "ppt/slides/slide" + strconv.Itoa(num) + ".xml"
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create: %v", err)
		}
		if _, err := w.Write([]byte(wrap(body))); err != nil {
			t.Fatalf("zip.Write: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestConvertPPTX_SlidesInOrder(t *testing.T) {
	data := buildPPTX(t, map[int]string{
		2: pptxPara("Second paragraph on slide 2"),
		1: pptxPara("First slide title"),
		3: pptxPara("Slide three point A") + pptxPara("Slide three point B"),
	})
	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}

	idx1 := strings.Index(got, "## Slide 1")
	idx2 := strings.Index(got, "## Slide 2")
	idx3 := strings.Index(got, "## Slide 3")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Fatalf("slide headings missing; got:\n%s", got)
	}
	if idx1 >= idx2 || idx2 >= idx3 {
		t.Errorf("slides not in numeric order; got:\n%s", got)
	}
	if !strings.Contains(got, "- First slide title") {
		t.Errorf("slide 1 bullet missing; got:\n%s", got)
	}
	if !strings.Contains(got, "- Slide three point A") || !strings.Contains(got, "- Slide three point B") {
		t.Errorf("slide 3 bullets missing; got:\n%s", got)
	}
}

func TestConvertPPTX_EmptySlide(t *testing.T) {
	data := buildPPTX(t, map[int]string{1: ""})
	got, err := convertPPTXToMarkdown(data)
	if err != nil {
		t.Fatalf("convertPPTX error: %v", err)
	}
	if !strings.Contains(got, "## Slide 1") {
		t.Errorf("expected slide heading; got %q", got)
	}
	if !strings.Contains(got, "_(no text)_") {
		t.Errorf("expected empty slide marker; got %q", got)
	}
}

func TestConvertPPTX_NoSlides(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("ppt/presentation.xml")
	_, _ = w.Write([]byte("<x/>"))
	_ = zw.Close()

	_, err := convertPPTXToMarkdown(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for pptx without slides")
	}
}

func pptxPara(text string) string {
	return `<a:p><a:r><a:t>` + text + `</a:t></a:r></a:p>`
}

func buildEPUB(t *testing.T, chapters map[string]string, spineOrder []string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%s): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip.Write(%s): %v", name, err)
		}
	}

	add("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)

	var manifestXML, spineXML strings.Builder
	for id, body := range chapters {
		manifestXML.WriteString(`<item id="` + id + `" href="` + id + `.xhtml" media-type="application/xhtml+xml"/>`)
		add("OEBPS/"+id+".xhtml", body)
	}
	for _, id := range spineOrder {
		spineXML.WriteString(`<itemref idref="` + id + `"/>`)
	}

	add("OEBPS/content.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <manifest>`+manifestXML.String()+`</manifest>
  <spine>`+spineXML.String()+`</spine>
</package>`)

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestConvertEPUB_SpineOrder(t *testing.T) {
	data := buildEPUB(t,
		map[string]string{
			"ch1": "<html><body><h1>Chapter One</h1><p>Opening lines.</p></body></html>",
			"ch2": "<html><body><h1>Chapter Two</h1><p>More content.</p></body></html>",
		},
		[]string{"ch1", "ch2"},
	)
	got, err := convertEPUBToMarkdown(data)
	if err != nil {
		t.Fatalf("convertEPUB error: %v", err)
	}
	idx1 := strings.Index(got, "# Chapter One")
	idx2 := strings.Index(got, "# Chapter Two")
	if idx1 < 0 || idx2 < 0 {
		t.Fatalf("chapter headings missing; got:\n%s", got)
	}
	if idx1 > idx2 {
		t.Errorf("spine order not preserved; got:\n%s", got)
	}
	if !strings.Contains(got, "Opening lines.") {
		t.Errorf("chapter 1 content missing; got:\n%s", got)
	}
}

func TestConvertEPUB_MissingContainer(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("not-an-epub.txt")
	_, _ = w.Write([]byte("hi"))
	_ = zw.Close()
	_, err := convertEPUBToMarkdown(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for epub without container.xml")
	}
}

func buildIPYNB(t *testing.T, cells []map[string]any, language string) []byte {
	t.Helper()
	nb := map[string]any{
		"nbformat":       4,
		"nbformat_minor": 5,
		"cells":          cells,
	}
	if language != "" {
		nb["metadata"] = map[string]any{
			"kernelspec": map[string]any{"language": language},
		}
	}
	b, err := json.Marshal(nb)
	if err != nil {
		t.Fatalf("marshal ipynb: %v", err)
	}
	return b
}

func TestConvertIPYNB_MarkdownAndCode(t *testing.T) {
	data := buildIPYNB(t, []map[string]any{
		{"cell_type": "markdown", "source": []string{"# Notebook Title\n", "Some prose.\n"}},
		{"cell_type": "code", "source": "print('hello')\n"},
		{"cell_type": "raw", "source": "this is raw\n"},
	}, "python")
	got, err := convertIPYNBToMarkdown(data)
	if err != nil {
		t.Fatalf("convertIPYNB error: %v", err)
	}
	if !strings.Contains(got, "# Notebook Title") {
		t.Errorf("missing markdown heading; got:\n%s", got)
	}
	if !strings.Contains(got, "Some prose.") {
		t.Errorf("missing prose; got:\n%s", got)
	}
	if !strings.Contains(got, "```python\nprint('hello')\n```") {
		t.Errorf("missing code fence with language; got:\n%s", got)
	}
	if strings.Contains(got, "this is raw") {
		t.Errorf("raw cell should be skipped; got:\n%s", got)
	}
}

func TestConvertIPYNB_DefaultsToPython(t *testing.T) {
	data := buildIPYNB(t, []map[string]any{
		{"cell_type": "code", "source": "x = 1"},
	}, "")
	got, err := convertIPYNBToMarkdown(data)
	if err != nil {
		t.Fatalf("convertIPYNB error: %v", err)
	}
	if !strings.Contains(got, "```python") {
		t.Errorf("default language should be python; got:\n%s", got)
	}
}

func TestConvertIPYNB_HonoursKernelLanguage(t *testing.T) {
	data := buildIPYNB(t, []map[string]any{
		{"cell_type": "code", "source": "println(\"hi\")"},
	}, "julia")
	got, err := convertIPYNBToMarkdown(data)
	if err != nil {
		t.Fatalf("convertIPYNB error: %v", err)
	}
	if !strings.Contains(got, "```julia") {
		t.Errorf("kernel language not used; got:\n%s", got)
	}
}

func TestConvertIPYNB_Malformed(t *testing.T) {
	_, err := convertIPYNBToMarkdown([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for malformed ipynb")
	}
}

func TestExecuteConvert_DOCXViaDispatch(t *testing.T) {
	docx := buildDOCX(t, `<w:p><w:r><w:t>Hello from DOCX.</w:t></w:r></w:p>`)
	params := ConvertParams{
		InputFilename: sdk.Artifact{
			Filename: "greeting.docx",
			Content:  docx,
		},
	}
	result, err := executeConvert(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("executeConvert error: %v", err)
	}
	if result.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if len(result.DataObjects) == 0 {
		t.Fatal("no DataObjects")
	}
	if !strings.Contains(string(result.DataObjects[0].Content), "Hello from DOCX.") {
		t.Errorf("docx text not round-tripped; got:\n%s", result.DataObjects[0].Content)
	}
	if result.DataObjects[0].Name != "greeting_converted.md" {
		t.Errorf("output filename = %q, want greeting_converted.md", result.DataObjects[0].Name)
	}
}

func TestExecuteConvert_UnsupportedFormat(t *testing.T) {
	params := ConvertParams{
		InputFilename: sdk.Artifact{
			Filename: "random.zzz",
			Content:  []byte{0xff, 0xff},
			MIMEType: "application/x-unknown",
		},
	}
	result, err := executeConvert(context.Background(), params, nil)
	if err != nil {
		t.Fatalf("executeConvert error: %v", err)
	}
	if result.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "Unsupported file format for MarkItDown") {
		t.Errorf("error message should start with Python MarkItDown prefix; got %q", result.Message)
	}
}
