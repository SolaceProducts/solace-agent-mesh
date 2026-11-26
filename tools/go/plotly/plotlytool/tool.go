// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package plotlytool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"gopkg.in/yaml.v3"
)

const toolName = "create_chart_from_plotly_config"

const description = `Render a Plotly configuration (a JSON or YAML string) to a static image and save it as an artifact. For a complex chart, or if you are unsure it rendered correctly, you may load_artifact the image to inspect it (needs a vision-capable model) and regenerate it if it looks wrong; skip that for simple charts.`

type Params struct {
	ConfigContent  string  `json:"config_content" desc:"The Plotly configuration (JSON or YAML) as a string."`
	ConfigFormat   string  `json:"config_format" desc:"The format of config_content: 'json' or 'yaml'."`
	OutputFilename string  `json:"output_filename" desc:"The desired filename for the output image artifact."`
	OutputFormat   *string `json:"output_format,omitempty" desc:"Image format: 'png' (default), 'jpg', 'jpeg', 'webp', 'svg', or 'pdf'."`
}

var mimeTypes = map[string]string{
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"webp": "image/webp",
	"svg":  "image/svg+xml",
	"pdf":  "application/pdf",
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool(toolName, description, handle, sdk.WithEffect(sdk.EffectSideEffecting)),
	}
}

func handle(ctx context.Context, params Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	figJSON, err := normalizeConfig(params.ConfigContent, params.ConfigFormat)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	format := "png"
	if params.OutputFormat != nil && strings.TrimSpace(*params.OutputFormat) != "" {
		format = strings.ToLower(strings.TrimSpace(*params.OutputFormat))
	}
	mimeType, ok := mimeTypes[format]
	if !ok {
		return sdk.Error(fmt.Sprintf("Unsupported output_format %q. Supported formats: png, jpg, jpeg, webp, svg, pdf.", format)), nil
	}

	_ = tc.SendStatus("Rendering Plotly chart...")

	payload, err := defaultRenderer.Render(ctx, figJSON, renderFormat(format), 1)
	if err != nil {
		var notFound *errChromiumNotFound
		if errors.As(err, &notFound) {
			return sdk.Error("Chart rendering requires a Chromium/Chrome browser, which was not found on this host. Install it — Windows: winget install Google.Chrome; macOS: brew install --cask chromium; Linux: apt-get install chromium — or set SAM_CHROMIUM_PATH."), nil
		}
		return sdk.Error(fmt.Sprintf("Failed to render Plotly chart: %v", err)), nil
	}
	if len(payload) == 0 {
		return sdk.Error("Failed to render Plotly chart. No image data returned."), nil
	}

	filename := ensureExtension(params.OutputFilename, format)
	artifactDescription := fmt.Sprintf("Chart generated from Plotly config (%s)", params.ConfigFormat)

	return sdk.OK(
		"Chart image created successfully.",
		sdk.WithData(map[string]any{
			"source_format": params.ConfigFormat,
			"output_format": format,
		}),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        filename,
			Content:     payload,
			MIMEType:    mimeType,
			Disposition: sdk.DispositionArtifact,
			Description: artifactDescription,
			Metadata: map[string]any{
				"description":          artifactDescription,
				"source_format":        params.ConfigFormat,
				"output_format":        format,
				"generation_tool":      toolName,
				"generation_timestamp": time.Now().UTC().Format(time.RFC3339),
			},
		}),
	), nil
}

func normalizeConfig(content, format string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("config_content is required")
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		var probe any
		if err := json.Unmarshal([]byte(content), &probe); err != nil {
			return "", fmt.Errorf("failed to parse Plotly config as JSON: %v", err)
		}
		return content, nil
	case "yaml":
		var doc any
		if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
			return "", fmt.Errorf("failed to parse Plotly config as YAML: %v", err)
		}
		if _, ok := doc.(map[string]any); !ok {
			return "", fmt.Errorf("YAML content did not parse into a mapping")
		}
		asJSON, err := json.Marshal(doc)
		if err != nil {
			return "", fmt.Errorf("failed to convert YAML config to JSON: %v", err)
		}
		return string(asJSON), nil
	default:
		return "", fmt.Errorf("invalid config_format %q: expected 'json' or 'yaml'", format)
	}
}

func renderFormat(format string) string {
	if format == "jpg" {
		return "jpeg"
	}
	return format
}

func ensureExtension(filename, format string) string {
	base := strings.TrimSpace(filename)
	if base == "" {
		base = "plotly_chart_" + idgen.New()
	}
	ext := "." + format
	if strings.HasSuffix(strings.ToLower(base), ext) {
		return base
	}
	return base + ext
}
