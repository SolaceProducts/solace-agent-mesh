// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"gopkg.in/yaml.v3"
)

type ValidateBuildManifestTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *ValidateBuildManifestTool) Name() string { return "validate_build_manifest" }

func (t *ValidateBuildManifestTool) Description() string {
	return "Validates a SAM build manifest against structural and business rules. " +
		"Checks required fields, valid component types, dependency consistency, " +
		"and entrypoint restrictions. Call this after saving any build manifest. " +
		"Set full_validation=true after ALL configs are generated to also check " +
		"cross-component wiring (e.g. agents referencing connectors, workflow node agent references) " +
		"and workflow schema compatibility (template ref validity, input schema coverage, output schema compatibility). " +
		"A passing full validation records the component versions it checked, so editing a component afterwards makes the result stale until it is run again."
}

func (t *ValidateBuildManifestTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"manifest_yaml": map[string]any{
				"type":        "string",
				"description": "The build manifest YAML string to validate.",
			},
			"full_validation": map[string]any{
				"type":        "boolean",
				"description": "When true, performs cross-component validation: checks agent-connector wiring, workflow node agent references, and workflow schema compatibility (template ref validity against output schemas, input schema required field coverage, cross-node output schema compatibility). Only set this to true after ALL component configs have been generated.",
			},
		},
		Required: []string{"manifest_yaml"},
	}
}

func (t *ValidateBuildManifestTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "valid", typ: "boolean", description: "Whether the manifest produced no validation errors."},
		resultField{name: "errors", typ: "array", description: "Why validation failed, as {path, message} entries. Present only when valid is false.", optional: true},
		resultField{name: "warnings", typ: "array", description: "Non-blocking {path, message} findings — a manifest that will save or run differently than intended. Only produced under full_validation, and only written when there is at least one.", optional: true},
		resultField{name: "full_validation_not_stamped", typ: "string", description: "Why a passing full validation did not stamp the manifest: a component changed while it was being validated. Present only then.", optional: true},
	)
}

func (t *ValidateBuildManifestTool) RawStringArgs() []string {
	return []string{"manifest_yaml"}
}

var (
	validComponentTypes = map[string]bool{
		"agent": true, "workflow": true, "gateway": true, "connector": true, "skill": true, "a2a_agent": true, "toolset": true,
	}
	validComponentTypeList = strings.Join(slices.Sorted(maps.Keys(validComponentTypes)), ", ")
	validStatuses          = map[string]bool{
		"validated": true, "draft": true, "error": true, "deployed": true, componentStatusBlocked: true,
	}
	validOrigins = map[string]bool{
		"session": true, "platform": true,
	}
)

