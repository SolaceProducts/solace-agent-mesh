// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func workflowToolsetNames(appConfig map[string]any) ([]string, error) {
	var names []string
	seen := map[string]struct{}{}
	for _, m := range toolsetCarriers(appConfig) {
		raw, present := m["toolsets"]
		if !present {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, &ValidationError{Message: fmt.Sprintf("toolsets must be a list of toolset names, got %T", raw)}
		}
		for i, item := range list {
			name, ok := item.(string)
			if !ok || name == "" {
				return nil, &ValidationError{Message: fmt.Sprintf("toolsets[%d] must be a non-empty toolset name", i)}
			}
			if _, dup := seen[name]; dup {
				return nil, &ValidationError{Message: fmt.Sprintf("toolsets lists %q more than once", name)}
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	return schema.CanonicalBuiltinGroups(names), nil
}

func toolsetCarriers(appConfig map[string]any) []map[string]any {
	carriers := []map[string]any{appConfig}
	if body, ok := appConfig["workflow"].(map[string]any); ok {
		carriers = append(carriers, body)
	}
	return carriers
}

func resolveWorkflowToolsets(ctx context.Context, appConfig map[string]any, repo *store.ToolPackageRepo) ([]string, error) {
	names, err := workflowToolsetNames(appConfig)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	if err := validateToolsets(ctx, names, repo); err != nil {
		return nil, err
	}
	return names, nil
}

func hydrateWorkflowToolsets(ctx context.Context, appConfig map[string]any, repo *store.ToolPackageRepo, logger *slog.Logger) error {
	names, err := workflowToolsetNames(appConfig)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		stripToolsetsKey(appConfig)
		return nil
	}
	if repo == nil {
		return fmt.Errorf("toolset references require the platform toolset store, which is not configured")
	}
	if verr := validateToolsets(ctx, names, repo); verr != nil {
		return verr
	}
	if verr := validateToolsetsReady(ctx, names, repo); verr != nil {
		return verr
	}

	packages, err := resolveToolPackageInfos(ctx, "", names, nil, repo, logger)
	if err != nil {
		return fmt.Errorf("resolve tool packages: %w", err)
	}
	for _, name := range names {
		if _, isPackage := packages[name]; !isPackage && !schema.IsValidBuiltinGroup(name) {
			return fmt.Errorf("toolset %q did not resolve to a ready tool package or built-in group", name)
		}
	}

	existing, err := existingToolNames(appConfig)
	if err != nil {
		return err
	}
	var emitted []any
	for _, entry := range buildToolsListWithPackages(names, nil, ToolEmission{Packages: packages}) {
		if toolName, _ := entry["tool_name"].(string); toolName != "" {
			if src, dup := existing[toolName]; dup {
				return fmt.Errorf("toolset tool %q collides with %s", toolName, src)
			}
			existing[toolName] = fmt.Sprintf("the toolset-emitted tool %q", toolName)
		}
		emitted = append(emitted, entry)
	}

	stripToolsetsKey(appConfig)
	if len(emitted) > 0 {
		tools, _ := appConfig["tools"].([]any)
		appConfig["tools"] = append(tools, emitted...)
	}
	return nil
}

func stripToolsetsKey(appConfig map[string]any) {
	for _, m := range toolsetCarriers(appConfig) {
		delete(m, "toolsets")
	}
}
