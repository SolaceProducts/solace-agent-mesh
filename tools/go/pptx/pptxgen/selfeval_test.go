// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfEvalDeck(t *testing.T) {
	out := os.Getenv("PPTXGEN_WRITE_DECK")
	if out == "" {
		t.Skip("PPTXGEN_WRITE_DECK not set; skipping deck dump")
	}
	p := buildEmptyDeck()
	data, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d bytes to %s", len(data), out)
}

func TestGenerateChartSelfEvalDeck(t *testing.T) {
	out := os.Getenv("PPTXGEN_WRITE_CHART_DECK")
	if out == "" {
		t.Skip("PPTXGEN_WRITE_CHART_DECK not set; skipping chart deck dump")
	}
	p := New()
	p.SetTitle("pptxgen chart deck")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)

	labels := []string{"Q1", "Q2", "Q3", "Q4"}
	apples := ChartSeries{Name: "Apples", Labels: labels, Values: []float64{12, 18, 15, 22}}
	oranges := ChartSeries{Name: "Oranges", Labels: labels, Values: []float64{9, 14, 16, 11}}
	pears := ChartSeries{Name: "Pears", Labels: labels, Values: []float64{4.5, 7, 6.25, 8}}

	add := func(kind ChartType, title string, opts ChartOptions, ser ...ChartSeries) {
		opts.X, opts.Y, opts.Width, opts.Height = 1, 1, 8, 5
		opts.Title = title
		opts.ShowTitle = title != ""
		s := p.AddSlide()
		s.AddChart(kind, ser, opts)
	}

	add(ChartBar, "Bar — title + legend + values",
		ChartOptions{ShowLegend: true, ShowValues: true},
		apples, oranges)
	add(ChartBar, "Bar — no legend",
		ChartOptions{},
		apples, oranges)
	add(ChartBar, "",
		ChartOptions{ShowLegend: true},
		apples, oranges)
	add(ChartBar, "Bar — custom palette",
		ChartOptions{ShowLegend: true, Colors: []string{"#3366CC", "#CC3333", "#33AA55"}},
		apples, oranges, pears)

	add(ChartLine, "Line — multi-series",
		ChartOptions{ShowLegend: true},
		apples, oranges, pears)
	add(ChartLine, "Line — single series + values",
		ChartOptions{ShowLegend: false, ShowValues: true},
		apples)

	add(ChartArea, "Area — multi-series",
		ChartOptions{ShowLegend: true},
		apples, oranges)
	add(ChartArea, "Area — single series",
		ChartOptions{ShowLegend: false},
		apples)

	data, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d bytes to %s", len(data), out)
}

func TestGeneratePieDoughnutSelfEvalDeck(t *testing.T) {
	out := os.Getenv("PPTXGEN_WRITE_PIE_DOUGHNUT_DECK")
	if out == "" {
		t.Skip("PPTXGEN_WRITE_PIE_DOUGHNUT_DECK not set; skipping deck dump")
	}
	p := New()
	p.SetTitle("pptxgen pie + doughnut deck")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)

	pie5 := ChartSeries{
		Name:   "Mix",
		Labels: []string{"Apples", "Oranges", "Pears", "Plums", "Berries"},
		Values: []float64{30, 22, 18, 15, 15},
	}
	pie7 := ChartSeries{
		Name:   "Sectors",
		Labels: []string{"Tech", "Finance", "Health", "Retail", "Energy", "Industrial", "Other"},
		Values: []float64{28, 18, 14, 12, 10, 9, 9},
	}
	extraSeries := ChartSeries{
		Name:   "Bogus",
		Labels: pie5.Labels,
		Values: []float64{1, 1, 1, 1, 1},
	}

	add := func(kind ChartType, title string, opts ChartOptions, ser ...ChartSeries) {
		opts.X, opts.Y, opts.Width, opts.Height = 1, 1, 8, 5
		opts.Title = title
		opts.ShowTitle = title != ""
		s := p.AddSlide()
		s.AddChart(kind, ser, opts)
	}

	add(ChartPie, "Pie — title + legend",
		ChartOptions{ShowLegend: true},
		pie5)
	add(ChartPie, "",
		ChartOptions{},
		pie5)
	add(ChartPie, "Pie — show values",
		ChartOptions{ShowLegend: true, ShowValues: true},
		pie5)
	add(ChartPie, "Pie — custom palette",
		ChartOptions{ShowLegend: true, Colors: []string{"#3366CC", "#33AA55", "#CC3333", "#F39C12"}},
		pie5)
	add(ChartPie, "Pie — extra series silently dropped",
		ChartOptions{ShowLegend: true},
		pie5, extraSeries, extraSeries)

	add(ChartDoughnut, "Doughnut — title + legend",
		ChartOptions{ShowLegend: true},
		pie5)
	add(ChartDoughnut, "Doughnut — show values",
		ChartOptions{ShowLegend: true, ShowValues: true},
		pie5)
	add(ChartDoughnut, "Doughnut — custom palette",
		ChartOptions{ShowLegend: true, Colors: []string{"#3366CC", "#33AA55", "#CC3333", "#F39C12"}},
		pie5)
	add(ChartDoughnut, "Doughnut — 7-slice ring",
		ChartOptions{ShowLegend: true},
		pie7)

	data, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d bytes to %s", len(data), out)
}

