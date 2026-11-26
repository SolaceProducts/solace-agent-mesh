// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strconv"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
	"gopkg.in/yaml.v3"
)

const pptxMIMEType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"

type CreatePresentationParams struct {
	Title      string         `json:"title" desc:"Title of the presentation (used in metadata)"`
	Author     *string        `json:"author" desc:"Author name for metadata"`
	Theme      *ThemeOverride `json:"theme" desc:"Color and font theme. Uses PptxGenJS/OOXML naming: headingFont, bodyFont, colors with background1/background2/text1/text2/accent1-6. An overlay on the deployment's template profile: omit it to keep the profile's colors and fonts. The logo and decorations come from the profile, not from this."`
	Style      *StyleOverride `json:"style,omitempty" desc:"Optional visual-style overrides. Omit to inherit the template's styling — only set a field here when the user explicitly asks for a specific look."`
	OutputName *string        `json:"output_name" desc:"Output filename without extension (default: 'presentation')"`
	Images     []sdk.Artifact `json:"images,omitempty" desc:"Image artifacts (PNG/JPEG) that can be referenced by slides via image_ref. Pass images as names of files already saved in this session, never paths, and reference them by index (0-based). Omit if no images."`
	Slides     sdk.Artifact   `json:"slides" desc:"Filename of a YAML or JSON artifact containing the slides array. Create the artifact via save_artifact first (or the «««save_artifact:...»»» fenced block), then pass the filename here (optionally suffixed with :version). It must be a name already saved in this session, never a path or an invented name. See the tool instructions for the slide spec schema. This indirection is intentional: it lets you iterate on a deck by editing the spec artifact in place rather than re-emitting the entire slide array on every revision."`
}

type LayoutSlide struct {
	Layout      string     `json:"layout" desc:"Slide layout: 'title', 'section', 'text', 'bullet_list', 'two_column', 'three_column', 'comparison', 'image_left', 'image_right', 'image_center', 'image_full', 'table', 'chart', 'quote', 'big_number', 'timeline', 'icon_grid', 'closing', 'blank'"`
	Kicker      *string    `json:"kicker" desc:"Short uppercase eyebrow label shown above the title (e.g. 'CAPABILITIES TODAY', 'THE BIG IDEA'). Rendered in the accent label font. Omit for no kicker."`
	Title       *string    `json:"title" desc:"Slide title (displayed in the title bar area at top)"`
	Subtitle    *string    `json:"subtitle" desc:"Subtitle text (used by title, section, and closing layouts)"`
	Body        *string    `json:"body" desc:"Main body text content. Use \\n for line breaks."`
	Bullets     []string   `json:"bullets,omitempty" desc:"Bullet point items (for bullet_list layout). Each string is one bullet. Omit on layouts that don't use bullets."`
	Column1     *string    `json:"column1" desc:"Left column plain text (for two_column, three_column, comparison layouts). Use bullets1 instead for bullet lists."`
	Column2     *string    `json:"column2" desc:"Right/middle column plain text (for two_column, three_column, comparison layouts). Use bullets2 instead for bullet lists."`
	Column3     *string    `json:"column3" desc:"Right column plain text (for three_column layout). Use bullets3 instead for bullet lists."`
	Bullets1    []string   `json:"bullets1,omitempty" desc:"Left column bullet points (for two_column, three_column, comparison layouts). Each string is one bullet item. Preferred over column1 when content is a list. Omit on layouts that don't use columns."`
	Bullets2    []string   `json:"bullets2,omitempty" desc:"Right/middle column bullet points (for two_column, three_column, comparison layouts). Each string is one bullet item. Preferred over column2 when content is a list. Omit on layouts that don't use columns."`
	Bullets3    []string   `json:"bullets3,omitempty" desc:"Right column bullet points (for three_column layout). Each string is one bullet item. Preferred over column3 when content is a list. Omit on layouts that don't use three columns."`
	Header1     *string    `json:"header1" desc:"Left column header (for comparison layout)"`
	Header2     *string    `json:"header2" desc:"Right column header (for comparison layout)"`
	ImageRef    *string    `json:"image_ref" desc:"Index into the top-level images array (e.g. '0', '1'). Preferred way to include images — pass them as artifacts."`
	ImageData   *string    `json:"image_data" desc:"Base64-encoded image (PNG/JPEG) for image layouts. Use image_ref with artifacts instead when possible."`
	ImageAlt    *string    `json:"image_alt" desc:"Image alt text description"`
	BgImageRef  *string    `json:"bg_image_ref" desc:"Background image: index into the top-level images array. Used by title layout for a full-bleed background image."`
	BgImageData *string    `json:"bg_image_data" desc:"Background image as base64-encoded PNG/JPEG. Use bg_image_ref with artifacts instead when possible."`
	Caption     *string    `json:"caption" desc:"Caption text (used by image_center and quote layouts for attribution)"`
	QuoteText   *string    `json:"quote_text" desc:"Quote text (for quote layout)"`
	BigNumber   *string    `json:"big_number" desc:"Large number or metric (for big_number layout, e.g. '99.9%', '$1.2M')"`
	BigLabel    *string    `json:"big_label" desc:"Label below the big number (for big_number layout)"`
	Items       []GridItem `json:"items,omitempty" desc:"Items for timeline or icon_grid layouts. Omit on other layouts."`
	TableData   *TableData `json:"table_data" desc:"Table data with headers and rows (for table layout)"`
	ChartData   *ChartData `json:"chart_data" desc:"Chart configuration (for chart layout)"`
	Notes       *string    `json:"notes" desc:"Speaker notes for this slide"`
	Background  *string    `json:"background" desc:"Override background color (hex). Default uses theme background1."`
	Footer      *string    `json:"footer" desc:"Footer text at bottom of slide"`
	Icon        *string    `json:"icon" desc:"Glyph name for layouts that show a single icon (e.g. callout). Pick from the tool's icon list, same names as grid item icons: 'shield', 'rocket', 'bolt', 'people', etc."`
}

type GridItem struct {
	Title       string  `json:"title" desc:"Item title or milestone name"`
	Description *string `json:"description" desc:"Item description or detail text"`
	Icon        *string `json:"icon,omitempty" desc:"Glyph shown in the item's medallion. Set this on EVERY item — an item without a glyph renders a mostly-empty colored circle that looks unfinished. Pick the closest name from the tool's icon list (e.g. 'database', 'shield', 'rocket', 'chart-line', 'plug', 'people', 'robot', 'lightbulb', 'target', 'gears'); pick the nearest match rather than leaving it blank."`
	Label       *string `json:"label,omitempty" desc:"Optional short eyebrow above the item title (e.g. a phase 'NOW'/'NEXT', a stage 'INTERACTIVE', or a step '1 · Build'). Used by timeline and process layouts."`
	Pill        *string `json:"pill,omitempty" desc:"Optional pill/badge text shown under the item title (e.g. 'SELF-SERVE', 'GATED'). Used by process layout."`
}

