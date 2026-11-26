// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

func (rc *RenderContext) Styled() bool {
	return rc.Profile != nil
}

type StyleOverride struct {
	CardStyle   *string `json:"card_style" desc:"Card surface treatment: 'elevated' (soft shadow), 'flat' (no shadow, hairline border), 'outlined' (no shadow, defined border), or 'filled' (tinted fill, no border, no shadow). Omit to inherit the template. Corner shape is separate — see card_corners."`
	CardCorners *string `json:"card_corners" desc:"Card corner shape: 'rounded' or 'square'. Independent of card_style — combine them (e.g. rounded + elevated). Omit to inherit the template."`
}

const (
	cardPresetElevated = "elevated"
	cardPresetFlat     = "flat"
	cardPresetOutlined = "outlined"
	cardPresetFilled   = "filled"
)

const (
	cardCornersRounded = "rounded"
	cardCornersSquare  = "square"
)

const roundedCardRadiusIn = 0.15

var (
	validCardPresets = []string{cardPresetElevated, cardPresetFlat, cardPresetOutlined, cardPresetFilled}
	validCardCorners = []string{cardCornersRounded, cardCornersSquare}
)

func isValidCardPreset(name string) bool {
	return slices.Contains(validCardPresets, name)
}

func isValidCardCorners(name string) bool {
	return slices.Contains(validCardCorners, name)
}

func (rc *RenderContext) LabelFont() string {
	if rc.Theme.LabelFont != "" {
		return rc.Theme.LabelFont
	}
	return rc.Theme.BodyFont
}

func (rc *RenderContext) KickerColor() string {
	if rc.Category != nil && rc.Category.KickerColor != "" {
		return normalizeColor(rc.Category.KickerColor)
	}
	return normalizeColor(rc.Theme.Colors.Accent1)
}

func (rc *RenderContext) fillMedallionContent(slide *pptxgen.Slide, item GridItem, medX, medY, medD, glyphD float64, medColor string) {
	if glyph := resolveGlyph(item); glyph != nil {
		slide.AddImage(pptxgen.ImageOptions{
			X: medX + (medD-glyphD)/2, Y: medY + (medD-glyphD)/2,
			Width: glyphD, Height: glyphD, Data: glyph,
		})
		return
	}
	mono := monogram(item.Title)
	if mono == "" {
		return
	}
	slide.AddText(mono, pptxgen.TextOptions{
		X: medX, Y: medY, Width: medD, Height: medD,
		FontSize: medD * 26, Bold: true, FontFace: rc.Theme.HeadingFont,
		FontColor: rc.Theme.ensureContrast(normalizeColor(rc.Theme.Colors.Text1), medColor),
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})
}

func (rc *RenderContext) medallionPalette() []string {
	if relativeLuminance(rc.BgColor) < 0.2 {
		return []string{normalizeColor(rc.Theme.Colors.Accent1)}
	}
	return []string{
		normalizeColor(rc.Theme.Colors.Accent4),
		normalizeColor(rc.Theme.Colors.Accent5),
		normalizeColor(rc.Theme.Colors.Accent6),
	}
}

func (rc *RenderContext) cardFill() string {
	if relativeLuminance(rc.BgColor) < 0.2 {
		return darken(rc.BgColor, -0.5)
	}
	if rc.CardPreset == cardPresetFilled {
		return normalizeColor(rc.Theme.Colors.Background2)
	}
	return normalizeColor(rc.Theme.Colors.Background1)
}

const defaultCardRadiusIn = 0.15

func (rc *RenderContext) cardShadow() *pptxgen.Shadow {
	switch rc.CardPreset {
	case cardPresetFlat, cardPresetOutlined, cardPresetFilled:
		return nil
	case cardPresetElevated:
	default:
		if rc.Profile != nil && rc.Profile.CardStyle.Shadow != nil && !*rc.Profile.CardStyle.Shadow {
			return nil
		}
	}
	col := normalizeColor(rc.Theme.Colors.Text1)
	if col == "" {
		col = "0A1622"
	}
	return &pptxgen.Shadow{Color: col, BlurPt: 5, DistancePt: 2, DirectionDeg: 90, OpacityPct: 11}
}

func (rc *RenderContext) cardRadius() float64 {
	switch rc.CardCorners {
	case cardCornersRounded:
		return roundedCardRadiusIn
	case cardCornersSquare:
		return 0
	}
	if rc.Profile != nil && rc.Profile.CardStyle.CornerRadiusIn != nil {
		return *rc.Profile.CardStyle.CornerRadiusIn
	}
	return defaultCardRadiusIn
}

func (rc *RenderContext) addCard(slide *pptxgen.Slide, x, y, w, h float64, fill, line string, lineW float64, shadow *pptxgen.Shadow) {
	r := rc.cardRadius()
	kind := pptxgen.ShapeRoundRect
	if r <= 0 {
		kind = pptxgen.ShapeRect
	}
	slide.AddShape(kind, pptxgen.ShapeOptions{
		X: x, Y: y, Width: w, Height: h,
		Fill: fill, LineColor: line, LineWidth: lineW,
		CornerRadiusIn: r, Shadow: shadow,
	})
}

func (rc *RenderContext) cardBorder() (color string, widthPt float64) {
	if rc.CardPreset == cardPresetOutlined {
		if relativeLuminance(rc.BgColor) < 0.2 {
			return "5B6B7A", 1.0
		}
		return "B9C0C8", 1.0
	}
	if rc.CardPreset == cardPresetFilled {
		return "", 0
	}
	if rc.Profile != nil && rc.Profile.CardStyle.Border != nil && !*rc.Profile.CardStyle.Border {
		return "", 0
	}
	if relativeLuminance(rc.BgColor) < 0.2 {
		return "", 0
	}
	return "E3E7EB", 0.75
}

func darken(hex string, amount float64) string {
	hex = normalizeColor(hex)
	if len(hex) != 6 {
		return hex
	}
	shift := func(c float64) int {
		if amount >= 0 {
			c *= (1 - amount)
		} else {
			c += (1 - c) * (-amount)
		}
		v := int(c*255 + 0.5)
		return max(0, min(255, v))
	}
	r := shift(hexToByte(hex[0:2]))
	g := shift(hexToByte(hex[2:4]))
	b := shift(hexToByte(hex[4:6]))
	const digits = "0123456789ABCDEF"
	return string([]byte{
		digits[r>>4], digits[r&0xF],
		digits[g>>4], digits[g&0xF],
		digits[b>>4], digits[b&0xF],
	})
}
