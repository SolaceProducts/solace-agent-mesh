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

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ToolPackageInfo struct {
	ToolFiles     []store.ToolPackageFile
	ConfigValues  map[string]map[string]any
	ExcludedTools []string
}

type StandardAgentDeployer struct {
	yamlSvc         *YAMLService
	connectorRepo   *store.ConnectorRepo
	toolPackageRepo *store.ToolPackageRepo
	skillRepo       *store.SkillRepo
	logger          *slog.Logger
}

func NewStandardAgentDeployer(yamlSvc *YAMLService, connectorRepo *store.ConnectorRepo, toolPackageRepo *store.ToolPackageRepo, skillRepo *store.SkillRepo, logger *slog.Logger) *StandardAgentDeployer {
	return &StandardAgentDeployer{yamlSvc: yamlSvc, connectorRepo: connectorRepo, toolPackageRepo: toolPackageRepo, skillRepo: skillRepo, logger: logger}
}

func (*StandardAgentDeployer) Kind() string { return "agent" }

func (d *StandardAgentDeployer) Validate(ctx context.Context, input DeployInput) error {
	if input.Agent == nil {
		return &ValidationError{Message: "agent is required"}
	}
	if input.Action == DeployActionUndeploy {
		return nil
	}
	if input.Agent.SystemPrompt == nil || *input.Agent.SystemPrompt == "" {
		return &ValidationError{Message: "agent must have a system prompt before deploying"}
	}
	if err := requireConnectorsFilled(ctx, d.connectorRepo, input.ConnectorIDs, unfilledAgentMessage); err != nil {
		return err
	}
	if err := d.validateSecretFields(ctx, input); err != nil {
		return err
	}
	if err := d.validateSkillSecretFields(ctx, input); err != nil {
		return err
	}
	return nil
}

func (d *StandardAgentDeployer) validateSkillSecretFields(ctx context.Context, input DeployInput) error {
	if d.skillRepo == nil || len(input.SkillIDs) == 0 {
		return nil
	}
	configs, err := resolveAttachedSkillConfigs(ctx, input.Agent.ID, input.SkillIDs, d.skillRepo, d.logger)
	if err != nil {
		return fmt.Errorf("resolve attached skill configs: %w", err)
	}
	return validateSkillRequiredFields(configs)
}

func (d *StandardAgentDeployer) validateSecretFields(ctx context.Context, input DeployInput) error {
	skillToolsets, err := resolveSkillImpliedToolsets(ctx, d.skillRepo, input.SkillIDs, d.logger)
	if err != nil {
		return fmt.Errorf("resolve skill-implied toolsets: %w", err)
	}
	effectiveToolsets := unionToolsets(input.Toolsets, skillToolsets)

	packageInfos, err := d.resolveToolPackagesWithConfigs(ctx, input.Agent.ID, effectiveToolsets, input.ToolsetExclusions)
	if err != nil {
		return fmt.Errorf("resolve tool packages: %w", err)
	}
	var missing []string
	missingByToolset := make(map[string][]string)
	for ts, pkg := range packageInfos {
		for _, f := range pkg.ToolFiles {
			if f.ConfigSchema == nil || *f.ConfigSchema == "" {
				continue
			}
			var schema []map[string]any
			if err := json.Unmarshal([]byte(*f.ConfigSchema), &schema); err != nil {
				continue
			}
			for _, field := range schema {
				isSecret, _ := field["secret"].(bool)
				if !isSecret {
					continue
				}
				key, _ := field["key"].(string)
				if key == "" {
					continue
				}
				if _, hasDefault := field["default"]; hasDefault {
					continue
				}
				if toolCfg, ok := pkg.ConfigValues[f.ToolName]; ok {
					if v, hasValue := toolCfg[key]; hasValue && v != nil && v != "" {
						continue
					}
				}
				missing = append(missing, fmt.Sprintf("%s/%s/%s", ts, f.ToolName, key))
				missingByToolset[ts] = append(missingByToolset[ts], strings.ToUpper(key))
			}
		}
	}
	if len(missing) > 0 {
		return &ValidationError{Message: formatMissingSecretsError(missing, missingByToolset)}
	}
	return nil
}

