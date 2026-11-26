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

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const skillBundledToolSep = "__"

const resourceSkill = "skill"

type bundledToolSchemaEntry struct {
	ToolName     string              `json:"tool_name"`
	ConfigSchema []configSchemaField `json:"config_schema,omitempty"`
}

func parseSkillBundledToolSchemas(skillName string, bundledTools *string) map[string][]configSchemaField {
	out := map[string][]configSchemaField{}
	if bundledTools == nil || *bundledTools == "" {
		return out
	}
	var entries []bundledToolSchemaEntry
	if err := json.Unmarshal([]byte(*bundledTools), &entries); err != nil {
		return out
	}
	prefix := skillName + skillBundledToolSep
	for _, e := range entries {
		if e.ToolName == "" || len(e.ConfigSchema) == 0 {
			continue
		}
		key := strings.TrimPrefix(e.ToolName, prefix)
		out[key] = e.ConfigSchema
	}
	return out
}

func skillSecretFields(fields []configSchemaField) map[string]secretFieldInfo {
	out := map[string]secretFieldInfo{}
	for _, f := range fields {
		if f.Secret {
			out[f.Key] = secretFieldInfo{Required: f.isRequired()}
		}
	}
	return out
}

func (s *SkillService) requireReadySkill(ctx context.Context, skillID string) (*store.Skill, error) {
	sk, err := s.skillRepo.GetByID(ctx, skillID)
	if err != nil {
		return nil, fmt.Errorf(getSkillErrFmt, err)
	}
	if sk == nil {
		return nil, &NotFoundError{Resource: resourceSkill, ID: skillID}
	}
	if sk.DiscoveryStatus != "ready" {
		return nil, errNotReady(fmt.Sprintf("skill is not ready for configuration (status=%s)", sk.DiscoveryStatus))
	}
	return sk, nil
}

func (s *SkillService) GetConfig(ctx context.Context, skillID string) (*SkillConfigResponse, error) {
	sk, err := s.requireReadySkill(ctx, skillID)
	if err != nil {
		return nil, err
	}

	schemasByTool := parseSkillBundledToolSchemas(sk.Name, sk.BundledTools)

	cfg, err := s.skillRepo.GetSkillConfig(ctx, skillID)
	if err != nil {
		return nil, fmt.Errorf("get skill config: %w", err)
	}
	resp := &SkillConfigResponse{Values: map[string]map[string]any{}}
	if cfg == nil {
		return resp, nil
	}
	resp.UpdatedBy = cfg.UpdatedBy
	resp.UpdatedAt = Timestamp(cfg.UpdatedTime)
	if cfg.ConfigValues == nil || *cfg.ConfigValues == "" {
		return resp, nil
	}
	var stored map[string]map[string]any
	if err := json.Unmarshal([]byte(*cfg.ConfigValues), &stored); err != nil {
		s.logger.Warn("skill config: stored values not valid JSON", "skillID", skillID, "error", err)
		return resp, nil
	}
	for toolName, vals := range stored {
		resp.Values[toolName] = redactSecretFields(vals, skillSecretFields(schemasByTool[toolName]))
	}
	return resp, nil
}

func (s *SkillService) GetSecretsDiff(ctx context.Context, skillID string, candidate map[string]any) (map[string]SecretFieldDiffResult, error) {
	sk, err := s.requireReadySkill(ctx, skillID)
	if err != nil {
		return nil, err
	}

	schemasByTool := parseSkillBundledToolSchemas(sk.Name, sk.BundledTools)
	stored, err := s.storedSkillConfigCleartext(ctx, skillID)
	if err != nil {
		return nil, err
	}

	out := map[string]SecretFieldDiffResult{}
	for toolName, schema := range schemasByTool {
		secrets := skillSecretFields(schema)
		if len(secrets) == 0 {
			continue
		}
		toolCand, _ := candidate[toolName].(map[string]any)
		for name, res := range compareSecrets(registryToSecretFields(secrets), toolCand, stored[toolName]) {
			out[toolName+"."+name] = res
		}
	}
	return out, nil
}

