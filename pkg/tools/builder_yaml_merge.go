// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"gopkg.in/yaml.v3"
)

type YAMLMergeArtifactTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *YAMLMergeArtifactTool) Name() string { return "yaml_merge_artifact" }

func (t *YAMLMergeArtifactTool) Description() string {
	return "Deep-merges a YAML fragment into an existing config artifact. " +
		"Maps are merged recursively (preserving key order); when merging at a path, " +
		"arrays are concatenated (fragment items appended to existing); scalars are replaced. " +
		"At root level, arrays are replaced, except the 'components' list of the build manifest, " +
		"which (at the root or at path 'components') is merged by component name and type: " +
		"listed fields change on the matching component, a listed list field such as depends_on, tools or nodes " +
		"is replaced whole, and unlisted fields and components are kept. For the manifest, a path with a dot (components.0) and a YAML anchor, alias, explicit tag, non-text key or << key are refused, and comments in the fragment are dropped. A null full_validation_passed is removed, not written. " +
		"A fragment with a second YAML document (a --- section) is refused for every artifact. " +
		"Use the optional 'path' parameter to target a specific section " +
		"(e.g., 'apps.0.app_config.tools'). Saves the result as a new artifact version."
}

func (t *YAMLMergeArtifactTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"artifact_name": map[string]any{
				"type":        "string",
				"description": "The artifact filename containing the YAML config to modify.",
			},
			"yaml_fragment": map[string]any{
				"type":        "string",
				"description": "YAML fragment to deep-merge into the existing config.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional dot-separated path to merge at (e.g., 'apps.0.app_config.tools'). Integer segments index into arrays. If omitted, merges at the root.",
			},
		},
		Required: []string{"artifact_name", "yaml_fragment"},
	}
}

func (t *YAMLMergeArtifactTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "artifact_name", typ: "string", description: "Filename of the artifact that was merged into and re-saved."},
		resultField{name: "new_version", typ: "integer", description: "Version number of the artifact version this merge created."},
		resultField{name: "path", typ: "string", description: "The dot-separated merge target, echoed back. Empty when the fragment was merged at the document root."},
		resultField{name: "merge_report", typ: "string", description: "For the build manifest, which components were updated or added and which fields were not written. Empty for any other artifact."},
	)
}

func (t *YAMLMergeArtifactTool) RawStringArgs() []string {
	return []string{"yaml_fragment"}
}

func (t *YAMLMergeArtifactTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	artifactName, _ := args["artifact_name"].(string)
	yamlFragment, _ := args["yaml_fragment"].(string)
	path, _ := args["path"].(string)

	if artifactName == "" {
		return tool.Error("artifact_name is required"), nil
	}
	if yamlFragment == "" {
		return tool.Error("yaml_fragment is required and must be non-empty"), nil
	}

	defer lockBuildManifest(toolCtx, artifactName).Unlock()

	facade := toolCtx.Facade()

	content, mimeType, err := facade.LoadArtifact(ctx, artifactName, nil)
	if err != nil {
		return tool.Error("failed to load artifact: " + err.Error()), nil
	}
	if content == nil {
		return tool.Error("artifact not found: " + artifactName + createArtifactHint), nil
	}

	var existingDoc yaml.Node
	if parseErr := yaml.Unmarshal(content, &existingDoc); parseErr != nil {
		return tool.Error("failed to parse existing YAML: " + parseErr.Error()), nil
	}
	if existingDoc.Kind != yaml.DocumentNode || len(existingDoc.Content) == 0 {
		return tool.Error("existing YAML is empty or invalid"), nil
	}
	existingRoot := existingDoc.Content[0]

	fragmentRoot, fragErr := parseYAMLFragment(yamlFragment)
	if fragErr != nil {
		return tool.Error(fragErr.Error()), nil
	}

	isManifest := isBuildManifestArtifact(artifactName, mimeType)
	mergeNote, mergeErr := mergeYAMLFragment(existingRoot, fragmentRoot, path, isManifest)
	if mergeErr != nil {
		return tool.Error(mergeErr.Error()), nil
	}

	result, marshalErr := marshalMergedYAML(&existingDoc, isManifest, content)
	if marshalErr != nil {
		return tool.Error(marshalErr.Error()), nil
	}

	if mimeType == "" {
		mimeType = SAMAgentConfigMIMEType
	}

	if mimeType == SAMConnectorConfigMIMEType {
		return tool.Error("cannot YAML-merge into a connector config (JSON format); use direct edit instead"), nil
	}

	saveResult, err := facade.SaveArtifactWithMetadata(ctx, artifactName, result, mimeType, artifact.MetadataOpts{
		Tags: []string{"__working"},
	})
	if err != nil {
		return tool.Error("failed to save merged artifact: " + err.Error()), nil
	}

	return tool.OK(
		fmt.Sprintf("Merged YAML fragment into '%s'. New version: %d.%s", artifactName, saveResult.DataVersion, mergeNote),
		tool.WithData(map[string]any{
			"artifact_name": artifactName,
			"new_version":   saveResult.DataVersion,
			"path":          path,
			"merge_report":  strings.TrimSpace(mergeNote),
		}),
	), nil
}

