// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxgen

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func zipFile(t *testing.T, docx []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("open docx as zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %q: %v", name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %q: %v", name, err)
		}
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			assertWellFormedXML(t, name, data)
		}
		return data
	}
	t.Fatalf("docx has no entry %q", name)
	return nil
}

func assertWellFormedXML(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := wellFormedXML(data); err != nil {
		t.Fatalf("part %q is not well-formed XML: %v\n%s", name, err, data)
	}
}

func wellFormedXML(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return fmt.Errorf("second root element %q: an XML document has exactly one", t.Name.Local)
				}
			}
			depth++
			if err := checkResolvedNamespaces(t); err != nil {
				return err
			}
		case xml.EndElement:
			depth--
		}
	}
}

func checkResolvedNamespaces(e xml.StartElement) error {
	if e.Name.Space != "" && !strings.Contains(e.Name.Space, "://") {
		return fmt.Errorf("element %q uses undeclared namespace prefix %q", e.Name.Local, e.Name.Space)
	}
	for _, a := range e.Attr {
		if a.Name.Space == "" || a.Name.Space == "xmlns" || strings.Contains(a.Name.Space, "://") {
			continue
		}
		return fmt.Errorf("attribute %q on %q uses undeclared namespace prefix %q", a.Name.Local, e.Name.Local, a.Name.Space)
	}
	return nil
}

func TestWellFormedXML_CatchesWhatUnmarshalMissed(t *testing.T) {
	bad := map[string][]byte{
		"trailing garbage after the root": []byte(`<a/>THIS IS NOT XML <<<>>>`),
		"two root elements":               []byte(`<a/><b/>`),
		"undeclared namespace prefix":     []byte(`<w:a xmlns:w="http://example.com/w"><q:b/></w:a>`),
		"unclosed element":                []byte(`<a><b></a>`),
	}
	for name, data := range bad {
		t.Run(name, func(t *testing.T) {
			if xml.Unmarshal(data, new(any)) == nil && wellFormedXML(data) == nil {
				t.Errorf("neither the old nor the new check rejected %s: %s", name, data)
			}
			if err := wellFormedXML(data); err == nil {
				t.Errorf("wellFormedXML accepted malformed input (%s): %s", name, data)
			}
		})
	}

	valid := []byte(`<w:a xmlns:w="http://example.com/w" w:id="1"><w:b/></w:a>`)
	if err := wellFormedXML(valid); err != nil {
		t.Errorf("wellFormedXML rejected valid XML: %v", err)
	}
}