func (t *ValidateBuildManifestTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	manifestStr, _ := args["manifest_yaml"].(string)
	if manifestStr == "" {
		return tool.Error("manifest_yaml is required and must be non-empty"), nil
	}
	fullValidation, _ := args["full_validation"].(bool)

	restorePersistFailed := false
	repairedManifest, restoredEvidence, restoreErr := restoreDeployEvidence(ctx, manifestStr, toolCtx)
	if restoreErr != nil {
		slog.Default().With("logger_name", "validate_build_manifest").Warn(
			"validate_build_manifest: could not restore deploy evidence; validating the manifest as received",
			"filename", manifestFilename, "error", restoreErr)
	} else if len(restoredEvidence) > 0 {
		manifestStr = repairedManifest
		logRestoredEvidence(restoredEvidence)
	}

	errors := validateManifest(manifestStr)
	var warnings []schema.ValidationError

	components := parseManifestComponents(manifestStr)
	var versionsBefore map[string]int
	var versionsBeforeErr error
	if fullValidation && toolCtx != nil {
		versionsBefore, versionsBeforeErr = latestArtifactVersions(ctx, toolCtx, versionedComponentFilenames(components))
	}

	dropped := droppedComponentErrors(ctx, components, toolCtx)
	errors = append(errors, dropped...)
	blocked := blockedComponents(components)

	if fullValidation {
		if components != nil {
			errors = append(errors, crossValidateComponents(components)...)
			warnings = append(warnings, unstatedConnectorWarnings(components)...)
			errors = append(errors, validateSessionArtifactsExist(ctx, components, toolCtx)...)
			errors = append(errors, validateOpenAPIConnectorSpecs(ctx, components, toolCtx)...)
			workflowFindings := validateWorkflowSchemas(ctx, components, toolCtx)
			errors = append(errors, workflowFindings.errors...)
			warnings = append(warnings, workflowFindings.warnings...)
			errors = append(errors, validateWorkflowAPIConnectors(ctx, components, toolCtx)...)
			inputFindings := validateWorkflowConnectorToolInputs(ctx, components, toolCtx)
			errors = append(errors, inputFindings.errors...)
			warnings = append(warnings, inputFindings.warnings...)
		}
	}

	if err := ctx.Err(); err != nil {
		return tool.Error("validate_build_manifest was cancelled before it finished: " + err.Error()), nil
	}

	valid := len(errors) == 0
	data := map[string]any{"valid": valid}
	if !valid {
		data["errors"] = errors
	}
	if valid && len(blocked) > 0 {
		data["blocked"] = blocked
	}
	if len(warnings) > 0 {
		data["warnings"] = warnings
	}

	stampable := fullValidation && valid && len(blocked) == 0 && toolCtx != nil
	var versionsToStamp map[string]int
	var versionsErr error
	if stampable {
		filenames := versionedComponentFilenames(components)
		versionsToStamp, versionsErr = latestArtifactVersions(ctx, toolCtx, filenames)
		if versionsErr == nil && versionsBeforeErr != nil {
			versionsErr = versionsBeforeErr
		}
		if versionsErr == nil && !maps.Equal(versionsBefore, versionsToStamp) {
			data["full_validation_not_stamped"] = componentChangedWhileValidating
			stampable = false
		} else if versionsErr == nil && len(versionsToStamp) != len(filenames) {
			data["full_validation_not_stamped"] = componentVersionUnknown
			stampable = false
		}
	}
	if stampable {
		stampErr := versionsErr
		if stampErr == nil {
			stampErr = stampManifestFullValidation(ctx, manifestStr, versionsToStamp, toolCtx)
		}
		if stampErr != nil {
			slog.Default().With("logger_name", "validate_build_manifest").Error("validate_build_manifest: failed to stamp full_validation_passed; build not deployable",
				"filename", manifestFilename, "error", stampErr)
			data["stamp_failed"] = true
			data["stamp_error"] = stampErr.Error()
			return tool.Error("Build manifest passed validation, but the full_validation stamp could not be saved (transient storage error). The build is NOT yet deployable — re-run validate_build_manifest with full_validation=true to retry.", tool.WithData(data)), nil
		}
	} else if len(restoredEvidence) > 0 && len(dropped) == 0 && toolCtx != nil {
		stripped, hadStamp := stripFullValidationStamp(manifestStr)
		if hadStamp {
			manifestStr = stripped
		}
		if saveErr := saveManifestArtifact(ctx, []byte(manifestStr), toolCtx); saveErr != nil {
			slog.Default().With("logger_name", "validate_build_manifest").Warn(
				"validate_build_manifest: restored deploy evidence could not be persisted; the manifest still reads as never deployed",
				"filename", manifestFilename, "componentCount", len(restoredEvidence), "error", saveErr)
			restorePersistFailed = true
		} else if hadStamp {
			data["retired_full_validation"] = true
		}
	}
	if len(restoredEvidence) > 0 {
		if restorePersistFailed {
			data["restore_persist_failed"] = true
		} else {
			data["restored_deploy_evidence"] = restoredEvidence
		}
	}

	resultJSON, _ := json.MarshalIndent(data, "", "  ")

	if valid {
		msg := "Build manifest is valid"
		if len(warnings) > 0 {
			msg += " with warnings"
		}
		if reason, notStamped := data["full_validation_not_stamped"].(string); notStamped {
			msg += ", but " + reason
		}
		if len(blocked) > 0 {
			msg += ", but " + blockedSummary(blocked)
		}
		return tool.OK(msg, tool.WithData(data)), nil
	}
	return tool.OK(string(resultJSON), tool.WithData(data)), nil
}

const componentChangedWhileValidating = "a component changed while it was being validated; run full_validation again"

const componentVersionUnknown = "a component's config artifact has no stored version to record; save every component's config, then run full_validation again"

const manifestFilename = "build_manifest.yaml"

const stampSaveMaxAttempts = 3

const fullValidationPassedKey = "full_validation_passed"

