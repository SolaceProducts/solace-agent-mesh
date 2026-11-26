// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

func TestRenderNoIconFallback(t *testing.T) {
	profile, err := loadBuiltinProfile(defaultProfileName)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	rc := NewRenderContext(profile, nil, defaultTheme())

	s := func(v string) *string { return &v }
	bare := func(title, desc string) GridItem {
		return GridItem{Title: title, Description: s(desc)}
	}
	step := func(label, title, desc string) GridItem {
		return GridItem{Label: s(label), Title: title, Description: s(desc)}
	}

	slides := []LayoutSlide{
		{Layout: "timeline", Title: s("Roadmap"),
			Items: []GridItem{
				step("Now", "Foundation", "Core platform deployed and stable."),
				step("Next", "Expansion", "New integrations and connectors added."),
				step("Then", "Scale", "Global rollout and enterprise onboarding."),
			}},
		{Layout: "icon_grid", Title: s("What the mesh gives you"),
			Items: []GridItem{
				bare("Agents", "Reasoning personas with a toolset."),
				bare("Data access", "Query your systems in plain language."),
				bare("Security", "Role-based access on every call."),
				bare("Deploy", "Promote under review and RBAC."),
				bare("Research", "One cited answer from many sources."),
				bare("Xylophone", "A title with no matching glyph at all."),
			}},
		{Layout: "process", Title: s("Idea to trusted agent"),
			Items: []GridItem{
				step("1", "Build", "Assemble from the catalog."),
				step("2", "Evaluate", "Run evals in a sandbox."),
				step("3", "Deploy", "Promote under review."),
			}},
	}

	pres := pptxgen.New()
	pres.SetSlideSize(rc.SlideW, rc.SlideH)
	for i, sl := range slides {
		renderSlide(pres.AddSlide(), sl, rc, i+1)
	}
	data, err := pres.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if out := os.Getenv("PPTX_FALLBACK_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote %d-slide fallback deck to %s (%d bytes)", len(slides), out, len(data))
	}
}

func TestResolveGlyphInference(t *testing.T) {
	s := func(v string) *string { return &v }

	tests := []struct {
		name      string
		item      GridItem
		wantGlyph bool
	}{
		{"explicit valid", GridItem{Title: "X", Icon: s("shield")}, true},
		{"explicit alias", GridItem{Title: "X", Icon: s("security")}, true},
		{"infer from title", GridItem{Title: "Security"}, true},
		{"label is not an inference source", GridItem{Label: s("Deploy"), Title: "Foundation"}, false},
		{"infer from description", GridItem{Title: "Phase 1", Description: s("Query the database.")}, true},
		{"unknown explicit still infers", GridItem{Title: "Data access", Icon: s("bogus-name")}, true},
		{"no match", GridItem{Title: "Foundation"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveGlyph(tt.item) != nil
			if got != tt.wantGlyph {
				t.Errorf("resolveGlyph(%+v) glyph=%v, want %v", tt.item, got, tt.wantGlyph)
			}
		})
	}

	monogramCases := map[string]string{
		"Foundation":  "F",
		"  -Scale":    "S",
		"über":        "Ü",
		"日本語":         "日",
		"2024 Review": "2",
		"":            "",
		"   ":         "",
	}
	for in, want := range monogramCases {
		if got := monogram(in); got != want {
			t.Errorf("monogram(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderCardStylePresets(t *testing.T) {
	profile, err := loadBuiltinProfile(defaultProfileName)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	s := func(v string) *string { return &v }
	item := func(title, desc, icon string) GridItem {
		return GridItem{Title: title, Description: s(desc), Icon: s(icon)}
	}
	items := []GridItem{
		item("Agents", "Reasoning personas with a toolset.", "robot"),
		item("Data access", "Query your systems in plain language.", "database"),
		item("Security", "Role-based access on every call.", "shield"),
	}

	pres := pptxgen.New()
	rc0 := NewRenderContext(profile, nil, defaultTheme())
	pres.SetSlideSize(rc0.SlideW, rc0.SlideH)

	type variant struct{ title, style, corners string }
	variants := []variant{
		{"inherit (template default)", "", ""},
		{"card_style: elevated", cardPresetElevated, ""},
		{"card_style: flat", cardPresetFlat, ""},
		{"card_style: outlined", cardPresetOutlined, ""},
		{"card_style: filled (no border, no shadow)", cardPresetFilled, ""},
		{"card_corners: rounded", "", cardCornersRounded},
		{"elevated + rounded", cardPresetElevated, cardCornersRounded},
		{"filled + rounded", cardPresetFilled, cardCornersRounded},
	}
	for _, v := range variants {
		rc := NewRenderContext(profile, nil, defaultTheme())
		rc.CardPreset = v.style
		rc.CardCorners = v.corners
		title := v.title
		renderSlide(pres.AddSlide(), LayoutSlide{Layout: "icon_grid", Title: &title, Items: items}, rc, 1)
	}
	data, err := pres.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if out := os.Getenv("PPTX_PRESETS_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote preset showcase to %s", out)
	}
}

func TestCardStylePresets(t *testing.T) {
	profile, err := loadBuiltinProfile(defaultProfileName)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	newRC := func(preset string) *RenderContext {
		rc := NewRenderContext(profile, nil, defaultTheme())
		rc.CardPreset = preset
		rc.BgColor = "FFFFFF"
		return rc
	}

	theme := newRC("").Theme
	bg1 := normalizeColor(theme.Colors.Background1)
	bg2 := normalizeColor(theme.Colors.Background2)

	tests := []struct {
		preset     string
		wantShadow bool
		wantBorder bool
		wantFill   string
	}{
		{cardPresetElevated, true, true, bg1},
		{cardPresetFlat, false, true, bg1},
		{cardPresetOutlined, false, true, bg1},
		{cardPresetFilled, false, false, bg2},
	}
	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			rc := newRC(tt.preset)
			if got := rc.cardShadow() != nil; got != tt.wantShadow {
				t.Errorf("cardShadow present=%v, want %v", got, tt.wantShadow)
			}
			color, width := rc.cardBorder()
			if got := color != "" && width > 0; got != tt.wantBorder {
				t.Errorf("cardBorder present=%v (color=%q width=%v), want %v", got, color, width, tt.wantBorder)
			}
			if got := rc.cardFill(); got != tt.wantFill {
				t.Errorf("cardFill=%q, want %q", got, tt.wantFill)
			}
		})
	}

	if _, w := newRC(cardPresetOutlined).cardBorder(); w <= 0.75 {
		t.Errorf("outlined border width=%v, want > 0.75 (heavier than hairline)", w)
	}

	rounded := newRC(cardPresetElevated)
	rounded.CardCorners = cardCornersRounded
	if got := rounded.cardRadius(); got != roundedCardRadiusIn {
		t.Errorf("rounded corners radius=%v, want %v", got, roundedCardRadiusIn)
	}
	square := newRC(cardPresetElevated)
	square.CardCorners = cardCornersSquare
	if got := square.cardRadius(); got != 0 {
		t.Errorf("square corners radius=%v, want 0", got)
	}

	if isValidCardPreset("bogus") || isValidCardCorners("bogus") {
		t.Error("validation accepted a bogus value")
	}
	for _, p := range validCardPresets {
		if !isValidCardPreset(p) {
			t.Errorf("isValidCardPreset(%q) = false, want true", p)
		}
	}
	for _, c := range validCardCorners {
		if !isValidCardCorners(c) {
			t.Errorf("isValidCardCorners(%q) = false, want true", c)
		}
	}
}
