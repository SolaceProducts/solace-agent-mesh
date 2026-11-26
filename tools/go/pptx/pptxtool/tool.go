// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("pptx_create_presentation",
			"Creates a PowerPoint (PPTX) presentation from a saved slide-spec artifact (a YAML or JSON array of slides), passed by filename as `slides`.",
			createPresentation,
			sdk.WithInstructions(createPresentationInstructions),
			sdk.WithDynamicSchema(createPresentationDynamicSchema),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func createPresentationDynamicSchema(config map[string]any) *sdk.SchemaOverride {
	profile := loadProfileFromConfigMap(config)
	if profile == nil {
		return nil
	}
	return &sdk.SchemaOverride{
		Instructions: createPresentationInstructions + activeProfileNote(profile),
	}
}

func activeProfileNote(profile *Profile) string {
	if len(profile.Assets) > 0 {
		return fmt.Sprintf("\n\nTemplate profile %q is active: it sets the colors, fonts, logo, decorations and footer, so leave `theme` out.", profile.Name)
	}
	return fmt.Sprintf("\n\nTemplate profile %q is active: it sets the colors, fonts and footer and adds no logo or decorations, so leave `theme` out.", profile.Name)
}

const createPresentationInstructions = `Two steps. First write the slide spec, a top-level YAML (preferred) or JSON array of slides, in a save block:
` + "`«««save_artifact: filename=\"deck-spec.yaml\" mime_type=\"application/yaml\" tags=\"__working\" description=\"...\"»»»`" + `
Then call this tool with ` + "`slides`" + ` set to that filename (` + "`deck-spec.yaml:2`" + ` pins a version). To change a deck, edit the spec with artifact_search_and_replace_regex and call the tool again.

Every slide needs ` + "`layout`" + `; an unknown layout is rejected. Layouts and the fields they use:
- title: title, subtitle, bg_image_ref
- section, closing: title, subtitle
- text: title, body
- bullet_list: title, bullets[]
- two_column, three_column: title, bullets1..bullets3 (or column1..column3 as plain text)
- comparison: title, header1, header2, bullets1, bullets2
- image_left, image_right: title, body, image_ref
- image_center: title, image_ref, caption
- image_full: image_ref, optional title
- table: title, table_data {headers: [..], rows: [[..]]}
- chart: title, chart_data {chart_type: bar|line|pie|doughnut|area, series: [{name, categories: [..], values: [..]}], show_legend, show_values}
- quote: quote_text, caption
- big_number: title, big_number, big_label, body
- timeline, process, icon_grid: title, items[] of {title, description, icon, label, pill}
- callout: title, body, icon
- blank
Any slide also takes notes, footer and background (hex).

Give every timeline, process and icon_grid item and every callout an ` + "`icon`" + `; without one its medallion renders empty. Pick the nearest of: robot, chat, database, headset, search, magic-wand, plug, chart-line, shield, lock, workflow, sitemap, people, book, wrench, gears, rocket, flask, gavel, scales, calculator, briefcase, sprout, trophy, code, puzzle, layers, expand, arrow-right, check-circle, compass, box, folder, window, target, lightbulb, eye, bolt, road.

Quote every string value in the YAML; an unquoted colon breaks it. Text fields accept **bold**, *italic* and ` + "`code`" + `. image_ref and bg_image_ref are 0-based indexes into the tool's ` + "`images`" + ` parameter. Leave ` + "`theme`" + ` and ` + "`style`" + ` out unless the user asks for a look. Open with a title slide, end with closing, and keep to 5-7 bullets a slide.

` + "```yaml" + `
- layout: title
  title: "Q3 Review"
- layout: bullet_list
  title: "Highlights"
  bullets: ["Shipped v2", "Cut p99 latency by 38%"]
- layout: closing
  title: "Thank you"
` + "```"
