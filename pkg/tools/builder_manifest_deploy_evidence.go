// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	componentIDKey              = "id"
	componentDeployedVersionKey = "deployed_version"
)

const deployEvidenceMaxHistory = 30

type deployEvidence struct {
	id              *yaml.Node
	deployedVersion *yaml.Node
}

type deployEvidenceNeed struct {
	id              bool
	deployedVersion bool
}

func (e deployEvidence) satisfies(need deployEvidenceNeed) bool {
	return (!need.id || e.id != nil) && (!need.deployedVersion || e.deployedVersion != nil)
}

func restoreDeployEvidence(ctx context.Context, manifestStr string, toolCtx *tool.Context) (string, []string, error) {
	if toolCtx == nil {
		return manifestStr, nil, nil
	}
	facade := toolCtx.Facade()
	if facade == nil {
		return manifestStr, nil, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(manifestStr), &doc); err != nil {
		return manifestStr, nil, fmt.Errorf("parse manifest: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return manifestStr, nil, nil
	}

	wanted := componentsMissingEvidence(doc.Content[0])
	if len(wanted) == 0 {
		return manifestStr, nil, nil
	}

	versions, err := facade.ListArtifactVersions(ctx, manifestFilename)
	if err != nil {
		return manifestStr, nil, fmt.Errorf("list manifest versions: %w", err)
	}

	found := map[string]deployEvidence{}
	unreadable := 0
	for i := len(versions) - 1; i >= 0 && len(versions)-i <= deployEvidenceMaxHistory; i-- {
		if ctx.Err() != nil {
			break
		}
		version := versions[i]
		prior, _, loadErr := facade.LoadArtifact(ctx, manifestFilename, &version)
		if loadErr != nil || len(prior) == 0 {
			unreadable++
			continue
		}
		if !collectDeployEvidence(prior, wanted, found) {
			unreadable++
		}
		if evidenceComplete(wanted, found) {
			break
		}
	}
	logUnreadableHistory(unreadable)
	if len(found) == 0 {
		return manifestStr, nil, nil
	}

	restored := applyDeployEvidence(doc.Content[0], found)
	if len(restored) == 0 {
		return manifestStr, nil, nil
	}

	repaired, err := yaml.Marshal(&doc)
	if err != nil {
		return manifestStr, nil, fmt.Errorf("marshal repaired manifest: %w", err)
	}
	return string(repaired), restored, nil
}

func componentsMissingEvidence(root *yaml.Node) map[string]deployEvidenceNeed {
	occurrences := map[string]int{}
	for _, comp := range manifestComponentNodes(root) {
		occurrences[scalarValue(mappingValue(comp, "name"))]++
	}

	missing := map[string]deployEvidenceNeed{}
	for _, comp := range manifestComponentNodes(root) {
		name := scalarValue(mappingValue(comp, "name"))
		if name == "" || occurrences[name] > 1 || scalarValue(mappingValue(comp, "origin")) == "platform" {
			continue
		}
		need := deployEvidenceNeed{
			id:              mappingValue(comp, componentIDKey) == nil,
			deployedVersion: mappingValue(comp, componentDeployedVersionKey) == nil,
		}
		if need.id || need.deployedVersion {
			missing[name] = need
		}
	}
	return missing
}

func collectDeployEvidence(priorYAML []byte, wanted map[string]deployEvidenceNeed, found map[string]deployEvidence) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal(priorYAML, &doc); err != nil {
		return false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return false
	}
	for _, comp := range manifestComponentNodes(doc.Content[0]) {
		name := scalarValue(mappingValue(comp, "name"))
		need, ok := wanted[name]
		if !ok {
			continue
		}
		have := found[name]
		if have.satisfies(need) {
			continue
		}
		if need.id && have.id == nil {
			have.id = platformIDNode(mappingValue(comp, componentIDKey))
		}
		if need.deployedVersion && have.deployedVersion == nil {
			have.deployedVersion = deployedVersionNode(mappingValue(comp, componentDeployedVersionKey))
		}
		if have.id != nil || have.deployedVersion != nil {
			found[name] = have
		}
	}
	return true
}

func platformIDNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if _, err := uuid.Parse(node.Value); err != nil {
		return nil
	}
	return node
}

func deployedVersionNode(node *yaml.Node) *yaml.Node {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return nil
	}
	if version, err := strconv.Atoi(node.Value); err != nil || version < 0 {
		return nil
	}
	return node
}

func evidenceComplete(wanted map[string]deployEvidenceNeed, found map[string]deployEvidence) bool {
	for name, need := range wanted {
		if !found[name].satisfies(need) {
			return false
		}
	}
	return true
}

func applyDeployEvidence(root *yaml.Node, found map[string]deployEvidence) []string {
	var restored []string
	for _, comp := range manifestComponentNodes(root) {
		name := scalarValue(mappingValue(comp, "name"))
		evidence, ok := found[name]
		if !ok {
			continue
		}
		repaired := false
		if evidence.id != nil && mappingValue(comp, componentIDKey) == nil {
			appendMapping(comp, componentIDKey, evidence.id)
			repaired = true
		}
		if evidence.deployedVersion != nil && mappingValue(comp, componentDeployedVersionKey) == nil {
			appendMapping(comp, componentDeployedVersionKey, evidence.deployedVersion)
			repaired = true
		}
		if repaired {
			restored = append(restored, name)
		}
	}
	return restored
}

func manifestComponentNodes(root *yaml.Node) []*yaml.Node {
	components := mappingValue(root, "components")
	if components == nil || components.Kind != yaml.SequenceNode {
		return nil
	}
	nodes := make([]*yaml.Node, 0, len(components.Content))
	for _, comp := range components.Content {
		if comp.Kind == yaml.MappingNode {
			nodes = append(nodes, comp)
		}
	}
	return nodes
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func scalarValue(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	return node.Value
}

func appendMapping(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: value.Tag, Value: value.Value, Style: value.Style},
	)
}

func logUnreadableHistory(count int) {
	if count == 0 {
		return
	}
	slog.Default().With("logger_name", "validate_build_manifest").Warn(
		"validate_build_manifest: skipped manifest versions that could not be read",
		"versionCount", count)
}

func logRestoredEvidence(restored []string) {
	if len(restored) == 0 {
		return
	}
	slog.Default().With("logger_name", "validate_build_manifest").Warn(
		"validate_build_manifest: restored deploy evidence the manifest had lost",
		"componentCount", len(restored), "components", restored)
}
