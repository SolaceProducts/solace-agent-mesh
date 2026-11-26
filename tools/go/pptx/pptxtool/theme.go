// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"encoding/json"
	"math"
)

type Theme struct {
	HeadingFont string      `json:"headingFont" desc:"Font for titles and headings (e.g. 'Calibri Light', 'Arial')"`
	BodyFont    string      `json:"bodyFont" desc:"Font for body text and content (e.g. 'Calibri', 'Arial')"`
	LabelFont   string      `json:"labelFont" desc:"Font for kickers, eyebrow labels, pills and footers (typically monospace, e.g. 'Space Mono', 'Consolas')"`
	Colors      ThemeColors `json:"colors" desc:"Color palette using the standard 12-slot OOXML scheme"`
}

type ThemeColors struct {
	Background1 string `json:"background1" desc:"Primary background color (hex, e.g. 'FFFFFF')"`
	Background2 string `json:"background2" desc:"Secondary/alternate background color (hex, e.g. 'E8E8E8')"`
	Text1       string `json:"text1" desc:"Primary text color (hex, e.g. '000000')"`
	Text2       string `json:"text2" desc:"Secondary text color for subtitles/captions (hex, e.g. '444444')"`
	Accent1     string `json:"accent1" desc:"Primary accent (hex) — used for headers, chart series 1, key shapes"`
	Accent2     string `json:"accent2" desc:"Secondary accent (hex) — chart series 2, secondary highlights"`
	Accent3     string `json:"accent3" desc:"Tertiary accent (hex) — chart series 3"`
	Accent4     string `json:"accent4" desc:"Accent 4 (hex) — chart series 4"`
	Accent5     string `json:"accent5" desc:"Accent 5 (hex) — chart series 5"`
	Accent6     string `json:"accent6" desc:"Accent 6 (hex) — chart series 6"`
}

type ThemeOverride struct {
	HeadingFont *string              `json:"headingFont" desc:"Font for titles and headings (e.g. 'Calibri Light', 'Arial')"`
	BodyFont    *string              `json:"bodyFont" desc:"Font for body text and content (e.g. 'Calibri', 'Arial')"`
	LabelFont   *string              `json:"labelFont" desc:"Font for kickers, eyebrow labels, pills and footers (typically monospace, e.g. 'Space Mono', 'Consolas')"`
	Colors      *ThemeColorsOverride `json:"colors" desc:"Color palette using the standard 12-slot OOXML scheme"`
}

type ThemeColorsOverride struct {
	Background1 *string `json:"background1" desc:"Primary background color (hex, e.g. 'FFFFFF')"`
	Background2 *string `json:"background2" desc:"Secondary/alternate background color (hex, e.g. 'E8E8E8')"`
	Text1       *string `json:"text1" desc:"Primary text color (hex, e.g. '000000')"`
	Text2       *string `json:"text2" desc:"Secondary text color for subtitles/captions (hex, e.g. '444444')"`
	Accent1     *string `json:"accent1" desc:"Primary accent (hex) — used for headers, chart series 1, key shapes"`
	Accent2     *string `json:"accent2" desc:"Secondary accent (hex) — chart series 2, secondary highlights"`
	Accent3     *string `json:"accent3" desc:"Tertiary accent (hex) — chart series 3"`
	Accent4     *string `json:"accent4" desc:"Accent 4 (hex) — chart series 4"`
	Accent5     *string `json:"accent5" desc:"Accent 5 (hex) — chart series 5"`
	Accent6     *string `json:"accent6" desc:"Accent 6 (hex) — chart series 6"`
}

func (o *ThemeOverride) overlay() *Theme {
	if o == nil {
		return nil
	}
	t := &Theme{
		HeadingFont: strOr(o.HeadingFont, ""),
		BodyFont:    strOr(o.BodyFont, ""),
		LabelFont:   strOr(o.LabelFont, ""),
	}
	if c := o.Colors; c != nil {
		t.Colors = ThemeColors{
			Background1: strOr(c.Background1, ""),
			Background2: strOr(c.Background2, ""),
			Text1:       strOr(c.Text1, ""),
			Text2:       strOr(c.Text2, ""),
			Accent1:     strOr(c.Accent1, ""),
			Accent2:     strOr(c.Accent2, ""),
			Accent3:     strOr(c.Accent3, ""),
			Accent4:     strOr(c.Accent4, ""),
			Accent5:     strOr(c.Accent5, ""),
			Accent6:     strOr(c.Accent6, ""),
		}
	}
	return t
}

