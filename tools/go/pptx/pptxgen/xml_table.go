// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import "strings"

const defaultTableRowHeightEMU = 365760

const defaultTableWidthInches = 8.0

const noStyleNoGridTableStyleID = "{2D5ABB26-0587-4C30-8999-92F81FD0307C}"

type tableElement struct {
	rows [][]TableCell
	opts TableOptions
}

func (t *tableElement) emitXML(b *strings.Builder, ids *idAllocator) {
	rowCount := len(t.rows)
	if rowCount == 0 {
		return
	}
	colCount := 0
	for _, row := range t.rows {
		if len(row) > colCount {
			colCount = len(row)
		}
	}
	if colCount == 0 {
		return
	}

	xEMU := inchToEMU(t.opts.X)
	yEMU := inchToEMU(t.opts.Y)
	width := t.opts.Width
	if width <= 0 {
		width = defaultTableWidthInches
	}
	widthEMU := inchToEMU(width)

	colWidthsEMU := resolveColWidths(t.opts.ColWidths, colCount, widthEMU)
	rowHeightsEMU := resolveRowHeights(t.opts.RowHeights, rowCount)

	totalHeight := 0
	for _, h := range rowHeightsEMU {
		totalHeight += h
	}

	id := ids.Next()

	b.WriteString(`<p:graphicFrame>`)
	b.WriteString(`<p:nvGraphicFramePr>`)
	b.WriteString(`<p:cNvPr id="`)
	b.WriteString(fmtInt(id))
	b.WriteString(`" name="Table `)
	b.WriteString(fmtInt(id))
	b.WriteString(`"/>`)
	b.WriteString(`<p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr>`)
	b.WriteString(`<p:nvPr/>`)
	b.WriteString(`</p:nvGraphicFramePr>`)
	b.WriteString(`<p:xfrm>`)
	b.WriteString(`<a:off x="`)
	b.WriteString(fmtInt(xEMU))
	b.WriteString(`" y="`)
	b.WriteString(fmtInt(yEMU))
	b.WriteString(`"/>`)
	b.WriteString(`<a:ext cx="`)
	b.WriteString(fmtInt(widthEMU))
	b.WriteString(`" cy="`)
	b.WriteString(fmtInt(totalHeight))
	b.WriteString(`"/>`)
	b.WriteString(`</p:xfrm>`)
	b.WriteString(`<a:graphic>`)
	b.WriteString(`<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table">`)
	b.WriteString(`<a:tbl>`)

	b.WriteString(`<a:tblPr`)
	if t.opts.FirstRowBold || t.opts.FirstRowFill != "" {
		b.WriteString(` firstRow="1"`)
	}
	b.WriteString(`>`)
	b.WriteString(`<a:tableStyleId>`)
	b.WriteString(noStyleNoGridTableStyleID)
	b.WriteString(`</a:tableStyleId>`)
	b.WriteString(`</a:tblPr>`)

	b.WriteString(`<a:tblGrid>`)
	for _, w := range colWidthsEMU {
		b.WriteString(`<a:gridCol w="`)
		b.WriteString(fmtInt(w))
		b.WriteString(`"/>`)
	}
	b.WriteString(`</a:tblGrid>`)

	for rowIdx, row := range t.rows {
		b.WriteString(`<a:tr h="`)
		b.WriteString(fmtInt(rowHeightsEMU[rowIdx]))
		b.WriteString(`">`)
		for colIdx := range colCount {
			var cell TableCell
			if colIdx < len(row) {
				cell = row[colIdx]
			}
			emitTableCell(b, cell, &t.opts, rowIdx)
		}
		b.WriteString(`</a:tr>`)
	}

	b.WriteString(`</a:tbl>`)
	b.WriteString(`</a:graphicData>`)
	b.WriteString(`</a:graphic>`)
	b.WriteString(`</p:graphicFrame>`)
}

func resolveColWidths(colWidths []float64, colCount, totalEMU int) []int {
	out := make([]int, colCount)
	if len(colWidths) >= colCount {
		for i := range colCount {
			out[i] = inchToEMU(colWidths[i])
		}
		return out
	}
	per := totalEMU / colCount
	for i := range colCount {
		out[i] = per
	}
	if rem := totalEMU - per*colCount; rem != 0 {
		out[0] += rem
	}
	return out
}

func resolveRowHeights(rowHeights []float64, rowCount int) []int {
	out := make([]int, rowCount)
	for i := range rowCount {
		if i < len(rowHeights) {
			out[i] = inchToEMU(rowHeights[i])
		} else {
			out[i] = defaultTableRowHeightEMU
		}
	}
	return out
}

