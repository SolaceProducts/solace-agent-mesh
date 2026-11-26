// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const manifestComponentsKey = "components"

const canonicalUUIDLength = 36

const mergeKey = "<<"

func isBuildManifestArtifact(filename, mimeType string) bool {
	return filename == manifestFilename || mimeType == SAMBuildManifestMIMEType
}

type manifestMergeReport struct {
	updated      []string
	added        []string
	ignored      []string
	stampRetired bool
	stampRefused bool
}

const maxReportedNames = 10

func capped(items []string) string {
	return cappedJoin(items, "; ")
}

func cappedJoin(items []string, separator string) string {
	if len(items) <= maxReportedNames {
		return strings.Join(items, separator)
	}
	return strings.Join(items[:maxReportedNames], separator) + fmt.Sprintf("%sand %d more", separator, len(items)-maxReportedNames)
}

func (r *manifestMergeReport) summary() string {
	var parts []string
	if len(r.updated) > 0 {
		parts = append(parts, "Updated: "+capped(r.updated)+".")
	}
	if len(r.added) > 0 {
		parts = append(parts, "Added: "+capped(r.added)+".")
	}
	if len(r.ignored) > 0 {
		parts = append(parts, "Not written, the Builder UI owns them: "+capped(r.ignored)+".")
	}
	if r.stampRetired {
		parts = append(parts, fullValidationPassedKey+" is no longer true: only validate_build_manifest sets it, so Save and Deploy wait for it to run again.")
	}
	if r.stampRefused {
		parts = append(parts, fullValidationPassedKey+" was not written: only validate_build_manifest sets it.")
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func mergeManifestFragment(existingRoot, fragmentRoot *yaml.Node, path string) (string, error) {
	var probe any
	if err := fragmentRoot.Decode(&probe); err != nil {
		return "", withNothingChanged(fmt.Errorf("yaml_fragment is not valid: %w", err))
	}
	if problem := fragmentSyntaxProblem(fragmentRoot); problem != "" {
		return "", withNothingChanged(errors.New(problem))
	}
	dropComments(fragmentRoot)
	if err := manifestPathProblem(path); err != nil {
		return "", withNothingChanged(err)
	}
	report := &manifestMergeReport{}
	stampWasEarned := stampNodeIsTrue(mappingValue(existingRoot, fullValidationPassedKey))
	storedVersions := storedValidationVersions(existingRoot)
	wiringBefore := nodeComponentWiring(existingRoot)
	var err error
	switch {
	case path == "":
		if existingRoot.Kind != yaml.MappingNode || fragmentRoot.Kind != yaml.MappingNode {
			return "", withNothingChanged(errors.New("yaml_fragment must be a YAML mapping when merging at root"))
		}
		err = mergeManifestRoot(existingRoot, fragmentRoot, report)
	case path == manifestComponentsKey && existingRoot.Kind == yaml.MappingNode:
		err = mergeManifestComponentsAt(existingRoot, fragmentRoot, report)
	default:
		err = nodeMergeAtPath(existingRoot, path, fragmentRoot)
	}
	if err != nil {
		return "", withNothingChanged(err)
	}
	removeNullStamp(existingRoot)
	rewired := wiringBefore != nodeComponentWiring(existingRoot)
	if settleStamp(report, stampWasEarned, stampNodeIsTrue(mappingValue(existingRoot, fullValidationPassedKey)), rewired) {
		carryValidationVersionsNode(existingRoot, storedVersions)
	} else {
		removeYAMLKey(existingRoot, fullValidationVersionsKey)
		if stampNodeIsTrue(mappingValue(existingRoot, fullValidationPassedKey)) {
			removeYAMLKey(existingRoot, fullValidationPassedKey)
		}
	}
	return report.summary(), nil
}

func settleStamp(report *manifestMergeReport, earnedBefore, trueAfter, rewired bool) (earned bool) {
	earned = earnedBefore && trueAfter && !rewired
	report.stampRetired = earnedBefore && !earned
	report.stampRefused = trueAfter && !earnedBefore
	return earned
}

var wiringFields = []string{"name", "type", "filename", "origin", "connectors", "depends_on", "skills", "tools", "nodes"}

func componentWiring(components any) string {
	list, ok := components.([]any)
	if !ok {
		return ""
	}
	wiring := make([]map[string]any, 0, len(list))
	for _, item := range list {
		component, isMap := item.(map[string]any)
		picked := map[string]any{}
		for _, field := range wiringFields {
			if value, present := component[field]; isMap && present {
				picked[field] = value
			}
		}
		wiring = append(wiring, picked)
	}
	encoded, err := json.Marshal(wiring)
	if err != nil {
		return fmt.Sprintf("%v", wiring)
	}
	return string(encoded)
}

func nodeComponentWiring(root *yaml.Node) string {
	components := mappingValue(root, manifestComponentsKey)
	if components == nil {
		return ""
	}
	var decoded any
	if err := components.Decode(&decoded); err != nil {
		return ""
	}
	return componentWiring(decoded)
}

func storedValidationVersions(root *yaml.Node) any {
	versions := mappingValue(root, fullValidationVersionsKey)
	if versions == nil {
		return nil
	}
	var decoded any
	if err := versions.Decode(&decoded); err != nil {
		return nil
	}
	return decoded
}

func carryValidationVersionsNode(root *yaml.Node, stored any) {
	removeYAMLKey(root, fullValidationVersionsKey)
	if stored == nil {
		return
	}
	var carriedNode yaml.Node
	if err := carriedNode.Encode(stored); err != nil {
		return
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fullValidationVersionsKey},
		&carriedNode,
	)
}

func carryValidationVersionsMap(manifest map[string]any, stored any) {
	delete(manifest, fullValidationVersionsKey)
	if stored != nil {
		manifest[fullValidationVersionsKey] = stored
	}
}

func removeNullStamp(root *yaml.Node) {
	stamp := mappingValue(root, fullValidationPassedKey)
	if stamp != nil && stamp.Kind == yaml.ScalarNode && stamp.Tag == "!!null" {
		removeYAMLKey(root, fullValidationPassedKey)
	}
}

func stampNodeIsTrue(stamp *yaml.Node) bool {
	return stamp != nil && stamp.Kind == yaml.ScalarNode && stamp.Tag == "!!bool" && stamp.Value == "true"
}

func manifestPathProblem(path string) error {
	if path == mergeKey {
		return errors.New("a path cannot be the YAML merge key <<")
	}
	if strings.Contains(path, ".") {
		return fmt.Errorf("the build manifest takes a merge at the root (components merge by name and type) or at a path without a dot; %q goes deeper", path)
	}
	return nil
}

func withNothingChanged(err error) error {
	return fmt.Errorf("%w; the manifest was not changed", err)
}

func fragmentSyntaxProblem(node *yaml.Node) string {
	if node.Anchor != "" {
		return "anchors and aliases cannot be used in a manifest fragment; write the value out"
	}
	if node.Style&yaml.TaggedStyle != 0 {
		return "explicit YAML tags cannot be used in a manifest fragment; write the value plainly"
	}
	for i, child := range node.Content {
		if problem := fragmentSyntaxProblem(child); problem != "" {
			return problem
		}
		if node.Kind == yaml.MappingNode && i%2 == 0 {
			if child.Value == mergeKey {
				return "a << merge key cannot be used in a manifest fragment; write the fields out"
			}
			if child.Tag != "!!str" {
				return fmt.Sprintf("the key %s in a manifest fragment must be text; quote a key that looks like a number, true, null or a date", strconv.Quote(child.Value))
			}
		}
	}
	return ""
}

func mergeKeyInPatch(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == mergeKey || mergeKeyInPatch(child) {
				return true
			}
		}
	case map[any]any:
		for key, child := range typed {
			if key == mergeKey || mergeKeyInPatch(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if mergeKeyInPatch(child) {
				return true
			}
		}
	}
	return false
}