func defaultTheme() Theme {
	return Theme{
		HeadingFont: "Calibri Light",
		BodyFont:    "Calibri",
		LabelFont:   "Consolas",
		Colors: ThemeColors{
			Background1: "FFFFFF",
			Background2: "F2F2F2",
			Text1:       "000000",
			Text2:       "595959",
			Accent1:     "4472C4",
			Accent2:     "ED7D31",
			Accent3:     "A5A5A5",
			Accent4:     "FFC000",
			Accent5:     "5B9BD5",
			Accent6:     "70AD47",
		},
	}
}

func (t *Theme) accentColors() []string {
	return []string{
		normalizeColor(t.Colors.Accent1),
		normalizeColor(t.Colors.Accent2),
		normalizeColor(t.Colors.Accent3),
		normalizeColor(t.Colors.Accent4),
		normalizeColor(t.Colors.Accent5),
		normalizeColor(t.Colors.Accent6),
	}
}

func normalizeColor(c string) string {
	if len(c) > 0 && c[0] == '#' {
		return c[1:]
	}
	return c
}

func relativeLuminance(hex string) float64 {
	hex = normalizeColor(hex)
	if len(hex) != 6 {
		return 0
	}
	r := sRGBtoLinear(hexToByte(hex[0:2]))
	g := sRGBtoLinear(hexToByte(hex[2:4]))
	b := sRGBtoLinear(hexToByte(hex[4:6]))
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func hexToByte(s string) float64 {
	var val int
	for _, c := range s {
		val <<= 4
		switch {
		case c >= '0' && c <= '9':
			val += int(c - '0')
		case c >= 'a' && c <= 'f':
			val += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			val += int(c-'A') + 10
		}
	}
	return float64(val) / 255.0
}

func sRGBtoLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func contrastRatio(fg, bg string) float64 {
	l1 := relativeLuminance(fg)
	l2 := relativeLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func validateThemeContrast(t Theme) []string {
	var warnings []string
	check := func(fgName, bgName, fg, bg string) {
		ratio := contrastRatio(fg, bg)
		if ratio < 4.5 {
			warnings = append(warnings, fgName+" on "+bgName+": contrast ratio "+formatFloat(ratio)+" < 4.5:1")
		}
	}
	check("text1", "background1", t.Colors.Text1, t.Colors.Background1)
	check("text2", "background1", t.Colors.Text2, t.Colors.Background1)
	check("text1", "background2", t.Colors.Text1, t.Colors.Background2)
	return warnings
}

func formatFloat(f float64) string {
	i := int(f * 10)
	return string(rune('0'+i/10)) + "." + string(rune('0'+i%10))
}

func themeFromConfig(config map[string]any) Theme {
	base := defaultTheme()
	raw, ok := config["default_theme"]
	if !ok {
		return base
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return base
	}
	var user Theme
	if err := json.Unmarshal(data, &user); err != nil {
		return base
	}
	return mergeTheme(base, user)
}

func themeFromProfile(p *Profile) Theme {
	t := defaultTheme()
	if p.HeadingFont != "" {
		t.HeadingFont = p.HeadingFont
	}
	if p.BodyFont != "" {
		t.BodyFont = p.BodyFont
	}
	if p.LabelFont != "" {
		t.LabelFont = p.LabelFont
	}
	c := &t.Colors
	if p.Colors.Background1 != "" {
		c.Background1 = normalizeColor(p.Colors.Background1)
	}
	if p.Colors.Background2 != "" {
		c.Background2 = normalizeColor(p.Colors.Background2)
	}
	if p.Colors.Text1 != "" {
		c.Text1 = normalizeColor(p.Colors.Text1)
	}
	if p.Colors.Text2 != "" {
		c.Text2 = normalizeColor(p.Colors.Text2)
	}
	if p.Colors.Accent1 != "" {
		c.Accent1 = normalizeColor(p.Colors.Accent1)
	}
	if p.Colors.Accent2 != "" {
		c.Accent2 = normalizeColor(p.Colors.Accent2)
	}
	if p.Colors.Accent3 != "" {
		c.Accent3 = normalizeColor(p.Colors.Accent3)
	}
	if p.Colors.Accent4 != "" {
		c.Accent4 = normalizeColor(p.Colors.Accent4)
	}
	if p.Colors.Accent5 != "" {
		c.Accent5 = normalizeColor(p.Colors.Accent5)
	}
	if p.Colors.Accent6 != "" {
		c.Accent6 = normalizeColor(p.Colors.Accent6)
	}
	return t
}

func (t *Theme) ensureContrast(textColor, bgColor string) string {
	if contrastRatio(textColor, bgColor) >= 4.5 {
		return textColor
	}
	if contrastRatio("FFFFFF", bgColor) >= 4.5 {
		return "FFFFFF"
	}
	return "000000"
}
