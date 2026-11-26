// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type SkillConfigInfo struct {
	SkillName     string
	ConfigValues  map[string]map[string]any
	SchemasByTool map[string][]configSchemaField
}

func resolveSkillImpliedToolsets(ctx context.Context, repo *store.SkillRepo, skillIDs []string, logger *slog.Logger) ([]string, error) {
	if repo == nil || len(skillIDs) == 0 {
		return nil, nil
	}
	skills, err := repo.GetByIDs(ctx, skillIDs)
	if err != nil {
		return nil, fmt.Errorf("look up skills by ids: %w", err)
	}
	seen := make(map[string]struct{})
	for _, sk := range skills {
		if sk.DiscoveryStatus != "ready" || sk.Toolsets == nil || *sk.Toolsets == "" {
			continue
		}
		var names []string
		if err := json.Unmarshal([]byte(*sk.Toolsets), &names); err != nil {
			if logger != nil {
				logger.Warn("malformed skills.toolsets JSON — skipping",
					"skillID", sk.ID, "skillName", sk.Name, "error", err)
			}
			continue
		}
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			seen[n] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out, nil
}

func unionToolsets(direct, add []string) []string {
	if len(add) == 0 {
		return schema.CanonicalBuiltinGroups(direct)
	}
	direct, add = schema.CanonicalBuiltinGroups(direct), schema.CanonicalBuiltinGroups(add)
	seen := make(map[string]struct{}, len(direct)+len(add))
	out := make([]string, 0, len(direct)+len(add))
	for _, n := range direct {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	for _, n := range add {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func resolveAttachedSkillConfigs(ctx context.Context, agentID string, skillIDs []string, repo *store.SkillRepo, logger *slog.Logger) (map[string]SkillConfigInfo, error) {
	if repo == nil || len(skillIDs) == 0 {
		return nil, nil
	}

	skills, err := repo.GetByIDs(ctx, skillIDs)
	if err != nil {
		return nil, fmt.Errorf("look up skills by ids: %w", err)
	}

	readySkills := make([]store.Skill, 0, len(skills))
	readyIDs := make([]string, 0, len(skills))
	for _, sk := range skills {
		if sk.DiscoveryStatus != "ready" {
			continue
		}
		readySkills = append(readySkills, sk)
		readyIDs = append(readyIDs, sk.ID)
	}
	if len(readySkills) == 0 {
		return nil, nil
	}

	skillCfgByID, err := repo.GetSkillConfigsForSkills(ctx, readyIDs)
	if err != nil {
		return nil, fmt.Errorf("get skill configs for skills: %w", err)
	}

	agentBindings, err := repo.GetAgentSkillRowsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent skill rows: %w", err)
	}
	agentOverridesBySkillID := map[string]map[string]map[string]any{}
	for _, row := range agentBindings {
		if row.ConfigValues == nil || *row.ConfigValues == "" {
			continue
		}
		var nested map[string]map[string]any
		if err := json.Unmarshal([]byte(*row.ConfigValues), &nested); err != nil {
			if logger != nil {
				logger.Warn("malformed agent_skills.config_values JSON — skipping",
					"agentID", agentID, "skillID", row.SkillID, "error", err)
			}
			continue
		}
		agentOverridesBySkillID[row.SkillID] = nested
	}

	out := make(map[string]SkillConfigInfo, len(readySkills))
	for _, sk := range readySkills {
		schemas := parseSkillBundledToolSchemas(sk.Name, sk.BundledTools)

		skillVals := map[string]map[string]any{}
		if cfg, ok := skillCfgByID[sk.ID]; ok && cfg.ConfigValues != nil && *cfg.ConfigValues != "" {
			if err := json.Unmarshal([]byte(*cfg.ConfigValues), &skillVals); err != nil {
				if logger != nil {
					logger.Warn("malformed skill_configs.config_values JSON — skipping",
						"skillID", sk.ID, "error", err)
				}
				skillVals = map[string]map[string]any{}
			}
		}
		agentVals := agentOverridesBySkillID[sk.ID]

		merged := buildPerSkillToolConfig(schemas, skillVals, agentVals)
		out[sk.Name] = SkillConfigInfo{
			SkillName:     sk.Name,
			ConfigValues:  merged,
			SchemasByTool: schemas,
		}
	}
	return out, nil
}

func buildPerSkillToolConfig(schemas map[string][]configSchemaField, skillVals, agentVals map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(schemas))
	for toolName := range schemas {
		merged := map[string]any{}
		for k, v := range skillVals[toolName] {
			if v != nil {
				merged[k] = v
			}
		}
		if agentTool, ok := agentVals[toolName]; ok {
			for k, v := range agentTool {
				if v == nil {
					delete(merged, k)
				} else {
					merged[k] = v
				}
			}
		}
		if len(merged) > 0 {
			out[toolName] = merged
		}
	}
	return out
}

func buildPerSkillToolConfigWithSchemaDefaults(info SkillConfigInfo) map[string]map[string]any {
	out := make(map[string]map[string]any, len(info.SchemasByTool))
	for toolName, fields := range info.SchemasByTool {
		merged := map[string]any{}
		for k, v := range info.ConfigValues[toolName] {
			if v != nil {
				merged[k] = v
			}
		}
		for _, f := range fields {
			if _, set := merged[f.Key]; set {
				continue
			}
			if f.Default != nil {
				merged[f.Key] = f.Default
			}
		}
		if len(merged) > 0 {
			out[toolName] = merged
		}
	}
	return out
}

func validateSkillRequiredFields(skillConfigs map[string]SkillConfigInfo) error {
	details := map[string][]string{}
	var missing []string
	for skillName, info := range skillConfigs {
		for toolName, fields := range info.SchemasByTool {
			toolVals := info.ConfigValues[toolName]
			for _, field := range fields {
				if !field.isRequired() {
					continue
				}
				if field.Default != nil {
					continue
				}
				if v, ok := toolVals[field.Key]; ok && v != nil && v != "" {
					continue
				}
				key := fmt.Sprintf("%s/%s/%s", skillName, toolName, field.Key)
				details[key] = append(details[key], "missing required value")
				missing = append(missing, key)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &ValidationError{
		Message: "missing required skill config values: " + joinSorted(missing),
		Details: details,
	}
}

func joinSorted(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	cp := make([]string, len(ss))
	copy(cp, ss)
	slices.Sort(cp)
	return strings.Join(cp, ", ")
}