func mergeManifestRoot(base, overlay *yaml.Node, report *manifestMergeReport) error {
	rest := &yaml.Node{Kind: overlay.Kind, Tag: overlay.Tag}
	var fragment *yaml.Node
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		if overlay.Content[i].Value == manifestComponentsKey {
			fragment = overlay.Content[i+1]
			continue
		}
		rest.Content = append(rest.Content, overlay.Content[i], overlay.Content[i+1])
	}
	if fragment != nil {
		if err := mergeManifestComponentsAt(base, fragment, report); err != nil {
			return err
		}
	}
	nodeMergeMapping(base, rest)
	return nil
}

func mergeManifestComponentsAt(root, fragment *yaml.Node, report *manifestMergeReport) error {
	if fragment.Kind != yaml.SequenceNode {
		return errors.New("components must be a list of component entries")
	}
	for i, entry := range fragment.Content {
		if err := validateComponentEntry(entry); err != nil {
			return fmt.Errorf("components[%d]: %w", i, err)
		}
	}
	existing := mappingValue(root, manifestComponentsKey)
	if existing == nil {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: manifestComponentsKey},
			&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"},
		)
		existing = root.Content[len(root.Content)-1]
	}
	if existing.Kind != yaml.SequenceNode {
		*existing = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	existing.Style &^= yaml.FlowStyle
	for i, entry := range fragment.Content {
		if err := mergeManifestComponent(existing, entry, report); err != nil {
			return fmt.Errorf("components[%d]: %w", i, err)
		}
	}
	return nil
}

