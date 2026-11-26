// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"gopkg.in/yaml.v3"
)

type MergeStructuredDataTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *MergeStructuredDataTool) Name() string { return "merge_structured_data" }

func (t *MergeStructuredDataTool) Description() string {
	return `Deep-merge a patch into a JSON or YAML artifact (RFC 7396) and save it as a new version. Objects merge at every level and unmentioned keys are kept; arrays and scalars are replaced whole. A key set to null is deleted; to store a literal null, use artifact_search_and_replace_regex. Example: patch {"server": {"port": 9090, "debug": null, "tls": true}} changes port, deletes debug, adds tls and keeps the other server keys.

In the Builder's build_manifest.yaml the components list is merged by component name and type: a patch entry changes only the fields it lists (a listed list field such as depends_on, tools or nodes is replaced whole), and components not in the patch are kept. For the manifest, a path with a dot (components.0), a numeric :N version suffix on input_filename and a << key are refused, and a null full_validation_passed is removed, not written.`
}

func (t *MergeStructuredDataTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_filename": map[string]any{
				"type":        "string",
				"description": "Filename of the JSON or YAML artifact to modify (with optional ':version' suffix).",
			},
			"patch": map[string]any{
				"type":        "object",
				"description": "The patch object to deep-merge into the artifact. Use null values to delete keys.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Dot-separated path to merge at, e.g. 'server.logging'; integer segments index into arrays. Omit to merge at the root.",
				"nullable":    true,
			},
			"result_description": map[string]any{
				"type":        "string",
				"description": "Optional description for the result artifact's metadata.",
				"nullable":    true,
			},
		},
		Required: []string{"input_filename", "patch"},
	}
}

func (t *MergeStructuredDataTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "output_filename", typ: "string",
			description: "Name of the artifact the merge patch was applied to."},
		resultField{name: "output_version", typ: "integer",
			description: "New version created by the merge."},
		resultField{name: "format", typ: "string",
			description: "Serialization of the merged artifact: \"JSON\" or \"YAML\"."},
		resultField{name: "status", typ: "string",
			description: "Always \"success\" on this path."},
		resultField{name: "message", typ: "string",
			description: "Human-readable summary of the merge."},
		resultField{name: "merge_path", typ: "string", optional: true,
			description: "Present only when the patch targeted a sub-path rather than the document root."},
	)
}

func (t *MergeStructuredDataTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	inputFilename, _ := args["input_filename"].(string)
	if inputFilename == "" {
		return tool.Error("input_filename is required"), nil
	}
	patch, _ := args["patch"].(map[string]any)
	if patch == nil {
		return tool.Error("patch is required and must be a JSON object"), nil
	}
	path, _ := args["path"].(string)
	resultDescription, _ := args["result_description"].(string)

	facade := toolCtx.Facade()

	artFilename, version := parseFilenameVersion(inputFilename)
	defer lockBuildManifest(toolCtx, artFilename).Unlock()

	content, mimeType, err := facade.LoadArtifact(ctx, artFilename, version)
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to load artifact '%s': %s", inputFilename, err)), nil
	}
	if content == nil {
		return tool.Error(fmt.Sprintf("Artifact '%s' not found", inputFilename)), nil
	}

	mimeType = artifact.ResolveDataMIME(artFilename, mimeType, content)

	existing, isYAML, parseErr := parseStructuredData(content, mimeType)
	if parseErr != nil {
		return tool.Error(fmt.Sprintf("Failed to parse '%s': %s", inputFilename, parseErr)), nil
	}

	mergeNote, mergeErr := mergeStructuredPatch(existing, patch, path, isBuildManifestArtifact(artFilename, mimeType), version != nil)
	if mergeErr != nil {
		return tool.Error(mergeErr.Error()), nil
	}

	var result []byte
	if isYAML {
		result, err = marshalMergedStructuredYAML(existing, isBuildManifestArtifact(artFilename, mimeType), content)
	} else {
		result, err = json.MarshalIndent(existing, "", "  ")
	}
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to serialize result: %s", err)), nil
	}

	var sourceVersion int
	if version != nil {
		sourceVersion = *version
	} else {
		if versions, vErr := facade.ListArtifactVersions(ctx, artFilename); vErr == nil && len(versions) > 0 {
			sourceVersion = versions[len(versions)-1]
		}
	}

	metaDesc := resultDescription
	if metaDesc == "" {
		metaDesc = fmt.Sprintf("Merge patch applied to %s", artFilename)
		if path != "" {
			metaDesc += fmt.Sprintf(" at path '%s'", path)
		}
	}

	saveResult, saveErr := facade.SaveArtifactWithMetadata(ctx, artFilename, result, mimeType, artifact.MetadataOpts{
		Description: metaDesc,
		Custom: map[string]any{
			"source_artifact_version": sourceVersion,
			"merge_path":              path,
		},
	})
	if saveErr != nil {
		return tool.Error(fmt.Sprintf("Failed to save merged artifact: %s", saveErr)), nil
	}

	formatName := "JSON"
	if isYAML {
		formatName = "YAML"
	}

	respData := map[string]any{
		"status":          "success",
		"message":         fmt.Sprintf("Merge patch applied successfully to '%s'.%s", artFilename, mergeNote),
		"output_filename": artFilename,
		"output_version":  saveResult.DataVersion,
		"format":          formatName,
	}
	if path != "" {
		respData["merge_path"] = path
	}

	return tool.OK(
		fmt.Sprintf("Merge patch applied to '%s'. New version: %d.%s", artFilename, saveResult.DataVersion, mergeNote),
		tool.WithData(respData),
	), nil
}