type TableData struct {
	Headers []string   `json:"headers" desc:"Column header texts"`
	Rows    [][]string `json:"rows" desc:"Array of row arrays with cell texts"`
}

type ChartData struct {
	ChartType  string        `json:"chart_type" desc:"Chart type: 'bar', 'line', 'pie', 'doughnut', 'area'"`
	Title      *string       `json:"title" desc:"Chart title"`
	Series     []ChartSeries `json:"series" desc:"Data series for the chart"`
	ShowLegend *bool         `json:"show_legend" desc:"Show chart legend (default: true)"`
	ShowValues *bool         `json:"show_values" desc:"Show data value labels on chart"`
}

type ChartSeries struct {
	Name       *string   `json:"name" desc:"Series name (shown in legend)"`
	Categories []string  `json:"categories" desc:"Category labels (X-axis or pie slice labels)"`
	Values     []float64 `json:"values" desc:"Data values"`
}

func createPresentation(_ context.Context, p CreatePresentationParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	slides, err := parseSlidesArtifact(p.Slides)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	userTheme := p.Theme.overlay()
	if problems := validateDeck(slides, userTheme); len(problems) > 0 {
		return sdk.Error(fmt.Sprintf("slides artifact %q has %d problem(s):\n%s",
			p.Slides.Filename, len(problems), strings.Join(problems, "\n"))), nil
	}

	_ = tc.SendStatus(fmt.Sprintf("Creating presentation with %d slides, %d images...", len(slides), len(p.Images)))

	profile, profileErr := loadProfileFromConfig(tc)
	if profileErr != nil {
		return sdk.Error(profileErr.Error()), nil
	}

	configTheme := configuredDefaultTheme(tc)
	rc := NewRenderContext(profile, userTheme, configTheme)
	rc.Images = p.Images

	if p.Style != nil {
		if p.Style.CardStyle != nil {
			if !isValidCardPreset(*p.Style.CardStyle) {
				return sdk.Error(fmt.Sprintf("invalid style.card_style %q: must be one of %s", *p.Style.CardStyle, strings.Join(validCardPresets, ", "))), nil
			}
			rc.CardPreset = *p.Style.CardStyle
		}
		if p.Style.CardCorners != nil {
			if !isValidCardCorners(*p.Style.CardCorners) {
				return sdk.Error(fmt.Sprintf("invalid style.card_corners %q: must be one of %s", *p.Style.CardCorners, strings.Join(validCardCorners, ", "))), nil
			}
			rc.CardCorners = *p.Style.CardCorners
		}
	}

	warnings := validateThemeContrast(rc.Theme)
	if len(warnings) > 0 {
		_ = tc.SendStatus("Warning: theme contrast issues detected — auto-correcting text colors")
	}

	pres := pptxgen.New()
	pres.SetTitle(p.Title)
	if p.Author != nil {
		pres.SetAuthor(*p.Author)
	}
	pres.SetDescription(iconAttribution)
	pres.SetSlideSize(rc.SlideW, rc.SlideH)

	for i, sl := range slides {
		_ = tc.SendStatus(fmt.Sprintf("Building slide %d of %d...", i+1, len(slides)))
		slide := pres.AddSlide()
		renderSlide(slide, sl, rc, i+1)
	}

	data, err := pres.ToBytes()
	if err != nil {
		return sdk.Error(fmt.Sprintf("Failed to generate PPTX: %v", err)), nil
	}

	outputName := "presentation"
	if p.OutputName != nil && *p.OutputName != "" {
		outputName = *p.OutputName
	}
	filename := outputName
	if !strings.HasSuffix(strings.ToLower(filename), ".pptx") {
		filename += ".pptx"
	}

	objects := []sdk.DataObject{{
		Name:        filename,
		Content:     data,
		MIMEType:    pptxMIMEType,
		Disposition: sdk.DispositionArtifact,
		Description: fmt.Sprintf("PowerPoint presentation: %s", p.Title),
	}}

	specSharesDeckName := p.Slides.Filename == filename
	if p.Slides.Filename != "" && !specSharesDeckName {
		objects = append(objects, sdk.DataObject{
			Name:        p.Slides.Filename,
			Content:     p.Slides.Content,
			MIMEType:    p.Slides.MIMEType,
			Disposition: sdk.DispositionArtifact,
			Tags:        []string{sdk.TagWorking},
		})
	}

	return sdk.OK(
		fmt.Sprintf("Created presentation '%s' with %d slides", filename, len(slides)),
		sdk.WithDataObjects(objects...),
	), nil
}

func parseSlidesArtifact(art sdk.Artifact) ([]LayoutSlide, error) {
	if art.Filename == "" {
		return nil, fmt.Errorf("slides artifact is missing (the slides parameter must be the filename of a saved YAML or JSON artifact containing the slide array)")
	}
	if len(art.Content) == 0 {
		return nil, fmt.Errorf("slides artifact %q is empty", art.Filename)
	}
	var generic any
	if err := yaml.Unmarshal(art.Content, &generic); err != nil {
		return nil, fmt.Errorf("slides artifact %q is not valid YAML or JSON: %v", art.Filename, err)
	}
	jsonBytes, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("slides artifact %q has non-JSON-representable content: %v", art.Filename, err)
	}
	var slides []LayoutSlide
	if err := json.Unmarshal(jsonBytes, &slides); err != nil {
		return nil, fmt.Errorf("slides artifact %q does not match the expected slide-array schema: %v", art.Filename, err)
	}
	if len(slides) == 0 {
		return nil, fmt.Errorf("slides artifact %q parsed to an empty array (expected at least one slide)", art.Filename)
	}
	return slides, nil
}

func configuredDefaultTheme(tc *sdk.ToolContext) Theme {
	raw, ok := tc.GetConfig("default_theme")
	if !ok {
		return defaultTheme()
	}
	cfg := map[string]any{"default_theme": raw}
	return themeFromConfig(cfg)
}

func mergeTheme(base Theme, user Theme) Theme {
	if user.HeadingFont != "" {
		base.HeadingFont = user.HeadingFont
	}
	if user.BodyFont != "" {
		base.BodyFont = user.BodyFont
	}
	if user.LabelFont != "" {
		base.LabelFont = user.LabelFont
	}
	c := &base.Colors
	u := user.Colors
	if u.Background1 != "" {
		c.Background1 = normalizeColor(u.Background1)
	}
	if u.Background2 != "" {
		c.Background2 = normalizeColor(u.Background2)
	}
	if u.Text1 != "" {
		c.Text1 = normalizeColor(u.Text1)
	}
	if u.Text2 != "" {
		c.Text2 = normalizeColor(u.Text2)
	}
	if u.Accent1 != "" {
		c.Accent1 = normalizeColor(u.Accent1)
	}
	if u.Accent2 != "" {
		c.Accent2 = normalizeColor(u.Accent2)
	}
	if u.Accent3 != "" {
		c.Accent3 = normalizeColor(u.Accent3)
	}
	if u.Accent4 != "" {
		c.Accent4 = normalizeColor(u.Accent4)
	}
	if u.Accent5 != "" {
		c.Accent5 = normalizeColor(u.Accent5)
	}
	if u.Accent6 != "" {
		c.Accent6 = normalizeColor(u.Accent6)
	}
	return base
}