func marshalMergedYAML(doc *yaml.Node, isManifest bool, stored []byte) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, errors.New("failed to marshal merged YAML: " + err.Error())
	}
	if err := enc.Close(); err != nil {
		return nil, errors.New("failed to marshal merged YAML: " + err.Error())
	}
	if isManifest {
		if err := manifestBytesProblem(stored, buf.Bytes()); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func parseYAMLFragment(fragment string) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(strings.NewReader(fragment))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("yaml_fragment is empty or invalid")
		}
		return nil, errors.New("failed to parse yaml_fragment: " + err.Error())
	}
	for {
		var extra yaml.Node
		err := decoder.Decode(&extra)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("failed to parse yaml_fragment: " + err.Error())
		}
		if !isEmptyDocument(&extra) {
			return nil, errors.New("yaml_fragment must be a single YAML document; remove the extra --- section; nothing was saved")
		}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("yaml_fragment is empty or invalid")
	}
	return doc.Content[0], nil
}

func isEmptyDocument(doc *yaml.Node) bool {
	return len(doc.Content) == 1 && doc.Content[0].Kind == yaml.ScalarNode && doc.Content[0].Tag == "!!null" && doc.Content[0].Value == ""
}

func mergeYAMLFragment(existingRoot, fragmentRoot *yaml.Node, path string, isManifest bool) (string, error) {
	if isManifest {
		return mergeManifestFragment(existingRoot, fragmentRoot, path)
	}
	if path != "" {
		return "", nodeMergeAtPath(existingRoot, path, fragmentRoot)
	}
	if existingRoot.Kind != yaml.MappingNode || fragmentRoot.Kind != yaml.MappingNode {
		return "", errors.New("yaml_fragment must be a YAML mapping when merging at root")
	}
	nodeMergeMapping(existingRoot, fragmentRoot)
	return "", nil
}

func nodeMergeMapping(base, overlay *yaml.Node) {
	for i := 0; i < len(overlay.Content)-1; i += 2 {
		overlayKey := overlay.Content[i]
		overlayVal := overlay.Content[i+1]

		baseIdx := nodeFindKey(base, overlayKey.Value)
		if baseIdx < 0 {
			base.Content = append(base.Content, overlayKey, overlayVal)
			continue
		}

		baseVal := base.Content[baseIdx+1]

		if baseVal.Kind == yaml.MappingNode && overlayVal.Kind == yaml.MappingNode {
			nodeMergeMapping(baseVal, overlayVal)
			continue
		}

		base.Content[baseIdx+1] = overlayVal
	}
}