func formatMissingSecretsError(missing []string, missingByToolset map[string][]string) string {
	var b strings.Builder
	b.WriteString("missing required secret config values: ")
	b.WriteString(strings.Join(missing, ", "))
	b.WriteString("\n\nResolve by either:\n")
	b.WriteString("  - exporting the referenced env vars in the shell running `sam config apply`, or\n")
	b.WriteString("  - placing them in <SAM home>/secrets/<toolset>.env (KEY=value per line; the CLI auto-loads this dir)\n")
	if len(missingByToolset) > 0 {
		b.WriteString("\nExamples:\n")
		toolsets := make([]string, 0, len(missingByToolset))
		for ts := range missingByToolset {
			toolsets = append(toolsets, ts)
		}
		slices.Sort(toolsets)
		for _, ts := range toolsets {
			keys := missingByToolset[ts]
			fmt.Fprintf(&b, "  <SAM home>/secrets/%s.env: %s=...\n", ts, keys[0])
		}
	}
	b.WriteString("\nAuthor spec.toolsetConfigs[*].configValues as a FLAT map keyed by the\n")
	b.WriteString("schema field name — e.g. {api_key: ${MY_API_KEY}}, not\n")
	b.WriteString("{tool_name: {api_key: ${MY_API_KEY}}}. Schema field keys are globally\n")
	b.WriteString("unique across a package's tools, so the same flat value is fanned out\n")
	b.WriteString("to every tool that declares the field in its config_schema.\n")
	return b.String()
}

func (*StandardAgentDeployer) InstanceName(agent *store.Agent) string {
	return AgentInstanceName(agent)
}

