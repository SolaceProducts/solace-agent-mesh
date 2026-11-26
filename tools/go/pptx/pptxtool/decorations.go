// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

func renderDecorations(slide *pptxgen.Slide, rc *RenderContext) {
	if rc.Profile == nil || rc.Category == nil {
		return
	}

	for _, deco := range rc.Category.Decorations {
		asset, ok := rc.Profile.Assets[deco.Asset]
		if !ok || len(asset.Data) == 0 {
			continue
		}

		slide.AddImage(pptxgen.ImageOptions{
			X: deco.X, Y: deco.Y, Width: deco.Width, Height: deco.Height,
			Data: asset.Data,
		})
	}
}

func renderFooter(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext, slideNum int) {
	if rc.Profile != nil && rc.Profile.Footer.Enabled {
		renderProfileFooter(slide, sl, rc, slideNum)
		return
	}

	if sl.Footer != nil && *sl.Footer != "" {
		mdText(slide, *sl.Footer, pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.FooterY, Width: rc.ContentW, Height: 0.4,
			FontSize: 10, FontFace: rc.Theme.BodyFont,
			FontColor: rc.SubtitleColor(),
			Align:     pptxgen.AlignCenter,
		})
	}
}

func renderProfileFooter(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext, slideNum int) {
	f := &rc.Profile.Footer
	isDark := relativeLuminance(rc.BgColor) < 0.2

	footerColor := normalizeColor(f.ColorOnLight)
	if isDark {
		footerColor = normalizeColor(f.ColorOnDark)
	}
	if footerColor == "" {
		footerColor = rc.SubtitleColor()
	}

	fontSize := f.FontSize
	if fontSize == 0 {
		fontSize = 6
	}

	if f.ShowSlideNumber && f.SlideNumberPosition != nil {
		pos := f.SlideNumberPosition
		numText := fmt.Sprintf("%d", slideNum)

		if f.SlideNumberStyle == "circle" {
			accent := rc.AccentColor()
			slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
				X: pos.X, Y: pos.Y, Width: pos.Width, Height: pos.Height,
				Fill: accent, LineColor: accent, LineWidth: 0,
			})
			numColor := rc.Theme.ensureContrast("FFFFFF", accent)
			slide.AddText(numText, pptxgen.TextOptions{
				X: pos.X, Y: pos.Y, Width: pos.Width, Height: pos.Height,
				FontSize: 8, FontFace: rc.Theme.BodyFont,
				FontColor: numColor,
				Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
			})
		} else {
			slide.AddText(numText, pptxgen.TextOptions{
				X: pos.X, Y: pos.Y, Width: pos.Width, Height: pos.Height,
				FontSize: fontSize, FontFace: rc.Theme.BodyFont,
				FontColor: footerColor,
				Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
			})
		}
	}

	if f.ShowFooterText && f.FooterTextPosition != nil {
		footerText := ""
		if sl.Footer != nil {
			footerText = *sl.Footer
		}
		if footerText != "" {
			pos := f.FooterTextPosition
			mdText(slide, footerText, pptxgen.TextOptions{
				X: pos.X, Y: pos.Y, Width: pos.Width, Height: pos.Height,
				FontSize: fontSize, FontFace: rc.Theme.BodyFont,
				FontColor: footerColor,
				VAlign:    pptxgen.VAlignMiddle,
			})
		}
	}
}