func stripFullValidationStamp(manifestStr string) (string, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(manifestStr), &doc); err != nil {
		return manifestStr, false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return manifestStr, false
	}
	root := doc.Content[0]
	if mappingValue(root, fullValidationPassedKey) == nil {
		return manifestStr, false
	}
	removeYAMLKey(root, fullValidationPassedKey)
	removeYAMLKey(root, fullValidationVersionsKey)
	stripped, err := yaml.Marshal(&doc)
	if err != nil {
		return manifestStr, false
	}
	return string(stripped), true
}

const fullValidationVersionsKey = "full_validation_versions"

func versionedComponentFilenames(components []map[string]any) []string {
	var filenames []string
	for _, comp := range components {
		if filename, ok := versionedComponentFilename(comp); ok && !slices.Contains(filenames, filename) {
			filenames = append(filenames, filename)
		}
	}
	return filenames
}

func versionedComponentFilename(comp map[string]any) (string, bool) {
	if isPlatformOrigin(comp) || isBlockedComponent(comp) {
		return "", false
	}
	filename, _ := comp["filename"].(string)
	return filename, strings.TrimSpace(filename) != ""
}

var errNoArtifactFacade = errors.New("no artifact facade available")

func latestArtifactVersions(ctx context.Context, toolCtx *tool.Context, filenames []string) (map[string]int, error) {
	facade := toolCtx.Facade()
	if facade == nil {
		return nil, errNoArtifactFacade
	}
	latest := make(map[string]int, len(filenames))
	for _, filename := range filenames {
		versions, err := facade.ListArtifactVersions(ctx, filename)
		if err != nil {
			return nil, fmt.Errorf("list versions of %s: %w", filename, err)
		}
		if len(versions) > 0 {
			latest[filename] = versions[len(versions)-1]
		}
	}
	return latest, nil
}

func stampManifestFullValidation(ctx context.Context, manifestStr string, versions map[string]int, toolCtx *tool.Context) error {
	facade := toolCtx.Facade()
	if facade == nil {
		return errNoArtifactFacade
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(manifestStr), &doc); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("unexpected manifest structure")
	}
	root := doc.Content[0]

	removeYAMLKey(root, fullValidationPassedKey)
	removeYAMLKey(root, fullValidationVersionsKey)
	var versionsNode yaml.Node
	if err := versionsNode.Encode(versions); err != nil {
		return fmt.Errorf("encode component versions: %w", err)
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fullValidationPassedKey},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fullValidationVersionsKey},
		&versionsNode,
	)

	stamped, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshal stamped manifest: %w", err)
	}

	return saveManifestArtifact(ctx, stamped, toolCtx)
}

func saveManifestArtifact(ctx context.Context, content []byte, toolCtx *tool.Context) error {
	facade := toolCtx.Facade()
	if facade == nil {
		return errNoArtifactFacade
	}
	defer lockBuildManifest(toolCtx, manifestFilename).Unlock()

	existing, _, loadErr := facade.LoadArtifact(ctx, manifestFilename, nil)
	if loadErr != nil {
		slog.Default().With("logger_name", "validate_build_manifest").Warn(
			"validate_build_manifest: could not read the stored manifest for write dedup; writing unconditionally",
			"filename", manifestFilename, "error", loadErr)
	} else if bytes.Equal(existing, content) {
		return nil
	}

	for attempt := 1; ; attempt++ {
		_, err := facade.SaveArtifactWithMetadata(ctx, manifestFilename, content, SAMBuildManifestMIMEType, artifact.MetadataOpts{
			Tags: []string{"__working"},
		})
		if err == nil {
			return nil
		}
		if attempt >= stampSaveMaxAttempts || ctx.Err() != nil {
			return fmt.Errorf("manifest save failed after %d attempts: %w", attempt, err)
		}
		slog.Default().With("logger_name", "validate_build_manifest").Warn("validate_build_manifest: manifest save attempt failed, retrying",
			"attempt", attempt, "filename", manifestFilename, "error", err)
	}
}

func removeYAMLKey(mapping *yaml.Node, key string) {
	for i := len(mapping.Content) - 2; i >= 0; i -= 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
		}
	}
}

