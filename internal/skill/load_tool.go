// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type SkillLoader interface {
	LoadSkill(ctx context.Context, skillName string, toolCtx *tool.Context) (map[string]any, error)
}

type ToolRegistrar interface {
	RegisterSkillBundledTool(bt remotetool.SkillBundledTool)

	RegisterSkillTools(skillName string, configs []map[string]any) (registered []string, retry []map[string]any)

	ToolInstructions(toolName string) string
}

type LoadSkillTool struct {
	tool.SideEffectingTool
	manager   *Manager
	loader    SkillLoader
	registrar ToolRegistrar
}

func NewLoadSkillTool(mgr *Manager, loader SkillLoader, registrar ToolRegistrar) *LoadSkillTool {
	return &LoadSkillTool{manager: mgr, loader: loader, registrar: registrar}
}

func (t *LoadSkillTool) Name() string { return agent.LoadSkillToolName }

func (t *LoadSkillTool) Description() string {
	skills := t.manager.ListAvailable()
	if len(skills) == 0 {
		return "Load a skill to activate its instructions and tools."
	}
	var lines []string
	lines = append(lines, "Load a skill to activate its instructions and tools. Available skills:")
	for _, s := range skills {
		if s.Description != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", s.Name, s.Description))
		} else {
			lines = append(lines, fmt.Sprintf("- %s", s.Name))
		}
	}
	return strings.Join(lines, "\n")
}

func (t *LoadSkillTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			agent.SkillNameArg: map[string]any{
				"type":        "string",
				"description": "The name of the skill to load.",
			},
		},
		Required: []string{agent.SkillNameArg},
	}
}

func (t *LoadSkillTool) Instructions() string {
	return "Load skills relevant to the user's request before proceeding. " +
		"The load_skill result carries the skill's instructions, which apply from then on; " +
		"its tools become available on the next turn. " +
		"When a loaded skill has resource files, its load_skill result names them: " +
		"search them with grep_skill_resources (skill_name, pattern), then read just the sections you need " +
		"with read_skill_resource (skill_name, path, and a start_line/end_line range around a match's line); " +
		"list_skill_resources (skill_name) lists them all."
}

func (t *LoadSkillTool) IsLongRunning() bool { return true }

func (t *LoadSkillTool) RawStringArgs() []string { return nil }

func (t *LoadSkillTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return nil
}

func (t *LoadSkillTool) ContextParam() string { return "" }

func (t *LoadSkillTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	name, _ := args[agent.SkillNameArg].(string)
	if name == "" {
		return tool.Error(fmt.Sprintf("'%s' parameter is required", agent.SkillNameArg)), nil
	}

	info := t.manager.GetAvailable(name)
	if info == nil {
		available := t.manager.ListAvailable()
		names := make([]string, len(available))
		for i, a := range available {
			names[i] = a.Name
		}
		return tool.Error(fmt.Sprintf("skill %q not found. Available skills: %s", name, strings.Join(names, ", "))), nil
	}

	if toolCtx.SkillState == nil {
		return tool.Error("skill loading is not available in this context"), nil
	}
	if toolCtx.SkillState.IsLoaded(name) && t.manager.GetLoaded(name) != nil {
		return t.alreadyLoadedResult(name), nil
	}

	if t.loader == nil {
		return tool.Error("skill loader not configured"), nil
	}

	result, err := t.loader.LoadSkill(ctx, name, toolCtx)
	if err != nil {
		return tool.Error(fmt.Sprintf("failed to load skill %q from STR: %v", name, err)), nil
	}

	ls := ParseLoadSkillResult(name, result)
	if _, err := t.manager.Hold(ctx, name, ls, t.registrar); err != nil {
		return tool.Error(fmt.Sprintf("loading skill %q stopped before its tools were ready: %v", name, err)), nil
	}
	markLoaded(name, toolCtx)

	data := t.deliveryData(name, ls)
	return t.delivered(name, loadedMessage(name, ls, data), data), nil
}