func validateComponentEntry(entry *yaml.Node) error {
	if entry.Kind != yaml.MappingNode {
		return errors.New("a component entry must be a mapping")
	}
	name := mappingValue(entry, "name")
	if name == nil || strings.TrimSpace(name.Value) == "" {
		return errors.New("a component entry needs a name")
	}
	for _, key := range []string{"name", "type"} {
		if value := mappingValue(entry, key); value != nil && (value.Kind != yaml.ScalarNode || value.Tag != "!!str") {
			return fmt.Errorf("%s must be a string; quote the value if it looks like a number or a date", key)
		}
	}
	if kind := mappingValue(entry, "type"); kind != nil && strings.TrimSpace(kind.Value) == "" {
		return errors.New("type cannot be blank; leave it out to match by name")
	}
	return nil
}

func dropComments(node *yaml.Node) {
	node.HeadComment, node.LineComment, node.FootComment = "", "", ""
	for _, child := range node.Content {
		dropComments(child)
	}
}

func manifestBytesProblem(stored, merged []byte) error {
	var parsed any
	if err := yaml.Unmarshal(merged, &parsed); err == nil {
		return nil
	}
	if err := yaml.Unmarshal(stored, &parsed); err != nil {
		return withNothingChanged(fmt.Errorf("the stored manifest does not parse as YAML (%w); fix it by editing the artifact text first", err))
	}
	err := yaml.Unmarshal(merged, &parsed)
	return withNothingChanged(fmt.Errorf("the merged manifest would not parse as YAML (%w)", err))
}

func mergeManifestComponent(components, entry *yaml.Node, report *manifestMergeReport) error {
	name := scalarValue(mappingValue(entry, "name"))
	match, err := findManifestComponent(components, entry, name)
	if err != nil {
		return err
	}
	if match == nil {
		if err := checkNewComponent(components, entry, name); err != nil {
			return err
		}
	}
	for _, key := range dropUIOwnedFields(entry) {
		report.ignored = append(report.ignored, key+" on "+strconv.Quote(name))
	}
	if match == nil {
		components.Content = append(components.Content, entry)
		report.added = append(report.added, strconv.Quote(name))
		return nil
	}
	report.updated = append(report.updated, describeUpdate(match, entry))
	nodeMergeMapping(match, entry)
	return nil
}