func validateManifest(manifestStr string) []schema.ValidationError {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(manifestStr), &raw); err != nil {
		return []schema.ValidationError{{Path: "", Message: "invalid YAML: " + err.Error()}}
	}
	if raw == nil {
		return []schema.ValidationError{{Path: "", Message: "manifest is empty"}}
	}

	var errs []schema.ValidationError

	errs = append(errs, stringListFieldErrors(raw, "", rootStringListFields, "plain text", "a plain sentence")...)
	errs = append(errs, envVarShapeErrors(raw)...)

	name, _ := raw["name"].(string)
	if strings.TrimSpace(name) == "" {
		errs = append(errs, schema.ValidationError{Path: "name", Message: "required field 'name' is missing or empty"})
	}

	componentsRaw, hasComponents := raw["components"]
	if !hasComponents {
		errs = append(errs, schema.ValidationError{Path: "components", Message: "required field 'components' is missing"})
		return errs
	}
	componentsList, ok := componentsRaw.([]any)
	if !ok {
		errs = append(errs, schema.ValidationError{Path: "components", Message: "'components' must be an array"})
		return errs
	}
	if len(componentsList) == 0 {
		errs = append(errs, schema.ValidationError{Path: "components", Message: "'components' must not be empty"})
		return errs
	}

	nameSet := make(map[string]int)
	for i, item := range componentsList {
		comp, ok := item.(map[string]any)
		if !ok {
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("components[%d]", i),
				Message: "component must be an object",
			})
			continue
		}
		errs = append(errs, validateComponent(comp, i, nameSet)...)
	}

	for i, item := range componentsList {
		comp, ok := item.(map[string]any)
		if !ok {
			continue
		}
		depsRaw, hasDeps := comp["depends_on"]
		if !hasDeps {
			continue
		}
		deps, ok := depsRaw.([]any)
		if !ok {
			continue
		}
		for j, dep := range deps {
			depName, _ := dep.(string)
			if depName == "" {
				continue
			}
			if _, exists := nameSet[depName]; !exists {
				errs = append(errs, schema.ValidationError{
					Path:    fmt.Sprintf("components[%d].depends_on[%d]", i, j),
					Message: fmt.Sprintf("dependency '%s' does not match any component name", depName),
				})
			}
		}
	}

	return errs
}

func validateComponent(comp map[string]any, idx int, nameSet map[string]int) []schema.ValidationError {
	prefix := fmt.Sprintf("components[%d]", idx)
	var errs []schema.ValidationError

	errs = append(errs, stringListFieldErrors(comp, prefix, componentStringListFields, "plain names", "my_component")...)
	errs = append(errs, workflowNodeShapeErrors(comp, prefix)...)

	compName, _ := comp["name"].(string)
	if strings.TrimSpace(compName) == "" {
		errs = append(errs, schema.ValidationError{
			Path:    prefix + ".name",
			Message: "required field 'name' is missing or empty",
		})
	} else {
		if firstIdx, exists := nameSet[compName]; exists {
			errs = append(errs, schema.ValidationError{
				Path:    prefix + ".name",
				Message: fmt.Sprintf("duplicate component name '%s' (first defined at components[%d])", compName, firstIdx),
			})
		} else {
			nameSet[compName] = idx
		}
	}

	compType, _ := comp["type"].(string)
	if compType == "" {
		errs = append(errs, schema.ValidationError{
			Path:    prefix + ".type",
			Message: "required field 'type' is missing or empty",
		})
	} else if !validComponentTypes[compType] {
		errs = append(errs, schema.ValidationError{
			Path:    prefix + ".type",
			Message: fmt.Sprintf("invalid component type '%s'; must be one of: %s", compType, validComponentTypeList),
		})
	}

	if compType == "gateway" {
		gwType, _ := comp["gateway_type"].(string)
		if gwType == "" {
			errs = append(errs, schema.ValidationError{
				Path:    prefix + ".gateway_type",
				Message: "entrypoint components must specify 'gateway_type: event_mesh' — HTTP SSE entrypoints are platform-managed and cannot be built",
			})
		} else if gwType != "event_mesh" {
			errs = append(errs, schema.ValidationError{
				Path:    prefix + ".gateway_type",
				Message: fmt.Sprintf("invalid gateway_type '%s'; only 'event_mesh' is allowed — HTTP SSE entrypoints are platform-managed", gwType),
			})
		}
	}

	status, _ := comp["status"].(string)
	if status != "" && !validStatuses[status] {
		errs = append(errs, schema.ValidationError{
			Path:    prefix + ".status",
			Message: fmt.Sprintf("invalid status '%s'; must be one of: validated, draft, error, deployed, blocked", status),
		})
	}

	errs = append(errs, blockedComponentErrors(comp, prefix)...)

	origin, _ := comp["origin"].(string)
	if origin != "" && !validOrigins[origin] {
		errs = append(errs, schema.ValidationError{
			Path:    prefix + ".origin",
			Message: fmt.Sprintf("invalid origin '%s'; must be one of: session, platform", origin),
		})
	}

	if raw, stated := comp["filename"]; !stated {
		errs = append(errs, schema.ValidationError{
			Path:    filenamePath(prefix),
			Message: "'filename' is required on every component — write `filename: \"\"` for one that is already deployed. Omitting it " + canvasUnreadable,
		})
	} else if _, isText := raw.(string); !isText {
		errs = append(errs, schema.ValidationError{
			Path:    filenamePath(prefix),
			Message: fmt.Sprintf("'filename' is %T, not text — any other shape %s", raw, canvasUnreadable),
		})
	}

	filename, _ := comp["filename"].(string)
	if origin == "session" && strings.TrimSpace(filename) == "" {
		errs = append(errs, schema.ValidationError{
			Path:    filenamePath(prefix),
			Message: "components with origin 'session' must have a non-empty filename",
		})
	}
	if origin == "platform" && strings.TrimSpace(filename) != "" {
		errs = append(errs, schema.ValidationError{
			Path:    filenamePath(prefix),
			Message: "components with origin 'platform' should have an empty filename (they are already deployed)",
		})
	}

	errs = append(errs, deployEvidenceShapeErrors(comp, prefix)...)

	return errs
}