func TestGenerateTextShapeDeck(t *testing.T) {
	out := os.Getenv("PPTXGEN_WRITE_TEXT_SHAPE_DECK")
	if out == "" {
		t.Skip("PPTXGEN_WRITE_TEXT_SHAPE_DECK not set; skipping deck dump")
	}
	p := buildTextShapeDeck()
	data, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d bytes to %s", len(data), out)
}

func TestGenerateImageNotesSelfEvalDeck(t *testing.T) {
	out := os.Getenv("PPTXGEN_WRITE_IMAGE_NOTES_DECK")
	if out == "" {
		t.Skip("PPTXGEN_WRITE_IMAGE_NOTES_DECK not set; skipping deck dump")
	}
	pngBytes := encodePNG(t, color.RGBA{R: 0xCC, G: 0x33, B: 0x33, A: 0xFF}, 256, 192)
	jpegBytes := encodeJPEG(t, color.RGBA{R: 0x33, G: 0x66, B: 0xCC, A: 0xFF}, 320, 240)

	p := New()
	p.SetTitle("pptxgen image + notes self-eval")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)

	s1 := p.AddSlide()
	s1.SetBackground(BackgroundOptions{Color: "#F4F1E8"})
	s1.AddImage(ImageOptions{X: 0.5, Y: 0.5, Width: 3, Height: 2.25, Data: pngBytes})
	s1.SetNotes("Slide 1: red PNG anchored top-left.")

	s2 := p.AddSlide()
	s2.AddImage(ImageOptions{X: 1.5, Y: 0.75, Width: 7, Height: 5, Data: jpegBytes})

	s3 := p.AddSlide()
	s3.AddImage(ImageOptions{X: 0.5, Y: 1.5, Width: 4, Height: 3, Data: pngBytes})
	s3.AddImage(ImageOptions{X: 5, Y: 1.5, Width: 4, Height: 3, Data: jpegBytes})
	s3.SetNotes("Slide 3 — left: PNG, right: JPEG.\n\nVerify both render at the same height.")

	s4 := p.AddSlide()
	s4.SetNotes("Slide 4: notes-only, no images. Confirm notes pane in PowerPoint.")

	data, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d bytes to %s", len(data), out)
}