func (t *LoadSkillTool) alreadyLoadedResult(name string) *tool.Result {
	data := t.deliveryData(name, nil)
	msg := fmt.Sprintf("Skill '%s' is already loaded and its tools are available. Do not call load_skill again for this skill.", name)
	if _, ok := data[agent.SkillInstructionsDataKey]; ok {
		msg = fmt.Sprintf("Skill '%s' is already loaded and its tools are available; its current instructions are repeated in %s. Do not call load_skill again for this skill.", name, agent.SkillInstructionsDataKey)
	}
	return t.delivered(name, msg, data)
}

func markLoaded(name string, toolCtx *tool.Context) {
	if toolCtx.SkillState.IsLoaded(name) {
		skillLoaderLogger().Warn("load_skill: loaded a skill the session listed but the agent did not hold",
			"skill", name, "sessionID", toolCtx.SessionID)
		toolCtx.SkillState.Unload(name)
	}
	toolCtx.SkillState.Load(name)
}

func loadedMessage(name string, ls *LoadedSkill, data map[string]any) string {
	msg := fmt.Sprintf("Loaded skill '%s'. Its tools are available from your next step.", name)
	if _, ok := data[agent.SkillInstructionsDataKey]; ok {
		msg = fmt.Sprintf("Loaded skill '%s'. Follow its instructions (%s) from now on; its tools are available from your next step.", name, agent.SkillInstructionsDataKey)
	}
	if ls.ResourceCount > 0 {
		msg += " " + skillruntime.LoadedResourcesGuidance(name, ls.ResourceFiles, ls.ResourceCount)
	}
	if len(ls.Templates) > 0 {
		msg += " Asset templates you can fill with instantiate_template: " + describeTemplates(ls.Templates) +
			" Read a template's data contract with read_template (skill_name + asset) before instantiating."
	}
	return msg
}

func skillLoaderLogger() *slog.Logger {
	return slog.Default().With("logger_name", "skill-loader")
}

func (t *LoadSkillTool) deliveryData(name string, fetched *LoadedSkill) map[string]any {
	held := t.manager.GetLoaded(name)
	if held == nil {
		skillLoaderLogger().Warn("load_skill: no loaded copy held for a loaded skill",
			"skill", name, "fallbackToFetched", fetched != nil)
		held = fetched
	}
	data := map[string]any{agent.LoadedSkillDataKey: name}
	if held.hasInstructions() {
		data[agent.SkillInstructionsDataKey] = held.InstructionContent
	}
	if toolInstructions := t.skillToolInstructions(name); toolInstructions != "" {
		data[agent.ToolInstructionsDataKey] = toolInstructions
	}
	return data
}

func (t *LoadSkillTool) delivered(name, message string, data map[string]any) *tool.Result {
	result := tool.OK(message)
	result.LLMData = data
	return t.withLoadedVersion(name, result)
}

func (t *LoadSkillTool) skillToolInstructions(name string) string {
	if t.registrar == nil {
		return ""
	}
	var lines []string
	for _, toolName := range t.manager.SkillToolNames(name) {
		if inst := t.registrar.ToolInstructions(toolName); inst != "" {
			lines = append(lines, agent.ToolInstructionLine(toolName, inst))
		}
	}
	return strings.Join(lines, "\n\n")
}

func (t *LoadSkillTool) withLoadedVersion(name string, result *tool.Result) *tool.Result {
	if loaded := t.manager.GetLoaded(name); loaded != nil {
		result.LoadedSkillContentHash = loaded.ContentHash
	}
	return result
}