func deployEvidenceShapeErrors(comp map[string]any, prefix string) []schema.ValidationError {
	var errs []schema.ValidationError
	if raw, stated := comp[componentDeployedVersionKey]; stated && raw != nil {
		if _, isInt := raw.(int); !isInt {
			errs = append(errs, schema.ValidationError{
				Path: prefix + "." + componentDeployedVersionKey,
				Message: fmt.Sprintf("'%s' is %T, not a plain integer — quoting it or spelling it any other way %s",
					componentDeployedVersionKey, raw, canvasUnreadable),
			})
		}
	}
	if raw, stated := comp[componentIDKey]; stated && raw != nil {
		if _, isText := raw.(string); !isText {
			errs = append(errs, schema.ValidationError{
				Path: prefix + "." + componentIDKey,
				Message: fmt.Sprintf("'%s' is %T, not text — any other shape %s",
					componentIDKey, raw, canvasUnreadable),
			})
		}
	}
	return errs
}

func parseManifestComponents(manifestStr string) []map[string]any {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(manifestStr), &raw); err != nil {
		return nil
	}
	componentsList, ok := raw["components"].([]any)
	if !ok {
		return nil
	}
	var components []map[string]any
	for _, item := range componentsList {
		comp, ok := item.(map[string]any)
		if !ok {
			continue
		}
		components = append(components, comp)
	}
	return components
}

func crossValidateComponents(components []map[string]any) []schema.ValidationError {
	nameToType := make(map[string]string)
	for _, comp := range components {
		name, _ := comp["name"].(string)
		compType, _ := comp["type"].(string)
		if name != "" && compType != "" {
			nameToType[name] = compType
		}
	}

	var errs []schema.ValidationError

	for i, comp := range components {
		compType, _ := comp["type"].(string)
		compName, _ := comp["name"].(string)
		prefix := fmt.Sprintf("components[%d]", i)

		switch compType {
		case "agent":
			errs = append(errs, validateAgentConnectorWiring(comp, compName, prefix, nameToType)...)
			errs = append(errs, validateAgentSkillRefs(comp, compName, prefix, nameToType)...)
		case "workflow":
			errs = append(errs, validateWorkflowNodeRefs(comp, compName, prefix, nameToType)...)
		}
	}

	return errs
}

