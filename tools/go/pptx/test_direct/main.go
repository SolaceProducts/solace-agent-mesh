// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

type Theme struct {
	HeadingFont string      `json:"headingFont"`
	BodyFont    string      `json:"bodyFont"`
	Colors      ThemeColors `json:"colors"`
}

type ThemeColors struct {
	Background1 string `json:"background1"`
	Background2 string `json:"background2"`
	Text1       string `json:"text1"`
	Text2       string `json:"text2"`
	Accent1     string `json:"accent1"`
	Accent2     string `json:"accent2"`
	Accent3     string `json:"accent3"`
	Accent4     string `json:"accent4"`
	Accent5     string `json:"accent5"`
	Accent6     string `json:"accent6"`
}

type LayoutSlide struct {
	Layout     string     `json:"layout"`
	Title      *string    `json:"title,omitempty"`
	Subtitle   *string    `json:"subtitle,omitempty"`
	Body       *string    `json:"body,omitempty"`
	Column1    *string    `json:"column1,omitempty"`
	Column2    *string    `json:"column2,omitempty"`
	Column3    *string    `json:"column3,omitempty"`
	ImageRef   *string    `json:"image_ref,omitempty"`
	ImageData  *string    `json:"image_data,omitempty"`
	Footer     *string    `json:"footer,omitempty"`
	Background *string    `json:"background,omitempty"`
	TableData  *TableData `json:"table_data,omitempty"`
	ChartData  *ChartData `json:"chart_data,omitempty"`
}

type TableData struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

type ChartData struct {
	ChartType  string        `json:"chart_type"`
	Title      *string       `json:"title,omitempty"`
	Series     []ChartSeries `json:"series"`
	ShowLegend *bool         `json:"show_legend,omitempty"`
	ShowValues *bool         `json:"show_values,omitempty"`
}

type ChartSeries struct {
	Name       *string   `json:"name,omitempty"`
	Categories []string  `json:"categories"`
	Values     []float64 `json:"values"`
}

