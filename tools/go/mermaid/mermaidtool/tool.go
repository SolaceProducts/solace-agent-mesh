// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mermaidtool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const toolName = "mermaid_diagram_generator"

const description = `Generates a PNG or SVG image from Mermaid diagram syntax and saves it as an artifact.

Args:
    mermaid_syntax: The Mermaid diagram syntax (string).
    output_filename: Optional desired name for the output file. Extension controls format when format is unset.
    format: Optional output format: "png" (default) or "svg". SVG is significantly faster and smaller.

Returns:
    ToolResult with output artifact details.

Optional: for a complex diagram, or if you are unsure it rendered correctly, you may load_artifact the generated PNG to inspect it visually (requires a vision-capable model) and regenerate with adjusted syntax if it looks wrong. Not required — skip it for simple diagrams.`

type Params struct {
	MermaidSyntax  string  `json:"mermaid_syntax" desc:"The Mermaid diagram syntax (string)."`
	OutputFilename *string `json:"output_filename,omitempty" desc:"Optional desired name for the output file."`
	Format         *string `json:"format,omitempty" desc:"Output format: \"png\" (default) or \"svg\"."`
}

var defaultRenderer svgRenderer = &chromedpRenderer{}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool(toolName, description, handle, sdk.WithEffect(sdk.EffectSideEffecting)),
	}
}

func handle(ctx context.Context, params Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	if strings.TrimSpace(params.MermaidSyntax) == "" {
		return sdk.Error("Mermaid syntax is required."), nil
	}

	format := resolveFormat(params.Format, params.OutputFilename)

	scale := 2

	_ = tc.SendStatus("Rendering Mermaid diagram...")

	out, err := defaultRenderer.RenderAll(ctx, params.MermaidSyntax, scale, format == "svg")
	if err != nil {
		if _, ok := errors.AsType[*errChromiumNotFound](err); ok {
			return sdk.Error("Mermaid rendering requires a Chromium/Chrome browser, which was not found on this host. Install it — Windows: winget install Google.Chrome; macOS: brew install --cask chromium; Linux: apt-get install chromium — or set SAM_CHROMIUM_PATH."), nil
		}
		return sdk.Error(fmt.Sprintf("Error rendering Mermaid diagram: %v", err)), nil
	}

	var (
		payload  []byte
		mimeType string
	)
	if format == "svg" {
		if out.SVG == "" {
			return sdk.Error("Failed to render Mermaid diagram. No SVG returned."), nil
		}
		payload = []byte(out.SVG)
		mimeType = "image/svg+xml"
	} else {
		if len(out.PNG) == 0 {
			return sdk.Error("Failed to render Mermaid diagram. No image data returned."), nil
		}
		payload = out.PNG
		mimeType = "image/png"
	}

	filename := finalFilename(params.OutputFilename, format)
	titleOut := naIfEmpty(out.Title)
	descOut := naIfEmpty(out.Desc)

	requested := "N/A"
	if params.OutputFilename != nil {
		requested = *params.OutputFilename
	}

	artifactDescription := fmt.Sprintf("%s diagram from Mermaid syntax", strings.ToUpper(format))
	if out.Title != "" {
		artifactDescription = fmt.Sprintf("%s diagram from Mermaid syntax: %s", strings.ToUpper(format), out.Title)
	}

	return sdk.OK(
		"Mermaid diagram rendered successfully.",
		sdk.WithData(map[string]any{
			"mermaid_title":       titleOut,
			"mermaid_description": descOut,
			"format":              format,
		}),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        filename,
			Content:     payload,
			MIMEType:    mimeType,
			Disposition: sdk.DispositionArtifact,
			Description: artifactDescription,
			Metadata: map[string]any{
				"description":          fmt.Sprintf("%s image generated from Mermaid syntax. Original requested filename: %s", strings.ToUpper(format), requested),
				"source_format":        "mermaid_syntax",
				"generation_tool":      "mermaid_diagram_generator (chromedp+mermaid.min.js)",
				"generation_timestamp": time.Now().UTC().Format(time.RFC3339),
				"mermaid_title":        titleOut,
				"mermaid_description":  descOut,
			},
		}),
	), nil
}

func resolveFormat(explicit *string, filename *string) string {
	if explicit != nil {
		switch strings.ToLower(strings.TrimSpace(*explicit)) {
		case "svg":
			return "svg"
		case "png", "":
			return "png"
		}
	}
	if filename != nil && strings.HasSuffix(strings.ToLower(*filename), ".svg") {
		return "svg"
	}
	return "png"
}

func ensureExtension(filename, format string) string {
	ext := "." + format
	if strings.HasSuffix(strings.ToLower(filename), ext) {
		return filename
	}
	if strings.HasSuffix(strings.ToLower(filename), ".png") || strings.HasSuffix(strings.ToLower(filename), ".svg") {
		filename = filename[:len(filename)-4]
	}
	return filename + ext
}

func finalFilename(requested *string, format string) string {
	if requested != nil && *requested != "" {
		return ensureExtension(*requested, format)
	}
	return fmt.Sprintf("mermaid_diagram_%s.%s", idgen.New(), format)
}

func naIfEmpty(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}