func (s *SkillService) storedSkillConfigCleartext(ctx context.Context, skillID string) (map[string]map[string]any, error) {
	cfg, err := s.skillRepo.GetSkillConfig(ctx, skillID)
	if err != nil {
		return nil, fmt.Errorf("get skill config: %w", err)
	}
	if cfg == nil || cfg.ConfigValues == nil || *cfg.ConfigValues == "" {
		return map[string]map[string]any{}, nil
	}
	var stored map[string]map[string]any
	if err := json.Unmarshal([]byte(*cfg.ConfigValues), &stored); err != nil {
		s.logger.Warn("skill config: stored values not valid JSON", "skillID", skillID, "error", err)
		return map[string]map[string]any{}, nil
	}
	return stored, nil
}

func (s *SkillService) PatchConfig(ctx context.Context, skillID string, req SkillConfigPatchRequest, userID string) (*SkillConfigResponse, error) {
	sk, err := s.requireReadySkill(ctx, skillID)
	if err != nil {
		return nil, err
	}

	schemasByTool := parseSkillBundledToolSchemas(sk.Name, sk.BundledTools)

	if details := validateSkillPatchValues(req.Values, schemasByTool); len(details) > 0 {
		return nil, &ValidationError{Message: "invalid skill configuration values", Details: details}
	}

	existing, err := s.loadExistingSkillConfigValues(ctx, skillID)
	if err != nil {
		return nil, err
	}

	merged := map[string]map[string]any{}
	maps.Copy(merged, existing)

	for toolName, vals := range req.Values {
		toolMerged, mergeErr := s.mergeSkillConfigTool(toolName, vals, existing[toolName], schemasByTool[toolName], skillID)
		if mergeErr != nil {
			return nil, mergeErr
		}
		if len(toolMerged) == 0 {
			delete(merged, toolName)
		} else {
			merged[toolName] = toolMerged
		}
	}

	for toolName := range merged {
		if _, ok := schemasByTool[toolName]; !ok {
			s.logger.Debug("skill config: dropping orphan tool entry", "skillID", skillID, "tool", toolName)
			delete(merged, toolName)
		}
	}

	var serialized *string
	if len(merged) > 0 {
		b, err := json.Marshal(merged)
		if err != nil {
			return nil, fmt.Errorf("marshal merged skill config values: %w", err)
		}
		raw := string(b)
		serialized = &raw
	}

	agentIDsBeforeTx := s.affectedAgentIDsForSkill(ctx, sk.ID, sk.Name)

	if err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := s.skillRepo.SetSkillConfigInTx(ctx, tx, skillID, serialized, userID); err != nil {
			return fmt.Errorf("persist skill config: %w", err)
		}
		return s.writeSkillOutboxEventInTx(ctx, tx, eventSkillConfigUpdated, sk.ID, sk.Name, userID, agentIDsBeforeTx)
	}); err != nil {
		return nil, err
	}
	s.notifyOutbox()

	return s.GetConfig(ctx, skillID)
}

func (s *SkillService) BuildSkillConfigSummary(ctx context.Context, skillID, discoveryStatus string) (*ConfigSummary, error) {
	if discoveryStatus != "ready" {
		return nil, nil
	}
	sk, err := s.skillRepo.GetByID(ctx, skillID)
	if err != nil {
		return nil, fmt.Errorf(getSkillErrFmt, err)
	}
	if sk == nil {
		return nil, &NotFoundError{Resource: resourceSkill, ID: skillID}
	}
	return buildSkillConfigSummaryFromLoaded(sk.Name, sk.BundledTools, sk.DiscoveryStatus, s.loadSkillConfigValues(ctx, skillID)), nil
}

func (s *SkillService) loadSkillConfigValues(ctx context.Context, skillID string) map[string]map[string]any {
	cfg, err := s.skillRepo.GetSkillConfig(ctx, skillID)
	if err != nil || cfg == nil || cfg.ConfigValues == nil || *cfg.ConfigValues == "" {
		return nil
	}
	var stored map[string]map[string]any
	if err := json.Unmarshal([]byte(*cfg.ConfigValues), &stored); err != nil {
		s.logger.Warn("skill config: stored values not valid JSON for summary", "skillID", skillID, "error", err)
		return nil
	}
	return stored
}