func (d *StandardAgentDeployer) BuildPayloads(ctx context.Context, input DeployInput) (BuildResult, error) {
	if input.Agent == nil {
		return BuildResult{}, fmt.Errorf("agent is nil")
	}

	connectors, err := d.resolveConnectors(ctx, input.Agent.ID, input.ConnectorIDs)
	if err != nil {
		return BuildResult{}, err
	}

	skillToolsets, err := resolveSkillImpliedToolsets(ctx, d.skillRepo, input.SkillIDs, d.logger)
	if err != nil {
		return BuildResult{}, err
	}
	effectiveToolsets := unionToolsets(input.Toolsets, skillToolsets)

	packageInfos, err := d.resolveToolPackagesWithConfigs(ctx, input.Agent.ID, effectiveToolsets, input.ToolsetExclusions)
	if err != nil {
		return BuildResult{}, err
	}

	emission, err := resolveBuiltinToolConfigs(ctx, input.Agent.ID, d.toolPackageRepo, d.logger)
	if err != nil {
		return BuildResult{}, err
	}

	skillConfigs, err := resolveAttachedSkillConfigs(ctx, input.Agent.ID, input.SkillIDs, d.skillRepo, d.logger)
	if err != nil {
		return BuildResult{}, err
	}
	if len(skillConfigs) > 0 {
		if input.Opts.SkillConfigsByName == nil {
			input.Opts.SkillConfigsByName = make(map[string]map[string]map[string]any, len(skillConfigs))
		}
		for skillName, info := range skillConfigs {
			merged := buildPerSkillToolConfigWithSchemaDefaults(info)
			if len(merged) > 0 {
				input.Opts.SkillConfigsByName[skillName] = merged
			}
		}
	}

	builtinExcluded, err := decodeExclusions(input.ToolsetExclusions)
	if err != nil {
		return BuildResult{}, fmt.Errorf("agent %q has unreadable toolset exclusions: %w", input.Agent.Name, err)
	}
	emission.Packages = packageInfos
	emission.BuiltinExcludedTools = builtinExcluded
	config, err := d.yamlSvc.BuildAgentConfigFull(input.Agent, effectiveToolsets, input.InputModes, input.OutputModes, connectors, emission, input.Opts)
	if err != nil {
		return BuildResult{}, fmt.Errorf("build agent config: %w", err)
	}

	yamlStr, err := configbuilder.MarshalYAML(config)
	if err != nil {
		return BuildResult{}, fmt.Errorf("marshal agent config: %w", err)
	}

	payloads, err := configbuilder.ToControlPayloads(config)
	if err != nil {
		return BuildResult{}, fmt.Errorf("extract control payloads: %w", err)
	}

	card := &agentCardSurface{Name: input.Agent.Name, Description: derefStr(input.Agent.Description)}
	surfaceHashes := d.snapshotSurfaceHashes(ctx, input.Agent.Name, yamlStr, card)

	var skillRefs []string
	if input.Agent.SkillRefs != nil && *input.Agent.SkillRefs != "" {
		var refs []string
		if err := json.Unmarshal([]byte(*input.Agent.SkillRefs), &refs); err != nil {
			d.logger.Warn("parse skill_refs for deploy snapshot", "agentID", input.Agent.ID, "error", err)
		}
		hasM2M := len(input.SkillIDs) > 0
		for _, ref := range refs {
			if keepSkillRef(ref, hasM2M) {
				skillRefs = append(skillRefs, ref)
			}
		}
	}

	var skills []SkillResponse
	if input.Agent.Skills != nil && *input.Agent.Skills != "" {
		if err := json.Unmarshal([]byte(*input.Agent.Skills), &skills); err != nil {
			d.logger.Warn("parse skills for deploy snapshot", "agentID", input.Agent.ID, "error", err)
		}
	}

	dataSnapshot := DeployedAgentInfo{
		Name:                     input.Agent.Name,
		Description:              derefStr(input.Agent.Description),
		SystemPrompt:             derefStr(input.Agent.SystemPrompt),
		Toolsets:                 ensureSlice(input.Toolsets),
		InputModes:               ensureSlice(input.InputModes),
		OutputModes:              ensureSlice(input.OutputModes),
		Connectors:               ensureSlice(input.ConnectorIDs),
		ConnectorSnapshots:       resolveConnectorSnapshots(ctx, input.ConnectorIDs, d.connectorRepo),
		SkillIDs:                 ensureSlice(input.SkillIDs),
		SkillRefs:                skillRefs,
		Skills:                   skills,
		AdditionalConfigurations: cloneAdditionalConfigurations(input.Agent.AdditionalConfigurations),
	}

	return BuildResult{
		Payloads:      payloads,
		SnapshotYAML:  yamlStr,
		DataSnapshot:  dataSnapshot,
		ConfigHash:    computeAgentConfigHash(input.Agent, input.Toolsets, input.InputModes, input.OutputModes, connectorConfigFingerprints(ctx, d.connectorRepo, input.ConnectorIDs), input.SkillIDs),
		SurfaceHashes: surfaceHashes,
	}, nil
}

func (d *StandardAgentDeployer) snapshotSurfaceHashes(ctx context.Context, agentName, snapshotYAML string, card *agentCardSurface) AgentSurfaceHashes {
	replayed, err := d.ReplayFromSnapshot(snapshotYAML)
	if err != nil {
		d.logger.Warn("agent config snapshot does not replay; its surface hashes stay unknown", "agentName", agentName, "error", err)
		replayed = nil
	}
	hashes, err := computeAgentSurfaceHashes(ctx, replayed, card, skillContentDigests(d.skillRepo))
	if err != nil {
		d.logger.Warn("some agent surface hashes could not be computed; they stay unknown", "agentName", agentName, "error", err)
	}
	return hashes
}

func (*StandardAgentDeployer) ReplayFromSnapshot(snapshotYAML string) ([]ControlPayload, error) {
	if snapshotYAML == "" {
		return nil, ErrNoReplay
	}
	payloads, err := configbuilder.ParseYAMLToControlPayloads([]byte(snapshotYAML))
	if err != nil {
		return nil, fmt.Errorf("parse snapshot YAML: %w", err)
	}
	return payloads, nil
}

func (d *StandardAgentDeployer) resolveToolPackagesWithConfigs(ctx context.Context, agentID string, toolsets []string, toolsetExclusions map[string]string) (map[string]ToolPackageInfo, error) {
	return resolveToolPackageInfos(ctx, agentID, toolsets, toolsetExclusions, d.toolPackageRepo, d.logger)
}