func renderSlide(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext, slideNum int) {
	layout := strings.ToLower(sl.Layout)

	rc.SetSlideCategory(layout)
	rc.BgColor = rc.ResolveBgColor(sl.Background)

	slide.SetBackground(pptxgen.BackgroundOptions{Color: rc.BgColor})

	if sl.Notes != nil {
		slide.SetNotes(*sl.Notes)
	}

	renderDecorations(slide, rc)

	switch layout {
	case "title":
		renderTitle(slide, sl, rc)
	case "section":
		renderSection(slide, sl, rc)
	case "text":
		renderText(slide, sl, rc)
	case "bullet_list":
		renderBulletList(slide, sl, rc)
	case "two_column":
		renderTwoColumn(slide, sl, rc)
	case "three_column":
		renderThreeColumn(slide, sl, rc)
	case "comparison":
		renderComparison(slide, sl, rc)
	case "image_left":
		renderImageLeft(slide, sl, rc)
	case "image_right":
		renderImageRight(slide, sl, rc)
	case "image_center":
		renderImageCenter(slide, sl, rc)
	case "image_full":
		renderImageFull(slide, sl, rc)
	case "table":
		renderTable(slide, sl, rc)
	case "chart":
		renderChart(slide, sl, rc)
	case "quote":
		renderQuote(slide, sl, rc)
	case "big_number", "stat":
		renderBigNumber(slide, sl, rc)
	case "timeline":
		renderTimeline(slide, sl, rc)
	case "process", "flow", "steps":
		renderProcess(slide, sl, rc)
	case "callout", "banner":
		renderCallout(slide, sl, rc)
	case "icon_grid", "features":
		renderIconGrid(slide, sl, rc)
	case "closing":
		renderClosing(slide, sl, rc)
	case "blank":
	default:
		renderText(slide, sl, rc)
	}

	renderFooter(slide, sl, rc, slideNum)
}

func mdText(slide *pptxgen.Slide, text string, opts pptxgen.TextOptions) {
	opts.Markdown = true
	slide.AddText(text, opts)
}

func renderTitle(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme

	hasBgImage := false
	if bgData := resolveBgImage(sl, rc.Images); bgData != nil {
		slide.AddImage(pptxgen.ImageOptions{
			X: 0, Y: 0, Width: rc.SlideW, Height: rc.SlideH,
			Data: bgData,
		})
		slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
			X: 0, Y: 0, Width: rc.SlideW, Height: rc.SlideH,
			Fill: "000000", LineColor: "000000", LineWidth: 0,
			Transparency: 50,
		})
		hasBgImage = true
	}

	if rc.Styled() && !hasBgImage {
		renderTitleStyled(slide, sl, rc)
		return
	}

	if !hasBgImage {
		if accentBar := rc.AccentBarColor(); accentBar != "" {
			slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
				X: 0, Y: 0, Width: rc.SlideW, Height: rc.SlideH * 0.014,
				Fill: accentBar, LineColor: accentBar, LineWidth: 0,
			})
		}
	}

	titleColor := rc.TitleColor()
	subtitleColor := rc.SubtitleColor()
	accentColor := rc.AccentColor()
	if hasBgImage {
		titleColor = "FFFFFF"
		subtitleColor = "FFFFFF"
		accentColor = "FFFFFF"
	}

	cx := rc.SlideW * 0.1
	cw := rc.SlideW * 0.8

	title := strOr(sl.Title, "")
	mdText(slide, title, pptxgen.TextOptions{
		X: cx, Y: rc.SlideH * 0.267, Width: cw, Height: rc.SlideH * 0.213,
		FontSize: 44, Bold: true, FontFace: t.HeadingFont,
		FontColor: titleColor,
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})

	barW := rc.SlideW * 0.4
	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: (rc.SlideW - barW) / 2, Y: rc.SlideH * 0.516, Width: barW, Height: rc.SlideH * 0.007,
		Fill: accentColor, LineColor: accentColor,
	})

	if sl.Subtitle != nil && *sl.Subtitle != "" {
		mdText(slide, *sl.Subtitle, pptxgen.TextOptions{
			X: cx + rc.SlideW*0.05, Y: rc.SlideH * 0.569, Width: cw - rc.SlideW*0.1, Height: rc.SlideH * 0.142,
			FontSize: 22, FontFace: t.BodyFont,
			FontColor: subtitleColor,
			Align:     pptxgen.AlignCenter,
		})
	}
}

func renderTitleStyled(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	titleColor := rc.TitleColor()
	lime := normalizeColor(t.Colors.Accent2)

	y := rc.SlideH * 0.16
	if sl.Kicker != nil && *sl.Kicker != "" {
		slide.AddText(strings.ToUpper(*sl.Kicker), pptxgen.TextOptions{
			X: rc.MarginX, Y: y, Width: rc.SlideW * 0.75, Height: 0.35,
			FontSize: 13, Bold: true, FontFace: rc.LabelFont(),
			FontColor: rc.KickerColor(), VAlign: pptxgen.VAlignMiddle,
		})
		y += 0.55
	}

	title := strOr(sl.Title, "")
	slide.AddText(title, pptxgen.TextOptions{
		X: rc.MarginX - 0.02, Y: y, Width: rc.SlideW * 0.82, Height: 1.6,
		FontSize: 54, Bold: true, FontFace: t.HeadingFont,
		FontColor: titleColor, VAlign: pptxgen.VAlignTop,
		Markdown: true, HighlightColor: lime,
	})
	y += 1.55

	if sl.Subtitle != nil && *sl.Subtitle != "" {
		mdText(slide, *sl.Subtitle, pptxgen.TextOptions{
			X: rc.MarginX, Y: y, Width: rc.SlideW * 0.72, Height: 0.7,
			FontSize: 28, FontFace: t.HeadingFont, FontColor: lime,
		})
		y += 0.85
	}

	if sl.Body != nil && *sl.Body != "" {
		mdText(slide, *sl.Body, pptxgen.TextOptions{
			X: rc.MarginX, Y: y + 0.15, Width: rc.SlideW * 0.62, Height: 1.2,
			FontSize: 16, FontFace: t.BodyFont, FontColor: rc.SubtitleColor(),
			LineSpacing: 1.3,
		})
	}
}

