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

func TestRenderIconShowcase(t *testing.T) {
	profile, err := loadBuiltinProfile(defaultProfileName)
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
	f := s("Acme")

	slides := []LayoutSlide{
		{Layout: "title", Kicker: s("Acme · AI platform"), Title: s("The ==Acme== icon set"),
			Subtitle: s("Bootstrap Icons, Solace navy"),
			Body:     s("Every medallion below is an MIT-licensed Bootstrap glyph, recolored and embedded in the tool.")},

		{Layout: "icon_grid", Kicker: s("Platform capabilities"), Title: s("What the mesh gives you"),
			Items: []GridItem{
				item("Agents", "Reasoning personas with their own toolset.", "robot"),
				item("Conversations", "Ask your systems in plain language.", "chat"),
				item("Data access", "Query Salesforce, Jira, GP and more.", "database"),
				item("Support intel", "Triage tickets and surface similar cases.", "headset"),
				item("Research", "Web and internal sources, one cited answer.", "search"),
				item("Agent Builder", "Create agents from a description.", "magic-wand"),
			}, Footer: f},

		{Layout: "icon_grid", Kicker: s("Governed by design"), Title: s("Trust, security & operations"),
			Items: []GridItem{
				item("RBAC", "Role-based access on every call.", "shield"),
				item("Identity", "Per-user permissions and audit.", "lock"),
				item("Connectors", "Vetted, reusable integrations.", "plug"),
				item("Observability", "Usage, cost and latency tracked.", "chart-line"),
				item("Knowledge", "Grounded in your own documents.", "book"),
				item("Teams", "A path in for everyone.", "people"),
			}, Footer: f},

		{Layout: "icon_grid", Kicker: s("Domains"), Title: s("Expertise at the edge"),
			Items: []GridItem{
				item("Engineering", "Build and ship agents.", "gears"),
				item("Finance", "Numbers, reconciliations, reports.", "calculator"),
				item("Legal", "Policy and contract lookups.", "gavel"),
				item("BizOps", "Cross-functional workflows.", "briefcase"),
				item("Sales", "Pipeline and account intel.", "chart-line"),
				item("Product", "Roadmap and research.", "box"),
			}, Footer: f},

		{Layout: "process", Kicker: s("The development model"), Title: s("Idea to trusted agent"),
			Items: []GridItem{
				step("1 · Build", "Build", "Assemble from the catalog or author new tools.", "wrench"),
				step("2 · Evaluate", "Evaluate", "Run evals and UAT in a sandbox.", "flask"),
				step("3 · Deploy", "Deploy", "Promote under review and RBAC.", "rocket"),
			}, Footer: f},

		{Layout: "timeline", Kicker: s("Where we are"), Title: s("A phased path"),
			Items: []GridItem{
				step("Now", "Orient", "Lock the operating model.", "compass"),
				step("Next", "Foundation", "Observability and first wins.", "sprout"),
				step("Then", "Scale", "More domains onboard.", "expand"),
				step("Goal", "Maturity", "Autonomous, governed use.", "trophy"),
			}, Footer: f},

		{Layout: "callout", Kicker: s("Best practice"), Title: s("Observable by default"),
			Icon: s("eye"),
			Body: s("==**If we can't see it, we can't trust it.**== Usage, cost and failures are tracked across the mesh from day one."), Footer: f},

		{Layout: "closing", Kicker: s("Join the build"), Title: s("Let's build it together"), Footer: f},
	}

	pres := pptxgen.New()
	pres.SetTitle("Acme — icon showcase")
	pres.SetDescription(iconAttribution)
	pres.SetSlideSize(rc.SlideW, rc.SlideH)
	for i, sl := range slides {
		renderSlide(pres.AddSlide(), sl, rc, i+1)
	}
	data, err := pres.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	if out := os.Getenv("PPTX_SHOWCASE_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote %d-slide showcase to %s (%d bytes)", len(slides), out, len(data))
	}
}