func (d *StandardAgentDeployer) resolveConnectors(ctx context.Context, agentID string, connectorIDs []string) ([]ConnectorConfig, error) {
	logger := d.logger
	if logger != nil {
		logger = logger.With("agentID", agentID)
	}
	return resolveConnectorValues(ctx, connectorIDs, d.connectorRepo, logger)
}

type resolvedPackage struct {
	ts       string
	pkg      *store.ToolPackage
	files    []store.ToolPackageFile
	excluded []string
}

func resolveToolPackageInfos(ctx context.Context, agentID string, toolsets []string, toolsetExclusions map[string]string, repo *store.ToolPackageRepo, logger *slog.Logger) (map[string]ToolPackageInfo, error) {
	if repo == nil || len(toolsets) == 0 {
		return nil, nil
	}

	agentValsByPkgID, err := loadAgentValsByPkgID(ctx, agentID, repo, logger)
	if err != nil {
		return nil, err
	}

	resolvedList, pkgIDs, err := resolveReadyPackages(ctx, toolsets, toolsetExclusions, repo)
	if err != nil {
		return nil, err
	}

	toolsetCfgByPkgID, err := repo.GetToolsetConfigsForPackages(ctx, pkgIDs)
	if err != nil {
		return nil, fmt.Errorf("get toolset configs for packages: %w", err)
	}

	packages := make(map[string]ToolPackageInfo, len(resolvedList))
	for _, r := range resolvedList {
		tsVals := parseToolsetVals(toolsetCfgByPkgID, r.pkg.ID, logger)
		tsVals = normalizeToolsetConfigShape(r.files, tsVals, logger, agentID, r.pkg.ID, "toolset_configs")
		agentVals := normalizeToolsetConfigShape(r.files, agentValsByPkgID[r.pkg.ID], logger, agentID, r.pkg.ID, "agent_toolset_configs")
		warnOrphanRequiredScopes(r.ts, r.files, tsVals, agentVals, logger)
		perTool := buildPerToolConfig(r.files, tsVals, agentVals)
		packages[r.ts] = ToolPackageInfo{
			ToolFiles:     r.files,
			ConfigValues:  perTool,
			ExcludedTools: r.excluded,
		}
	}
	return packages, nil
}

func loadAgentValsByPkgID(ctx context.Context, agentID string, repo *store.ToolPackageRepo, logger *slog.Logger) (map[string]map[string]any, error) {
	if agentID == "" {
		return map[string]map[string]any{}, nil
	}
	configs, err := repo.GetToolsetConfigsForAgent(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("get toolset configs for agent: %w", err)
	}
	out := make(map[string]map[string]any, len(configs))
	for _, cfg := range configs {
		if cfg.ConfigValues == nil || *cfg.ConfigValues == "" {
			continue
		}
		var vals map[string]any
		if err := json.Unmarshal([]byte(*cfg.ConfigValues), &vals); err != nil {
			if logger != nil {
				logger.Warn("malformed config_values JSON — skipping", "agentID", agentID, "packageID", cfg.PackageID, "error", err)
			}
			continue
		}
		out[cfg.PackageID] = vals
	}
	return out, nil
}

func resolveReadyPackages(ctx context.Context, toolsets []string, toolsetExclusions map[string]string, repo *store.ToolPackageRepo) ([]resolvedPackage, []string, error) {
	resolvedList := make([]resolvedPackage, 0, len(toolsets))
	pkgIDs := make([]string, 0, len(toolsets))
	for _, ts := range toolsets {
		pkg, err := repo.GetByName(ctx, ts)
		if err != nil {
			return nil, nil, fmt.Errorf("look up tool package %q: %w", ts, err)
		}
		if pkg == nil || pkg.DiscoveryStatus != "ready" {
			continue
		}
		files, err := repo.GetFilesForPackage(ctx, pkg.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("list files for package %q: %w", ts, err)
		}
		var excluded []string
		if jsonStr, ok := toolsetExclusions[ts]; ok {
			_ = json.Unmarshal([]byte(jsonStr), &excluded)
		}
		pkgIDs = append(pkgIDs, pkg.ID)
		resolvedList = append(resolvedList, resolvedPackage{ts: ts, pkg: pkg, files: files, excluded: excluded})
	}
	return resolvedList, pkgIDs, nil
}