func ptr(s string) *string { return &s }
func bptr(b bool) *bool    { return &b }

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: test_direct <image0.png> <image1.png> <output.pptx>")
		os.Exit(2)
	}
	imgPath0 := os.Args[1]
	imgBytes0, err := os.ReadFile(imgPath0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading image 0: %v\n", err)
		os.Exit(1)
	}
	imgPath1 := os.Args[2]
	imgBytes1, err := os.ReadFile(imgPath1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading image 1: %v\n", err)
		os.Exit(1)
	}
	imageData := [][]byte{imgBytes0, imgBytes1}

	theme := Theme{
		HeadingFont: "Calibri Light",
		BodyFont:    "Calibri",
		Colors: ThemeColors{
			Background1: "FFFFFF",
			Background2: "F0F4F8",
			Text1:       "2D3748",
			Text2:       "718096",
			Accent1:     "00838F",
			Accent2:     "FF6F00",
			Accent3:     "43A047",
			Accent4:     "5C6BC0",
			Accent5:     "E53935",
			Accent6:     "8E24AA",
		},
	}

	slides := []LayoutSlide{
		{
			Layout:   "title",
			Title:    ptr("Meet Ollie the Otter"),
			Subtitle: ptr("SAM PPTX Tool — Image Layout Demo"),
		},
		{
			Layout:   "image_right",
			Title:    ptr("Your Friendly Mascot"),
			Body:     ptr("Ollie the Otter is the official mascot of the SAM PPTX tool.\n\nOllie helps you create beautiful presentations with:\n\n- Professional slide layouts\n- Consistent theme colors\n- Automatic contrast correction\n- Image placement (like this!)"),
			ImageRef: ptr("0"),
			Footer:   ptr("Solace Agent Mesh"),
		},
		{
			Layout:   "image_left",
			Title:    ptr("Image on the Left"),
			Body:     ptr("The image_left layout places the image on the left side with text on the right.\n\nPerfect for:\n- Team member introductions\n- Product screenshots with descriptions\n- Before/after comparisons\n- Any visual + text combination"),
			ImageRef: ptr("1"),
			Footer:   ptr("Solace Agent Mesh"),
		},
		{
			Layout:   "image_full",
			Title:    ptr("Full-Page Image with Overlay"),
			ImageRef: ptr("1"),
		},
		{
			Layout: "text",
			Title:  ptr("All Available Layouts"),
			Body:   ptr("title — Title slide with centered heading and subtitle\nsection — Full-accent section divider\ntext — Title bar with body text\ntwo_column — Side-by-side text columns\nthree_column — Three text columns\nimage_left — Image on left, text on right\nimage_right — Text on left, image on right\nimage_full — Full-bleed image with optional title overlay\ntable — Data table with themed header row\nchart — Bar, line, pie, doughnut, or area chart\nblank — Empty slide with just the background"),
			Footer: ptr("Solace Agent Mesh"),
		},
		{
			Layout:  "two_column",
			Title:   ptr("Theme Features"),
			Column1: ptr("Color System\n\n- 10 named color slots\n- OOXML/PptxGenJS standard\n- background1/2 for fills\n- text1/2 for content\n- accent1-6 for highlights\n- Auto contrast correction"),
			Column2: ptr("Font System\n\n- headingFont for titles\n- bodyFont for content\n- Config-based defaults\n- Per-presentation overrides\n- get_default_theme tool\n  shows active defaults"),
			Footer:  ptr("Solace Agent Mesh"),
		},
		{
			Layout: "table",
			Title:  ptr("Slide Layout Reference"),
			TableData: &TableData{
				Headers: []string{"Layout", "Key Fields", "Best For"},
				Rows: [][]string{
					{"title", "title, subtitle", "Opening slide"},
					{"section", "title, subtitle", "Topic dividers"},
					{"text", "title, body", "Bullet points, descriptions"},
					{"two_column", "title, column1, column2", "Comparisons, side-by-side"},
					{"image_left/right", "title, body, image_data", "Visual + text content"},
					{"table", "title, table_data", "Data tables"},
					{"chart", "title, chart_data", "Data visualization"},
				},
			},
			Footer: ptr("Solace Agent Mesh"),
		},
		{
			Layout: "chart",
			Title:  ptr("Layout Popularity"),
			ChartData: &ChartData{
				ChartType:  "pie",
				Title:      ptr("Most Used Layouts"),
				ShowLegend: bptr(true),
				Series: []ChartSeries{
					{
						Name:       ptr("Usage"),
						Categories: []string{"Text", "Two Column", "Image", "Table", "Chart", "Title/Section"},
						Values:     []float64{30, 20, 25, 10, 10, 5},
					},
				},
			},
			Footer: ptr("Solace Agent Mesh"),
		},
		{
			Layout:   "section",
			Title:    ptr("That's All Folks!"),
			Subtitle: ptr("Created with the SAM PPTX Tool"),
		},
	}

	fmt.Fprintf(os.Stderr, "Creating presentation with %d slides, %d image artifact(s)\n", len(slides), len(imageData))

	pres := pptxgen.New()
	pres.SetTitle("Meet Ollie the Otter")
	pres.SetAuthor("Solace Agent Mesh")

	for _, sl := range slides {
		slide := pres.AddSlide()
		renderSlideTest(slide, sl, theme, imageData)
	}

	output := os.Args[3]
	data, err := pres.ToBytes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(output, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created: %s\n", output)
}

const (
	slideW      = 10.0
	slideH      = 5.625
	marginX     = 0.5
	marginY     = 0.3
	contentTop  = 1.3
	contentW    = 9.0
	titleHeight = 0.7
	footerY     = 5.1
)

func normalizeColor(c string) string {
	if len(c) > 0 && c[0] == '#' {
		return c[1:]
	}
	return c
}

func strOr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}

func boolOr(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

func pickTextColor(bg string) string {
	bg = normalizeColor(bg)
	if len(bg) != 6 {
		return "000000"
	}
	r := hexVal(bg[0:2])
	g := hexVal(bg[2:4])
	b := hexVal(bg[4:6])
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	if lum > 128 {
		return "000000"
	}
	return "FFFFFF"
}

func hexVal(s string) int {
	var v int
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v += int(c - '0')
		case c >= 'a' && c <= 'f':
			v += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v += int(c-'A') + 10
		}
	}
	return v
}

func ensureContrast(text, bg string) string {
	text = normalizeColor(text)
	bg = normalizeColor(bg)
	tLum := 0.299*float64(hexVal(text[0:2])) + 0.587*float64(hexVal(text[2:4])) + 0.114*float64(hexVal(text[4:6]))
	bLum := 0.299*float64(hexVal(bg[0:2])) + 0.587*float64(hexVal(bg[2:4])) + 0.114*float64(hexVal(bg[4:6]))
	diff := tLum - bLum
	if diff < 0 {
		diff = -diff
	}
	if diff < 60 {
		return pickTextColor(bg)
	}
	return text
}

