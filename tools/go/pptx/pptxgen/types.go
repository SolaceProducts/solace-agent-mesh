// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

type TextOptions struct {
	X, Y, Width, Height float64
	FontFace            string
	FontSize            float64
	FontColor           string
	Bold, Italic        bool
	Align               Align
	VAlign              VAlign
	LineSpacing         float64
	Fill                string
	Markdown            bool
	HighlightColor      string
}

type ShapeOptions struct {
	X, Y, Width, Height float64
	Fill                string
	LineColor           string
	LineWidth           float64
	Transparency        float64
	Shadow              *Shadow
	CornerRadiusIn      float64
}

type Shadow struct {
	Color        string
	BlurPt       float64
	DistancePt   float64
	DirectionDeg float64
	OpacityPct   float64
}

type ImageOptions struct {
	X, Y, Width, Height float64
	Data                []byte
	Path                string
	AltText             string
}

type TableOptions struct {
	X, Y, Width  float64
	RowHeights   []float64
	ColWidths    []float64
	FontFace     string
	FontSize     float64
	FontColor    string
	Fill         string
	Border       Border
	FirstRowBold bool
	FirstRowFill string
}

type TableCell struct {
	Text      string
	FontFace  string
	FontSize  float64
	FontColor string
	Bold      bool
	Italic    bool
	Fill      string
	Align     Align
	VAlign    VAlign
}

type Border struct {
	Color string
	Width float64
	Style BorderStyle
}

type BackgroundOptions struct {
	Color string
}

type ChartOptions struct {
	X, Y, Width, Height float64
	Title               string
	ShowTitle           bool
	ShowLegend          bool
	ShowValues          bool
	Colors              []string
}

type ChartSeries struct {
	Name   string
	Labels []string
	Values []float64
}

type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

type VAlign int

const (
	VAlignTop VAlign = iota
	VAlignMiddle
	VAlignBottom
)

type ShapeType int

const (
	ShapeRect ShapeType = iota
	ShapeRoundRect
	ShapeEllipse
)

type ChartType int

const (
	ChartBar ChartType = iota
	ChartLine
	ChartPie
	ChartDoughnut
	ChartArea
)

type BorderStyle int

const (
	BorderSolid BorderStyle = iota
)