func parseToolsetVals(toolsetCfgByPkgID map[string]store.ToolsetConfig, packageID string, logger *slog.Logger) map[string]any {
	tsCfg, ok := toolsetCfgByPkgID[packageID]
	if !ok || tsCfg.ConfigValues == nil || *tsCfg.ConfigValues == "" {
		return nil
	}
	var vals map[string]any
	if err := json.Unmarshal([]byte(*tsCfg.ConfigValues), &vals); err != nil {
		if logger != nil {
			logger.Warn("malformed toolset_configs.config_values — skipping", "packageID", packageID, "error", err)
		}
		return nil
	}
	return vals
}

func buildPerToolConfig(files []store.ToolPackageFile, tsVals map[string]any, agentVals map[string]any) map[string]map[string]any {
	var mergedAuth any
	tsAuth := tsVals[reservedAuthConfigKey]
	agentAuth := agentVals[reservedAuthConfigKey]
	switch {
	case agentAuth != nil:
		mergedAuth = mergeDeployerAuthBlock(agentAuth, tsAuth)
	case tsAuth != nil:
		mergedAuth = tsAuth
	}

	tsHIL, _ := tsVals[reservedHILConfigKey].(map[string]any)
	agentHIL, _ := agentVals[reservedHILConfigKey].(map[string]any)

	tsScopes, _ := tsVals[reservedRequiredScopesConfigKey].(map[string]any)
	agentScopes, _ := agentVals[reservedRequiredScopesConfigKey].(map[string]any)

	perTool := make(map[string]map[string]any, len(files))
	for _, f := range files {
		merged := make(map[string]any)
		if tsToolVals, ok := tsVals[f.ToolName]; ok {
			if tv, ok := tsToolVals.(map[string]any); ok {
				for k, v := range tv {
					if v != nil {
						merged[k] = v
					}
				}
			}
		}
		if agentToolVals, ok := agentVals[f.ToolName]; ok {
			if av, ok := agentToolVals.(map[string]any); ok {
				for k, v := range av {
					if v == nil {
						delete(merged, k)
					} else {
						merged[k] = v
					}
				}
			}
		}
		if mergedAuth != nil {
			merged[reservedAuthConfigKey] = mergedAuth
		}
		if h := hilForTool(agentHIL, tsHIL, f.ToolName); h != nil {
			merged[reservedHILConfigKey] = h
		}
		if s := scopesForTool(agentScopes, tsScopes, f.ToolName); len(s) > 0 {
			merged[reservedRequiredScopesConfigKey] = s
		}
		if len(merged) > 0 {
			perTool[f.ToolName] = merged
		}
	}
	return perTool
}

func warnOrphanRequiredScopes(toolset string, files []store.ToolPackageFile, tsVals, agentVals map[string]any, logger *slog.Logger) {
	if logger == nil {
		return
	}
	names := toolNamesFromFiles(files)
	for _, layer := range []struct {
		vals  map[string]any
		label string
	}{{tsVals, "toolset"}, {agentVals, "agent"}} {
		block, _ := layer.vals[reservedRequiredScopesConfigKey].(map[string]any)
		for tool := range block {
			if _, ok := names[tool]; !ok {
				logger.Warn("required_scopes names a tool the package no longer registers; that tool deploys UNGATED while the API still reports it gated — re-author the gate under the tool's current name, or re-send the block without this entry to drop it",
					"toolset", toolset, "tool", tool, "layer", layer.label)
			}
		}
	}
}

