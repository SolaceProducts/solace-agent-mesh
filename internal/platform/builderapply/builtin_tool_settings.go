// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const (
	toolConfigSetting = "tool_config"
	hilSetting        = "hil"
)

type toolSetting struct {
	tool, kind, group string
	index             int
	value             any
	noDefault         bool
	memberOverrides   map[string]any
}

type builtinToolSettings struct {
	stated  []toolSetting
	catalog toolsetCatalog
}

const hilToolsKey = "tools"

func statedBuiltinToolSettings(appConfig map[string]any) (stated []toolSetting, warnings []string) {
	entries, _ := appConfig["tools"].([]any)
	for i, e := range entries {
		entry, _ := e.(map[string]any)
		if !isBuiltinEntry(entry) {
			continue
		}
		tool, group := stringField(entry, "tool_name"), stringField(entry, "group_name")
		switch stringField(entry, "tool_type") {
		case "builtin":
			group = ""
		case "builtin-group":
			tool = ""
		}
		if tool == "" && group == "" {
			continue
		}
		for _, kind := range []string{toolConfigSetting, hilSetting} {
			raw, present := entry[kind]
			if !present {
				continue
			}
			if group != "" && kind == toolConfigSetting {
				warnings = append(warnings, fmt.Sprintf("tools[%d].tool_config on a builtin-group was not saved; set tool_config on a builtin entry for each tool that needs it", i))
				continue
			}
			block, isMap := raw.(map[string]any)
			if raw != nil && !isMap {
				warnings = append(warnings, fmt.Sprintf("tools[%d].%s is %s, not a mapping, and was ignored", i, kind, describeEntry(raw)))
				continue
			}
			setting := toolSetting{tool: tool, kind: kind, group: group, index: i, value: raw}
			if kind == hilSetting && isMap {
				setting = withGroupHILSplit(setting, block)
			}
			stated = append(stated, setting)
		}
	}
	return stated, warnings
}

func withGroupHILSplit(setting toolSetting, block map[string]any) toolSetting {
	if len(block) == 0 {
		setting.value = nil
		return setting
	}
	if setting.group == "" {
		return setting
	}
	overrides, hasTools := block[hilToolsKey].(map[string]any)
	if !hasTools {
		return setting
	}
	defaults := maps.Clone(block)
	delete(defaults, hilToolsKey)
	setting.memberOverrides = overrides
	setting.noDefault = len(defaults) == 0
	setting.value = defaults
	return setting
}

func hilGates(value any) bool {
	block, isBlock := value.(map[string]any)
	if !isBlock {
		return false
	}
	if required, _ := block["require_approval"].(bool); required {
		return true
	}
	rules, _ := block["require_approval_when"].([]any)
	return len(rules) > 0
}

func mergeHIL(defaults, override any) any {
	if override == nil {
		return nil
	}
	base, _ := defaults.(map[string]any)
	if len(base) == 0 {
		return override
	}
	merged := maps.Clone(base)
	overrideBlock, _ := override.(map[string]any)
	maps.Copy(merged, overrideBlock)
	return merged
}

var gatingHILFields = []string{"require_approval", "require_approval_when"}

func layOverGate(gate, override any) (merged any, held bool) {
	merged = mergeHIL(gate, override)
	rank := hilRank(gate)
	if rank < 2 || hilRank(merged) >= rank {
		return merged, false
	}
	base, _ := gate.(map[string]any)
	out := maps.Clone(base)
	if block, isBlock := merged.(map[string]any); isBlock {
		out = block
	}
	if rank == 3 {
		out["require_approval"] = true
		return out, true
	}
	for _, field := range gatingHILFields {
		if value, has := base[field]; has {
			out[field] = value
		} else {
			delete(out, field)
		}
	}
	return out, true
}

type hilCandidate struct {
	group, name string
	index       int
	value       any
}

