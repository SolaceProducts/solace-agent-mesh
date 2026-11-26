// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image/color"
	"io"
	"os"
	"strings"
	"testing"
)

func TestE2EAllPrimitives(t *testing.T) {
	p := buildE2EDeck(t)

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}

	parts := map[string][]byte{}
	for _, f := range zr.File {
		rc, oerr := f.Open()
		if oerr != nil {
			t.Fatalf("open %q: %v", f.Name, oerr)
		}
		data, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr != nil {
			t.Fatalf("read %q: %v", f.Name, rerr)
		}
		parts[f.Name] = data
	}

	for name, data := range parts {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		if perr := xmlParse(data); perr != nil {
			t.Errorf("malformed XML in %s: %v", name, perr)
		}
	}

	wantPrefixes := []string{
		"ppt/slides/slide1.xml",
		"ppt/slides/slide2.xml",
		"ppt/slides/slide3.xml",
		"ppt/slides/slide4.xml",
		"ppt/charts/chart1.xml",
		"ppt/notesSlides/notesSlide2.xml",
		"ppt/media/image1.png",
		"ppt/media/image2.jpeg",
	}
	for _, prefix := range wantPrefixes {
		if _, ok := parts[prefix]; !ok {
			t.Errorf("expected part %q not present in deck", prefix)
		}
	}
}

func TestGenerateE2EDeck(t *testing.T) {
	out := os.Getenv("PPTXGEN_WRITE_E2E_DECK")
	if out == "" {
		t.Skip("PPTXGEN_WRITE_E2E_DECK not set; skipping deck dump")
	}
	p := buildE2EDeck(t)
	data, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
	t.Logf("wrote %d bytes to %s", len(data), out)
}

func buildE2EDeck(t *testing.T) *Presentation {
	t.Helper()

	pngBytes := encodePNG(t, color.RGBA{R: 0xCC, G: 0x33, B: 0x33, A: 0xFF}, 320, 240)
	jpegBytes := encodeJPEG(t, color.RGBA{R: 0x33, G: 0x66, B: 0xCC, A: 0xFF}, 320, 240)

	p := New()
	p.SetTitle("pptxgen E2E primitives deck")
	p.SetAuthor("pptxgen tests")
	p.setFixedTime(fixedTimeForGolden)

	{
		s := p.AddSlide()
		s.SetBackground(BackgroundOptions{Color: "#F4F1E8"})

		s.AddText("Slide 1 — Text & Shapes", TextOptions{
			X: 0.5, Y: 0.4, Width: 9, Height: 0.8,
			FontFace: "Calibri", FontSize: 28,
			FontColor: "1E3A5F", Bold: true,
		})

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

		s.AddText("Bold sample", TextOptions{
			X: 0.5, Y: 4, Width: 4, Height: 0.7,
			FontFace: "Calibri", FontSize: 22, Bold: true,
			FontColor: "111827",
		})
		s.AddText("Italic sample", TextOptions{
			X: 5, Y: 4, Width: 4, Height: 0.7,
			FontFace: "Calibri", FontSize: 22, Italic: true,
			FontColor: "111827",
		})
		s.AddText("Centred coloured headline", TextOptions{
			X: 0.5, Y: 5, Width: 9, Height: 1,
			FontFace: "Calibri", FontSize: 26,
			FontColor: "1D4ED8", Bold: true,
			Align: AlignCenter, VAlign: VAlignMiddle,
			Fill: "FFFFFF",
		})
	}

	{
		s := p.AddSlide()
		s.AddText("Slide 2 — Images & Table", TextOptions{
			X: 0.5, Y: 0.3, Width: 9, Height: 0.7,
			FontFace: "Calibri", FontSize: 24,
			FontColor: "1E3A5F", Bold: true,
		})

		s.AddImage(ImageOptions{
			X: 0.5, Y: 1.2, Width: 4, Height: 3, Data: pngBytes,
		})
		s.AddImage(ImageOptions{
			X: 5.5, Y: 1.2, Width: 4, Height: 3, Data: jpegBytes,
		})

		rows := [][]TableCell{
			{{Text: "Region"}, {Text: "Q1"}, {Text: "Q2"}, {Text: "Q3"}, {Text: "Q4"}},
			{{Text: "North"}, {Text: "12"}, {Text: "18"}, {Text: "15"}, {Text: "22"}},
			{{Text: "South"}, {Text: "9"}, {Text: "14"}, {Text: "16"}, {Text: "11"}},
			{{Text: "East"}, {Text: "4.5"}, {Text: "7"}, {Text: "6.25"}, {Text: "8"}},
		}
		s.AddTable(rows, TableOptions{
			X: 0.5, Y: 4.5, Width: 9,
			RowHeights:   []float64{0.4, 0.4, 0.4, 0.4},
			FontFace:     "Calibri",
			FontSize:     14,
			FontColor:    "111827",
			Border:       Border{Color: "9CA3AF", Width: 0.75, Style: BorderSolid},
			FirstRowBold: true,
			FirstRowFill: "DBEAFE",
		})

		s.SetNotes("Slide 2 walks through the visual primitives: " +
			"two images of equal height and a four-row table with a " +
			"highlighted header row.")
	}

	{
		s := p.AddSlide()
		s.AddText("Slide 3 — Bar / Line / Area Charts", TextOptions{
			X: 0.5, Y: 0.3, Width: 9, Height: 0.7,
			FontFace: "Calibri", FontSize: 24,
			FontColor: "1E3A5F", Bold: true,
		})

		labels := []string{"Q1", "Q2", "Q3", "Q4"}
		apples := ChartSeries{Name: "Apples", Labels: labels, Values: []float64{12, 18, 15, 22}}
		oranges := ChartSeries{Name: "Oranges", Labels: labels, Values: []float64{9, 14, 16, 11}}

		s.AddChart(ChartBar,
			[]ChartSeries{apples, oranges},
			ChartOptions{
				X: 0.3, Y: 1.2, Width: 3.1, Height: 6,
				Title: "Bar", ShowTitle: true, ShowLegend: true,
			})
		s.AddChart(ChartLine,
			[]ChartSeries{apples, oranges},
			ChartOptions{
				X: 3.45, Y: 1.2, Width: 3.1, Height: 6,
				Title: "Line", ShowTitle: true, ShowLegend: true,
			})
		s.AddChart(ChartArea,
			[]ChartSeries{apples, oranges},
			ChartOptions{
				X: 6.6, Y: 1.2, Width: 3.1, Height: 6,
				Title: "Area", ShowTitle: true, ShowLegend: true,
			})
	}

	{
		s := p.AddSlide()
		s.AddText("Slide 4 — Pie / Doughnut Charts", TextOptions{
			X: 0.5, Y: 0.3, Width: 9, Height: 0.7,
			FontFace: "Calibri", FontSize: 24,
			FontColor: "1E3A5F", Bold: true,
		})

		mix := ChartSeries{
			Name:   "Mix",
			Labels: []string{"Apples", "Oranges", "Pears", "Plums", "Berries"},
			Values: []float64{30, 22, 18, 15, 15},
		}

		s.AddChart(ChartPie,
			[]ChartSeries{mix},
			ChartOptions{
				X: 0.5, Y: 1.2, Width: 4.5, Height: 6,
				Title: "Pie", ShowTitle: true, ShowLegend: true,
			})
		s.AddChart(ChartDoughnut,
			[]ChartSeries{mix},
			ChartOptions{
				X: 5.0, Y: 1.2, Width: 4.5, Height: 6,
				Title: "Doughnut", ShowTitle: true, ShowLegend: true, ShowValues: true,
			})
	}

	return p
}

func xmlParse(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
