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
	"maps"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const InvalidAgentSkillConfigMessage = "invalid agent skill configuration values"

func buildAgentSkillRows(ctx context.Context, agentID string, skillIDs []string, bindings []SkillConfigBinding, repo *store.SkillRepo) ([]store.AgentSkillRow, error) {
	existingRows, err := repo.GetAgentSkillRowsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("load existing agent skill rows: %w", err)
	}
	existingByID := make(map[string]*string, len(existingRows))
	for _, r := range existingRows {
		existingByID[r.SkillID] = r.ConfigValues
	}
	return agentSkillRowsOver(ctx, agentID, existingByID, skillIDs, bindings, repo)
}

func buildNewAgentSkillRows(ctx context.Context, skillIDs []string, bindings []SkillConfigBinding, repo *store.SkillRepo) ([]store.AgentSkillRow, error) {
	return agentSkillRowsOver(ctx, "", nil, skillIDs, bindings, repo)
}

func agentSkillRowsOver(ctx context.Context, agentID string, existingByID map[string]*string, skillIDs []string, bindings []SkillConfigBinding, repo *store.SkillRepo) ([]store.AgentSkillRow, error) {
	skillIDSet := make(map[string]struct{}, len(skillIDs))
	for _, id := range skillIDs {
		skillIDSet[id] = struct{}{}
	}
	if bindings == nil {
		return keptAgentSkillRows(agentID, skillIDs, existingByID), nil
	}

	configsBySkillID, details, err := resolveAgentSkillBindings(ctx, bindings, skillIDSet, repo)
	if err != nil {
		return nil, err
	}

	skills, err := repo.GetByIDs(ctx, skillIDs)
	if err != nil {
		return nil, fmt.Errorf("look up skills by ids: %w", err)
	}
	skillByID := make(map[string]store.Skill, len(skills))
	for _, sk := range skills {
		skillByID[sk.ID] = sk
	}

	rows := make([]store.AgentSkillRow, 0, len(skillIDs))
	for _, id := range skillIDs {
		row := store.AgentSkillRow{AgentID: agentID, SkillID: id}
		vals, ok := configsBySkillID[id]
		if !ok || len(vals) == 0 {
			rows = append(rows, row)
			continue
		}

		sk, skOk := skillByID[id]
		if !skOk {
			details[id] = append(details[id], "skill not found")
			continue
		}

		schemasByTool := parseSkillBundledToolSchemas(sk.Name, sk.BundledTools)
		validated, validationDetails := validateAgentSkillBindingValues(sk.Name, vals, schemasByTool)
		for k, v := range validationDetails {
			details[k] = append(details[k], v...)
		}
		if len(details) > 0 {
			continue
		}

		merged, mergeErr := mergeAgentSkillBindingSecrets(validated, existingByID[id], schemasByTool)
		if mergeErr != nil {
			return nil, mergeErr
		}

		if len(merged) == 0 {
			rows = append(rows, row)
			continue
		}
		b, mErr := json.Marshal(merged)
		if mErr != nil {
			return nil, fmt.Errorf("marshal agent skill config for %s: %w", id, mErr)
		}
		s := string(b)
		row.ConfigValues = &s
		rows = append(rows, row)
	}

	if len(details) > 0 {
		return nil, &ValidationError{Message: InvalidAgentSkillConfigMessage, Details: details}
	}
	return rows, nil
}

func keptAgentSkillRows(agentID string, skillIDs []string, existingByID map[string]*string) []store.AgentSkillRow {
	rows := make([]store.AgentSkillRow, len(skillIDs))
	for i, id := range skillIDs {
		rows[i] = store.AgentSkillRow{AgentID: agentID, SkillID: id, ConfigValues: existingByID[id]}
	}
	return rows
}

func resolveAgentSkillBindings(ctx context.Context, bindings []SkillConfigBinding, skillIDSet map[string]struct{}, repo *store.SkillRepo) (map[string]map[string]map[string]any, map[string][]string, error) {
	configsBySkillID := make(map[string]map[string]map[string]any, len(bindings))
	details := map[string][]string{}
	for _, b := range bindings {
		if len(b.ConfigValues) == 0 {
			continue
		}
		id := b.SkillID
		if id == "" && b.SkillName != "" {
			sk, err := repo.GetByName(ctx, b.SkillName)
			if err != nil {
				return nil, nil, fmt.Errorf("look up skill %q: %w", b.SkillName, err)
			}
			if sk == nil {
				details[b.SkillName] = append(details[b.SkillName], "unknown skill")
				continue
			}
			id = sk.ID
		}
		if id == "" {
			continue
		}
		if _, ok := skillIDSet[id]; !ok {
			details[id] = append(details[id], "skill is not bound to this agent; add it to skillIds before setting config")
			continue
		}
		configsBySkillID[id] = b.ConfigValues
	}
	return configsBySkillID, details, nil
}

func validateAgentSkillBindingValues(skillName string, vals map[string]map[string]any, schemasByTool map[string][]configSchemaField) (map[string]map[string]any, map[string][]string) {
	details := map[string][]string{}
	cleaned := map[string]map[string]any{}
	for toolName, toolVals := range vals {
		toolSchema, ok := schemasByTool[toolName]
		if !ok {
			details[skillName+"/"+toolName] = append(details[skillName+"/"+toolName], "unknown bundled tool for this skill")
			continue
		}
		schemaByKey := make(map[string]configSchemaField, len(toolSchema))
		for _, f := range toolSchema {
			schemaByKey[f.Key] = f
		}
		out := make(map[string]any, len(toolVals))
		for key, val := range toolVals {
			entry, ok := schemaByKey[key]
			if !ok {
				details[skillName+"/"+toolName+"."+key] = append(details[skillName+"/"+toolName+"."+key], "unknown configuration field for this bundled tool")
				continue
			}
			if msg := validateConfigValueType(entry, val); msg != "" {
				details[skillName+"/"+toolName+"."+key] = append(details[skillName+"/"+toolName+"."+key], msg)
				continue
			}
			out[key] = val
		}
		if len(out) > 0 {
			cleaned[toolName] = out
		}
	}
	return cleaned, details
}

