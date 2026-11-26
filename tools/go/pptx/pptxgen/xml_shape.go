// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import "strings"

type shapeElement struct {
	kind ShapeType
	opts ShapeOptions
}

const roundRectAdj = 20000

func (e *shapeElement) emitXML(b *strings.Builder, ids *idAllocator) {
	id := ids.Next()
	idStr := fmtInt(id)

	b.WriteString(`<p:sp>`)
	b.WriteString(`<p:nvSpPr><p:cNvPr id="`)
	b.WriteString(idStr)
	b.WriteString(`" name="Shape `)
	b.WriteString(idStr)
	b.WriteString(`"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr>`)

	b.WriteString(`<p:spPr>`)
	emitXfrm(b, e.opts.X, e.opts.Y, e.opts.Width, e.opts.Height)
	emitShapeGeom(b, e.kind, e.opts)
	emitFillAlpha(b, e.opts.Fill, e.opts.Transparency)
	emitLineAlpha(b, e.opts.LineColor, e.opts.LineWidth, e.opts.Transparency)
	emitShadow(b, e.opts.Shadow)
	b.WriteString(`</p:spPr>`)

	b.WriteString(`</p:sp>`)
}

func emitShapeGeom(b *strings.Builder, kind ShapeType, opts ShapeOptions) {
	switch kind {
	case ShapeRoundRect:
		b.WriteString(`<a:prstGeom prst="roundRect"><a:avLst><a:gd name="adj" fmla="val `)
		b.WriteString(fmtInt(roundRectAdjFor(opts)))
		b.WriteString(`"/></a:avLst></a:prstGeom>`)
	case ShapeEllipse:
		b.WriteString(`<a:prstGeom prst="ellipse"><a:avLst/></a:prstGeom>`)
	default:
		b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)
	}
}

func roundRectAdjFor(opts ShapeOptions) int {
	if opts.CornerRadiusIn <= 0 {
		return roundRectAdj
	}
	smaller := opts.Width
	if opts.Height < smaller {
		smaller = opts.Height
	}
	if smaller <= 0 {
		return roundRectAdj
	}
	adj := int(opts.CornerRadiusIn/smaller*100000 + 0.5)
	if adj > 50000 {
		adj = 50000
	}
	if adj < 0 {
		adj = 0
	}
	return adj
}

func emitShadow(b *strings.Builder, s *Shadow) {
	if s == nil {
		return
	}
	col := parseColor(s.Color)
	if col == "" {
		col = "000000"
	}
	opacity := s.OpacityPct
	if opacity <= 0 {
		opacity = 40
	}
	if opacity > 100 {
		opacity = 100
	}
	b.WriteString(`<a:effectLst><a:outerShdw blurRad="`)
	b.WriteString(fmtInt(pointToEMU(s.BlurPt)))
	b.WriteString(`" dist="`)
	b.WriteString(fmtInt(pointToEMU(s.DistancePt)))
	b.WriteString(`" dir="`)
	b.WriteString(fmtInt(int(s.DirectionDeg*60000 + 0.5)))
	b.WriteString(`" algn="bl" rotWithShape="0"><a:srgbClr val="`)
	b.WriteString(col)
	b.WriteString(`"><a:alpha val="`)
	b.WriteString(fmtInt(int(opacity*1000 + 0.5)))
	b.WriteString(`"/></a:srgbClr></a:outerShdw></a:effectLst>`)
}

func emitFillAlpha(b *strings.Builder, hex string, transparencyPct float64) {
	if transparencyPct <= 0 {
		emitFill(b, hex)
		return
	}
	col := parseColor(hex)
	if col == "" {
		b.WriteString(`<a:noFill/>`)
		return
	}
	b.WriteString(`<a:solidFill><a:srgbClr val="`)
	b.WriteString(col)
	b.WriteString(`">`)
	emitAlpha(b, transparencyPct)
	b.WriteString(`</a:srgbClr></a:solidFill>`)
}

func emitLineAlpha(b *strings.Builder, hexColor string, widthPt, transparencyPct float64) {
	if widthPt <= 0 {
		return
	}
	col := parseColor(hexColor)
	if col == "" {
		return
	}
	b.WriteString(`<a:ln w="`)
	b.WriteString(fmtInt(pointToEMU(widthPt)))
	b.WriteString(`"><a:solidFill><a:srgbClr val="`)
	b.WriteString(col)
	if transparencyPct > 0 {
		b.WriteString(`">`)
		emitAlpha(b, transparencyPct)
		b.WriteString(`</a:srgbClr></a:solidFill></a:ln>`)
		return
	}
	b.WriteString(`"/></a:solidFill></a:ln>`)
}

func emitAlpha(b *strings.Builder, transparencyPct float64) {
	if transparencyPct < 0 {
		transparencyPct = 0
	}
	if transparencyPct > 100 {
		transparencyPct = 100
	}
	alpha := int((100 - transparencyPct) * 1000)
	b.WriteString(`<a:alpha val="`)
	b.WriteString(fmtInt(alpha))
	b.WriteString(`"/>`)
}