func normalizeToolsetConfigShape(files []store.ToolPackageFile, vals map[string]any, logger *slog.Logger, agentID, packageID, source string) map[string]any {
	if len(vals) == 0 {
		return vals
	}
	toolNames := make(map[string]struct{}, len(files))
	for _, f := range files {
		toolNames[f.ToolName] = struct{}{}
	}
	hasNonReservedKey := false
	for k := range vals {
		if k != reservedAuthConfigKey && k != reservedHILConfigKey && k != reservedRequiredScopesConfigKey {
			hasNonReservedKey = true
			break
		}
	}
	if !hasNonReservedKey {
		return vals
	}
	for k, v := range vals {
		if k == reservedAuthConfigKey || k == reservedHILConfigKey || k == reservedRequiredScopesConfigKey {
			continue
		}
		if _, isToolName := toolNames[k]; !isToolName {
			continue
		}
		if _, isMap := v.(map[string]any); isMap {
			return vals
		}
	}

	type toolSchema struct {
		name string
		keys map[string]struct{}
	}
	var schemas []toolSchema
	allDeclared := make(map[string]struct{})
	for _, f := range files {
		if f.ConfigSchema == nil || *f.ConfigSchema == "" {
			continue
		}
		var schema []map[string]any
		if err := json.Unmarshal([]byte(*f.ConfigSchema), &schema); err != nil {
			continue
		}
		keys := make(map[string]struct{}, len(schema))
		for _, entry := range schema {
			key, _ := entry["key"].(string)
			if key == "" {
				continue
			}
			keys[key] = struct{}{}
			allDeclared[key] = struct{}{}
		}
		schemas = append(schemas, toolSchema{name: f.ToolName, keys: keys})
	}

	nested := make(map[string]any, len(schemas))
	for _, s := range schemas {
		toolVals := map[string]any{}
		for key, val := range vals {
			if _, declared := s.keys[key]; declared {
				toolVals[key] = val
			}
		}
		if len(toolVals) > 0 {
			nested[s.name] = toolVals
		}
	}
	for k, v := range vals {
		if k == reservedAuthConfigKey || k == reservedHILConfigKey || k == reservedRequiredScopesConfigKey {
			nested[k] = v
			continue
		}
		if _, declared := allDeclared[k]; declared {
			continue
		}
		if _, isToolName := toolNames[k]; isToolName {
			continue
		}
		nested[k] = v
	}

	if logger != nil {
		logger.Warn(
			"legacy flat-shaped toolset config detected — converted to nested for read; re-apply this toolset to migrate the stored row",
			"agentID", agentID,
			"packageID", packageID,
			"source", source,
			"fields", len(allDeclared),
		)
	}
	return nested
}

func resolveBuiltinToolConfigs(ctx context.Context, agentID string, repo *store.ToolPackageRepo, logger *slog.Logger) (ToolEmission, error) {
	if repo == nil || agentID == "" {
		return ToolEmission{}, nil
	}
	rows, err := repo.GetBuiltinToolsetConfigsForAgent(ctx, agentID)
	if err != nil {
		return ToolEmission{}, fmt.Errorf("get builtin toolset configs for agent: %w", err)
	}
	if len(rows) == 0 {
		return ToolEmission{}, nil
	}
	out := make(map[string]map[string]map[string]any, len(rows))
	hil := make(map[string]map[string]any, len(rows))
	volumeBindings := make(map[string]map[string]string, len(rows))
	networkBindings := make(map[string]map[string]string, len(rows))
	unmarshalBindings := func(raw *string, kind string, dst map[string]map[string]string, toolset string) {
		if raw == nil || *raw == "" {
			return
		}
		var binds map[string]string
		if err := json.Unmarshal([]byte(*raw), &binds); err != nil {
			if logger != nil {
				logger.Warn("malformed builtin toolset bindings JSON — skipping", "kind", kind, "agentID", agentID, "toolset", toolset, "error", err)
			}
		} else if len(binds) > 0 {
			dst[toolset] = binds
		}
	}
	for _, r := range rows {
		if r.ConfigValues != nil && *r.ConfigValues != "" {
			var raw map[string]any
			if err := json.Unmarshal([]byte(*r.ConfigValues), &raw); err != nil {
				if logger != nil {
					logger.Warn("malformed builtin toolset config_values JSON — skipping", "agentID", agentID, "toolset", r.ToolsetName, "error", err)
				}
			} else {
				if gates := splitBuiltinHIL(raw, r.ToolsetName, agentID, logger); len(gates) > 0 {
					hil[r.ToolsetName] = gates
				}
				if nested := builtinToolConfigsOf(raw, r.ToolsetName, agentID, logger); len(nested) > 0 {
					out[r.ToolsetName] = nested
				}
			}
		}
		unmarshalBindings(r.VolumeBindings, "volume", volumeBindings, r.ToolsetName)
		unmarshalBindings(r.NetworkBindings, "network", networkBindings, r.ToolsetName)
	}
	if len(out) == 0 {
		out = nil
	}
	if len(volumeBindings) == 0 {
		volumeBindings = nil
	}
	if len(networkBindings) == 0 {
		networkBindings = nil
	}
	if len(hil) == 0 {
		hil = nil
	}
	return ToolEmission{
		BuiltinToolConfigs:     out,
		BuiltinVolumeBindings:  volumeBindings,
		BuiltinNetworkBindings: networkBindings,
		BuiltinToolHIL:         hil,
	}, nil
}