func hilRank(value any) int {
	block, _ := value.(map[string]any)
	switch {
	case value == nil:
		return 0
	case block["require_approval"] == true:
		return 3
	case hilGates(value):
		return 2
	}
	return 1
}

func hilKeptBecause(kept, other hilCandidate) string {
	switch {
	case hilRank(kept.value) == 3 && hilRank(other.value) < 3:
		return "it requires approval on every call"
	case hilRank(kept.value) == 2 && hilRank(other.value) < 2:
		return "it requires approval when its rules match and the other never does"
	case hilRank(kept.value) > hilRank(other.value):
		return "a null never overrides a block"
	}
	return "neither outranks the other, so the first by group name wins"
}

func resolveStatedSettings(stated []toolSetting, excluded map[string][]string, cat toolsetCatalog) ([]toolSetting, []string) {
	type slot struct{ tool, kind string }
	candidates := map[slot][]hilCandidate{}
	var warnings []string
	for _, s := range stated {
		if s.group == "" {
			continue
		}
		id := schema.CanonicalBuiltinGroup(s.group)
		reached := false
		for _, member := range cat.toolNamesByToolsetID[id] {
			if slices.Contains(excluded[id], member) {
				continue
			}
			reached = true
			value, stating := s.value, !s.noDefault
			if override, overridden := s.memberOverrides[member]; overridden {
				var held bool
				value, held = layOverGate(s.value, override)
				stating = true
				if held {
					warnings = appendOnce(warnings, fmt.Sprintf("tools[%d] (%s): %s is still gated by the group's hil; hil.tools.%s cannot turn that gate off or make an always-on gate conditional, so remove the group's hil to change that", s.index, s.group, member, member))
				}
			}
			if stating {
				candidates[slot{member, s.kind}] = append(candidates[slot{member, s.kind}], hilCandidate{group: id, name: s.group, index: s.index, value: value})
			}
		}
		if !reached && (s.value != nil || len(s.memberOverrides) > 0) {
			warnings = appendOnce(warnings, fmt.Sprintf("tools[%d] (%s): %s on this built-in group was not saved: it has no tools whose approval can be stored", s.index, s.group, s.kind))
		}
	}

	byTool := map[slot]toolSetting{}
	chosen := map[slot]hilCandidate{}
	type disagreement struct{ kept, other hilCandidate }
	disagreements := map[[2]int][]string{}
	detail := map[[2]int]disagreement{}
	for sl, cands := range candidates {
		best := slices.MinFunc(cands, func(a, b hilCandidate) int {
			return cmp.Or(cmp.Compare(hilRank(b.value), hilRank(a.value)), cmp.Compare(a.group, b.group), cmp.Compare(a.index, b.index))
		})
		chosen[sl] = best
		byTool[sl] = toolSetting{tool: sl.tool, kind: sl.kind, value: best.value}
		for _, other := range cands {
			if other.index != best.index && !reflect.DeepEqual(other.value, best.value) {
				key := [2]int{best.index, other.index}
				disagreements[key] = append(disagreements[key], sl.tool)
				detail[key] = disagreement{kept: best, other: other}
			}
		}
	}
	for _, key := range slices.SortedFunc(maps.Keys(disagreements), func(a, b [2]int) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	}) {
		d := detail[key]
		warnings = append(warnings, fmt.Sprintf("tools[%d] (%s) and tools[%d] (%s) state different hil for %s; kept the block from tools[%d] (%s) because %s",
			d.kept.index, d.kept.name, d.other.index, d.other.name, strings.Join(slices.Sorted(slices.Values(disagreements[key])), ", "), d.kept.index, d.kept.name, hilKeptBecause(d.kept, d.other)))
	}

	own := map[slot]toolSetting{}
	for _, s := range stated {
		if s.group == "" {
			own[slot{s.tool, s.kind}] = s
		}
	}
	for _, sl := range slices.SortedFunc(maps.Keys(own), func(a, b slot) int {
		return cmp.Or(cmp.Compare(a.tool, b.tool), cmp.Compare(a.kind, b.kind))
	}) {
		s := own[sl]
		if sl.kind == hilSetting {
			if group, grouped := chosen[sl]; grouped && hilGates(group.value) {
				merged, held := layOverGate(group.value, s.value)
				if held {
					warnings = appendOnce(warnings, fmt.Sprintf("tools[%d] (%s): the tool is still gated by the hil on tools[%d] (%s); a tool's own hil cannot turn that gate off or make an always-on gate conditional, so remove the group's hil to change that", s.index, s.tool, group.index, group.name))
				}
				s.value = merged
			} else if grouped {
				s.value = mergeHIL(group.value, s.value)
			}
		}
		byTool[sl] = s
	}
	resolved := slices.SortedFunc(maps.Values(byTool), func(a, b toolSetting) int {
		return cmp.Or(cmp.Compare(a.tool, b.tool), cmp.Compare(a.kind, b.kind))
	})
	return resolved, warnings
}