var notWrittenByAMerge = []string{componentIDKey, componentDeployedVersionKey}

func dropUIOwnedFields(entry *yaml.Node) []string {
	var dropped []string
	for _, key := range notWrittenByAMerge {
		if mappingValue(entry, key) == nil {
			continue
		}
		dropped = append(dropped, key)
		removeYAMLKey(entry, key)
	}
	return dropped
}

func describeUpdate(match, entry *yaml.Node) string {
	name := scalarValue(mappingValue(match, "name"))
	var fields []string
	for i := 0; i+1 < len(entry.Content); i += 2 {
		if key := entry.Content[i].Value; key != "name" && key != "type" {
			fields = append(fields, strconv.Quote(key))
		}
	}
	label := strconv.Quote(name)
	if renamed := scalarValue(mappingValue(entry, "name")); renamed != name {
		label += " renamed to " + strconv.Quote(renamed)
	}
	if len(fields) == 0 {
		return label + " (no fields)"
	}
	return label + " (" + cappedJoin(fields, ", ") + ")"
}

func checkNewComponent(components, entry *yaml.Node, name string) error {
	if mappingValue(entry, "type") == nil {
		if len(componentsNamed(components, name)) > 0 {
			return fmt.Errorf("the id on %q is not that component's id; leave id out of a status update", name)
		}
		return fmt.Errorf("no component named %q exists; a new component entry must give its name, type and filename", name)
	}
	if held := componentsNamed(components, name); len(held) > 0 {
		return fmt.Errorf("component name %q is already held by %s; component names are unique", name, describeComponent(held[0]))
	}
	if mappingValue(entry, "filename") == nil {
		return fmt.Errorf("new component %q needs a filename (\"\" for a component that is already running)", name)
	}
	return nil
}

func describeComponent(component *yaml.Node) string {
	kind := scalarValue(mappingValue(component, "type"))
	if kind == "" {
		return "a component with no type"
	}
	if strings.ContainsRune("aeiou", rune(kind[0])) {
		return "an " + kind + " component"
	}
	return "a " + kind + " component"
}

func manifestComponentID(component *yaml.Node) string {
	id := platformIDNode(mappingValue(component, componentIDKey))
	if id == nil || len(id.Value) != canonicalUUIDLength {
		return ""
	}
	return id.Value
}

func entryMatchesComponent(component, entry *yaml.Node) bool {
	return matchesByIdentity(component, entry, mappingValue(entry, "type") != nil)
}

func matchesByIdentity(a, b *yaml.Node, compareType bool) bool {
	idA, idB := manifestComponentID(a), manifestComponentID(b)
	if idA != "" && idB != "" {
		return idA == idB
	}
	if scalarValue(mappingValue(a, "name")) != scalarValue(mappingValue(b, "name")) {
		return false
	}
	return !compareType || scalarValue(mappingValue(a, "type")) == scalarValue(mappingValue(b, "type"))
}

func findManifestComponent(components, entry *yaml.Node, name string) (*yaml.Node, error) {
	var candidates []*yaml.Node
	for _, comp := range components.Content {
		if entryMatchesComponent(comp, entry) {
			candidates = append(candidates, comp)
		}
	}
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return candidates[0], checkRenameAndRetype(components, candidates[0], entry, name)
	default:
		return nil, fmt.Errorf("%d components match the entry named %q; give it a type, or an id, to say which one", len(candidates), name)
	}
}

func checkRenameAndRetype(components, match, entry *yaml.Node, name string) error {
	renamedOntoAnother := name != scalarValue(mappingValue(match, "name")) && len(componentsNamed(components, name)) > 0
	if sameType(match, entry) && !renamedOntoAnother {
		return nil
	}
	return fmt.Errorf("the id on the entry named %q belongs to %q, which is %s; a merge cannot retype a component or rename it onto another's name, so leave id out unless the entry is that component",
		name, scalarValue(mappingValue(match, "name")), describeComponent(match))
}

