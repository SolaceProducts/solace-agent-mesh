// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"strings"
)

type textElement struct {
	text string
	opts TextOptions
}

func (e *textElement) emitXML(b *strings.Builder, ids *idAllocator) {
	id := ids.Next()
	idStr := fmtInt(id)

	b.WriteString(`<p:sp>`)
	b.WriteString(`<p:nvSpPr><p:cNvPr id="`)
	b.WriteString(idStr)
	b.WriteString(`" name="TextBox `)
	b.WriteString(idStr)
	b.WriteString(`"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`)

	b.WriteString(`<p:spPr>`)
	emitXfrm(b, e.opts.X, e.opts.Y, e.opts.Width, e.opts.Height)
	b.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)
	emitFill(b, e.opts.Fill)
	b.WriteString(`</p:spPr>`)

	b.WriteString(`<p:txBody>`)
	b.WriteString(`<a:bodyPr wrap="square" rtlCol="0" anchor="`)
	b.WriteString(vAlignToken(e.opts.VAlign))
	b.WriteString(`"/>`)
	b.WriteString(`<a:lstStyle/>`)

	for _, line := range splitTextLines(e.text) {
		emitParagraph(b, line, e.opts)
	}

	b.WriteString(`</p:txBody>`)
	b.WriteString(`</p:sp>`)
}

func emitParagraph(b *strings.Builder, text string, opts TextOptions) {
	b.WriteString(`<a:p>`)
	b.WriteString(`<a:pPr algn="`)
	b.WriteString(alignToken(opts.Align))
	b.WriteString(`">`)
	if opts.LineSpacing > 0 {
		b.WriteString(`<a:lnSpc><a:spcPct val="`)
		b.WriteString(fmtInt(int(opts.LineSpacing*100000 + 0.5)))
		b.WriteString(`"/></a:lnSpc>`)
	}
	b.WriteString(`</a:pPr>`)

	if opts.Markdown {
		runs := parseInlineMarkdown(text, opts.HighlightColor != "")
		if len(runs) == 0 {
			emitRun(b, "", opts, opts.Bold, opts.Italic, false, false)
		}
		for _, r := range runs {
			emitRun(b, r.text, opts, opts.Bold || r.bold, opts.Italic || r.italic, r.code, r.highlight)
		}
	} else {
		emitRun(b, text, opts, opts.Bold, opts.Italic, false, false)
	}

	b.WriteString(`<a:endParaRPr lang="en-US"/>`)
	b.WriteString(`</a:p>`)
}

func emitRun(b *strings.Builder, text string, opts TextOptions, bold, italic, code, highlight bool) {
	b.WriteString(`<a:r>`)
	b.WriteString(`<a:rPr lang="en-US"`)
	if opts.FontSize > 0 {
		b.WriteString(` sz="`)
		b.WriteString(fmtInt(int(opts.FontSize*100 + 0.5)))
		b.WriteString(`"`)
	}
	if bold {
		b.WriteString(` b="1"`)
	}
	if italic {
		b.WriteString(` i="1"`)
	}
	b.WriteString(`>`)
	colorHex := opts.FontColor
	if highlight && opts.HighlightColor != "" {
		colorHex = opts.HighlightColor
	}
	if col := parseColor(colorHex); col != "" {
		b.WriteString(`<a:solidFill><a:srgbClr val="`)
		b.WriteString(col)
		b.WriteString(`"/></a:solidFill>`)
	}
	face := opts.FontFace
	if code {
		face = monospaceFont
	}
	if face != "" {
		face = escapeXML(face)
		b.WriteString(`<a:latin typeface="`)
		b.WriteString(face)
		b.WriteString(`"/>`)
		b.WriteString(`<a:ea typeface="`)
		b.WriteString(face)
		b.WriteString(`"/>`)
		b.WriteString(`<a:cs typeface="`)
		b.WriteString(face)
		b.WriteString(`"/>`)
	}
	b.WriteString(`</a:rPr>`)
	b.WriteString(`<a:t>`)
	b.WriteString(escapeXML(text))
	b.WriteString(`</a:t>`)
	b.WriteString(`</a:r>`)
}

func emitXfrm(b *strings.Builder, xIn, yIn, wIn, hIn float64) {
	b.WriteString(`<a:xfrm><a:off x="`)
	b.WriteString(fmtInt(inchToEMU(xIn)))
	b.WriteString(`" y="`)
	b.WriteString(fmtInt(inchToEMU(yIn)))
	b.WriteString(`"/><a:ext cx="`)
	b.WriteString(fmtInt(inchToEMU(wIn)))
	b.WriteString(`" cy="`)
	b.WriteString(fmtInt(inchToEMU(hIn)))
	b.WriteString(`"/></a:xfrm>`)
}

func emitFill(b *strings.Builder, hex string) {
	col := parseColor(hex)
	if col == "" {
		b.WriteString(`<a:noFill/>`)
		return
	}
	b.WriteString(`<a:solidFill><a:srgbClr val="`)
	b.WriteString(col)
	b.WriteString(`"/></a:solidFill>`)
}

func alignToken(a Align) string {
	switch a {
	case AlignCenter:
		return "ctr"
	case AlignRight:
		return "r"
	default:
		return "l"
	}
}

func vAlignToken(v VAlign) string {
	switch v {
	case VAlignMiddle:
		return "ctr"
	case VAlignBottom:
		return "b"
	default:
		return "t"
	}
}

func splitTextLines(text string) []string {
	if text == "" {
		return []string{""}
	}
	parts := strings.Split(text, "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}