func carryBuiltinToolSettings(stored []entplatform.ToolsetConfigBinding, toolsets []string, excluded map[string][]string, s builtinToolSettings) ([]entplatform.ToolsetConfigBinding, []string) {
	owner := toolOwners(toolsets, excluded, s.catalog)
	c := &toolsetConfigCarry{out: slices.Clone(stored), touched: map[string]bool{}}
	warnings := c.relocate(owner, func(id string) bool { return s.catalog.toolNamesByToolsetID[id] != nil })
	resolved, groupWarnings := resolveStatedSettings(s.stated, excluded, s.catalog)
	for _, w := range groupWarnings {
		warnings = appendOnce(warnings, w)
	}
	for _, st := range resolved {
		to, owned := owner[st.tool]
		if !owned {
			if st.value == nil {
				continue
			}
			warnings = appendOnce(warnings, fmt.Sprintf("%s for built-in tool %s was not saved: no toolset this agent binds provides it", st.kind, st.tool))
			continue
		}
		if st.kind == toolConfigSetting {
			held, _ := c.held(to, st.tool, toolConfigSetting)
			kept, ok := keepManagedToolConfig(held, st.value)
			if !ok {
				continue
			}
			if st.value == nil && kept != nil {
				warnings = appendOnce(warnings, fmt.Sprintf("tool_config for built-in tool %s was removed except allow_loopback, which is set with `sam config apply`, not by the Builder", st.tool))
			}
			st.value = kept
		}
		c.set(to, st.tool, st.kind, st.value)
	}
	result := c.result()
	warnings = append(warnings, removedGateWarnings(stored, result, owner)...)
	return result, warnings
}

var managedToolConfigKeys = []string{"allow_loopback"}

func keepManagedToolConfig(held, stated any) (any, bool) {
	block, isBlock := stated.(map[string]any)
	if stated != nil && !isBlock {
		return stated, true
	}
	own := maps.Clone(block)
	if own == nil {
		own = map[string]any{}
	}
	maps.DeleteFunc(own, func(key string, _ any) bool { return slices.Contains(managedToolConfigKeys, key) })
	if len(block) > 0 && len(own) == 0 {
		return nil, false
	}
	storedBlock, _ := held.(map[string]any)
	for _, key := range managedToolConfigKeys {
		if value, has := storedBlock[key]; has {
			own[key] = value
		}
	}
	if len(own) == 0 && !isBlock {
		return nil, true
	}
	return own, true
}

func removedGateWarnings(before, after []entplatform.ToolsetConfigBinding, owner map[string]string) []string {
	var warnings []string
	now := strongestGates(after)
	for _, st := range gatesIn(before) {
		if _, owned := owner[st.tool]; !owned {
			continue
		}
		switch rank := now[st.tool]; {
		case rank == 0:
			warnings = appendOnce(warnings, fmt.Sprintf("the approval gate for built-in tool %s (toolset %s) was removed: the hil this build states no longer requires approval, so the tool now runs without asking", st.tool, st.toolset))
		case st.rank == 3 && rank == 2:
			warnings = appendOnce(warnings, fmt.Sprintf("the approval gate for built-in tool %s (toolset %s) was weakened: it asked on every call and now asks only when its rules match", st.tool, st.toolset))
		}
	}
	return warnings
}