func ParseLoadSkillResult(skillName string, result map[string]any) *LoadedSkill {
	ls := &LoadedSkill{Name: skillName}

	if instructions, ok := result["instructions"].(string); ok {
		ls.InstructionContent = instructions
	}
	if hasRefs, ok := result["has_references"].(bool); ok {
		ls.HasReferences = hasRefs
	}
	if hasAssets, ok := result["has_assets"].(bool); ok {
		ls.HasAssets = hasAssets
	}
	if rc, ok := result["resource_count"].(float64); ok {
		ls.ResourceCount = int(rc)
	}
	if hash, ok := result["content_hash"].(string); ok {
		ls.ContentHash = hash
	}
	ls.ResourceFiles = stringList(result["resource_files"])

	if btRaw, ok := result["bundled_tools"]; ok {
		data, err := json.Marshal(btRaw)
		if err == nil {
			var tools []remotetool.SkillBundledTool
			if json.Unmarshal(data, &tools) == nil {
				ls.BundledTools = tools
			}
		}
	}

	ls.RequiredBuiltins = stringList(result["required_builtins"])

	if tplRaw, ok := result["templates"]; ok {
		data, err := json.Marshal(tplRaw)
		if err == nil {
			var tpls []remotetool.SkillTemplate
			if json.Unmarshal(data, &tpls) == nil {
				ls.Templates = tpls
			}
		}
	}

	if toolsRaw, ok := result["tools"]; ok {
		ls.Tools = coerceToolsList(toolsRaw)
	}

	if len(ls.RequiredBuiltins) > 0 {
		ls.Tools = skillruntime.MergeRequiredBuiltinsIntoTools(ls.RequiredBuiltins, ls.Tools)
	}

	return ls
}

func stringList(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func describeTemplates(tpls []remotetool.SkillTemplate) string {
	parts := make([]string, len(tpls))
	for i, t := range tpls {
		label := t.Name
		if t.Description != "" {
			label = fmt.Sprintf("%s (%s)", t.Name, t.Description)
		}
		if t.Asset != "" {
			label += fmt.Sprintf(" [asset: %s]", t.Asset)
		}
		parts[i] = label
	}
	return strings.Join(parts, ", ") + "."
}

func coerceToolsList(raw any) []map[string]any {
	switch v := raw.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for i, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				skillLoaderLogger().Debug("dropping non-map tool entry in skill tools list",
					"index", i, "type", fmt.Sprintf("%T", item))
				continue
			}
			out = append(out, m)
		}
		return out
	}
	return nil
}

type UnloadSkillTool struct {
	tool.ReadOnlyTool
}

func NewUnloadSkillTool() *UnloadSkillTool {
	return &UnloadSkillTool{}
}

func (t *UnloadSkillTool) Name() string { return agent.UnloadSkillToolName }

func (t *UnloadSkillTool) Description() string {
	return "Unload a previously loaded skill to remove its instructions and tools."
}

func (t *UnloadSkillTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			agent.SkillNameArg: map[string]any{
				"type":        "string",
				"description": "The name of the skill to unload.",
			},
		},
		Required: []string{agent.SkillNameArg},
	}
}

func (t *UnloadSkillTool) Instructions() string { return "" }

func (t *UnloadSkillTool) IsLongRunning() bool { return false }

func (t *UnloadSkillTool) RawStringArgs() []string { return nil }

func (t *UnloadSkillTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *UnloadSkillTool) ContextParam() string { return "" }

func (t *UnloadSkillTool) Execute(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	name, _ := args[agent.SkillNameArg].(string)
	if name == "" {
		return tool.Error(fmt.Sprintf("'%s' parameter is required", agent.SkillNameArg)), nil
	}

	if toolCtx.SkillState == nil {
		return tool.Error("skill loading is not available in this context"), nil
	}
	if !toolCtx.SkillState.IsLoaded(name) {
		return tool.Error(fmt.Sprintf("skill %q is not currently loaded", name)), nil
	}

	toolCtx.SkillState.Unload(name)

	return tool.OK(fmt.Sprintf("Unloaded skill '%s'. Disregard the instructions it gave when it was loaded; its tools are no longer available.", name)), nil
}
