// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"fmt"
	"strconv"
	"strings"
)

var defaultChartColors = []string{
	"4472C4", "ED7D31", "A5A5A5", "FFC000",
	"5B9BD5", "70AD47", "264478", "9E480E",
}

const (
	chartTitleFontSize     = 1800
	chartLegendFontSize    = 1200
	chartDataLabelFontSize = 1200
)

func emitChartTextProps(b *strings.Builder, sizeHundredthsPt int) {
	b.WriteString(`<c:txPr><a:bodyPr/><a:lstStyle/><a:p><a:pPr><a:defRPr sz="`)
	b.WriteString(fmtInt(sizeHundredthsPt))
	b.WriteString(`"/></a:pPr><a:endParaRPr lang="en-US"/></a:p></c:txPr>`)
}

type chartElement struct {
	kind     ChartType
	series   []ChartSeries
	opts     ChartOptions
	chartIdx int
	rID      string
}

func (c *chartElement) emitXML(b *strings.Builder, ids *idAllocator) {
	cnvprID := ids.Next()
	x := inchToEMU(c.opts.X)
	y := inchToEMU(c.opts.Y)
	cx := inchToEMU(c.opts.Width)
	cy := inchToEMU(c.opts.Height)

	b.WriteString(`<p:graphicFrame>`)
	b.WriteString(`<p:nvGraphicFramePr>`)
	b.WriteString(`<p:cNvPr id="`)
	b.WriteString(fmtInt(cnvprID))
	b.WriteString(`" name="Chart `)
	b.WriteString(fmtInt(c.chartIdx))
	b.WriteString(`"/>`)
	b.WriteString(`<p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr>`)
	b.WriteString(`<p:nvPr/>`)
	b.WriteString(`</p:nvGraphicFramePr>`)
	b.WriteString(`<p:xfrm>`)
	b.WriteString(`<a:off x="`)
	b.WriteString(fmtInt(x))
	b.WriteString(`" y="`)
	b.WriteString(fmtInt(y))
	b.WriteString(`"/>`)
	b.WriteString(`<a:ext cx="`)
	b.WriteString(fmtInt(cx))
	b.WriteString(`" cy="`)
	b.WriteString(fmtInt(cy))
	b.WriteString(`"/>`)
	b.WriteString(`</p:xfrm>`)
	b.WriteString(`<a:graphic>`)
	b.WriteString(`<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart">`)
	b.WriteString(`<c:chart xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" r:id="`)
	b.WriteString(escapeXML(c.rID))
	b.WriteString(`"/>`)
	b.WriteString(`</a:graphicData>`)
	b.WriteString(`</a:graphic>`)
	b.WriteString(`</p:graphicFrame>`)
}

func emitChartPart(b *strings.Builder, c *chartElement) {
	writeXMLDecl(b)
	b.WriteString(`<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<c:roundedCorners val="0"/>`)
	b.WriteString(`<c:chart>`)

	emitChartTitle(b, c.opts)

	b.WriteString(`<c:plotArea>`)
	b.WriteString(`<c:layout/>`)
	switch c.kind {
	case ChartBar:
		emitBarChart(b, c)
		emitChartAxes(b)
	case ChartLine:
		emitLineChart(b, c)
		emitChartAxes(b)
	case ChartArea:
		emitAreaChart(b, c)
		emitChartAxes(b)
	case ChartPie:
		emitPieChart(b, c, false)
	case ChartDoughnut:
		emitPieChart(b, c, true)
	default:
		panic(fmt.Sprintf("pptxgen: unknown ChartType %d", c.kind))
	}
	b.WriteString(`</c:plotArea>`)

	if c.opts.ShowLegend {
		legendPos := "r"
		if c.kind == ChartPie || c.kind == ChartDoughnut {
			legendPos = "b"
		}
		b.WriteString(`<c:legend>`)
		b.WriteString(`<c:legendPos val="`)
		b.WriteString(legendPos)
		b.WriteString(`"/>`)
		b.WriteString(`<c:overlay val="0"/>`)
		emitChartTextProps(b, chartLegendFontSize)
		b.WriteString(`</c:legend>`)
	}

	b.WriteString(`<c:plotVisOnly val="1"/>`)
	b.WriteString(`<c:dispBlanksAs val="gap"/>`)
	b.WriteString(`</c:chart>`)
	b.WriteString(`</c:chartSpace>`)
}

