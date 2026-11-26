// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"os"
	"testing"
)

func TestGenerateTableSelfEvalDeck(t *testing.T) {
	out := os.Getenv("PPTXGEN_WRITE_TABLE_DECK")
	if out == "" {
		t.Skip("PPTXGEN_WRITE_TABLE_DECK not set; skipping deck dump")
	}
	p := buildTableEvalDeck()
	data, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d bytes to %s (%d slides)", len(data), out, len(p.slides))
}

func buildTableEvalDeck() *Presentation {
	p := New()
	p.SetTitle("pptxgen table self-eval")
	p.setFixedTime(fixedTimeForGolden)

	s1 := p.AddSlide()
	s1.AddTable([][]TableCell{
		{{Text: "Auto"}, {Text: "Width"}, {Text: "Distributed"}},
		{{Text: "1"}, {Text: "2"}, {Text: "3"}},
	}, TableOptions{X: 1, Y: 1, Width: 8, FontFace: "Calibri", FontSize: 14})

	s2 := p.AddSlide()
	s2.AddTable([][]TableCell{
		{{Text: "Wide column"}, {Text: "Narrow"}},
		{{Text: "lots of room"}, {Text: "x"}},
	}, TableOptions{
		X: 1, Y: 1, Width: 8, ColWidths: []float64{6, 2},
		FontFace: "Calibri", FontSize: 14,
	})

	s3 := p.AddSlide()
	s3.AddTable([][]TableCell{
		{{Text: "Quarter"}, {Text: "Revenue"}, {Text: "Growth"}},
		{{Text: "Q1"}, {Text: "$10M"}, {Text: "+5%"}},
		{{Text: "Q2"}, {Text: "$12M"}, {Text: "+20%"}},
		{{Text: "Q3"}, {Text: "$15M"}, {Text: "+25%"}},
	}, TableOptions{
		X: 1, Y: 1, Width: 8,
		FontFace:     "Calibri",
		FontSize:     14,
		FontColor:    "#202020",
		FirstRowBold: true,
		FirstRowFill: "#1F4E79",
	})

	s4 := p.AddSlide()
	s4.AddTable([][]TableCell{
		{
			{Text: "bold", Bold: true},
			{Text: "italic", Italic: true},
			{Text: "size 24", FontSize: 24},
		},
		{
			{Text: "red text", FontColor: "#C00000"},
			{Text: "yellow fill", Fill: "#FFE699"},
			{Text: "right", Align: AlignRight},
		},
		{
			{Text: "top", VAlign: VAlignTop},
			{Text: "middle", VAlign: VAlignMiddle},
			{Text: "bottom", VAlign: VAlignBottom},
		},
	}, TableOptions{
		X: 0.5, Y: 0.5, Width: 9,
		FontFace:   "Calibri",
		FontSize:   12,
		RowHeights: []float64{0.6, 0.6, 1.2},
	})

	s5 := p.AddSlide()
	s5.AddTable([][]TableCell{
		{{Text: "Bordered"}, {Text: "Cells"}},
		{{Text: "1"}, {Text: "2"}},
		{{Text: "3"}, {Text: "4"}},
	}, TableOptions{
		X: 1, Y: 1, Width: 6,
		FontFace:  "Calibri",
		FontSize:  14,
		FontColor: "#202020",
		Border:    Border{Color: "#333333", Width: 1.5, Style: BorderSolid},
	})

	s6 := p.AddSlide()
	s6.AddTable([][]TableCell{
		{{Text: "Region", Bold: true, FontColor: "#FFFFFF"},
			{Text: "Sales", Bold: true, FontColor: "#FFFFFF"},
			{Text: "Trend", Bold: true, FontColor: "#FFFFFF"}},
		{{Text: "North"}, {Text: "$5M"}, {Text: "Up"}},
		{{Text: "South"}, {Text: "$3M"}, {Text: "Flat"}},
		{{Text: "East"}, {Text: "$7M"}, {Text: "Up"}},
		{{Text: "West"}, {Text: "$4M"}, {Text: "Down"}},
	}, TableOptions{
		X: 1, Y: 1, Width: 8,
		FontFace:     "Calibri",
		FontSize:     14,
		FontColor:    "#202020",
		FirstRowBold: true,
		FirstRowFill: "#1F4E79",
		Border:       Border{Color: "#A6A6A6", Width: 1, Style: BorderSolid},
	})

	return p
}