func isDarkBg(bg string) bool {
	bg = normalizeColor(bg)
	if len(bg) != 6 {
		return false
	}
	r := hexVal(bg[0:2])
	g := hexVal(bg[2:4])
	b := hexVal(bg[4:6])
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return lum < 100
}

func renderSlideTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, images [][]byte) {
	bg := normalizeColor(t.Colors.Background1)
	if sl.Background != nil {
		bg = normalizeColor(*sl.Background)
	}
	slide.SetBackground(pptxgen.BackgroundOptions{Color: bg})

	switch sl.Layout {
	case "title":
		renderTitleTest(slide, sl, t, bg)
	case "section":
		renderSectionTest(slide, sl, t, bg)
	case "text":
		renderTextTest(slide, sl, t, bg)
	case "two_column":
		renderTwoColumnTest(slide, sl, t, bg)
	case "three_column":
		renderThreeColumnTest(slide, sl, t, bg)
	case "image_left":
		renderImageLeftTest(slide, sl, t, bg, images)
	case "image_right":
		renderImageRightTest(slide, sl, t, bg, images)
	case "image_full":
		renderImageFullTest(slide, sl, t, bg, images)
	case "table":
		renderTableTest(slide, sl, t, bg)
	case "chart":
		renderChartTest(slide, sl, t, bg)
	}

	if sl.Footer != nil && *sl.Footer != "" {
		slide.AddText(*sl.Footer, pptxgen.TextOptions{
			X: marginX, Y: footerY, Width: contentW, Height: 0.4,
			FontSize: 10, FontFace: t.BodyFont,
			FontColor: ensureContrast(normalizeColor(t.Colors.Text2), bg),
			Align:     pptxgen.AlignCenter,
		})
	}
}

func addTitleBarTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	if sl.Title == nil || *sl.Title == "" {
		return
	}

	accent := normalizeColor(t.Colors.Accent1)
	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: marginX, Y: marginY + titleHeight + 0.05, Width: contentW, Height: 0.03,
		Fill: accent, LineColor: accent,
	})
	slide.AddText(*sl.Title, pptxgen.TextOptions{
		X: marginX, Y: marginY, Width: contentW, Height: titleHeight,
		FontSize: 28, Bold: true, FontFace: t.HeadingFont,
		FontColor: ensureContrast(normalizeColor(t.Colors.Accent1), bg),
		VAlign:    pptxgen.VAlignBottom,
	})
}

func renderTitleTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	accent := normalizeColor(t.Colors.Accent1)
	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: 0, Y: 0, Width: slideW, Height: 0.08,
		Fill: accent, LineColor: accent,
	})

	slide.AddText(strOr(sl.Title, ""), pptxgen.TextOptions{
		X: 1, Y: 1.5, Width: 8, Height: 1.2,
		FontSize: 44, Bold: true, FontFace: t.HeadingFont,
		FontColor: ensureContrast(normalizeColor(t.Colors.Text1), bg),
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})

	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: 3, Y: 2.9, Width: 4, Height: 0.04,
		Fill: accent, LineColor: accent,
	})

	if sl.Subtitle != nil && *sl.Subtitle != "" {
		slide.AddText(*sl.Subtitle, pptxgen.TextOptions{
			X: 1.5, Y: 3.2, Width: 7, Height: 0.8,
			FontSize: 22, FontFace: t.BodyFont,
			FontColor: ensureContrast(normalizeColor(t.Colors.Text2), bg),
			Align:     pptxgen.AlignCenter,
		})
	}
}

func renderSectionTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	accent := normalizeColor(t.Colors.Accent1)
	slide.SetBackground(pptxgen.BackgroundOptions{Color: accent})

	textColor := ensureContrast("FFFFFF", accent)

	slide.AddText(strOr(sl.Title, ""), pptxgen.TextOptions{
		X: 1, Y: 1.8, Width: 8, Height: 1.2,
		FontSize: 40, Bold: true, FontFace: t.HeadingFont,
		FontColor: textColor,
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})

	if sl.Subtitle != nil && *sl.Subtitle != "" {
		slide.AddText(*sl.Subtitle, pptxgen.TextOptions{
			X: 1.5, Y: 3.1, Width: 7, Height: 0.7,
			FontSize: 20, FontFace: t.BodyFont,
			FontColor: textColor,
			Align:     pptxgen.AlignCenter,
		})
	}
}

func renderTextTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	addTitleBarTest(slide, sl, t, bg)
	slide.AddText(strOr(sl.Body, ""), pptxgen.TextOptions{
		X: marginX, Y: contentTop, Width: contentW, Height: slideH - contentTop - 0.6,
		FontSize: 18, FontFace: t.BodyFont,
		FontColor: ensureContrast(normalizeColor(t.Colors.Text1), bg),
	})
}

func renderTwoColumnTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	addTitleBarTest(slide, sl, t, bg)
	colW := (contentW - 0.3) / 2
	textColor := ensureContrast(normalizeColor(t.Colors.Text1), bg)

	slide.AddText(strOr(sl.Column1, ""), pptxgen.TextOptions{
		X: marginX, Y: contentTop, Width: colW, Height: slideH - contentTop - 0.6,
		FontSize: 16, FontFace: t.BodyFont, FontColor: textColor,
	})
	slide.AddText(strOr(sl.Column2, ""), pptxgen.TextOptions{
		X: marginX + colW + 0.3, Y: contentTop, Width: colW, Height: slideH - contentTop - 0.6,
		FontSize: 16, FontFace: t.BodyFont, FontColor: textColor,
	})
}

func renderThreeColumnTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	addTitleBarTest(slide, sl, t, bg)
	colW := (contentW - 0.6) / 3
	gap := 0.3
	textColor := ensureContrast(normalizeColor(t.Colors.Text1), bg)

	for i, text := range []*string{sl.Column1, sl.Column2, sl.Column3} {
		slide.AddText(strOr(text, ""), pptxgen.TextOptions{
			X: marginX + float64(i)*(colW+gap), Y: contentTop,
			Width: colW, Height: slideH - contentTop - 0.6,
			FontSize: 14, FontFace: t.BodyFont, FontColor: textColor,
		})
	}
}

func renderImageLeftTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string, images [][]byte) {
	addTitleBarTest(slide, sl, t, bg)

	imgW := 4.2
	addImageTest(slide, sl, marginX, contentTop, imgW, slideH-contentTop-0.6, images)

	slide.AddText(strOr(sl.Body, ""), pptxgen.TextOptions{
		X: marginX + imgW + 0.3, Y: contentTop,
		Width: contentW - imgW - 0.3, Height: slideH - contentTop - 0.6,
		FontSize: 16, FontFace: t.BodyFont,
		FontColor: ensureContrast(normalizeColor(t.Colors.Text1), bg),
	})
}

func renderImageRightTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string, images [][]byte) {
	addTitleBarTest(slide, sl, t, bg)

	imgW := 4.2
	textW := contentW - imgW - 0.3

	slide.AddText(strOr(sl.Body, ""), pptxgen.TextOptions{
		X: marginX, Y: contentTop,
		Width: textW, Height: slideH - contentTop - 0.6,
		FontSize: 16, FontFace: t.BodyFont,
		FontColor: ensureContrast(normalizeColor(t.Colors.Text1), bg),
	})

	addImageTest(slide, sl, marginX+textW+0.3, contentTop, imgW, slideH-contentTop-0.6, images)
}

func renderImageFullTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string, images [][]byte) {
	addImageTest(slide, sl, 0, 0, slideW, slideH, images)

	if sl.Title != nil && *sl.Title != "" {
		slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
			X: 0, Y: slideH - 1.2, Width: slideW, Height: 1.2,
			Fill: "000000", LineColor: "000000",
			Transparency: 40,
		})
		slide.AddText(*sl.Title, pptxgen.TextOptions{
			X: marginX, Y: slideH - 1.1, Width: contentW, Height: 0.9,
			FontSize: 28, Bold: true, FontFace: t.HeadingFont,
			FontColor: "FFFFFF", Align: pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
		})
	}
}

func addImageTest(slide *pptxgen.Slide, sl LayoutSlide, x, y, w, h float64, images [][]byte) {
	var data []byte
	if sl.ImageRef != nil && *sl.ImageRef != "" {
		idx, err := strconv.Atoi(*sl.ImageRef)
		if err == nil && idx >= 0 && idx < len(images) {
			data = images[idx]
		}
	}
	if data == nil && sl.ImageData != nil && *sl.ImageData != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(*sl.ImageData)
		if err != nil {
			data = nil
		}
	}

	if data == nil {
		slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
			X: x, Y: y, Width: w, Height: h,
			Fill: "E0E0E0", LineColor: "CCCCCC",
		})
		slide.AddText("[Image placeholder]", pptxgen.TextOptions{
			X: x, Y: y, Width: w, Height: h,
			FontSize: 14, FontColor: "999999",
			Align: pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
		})
		return
	}

	imgX, imgY, imgW, imgH := fitImageTest(data, x, y, w, h)
	slide.AddImage(pptxgen.ImageOptions{
		X: imgX, Y: imgY, Width: imgW, Height: imgH,
		Data: data,
	})
}