func splitBuiltinHIL(raw map[string]any, toolset, agentID string, logger *slog.Logger) map[string]any {
	value, ok := raw[reservedHILConfigKey]
	if !ok {
		return nil
	}
	delete(raw, reservedHILConfigKey)
	gates, isMap := value.(map[string]any)
	if !isMap && value != nil && logger != nil {
		logger.Warn("builtin toolset hil is not an object — skipping", "agentID", agentID, "toolset", toolset)
	}
	return gates
}

func builtinToolConfigsOf(raw map[string]any, toolset, agentID string, logger *slog.Logger) map[string]map[string]any {
	nested := make(map[string]map[string]any, len(raw))
	for tool, value := range raw {
		if value == nil {
			continue
		}
		cfg, ok := value.(map[string]any)
		if !ok {
			if logger != nil {
				logger.Warn("builtin toolset tool config is not an object — skipping the tool", "agentID", agentID, "toolset", toolset, "tool", tool)
			}
			continue
		}
		nested[tool] = cfg
	}
	return nested
}

func resolveConnectorSnapshots(ctx context.Context, connectorIDs []string, repo *store.ConnectorRepo) []ConnectorSnapshot {
	if repo == nil || len(connectorIDs) == 0 {
		return nil
	}
	out := make([]ConnectorSnapshot, 0, len(connectorIDs))
	for _, connID := range connectorIDs {
		conn, err := repo.GetByID(ctx, connID)
		if err != nil || conn == nil {
			continue
		}
		cs := ConnectorSnapshot{ID: conn.ID, Name: conn.Name, Type: conn.Type}
		if conn.Subtype != nil {
			cs.Subtype = *conn.Subtype
		}
		out = append(out, cs)
	}
	return out
}

func resolveConnectorValues(ctx context.Context, connectorIDs []string, repo *store.ConnectorRepo, logger *slog.Logger) ([]ConnectorConfig, error) {
	if repo == nil || len(connectorIDs) == 0 {
		return nil, nil
	}
	connectors := make([]ConnectorConfig, 0, len(connectorIDs))
	for _, connID := range connectorIDs {
		conn, err := repo.GetByID(ctx, connID)
		if err != nil && logger != nil {
			logger.Warn("could not read a bound connector; deploying the agent without it", "connectorID", connID, "error", err)
		}
		if err != nil || conn == nil {
			continue
		}
		cc := ConnectorConfig{ID: conn.ID, Name: conn.Name, Type: conn.Type}
		if conn.Subtype != nil {
			cc.Subtype = *conn.Subtype
		}
		if conn.ResourceKey != nil {
			cc.ResourceKey = *conn.ResourceKey
		}
		if conn.Values != nil {
			var vals map[string]any
			if err := json.Unmarshal([]byte(*conn.Values), &vals); err != nil {
				if logger != nil {
					logger.Warn("malformed connector values JSON — skipping", "connectorID", connID, "error", err)
				}
				continue
			}
			cc.Values = vals
		}
		connectors = append(connectors, cc)
	}
	return connectors, nil
}
