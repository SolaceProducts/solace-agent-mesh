// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func CanonicalizeToolsetRefs(toolsets []string, configs []ToolsetConfigBinding, excluded map[string][]string) ([]string, []ToolsetConfigBinding, map[string][]string, error) {
	canonicalToolsets := schema.CanonicalBuiltinGroups(toolsets)

	canonicalConfigs, err := canonicalizeToolsetConfigNames(configs)
	if err != nil {
		return nil, nil, nil, err
	}

	return canonicalToolsets, canonicalConfigs, canonicalizeExclusionKeys(excluded), nil
}

func (s *AgentService) canonicalizeAndValidateToolsetRefs(ctx context.Context, toolsets *[]string, configs *[]ToolsetConfigBinding, excluded *map[string][]string, additionalConfig json.RawMessage) error {
	canonicalToolsets, canonicalConfigs, canonicalExcluded, err := CanonicalizeToolsetRefs(*toolsets, *configs, *excluded)
	if err != nil {
		return err
	}
	*toolsets, *configs, *excluded = canonicalToolsets, canonicalConfigs, canonicalExcluded

	if s.toolPackageRepo == nil {
		return nil
	}
	return validateToolsetConfigBindings(ctx, *configs, *toolsets, additionalConfig, s.toolPackageRepo)
}

func canonicalizeToolsetConfigNames(configs []ToolsetConfigBinding) ([]ToolsetConfigBinding, error) {
	if configs == nil {
		return nil, nil
	}
	out := make([]ToolsetConfigBinding, 0, len(configs))
	seenBy := make(map[string]string, len(configs))
	for _, b := range configs {
		id := schema.CanonicalBuiltinGroup(b.ToolsetName)
		if first, dup := seenBy[id]; dup {
			return nil, &ValidationError{Message: fmt.Sprintf(
				"toolsetConfigs names the same toolset twice: %q and %q both refer to %q; keep one entry",
				first, b.ToolsetName, id)}
		}
		seenBy[id] = b.ToolsetName
		b.ToolsetName = id
		out = append(out, b)
	}
	return out, nil
}

func validateToolsetConfigBindings(ctx context.Context, bindings []ToolsetConfigBinding, boundToolsets []string, additionalConfig json.RawMessage, repo *store.ToolPackageRepo) error {
	if len(bindings) == 0 {
		return nil
	}

	boundSet := make(map[string]bool, len(boundToolsets))
	for _, ts := range boundToolsets {
		boundSet[ts] = true
	}

	slotNames := volumeSlotNamesFromConfig(additionalConfig)
	policyNames := networkPolicyNamesFromConfig(additionalConfig)

	for _, b := range bindings {
		if !boundSet[b.ToolsetName] {
			return &ValidationError{Message: fmt.Sprintf("toolset %q is not bound to this agent; add it to toolsets before setting config", b.ToolsetName)}
		}

		if err := validateToolsetSecretFieldTypes(ctx, b, repo); err != nil {
			return err
		}

		if _, isBuiltin := builtinToolsetTools[b.ToolsetName]; isBuiltin {
			if err := validateBuiltinToolsetConfigKeys(b.ToolsetName, b.ConfigValues); err != nil {
				return err
			}
			if err := validateBuiltinToolsetVolumeBindings(b.ToolsetName, b.VolumeBindings, slotNames); err != nil {
				return err
			}
			if err := validateBuiltinToolsetNetworkBindings(b.ToolsetName, b.NetworkBindings, policyNames); err != nil {
				return err
			}
			continue
		}

		if len(b.VolumeBindings) > 0 {
			return &ValidationError{Message: fmt.Sprintf("toolset %q does not support volume bindings", b.ToolsetName)}
		}
		if len(b.NetworkBindings) > 0 {
			return &ValidationError{Message: fmt.Sprintf("toolset %q does not support network bindings", b.ToolsetName)}
		}

		pkg, err := repo.GetByName(ctx, b.ToolsetName)
		if err != nil {
			return fmt.Errorf("resolve toolset binding %q: %w", b.ToolsetName, err)
		}
		if pkg == nil {
			return &ValidationError{Message: fmt.Sprintf("toolset %q is not a custom tool package or builtin toolset; only those accept config values", b.ToolsetName)}
		}
		if err := validateToolsetBindingValues(ctx, b.ToolsetName, pkg.ID, b.ConfigValues, repo); err != nil {
			return err
		}
	}
	return nil
}

func validateToolsetSecretFieldTypes(ctx context.Context, b ToolsetConfigBinding, repo *store.ToolPackageRepo) error {
	if len(b.ConfigValues) == 0 {
		return nil
	}
	if _, isBuiltin := builtinToolsetTools[b.ToolsetName]; isBuiltin {
		return builtinToolsetSecretFieldTypes(b)
	}
	pkg, err := repo.GetByName(ctx, b.ToolsetName)
	if err != nil || pkg == nil {
		return nil
	}
	files, err := repo.GetFilesForPackage(ctx, pkg.ID)
	if err != nil {
		return fmt.Errorf("load tool files for %q: %w", b.ToolsetName, err)
	}
	schemaByTool := loadConfigSchemaByTool(files)
	return secretFieldTypeErrors(b, func(toolName string) map[string]secretFieldInfo {
		return toolsetSecretFields(schemaByTool[toolName])
	})
}

func builtinToolsetSecretFieldTypes(b ToolsetConfigBinding) error {
	return secretFieldTypeErrors(b, builtinToolSecretFields)
}

func secretFieldTypeErrors(b ToolsetConfigBinding, secretFieldsOf func(toolName string) map[string]secretFieldInfo) error {
	var messages []string
	details := map[string][]string{}
	for _, toolName := range slices.Sorted(maps.Keys(b.ConfigValues)) {
		toolVals, ok := b.ConfigValues[toolName].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range slices.Sorted(maps.Keys(secretFieldsOf(toolName))) {
			val, present := toolVals[key]
			if !present || val == nil {
				continue
			}
			if _, isString := val.(string); !isString {
				messages = append(messages, fmt.Sprintf(
					"toolset %q: secret field %q must be a string, got %T", b.ToolsetName, key, val))
				path := b.ToolsetName + "." + toolName + "." + key
				details[path] = append(details[path], fmt.Sprintf("must be a string, got %T", val))
			}
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return &ValidationError{Message: strings.Join(messages, "; "), Details: details}
}

func canonicalizeExclusionKeys(excluded map[string][]string) map[string][]string {
	if excluded == nil {
		return nil
	}
	out := make(map[string][]string, len(excluded))
	for ts, tools := range excluded {
		id := schema.CanonicalBuiltinGroup(ts)
		merged := slices.Concat(out[id], tools)
		slices.Sort(merged)
		out[id] = slices.Compact(merged)
	}
	return out
}
