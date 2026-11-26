// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

type cardRowOptions struct {
	arrows bool
}

func renderStyledCardRow(slide *pptxgen.Slide, items []GridItem, rc *RenderContext, o cardRowOptions) {
	t := &rc.Theme
	n := len(items)
	if n == 0 {
		return
	}

	gap := 0.27
	if o.arrows {
		gap = 0.45
	}
	cellW := (rc.ContentW - gap*float64(n-1)) / float64(n)

	top := rc.ContentTop + 0.45
	cellH := rc.FooterY - top - 0.15
	if maxH := rc.SlideH * 0.64; cellH > maxH {
		cellH = maxH
	}

	medallions := rc.medallionPalette()
	cardFill := rc.cardFill()
	shadow := rc.cardShadow()
	borderColor, borderWidth := rc.cardBorder()
	labelColor := rc.KickerColor()
	titleColor := rc.ensureCardText(t.Colors.Text1, cardFill)
	descColor := rc.ensureCardText(t.Colors.Text2, cardFill)

	const pad = 0.22
	const medD = 0.72
	const glyphD = 0.36

	for i, item := range items {
		cx := rc.MarginX + float64(i)*(cellW+gap)

		cardLine := cardFill
		if borderColor != "" {
			cardLine = borderColor
		}
		rc.addCard(slide, cx, top, cellW, cellH, cardFill, cardLine, borderWidth, shadow)

		y := top + pad
		if item.Label != nil && *item.Label != "" {
			slide.AddText(strings.ToUpper(*item.Label), pptxgen.TextOptions{
				X: cx + pad, Y: y, Width: cellW - 2*pad, Height: 0.28,
				FontSize: 10, Bold: true, FontFace: rc.LabelFont(),
				FontColor: labelColor, VAlign: pptxgen.VAlignMiddle,
			})
			y += 0.4
		}

		medColor := medallions[i%len(medallions)]
		slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
			X: cx + pad, Y: y, Width: medD, Height: medD,
			Fill: medColor, LineColor: medColor,
		})
		rc.fillMedallionContent(slide, item, cx+pad, y, medD, glyphD, medColor)
		y += medD + 0.22

		mdText(slide, item.Title, pptxgen.TextOptions{
			X: cx + pad, Y: y, Width: cellW - 2*pad, Height: 0.66,
			FontSize: 15, Bold: true, FontFace: t.HeadingFont, FontColor: titleColor,
		})
		y += 0.7

		if item.Pill != nil && *item.Pill != "" {
			pillW := 0.14*float64(len([]rune(*item.Pill))) + 0.4
			if pillW > cellW-2*pad {
				pillW = cellW - 2*pad
			}
			accent := normalizeColor(t.Colors.Accent1)
			slide.AddShape(pptxgen.ShapeRoundRect, pptxgen.ShapeOptions{
				X: cx + pad, Y: y, Width: pillW, Height: 0.32,
				Fill: accent, LineColor: accent, CornerRadiusIn: 0.5,
			})
			slide.AddText(strings.ToUpper(*item.Pill), pptxgen.TextOptions{
				X: cx + pad, Y: y, Width: pillW, Height: 0.32,
				FontSize: 9, Bold: true, FontFace: rc.LabelFont(),
				FontColor: rc.Theme.ensureContrast("FFFFFF", accent),
				Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
			})
			y += 0.46
		}

		if item.Description != nil && *item.Description != "" {
			mdText(slide, *item.Description, pptxgen.TextOptions{
				X: cx + pad, Y: y, Width: cellW - 2*pad, Height: top + cellH - y - pad,
				FontSize: 12.5, FontFace: t.BodyFont, FontColor: descColor, LineSpacing: 1.2,
			})
		}

		if o.arrows && i < n-1 {
			slide.AddText("→", pptxgen.TextOptions{
				X: cx + cellW, Y: top, Width: gap, Height: cellH,
				FontSize: 26, Bold: true, FontFace: t.HeadingFont,
				FontColor: normalizeColor(t.Colors.Accent1),
				Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
			})
		}
	}
}

func renderCallout(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	hasTitle := sl.Title != nil && *sl.Title != "" && sl.Kicker != nil
	if hasTitle {
		addTitleBar(slide, sl, rc)
	}

	panelH := 1.7
	panelY := rc.ContentTop + (rc.FooterY-rc.ContentTop-panelH)/2
	if hasTitle {
		panelY = rc.ContentTop + 0.6
	}
	navy := normalizeColor(t.Colors.Text1)
	rc.addCard(slide, rc.MarginX, panelY, rc.ContentW, panelH, navy, navy, 0, rc.cardShadow())

	textX := rc.MarginX + 0.5
	if sl.Icon != nil {
		if glyph := lookupIcon(*sl.Icon); glyph != nil {
			accent := normalizeColor(t.Colors.Accent1)
			medD := 0.9
			slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
				X: rc.MarginX + 0.45, Y: panelY + (panelH-medD)/2, Width: medD, Height: medD,
				Fill: accent, LineColor: accent,
			})
			gd := 0.46
			slide.AddImage(pptxgen.ImageOptions{
				X: rc.MarginX + 0.45 + (medD-gd)/2, Y: panelY + (panelH-gd)/2,
				Width: gd, Height: gd, Data: glyph,
			})
			textX = rc.MarginX + 0.45 + medD + 0.4
		}
	}

	lead := ""
	if sl.Title != nil && !hasTitle {
		lead = *sl.Title
	}
	body := strOr(sl.Body, "")
	combined := body
	if lead != "" {
		combined = "==" + lead + "==  " + body
	}
	mdText(slide, combined, pptxgen.TextOptions{
		X: textX, Y: panelY + 0.2, Width: rc.MarginX + rc.ContentW - textX - 0.4, Height: panelH - 0.4,
		FontSize: 18, FontFace: t.HeadingFont, FontColor: "FFFFFF",
		VAlign: pptxgen.VAlignMiddle, Markdown: true, HighlightColor: normalizeColor(t.Colors.Accent2),
	})
}