func mergeAgentSkillBindingSecrets(incoming map[string]map[string]any, prior *string, schemasByTool map[string][]configSchemaField) (map[string]map[string]any, error) {
	priorVals := map[string]map[string]any{}
	var priorErr error
	if prior != nil && *prior != "" {
		if priorErr = json.Unmarshal([]byte(*prior), &priorVals); priorErr != nil {
			priorVals = map[string]map[string]any{}
		}
	}
	out := make(map[string]map[string]any, len(incoming))
	for toolName, toolVals := range incoming {
		working := make(map[string]any, len(toolVals))
		maps.Copy(working, toolVals)
		secretFields := skillSecretFields(schemasByTool[toolName])
		if priorErr != nil && hasRedactedSentinel(working, secretFields) {
			return nil, fmt.Errorf("resolve %s for bundled tool %s: decode stored config_values: %w", redactedSentinel, toolName, priorErr)
		}
		if _, err := mergeSecretFields(working, priorVals[toolName], secretFields); err != nil {
			return nil, err
		}
		dropUnresolvedSentinels(working, secretFields)
		if len(working) > 0 {
			out[toolName] = working
		}
	}
	return out, nil
}

func hasRedactedSentinel(values map[string]any, secretFields map[string]secretFieldInfo) bool {
	for key := range secretFields {
		if values[key] == redactedSentinel {
			return true
		}
	}
	return false
}

func loadAgentSkillConfigBindings(ctx context.Context, agentID string, repo *store.SkillRepo) ([]SkillConfigBinding, error) {
	if repo == nil || agentID == "" {
		return nil, nil
	}
	rows, err := repo.GetAgentSkillRowsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("read agent skill rows: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ConfigValues != nil && *r.ConfigValues != "" {
			ids = append(ids, r.SkillID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	skills, err := repo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read skills for agent bindings: %w", err)
	}
	skillByID := make(map[string]store.Skill, len(skills))
	for _, s := range skills {
		skillByID[s.ID] = s
	}

	out := make([]SkillConfigBinding, 0, len(rows))
	for _, r := range rows {
		if r.ConfigValues == nil || *r.ConfigValues == "" {
			continue
		}
		var vals map[string]map[string]any
		if err := json.Unmarshal([]byte(*r.ConfigValues), &vals); err != nil {
			return nil, fmt.Errorf("decode config_values for skill %s: %w", r.SkillID, err)
		}
		sk, ok := skillByID[r.SkillID]
		if ok {
			schemasByTool := parseSkillBundledToolSchemas(sk.Name, sk.BundledTools)
			for toolName, toolVals := range vals {
				vals[toolName] = redactSecretFields(toolVals, skillSecretFields(schemasByTool[toolName]))
			}
		}
		binding := SkillConfigBinding{
			SkillID:      r.SkillID,
			ConfigValues: vals,
		}
		if ok {
			binding.SkillName = sk.Name
		}
		out = append(out, binding)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func loadSkillConfigFields(ctx context.Context, agentID string, skillRepo *store.SkillRepo, logger *slog.Logger) map[string][]SkillConfigField {
	if skillRepo == nil || agentID == "" {
		return nil
	}
	rows, err := skillRepo.GetAgentSkillRowsForAgent(ctx, agentID)
	if err != nil || len(rows) == 0 {
		return nil
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.SkillID
	}
	skills, err := skillRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil
	}
	skillCfgs, _ := skillRepo.GetSkillConfigsForSkills(ctx, ids)

	out := map[string][]SkillConfigField{}
	for _, sk := range skills {
		schemas := parseSkillBundledToolSchemas(sk.Name, sk.BundledTools)
		if len(schemas) == 0 {
			continue
		}

		skillVals := map[string]map[string]any{}
		if cfg, ok := skillCfgs[sk.ID]; ok && cfg.ConfigValues != nil && *cfg.ConfigValues != "" {
			if err := json.Unmarshal([]byte(*cfg.ConfigValues), &skillVals); err != nil && logger != nil {
				logger.Warn("malformed skill_configs.config_values JSON — treating as empty for field coverage",
					"skillID", sk.ID, "error", err)
			}
		}

		var agentVals map[string]map[string]any
		for _, r := range rows {
			if r.SkillID != sk.ID || r.ConfigValues == nil || *r.ConfigValues == "" {
				continue
			}
			if err := json.Unmarshal([]byte(*r.ConfigValues), &agentVals); err != nil && logger != nil {
				logger.Warn("malformed agent_skills.config_values JSON — treating as empty for field coverage",
					"agentID", agentID, "skillID", sk.ID, "error", err)
			}
		}

		fields := []SkillConfigField{}
		for toolName, schema := range schemas {
			for _, f := range schema {
				skillVal, hasSkill := skillVals[toolName][f.Key]
				agentVal, hasAgent := agentVals[toolName][f.Key]
				skillCovered := hasSkill && skillVal != nil
				agentCovered := hasAgent && agentVal != nil

				fields = append(fields, SkillConfigField{
					ToolName:       toolName,
					Key:            f.Key,
					Type:           f.Type,
					Secret:         f.Secret,
					Required:       f.isRequired(),
					Description:    f.Description,
					Default:        f.Default,
					CoveredBySkill: skillCovered,
					CoveredByAgent: agentCovered,
				})
			}
		}
		if len(fields) > 0 {
			out[sk.ID] = fields
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