func emitChartTitle(b *strings.Builder, opts ChartOptions) {
	if opts.ShowTitle && opts.Title != "" {
		b.WriteString(`<c:title>`)
		b.WriteString(`<c:tx><c:rich>`)
		b.WriteString(`<a:bodyPr rot="0" spcFirstLastPara="1" vertOverflow="ellipsis" wrap="square" anchor="ctr" anchorCtr="1"/>`)
		b.WriteString(`<a:lstStyle/>`)
		b.WriteString(`<a:p><a:pPr><a:defRPr sz="`)
		b.WriteString(fmtInt(chartTitleFontSize))
		b.WriteString(`" b="0"/></a:pPr><a:r><a:rPr lang="en-US" sz="`)
		b.WriteString(fmtInt(chartTitleFontSize))
		b.WriteString(`" b="1"/><a:t>`)
		b.WriteString(escapeXML(opts.Title))
		b.WriteString(`</a:t></a:r></a:p>`)
		b.WriteString(`</c:rich></c:tx>`)
		b.WriteString(`<c:overlay val="0"/>`)
		b.WriteString(`</c:title>`)
		b.WriteString(`<c:autoTitleDeleted val="0"/>`)
		return
	}
	b.WriteString(`<c:autoTitleDeleted val="1"/>`)
}

func emitChartAxes(b *strings.Builder) {
	b.WriteString(`<c:catAx>`)
	b.WriteString(`<c:axId val="1"/>`)
	b.WriteString(`<c:scaling><c:orientation val="minMax"/></c:scaling>`)
	b.WriteString(`<c:delete val="0"/>`)
	b.WriteString(`<c:axPos val="b"/>`)
	b.WriteString(`<c:majorTickMark val="out"/>`)
	b.WriteString(`<c:minorTickMark val="none"/>`)
	b.WriteString(`<c:tickLblPos val="nextTo"/>`)
	b.WriteString(`<c:crossAx val="2"/>`)
	b.WriteString(`<c:crosses val="autoZero"/>`)
	b.WriteString(`<c:auto val="1"/>`)
	b.WriteString(`<c:lblAlgn val="ctr"/>`)
	b.WriteString(`<c:lblOffset val="100"/>`)
	b.WriteString(`</c:catAx>`)
	b.WriteString(`<c:valAx>`)
	b.WriteString(`<c:axId val="2"/>`)
	b.WriteString(`<c:scaling><c:orientation val="minMax"/></c:scaling>`)
	b.WriteString(`<c:delete val="0"/>`)
	b.WriteString(`<c:axPos val="l"/>`)
	b.WriteString(`<c:majorGridlines/>`)
	b.WriteString(`<c:numFmt formatCode="General" sourceLinked="0"/>`)
	b.WriteString(`<c:majorTickMark val="out"/>`)
	b.WriteString(`<c:minorTickMark val="none"/>`)
	b.WriteString(`<c:tickLblPos val="nextTo"/>`)
	b.WriteString(`<c:crossAx val="1"/>`)
	b.WriteString(`<c:crosses val="autoZero"/>`)
	b.WriteString(`<c:crossBetween val="between"/>`)
	b.WriteString(`</c:valAx>`)
}

func emitBarChart(b *strings.Builder, c *chartElement) {
	b.WriteString(`<c:barChart>`)
	b.WriteString(`<c:barDir val="col"/>`)
	b.WriteString(`<c:grouping val="clustered"/>`)
	b.WriteString(`<c:varyColors val="0"/>`)
	for i, s := range c.series {
		emitChartSeries(b, c, i, s, chartKindBar)
	}
	if c.opts.ShowValues {
		emitDataLabels(b)
	}
	b.WriteString(`<c:gapWidth val="150"/>`)
	b.WriteString(`<c:axId val="1"/>`)
	b.WriteString(`<c:axId val="2"/>`)
	b.WriteString(`</c:barChart>`)
}

