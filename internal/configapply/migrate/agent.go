// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"fmt"
	"sort"
	"strings"
)

var agentDroppedKeys = map[string]struct{}{
	"agent_name":   {},
	"display_name": {},
	"namespace":    {},

	"supports_streaming":                   {},
	"inject_user_profile":                  {},
	"artifact_handling_mode":               {},
	"enable_embed_resolution":              {},
	"enable_artifact_content_instruction":  {},
	"inter_agent_communication":            {},
	"data_tools_config":                    {},
	"extract_content_from_artifact_config": {},

	"agent_card": {},
}

func MigrateAgent(doc map[string]any) (CleanSpec, []string, error) {
	ac, err := extractAppConfig(doc)
	if err != nil {
		return CleanSpec{}, nil, err
	}

	var warnings []string
	out := CleanSpec{Kind: "agent", Spec: map[string]any{}}

	displayName, _ := ac["display_name"].(string)
	agentName, _ := ac["agent_name"].(string)
	switch {
	case displayName != "" && displayName != agentName:
		out.Name = displayName
	case agentName != "":
		out.Name = agentName
	case displayName != "":
		out.Name = displayName
	default:
		return CleanSpec{}, nil, fmt.Errorf("agent has no agent_name and no display_name")
	}

	if card, ok := ac["agent_card"].(map[string]any); ok {
		if d, ok := card["description"].(string); ok && d != "" {
			out.Description = d
		}
		if rawSkills, ok := card["skills"].([]any); ok && len(rawSkills) > 0 {
			cleanSkills, w := migrateAgentSkills(rawSkills)
			if len(cleanSkills) > 0 {
				out.Spec["skills"] = cleanSkills
			}
			warnings = append(warnings, w...)
		}
		if modes, ok := asStringList(card["defaultInputModes"]); ok && len(modes) > 0 {
			out.Spec["inputModes"] = modes
		}
		if modes, ok := asStringList(card["defaultOutputModes"]); ok && len(modes) > 0 {
			out.Spec["outputModes"] = modes
		}
	}
	if out.Description == "" {
		if v, ok := ac["display_name"].(string); ok && v != "" {
			out.Description = v
		} else {
			out.Description = out.Name
			warnings = append(warnings, "no description in agent_card; using name as a placeholder — please rewrite")
		}
	}

	if v, ok := ac["instruction"].(string); ok && v != "" {
		out.Spec["systemPrompt"] = v
	}

	if v, ok := ac["model_provider"]; ok {
		if list, ok := asStringList(v); ok && len(list) > 0 {
			out.Spec["modelProvider"] = list
		} else {
			warnings = append(warnings, "model_provider has unexpected shape; dropped")
		}
	}

	if rawTools, ok := ac["tools"].([]any); ok && len(rawTools) > 0 {
		toolsets, toolWarnings := migrateAgentTools(rawTools)
		if len(toolsets) > 0 {
			out.Spec["toolsets"] = toolsets
		}
		warnings = append(warnings, toolWarnings...)
	}

	leftover := map[string]any{}
	for k, v := range dropCruft(ac) {
		if _, drop := agentDroppedKeys[k]; drop {
			continue
		}
		switch k {
		case "instruction", "model_provider", "tools", "model":
			continue
		}
		leftover[k] = v
	}
	if len(leftover) > 0 {
		keys := make([]string, 0, len(leftover))
		for k := range leftover {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		warnings = append(warnings, fmt.Sprintf(
			"unmapped legacy fields preserved under spec.legacy: %s — review and either translate to clean-spec equivalents or delete",
			strings.Join(keys, ", "),
		))
		out.Spec["legacy"] = leftover
	}

	return out, warnings, nil
}

func migrateAgentSkills(raw []any) ([]any, []string) {
	var warnings []string
	out := make([]any, 0, len(raw))
	for i, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("agent_card.skills[%d] is not a mapping; skipped", i))
			continue
		}
		clean := map[string]any{}
		if name, ok := m["name"].(string); ok && name != "" {
			clean["name"] = name
		} else if id, ok := m["id"].(string); ok && id != "" {
			clean["name"] = id
		} else {
			warnings = append(warnings, fmt.Sprintf("agent_card.skills[%d] has neither name nor id; skipped", i))
			continue
		}
		if desc, ok := m["description"].(string); ok {
			clean["description"] = desc
		}
		for _, k := range []string{"required", "optional"} {
			if v, ok := m[k]; ok {
				clean[k] = v
			}
		}
		out = append(out, clean)
	}
	return out, warnings
}

func migrateAgentTools(raw []any) ([]string, []string) {
	var warnings []string
	seen := map[string]struct{}{}
	miscNeeded := false

	for i, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("tools[%d] is not a mapping; dropped", i))
			continue
		}
		toolType, _ := m["tool_type"].(string)
		groupName, _ := m["group_name"].(string)

		switch {
		case toolType == "builtin-group" && groupName != "":
			seen[groupName] = struct{}{}
		case groupName != "":
			seen[groupName] = struct{}{}
			warnings = append(warnings, fmt.Sprintf(
				"tools[%d]: tool_type=%q is non-standard; mapped to toolsets[%q] anyway — verify the toolset name",
				i, toolType, groupName,
			))
		default:
			miscNeeded = true
			label := toolType
			if label == "" {
				label = "<unspecified>"
			}
			warnings = append(warnings, fmt.Sprintf(
				"tools[%d]: tool_type=%q has no group_name; bucketed under toolsets[\"misc\"] — define a real toolset for this",
				i, label,
			))
		}
	}
	if miscNeeded {
		seen["misc"] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, warnings
}