func sameType(match, entry *yaml.Node) bool {
	kind := mappingValue(entry, "type")
	return kind == nil || kind.Value == scalarValue(mappingValue(match, "type"))
}

func componentsNamed(components *yaml.Node, name string) []*yaml.Node {
	var named []*yaml.Node
	for _, comp := range components.Content {
		if scalarValue(mappingValue(comp, "name")) == name {
			named = append(named, comp)
		}
	}
	return named
}

func mergeManifestPatch(manifest, patch map[string]any, path string, pinned bool) (string, error) {
	if pinned {
		return "", withNothingChanged(errors.New("the build manifest merges into its latest version; leave the :version suffix off the input filename"))
	}
	if mergeKeyInPatch(patch) {
		return "", withNothingChanged(errors.New("a << merge key cannot be used in a manifest patch; write the fields out"))
	}
	if mergeKeyInPatch(manifest) {
		return "", withNothingChanged(errors.New("the stored manifest holds a << key, which this tool would write back as a merge; remove it by editing the artifact text; yaml_merge_artifact still merges this manifest"))
	}
	if err := manifestPathProblem(path); err != nil {
		return "", withNothingChanged(err)
	}
	report := &manifestMergeReport{}
	stampWasEarned := manifest[fullValidationPassedKey] == true
	storedVersions := cloneValidationVersions(manifest[fullValidationVersionsKey])
	wiringBefore := componentWiring(manifest[manifestComponentsKey])
	if err := applyManifestPatch(manifest, patch, path, report); err != nil {
		return "", withNothingChanged(err)
	}
	if stamp, present := manifest[fullValidationPassedKey]; present && stamp == nil {
		delete(manifest, fullValidationPassedKey)
	}
	rewired := wiringBefore != componentWiring(manifest[manifestComponentsKey])
	if settleStamp(report, stampWasEarned, manifest[fullValidationPassedKey] == true, rewired) {
		carryValidationVersionsMap(manifest, storedVersions)
	} else {
		delete(manifest, fullValidationVersionsKey)
		if manifest[fullValidationPassedKey] == true {
			delete(manifest, fullValidationPassedKey)
		}
	}
	return report.summary(), nil
}

func cloneValidationVersions(stored any) any {
	if versions, ok := stored.(map[string]any); ok {
		return maps.Clone(versions)
	}
	return stored
}

func applyManifestPatch(manifest, patch map[string]any, path string, report *manifestMergeReport) error {
	if path != "" {
		return mergePatchAtPath(manifest, path, patch)
	}
	fragment, hasComponents := patch[manifestComponentsKey]
	if !hasComponents {
		mergePatch(manifest, patch)
		return nil
	}
	if fragment == nil {
		return errors.New("components cannot be deleted from a build manifest; remove a single component by editing the artifact text instead")
	}
	merged, err := mergeComponentsPatch(manifest[manifestComponentsKey], fragment, report)
	if err != nil {
		return err
	}
	mergePatch(manifest, patch)
	manifest[manifestComponentsKey] = merged
	return nil
}

func mergeComponentsPatch(stored, fragment any, report *manifestMergeReport) (any, error) {
	var fragmentNode, currentNode yaml.Node
	if err := fragmentNode.Encode(fragment); err != nil {
		return nil, fmt.Errorf("components: %w", err)
	}
	if err := currentNode.Encode(stored); err != nil {
		return nil, fmt.Errorf("existing components: %w", err)
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: manifestComponentsKey},
		&currentNode,
	}}
	if err := mergeManifestComponentsAt(root, &fragmentNode, report); err != nil {
		return nil, err
	}
	var merged any
	if err := root.Content[1].Decode(&merged); err != nil {
		return nil, fmt.Errorf("merged components: %w", err)
	}
	return merged, nil
}