type gatedTool struct {
	tool, toolset string
	rank          int
}

func gatesIn(bindings []entplatform.ToolsetConfigBinding) []gatedTool {
	var gated []gatedTool
	for _, b := range bindings {
		gates, _ := b.ConfigValues[hilSetting].(map[string]any)
		for _, tool := range slices.Sorted(maps.Keys(gates)) {
			if hilGates(gates[tool]) {
				gated = append(gated, gatedTool{tool: tool, toolset: b.ToolsetName, rank: hilRank(gates[tool])})
			}
		}
	}
	return gated
}

func strongestGates(bindings []entplatform.ToolsetConfigBinding) map[string]int {
	out := map[string]int{}
	for _, g := range gatesIn(bindings) {
		out[g.tool] = max(out[g.tool], g.rank)
	}
	return out
}

type toolsetConfigCarry struct {
	out     []entplatform.ToolsetConfigBinding
	touched map[string]bool
}

func (c *toolsetConfigCarry) values(toolset string, create bool) map[string]any {
	i := slices.IndexFunc(c.out, func(b entplatform.ToolsetConfigBinding) bool { return b.ToolsetName == toolset })
	if i < 0 && !create {
		return nil
	}
	if i < 0 {
		c.out = append(c.out, entplatform.ToolsetConfigBinding{ToolsetName: toolset})
		i = len(c.out) - 1
	}
	if !c.touched[toolset] {
		vals := maps.Clone(c.out[i].ConfigValues)
		if vals == nil {
			vals = map[string]any{}
		}
		if gates, ok := vals[hilSetting].(map[string]any); ok {
			vals[hilSetting] = maps.Clone(gates)
		}
		c.out[i].ConfigValues, c.touched[toolset] = vals, true
	}
	return c.out[i].ConfigValues
}

func (c *toolsetConfigCarry) set(toolset, tool, kind string, value any) {
	vals := c.values(toolset, value != nil)
	if vals == nil {
		return
	}
	if kind == toolConfigSetting {
		if value == nil {
			delete(vals, tool)
		} else {
			vals[tool] = value
		}
		return
	}
	gates, _ := vals[hilSetting].(map[string]any)
	if value != nil {
		if gates == nil {
			gates = map[string]any{}
		}
		gates[tool] = value
		vals[hilSetting] = gates
		return
	}
	delete(gates, tool)
	if len(gates) == 0 {
		delete(vals, hilSetting)
	}
}

func (c *toolsetConfigCarry) relocate(owner map[string]string, isBuiltin func(string) bool) []string {
	type orphan struct {
		toolSetting
		from string
	}
	var orphans []orphan
	for _, b := range slices.Clone(c.out) {
		if !isBuiltin(b.ToolsetName) {
			continue
		}
		for _, st := range storedToolSettings(b.ConfigValues) {
			if owner[st.tool] != b.ToolsetName {
				c.set(b.ToolsetName, st.tool, st.kind, nil)
				orphans = append(orphans, orphan{toolSetting: st, from: b.ToolsetName})
			}
		}
	}
	var warnings []string
	for _, o := range orphans {
		to, owned := owner[o.tool]
		if !owned {
			warnings = appendOnce(warnings, fmt.Sprintf("dropped the stored %s for built-in tool %s: no toolset this agent binds provides it any more", o.kind, o.tool))
			continue
		}
		if kept, held := c.held(to, o.tool, o.kind); held {
			switch {
			case reflect.DeepEqual(kept, o.value):
			case o.kind == hilSetting && !hilGates(kept) && hilGates(o.value):
				c.set(to, o.tool, o.kind, o.value)
				warnings = appendOnce(warnings, fmt.Sprintf("kept the hil that requires approval for built-in tool %s, stored under %s, in place of the weaker one stored under %s, so the gate stays", o.tool, o.from, to))
			default:
				warnings = appendOnce(warnings, fmt.Sprintf("kept the %s for built-in tool %s from %s and dropped the different one stored under %s", o.kind, o.tool, to, o.from))
			}
			continue
		}
		c.set(to, o.tool, o.kind, o.value)
	}
	return warnings
}

