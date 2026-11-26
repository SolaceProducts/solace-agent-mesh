// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"

type RenderContext struct {
	SlideW, SlideH float64

	MarginX     float64
	MarginY     float64
	ContentTop  float64
	ContentW    float64
	TitleHeight float64
	FooterY     float64

	Theme Theme

	Profile *Profile

	CardPreset string

	CardCorners string

	Images []sdk.Artifact

	Category *ProfileCategory

	BgColor string
}

const (
	defaultSlideW      = 10.0
	defaultSlideH      = 5.625
	defaultMarginX     = 0.5
	defaultMarginY     = 0.3
	defaultContentTop  = 1.3
	defaultContentW    = 9.0
	defaultTitleHeight = 0.7
	defaultFooterY     = 5.1
)

func NewRenderContext(profile *Profile, userTheme *Theme, configTheme Theme) *RenderContext {
	rc := &RenderContext{
		Theme: configTheme,
	}

	if profile == nil {
		rc.SlideW = defaultSlideW
		rc.SlideH = defaultSlideH
		rc.MarginX = defaultMarginX
		rc.MarginY = defaultMarginY
		rc.ContentTop = defaultContentTop
		rc.ContentW = defaultContentW
		rc.TitleHeight = defaultTitleHeight
		rc.FooterY = defaultFooterY
	} else {
		rc.Profile = profile
		rc.SlideW = profile.SlideWidth
		rc.SlideH = profile.SlideHeight
		rc.MarginX = rc.SlideW * (defaultMarginX / defaultSlideW)
		rc.MarginY = rc.SlideH * (defaultMarginY / defaultSlideH)
		rc.ContentTop = rc.SlideH * (defaultContentTop / defaultSlideH)
		rc.ContentW = rc.SlideW - 2*rc.MarginX
		rc.TitleHeight = rc.SlideH * (defaultTitleHeight / defaultSlideH)
		rc.FooterY = rc.SlideH * (defaultFooterY / defaultSlideH)

		rc.Theme = themeFromProfile(profile)
	}

	if userTheme != nil {
		rc.Theme = mergeTheme(rc.Theme, *userTheme)
	}

	return rc
}

func (rc *RenderContext) SetSlideCategory(layout string) {
	rc.Category = nil
	if rc.Profile == nil {
		return
	}
	catName, ok := rc.Profile.LayoutMap[layout]
	if !ok {
		catName = "content_light"
	}
	if cat, ok := rc.Profile.Categories[catName]; ok {
		rc.Category = cat
	}
}

func (rc *RenderContext) ResolveBgColor(slideOverride *string) string {
	if slideOverride != nil && *slideOverride != "" {
		return normalizeColor(*slideOverride)
	}
	if rc.Category != nil && rc.Category.Background != "" {
		return normalizeColor(rc.Category.Background)
	}
	return normalizeColor(rc.Theme.Colors.Background1)
}

func (rc *RenderContext) TitleColor() string {
	if rc.Category != nil && rc.Category.TitleColor != "" {
		return rc.Theme.ensureContrast(normalizeColor(rc.Category.TitleColor), rc.BgColor)
	}
	return rc.Theme.ensureContrast(normalizeColor(rc.Theme.Colors.Accent1), rc.BgColor)
}

func (rc *RenderContext) BodyColor() string {
	if rc.Category != nil && rc.Category.BodyColor != "" {
		return rc.Theme.ensureContrast(normalizeColor(rc.Category.BodyColor), rc.BgColor)
	}
	return rc.Theme.ensureContrast(normalizeColor(rc.Theme.Colors.Text1), rc.BgColor)
}

func (rc *RenderContext) SubtitleColor() string {
	if rc.Category != nil && rc.Category.BodyColor != "" {
		return rc.Theme.ensureContrast(normalizeColor(rc.Category.BodyColor), rc.BgColor)
	}
	return rc.Theme.ensureContrast(normalizeColor(rc.Theme.Colors.Text2), rc.BgColor)
}

func (rc *RenderContext) AccentBarColor() string {
	if rc.Category != nil {
		if rc.Category.AccentBarColor == nil {
			return ""
		}
		return normalizeColor(*rc.Category.AccentBarColor)
	}
	return normalizeColor(rc.Theme.Colors.Accent1)
}

func (rc *RenderContext) AccentColor() string {
	return normalizeColor(rc.Theme.Colors.Accent1)
}
