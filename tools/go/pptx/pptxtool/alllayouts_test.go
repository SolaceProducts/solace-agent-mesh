// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"os"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxgen"
)

func TestRenderAllLayouts(t *testing.T) {
	for _, name := range builtinProfileNames() {
		t.Run(name, func(t *testing.T) { renderAllLayouts(t, name) })
	}
}

func renderAllLayouts(t *testing.T, profileName string) {
	profile, err := loadBuiltinProfile(profileName)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	rc := NewRenderContext(profile, nil, defaultTheme())

	s := func(v string) *string { return &v }
	item := func(title, desc, icon string) GridItem {
		return GridItem{Title: title, Description: s(desc), Icon: s(icon)}
	}
	step := func(label, title, desc, icon string) GridItem {
		return GridItem{Label: s(label), Title: title, Description: s(desc), Icon: s(icon)}
	}
	f := s("Acme · all layouts")

	slides := []LayoutSlide{
		{Layout: "title", Kicker: s("Layout catalog"), Title: s("Every ==Acme== slide type"),
			Subtitle: s("One example of each layout"),
			Body:     s("A living reference deck generated straight from the pptx tool with the Solace profile.")},

		{Layout: "section", Kicker: s("Section divider"), Title: s("Structure & text layouts"),
			Subtitle: s("title · section · text · bullet_list")},

		{Layout: "text", Kicker: s("Text"), Title: s("A plain prose slide"),
			Body:   s("The **text** layout is for a paragraph or two of narrative. It supports inline *italic*, **bold**, ==highlight==, and `code` spans.\n\nUse it when a bulleted list would over-structure the point."),
			Footer: f},

		{Layout: "bullet_list", Kicker: s("Bullets"), Title: s("What the bullet list is for"),
			Bullets: []string{"The most common content slide", "One idea per line, kept short",
				"Teal markers, brand type", "Five to seven bullets max"}, Footer: f},

		{Layout: "two_column", Kicker: s("Two column"), Title: s("Two-column bullets"),
			Bullets1: []string{"Left column point", "Another left point", "Third left point"},
			Bullets2: []string{"Right column point", "Another right point", "Third right point"}, Footer: f},

		{Layout: "three_column", Kicker: s("Three column"), Title: s("Three-column bullets"),
			Bullets1: []string{"Column one", "Point A", "Point B"},
			Bullets2: []string{"Column two", "Point A", "Point B"},
			Bullets3: []string{"Column three", "Point A", "Point B"}, Footer: f},

		{Layout: "comparison", Kicker: s("Comparison"), Title: s("Side-by-side, in cards"),
			Header1: s("Before"), Header2: s("After"),
			Bullets1: []string{"Manual, ad-hoc requests", "No audit trail", "Slow turnaround"},
			Bullets2: []string{"Self-serve agents", "Full observability", "Minutes, not days"}, Footer: f},

		{Layout: "icon_grid", Kicker: s("Icon grid"), Title: s("Feature cards with medallions"),
			Items: []GridItem{
				item("Conversations", "Ask your systems in plain language.", "chat"),
				item("Security", "RBAC and identity on every call.", "shield"),
				item("Connectors", "Reusable, vetted integrations.", "plug"),
				item("Analytics", "Usage, cost and latency.", "chart-line"),
				item("Knowledge", "Grounded in your own docs.", "book"),
				item("Teams", "A path in for everyone.", "people"),
			}, Footer: f},

		{Layout: "process", Kicker: s("Process / flow"), Title: s("Arrow-connected steps"),
			Items: []GridItem{
				step("1 · Build", "Build", "Assemble from the catalog or author new tools.", "wrench"),
				step("2 · Evaluate", "Evaluate", "Run evals and UAT in a sandbox.", "flask"),
				step("3 · Deploy", "Deploy", "Promote under review and RBAC.", "rocket"),
			}, Footer: f},

		{Layout: "timeline", Kicker: s("Timeline"), Title: s("Phased path"),
			Items: []GridItem{
				step("Now", "Orient", "Lock the operating model.", "compass"),
				step("Next", "Foundation", "Observability and first wins.", "sprout"),
				step("Then", "Scale", "More domains onboard.", "scale"),
				step("Goal", "Maturity", "Autonomous, governed use.", "trophy"),
			}, Footer: f},

		{Layout: "callout", Kicker: s("Callout"), Title: s("Full-width emphasis banner"),
			Icon: s("bolt"),
			Body: s("==**Ship value fast.**== Visible wins early compound into the foundation everyone builds on."), Footer: f},

		{Layout: "big_number", Kicker: s("Big number"), Title: s("A metric that matters"),
			BigNumber: s("99.94%"), BigLabel: s("task success rate, rolling 30-day"), Footer: f},

		{Layout: "quote", QuoteText: s("Not a chatbot. A shared platform where the whole company builds, runs, and trusts AI."),
			Caption: s("The Acme vision"), Footer: f},

		{Layout: "table", Kicker: s("Table"), Title: s("Tabular data"),
			TableData: &TableData{
				Headers: []string{"Tier", "Access", "Gate"},
				Rows: [][]string{
					{"DEV", "Self-serve", "None"},
					{"STAGING", "On request", "Security & cost review"},
					{"PROD", "On request", "Evals + sign-off"},
				}}, Footer: f},

		{Layout: "chart", Kicker: s("Chart"), Title: s("A simple bar chart"),
			ChartData: &ChartData{ChartType: "bar", Title: s("Agents onboarded"),
				Series: []ChartSeries{{Name: s("Count"),
					Categories: []string{"Q1", "Q2", "Q3", "Q4"}, Values: []float64{3, 7, 12, 19}}}}, Footer: f},

		{Layout: "image_left", Kicker: s("Image left"), Title: s("Image on the left"),
			Body: s("The **image_left** layout pairs a visual with supporting copy. (Placeholder shown — pass an image via the images array.)"), Footer: f},

		{Layout: "image_right", Kicker: s("Image right"), Title: s("Image on the right"),
			Body: s("The **image_right** layout mirrors image_left. (Placeholder shown.)"), Footer: f},

		{Layout: "image_center", Kicker: s("Image center"), Title: s("Centered image"),
			Caption: s("A centered figure with a caption. (Placeholder shown.)"), Footer: f},

		{Layout: "image_full", Title: s("Full-bleed image (placeholder)")},

		{Layout: "closing", Kicker: s("Closing"), Title: s("That's every layout"), Footer: f},
	}

	pres := pptxgen.New()
	pres.SetTitle("Acme — all layouts")
	pres.SetDescription(iconAttribution)
	pres.SetSlideSize(rc.SlideW, rc.SlideH)
	for i, sl := range slides {
		renderSlide(pres.AddSlide(), sl, rc, i+1)
	}
	data, err := pres.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if out := os.Getenv("PPTX_ALL_OUT"); out != "" {
		if profileName != defaultProfileName {
			out = strings.TrimSuffix(out, ".pptx") + "-" + profileName + ".pptx"
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote %d-slide catalog to %s (%d bytes)", len(slides), out, len(data))
	}
}