func resolveBgImage(sl LayoutSlide, images []sdk.Artifact) []byte {
	if sl.BgImageRef != nil && *sl.BgImageRef != "" {
		idx, err := strconv.Atoi(*sl.BgImageRef)
		if err == nil && idx >= 0 && idx < len(images) {
			return images[idx].Content
		}
	}
	if sl.BgImageData != nil && *sl.BgImageData != "" {
		data, err := base64.StdEncoding.DecodeString(*sl.BgImageData)
		if err == nil {
			return data
		}
	}
	return nil
}

func renderSection(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme

	bg := rc.BgColor
	if rc.Category == nil {
		bg = normalizeColor(t.Colors.Accent1)
		slide.SetBackground(pptxgen.BackgroundOptions{Color: bg})
		rc.BgColor = bg
	}

	titleColor := t.ensureContrast("FFFFFF", bg)
	subColor := titleColor
	if rc.Category != nil {
		if rc.Category.TitleColor != "" {
			titleColor = t.ensureContrast(normalizeColor(rc.Category.TitleColor), bg)
		}
		if rc.Category.BodyColor != "" {
			subColor = t.ensureContrast(normalizeColor(rc.Category.BodyColor), bg)
		}
	}

	if rc.Styled() && sl.Kicker != nil && *sl.Kicker != "" {
		slide.AddText(strings.ToUpper(*sl.Kicker), pptxgen.TextOptions{
			X: rc.SlideW * 0.1, Y: rc.SlideH * 0.24, Width: rc.SlideW * 0.8, Height: 0.3,
			FontSize: 13, Bold: true, FontFace: rc.LabelFont(),
			FontColor: rc.KickerColor(), Align: pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
		})
	}

	title := strOr(sl.Title, "")
	mdText(slide, title, pptxgen.TextOptions{
		X: rc.SlideW * 0.1, Y: rc.SlideH * 0.32, Width: rc.SlideW * 0.8, Height: rc.SlideH * 0.213,
		FontSize: 40, Bold: true, FontFace: t.HeadingFont,
		FontColor: titleColor,
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})

	if sl.Subtitle != nil && *sl.Subtitle != "" {
		mdText(slide, *sl.Subtitle, pptxgen.TextOptions{
			X: rc.SlideW * 0.15, Y: rc.SlideH * 0.551, Width: rc.SlideW * 0.7, Height: rc.SlideH * 0.124,
			FontSize: 20, FontFace: t.BodyFont,
			FontColor: subColor,
			Align:     pptxgen.AlignCenter,
		})
	}
}

func renderText(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)

	body := strOr(sl.Body, "")
	mdText(slide, body, pptxgen.TextOptions{
		X: rc.MarginX, Y: rc.ContentTop, Width: rc.ContentW, Height: rc.SlideH - rc.ContentTop - rc.SlideH*0.107,
		FontSize: 18, FontFace: rc.Theme.BodyFont,
		FontColor: rc.BodyColor(),
	})
}

func renderColumnBullets(slide *pptxgen.Slide, bullets []string, x, y, w, maxH, fontSize float64, rc *RenderContext) {
	if len(bullets) == 0 {
		return
	}
	textColor := rc.BodyColor()
	accent := rc.AccentColor()

	bulletH := maxH / float64(len(bullets))
	capH := rc.SlideH * 0.107
	if bulletH > capH {
		bulletH = capH
	}

	for i, bullet := range bullets {
		by := y + float64(i)*bulletH
		dotSize := 0.12
		dotY := by + (bulletH-dotSize)/2
		slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
			X: x + 0.05, Y: dotY, Width: dotSize, Height: dotSize,
			Fill: accent, LineColor: accent, LineWidth: 0,
		})
		mdText(slide, bullet, pptxgen.TextOptions{
			X: x + 0.3, Y: by, Width: w - 0.3, Height: bulletH,
			FontSize: fontSize, FontFace: rc.Theme.BodyFont, FontColor: textColor,
			VAlign: pptxgen.VAlignMiddle,
		})
	}
}

func renderBulletList(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)

	textColor := rc.BodyColor()
	bodyH := rc.SlideH - rc.ContentTop - rc.SlideH*0.107

	if len(sl.Bullets) == 0 {
		body := strOr(sl.Body, "")
		mdText(slide, body, pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.ContentTop, Width: rc.ContentW, Height: bodyH,
			FontSize: 18, FontFace: rc.Theme.BodyFont, FontColor: textColor,
		})
		return
	}

	bulletH := bodyH / float64(len(sl.Bullets))
	if bulletH > rc.SlideH*0.107 {
		bulletH = rc.SlideH * 0.107
	}
	accent := rc.AccentColor()

	for i, bullet := range sl.Bullets {
		y := rc.ContentTop + float64(i)*bulletH

		dotSize := 0.15
		dotY := y + (bulletH-dotSize)/2
		slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
			X: rc.MarginX + 0.1, Y: dotY, Width: dotSize, Height: dotSize,
			Fill: accent, LineColor: accent, LineWidth: 0,
		})

		mdText(slide, bullet, pptxgen.TextOptions{
			X: rc.MarginX + 0.5, Y: y, Width: rc.ContentW - 0.5, Height: bulletH,
			FontSize: 18, FontFace: rc.Theme.BodyFont, FontColor: textColor,
			VAlign: pptxgen.VAlignMiddle,
		})
	}
}

func renderComparison(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)
	if rc.Styled() {
		renderComparisonStyled(slide, sl, rc)
		return
	}
	renderComparisonPlain(slide, sl, rc)
}

func renderComparisonStyled(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	const gap = 0.4
	colW := (rc.ContentW - gap) / 2
	top := rc.ContentTop + 0.45
	cardH := rc.FooterY - top - 0.15

	cardFill := rc.cardFill()
	shadow := rc.cardShadow()
	borderColor, borderWidth := rc.cardBorder()
	titleColor := rc.ensureCardText(t.Colors.Text1, cardFill)
	descColor := rc.ensureCardText(t.Colors.Text2, cardFill)
	accents := []string{normalizeColor(t.Colors.Accent1), normalizeColor(t.Colors.Accent2)}

	headers := []string{strOr(sl.Header1, "Option A"), strOr(sl.Header2, "Option B")}
	bulletSets := [][]string{sl.Bullets1, sl.Bullets2}
	columns := []string{strOr(sl.Column1, ""), strOr(sl.Column2, "")}

	const pad = 0.32
	for i := range 2 {
		cx := rc.MarginX + float64(i)*(colW+gap)
		cardLine := cardFill
		if borderColor != "" {
			cardLine = borderColor
		}
		rc.addCard(slide, cx, top, colW, cardH, cardFill, cardLine, borderWidth, shadow)
		slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
			X: cx + pad, Y: top + pad + 0.5, Width: 0.5, Height: 0.05,
			Fill: accents[i], LineColor: accents[i],
		})
		mdText(slide, headers[i], pptxgen.TextOptions{
			X: cx + pad, Y: top + pad, Width: colW - 2*pad, Height: 0.5,
			FontSize: 20, Bold: true, FontFace: t.HeadingFont, FontColor: titleColor,
		})
		contentY := top + pad + 0.75
		contentH := top + cardH - contentY - pad
		if len(bulletSets[i]) > 0 {
			renderColumnBullets(slide, bulletSets[i], cx+pad, contentY, colW-2*pad, contentH, 15, rc)
		} else {
			mdText(slide, columns[i], pptxgen.TextOptions{
				X: cx + pad, Y: contentY, Width: colW - 2*pad, Height: contentH,
				FontSize: 15, FontFace: t.BodyFont, FontColor: descColor, LineSpacing: 1.3,
			})
		}
	}
}