func emitTableCell(b *strings.Builder, cell TableCell, opts *TableOptions, rowIdx int) {
	isFirstRow := rowIdx == 0

	fontFace := cell.FontFace
	if fontFace == "" {
		fontFace = opts.FontFace
	}
	fontSize := cell.FontSize
	if fontSize == 0 {
		fontSize = opts.FontSize
	}
	fontColor := parseColor(cell.FontColor)
	if fontColor == "" {
		fontColor = parseColor(opts.FontColor)
	}
	bold := cell.Bold || (isFirstRow && opts.FirstRowBold)
	italic := cell.Italic
	cellFill := parseColor(cell.Fill)
	if cellFill == "" && isFirstRow {
		cellFill = parseColor(opts.FirstRowFill)
	}
	if cellFill == "" {
		cellFill = parseColor(opts.Fill)
	}

	b.WriteString(`<a:tc>`)
	b.WriteString(`<a:txBody>`)
	b.WriteString(`<a:bodyPr/>`)
	b.WriteString(`<a:lstStyle/>`)
	b.WriteString(`<a:p>`)
	b.WriteString(`<a:pPr algn="`)
	b.WriteString(alignAttr(cell.Align))
	b.WriteString(`"/>`)
	if cell.Text != "" {
		b.WriteString(`<a:r>`)
		b.WriteString(`<a:rPr lang="en-US"`)
		if fontSize > 0 {
			b.WriteString(` sz="`)
			b.WriteString(fmtInt(int(fontSize*100 + 0.5)))
			b.WriteString(`"`)
		}
		if bold {
			b.WriteString(` b="1"`)
		}
		if italic {
			b.WriteString(` i="1"`)
		}
		b.WriteString(`>`)
		if fontColor != "" {
			b.WriteString(`<a:solidFill><a:srgbClr val="`)
			b.WriteString(fontColor)
			b.WriteString(`"/></a:solidFill>`)
		}
		if fontFace != "" {
			b.WriteString(`<a:latin typeface="`)
			b.WriteString(escapeXML(fontFace))
			b.WriteString(`"/>`)
			b.WriteString(`<a:ea typeface="`)
			b.WriteString(escapeXML(fontFace))
			b.WriteString(`"/>`)
			b.WriteString(`<a:cs typeface="`)
			b.WriteString(escapeXML(fontFace))
			b.WriteString(`"/>`)
		}
		b.WriteString(`</a:rPr>`)
		b.WriteString(`<a:t>`)
		b.WriteString(escapeXML(cell.Text))
		b.WriteString(`</a:t>`)
		b.WriteString(`</a:r>`)
	}
	b.WriteString(`<a:endParaRPr lang="en-US"/>`)
	b.WriteString(`</a:p>`)
	b.WriteString(`</a:txBody>`)

	b.WriteString(`<a:tcPr anchor="`)
	b.WriteString(vAlignAttr(cell.VAlign))
	b.WriteString(`">`)

	if borderColor := parseColor(opts.Border.Color); borderColor != "" && opts.Border.Width > 0 {
		borderWidth := pointToEMU(opts.Border.Width)
		for _, side := range []string{"lnL", "lnR", "lnT", "lnB"} {
			b.WriteString(`<a:`)
			b.WriteString(side)
			b.WriteString(` w="`)
			b.WriteString(fmtInt(borderWidth))
			b.WriteString(`" cap="flat" cmpd="sng" algn="ctr">`)
			b.WriteString(`<a:solidFill><a:srgbClr val="`)
			b.WriteString(borderColor)
			b.WriteString(`"/></a:solidFill>`)
			b.WriteString(`<a:prstDash val="solid"/>`)
			b.WriteString(`<a:round/>`)
			b.WriteString(`<a:headEnd type="none" w="med" len="med"/>`)
			b.WriteString(`<a:tailEnd type="none" w="med" len="med"/>`)
			b.WriteString(`</a:`)
			b.WriteString(side)
			b.WriteString(`>`)
		}
	}

	if cellFill != "" {
		b.WriteString(`<a:solidFill><a:srgbClr val="`)
		b.WriteString(cellFill)
		b.WriteString(`"/></a:solidFill>`)
	}

	b.WriteString(`</a:tcPr>`)
	b.WriteString(`</a:tc>`)
}

func alignAttr(a Align) string {
	switch a {
	case AlignCenter:
		return "ctr"
	case AlignRight:
		return "r"
	default:
		return "l"
	}
}

func vAlignAttr(v VAlign) string {
	switch v {
	case VAlignMiddle:
		return "ctr"
	case VAlignBottom:
		return "b"
	default:
		return "t"
	}
}
