// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tableGoldenCase struct {
	name string
	rows [][]TableCell
	opts TableOptions
}

func tableGoldenCases() []tableGoldenCase {
	return []tableGoldenCase{
		{
			name: "table_auto_width",
			rows: [][]TableCell{
				{{Text: "A"}, {Text: "B"}, {Text: "C"}},
				{{Text: "1"}, {Text: "2"}, {Text: "3"}},
			},
			opts: TableOptions{X: 1, Y: 1, Width: 6},
		},
		{
			name: "table_explicit_widths",
			rows: [][]TableCell{
				{{Text: "Wide"}, {Text: "Narrow"}},
				{{Text: "x"}, {Text: "y"}},
			},
			opts: TableOptions{X: 0.5, Y: 0.5, Width: 6, ColWidths: []float64{4, 2}},
		},
		{
			name: "table_first_row_bold",
			rows: [][]TableCell{
				{{Text: "H1"}, {Text: "H2"}},
				{{Text: "a"}, {Text: "b"}},
			},
			opts: TableOptions{X: 1, Y: 1, Width: 4, FirstRowBold: true},
		},
		{
			name: "table_first_row_fill",
			rows: [][]TableCell{
				{{Text: "Header"}, {Text: "Header"}},
				{{Text: "row"}, {Text: "row"}},
			},
			opts: TableOptions{X: 1, Y: 1, Width: 4, FirstRowFill: "#1F4E79"},
		},
		{
			name: "table_first_row_bold_and_fill",
			rows: [][]TableCell{
				{{Text: "H1"}, {Text: "H2"}},
				{{Text: "a"}, {Text: "b"}},
			},
			opts: TableOptions{
				X: 1, Y: 1, Width: 4,
				FirstRowBold: true,
				FirstRowFill: "#1F4E79",
				FontColor:    "#FFFFFF",
			},
		},
		{
			name: "table_per_cell_overrides",
			rows: [][]TableCell{
				{
					{Text: "bold", Bold: true},
					{Text: "italic", Italic: true},
					{Text: "size", FontSize: 18},
				},
				{
					{Text: "color", FontColor: "#C00000"},
					{Text: "fill", Fill: "#FFE699"},
					{Text: "right", Align: AlignRight},
				},
				{
					{Text: "top", VAlign: VAlignTop},
					{Text: "mid", VAlign: VAlignMiddle},
					{Text: "bot", VAlign: VAlignBottom},
				},
			},
			opts: TableOptions{X: 0.5, Y: 0.5, Width: 9, FontSize: 12, FontFace: "Calibri"},
		},
		{
			name: "table_uniform_border",
			rows: [][]TableCell{
				{{Text: "L"}, {Text: "R"}},
				{{Text: "1"}, {Text: "2"}},
			},
			opts: TableOptions{
				X: 1, Y: 1, Width: 4,
				Border: Border{Color: "#333333", Width: 1.5, Style: BorderSolid},
			},
		},
		{
			name: "table_explicit_row_heights",
			rows: [][]TableCell{
				{{Text: "Tall"}, {Text: "Tall"}},
				{{Text: "Short"}, {Text: "Short"}},
			},
			opts: TableOptions{X: 1, Y: 1, Width: 4, RowHeights: []float64{1.0, 0.3}},
		},
		{
			name: "table_xml_escaping",
			rows: [][]TableCell{
				{{Text: "<a&b>"}, {Text: `quote "x"`, FontFace: "Times <New> Roman"}},
			},
			opts: TableOptions{X: 1, Y: 1, Width: 4, FontFace: "Calibri & Co."},
		},
		{
			name: "table_ragged_rows",
			rows: [][]TableCell{
				{{Text: "a"}, {Text: "b"}, {Text: "c"}},
				{{Text: "d"}},
				{{Text: "e"}, {Text: "f"}, {Text: "g"}, {Text: "h"}},
			},
			opts: TableOptions{X: 1, Y: 1, Width: 4},
		},
	}
}

func TestEmitTableGolden(t *testing.T) {
	for _, c := range tableGoldenCases() {
		t.Run(c.name, func(t *testing.T) {
			el := &tableElement{rows: c.rows, opts: c.opts}
			var b strings.Builder
			el.emitXML(&b, newIDAllocator())
			got := []byte(b.String())

			path := filepath.Join("testdata", c.name+".xml")
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatalf("mkdir testdata: %v", err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("emitter output for %s differs from golden\n--- want ---\n%s\n--- got ---\n%s",
					c.name, want, got)
			}
		})
	}
}