func renderComparisonPlain(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme

	colW := (rc.ContentW - 0.4) / 2
	accent := rc.AccentColor()
	accent2 := normalizeColor(t.Colors.Accent2)
	headerH := 0.5
	headerTop := rc.ContentTop

	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: rc.MarginX, Y: headerTop, Width: colW, Height: headerH,
		Fill: accent, LineColor: accent, LineWidth: 0,
	})
	mdText(slide, strOr(sl.Header1, "Option A"), pptxgen.TextOptions{
		X: rc.MarginX, Y: headerTop, Width: colW, Height: headerH,
		FontSize: 18, Bold: true, FontFace: t.HeadingFont,
		FontColor: t.ensureContrast("FFFFFF", accent),
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})

	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: rc.MarginX + colW + 0.4, Y: headerTop, Width: colW, Height: headerH,
		Fill: accent2, LineColor: accent2, LineWidth: 0,
	})
	mdText(slide, strOr(sl.Header2, "Option B"), pptxgen.TextOptions{
		X: rc.MarginX + colW + 0.4, Y: headerTop, Width: colW, Height: headerH,
		FontSize: 18, Bold: true, FontFace: t.HeadingFont,
		FontColor: t.ensureContrast("FFFFFF", accent2),
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})

	contentY := headerTop + headerH + 0.15
	colContentH := rc.SlideH - contentY - rc.SlideH*0.107
	textColor := rc.BodyColor()
	if len(sl.Bullets1) > 0 {
		renderColumnBullets(slide, sl.Bullets1, rc.MarginX, contentY, colW, colContentH, 16, rc)
	} else {
		mdText(slide, strOr(sl.Column1, ""), pptxgen.TextOptions{
			X: rc.MarginX, Y: contentY, Width: colW, Height: colContentH,
			FontSize: 16, FontFace: t.BodyFont, FontColor: textColor,
		})
	}
	if len(sl.Bullets2) > 0 {
		renderColumnBullets(slide, sl.Bullets2, rc.MarginX+colW+0.4, contentY, colW, colContentH, 16, rc)
	} else {
		mdText(slide, strOr(sl.Column2, ""), pptxgen.TextOptions{
			X: rc.MarginX + colW + 0.4, Y: contentY, Width: colW, Height: colContentH,
			FontSize: 16, FontFace: t.BodyFont, FontColor: textColor,
		})
	}

	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: rc.MarginX + colW + 0.17, Y: headerTop, Width: 0.06, Height: colContentH + headerH + 0.15,
		Fill: normalizeColor(t.Colors.Background2), LineColor: normalizeColor(t.Colors.Background2),
	})
}

func renderQuote(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	accent := rc.AccentColor()
	textColor := rc.BodyColor()

	slide.AddText("\u201C", pptxgen.TextOptions{
		X: rc.SlideW * 0.08, Y: rc.SlideH * 0.107, Width: rc.SlideW * 0.15, Height: rc.SlideH * 0.267,
		FontSize: 120, FontFace: t.HeadingFont,
		FontColor: accent, Align: pptxgen.AlignLeft,
	})

	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: rc.MarginX, Y: rc.SlideH * 0.213, Width: 0.06, Height: rc.SlideH * 0.533,
		Fill: accent, LineColor: accent,
	})

	quote := strOr(sl.QuoteText, strOr(sl.Body, ""))
	mdText(slide, quote, pptxgen.TextOptions{
		X: rc.SlideW * 0.12, Y: rc.SlideH * 0.267, Width: rc.SlideW * 0.75, Height: rc.SlideH * 0.444,
		FontSize: 26, Italic: true, FontFace: t.BodyFont,
		FontColor: textColor,
		VAlign:    pptxgen.VAlignMiddle,
	})

	attribution := strOr(sl.Caption, strOr(sl.Subtitle, ""))
	if attribution != "" {
		mdText(slide, "\u2014 "+attribution, pptxgen.TextOptions{
			X: rc.SlideW * 0.12, Y: rc.SlideH * 0.747, Width: rc.SlideW * 0.75, Height: rc.SlideH * 0.089,
			FontSize: 18, FontFace: t.BodyFont,
			FontColor: rc.SubtitleColor(),
			Align:     pptxgen.AlignRight,
		})
	}
}

func renderImageCenter(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)

	imgH := rc.SlideH - rc.ContentTop - rc.SlideH*0.213
	addImageAt(slide, sl, rc.MarginX, rc.ContentTop, rc.ContentW, imgH, rc.Images)

	caption := strOr(sl.Caption, strOr(sl.Body, ""))
	if caption != "" {
		mdText(slide, caption, pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.ContentTop + imgH + 0.1, Width: rc.ContentW, Height: 0.5,
			FontSize: 14, FontFace: rc.Theme.BodyFont,
			FontColor: rc.SubtitleColor(),
			Align:     pptxgen.AlignCenter,
		})
	}
}

func renderBigNumber(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	addTitleBar(slide, sl, rc)

	accent := rc.AccentColor()
	textColor := rc.BodyColor()

	number := strOr(sl.BigNumber, "0")
	slide.AddText(number, pptxgen.TextOptions{
		X: rc.MarginX, Y: rc.ContentTop + 0.2, Width: rc.ContentW, Height: rc.SlideH * 0.356,
		FontSize: 72, Bold: true, FontFace: t.HeadingFont,
		FontColor: accent,
		Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
	})

	label := strOr(sl.BigLabel, "")
	if label != "" {
		mdText(slide, label, pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.ContentTop + rc.SlideH*0.356 + 0.2, Width: rc.ContentW, Height: rc.SlideH * 0.142,
			FontSize: 24, FontFace: t.BodyFont,
			FontColor: textColor,
			Align:     pptxgen.AlignCenter,
		})
	}

	body := strOr(sl.Body, "")
	if body != "" {
		mdText(slide, body, pptxgen.TextOptions{
			X: rc.SlideW * 0.15, Y: rc.ContentTop + rc.SlideH*0.533, Width: rc.SlideW * 0.7, Height: rc.SlideH * 0.142,
			FontSize: 16, FontFace: t.BodyFont,
			FontColor: rc.SubtitleColor(),
			Align:     pptxgen.AlignCenter,
		})
	}
}