func emitLineChart(b *strings.Builder, c *chartElement) {
	b.WriteString(`<c:lineChart>`)
	b.WriteString(`<c:grouping val="standard"/>`)
	b.WriteString(`<c:varyColors val="0"/>`)
	for i, s := range c.series {
		emitChartSeries(b, c, i, s, chartKindLine)
	}
	if c.opts.ShowValues {
		emitDataLabels(b)
	}
	b.WriteString(`<c:marker val="1"/>`)
	b.WriteString(`<c:axId val="1"/>`)
	b.WriteString(`<c:axId val="2"/>`)
	b.WriteString(`</c:lineChart>`)
}

func emitAreaChart(b *strings.Builder, c *chartElement) {
	b.WriteString(`<c:areaChart>`)
	b.WriteString(`<c:grouping val="standard"/>`)
	b.WriteString(`<c:varyColors val="0"/>`)
	for i, s := range c.series {
		emitChartSeries(b, c, i, s, chartKindArea)
	}
	if c.opts.ShowValues {
		emitDataLabels(b)
	}
	b.WriteString(`<c:axId val="1"/>`)
	b.WriteString(`<c:axId val="2"/>`)
	b.WriteString(`</c:areaChart>`)
}

func emitPieChart(b *strings.Builder, c *chartElement, doughnut bool) {
	if doughnut {
		b.WriteString(`<c:doughnutChart>`)
	} else {
		b.WriteString(`<c:pieChart>`)
	}
	b.WriteString(`<c:varyColors val="1"/>`)

	if len(c.series) > 0 {
		emitPieSeries(b, c, c.series[0])
	}

	if c.opts.ShowValues {
		emitPieDataLabels(b)
	}
	b.WriteString(`<c:firstSliceAng val="0"/>`)
	if doughnut {
		b.WriteString(`<c:holeSize val="50"/>`)
		b.WriteString(`</c:doughnutChart>`)
	} else {
		b.WriteString(`</c:pieChart>`)
	}
}

func emitPieSeries(b *strings.Builder, c *chartElement, s ChartSeries) {
	b.WriteString(`<c:ser>`)
	b.WriteString(`<c:idx val="0"/>`)
	b.WriteString(`<c:order val="0"/>`)
	if s.Name != "" {
		b.WriteString(`<c:tx><c:v>`)
		b.WriteString(escapeXML(s.Name))
		b.WriteString(`</c:v></c:tx>`)
	}

	for i := range len(s.Values) {
		color := seriesColor(c, i)
		if color == "" {
			continue
		}
		emitDPt(b, i, color)
	}

	if len(s.Labels) > 0 {
		b.WriteString(`<c:cat>`)
		emitStrLit(b, s.Labels)
		b.WriteString(`</c:cat>`)
	}
	if len(s.Values) > 0 {
		b.WriteString(`<c:val>`)
		emitNumLit(b, s.Values)
		b.WriteString(`</c:val>`)
	}
	b.WriteString(`</c:ser>`)
}

type chartSeriesKind int

const (
	chartKindBar chartSeriesKind = iota
	chartKindLine
	chartKindArea
)

func emitChartSeries(b *strings.Builder, c *chartElement, idx int, s ChartSeries, kind chartSeriesKind) {
	color := seriesColor(c, idx)

	b.WriteString(`<c:ser>`)
	b.WriteString(`<c:idx val="`)
	b.WriteString(fmtInt(idx))
	b.WriteString(`"/>`)
	b.WriteString(`<c:order val="`)
	b.WriteString(fmtInt(idx))
	b.WriteString(`"/>`)
	if s.Name != "" {
		b.WriteString(`<c:tx><c:v>`)
		b.WriteString(escapeXML(s.Name))
		b.WriteString(`</c:v></c:tx>`)
	}
	if color != "" {
		b.WriteString(`<c:spPr>`)
		b.WriteString(`<a:solidFill><a:srgbClr val="`)
		b.WriteString(color)
		b.WriteString(`"/></a:solidFill>`)
		if kind == chartKindLine {
			b.WriteString(`<a:ln w="28575"><a:solidFill><a:srgbClr val="`)
			b.WriteString(color)
			b.WriteString(`"/></a:solidFill></a:ln>`)
		}
		b.WriteString(`</c:spPr>`)
	}
	if kind == chartKindLine {
		b.WriteString(`<c:marker><c:symbol val="circle"/><c:size val="5"/>`)
		if color != "" {
			b.WriteString(`<c:spPr><a:solidFill><a:srgbClr val="`)
			b.WriteString(color)
			b.WriteString(`"/></a:solidFill></c:spPr>`)
		}
		b.WriteString(`</c:marker>`)
	}

	if len(s.Labels) > 0 {
		b.WriteString(`<c:cat>`)
		emitStrLit(b, s.Labels)
		b.WriteString(`</c:cat>`)
	}
	if len(s.Values) > 0 {
		b.WriteString(`<c:val>`)
		emitNumLit(b, s.Values)
		b.WriteString(`</c:val>`)
	}
	if kind == chartKindLine || kind == chartKindArea {
		b.WriteString(`<c:smooth val="0"/>`)
	}
	b.WriteString(`</c:ser>`)
}