func nodeFindKey(mapping *yaml.Node, key string) int {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func nodeMergeAtPath(base *yaml.Node, path string, fragment *yaml.Node) error {
	segments := strings.Split(path, ".")
	if len(segments) == 0 {
		return fmt.Errorf("path is empty")
	}

	current := base
	for i, seg := range segments[:len(segments)-1] {
		next := nodeNavigate(current, seg)
		if next == nil {
			return fmt.Errorf("path segment '%s' not found (at '%s')", seg, strings.Join(segments[:i+1], "."))
		}
		current = next
	}

	lastSeg := segments[len(segments)-1]

	switch current.Kind {
	case yaml.MappingNode:
		idx := nodeFindKey(current, lastSeg)
		if idx < 0 {
			current.Content = append(current.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: lastSeg, Tag: "!!str"},
				fragment,
			)
			return nil
		}
		existing := current.Content[idx+1]
		return nodeMergeValues(current, idx+1, existing, fragment)

	case yaml.SequenceNode:
		arrIdx, err := strconv.Atoi(lastSeg)
		if err != nil {
			return fmt.Errorf("path segment '%s' is not a valid array index", lastSeg)
		}
		if arrIdx < 0 || arrIdx >= len(current.Content) {
			return fmt.Errorf("array index %d out of bounds (length %d)", arrIdx, len(current.Content))
		}
		existing := current.Content[arrIdx]
		return nodeMergeValues(current, arrIdx, existing, fragment)

	default:
		return fmt.Errorf("cannot navigate into node of kind %d at path segment '%s'", current.Kind, lastSeg)
	}
}

func nodeMergeValues(parent *yaml.Node, idx int, existing, fragment *yaml.Node) error {
	if existing.Kind == yaml.MappingNode && fragment.Kind == yaml.MappingNode {
		nodeMergeMapping(existing, fragment)
		return nil
	}

	if existing.Kind == yaml.SequenceNode && fragment.Kind == yaml.SequenceNode {
		existing.Content = append(existing.Content, fragment.Content...)
		return nil
	}

	switch parent.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		parent.Content[idx] = fragment
	}
	return nil
}

func nodeNavigate(node *yaml.Node, seg string) *yaml.Node {
	switch node.Kind {
	case yaml.MappingNode:
		idx := nodeFindKey(node, seg)
		if idx < 0 {
			return nil
		}
		return node.Content[idx+1]
	case yaml.SequenceNode:
		arrIdx, err := strconv.Atoi(seg)
		if err != nil || arrIdx < 0 || arrIdx >= len(node.Content) {
			return nil
		}
		return node.Content[arrIdx]
	default:
		return nil
	}
}

func deepMergeYAML(base, overlay map[string]any) {
	for key, overlayVal := range overlay {
		baseVal, exists := base[key]
		if !exists {
			base[key] = overlayVal
			continue
		}

		baseMap, baseIsMap := baseVal.(map[string]any)
		overlayMap, overlayIsMap := overlayVal.(map[string]any)
		if baseIsMap && overlayIsMap {
			deepMergeYAML(baseMap, overlayMap)
			continue
		}

		base[key] = overlayVal
	}
}

func mergeAtPath(base map[string]any, path string, value any) error {
	segments := strings.Split(path, ".")
	if len(segments) == 0 {
		return fmt.Errorf("path is empty")
	}

	var current any = base
	for i, seg := range segments[:len(segments)-1] {
		current = navigateSegment(current, seg)
		if current == nil {
			return fmt.Errorf("path segment '%s' not found (at '%s')", seg, strings.Join(segments[:i+1], "."))
		}
	}

	lastSeg := segments[len(segments)-1]

	switch parent := current.(type) {
	case map[string]any:
		existingVal, exists := parent[lastSeg]
		if exists {
			existingMap, existIsMap := existingVal.(map[string]any)
			valueMap, valIsMap := value.(map[string]any)
			if existIsMap && valIsMap {
				deepMergeYAML(existingMap, valueMap)
				return nil
			}
		}
		parent[lastSeg] = value
		return nil
	case []any:
		idx, err := strconv.Atoi(lastSeg)
		if err != nil {
			return fmt.Errorf("path segment '%s' is not a valid array index", lastSeg)
		}
		if idx < 0 || idx >= len(parent) {
			return fmt.Errorf("array index %d out of bounds (length %d)", idx, len(parent))
		}
		existingMap, existIsMap := parent[idx].(map[string]any)
		valueMap, valIsMap := value.(map[string]any)
		if existIsMap && valIsMap {
			deepMergeYAML(existingMap, valueMap)
			return nil
		}
		parent[idx] = value
		return nil
	default:
		return fmt.Errorf("cannot navigate into %T at path segment '%s'", current, lastSeg)
	}
}

func navigateSegment(current any, seg string) any {
	switch c := current.(type) {
	case map[string]any:
		return c[seg]
	case []any:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= len(c) {
			return nil
		}
		return c[idx]
	default:
		return nil
	}
}