func renderTimeline(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)
	if len(sl.Items) == 0 {
		return
	}
	if rc.Styled() {
		renderStyledCardRow(slide, sl.Items, rc, cardRowOptions{arrows: false})
		return
	}
	renderTimelinePlain(slide, sl, rc)
}

func renderProcess(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)
	if len(sl.Items) == 0 {
		return
	}
	if rc.Styled() {
		renderStyledCardRow(slide, sl.Items, rc, cardRowOptions{arrows: true})
		return
	}
	renderTimelinePlain(slide, sl, rc)
}

func renderTimelinePlain(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme

	items := sl.Items
	if len(items) == 0 {
		return
	}

	accent := rc.AccentColor()
	textColor := rc.BodyColor()
	descColor := rc.SubtitleColor()
	n := len(items)

	lineY := rc.ContentTop + rc.SlideH*0.178
	slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
		X: rc.MarginX, Y: lineY, Width: rc.ContentW, Height: 0.04,
		Fill: accent, LineColor: accent,
	})

	itemW := rc.ContentW / float64(n)
	for i, item := range items {
		cx := rc.MarginX + float64(i)*itemW + itemW/2

		slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
			X: cx - 0.15, Y: lineY - 0.13, Width: 0.3, Height: 0.3,
			Fill: accent, LineColor: accent, LineWidth: 0,
		})

		mdText(slide, item.Title, pptxgen.TextOptions{
			X: rc.MarginX + float64(i)*itemW, Y: rc.ContentTop,
			Width: itemW, Height: rc.SlideH * 0.16,
			FontSize: 14, Bold: true, FontFace: t.HeadingFont,
			FontColor: textColor, Align: pptxgen.AlignCenter, VAlign: pptxgen.VAlignBottom,
		})

		if item.Description != nil && *item.Description != "" {
			mdText(slide, *item.Description, pptxgen.TextOptions{
				X: rc.MarginX + float64(i)*itemW, Y: lineY + 0.3,
				Width: itemW, Height: rc.SlideH * 0.267,
				FontSize: 12, FontFace: t.BodyFont,
				FontColor: descColor, Align: pptxgen.AlignCenter,
			})
		}
	}
}

func renderIconGrid(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)
	if len(sl.Items) == 0 {
		return
	}
	if rc.Styled() {
		renderIconGridStyled(slide, sl, rc)
		return
	}
	renderIconGridPlain(slide, sl, rc)
}

func renderIconGridStyled(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	items := sl.Items
	n := len(items)
	cols := 3
	if n <= 2 {
		cols = n
	} else if n == 4 {
		cols = 2
	}
	rows := (n + cols - 1) / cols

	const gap = 0.27
	gridTop := rc.ContentTop + 0.35
	cellW := (rc.ContentW - gap*float64(cols-1)) / float64(cols)
	availH := rc.FooterY - gridTop - 0.1
	cellH := (availH - gap*float64(rows-1)) / float64(rows)
	if cellH > 2.45 {
		cellH = 2.45
	}

	medallions := rc.medallionPalette()
	cardFill := rc.cardFill()
	shadow := rc.cardShadow()
	borderColor, borderWidth := rc.cardBorder()
	titleColor := rc.ensureCardText(rc.Theme.Colors.Text1, cardFill)
	descColor := rc.ensureCardText(rc.Theme.Colors.Text2, cardFill)

	const pad = 0.3
	const medD = 0.62
	const glyphD = 0.32
	const bottomPad = 0.28

	for i, item := range items {
		cx := rc.MarginX + float64(i%cols)*(cellW+gap)
		cy := gridTop + float64(i/cols)*(cellH+gap)

		cardLine := cardFill
		if borderColor != "" {
			cardLine = borderColor
		}
		rc.addCard(slide, cx, cy, cellW, cellH, cardFill, cardLine, borderWidth, shadow)

		medColor := medallions[i%len(medallions)]
		slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
			X: cx + pad, Y: cy + pad, Width: medD, Height: medD,
			Fill: medColor, LineColor: medColor,
		})
		rc.fillMedallionContent(slide, item, cx+pad, cy+pad, medD, glyphD, medColor)

		titleY := cy + pad + medD + 0.18
		mdText(slide, item.Title, pptxgen.TextOptions{
			X: cx + pad, Y: titleY, Width: cellW - 2*pad, Height: 0.4,
			FontSize: 16, Bold: true, FontFace: t.HeadingFont,
			FontColor: titleColor,
		})

		if item.Description != nil && *item.Description != "" {
			descY := titleY + 0.42
			mdText(slide, *item.Description, pptxgen.TextOptions{
				X: cx + pad, Y: descY, Width: cellW - 2*pad, Height: cy + cellH - descY - bottomPad,
				FontSize: 11.5, FontFace: t.BodyFont,
				FontColor: descColor, LineSpacing: 1.15,
			})
		}
	}
}

func (rc *RenderContext) ensureCardText(color, cardFill string) string {
	return rc.Theme.ensureContrast(normalizeColor(color), cardFill)
}

func renderIconGridPlain(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	items := sl.Items

	accent := rc.AccentColor()
	textColor := rc.BodyColor()
	descColor := rc.SubtitleColor()
	accent2 := normalizeColor(t.Colors.Accent2)

	n := len(items)
	cols := 3
	if n <= 2 {
		cols = n
	} else if n == 4 {
		cols = 2
	}
	rows := (n + cols - 1) / cols

	cellW := rc.ContentW / float64(cols)
	availH := rc.SlideH - rc.ContentTop - rc.SlideH*0.107
	cellH := availH / float64(rows)
	if cellH > rc.SlideH*0.356 {
		cellH = rc.SlideH * 0.356
	}

	accentColors := []string{accent, accent2, normalizeColor(t.Colors.Accent3),
		normalizeColor(t.Colors.Accent4), normalizeColor(t.Colors.Accent5), normalizeColor(t.Colors.Accent6)}

	for i, item := range items {
		col := i % cols
		row := i / cols
		cx := rc.MarginX + float64(col)*cellW
		cy := rc.ContentTop + float64(row)*cellH

		circleColor := accentColors[i%len(accentColors)]
		slide.AddShape(pptxgen.ShapeEllipse, pptxgen.ShapeOptions{
			X: cx + cellW/2 - 0.25, Y: cy + 0.1, Width: 0.5, Height: 0.5,
			Fill: circleColor, LineColor: circleColor, LineWidth: 0,
		})

		mdText(slide, item.Title, pptxgen.TextOptions{
			X: cx, Y: cy + 0.7, Width: cellW, Height: 0.4,
			FontSize: 16, Bold: true, FontFace: t.HeadingFont,
			FontColor: textColor, Align: pptxgen.AlignCenter,
		})

		if item.Description != nil && *item.Description != "" {
			mdText(slide, *item.Description, pptxgen.TextOptions{
				X: cx + 0.2, Y: cy + 1.1, Width: cellW - 0.4, Height: cellH - 1.3,
				FontSize: 12, FontFace: t.BodyFont,
				FontColor: descColor, Align: pptxgen.AlignCenter,
			})
		}
	}
}

