// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ReinterpretArtifactTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *ReinterpretArtifactTool) Name() string { return "reinterpret_artifact" }

func (t *ReinterpretArtifactTool) Description() string {
	return `Save a copy of an artifact's bytes, unchanged, under a new filename or MIME type, so the data tools accept JSON, YAML or CSV that is labelled as something else: a ".stim" task log is YAML detected as plain text, so reinterpret it as ".yaml". It relabels, never converts. The original is kept, and the result carries the new artifact's schema, so you can query it straight away.`
}

func (t *ReinterpretArtifactTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_filename": map[string]any{
				"type":        "string",
				"description": "Filename of the artifact to reinterpret (with optional ':version' suffix).",
			},
			"output_filename": map[string]any{
				"type":        "string",
				"description": "New filename; its extension sets the type when 'mime_type' is omitted. Defaults to the input's base name with an extension matching 'mime_type'.",
				"nullable":    true,
			},
			"mime_type": map[string]any{
				"type":        "string",
				"description": "Target MIME type, e.g. 'application/x-yaml', 'application/json', 'text/csv'. If omitted, it is derived from 'output_filename'.",
				"nullable":    true,
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional description for the new artifact's metadata.",
				"nullable":    true,
			},
		},
		Required: []string{"input_filename"},
	}
}

func (t *ReinterpretArtifactTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "output_filename", typ: "string",
			description: "Name of the reinterpreted artifact."},
		resultField{name: "output_version", typ: "integer",
			description: "Version of the reinterpreted artifact."},
		resultField{name: "mime_type", typ: "string",
			description: "MIME type the artifact was reinterpreted as."},
		resultField{name: "status", typ: "string",
			description: "Always \"success\" on this path."},
		resultField{name: "schema", anyType: true,
			description: "Schema inferred from the reinterpreted data. Its shape follows the data, so it is not fixed."},
	)
}

func (t *ReinterpretArtifactTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	inputFilename, _ := args["input_filename"].(string)
	if inputFilename == "" {
		return invalidArgument("input_filename is required"), nil
	}
	outputFilename, _ := args["output_filename"].(string)
	targetMIME, _ := args["mime_type"].(string)
	description, _ := args["description"].(string)

	if outputFilename == "" && targetMIME == "" {
		return invalidArgument("provide output_filename (e.g. 'log.yaml'), mime_type (e.g. 'application/x-yaml'), or both"), nil
	}

	facade := toolCtx.Facade()
	artFilename, version := parseFilenameVersion(inputFilename)

	content, _, err := facade.LoadArtifact(ctx, artFilename, version)
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to load artifact '%s': %s", inputFilename, err)), nil
	}
	if content == nil {
		return missingArtifactError(ctx, facade, artFilename, version), nil
	}

	if targetMIME == "" {
		targetMIME = artifact.DetectMIMEType(outputFilename, content)
	}
	if artifact.IsGenericMIME(targetMIME) {
		return invalidArgument(fmt.Sprintf("could not resolve a specific MIME type (got %q); pass a concrete mime_type (e.g. 'application/x-yaml') or an output_filename with a known extension", targetMIME)), nil
	}

	if outputFilename == "" {
		ext := extensionForMIME(targetMIME)
		if ext == "" {
			return invalidArgument(fmt.Sprintf("no default extension for mime_type '%s'; pass output_filename explicitly", targetMIME)), nil
		}
		base := strings.TrimSuffix(artFilename, filepath.Ext(artFilename))
		outputFilename = base + ext
	}
	outputFilename = sanitizeFilename(outputFilename)

	if outputFilename == "" || !artifact.IsFilenameSafe(outputFilename) {
		return invalidArgument(fmt.Sprintf("output_filename %q is not a valid artifact name", outputFilename)), nil
	}
	if artifact.IsInternalArtifact(outputFilename) {
		return invalidArgument(fmt.Sprintf("output_filename %q is reserved for internal use", outputFilename)), nil
	}

	if outputFilename == sanitizeFilename(artFilename) {
		return invalidArgument("output_filename must differ from input_filename so the original artifact is preserved"), nil
	}

	metaDesc := description
	if metaDesc == "" {
		metaDesc = fmt.Sprintf("Reinterpretation of '%s' as %s", artFilename, targetMIME)
	}

	schema := artifact.InferSchema(content, targetMIME, 0, 0)

	saveResult, saveErr := facade.SaveArtifactWithMetadata(ctx, outputFilename, content, targetMIME, artifact.MetadataOpts{
		Description:    metaDesc,
		ExplicitSchema: schema,
		Custom: map[string]any{
			"source_artifact":  artFilename,
			"reinterpreted_as": targetMIME,
		},
	})
	if saveErr != nil {
		return tool.Error(fmt.Sprintf("Failed to save reinterpreted artifact: %s", saveErr)), nil
	}

	return tool.OK(
		fmt.Sprintf("Reinterpreted '%s' as %s, saved as '%s' v%d. The discovered schema is included below — you can run the data-analysis tools on it directly.", artFilename, targetMIME, outputFilename, saveResult.DataVersion),
		tool.WithData(map[string]any{
			"status":          "success",
			"output_filename": outputFilename,
			"output_version":  saveResult.DataVersion,
			"mime_type":       targetMIME,
			"schema":          schema,
		}),
	), nil
}

func extensionForMIME(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	switch strings.TrimSpace(base) {
	case "application/json":
		return ".json"
	case "text/csv":
		return ".csv"
	case "application/xml", "text/xml":
		return ".xml"
	case "text/markdown":
		return ".md"
	}
	if artifact.IsYAMLMIME(base) {
		return ".yaml"
	}
	if artifact.IsJSONMIME(base) {
		return ".json"
	}
	return ""
}