func validateAgentConnectorWiring(comp map[string]any, compName, prefix string, nameToType map[string]string) []schema.ValidationError {
	var errs []schema.ValidationError

	connectors := toStringSlice(comp["connectors"])
	connectorSet := make(map[string]bool, len(connectors))
	for _, c := range connectors {
		connectorSet[c] = true
	}

	for j, c := range connectors {
		typ, exists := nameToType[c]
		if !exists || typ != "connector" {
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("%s.connectors[%d]", prefix, j),
				Message: fmt.Sprintf("agent '%s' lists '%s' in connectors but '%s' is not a connector component in the manifest", compName, c, c),
			})
		}
	}

	deps := toStringSlice(comp["depends_on"])
	for _, dep := range deps {
		if nameToType[dep] == "connector" && !connectorSet[dep] {
			errs = append(errs, schema.ValidationError{
				Path:    prefix + ".connectors",
				Message: fmt.Sprintf("agent '%s' depends on connector '%s' but does not list it in its 'connectors' field — add it so the platform can wire the connector's tools to this agent", compName, dep),
			})
		}
	}

	return errs
}

func unstatedConnectorWarnings(components []map[string]any) []schema.ValidationError {
	nameToType := make(map[string]string, len(components))
	for _, comp := range components {
		name, _ := comp["name"].(string)
		compType, _ := comp["type"].(string)
		nameToType[name] = compType
	}

	var out []schema.ValidationError
	for i, comp := range components {
		if compType, _ := comp["type"].(string); compType != "agent" {
			continue
		}
		if origin, _ := comp["origin"].(string); origin == "platform" {
			continue
		}
		if comp["connectors"] != nil {
			continue
		}
		statedViaDependsOn := false
		for _, dep := range toStringSlice(comp["depends_on"]) {
			if nameToType[dep] == "connector" {
				statedViaDependsOn = true
			}
		}
		if statedViaDependsOn {
			continue
		}
		name, _ := comp["name"].(string)
		out = append(out, schema.ValidationError{
			Path: fmt.Sprintf("components[%d].connectors", i),
			Message: fmt.Sprintf("agent '%s' does not state a 'connectors' field, so the save will keep whatever connectors are already wired to it. "+
				"List the connectors it uses. Write an empty list only if you have READ the agent's current configuration and it uses none — "+
				"an empty list detaches every connector currently wired, and on an agent you have not read that is a silent removal", name),
		})
	}
	return out
}

func filenamePath(prefix string) string { return prefix + ".filename" }

const canvasUnreadable = "makes the whole manifest unreadable to the Builder canvas, which then keeps showing the previous build"

var componentStringListFields = []string{"connectors", "depends_on", "tools"}

var rootStringListFields = []string{"risks"}

func stringListFieldErrors(comp map[string]any, prefix string, fields []string, entryNoun, entryExample string) []schema.ValidationError {
	var errs []schema.ValidationError
	for _, field := range fields {
		raw, stated := comp[field]
		if !stated || raw == nil {
			continue
		}
		fieldPath := field
		if prefix != "" {
			fieldPath = prefix + "." + field
		}
		list, isList := raw.([]any)
		if !isList {
			errs = append(errs, schema.ValidationError{
				Path:    fieldPath,
				Message: fmt.Sprintf("'%s' is %T, not a list — write `%s: []` for none. Any other spelling %s", field, raw, field, canvasUnreadable),
			})
			continue
		}
		for j, item := range list {
			if _, isString := item.(string); !isString {
				errs = append(errs, schema.ValidationError{
					Path: fmt.Sprintf("%s[%d]", fieldPath, j),
					Message: fmt.Sprintf("'%s' entries must be %s, but entry %d is %T — write `- %s`, not a mapping. Any other shape %s",
						field, entryNoun, j, item, entryExample, canvasUnreadable),
				})
			}
		}
	}
	return errs
}

func workflowNodeShapeErrors(comp map[string]any, prefix string) []schema.ValidationError {
	raw, stated := comp["nodes"]
	if !stated || raw == nil {
		return nil
	}
	list, isList := raw.([]any)
	if !isList {
		return []schema.ValidationError{{
			Path:    prefix + ".nodes",
			Message: fmt.Sprintf("'nodes' is %T, not a list — write `nodes: []` for none. Any other spelling %s", raw, canvasUnreadable),
		}}
	}
	var errs []schema.ValidationError
	for i, item := range list {
		path := fmt.Sprintf("%s.nodes[%d]", prefix, i)
		node, isMap := item.(map[string]any)
		if !isMap {
			errs = append(errs, schema.ValidationError{
				Path:    path,
				Message: fmt.Sprintf("node %d is %T, not a mapping — every node needs at least an `id` and a `type`", i, item),
			})
			continue
		}
		for _, field := range []string{"id", "type"} {
			value, present := node[field]
			if _, isText := value.(string); present && isText {
				continue
			}
			errs = append(errs, schema.ValidationError{
				Path: path + "." + field,
				Message: fmt.Sprintf("node %d has no usable '%s' (%v) — the canvas requires it as a string, and without it "+
					"the whole manifest is unreadable to it, which makes it keep showing the previous build", i, field, value),
			})
		}
		errs = append(errs, stringListFieldErrors(node, path, []string{"depends_on"}, "plain names", "other_node")...)
	}
	return errs
}

