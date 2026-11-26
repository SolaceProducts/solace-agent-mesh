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

func TestRenderStyledPreview(t *testing.T) {
	profile, err := loadBuiltinProfile(defaultProfileName)
	if err != nil {
		t.Fatalf("load profile: %v", err)
	}
	rc := NewRenderContext(profile, nil, defaultTheme())

	sptr := func(s string) *string { return &s }
	item := func(title, desc, icon string) GridItem {
		return GridItem{Title: title, Description: sptr(desc), Icon: sptr(icon)}
	}
	labeled := func(label, title, desc, icon string) GridItem {
		return GridItem{Label: sptr(label), Title: title, Description: sptr(desc), Icon: sptr(icon)}
	}

	slides := []LayoutSlide{
		{
			Layout:   "title",
			Kicker:   sptr("Acme · AI platform"),
			Title:    sptr("==Acme=="),
			Subtitle: sptr("Solace's in-house AI platform"),
			Body:     sptr("One collaborative AI framework — from interactive conversational tools to fully autonomous agents running real business processes."),
		},
		{
			Layout: "icon_grid",
			Kicker: sptr("Capabilities today"),
			Title:  sptr("What Acme can do now"),
			Items: []GridItem{
				item("Conversations with data", "Ask Salesforce, Jira, Maxio, GP, Solace Docs and more in plain language.", "database"),
				item("Support intelligence", "Triage tickets, find similar cases, summarize trends across Salesforce.", "support"),
				item("Research & synthesis", "Pull from the web and internal sources into one cited answer.", "research"),
				item("Agent Builder", "Create agents from a description — no YAML — and deploy from the UI.", "builder"),
				item("Connector catalog", "Vetted, reusable integrations any agent can be granted.", "connector"),
				item("Observability", "Usage, cost, latency and failures tracked across the mesh.", "observability"),
			},
			Footer: sptr("Acme"),
		},
		{
			Layout: "process",
			Kicker: sptr("The development model"),
			Title:  sptr("How an idea becomes a trusted agent"),
			Items: []GridItem{
				labeled("1 · Build", "Build", "Describe the need; assemble from the catalog or author new tools.", "wrench"),
				labeled("2 · Evaluate", "Evaluate", "Test in an ephemeral sandbox, then run evals and UAT against real cases.", "flask"),
				labeled("3 · Deploy", "Deploy", "Promote through DEV → STAGING → PROD under review and RBAC.", "rocket"),
			},
			Footer: sptr("Acme"),
		},
		{
			Layout: "timeline",
			Kicker: sptr("Where we are"),
			Title:  sptr("A phased path, balancing foundation and wins"),
			Items: []GridItem{
				labeled("Now", "Orient & decide", "Lock the operating model; stand up policies and the watch-list.", "compass"),
				labeled("Next", "Foundation + first wins", "Reliable hosted experience; observability; 1–2 lighthouse agents.", "sprout"),
				labeled("Then", "Scale", "Connector catalog, identity passthrough, more domains onboard.", "scale"),
				labeled("Goal", "Maturity", "Autonomous, action-taking use cases under stronger governance.", "trophy"),
			},
			Footer: sptr("Acme"),
		},
		{
			Layout:   "comparison",
			Kicker:   sptr("Getting to production"),
			Title:    sptr("Self-serve to build, gated to ship"),
			Header1:  sptr("DEV — self-serve"),
			Header2:  sptr("STAGING & PROD — gated"),
			Bullets1: []string{"Domains build and deploy freely to test.", "Own their source and move fast.", "No central bottleneck."},
			Bullets2: []string{"Platform team promotes on request.", "Security, cost & data-scope review.", "Promoted after the domain's evals and sign-off."},
			Footer:   sptr("Acme"),
		},
		{
			Layout: "callout",
			Kicker: sptr("Ownership model"),
			Title:  sptr("Agentic domains — expertise at the edge"),
			Icon:   sptr("people"),
			Body:   sptr("==**Deputize the experts.**== Push ownership to where the knowledge lives; the platform backstops it centrally with infrastructure, standards, and safety."),
			Footer: sptr("Acme"),
		},
		{
			Layout:    "big_number",
			Kicker:    sptr("Reliability"),
			Title:     sptr("Proven in production"),
			BigNumber: sptr("99.94%"),
			BigLabel:  sptr("task success rate, rolling 30-day"),
			Footer:    sptr("Acme"),
		},
		{
			Layout:    "quote",
			QuoteText: sptr("Not a chatbot. A shared platform where the whole company builds, runs, and trusts AI on Solace's own data."),
			Caption:   sptr("The Acme vision"),
			Footer:    sptr("Acme"),
		},
		{
			Layout: "closing",
			Title:  sptr("Let's build it together"),
			Kicker: sptr("Join the build"),
			Footer: sptr("Acme"),
		},
	}

	pres := pptxgen.New()
	pres.SetTitle("Acme styled preview")
	pres.SetDescription(iconAttribution)
	pres.SetSlideSize(rc.SlideW, rc.SlideH)
	for i, sl := range slides {
		renderSlide(pres.AddSlide(), sl, rc, i+1)
	}
	data, err := pres.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}

	if out := os.Getenv("PPTX_PREVIEW_OUT"); out != "" {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatalf("write preview: %v", err)
		}
		t.Logf("wrote preview to %s (%d bytes)", out, len(data))
	}
}