func buildTextShapeDeck() *Presentation {
	p := New()
	p.SetTitle("pptxgen text + shape self-eval")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)

	caption := func(s *Slide, label string) {
		s.AddText(label, TextOptions{
			X: 0.3, Y: 0.2, Width: 9.4, Height: 0.5,
			FontFace: "Calibri", FontSize: 14,
			FontColor: "6B7280",
			Bold:      true,
		})
	}

	{
		s := p.AddSlide()
		caption(s, "1. Plain text — default styling")
		s.AddText("The quick brown fox jumps over the lazy dog.", TextOptions{
			X: 0.5, Y: 1.2, Width: 9, Height: 1,
		})
	}

	{
		s := p.AddSlide()
		caption(s, "2. Run-level styling — bold, italic, both, coloured")
		s.AddText("Bold sample", TextOptions{
			X: 0.5, Y: 1.2, Width: 4, Height: 0.7,
			FontFace: "Calibri", FontSize: 24, Bold: true,
		})
		s.AddText("Italic sample", TextOptions{
			X: 5.0, Y: 1.2, Width: 4, Height: 0.7,
			FontFace: "Calibri", FontSize: 24, Italic: true,
		})
		s.AddText("Bold + Italic", TextOptions{
			X: 0.5, Y: 2.2, Width: 4, Height: 0.7,
			FontFace: "Calibri", FontSize: 24, Bold: true, Italic: true,
		})
		s.AddText("Coloured red", TextOptions{
			X: 5.0, Y: 2.2, Width: 4, Height: 0.7,
			FontFace: "Calibri", FontSize: 24, FontColor: "DC2626",
		})
		s.AddText("Bold + Italic + Coloured (combined)", TextOptions{
			X: 0.5, Y: 3.2, Width: 9, Height: 0.7,
			FontFace: "Calibri", FontSize: 24,
			FontColor: "1D4ED8", Bold: true, Italic: true,
		})
	}

	{
		s := p.AddSlide()
		caption(s, "3. Horizontal alignment — left / center / right")
		const y, h = 1.5, 1.5
		const cw = 3.0
		for i, a := range []Align{AlignLeft, AlignCenter, AlignRight} {
			x := 0.3 + float64(i)*(cw+0.2)
			s.AddShape(ShapeRect, ShapeOptions{
				X: x, Y: y, Width: cw, Height: h,
				LineColor: "9CA3AF", LineWidth: 1,
			})
			label := []string{"Left", "Center", "Right"}[i]
			s.AddText(label, TextOptions{
				X: x, Y: y, Width: cw, Height: h,
				FontFace: "Calibri", FontSize: 28,
				FontColor: "111827", Align: a, VAlign: VAlignMiddle,
			})
		}
	}

	{
		s := p.AddSlide()
		caption(s, "4. Vertical alignment — top / middle / bottom")
		const y, h = 1.5, 4
		const cw = 3.0
		for i, v := range []VAlign{VAlignTop, VAlignMiddle, VAlignBottom} {
			x := 0.3 + float64(i)*(cw+0.2)
			s.AddShape(ShapeRect, ShapeOptions{
				X: x, Y: y, Width: cw, Height: h,
				LineColor: "9CA3AF", LineWidth: 1,
			})
			label := []string{"Top", "Middle", "Bottom"}[i]
			s.AddText(label, TextOptions{
				X: x, Y: y, Width: cw, Height: h,
				FontFace: "Calibri", FontSize: 28,
				FontColor: "111827", Align: AlignCenter, VAlign: v,
			})
		}
	}

	{
		s := p.AddSlide()
		caption(s, "5. Line spacing — 1.0 / 1.5 / 2.0")
		multi := "Line one\nLine two\nLine three"
		const y, h = 1.5, 4.5
		const cw = 3.0
		for i, sp := range []float64{1.0, 1.5, 2.0} {
			x := 0.3 + float64(i)*(cw+0.2)
			s.AddShape(ShapeRect, ShapeOptions{
				X: x, Y: y, Width: cw, Height: h,
				LineColor: "9CA3AF", LineWidth: 1,
			})
			s.AddText(multi, TextOptions{
				X: x + 0.1, Y: y + 0.1, Width: cw - 0.2, Height: h - 0.2,
				FontFace: "Calibri", FontSize: 18,
				FontColor:   "111827",
				LineSpacing: sp,
			})
		}
	}

	{
		s := p.AddSlide()
		caption(s, "6. Multi-line text — newlines produce paragraphs")
		body := "First paragraph.\nSecond paragraph.\nThird paragraph with a longer body."
		s.AddText(body, TextOptions{
			X: 0.5, Y: 1.2, Width: 9, Height: 5,
			FontFace: "Calibri", FontSize: 20,
			FontColor: "111827",
		})
	}

	{
		s := p.AddSlide()
		caption(s, "7. Text frame fill — Fill option emits <a:solidFill> on <p:spPr>")
		s.AddText("Filled text frame", TextOptions{
			X: 1, Y: 2, Width: 8, Height: 2,
			FontFace: "Calibri", FontSize: 28,
			FontColor: "FFFFFF",
			Align:     AlignCenter, VAlign: VAlignMiddle,
			Fill: "1D4ED8",
		})
	}

	{
		s := p.AddSlide()
		caption(s, "8. Shape geometries — rect / roundRect / ellipse")
		s.AddShape(ShapeRect, ShapeOptions{
			X: 0.5, Y: 1.5, Width: 2.5, Height: 2,
			Fill:      "BFDBFE",
			LineColor: "1D4ED8", LineWidth: 1,
		})
		s.AddShape(ShapeRoundRect, ShapeOptions{
			X: 3.7, Y: 1.5, Width: 2.5, Height: 2,
			Fill:      "BBF7D0",
			LineColor: "16A34A", LineWidth: 1,
		})
		s.AddShape(ShapeEllipse, ShapeOptions{
			X: 6.9, Y: 1.5, Width: 2.5, Height: 2,
			Fill:      "FECACA",
			LineColor: "DC2626", LineWidth: 1,
		})
	}

	{
		s := p.AddSlide()
		caption(s, "9. Shape styling — fill / line / both / neither")
		s.AddShape(ShapeRect, ShapeOptions{
			X: 0.5, Y: 1.5, Width: 2, Height: 1.5,
			Fill: "BFDBFE",
		})
		s.AddText("fill only", TextOptions{
			X: 0.5, Y: 3.1, Width: 2, Height: 0.4,
			FontFace: "Calibri", FontSize: 12,
			FontColor: "111827", Align: AlignCenter,
		})
		s.AddShape(ShapeRect, ShapeOptions{
			X: 3, Y: 1.5, Width: 2, Height: 1.5,
			LineColor: "1D4ED8", LineWidth: 2,
		})
		s.AddText("line only", TextOptions{
			X: 3, Y: 3.1, Width: 2, Height: 0.4,
			FontFace: "Calibri", FontSize: 12,
			FontColor: "111827", Align: AlignCenter,
		})
		s.AddShape(ShapeRect, ShapeOptions{
			X: 5.5, Y: 1.5, Width: 2, Height: 1.5,
			Fill:      "BBF7D0",
			LineColor: "16A34A", LineWidth: 3,
		})
		s.AddText("fill + line", TextOptions{
			X: 5.5, Y: 3.1, Width: 2, Height: 0.4,
			FontFace: "Calibri", FontSize: 12,
			FontColor: "111827", Align: AlignCenter,
		})
		s.AddShape(ShapeRect, ShapeOptions{
			X: 8, Y: 1.5, Width: 1.5, Height: 1.5,
		})
		s.AddText("(empty)", TextOptions{
			X: 8, Y: 3.1, Width: 1.5, Height: 0.4,
			FontFace: "Calibri", FontSize: 12,
			FontColor: "111827", Align: AlignCenter,
		})
	}

	{
		s := p.AddSlide()
		caption(s, "10. Combined — text on a roundRect card")
		s.AddShape(ShapeRoundRect, ShapeOptions{
			X: 1.5, Y: 2, Width: 7, Height: 3.5,
			Fill:      "FFFFFF",
			LineColor: "9CA3AF", LineWidth: 1,
		})
		s.AddText("Headline", TextOptions{
			X: 1.5, Y: 2.2, Width: 7, Height: 0.8,
			FontFace: "Calibri", FontSize: 28, Bold: true,
			FontColor: "111827",
			Align:     AlignCenter, VAlign: VAlignMiddle,
		})
		s.AddText("Supporting body copy explaining the headline in a sentence or two.", TextOptions{
			X: 2, Y: 3.1, Width: 6, Height: 2.0,
			FontFace: "Calibri", FontSize: 16,
			FontColor: "374151",
			Align:     AlignCenter, VAlign: VAlignTop,
			LineSpacing: 1.3,
		})
	}

	return p
}

func encodePNG(t *testing.T, c color.Color, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	tmp := filepath.Join(t.TempDir(), "x.png")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	if encErr := png.Encode(f, img); encErr != nil {
		_ = f.Close()
		t.Fatalf("encode png: %v", encErr)
	}
	_ = f.Close()
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read png: %v", err)
	}
	return data
}

func encodeJPEG(t *testing.T, c color.Color, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	tmp := filepath.Join(t.TempDir(), "x.jpeg")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatalf("create jpeg: %v", err)
	}
	if encErr := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); encErr != nil {
		_ = f.Close()
		t.Fatalf("encode jpeg: %v", encErr)
	}
	_ = f.Close()
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read jpeg: %v", err)
	}
	return data
}