func appendOnce(warnings []string, w string) []string {
	if slices.Contains(warnings, w) {
		return warnings
	}
	return append(warnings, w)
}

func (c *toolsetConfigCarry) held(toolset, tool, kind string) (any, bool) {
	i := slices.IndexFunc(c.out, func(b entplatform.ToolsetConfigBinding) bool { return b.ToolsetName == toolset })
	if i < 0 {
		return nil, false
	}
	for _, st := range storedToolSettings(c.out[i].ConfigValues) {
		if st.tool == tool && st.kind == kind {
			return st.value, true
		}
	}
	return nil, false
}

func storedToolSettings(configValues map[string]any) []toolSetting {
	var settings []toolSetting
	for key, value := range configValues {
		if key != hilSetting {
			settings = append(settings, toolSetting{tool: key, kind: toolConfigSetting, value: value})
		}
	}
	gates, _ := configValues[hilSetting].(map[string]any)
	for tool, value := range gates {
		settings = append(settings, toolSetting{tool: tool, kind: hilSetting, value: value})
	}
	slices.SortFunc(settings, func(a, b toolSetting) int {
		return cmp.Or(cmp.Compare(a.tool, b.tool), cmp.Compare(a.kind, b.kind))
	})
	return settings
}

func (c *toolsetConfigCarry) result() []entplatform.ToolsetConfigBinding {
	if len(c.out) == 0 {
		return nil
	}
	out := make([]entplatform.ToolsetConfigBinding, 0, len(c.out))
	for _, b := range c.out {
		if c.touched[b.ToolsetName] && len(b.ConfigValues) == 0 {
			if len(b.VolumeBindings) == 0 && len(b.NetworkBindings) == 0 {
				continue
			}
			b.ConfigValues = nil
		}
		out = append(out, b)
	}
	return out
}

func rejectGroupHILToolsNotInGroup(componentName string, appConfig map[string]any, cat toolsetCatalog) error {
	entries, _ := appConfig["tools"].([]any)
	details := map[string][]string{}
	var messages []string
	refuse := func(path, message string) {
		details[path] = append(details[path], message)
		messages = append(messages, path+": "+message)
	}
	for i, e := range entries {
		entry, _ := e.(map[string]any)
		hil, _ := entry[hilSetting].(map[string]any)
		if stringField(entry, "tool_type") != "builtin-group" || hil == nil {
			continue
		}
		raw, present := hil[hilToolsKey]
		if !present {
			continue
		}
		path := fmt.Sprintf("tools[%d].hil.tools", i)
		overrides, isMap := raw.(map[string]any)
		if !isMap {
			refuse(path, "must be a mapping of tool name to hil block, not "+describeEntry(raw))
			continue
		}
		members := cat.toolNamesByToolsetID[schema.CanonicalBuiltinGroup(stringField(entry, "group_name"))]
		for _, name := range slices.Sorted(maps.Keys(overrides)) {
			if _, isBlock := overrides[name].(map[string]any); !isBlock {
				refuse(path+"."+name, "must be a hil block, not "+describeEntry(overrides[name])+"; an override adds to the group's gate and cannot remove it")
			} else if members != nil && !slices.Contains(members, name) {
				refuse(path+"."+name, fmt.Sprintf("%s is not a tool in built-in group %s; hil for it would gate nothing", name, stringField(entry, "group_name")))
			}
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return &entplatform.ValidationError{
		Message: fmt.Sprintf("agent %q: %s", componentName, strings.Join(messages, "; ")),
		Details: details,
	}
}