func envVarShapeErrors(raw map[string]any) []schema.ValidationError {
	stated, present := raw["env_vars"]
	if !present || stated == nil {
		return nil
	}
	list, isList := stated.([]any)
	if !isList {
		return []schema.ValidationError{{
			Path:    "env_vars",
			Message: fmt.Sprintf("'env_vars' is %T, not a list — write `env_vars: []` for none. Any other spelling %s", stated, canvasUnreadable),
		}}
	}
	var errs []schema.ValidationError
	for i, item := range list {
		entry, isMap := item.(map[string]any)
		if !isMap {
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("env_vars[%d]", i),
				Message: fmt.Sprintf("env var %d is %T, not a mapping — write `- name: MY_VAR`", i, item),
			})
			continue
		}
		if _, isText := entry["name"].(string); !isText {
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("env_vars[%d].name", i),
				Message: fmt.Sprintf("env var %d has no usable 'name' (%v) — the canvas requires it as a string", i, entry["name"]),
			})
		}
	}
	return errs
}

func validateAgentSkillRefs(comp map[string]any, compName, prefix string, nameToType map[string]string) []schema.ValidationError {
	skills := toStringSlice(comp["skills"])
	if len(skills) == 0 {
		return nil
	}

	var errs []schema.ValidationError
	for j, s := range skills {
		typ, exists := nameToType[s]
		if !exists || typ != "skill" {
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("%s.skills[%d]", prefix, j),
				Message: fmt.Sprintf("agent '%s' references skill '%s' which is not a skill component in the manifest", compName, s),
			})
		}
	}
	return errs
}

func validateSessionArtifactsExist(ctx context.Context, components []map[string]any, toolCtx *tool.Context) []schema.ValidationError {
	if toolCtx == nil || toolCtx.ArtifactService == nil {
		return nil
	}
	facade := toolCtx.Facade()
	if facade == nil {
		return nil
	}

	var errs []schema.ValidationError
	for i, comp := range components {
		filename, ok := versionedComponentFilename(comp)
		if !ok {
			continue
		}

		data, _, err := facade.LoadArtifact(ctx, filename, nil)
		if ctx.Err() != nil {
			break
		}
		if err != nil || len(data) == 0 {
			compName, _ := comp["name"].(string)
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("components[%d].filename", i),
				Message: fmt.Sprintf("component '%s' declares config artifact '%s' but it was not found — generate and save the component's config before requesting full validation (otherwise Build & Activate fails with 'Could not resolve config')", compName, filename),
			})
		}
	}
	return errs
}

func validateWorkflowNodeRefs(comp map[string]any, compName, prefix string, nameToType map[string]string) []schema.ValidationError {
	var errs []schema.ValidationError

	nodesRaw, ok := comp["nodes"].([]any)
	if !ok {
		return nil
	}

	for j, nodeRaw := range nodesRaw {
		node, ok := nodeRaw.(map[string]any)
		if !ok {
			continue
		}

		agentName, _ := node["agent_name"].(string)
		if agentName == "" {
			continue
		}

		nodeID, _ := node["id"].(string)
		if nodeID == "" {
			nodeID = fmt.Sprintf("index %d", j)
		}

		typ, exists := nameToType[agentName]
		if !exists || typ != "agent" {
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("%s.nodes[%d].agent_name", prefix, j),
				Message: fmt.Sprintf("workflow '%s' node '%s' references agent '%s' which is not an agent component in the manifest", compName, nodeID, agentName),
			})
		}
	}

	return errs
}

func toStringSlice(raw any) []string {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	var result []string
	for _, item := range list {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