func fitImageTest(data []byte, x, y, w, h float64) (fx, fy, fw, fh float64) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return x, y, w, h
	}
	imgAspect := float64(cfg.Width) / float64(cfg.Height)
	boxAspect := w / h
	if imgAspect > boxAspect {
		fw = w
		fh = w / imgAspect
		fx = x
		fy = y + (h-fh)/2
	} else {
		fh = h
		fw = h * imgAspect
		fx = x + (w-fw)/2
		fy = y
	}
	return
}

func renderTableTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	addTitleBarTest(slide, sl, t, bg)
	if sl.TableData == nil {
		return
	}

	accent := normalizeColor(t.Colors.Accent1)
	var rows [][]pptxgen.TableCell

	if len(sl.TableData.Headers) > 0 {
		hdrColor := pickTextColor(accent)
		headerCells := make([]pptxgen.TableCell, len(sl.TableData.Headers))
		for i, h := range sl.TableData.Headers {
			headerCells[i] = pptxgen.TableCell{Text: h, Bold: true, FontColor: hdrColor}
		}
		rows = append(rows, headerCells)
	}
	for _, row := range sl.TableData.Rows {
		cells := make([]pptxgen.TableCell, len(row))
		for i, cell := range row {
			cells[i] = pptxgen.TableCell{Text: cell}
		}
		rows = append(rows, cells)
	}

	slide.AddTable(rows, pptxgen.TableOptions{
		X: marginX, Y: contentTop, Width: contentW,
		FontFace: t.BodyFont, FontSize: 14,
		FontColor:    ensureContrast(normalizeColor(t.Colors.Text1), bg),
		Fill:         bg,
		FirstRowBold: true, FirstRowFill: accent,
		Border: pptxgen.Border{Color: normalizeColor(t.Colors.Background2), Width: 1, Style: pptxgen.BorderSolid},
	})
}

func renderChartTest(slide *pptxgen.Slide, sl LayoutSlide, t Theme, bg string) {
	addTitleBarTest(slide, sl, t, bg)
	if sl.ChartData == nil || len(sl.ChartData.Series) == 0 {
		return
	}

	chartType := pptxgen.ChartBar
	switch sl.ChartData.ChartType {
	case "line":
		chartType = pptxgen.ChartLine
	case "pie":
		chartType = pptxgen.ChartPie
	case "doughnut":
		chartType = pptxgen.ChartDoughnut
	case "area":
		chartType = pptxgen.ChartArea
	}

	var series []pptxgen.ChartSeries
	for _, s := range sl.ChartData.Series {
		cs := pptxgen.ChartSeries{Labels: s.Categories, Values: s.Values}
		if s.Name != nil {
			cs.Name = *s.Name
		}
		series = append(series, cs)
	}

	accentColors := []string{
		normalizeColor(t.Colors.Accent1),
		normalizeColor(t.Colors.Accent2),
		normalizeColor(t.Colors.Accent3),
		normalizeColor(t.Colors.Accent4),
		normalizeColor(t.Colors.Accent5),
		normalizeColor(t.Colors.Accent6),
	}

	isDark := isDarkBg(bg)
	chartTop := contentTop
	chartH := slideH - contentTop - 0.6

	if sl.ChartData.Title != nil && *sl.ChartData.Title != "" && isDark {
		slide.AddText(*sl.ChartData.Title, pptxgen.TextOptions{
			X: marginX, Y: contentTop, Width: contentW, Height: 0.5,
			FontSize: 16, Bold: true, FontFace: t.BodyFont,
			FontColor: ensureContrast(normalizeColor(t.Colors.Text1), bg),
			Align:     pptxgen.AlignCenter,
		})
		chartTop = contentTop + 0.5
		chartH -= 0.5
	}

	if isDark {
		slide.AddShape(pptxgen.ShapeRoundRect, pptxgen.ShapeOptions{
			X: marginX, Y: chartTop, Width: contentW, Height: chartH,
			Fill: "FFFFFF", LineColor: normalizeColor(t.Colors.Background2),
		})
	}

	opts := pptxgen.ChartOptions{
		X: marginX, Y: chartTop, Width: contentW, Height: chartH,
		ShowLegend: boolOr(sl.ChartData.ShowLegend, true),
		ShowValues: boolOr(sl.ChartData.ShowValues, false),
		Colors:     accentColors,
	}
	if sl.ChartData.Title != nil && !isDark {
		opts.Title = *sl.ChartData.Title
		opts.ShowTitle = true
	}

	slide.AddChart(chartType, series, opts)
}