func zipNames(t *testing.T, docx []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatalf("open docx as zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func TestToBytes_MinimalDocument_IsWellFormedPackage(t *testing.T) {
	d := New()
	d.SetMetadata("Test Title", "Test Author", "Test Subject")
	if err := d.AddHeading(1, "Introduction"); err != nil {
		t.Fatalf("AddHeading: %v", err)
	}
	if err := d.AddParagraph([]Run{{Text: "Hello, "}, {Text: "world", Bold: true}, {Text: "!"}}, ""); err != nil {
		t.Fatalf("AddParagraph: %v", err)
	}

	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	for _, name := range []string{
		"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "docProps/app.xml",
		"word/document.xml", "word/styles.xml", "word/numbering.xml", "word/_rels/document.xml.rels",
	} {
		zipFile(t, data, name)
	}

	doc := string(zipFile(t, data, "word/document.xml"))
	if !strings.Contains(doc, `<w:pStyle w:val="Heading1"></w:pStyle>`) {
		t.Errorf("document.xml missing Heading1 style reference:\n%s", doc)
	}
	if !strings.Contains(doc, "Introduction") {
		t.Errorf("document.xml missing heading text:\n%s", doc)
	}
	if !strings.Contains(doc, "Hello, ") || !strings.Contains(doc, "world") {
		t.Errorf("document.xml missing paragraph run text:\n%s", doc)
	}
	if !strings.Contains(doc, "<w:sectPr>") {
		t.Errorf("document.xml missing sectPr:\n%s", doc)
	}

	core := string(zipFile(t, data, "docProps/core.xml"))
	if !strings.Contains(core, "Test Title") || !strings.Contains(core, "Test Author") || !strings.Contains(core, "Test Subject") {
		t.Errorf("core.xml missing metadata:\n%s", core)
	}
}

func TestAddParagraph_EscapesSpecialCharacters(t *testing.T) {
	d := New()
	if err := d.AddParagraph([]Run{{Text: `Tom & Jerry <says> "hi" 'there'`}}, ""); err != nil {
		t.Fatalf("AddParagraph: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))
	if strings.Contains(doc, "<says>") {
		t.Errorf("raw unescaped '<says>' leaked into document.xml — XML injection risk:\n%s", doc)
	}
	if !strings.Contains(doc, "Tom &amp; Jerry &lt;says&gt;") {
		t.Errorf("expected escaped text in document.xml:\n%s", doc)
	}
}

func TestAddHeading_InvalidLevel_Errors(t *testing.T) {
	d := New()
	if err := d.AddHeading(0, "x"); err == nil {
		t.Error("expected error for heading level 0")
	}
	if err := d.AddHeading(5, "x"); err == nil {
		t.Error("expected error for heading level 5")
	}
}

func TestAddBulletList_AndNumberedList(t *testing.T) {
	d := New()
	if err := d.AddBulletList([]string{"first", "second"}); err != nil {
		t.Fatalf("AddBulletList: %v", err)
	}
	if err := d.AddNumberedList([]string{"one", "two"}); err != nil {
		t.Fatalf("AddNumberedList: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))
	if strings.Count(doc, `<w:numId w:val="1"></w:numId>`) != 2 {
		t.Errorf("expected 2 bullet-list paragraphs (numId 1):\n%s", doc)
	}
	if strings.Count(doc, `<w:numId w:val="2"></w:numId>`) != 2 {
		t.Errorf("expected 2 numbered-list paragraphs (numId 2):\n%s", doc)
	}
	for _, item := range []string{"first", "second", "one", "two"} {
		if !strings.Contains(doc, item) {
			t.Errorf("document.xml missing list item %q", item)
		}
	}
}

func TestAddNumberedList_EachListRestartsNumbering(t *testing.T) {
	d := New()
	if err := d.AddNumberedList([]string{"one", "two"}); err != nil {
		t.Fatalf("first AddNumberedList: %v", err)
	}
	if err := d.AddBulletList([]string{"bullet"}); err != nil {
		t.Fatalf("AddBulletList: %v", err)
	}
	if err := d.AddNumberedList([]string{"alpha", "beta"}); err != nil {
		t.Fatalf("second AddNumberedList: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	doc := string(zipFile(t, data, "word/document.xml"))
	if strings.Count(doc, `<w:numId w:val="2"></w:numId>`) != 2 {
		t.Errorf("first numbered list should use numId 2 for both items:\n%s", doc)
	}
	if strings.Count(doc, `<w:numId w:val="3"></w:numId>`) != 2 {
		t.Errorf("second numbered list should use its own numId 3:\n%s", doc)
	}

	numbering := string(zipFile(t, data, "word/numbering.xml"))
	for _, id := range []string{"1", "2", "3"} {
		if !strings.Contains(numbering, `<w:num w:numId="`+id+`">`) {
			t.Errorf("numbering.xml missing instance for numId %s:\n%s", id, numbering)
		}
	}
	if strings.Contains(numbering, `<w:num w:numId="4">`) {
		t.Errorf("numbering.xml declared an unused numId 4:\n%s", numbering)
	}

	if got := strings.Count(numbering, `<w:startOverride w:val="1"/>`); got != 2 {
		t.Errorf("expected a startOverride for each of the 2 numbered lists, got %d:\n%s", got, numbering)
	}
}

func TestAddTable_HeaderAndRows_PadsRaggedRows(t *testing.T) {
	d := New()
	err := d.AddTable(
		[]string{"Name", "Value", "Extra"},
		[][]string{
			{"a", "1"},
			{"b", "2", "z"},
		},
		TableOptions{HeaderBold: true},
	)
	if err != nil {
		t.Fatalf("AddTable: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))
	if strings.Count(doc, "<w:tr>") != 3 {
		t.Errorf("expected 3 table rows (1 header + 2 data), got document:\n%s", doc)
	}
	if strings.Count(doc, "<w:tc>") != 9 {
		t.Errorf("expected 9 cells (3 rows x 3 cols):\n%s", doc)
	}
	for _, want := range []string{"Name", "Value", "Extra", "a", "1", "b", "2", "z"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document.xml missing table cell text %q", want)
		}
	}
}

func gridColWidths(t *testing.T, doc string) []int {
	t.Helper()
	var out []int
	for _, m := range regexp.MustCompile(`<w:gridCol w:w="(\d+)"`).FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("gridCol width %q is not a number: %v", m[1], err)
		}
		out = append(out, n)
	}
	return out
}

func TestAddTable_DefaultWidth_SpansTextColumnUnderAutofit(t *testing.T) {
	d := New()
	if err := d.AddTable([]string{"Principle", "Description"},
		[][]string{{"Event-Driven", "All inter-agent communication flows through the broker."}},
		TableOptions{HeaderBold: true}); err != nil {
		t.Fatalf("AddTable: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))

	if !strings.Contains(doc, `<w:tblW w:type="pct" w:w="5000">`) {
		t.Errorf("table does not declare a full-width preferred width:\n%s", doc)
	}
	if !strings.Contains(doc, `<w:tblLayout w:type="autofit">`) {
		t.Errorf("table does not declare autofit layout:\n%s", doc)
	}
	if strings.Contains(doc, `w:w="0"`) {
		t.Errorf("table still emits a zero width, which renders at minimum-content width:\n%s", doc)
	}

	widths := gridColWidths(t, doc)
	want := []int{PrintableWidthTwips / 2, PrintableWidthTwips / 2}
	if !slices.Equal(widths, want) {
		t.Errorf("gridCol widths = %v, want %v", widths, want)
	}
	if got := strings.Count(doc, `<w:tcW w:type="dxa" w:w="4680">`); got != 4 {
		t.Errorf("expected 4 cells at 4680 twips, got %d:\n%s", got, doc)
	}
}

func TestAddTable_ColWidths_AppliesSharesUnderFixedLayout(t *testing.T) {
	d := New()
	if err := d.AddTable([]string{"Principle", "Description"},
		[][]string{{"Event-Driven", "All inter-agent communication flows through the broker."}},
		TableOptions{HeaderBold: true, ColWidths: []float64{1, 3}}); err != nil {
		t.Fatalf("AddTable: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))

	if !strings.Contains(doc, `<w:tblW w:type="dxa" w:w="9360">`) {
		t.Errorf("table does not declare an absolute width:\n%s", doc)
	}
	if !strings.Contains(doc, `<w:tblLayout w:type="fixed">`) {
		t.Errorf("table does not declare fixed layout:\n%s", doc)
	}
	if widths := gridColWidths(t, doc); !slices.Equal(widths, []int{2340, 7020}) {
		t.Errorf("gridCol widths = %v, want [2340 7020] for shares [1 3]", widths)
	}
}

func TestAddTable_ColWidths_AreRelative(t *testing.T) {
	render := func(shares []float64) []int {
		t.Helper()
		d := New()
		if err := d.AddTable([]string{"a", "b"}, [][]string{{"1", "2"}},
			TableOptions{ColWidths: shares}); err != nil {
			t.Fatalf("AddTable(%v): %v", shares, err)
		}
		data, err := d.ToBytes()
		if err != nil {
			t.Fatalf("ToBytes: %v", err)
		}
		return gridColWidths(t, string(zipFile(t, data, "word/document.xml")))
	}
	a, b := render([]float64{1, 3}), render([]float64{25, 75})
	if !slices.Equal(a, b) {
		t.Errorf("[1 3] gave %v but [25 75] gave %v; shares must be relative", a, b)
	}
	if len(a) != 2 || a[0] >= a[1] {
		t.Errorf("shares [1 3] gave %v; want two ascending positive widths", a)
	}
}

func TestAddTable_GridAlwaysSumsToPrintableWidth(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ncols  int
		shares []float64
	}{
		{name: "2 columns even", ncols: 2},
		{name: "3 columns even, does not divide", ncols: 3},
		{name: "7 columns even, does not divide", ncols: 7},
		{name: "63 columns even, Word's maximum", ncols: 63},
		{name: "3 columns, shares do not divide", ncols: 3, shares: []float64{1, 1, 1}},
		{name: "3 columns, lopsided shares", ncols: 3, shares: []float64{0.1, 99, 0.9}},
		{name: "3 columns, share rounds away", ncols: 3, shares: []float64{0.01, 99, 0.9}},
		{name: "2 columns, share four orders down", ncols: 2, shares: []float64{1, 10000}},
		{name: "2 columns, share rounds away even against the floor", ncols: 2, shares: []float64{1, 1e12}},
		{name: "63 columns, one share dominates", ncols: 63, shares: dominantShares(63)},
		{name: "3 columns, one NaN share", ncols: 3, shares: []float64{1, math.NaN(), 1}},
		{name: "3 columns, one negative share", ncols: 3, shares: []float64{1, -5, 1}},
		{name: "3 columns, one infinite share", ncols: 3, shares: []float64{1, math.Inf(1), 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := make([]string, tc.ncols)
			for i := range row {
				row[i] = "x"
			}
			d := New()
			if err := d.AddTable(nil, [][]string{row}, TableOptions{ColWidths: tc.shares}); err != nil {
				t.Fatalf("AddTable: %v", err)
			}
			data, err := d.ToBytes()
			if err != nil {
				t.Fatalf("ToBytes: %v", err)
			}
			widths := gridColWidths(t, string(zipFile(t, data, "word/document.xml")))
			if len(widths) != tc.ncols {
				t.Fatalf("got %d gridCols, want %d", len(widths), tc.ncols)
			}
			sum := 0
			for _, w := range widths {
				if w < minGridColTwips {
					t.Errorf("gridCol widths = %v; every column needs at least %d twips", widths, minGridColTwips)
					break
				}
				sum += w
			}
			if sum != PrintableWidthTwips {
				t.Errorf("gridCol widths sum to %d, want %d (widths %v)", sum, PrintableWidthTwips, widths)
			}
		})
	}
}

func TestResolveGridColTwips_InvariantsAcrossLegalRange(t *testing.T) {
	patterns := []struct {
		name string
		make func(n int) []float64
	}{
		{name: "nil shares (even split)", make: func(int) []float64 { return nil }},
		{name: "all equal", make: func(n int) []float64 { return fillShares(n, 1) }},
		{name: "one dominant", make: dominantShares},
		{name: "one vanishing", make: func(n int) []float64 {
			s := fillShares(n, 1)
			s[n-1] = 1e-12
			return s
		}},
		{name: "geometric decay", make: func(n int) []float64 {
			s := make([]float64, n)
			for i := range s {
				s[i] = math.Pow(0.5, float64(i))
			}
			return s
		}},
		{name: "ascending", make: func(n int) []float64 {
			s := make([]float64, n)
			for i := range s {
				s[i] = float64(i + 1)
			}
			return s
		}},
		{name: "all invalid but one", make: func(n int) []float64 {
			s := fillShares(n, math.NaN())
			s[0] = 1
			return s
		}},
		{name: "every share invalid", make: func(n int) []float64 { return fillShares(n, math.NaN()) }},
	}

	for _, p := range patterns {
		t.Run(p.name, func(t *testing.T) {
			for n := 1; n <= MaxTableColumns; n++ {
				got := resolveGridColTwips(p.make(n), n)
				if len(got) != n {
					t.Fatalf("ncols=%d: got %d widths", n, len(got))
				}
				sum := 0
				for _, w := range got {
					sum += w
					if w < minGridColTwips {
						t.Fatalf("ncols=%d: width %d below floor %d in %v", n, w, minGridColTwips, got)
					}
				}
				if sum != PrintableWidthTwips {
					t.Fatalf("ncols=%d: widths sum to %d, want %d", n, sum, PrintableWidthTwips)
				}
			}
		})
	}
}

func fillShares(n int, v float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func dominantShares(ncols int) []float64 {
	out := make([]float64, ncols)
	for i := range out {
		out[i] = 1
	}
	out[0] = 1e9
	return out
}

func TestAddTable_ColWidths_FloorDoesNotDistortReachableRatios(t *testing.T) {
	for _, tc := range []struct {
		shares []float64
		want   []int
	}{
		{shares: []float64{1, 3}, want: []int{2340, 7020}},
		{shares: []float64{1, 1}, want: []int{4680, 4680}},
		{shares: []float64{1, 2, 3}, want: []int{1560, 3120, 4680}},
	} {
		d := New()
		row := make([]string, len(tc.shares))
		for i := range row {
			row[i] = "x"
		}
		if err := d.AddTable(nil, [][]string{row}, TableOptions{ColWidths: tc.shares}); err != nil {
			t.Fatalf("AddTable(%v): %v", tc.shares, err)
		}
		data, err := d.ToBytes()
		if err != nil {
			t.Fatalf("ToBytes: %v", err)
		}
		if got := gridColWidths(t, string(zipFile(t, data, "word/document.xml"))); !slices.Equal(got, tc.want) {
			t.Errorf("shares %v gave %v, want %v", tc.shares, got, tc.want)
		}
	}
}

func TestAddTable_TblPrChildrenInSchemaOrder(t *testing.T) {
	d := New()
	if err := d.AddTable([]string{"a"}, [][]string{{"1"}}, TableOptions{}); err != nil {
		t.Fatalf("AddTable: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))
	style, width, layout := strings.Index(doc, "<w:tblStyle"), strings.Index(doc, "<w:tblW"), strings.Index(doc, "<w:tblLayout")
	if style < 0 || width < 0 || layout < 0 {
		t.Fatalf("tblPr is missing a child (tblStyle=%d tblW=%d tblLayout=%d):\n%s", style, width, layout, doc)
	}
	if style >= width || width >= layout {
		t.Errorf("tblPr children out of schema order: tblStyle=%d, tblW=%d, tblLayout=%d", style, width, layout)
	}
}

func TestAddTable_RejectsPaddedCellExplosion(t *testing.T) {
	wide := make([]string, 2000)
	for i := range wide {
		wide[i] = "x"
	}
	rows := [][]string{wide}
	for range 500 {
		rows = append(rows, []string{"a", "b"})
	}

	err := New().AddTable(nil, rows, TableOptions{})
	if err == nil {
		t.Fatal("expected an error for a 2000-column x 501-row padded table, got nil")
	}
	for _, want := range []string{"2000", "columns", "flat list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q so the model can correct the spec", err, want)
		}
	}
}

func TestAddTable_AllowsTallNarrowTable(t *testing.T) {
	rows := make([][]string, 0, 5000)
	for range 5000 {
		rows = append(rows, []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"})
	}
	headers := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	if err := New().AddTable(headers, rows, TableOptions{HeaderBold: true}); err != nil {
		t.Errorf("a 10-column x 5001-row table is ordinary report content, got: %v", err)
	}
}

func TestAddTable_RejectsExcessiveTotalCells(t *testing.T) {
	rows := make([][]string, 0, 20_000)
	row := make([]string, 60)
	for range 20_000 {
		rows = append(rows, row)
	}
	err := New().AddTable(nil, rows, TableOptions{})
	if err == nil {
		t.Fatal("expected an error for a 60-column x 20000-row table, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error %q should report the total-size limit", err)
	}
}

func TestAddTable_NoColumns_Errors(t *testing.T) {
	d := New()
	if err := d.AddTable(nil, nil, TableOptions{}); err == nil {
		t.Error("expected error for a table with no headers and no rows")
	}
}

func TestAddImage_EmbedsMediaAndRelationship(t *testing.T) {
	d := New()
	png := []byte{0x89, 'P', 'N', 'G', 0, 0, 0, 0}
	if err := d.AddImage(png, "png", 2*EMUPerInch, 1*EMUPerInch, AlignCenter); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	names := zipNames(t, data)
	if !slices.Contains(names, "word/media/image1.png") {
		t.Errorf("expected word/media/image1.png in package, got %v", names)
	}

	media := zipFile(t, data, "word/media/image1.png")
	if !bytes.Equal(media, png) {
		t.Errorf("embedded image bytes do not match input")
	}

	rels := string(zipFile(t, data, "word/_rels/document.xml.rels"))
	if !strings.Contains(rels, `Id="rId101"`) || !strings.Contains(rels, `Target="media/image1.png"`) {
		t.Errorf("document.xml.rels missing image relationship:\n%s", rels)
	}

	doc := string(zipFile(t, data, "word/document.xml"))
	if !strings.Contains(doc, `r:embed="rId101"`) {
		t.Errorf("document.xml drawing does not reference the image relationship:\n%s", doc)
	}
}

func TestAddImage_RejectsOversizedImage(t *testing.T) {
	err := New().AddImage(make([]byte, maxImageBytes+1), "png", EMUPerInch, EMUPerInch, "")
	if err == nil {
		t.Fatal("expected an error for an image over the size limit, got nil")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error %q should name the limit", err)
	}
}

func TestAddImage_RejectsUnsupportedExtensionAndEmptyData(t *testing.T) {
	d := New()
	if err := d.AddImage([]byte{1, 2, 3}, "bmp", EMUPerInch, EMUPerInch, ""); err == nil {
		t.Error("expected error for unsupported image extension")
	}
	if err := d.AddImage(nil, "png", EMUPerInch, EMUPerInch, ""); err == nil {
		t.Error("expected error for empty image data")
	}
	if err := d.AddImage([]byte{1}, "png", 0, EMUPerInch, ""); err == nil {
		t.Error("expected error for non-positive width")
	}
}

func TestAddPageBreak(t *testing.T) {
	d := New()
	if err := d.AddParagraph([]Run{{Text: "before"}}, ""); err != nil {
		t.Fatalf("AddParagraph: %v", err)
	}
	if err := d.AddPageBreak(); err != nil {
		t.Fatalf("AddPageBreak: %v", err)
	}
	if err := d.AddParagraph([]Run{{Text: "after"}}, ""); err != nil {
		t.Fatalf("AddParagraph: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))
	if !strings.Contains(doc, `<w:br w:type="page"></w:br>`) {
		t.Errorf("document.xml missing page break:\n%s", doc)
	}
}

func TestAddTableOfContents_EmitsFieldAndHeading(t *testing.T) {
	d := New()
	if err := d.AddTableOfContents("Table of Contents", "1-2"); err != nil {
		t.Fatalf("AddTableOfContents: %v", err)
	}
	if err := d.AddHeading(1, "Chapter One"); err != nil {
		t.Fatalf("AddHeading: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))
	if !strings.Contains(doc, "Table of Contents") {
		t.Errorf("missing TOC heading text:\n%s", doc)
	}
	if !strings.Contains(doc, `TOC \o &#34;1-2&#34; \h \z \u`) && !strings.Contains(doc, `TOC \o "1-2" \h \z \u`) {
		t.Errorf("missing TOC field instruction text:\n%s", doc)
	}
	if !strings.Contains(doc, `w:fldCharType="begin"`) || !strings.Contains(doc, `w:fldCharType="end"`) {
		t.Errorf("missing TOC field begin/end markers:\n%s", doc)
	}
}

func TestAddTableOfContents_DefaultsLevelsWhenEmpty(t *testing.T) {
	d := New()
	if err := d.AddTableOfContents("", ""); err != nil {
		t.Fatalf("AddTableOfContents: %v", err)
	}
	data, err := d.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	doc := string(zipFile(t, data, "word/document.xml"))
	if !strings.Contains(doc, `1-3`) {
		t.Errorf("expected default levels 1-3 in TOC field:\n%s", doc)
	}
}