func buildSkillConfigSummaryFromLoaded(skillName string, bundledTools *string, discoveryStatus string, stored map[string]map[string]any) *ConfigSummary {
	if discoveryStatus != "ready" {
		return nil
	}
	schemasByTool := parseSkillBundledToolSchemas(skillName, bundledTools)
	required := []string{}
	for toolName, fields := range schemasByTool {
		for _, f := range fields {
			if f.isRequired() {
				required = append(required, toolName+"."+f.Key)
			}
		}
	}
	covered := []string{}
	for toolName, fields := range schemasByTool {
		toolVals := stored[toolName]
		for _, f := range fields {
			if !f.isRequired() {
				continue
			}
			if v, ok := toolVals[f.Key]; ok && v != nil {
				covered = append(covered, toolName+"."+f.Key)
			}
		}
	}
	slices.Sort(required)
	slices.Sort(covered)
	return &ConfigSummary{
		RequiredFields:            required,
		RequiredCoveredByDefaults: covered,
		RequiresAgentConfig:       len(covered) < len(required),
	}
}

func validateSkillPatchValues(values map[string]map[string]any, schemasByTool map[string][]configSchemaField) map[string][]string {
	details := map[string][]string{}
	for toolName, vals := range values {
		toolSchema, ok := schemasByTool[toolName]
		if !ok {
			details[toolName] = append(details[toolName], "unknown bundled tool for this skill")
			continue
		}
		schemaByKey := map[string]configSchemaField{}
		for _, f := range toolSchema {
			schemaByKey[f.Key] = f
		}
		for key, val := range vals {
			entry, ok := schemaByKey[key]
			if !ok {
				details[toolName+"."+key] = append(details[toolName+"."+key], "unknown configuration field for this bundled tool")
				continue
			}
			if msg := validateConfigValueType(entry, val); msg != "" {
				details[toolName+"."+key] = append(details[toolName+"."+key], msg)
			}
		}
	}
	return details
}

func (s *SkillService) loadExistingSkillConfigValues(ctx context.Context, skillID string) (map[string]map[string]any, error) {
	existing := map[string]map[string]any{}
	row, err := s.skillRepo.GetSkillConfig(ctx, skillID)
	if err != nil {
		return nil, fmt.Errorf("get skill config: %w", err)
	}
	if row == nil || row.ConfigValues == nil || *row.ConfigValues == "" {
		return existing, nil
	}
	if err := json.Unmarshal([]byte(*row.ConfigValues), &existing); err != nil {
		s.logger.Warn("skill config: existing values not valid JSON, treating as empty", "skillID", skillID, "error", err)
		return map[string]map[string]any{}, nil
	}
	return existing, nil
}

func (s *SkillService) mergeSkillConfigTool(toolName string, incoming, existingTool map[string]any, toolSchema []configSchemaField, skillID string) (map[string]any, error) {
	if existingTool == nil {
		existingTool = map[string]any{}
	}
	working := make(map[string]any, len(incoming))
	maps.Copy(working, incoming)
	if _, err := mergeSecretFields(working, existingTool, skillSecretFields(toolSchema)); err != nil {
		return nil, err
	}

	merged := make(map[string]any, len(existingTool)+len(working))
	maps.Copy(merged, existingTool)
	for k, v := range working {
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}

	schemaByKey := map[string]struct{}{}
	for _, f := range toolSchema {
		schemaByKey[f.Key] = struct{}{}
	}
	for k := range merged {
		if _, ok := schemaByKey[k]; !ok {
			s.logger.Debug("skill config: dropping orphan key", "skillID", skillID, "tool", toolName, "key", k)
			delete(merged, k)
		}
	}
	return merged, nil
}

var _ = store.SkillConfig{}

var _ = (*dbutil.Tx)(nil)
