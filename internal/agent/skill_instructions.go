// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	LoadSkillToolName   = "load_skill"
	UnloadSkillToolName = "unload_skill"
	SkillNameArg        = "name"
)

const (
	LoadedSkillDataKey       = "loaded_skill"
	SkillInstructionsDataKey = "skill_instructions"
	ToolInstructionsDataKey  = "tool_instructions"
)

func ToolInstructionLine(toolName, instructions string) string {
	return fmt.Sprintf("**%s**: %s", toolName, instructions)
}

type skillDelivery struct {
	Status           string `json:"status"`
	Skill            string `json:"loaded_skill"`
	Instructions     string `json:"skill_instructions"`
	ToolInstructions string `json:"tool_instructions"`
}

func skillsDeliveredInHistory(msgs []llm.ChatMessage) map[string]skillDelivery {
	callTool := make(map[string]string)
	callSkill := make(map[string]string)
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.Name == LoadSkillToolName || tc.Name == UnloadSkillToolName {
				callTool[tc.ID] = tc.Name
				callSkill[tc.ID] = skillNameOf(tc.Arguments)
			}
		}
	}
	if len(callTool) == 0 {
		return nil
	}
	delivered := make(map[string]skillDelivery)
	for _, m := range msgs {
		toolName, isSkillCall := callTool[m.ToolCallID]
		if m.Role != "tool" || !isSkillCall {
			continue
		}
		var result skillDelivery
		if json.Unmarshal([]byte(m.Content), &result) != nil || result.Status != tool.StatusSuccess {
			continue
		}
		switch toolName {
		case LoadSkillToolName:
			if result.Skill != "" {
				delivered[result.Skill] = result
			}
		case UnloadSkillToolName:
			delete(delivered, callSkill[m.ToolCallID])
		}
	}
	return delivered
}

func skillNameOf(arguments string) string {
	var args map[string]any
	_ = json.Unmarshal([]byte(arguments), &args)
	name, _ := args[SkillNameArg].(string)
	return name
}

type promptSkillState struct {
	withInstructions []LoadedSkillInfo
	disregard        []string
	retracted        []string
	undeliveredTools []string
}

func (p promptSkillState) equal(other promptSkillState) bool {
	return slices.Equal(p.withInstructions, other.withInstructions) &&
		slices.Equal(p.disregard, other.disregard) &&
		slices.Equal(p.retracted, other.retracted) &&
		slices.Equal(p.undeliveredTools, other.undeliveredTools)
}

func (p promptSkillState) instructedSkills() []string {
	names := make([]string, len(p.withInstructions))
	for i, s := range p.withInstructions {
		names[i] = s.Name
	}
	return names
}

func (a *Agent) promptSkills(skillState tool.SkillStateProvider, msgs []llm.ChatMessage) promptSkillState {
	delivered := skillsDeliveredInHistory(msgs)
	if a.skillManager == nil || skillState == nil {
		return promptSkillState{disregard: slices.Sorted(maps.Keys(delivered))}
	}
	var out promptSkillState
	var deliveredTools strings.Builder
	for _, d := range delivered {
		deliveredTools.WriteString(d.ToolInstructions)
		deliveredTools.WriteByte('\n')
	}
	var toolsToCheck []string
	for _, name := range skillState.LoadedNames() {
		d, wasDelivered := delivered[name]
		held, hasInstructions := a.heldInstructions(name)
		switch {
		case !a.skillManager.IsHeld(name):
			if wasDelivered {
				out.disregard = append(out.disregard, name)
			}
			continue
		case hasInstructions && (!wasDelivered || d.Instructions != held.InstructionContent):
			out.withInstructions = append(out.withInstructions, held)
			continue
		case !hasInstructions && wasDelivered && d.Instructions != "":
			out.retracted = append(out.retracted, name)
		}
		toolsToCheck = append(toolsToCheck, a.skillManager.SkillToolNames(name)...)
	}
	for _, toolName := range toolsToCheck {
		line := a.skillToolInstructionLine(toolName)
		if line != "" && !strings.Contains(deliveredTools.String(), line) && !slices.Contains(out.undeliveredTools, toolName) {
			out.undeliveredTools = append(out.undeliveredTools, toolName)
		}
	}
	slices.Sort(out.undeliveredTools)
	for _, name := range slices.Sorted(maps.Keys(delivered)) {
		if !skillState.IsLoaded(name) {
			out.disregard = append(out.disregard, name)
		}
	}
	slices.Sort(out.disregard)
	return out
}

func (a *Agent) rehydrateSkills(ctx context.Context, toolCtx *tool.Context) {
	if a.skillRehydrator == nil {
		return
	}
	loaded := toolCtx.SkillState.LoadedNames()
	if len(loaded) == 0 {
		return
	}
	failed := a.skillRehydrator.RehydrateSkills(ctx, toolCtx, loaded)
	if ctx.Err() != nil {
		return
	}
	for _, name := range failed {
		toolCtx.SkillState.Unload(name)
	}
}

func (a *Agent) skillToolInstructionLine(toolName string) string {
	t := a.tools.GetTool(toolName)
	if t == nil || t.Instructions() == "" {
		return ""
	}
	return ToolInstructionLine(toolName, t.Instructions())
}

func (a *Agent) heldInstructions(name string) (LoadedSkillInfo, bool) {
	if held := a.skillManager.GetInstructions([]string{name}); len(held) > 0 {
		return held[0], true
	}
	return LoadedSkillInfo{}, false
}