func parseStructuredData(content []byte, mimeType string) (map[string]any, bool, error) {
	switch {
	case artifact.IsJSONMIME(mimeType):
		var data map[string]any
		if err := json.Unmarshal(content, &data); err != nil {
			return nil, false, fmt.Errorf("invalid JSON: %w", err)
		}
		if data == nil {
			data = make(map[string]any)
		}
		return data, false, nil

	case artifact.IsYAMLMIME(mimeType):
		var data map[string]any
		if err := yaml.Unmarshal(content, &data); err != nil {
			return nil, true, fmt.Errorf("invalid YAML: %w", err)
		}
		if data == nil {
			data = make(map[string]any)
		}
		return data, true, nil

	default:
		return nil, false, fmt.Errorf("unsupported file type '%s'. merge_structured_data only works with JSON or YAML files. For CSV or other formats, use artifact_search_and_replace_regex instead", mimeType)
	}
}

func mergePatch(target, patch map[string]any) {
	for key, patchVal := range patch {
		if patchVal == nil {
			delete(target, key)
			continue
		}

		patchMap, patchIsMap := patchVal.(map[string]any)
		if !patchIsMap {
			target[key] = patchVal
			continue
		}

		targetVal, exists := target[key]
		if exists {
			if targetMap, targetIsMap := targetVal.(map[string]any); targetIsMap {
				mergePatch(targetMap, patchMap)
				continue
			}
		}
		newMap := make(map[string]any)
		mergePatch(newMap, patchMap)
		target[key] = newMap
	}
}

func mergeStructuredPatch(existing, patch map[string]any, path string, isManifest, pinned bool) (string, error) {
	switch {
	case isManifest:
		return mergeManifestPatch(existing, patch, path, pinned)
	case path != "":
		return "", mergePatchAtPath(existing, path, patch)
	default:
		mergePatch(existing, patch)
		return "", nil
	}
}

func mergePatchAtPath(base map[string]any, path string, patch map[string]any) error {
	segments := strings.Split(path, ".")
	if len(segments) == 0 {
		return fmt.Errorf("path is empty")
	}

	var current any = base
	for i, seg := range segments {
		next := navigateSegment(current, seg)
		if next == nil {
			return fmt.Errorf("path segment '%s' not found (at '%s')", seg, strings.Join(segments[:i+1], "."))
		}
		current = next
	}

	targetMap, ok := current.(map[string]any)
	if !ok {
		kind := "a scalar value"
		if _, isArr := current.([]any); isArr {
			kind = "an array"
		}
		return fmt.Errorf("value at path '%s' is %s, not an object — merge_structured_data can only merge into objects. To modify arrays or scalar values, use artifact_search_and_replace_regex instead", path, kind)
	}

	mergePatch(targetMap, patch)
	return nil
}

func marshalMergedStructuredYAML(data any, isManifest bool, stored []byte) ([]byte, error) {
	result, err := yaml.Marshal(data)
	if err != nil {
		return nil, err
	}
	if isManifest {
		if err := manifestBytesProblem(stored, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}