func renderTwoColumn(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)

	colW := (rc.ContentW - 0.3) / 2
	textColor := rc.BodyColor()
	colH := rc.SlideH - rc.ContentTop - rc.SlideH*0.107

	if len(sl.Bullets1) > 0 {
		renderColumnBullets(slide, sl.Bullets1, rc.MarginX, rc.ContentTop, colW, colH, 16, rc)
	} else {
		mdText(slide, strOr(sl.Column1, ""), pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.ContentTop, Width: colW, Height: colH,
			FontSize: 16, FontFace: rc.Theme.BodyFont, FontColor: textColor,
		})
	}
	if len(sl.Bullets2) > 0 {
		renderColumnBullets(slide, sl.Bullets2, rc.MarginX+colW+0.3, rc.ContentTop, colW, colH, 16, rc)
	} else {
		mdText(slide, strOr(sl.Column2, ""), pptxgen.TextOptions{
			X: rc.MarginX + colW + 0.3, Y: rc.ContentTop, Width: colW, Height: colH,
			FontSize: 16, FontFace: rc.Theme.BodyFont, FontColor: textColor,
		})
	}
}

func renderThreeColumn(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)

	colW := (rc.ContentW - 0.6) / 3
	gap := 0.3
	textColor := rc.BodyColor()
	colH := rc.SlideH - rc.ContentTop - rc.SlideH*0.107

	bulletsArr := [3][]string{sl.Bullets1, sl.Bullets2, sl.Bullets3}
	textArr := [3]*string{sl.Column1, sl.Column2, sl.Column3}
	for i := range textArr {
		cx := rc.MarginX + float64(i)*(colW+gap)
		if len(bulletsArr[i]) > 0 {
			renderColumnBullets(slide, bulletsArr[i], cx, rc.ContentTop, colW, colH, 14, rc)
		} else {
			mdText(slide, strOr(textArr[i], ""), pptxgen.TextOptions{
				X: cx, Y: rc.ContentTop, Width: colW, Height: colH,
				FontSize: 14, FontFace: rc.Theme.BodyFont, FontColor: textColor,
			})
		}
	}
}

func renderImageLeft(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)

	imgW := rc.ContentW * 0.467
	colH := rc.SlideH - rc.ContentTop - rc.SlideH*0.107
	addImageAt(slide, sl, rc.MarginX, rc.ContentTop, imgW, colH, rc.Images)

	body := strOr(sl.Body, "")
	mdText(slide, body, pptxgen.TextOptions{
		X: rc.MarginX + imgW + 0.3, Y: rc.ContentTop,
		Width: rc.ContentW - imgW - 0.3, Height: colH,
		FontSize: 16, FontFace: rc.Theme.BodyFont,
		FontColor: rc.BodyColor(),
	})
}

func renderImageRight(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addTitleBar(slide, sl, rc)

	imgW := rc.ContentW * 0.467
	textW := rc.ContentW - imgW - 0.3
	colH := rc.SlideH - rc.ContentTop - rc.SlideH*0.107

	body := strOr(sl.Body, "")
	mdText(slide, body, pptxgen.TextOptions{
		X: rc.MarginX, Y: rc.ContentTop,
		Width: textW, Height: colH,
		FontSize: 16, FontFace: rc.Theme.BodyFont,
		FontColor: rc.BodyColor(),
	})

	addImageAt(slide, sl, rc.MarginX+textW+0.3, rc.ContentTop, imgW, colH, rc.Images)
}

func renderImageFull(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	addImageAt(slide, sl, 0, 0, rc.SlideW, rc.SlideH, rc.Images)

	if sl.Title != nil && *sl.Title != "" {
		barH := rc.SlideH * 0.213
		slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
			X: 0, Y: rc.SlideH - barH, Width: rc.SlideW, Height: barH,
			Fill: "000000", LineColor: "000000", LineWidth: 0,
			Transparency: 40,
		})
		mdText(slide, *sl.Title, pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.SlideH - barH + 0.1, Width: rc.ContentW, Height: barH - 0.2,
			FontSize: 28, Bold: true, FontFace: rc.Theme.HeadingFont,
			FontColor: "FFFFFF", Align: pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
		})
	}
}

func renderTable(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	addTitleBar(slide, sl, rc)

	if sl.TableData == nil || (len(sl.TableData.Headers) == 0 && len(sl.TableData.Rows) == 0) {
		return
	}

	var rows [][]pptxgen.TableCell
	accent := rc.AccentColor()
	headerTextColor := t.ensureContrast("FFFFFF", accent)

	if len(sl.TableData.Headers) > 0 {
		headerCells := make([]pptxgen.TableCell, len(sl.TableData.Headers))
		for i, h := range sl.TableData.Headers {
			headerCells[i] = pptxgen.TableCell{
				Text: h, Bold: true, FontColor: headerTextColor,
			}
		}
		rows = append(rows, headerCells)
	}

	for _, row := range sl.TableData.Rows {
		cells := make([]pptxgen.TableCell, len(row))
		for i, cell := range row {
			cells[i] = pptxgen.TableCell{Text: cell}
		}
		rows = append(rows, cells)
	}

	tableY := rc.ContentTop
	if rc.Styled() {
		tableY += 0.25
	}
	slide.AddTable(rows, pptxgen.TableOptions{
		X: rc.MarginX, Y: tableY, Width: rc.ContentW,
		FontFace:     t.BodyFont,
		FontSize:     14,
		FontColor:    rc.BodyColor(),
		Fill:         rc.BgColor,
		FirstRowBold: true,
		FirstRowFill: accent,
		Border:       pptxgen.Border{Color: normalizeColor(t.Colors.Background2), Width: 1, Style: pptxgen.BorderSolid},
	})
}

