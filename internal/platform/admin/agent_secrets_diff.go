// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (s *AgentService) GetOverlaySecretsDiff(ctx context.Context, agentID string, cand AgentOverlayCandidate) (map[string]SecretFieldDiffResult, error) {
	agent, _, _, _, _, err := s.agentRepo.GetByID(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if agent == nil {
		return nil, &NotFoundError{Resource: "agent", ID: agentID}
	}

	out := map[string]SecretFieldDiffResult{}
	if err := s.diffToolsetOverlaySecrets(ctx, agentID, cand.ToolsetConfigs, out); err != nil {
		return nil, err
	}
	if err := s.diffSkillOverlaySecrets(ctx, agentID, cand.SkillConfigs, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AgentService) diffToolsetOverlaySecrets(ctx context.Context, agentID string, bindings []ToolsetConfigBinding, out map[string]SecretFieldDiffResult) error {
	if len(bindings) == 0 || s.toolPackageRepo == nil {
		return nil
	}
	if err := s.diffBuiltinToolsetOverlaySecrets(ctx, agentID, bindings, out); err != nil {
		return err
	}
	return s.diffPackageToolsetOverlaySecrets(ctx, agentID, bindings, out)
}

func (s *AgentService) diffBuiltinToolsetOverlaySecrets(ctx context.Context, agentID string, bindings []ToolsetConfigBinding, out map[string]SecretFieldDiffResult) error {
	stored, err := priorBuiltinToolsetConfigValues(ctx, agentID, s.toolPackageRepo)
	if err != nil {
		return err
	}
	for _, b := range bindings {
		if _, isBuiltin := builtinToolsetTools[b.ToolsetName]; !isBuiltin {
			continue
		}
		for toolName, candRaw := range b.ConfigValues {
			secrets := builtinToolSecretFields(toolName)
			if len(secrets) == 0 {
				continue
			}
			storedTool := priorBuiltinToolValues(stored, b.ToolsetName, toolName)
			if storedTool == nil {
				continue
			}
			candTool, _ := candRaw.(map[string]any)
			mergeDiffResults(out, b.ToolsetName+"/"+toolName+".",
				compareSecrets(registryToSecretFields(secrets), candTool, storedTool))
		}
	}
	return nil
}

func (s *AgentService) diffPackageToolsetOverlaySecrets(ctx context.Context, agentID string, bindings []ToolsetConfigBinding, out map[string]SecretFieldDiffResult) error {
	rows, err := s.toolPackageRepo.GetToolsetConfigsForAgent(ctx, agentID)
	if err != nil {
		return fmt.Errorf("load agent toolset configs: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	pkgIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		pkgIDs = append(pkgIDs, r.PackageID)
	}
	nameByID, err := s.toolPackageRepo.GetPackageNamesByIDs(ctx, pkgIDs)
	if err != nil {
		return fmt.Errorf("look up package names: %w", err)
	}
	filesMap, err := s.toolPackageRepo.GetFilesForPackages(ctx, pkgIDs)
	if err != nil {
		return fmt.Errorf("load package files: %w", err)
	}

	type storedOverlay struct {
		packageID string
		values    map[string]any
	}
	storedByName := make(map[string]storedOverlay, len(rows))
	for _, r := range rows {
		name, ok := nameByID[r.PackageID]
		if !ok {
			continue
		}
		values := map[string]any{}
		if r.ConfigValues != nil && *r.ConfigValues != "" {
			_ = json.Unmarshal([]byte(*r.ConfigValues), &values)
		}
		storedByName[name] = storedOverlay{packageID: r.PackageID, values: values}
	}

	for _, b := range bindings {
		row, ok := storedByName[b.ToolsetName]
		if !ok {
			continue
		}
		for toolName, schema := range loadConfigSchemaByTool(filesMap[row.packageID]) {
			secrets := make(map[string]secretFieldInfo)
			for key, entry := range schema {
				if entry.Secret {
					secrets[key] = secretFieldInfo{Required: entry.isRequired()}
				}
			}
			if len(secrets) == 0 {
				continue
			}
			candTool, _ := b.ConfigValues[toolName].(map[string]any)
			storedTool, _ := row.values[toolName].(map[string]any)
			mergeDiffResults(out, b.ToolsetName+"/"+toolName+".",
				compareSecrets(registryToSecretFields(secrets), candTool, storedTool))
		}
	}
	return nil
}

func (s *AgentService) diffSkillOverlaySecrets(ctx context.Context, agentID string, bindings []SkillConfigBinding, out map[string]SecretFieldDiffResult) error {
	if len(bindings) == 0 || s.skillRepo == nil {
		return nil
	}
	rows, err := s.skillRepo.GetAgentSkillRowsForAgent(ctx, agentID)
	if err != nil {
		return fmt.Errorf("load agent skill rows: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rows))
	storedRawBySkillID := make(map[string]*string, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SkillID)
		storedRawBySkillID[r.SkillID] = r.ConfigValues
	}
	skills, err := s.skillRepo.GetByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("look up skills by ids: %w", err)
	}
	skillByID := make(map[string]store.Skill, len(skills))
	idByName := make(map[string]string, len(skills))
	for _, sk := range skills {
		skillByID[sk.ID] = sk
		idByName[sk.Name] = sk.ID
	}

	for _, b := range bindings {
		id := b.SkillID
		if id == "" {
			id = idByName[b.SkillName]
		}
		sk, ok := skillByID[id]
		if !ok {
			continue
		}
		stored := map[string]map[string]any{}
		if raw := storedRawBySkillID[id]; raw != nil && *raw != "" {
			_ = json.Unmarshal([]byte(*raw), &stored)
		}
		for toolName, schema := range parseSkillBundledToolSchemas(sk.Name, sk.BundledTools) {
			secrets := skillSecretFields(schema)
			if len(secrets) == 0 {
				continue
			}
			mergeDiffResults(out, sk.Name+"/"+toolName+".",
				compareSecrets(registryToSecretFields(secrets), b.ConfigValues[toolName], stored[toolName]))
		}
	}
	return nil
}

func mergeDiffResults(out map[string]SecretFieldDiffResult, prefix string, res map[string]SecretFieldDiffResult) {
	for name, r := range res {
		key := prefix + name
		if prev, seen := out[key]; seen {
			out[key] = SecretFieldDiffResult{Matches: prev.Matches && r.Matches}
		} else {
			out[key] = r
		}
	}
}