func emitStrLit(b *strings.Builder, labels []string) {
	b.WriteString(`<c:strLit>`)
	b.WriteString(`<c:ptCount val="`)
	b.WriteString(fmtInt(len(labels)))
	b.WriteString(`"/>`)
	for i, lbl := range labels {
		b.WriteString(`<c:pt idx="`)
		b.WriteString(fmtInt(i))
		b.WriteString(`"><c:v>`)
		b.WriteString(escapeXML(lbl))
		b.WriteString(`</c:v></c:pt>`)
	}
	b.WriteString(`</c:strLit>`)
}

func emitNumLit(b *strings.Builder, values []float64) {
	b.WriteString(`<c:numLit>`)
	b.WriteString(`<c:ptCount val="`)
	b.WriteString(fmtInt(len(values)))
	b.WriteString(`"/>`)
	for i, v := range values {
		b.WriteString(`<c:pt idx="`)
		b.WriteString(fmtInt(i))
		b.WriteString(`"><c:v>`)
		b.WriteString(formatChartFloat(v))
		b.WriteString(`</c:v></c:pt>`)
	}
	b.WriteString(`</c:numLit>`)
}

func emitDataLabels(b *strings.Builder) {
	b.WriteString(`<c:dLbls>`)
	emitChartTextProps(b, chartDataLabelFontSize)
	b.WriteString(`<c:showLegendKey val="0"/>`)
	b.WriteString(`<c:showVal val="1"/>`)
	b.WriteString(`<c:showCatName val="0"/>`)
	b.WriteString(`<c:showSerName val="0"/>`)
	b.WriteString(`<c:showPercent val="0"/>`)
	b.WriteString(`<c:showBubbleSize val="0"/>`)
	b.WriteString(`</c:dLbls>`)
}

func emitPieDataLabels(b *strings.Builder) {
	b.WriteString(`<c:dLbls>`)
	emitChartTextProps(b, chartDataLabelFontSize)
	b.WriteString(`<c:showLegendKey val="0"/>`)
	b.WriteString(`<c:showVal val="0"/>`)
	b.WriteString(`<c:showCatName val="1"/>`)
	b.WriteString(`<c:showSerName val="0"/>`)
	b.WriteString(`<c:showPercent val="1"/>`)
	b.WriteString(`<c:showBubbleSize val="0"/>`)
	b.WriteString(`</c:dLbls>`)
}

func emitDPt(b *strings.Builder, idx int, color string) {
	b.WriteString(`<c:dPt>`)
	b.WriteString(`<c:idx val="`)
	b.WriteString(fmtInt(idx))
	b.WriteString(`"/>`)
	b.WriteString(`<c:bubble3D val="0"/>`)
	if color != "" {
		b.WriteString(`<c:spPr>`)
		b.WriteString(`<a:solidFill><a:srgbClr val="`)
		b.WriteString(color)
		b.WriteString(`"/></a:solidFill>`)
		b.WriteString(`</c:spPr>`)
	}
	b.WriteString(`</c:dPt>`)
}

func seriesColor(c *chartElement, idx int) string {
	palette := c.opts.Colors
	if len(palette) == 0 {
		palette = defaultChartColors
	}
	if idx >= len(palette) {
		return ""
	}
	return parseColor(palette[idx])
}

func formatChartFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