func renderChart(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme
	addTitleBar(slide, sl, rc)

	if sl.ChartData == nil || len(sl.ChartData.Series) == 0 {
		return
	}

	chartType := pptxgen.ChartBar
	switch sl.ChartData.ChartType {
	case "line":
		chartType = pptxgen.ChartLine
	case "pie":
		chartType = pptxgen.ChartPie
	case "doughnut":
		chartType = pptxgen.ChartDoughnut
	case "area":
		chartType = pptxgen.ChartArea
	}

	var series []pptxgen.ChartSeries
	for _, s := range sl.ChartData.Series {
		cs := pptxgen.ChartSeries{
			Labels: s.Categories,
			Values: s.Values,
		}
		if s.Name != nil {
			cs.Name = *s.Name
		}
		series = append(series, cs)
	}

	isDark := relativeLuminance(rc.BgColor) < 0.2
	chartTop := rc.ContentTop
	chartH := rc.SlideH - rc.ContentTop - rc.SlideH*0.107

	if sl.ChartData.Title != nil && *sl.ChartData.Title != "" && isDark {
		slide.AddText(*sl.ChartData.Title, pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.ContentTop, Width: rc.ContentW, Height: 0.5,
			FontSize: 16, Bold: true, FontFace: t.BodyFont,
			FontColor: rc.BodyColor(),
			Align:     pptxgen.AlignCenter,
		})
		chartTop = rc.ContentTop + 0.5
		chartH -= 0.5
	}

	if isDark {
		slide.AddShape(pptxgen.ShapeRoundRect, pptxgen.ShapeOptions{
			X: rc.MarginX, Y: chartTop, Width: rc.ContentW, Height: chartH,
			Fill: "FFFFFF", LineColor: normalizeColor(t.Colors.Background2),
		})
	}

	opts := pptxgen.ChartOptions{
		X: rc.MarginX, Y: chartTop, Width: rc.ContentW, Height: chartH,
		ShowLegend: boolOr(sl.ChartData.ShowLegend, true),
		ShowValues: boolOr(sl.ChartData.ShowValues, false),
		Colors:     t.accentColors(),
	}
	if sl.ChartData.Title != nil && !isDark {
		opts.Title = *sl.ChartData.Title
		opts.ShowTitle = true
	}

	slide.AddChart(chartType, series, opts)
}

func renderClosing(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	t := &rc.Theme

	titleColor := rc.TitleColor()
	subtitleColor := rc.SubtitleColor()

	titleY := rc.SlideH * 0.35
	subtitleY := rc.SlideH * 0.55
	if rc.Category != nil && len(rc.Category.Decorations) > 0 {
		titleY = rc.SlideH * 0.55
		subtitleY = rc.SlideH * 0.70
	}

	title := strOr(sl.Title, "")
	if title != "" {
		mdText(slide, title, pptxgen.TextOptions{
			X: rc.SlideW * 0.15, Y: titleY, Width: rc.SlideW * 0.7, Height: rc.SlideH * 0.12,
			FontSize: 36, Bold: true, FontFace: t.HeadingFont,
			FontColor: titleColor,
			Align:     pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
		})
	}

	subtitle := strOr(sl.Subtitle, "")
	if subtitle != "" {
		mdText(slide, subtitle, pptxgen.TextOptions{
			X: rc.SlideW * 0.2, Y: subtitleY, Width: rc.SlideW * 0.6, Height: rc.SlideH * 0.10,
			FontSize: 20, FontFace: t.BodyFont,
			FontColor: subtitleColor,
			Align:     pptxgen.AlignCenter,
		})
	}
}

func addTitleBar(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	if sl.Title == nil || *sl.Title == "" {
		return
	}

	if rc.Styled() {
		addStyledTitleBar(slide, sl, rc)
		return
	}

	if accentBar := rc.AccentBarColor(); accentBar != "" {
		slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
			X: rc.MarginX, Y: rc.MarginY + rc.TitleHeight + 0.05, Width: rc.ContentW, Height: 0.03,
			Fill: accentBar, LineColor: accentBar,
		})
	}

	mdText(slide, *sl.Title, pptxgen.TextOptions{
		X: rc.MarginX, Y: rc.MarginY, Width: rc.ContentW, Height: rc.TitleHeight,
		FontSize: 28, Bold: true, FontFace: rc.Theme.HeadingFont,
		FontColor: rc.TitleColor(),
		VAlign:    pptxgen.VAlignBottom,
	})
}

func addStyledTitleBar(slide *pptxgen.Slide, sl LayoutSlide, rc *RenderContext) {
	if sl.Kicker != nil && *sl.Kicker != "" {
		slide.AddText(strings.ToUpper(*sl.Kicker), pptxgen.TextOptions{
			X: rc.MarginX, Y: rc.MarginY, Width: rc.ContentW, Height: 0.3,
			FontSize: 11, Bold: true, FontFace: rc.LabelFont(),
			FontColor: rc.KickerColor(),
			VAlign:    pptxgen.VAlignMiddle,
		})
	}

	titleY := rc.MarginY + 0.32
	mdText(slide, *sl.Title, pptxgen.TextOptions{
		X: rc.MarginX, Y: titleY, Width: rc.ContentW, Height: rc.ContentTop - titleY,
		FontSize: 32, Bold: true, FontFace: rc.Theme.HeadingFont,
		FontColor: rc.TitleColor(),
		VAlign:    pptxgen.VAlignBottom,
	})
}

func addImageAt(slide *pptxgen.Slide, sl LayoutSlide, x, y, w, h float64, images []sdk.Artifact) {
	var data []byte
	if sl.ImageRef != nil && *sl.ImageRef != "" {
		idx, err := strconv.Atoi(*sl.ImageRef)
		if err == nil && idx >= 0 && idx < len(images) {
			data = images[idx].Content
		}
	}
	if data == nil && sl.ImageData != nil && *sl.ImageData != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(*sl.ImageData)
		if err != nil {
			data = nil
		}
	}

	if data == nil {
		slide.AddShape(pptxgen.ShapeRect, pptxgen.ShapeOptions{
			X: x, Y: y, Width: w, Height: h,
			Fill: "E0E0E0", LineColor: "CCCCCC", LineWidth: 1,
		})
		slide.AddText("[Image placeholder]", pptxgen.TextOptions{
			X: x, Y: y, Width: w, Height: h,
			FontSize: 14, FontColor: "999999",
			Align: pptxgen.AlignCenter, VAlign: pptxgen.VAlignMiddle,
		})
		return
	}

	alt := ""
	if sl.ImageAlt != nil {
		alt = *sl.ImageAlt
	}

	imgX, imgY, imgW, imgH := fitImage(data, x, y, w, h)

	slide.AddImage(pptxgen.ImageOptions{
		X: imgX, Y: imgY, Width: imgW, Height: imgH,
		Data: data, AltText: alt,
	})
}

func fitImage(data []byte, x, y, w, h float64) (fx, fy, fw, fh float64) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return x, y, w, h
	}

	imgAspect := float64(cfg.Width) / float64(cfg.Height)
	boxAspect := w / h

	if imgAspect > boxAspect {
		fw = w
		fh = w / imgAspect
		fx = x
		fy = y + (h-fh)/2
	} else {
		fh = h
		fw = h * imgAspect
		fx = x + (w-fw)/2
		fy = y
	}
	return
}

func strOr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}

func boolOr(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}