func TestEmitTableEmpty(t *testing.T) {
	cases := []struct {
		name string
		rows [][]TableCell
	}{
		{"nil", nil},
		{"zero rows", [][]TableCell{}},
		{"all empty rows", [][]TableCell{nil, {}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			el := &tableElement{rows: c.rows, opts: TableOptions{X: 1, Y: 1, Width: 4}}
			var b strings.Builder
			el.emitXML(&b, newIDAllocator())
			if b.Len() != 0 {
				t.Errorf("expected empty output, got %q", b.String())
			}
		})
	}
}

func TestTableOptionsFill(t *testing.T) {
	rows := [][]TableCell{
		{{Text: "header"}},
		{{Text: "body"}},
		{{Text: "override", Fill: "00FF00"}},
	}
	el := &tableElement{
		rows: rows,
		opts: TableOptions{
			X: 1, Y: 1, Width: 4,
			FirstRowFill: "FF0000",
			FirstRowBold: true,
			Fill:         "0000FF",
		},
	}
	var b strings.Builder
	el.emitXML(&b, newIDAllocator())
	got := b.String()

	if !strings.Contains(got, `<a:solidFill><a:srgbClr val="FF0000"/></a:solidFill>`) {
		t.Errorf("expected FirstRowFill (FF0000) on row 0, got:\n%s", got)
	}
	if !strings.Contains(got, `<a:solidFill><a:srgbClr val="0000FF"/></a:solidFill>`) {
		t.Errorf("expected opts.Fill (0000FF) on row 1, got:\n%s", got)
	}
	if !strings.Contains(got, `<a:solidFill><a:srgbClr val="00FF00"/></a:solidFill>`) {
		t.Errorf("expected per-cell Fill (00FF00) on row 2, got:\n%s", got)
	}
}

func TestResolveColWidths(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		got := resolveColWidths([]float64{1, 2, 3}, 3, 0)
		want := []int{914400, 1828800, 2743200}
		if !equalInts(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("auto distribute even", func(t *testing.T) {
		got := resolveColWidths(nil, 4, 1000)
		want := []int{250, 250, 250, 250}
		if !equalInts(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("auto distribute remainder", func(t *testing.T) {
		got := resolveColWidths(nil, 3, 1000)
		want := []int{334, 333, 333}
		if !equalInts(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
	t.Run("colWidths shorter than colCount falls back to auto", func(t *testing.T) {
		got := resolveColWidths([]float64{1}, 3, 900)
		want := []int{300, 300, 300}
		if !equalInts(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
}

func TestResolveRowHeights(t *testing.T) {
	got := resolveRowHeights([]float64{0.5, 0.7}, 4)
	want := []int{457200, 640080, defaultTableRowHeightEMU, defaultTableRowHeightEMU}
	if !equalInts(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestAlignAttr(t *testing.T) {
	cases := []struct {
		in   Align
		want string
	}{
		{AlignLeft, "l"},
		{AlignCenter, "ctr"},
		{AlignRight, "r"},
	}
	for _, c := range cases {
		if got := alignAttr(c.in); got != c.want {
			t.Errorf("alignAttr(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVAlignAttr(t *testing.T) {
	cases := []struct {
		in   VAlign
		want string
	}{
		{VAlignTop, "t"},
		{VAlignMiddle, "ctr"},
		{VAlignBottom, "b"},
	}
	for _, c := range cases {
		if got := vAlignAttr(c.in); got != c.want {
			t.Errorf("vAlignAttr(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAddTableThroughPresentation(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddTable([][]TableCell{
		{{Text: "Header", Bold: true}},
		{{Text: "Body"}},
	}, TableOptions{X: 1, Y: 1, Width: 4, FirstRowBold: true})

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	slideXML := findEntry(t, out, "ppt/slides/slide1.xml")
	for _, marker := range [][]byte{
		[]byte(`<p:graphicFrame>`),
		[]byte(`<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table">`),
		[]byte(`<a:tbl>`),
		[]byte(`<a:tblPr firstRow="1">`),
		[]byte(`<a:t>Header</a:t>`),
		[]byte(`<a:t>Body</a:t>`),
	} {
		if !bytes.Contains(slideXML, marker) {
			t.Errorf("slide XML missing %q\nhave: %s", marker, slideXML)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
