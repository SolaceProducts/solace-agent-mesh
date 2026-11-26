// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type entryShape struct{ one, many string }

var (
	modeNameShape     = entryShape{"mode name", "mode names"}
	skillNameShape    = entryShape{"skill name", "skill names"}
	cardSkillShape    = entryShape{"skill descriptor with a name or id", "skill descriptors with a name or id"}
	providerNameShape = entryShape{"provider name", "provider names"}
)

type agentFieldReader struct {
	consumed map[string]struct{}
	warnings []string
	refusals []string
	prefix   string
}

func (r *agentFieldReader) label(key string) string { return r.prefix + key }

func (r *agentFieldReader) readable(container map[string]any, key string, wellFormed func(any) bool, shape string) bool {
	r.consumed[key] = struct{}{}
	raw, stated := container[key]
	switch {
	case !stated:
		return false
	case raw == nil, wellFormed(raw):
		return true
	}
	r.warnings = append(r.warnings, fmt.Sprintf("%s is %s, not %s, and was ignored", r.label(key), describeEntry(raw), shape))
	return false
}

func (r *agentFieldReader) readableList(container map[string]any, key string, goodEntry func(any) bool, shape entryShape, alsoBare bool) bool {
	r.consumed[key] = struct{}{}
	raw, stated := container[key]
	if !stated {
		return false
	}
	if raw == nil {
		return true
	}
	if _, isText := raw.(string); isText && alsoBare {
		if goodEntry(raw) {
			return true
		}
		r.warnings = append(r.warnings, fmt.Sprintf("%s is an empty %s, "+readableListKeptClause, r.label(key), shape.one))
		return false
	}
	entries, isSlice := raw.([]any)
	if !isSlice {
		wanted := "a list of " + shape.many
		if alsoBare {
			wanted = "a " + shape.one + " or a list of them"
		}
		r.warnings = append(r.warnings, fmt.Sprintf("%s is %s, not %s, and was ignored", r.label(key), describeEntry(raw), wanted))
		return false
	}
	var unreadable []string
	readableEntries := 0
	for i, e := range entries {
		if goodEntry(e) {
			readableEntries++
			continue
		}
		unreadable = append(unreadable, fmt.Sprintf("%s[%d] is %s, not a %s, and was ignored", r.label(key), i, describeEntry(e), shape.one))
	}
	if len(entries) > 0 && readableEntries == 0 {
		r.warnings = append(r.warnings, fmt.Sprintf("no entry of %s is a %s, "+readableListKeptClause, r.label(key), shape.one))
		return false
	}
	r.warnings = append(r.warnings, unreadable...)
	return true
}

func desiredAgent(componentName, content string, cat toolsetCatalog) (desiredComponent, error) {
	app, err := parseBuilderApp(content)
	if err != nil {
		return desiredComponent{}, fmt.Errorf("parse agent %q: %w", componentName, err)
	}
	appConfig := app.appConfig
	if modelErr := rejectUnsavedModelKeys(componentName, app); modelErr != nil {
		return desiredComponent{}, modelErr
	}
	if gateErr := rejectUnsavedToolGates(componentName, appConfig); gateErr != nil {
		return desiredComponent{}, gateErr
	}
	if groupHILErr := rejectGroupHILToolsNotInGroup(componentName, appConfig, cat); groupHILErr != nil {
		return desiredComponent{}, groupHILErr
	}

	spec, authored, warnings, err := agentSpecFromAppConfig(componentName, appConfig, cat)
	if err != nil {
		return desiredComponent{}, fmt.Errorf("agent %q: %w", componentName, err)
	}

	stated := extraConfigOf(spec)
	if len(stated) > 0 {
		written := withoutRemovals(stated, extraConfigKeys)
		extraWarnings, checkErr := checkWrittenExtraConfig(componentName, written)
		if checkErr != nil {
			return desiredComponent{}, checkErr
		}
		warnings = append(warnings, extraWarnings...)
		additional := mapField(spec, "additionalConfigurations")
		maps.DeleteFunc(additional, func(k string, _ any) bool { return k != interAgentCommunicationStoredKey })
		maps.Copy(additional, written)
		if len(additional) == 0 {
			delete(spec, "additionalConfigurations")
		}
	}

	create, err := translate.ForwardAgent(spec, translate.AgentHooks{})
	if err != nil {
		return desiredComponent{}, fmt.Errorf("translate agent %q: %w", componentName, err)
	}

	create.Deploy = false

	statedSettings, settingWarnings := statedBuiltinToolSettings(appConfig)
	warnings = append(warnings, settingWarnings...)
	warnings = append(warnings, platformManagedSettingWarnings(appConfig)...)
	builtinTools := builtinToolSettings{stated: statedSettings, catalog: cat}
	var carryWarnings []string
	create.ToolsetConfigs, carryWarnings = carryBuiltinToolSettings(nil, create.Toolsets, create.ToolsetExcludedTools, builtinTools)
	for _, w := range carryWarnings {
		warnings = appendOnce(warnings, w)
	}

	create.SkillIDs = unionStrings(create.SkillRefs, create.SkillIDs)
	builtins := builtinSkillNameSet()
	var builtinRefs []string
	for _, id := range create.SkillIDs {
		if _, ok := builtins[id]; ok {
			builtinRefs = append(builtinRefs, id)
		}
	}
	create.SkillRefs = unionStrings(create.SkillRefs, builtinRefs)

	if offersDeckTool(create.Toolsets, create.ToolsetExcludedTools) {
		create.SkillRefs = unionStrings(create.SkillRefs, []string{deckSkill})
	}

	return desiredComponent{
		planProto: create, create: create, warnings: warnings, authored: authored,
		extraConfig:  stated,
		peerAccess:   peerAccessOf(spec),
		builtinTools: builtinTools,
	}, nil
}

func agentSpecFromAppConfig(componentName string, appConfig map[string]any, cat toolsetCatalog) (map[string]any, map[string]struct{}, []string, error) {
	spec := map[string]any{}
	authored := authoredKeys("name")
	r := &agentFieldReader{consumed: map[string]struct{}{"agent_name": {}, "agent_card": {}}}
	present := func(container map[string]any, keys ...string) bool {
		found := false
		for _, key := range keys {
			r.consumed[key] = struct{}{}
			if _, ok := container[key]; ok {
				found = true
			}
		}
		return found
	}

	isText := func(v any) bool { _, ok := v.(string); return ok }
	isName := func(v any) bool { s, ok := v.(string); return ok && s != "" }
	isSkillRef := func(v any) bool {
		if isName(v) {
			return true
		}
		m, isMap := v.(map[string]any)
		return isMap && stringField(m, "name") != ""
	}
	isCardSkill := func(v any) bool {
		m, isMap := v.(map[string]any)
		return isMap && (stringField(m, "name") != "" || stringField(m, "id") != "")
	}

	name := cmp.Or(stringField(appConfig, "agent_name"), componentName)
	if name == "" {
		return nil, nil, nil, fmt.Errorf("no agent_name and no component name")
	}
	spec["name"] = name

	card := mapField(appConfig, "agent_card")
	if desc := cmp.Or(stringField(card, "description"), stringField(appConfig, "description")); desc != "" {
		spec["description"] = desc
	}
	if r.readable(card, "description", isText, "text") || r.readable(appConfig, "description", isText, "text") {
		authored["description"] = struct{}{}
	}
	if instruction := stringField(appConfig, "instruction"); instruction != "" {
		spec["systemPrompt"] = instruction
	}
	if r.readable(appConfig, "instruction", isText, "text") {
		authored["systemPrompt"] = struct{}{}
	}

	if advertised := advertisedCardSkills(card); len(advertised) > 0 {
		spec["skills"] = advertised
	}
	if r.readableList(card, "skills", isCardSkill, cardSkillShape, false) {
		authored["skills"] = struct{}{}
	}

	if refs := loadableSkillNames(appConfig["skills"]); len(refs) > 0 {
		spec["skillRefs"] = toAnySlice(refs)
	}
	if r.readableList(appConfig, "skills", isSkillRef, skillNameShape, false) {
		authored["skillRefs"] = struct{}{}
		authored["skillIds"] = struct{}{}
	}

	groups, tools, toolsReadable, toolWarnings := builtinToolRefs(appConfig)
	r.warnings = append(r.warnings, toolWarnings...)
	declared, toolsetsReadable, toolsetWarnings := declaredToolsetNames(appConfig)
	r.warnings = append(r.warnings, toolsetWarnings...)
	groups = append(groups, declared...)
	toolsStated := present(appConfig, "tools")
	toolsetsStated := present(appConfig, "toolsets")
	if (toolsStated && toolsReadable) || (toolsetsStated && toolsetsReadable) {
		authored["toolsets"] = struct{}{}
	}
	if toolsStated && toolsReadable {
		authored["toolsetExcludedTools"] = struct{}{}
	}
	if len(groups) > 0 || len(tools) > 0 {
		toolsets, excluded, err := resolveToolsetBindings(groups, tools, cat)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(toolsets) > 0 {
			spec["toolsets"] = toAnySlice(toolsets)
		}
		if len(excluded) > 0 {
			out := make(map[string]any, len(excluded))
			for id, drop := range excluded {
				out[id] = toAnySlice(drop)
			}
			spec["toolsetExcludedTools"] = out
		}
	}

	if in := firstNonEmpty(stringSliceField(card, "defaultInputModes"), stringSliceField(appConfig, "input_modes")); len(in) > 0 {
		spec["inputModes"] = toAnySlice(sortedCopy(in))
	}
	onCard, onApp := r.readableList(card, "defaultInputModes", isName, modeNameShape, false), r.readableList(appConfig, "input_modes", isName, modeNameShape, false)
	if onCard || onApp {
		authored["inputModes"] = struct{}{}
	}
	if out := firstNonEmpty(stringSliceField(card, "defaultOutputModes"), stringSliceField(appConfig, "output_modes")); len(out) > 0 {
		spec["outputModes"] = toAnySlice(sortedCopy(out))
	}
	onCard, onApp = r.readableList(card, "defaultOutputModes", isName, modeNameShape, false), r.readableList(appConfig, "output_modes", isName, modeNameShape, false)
	if onCard || onApp {
		authored["outputModes"] = struct{}{}
	}

	if providers := modelProviders(appConfig["model_provider"]); len(providers) > 0 {
		spec["modelProvider"] = toAnySlice(providers)
	}
	if r.readableList(appConfig, "model_provider", isName, providerNameShape, true) {
		authored["modelProvider"] = struct{}{}
	}

	r.readPeerAccess(appConfig, spec)
	r.readExtraConfig(appConfig, spec)
	if len(r.refusals) > 0 {
		return nil, nil, nil, &entplatform.ValidationError{Message: strings.Join(r.refusals, "; "), Details: map[string][]string{"additionalConfigurations": r.refusals}}
	}

	r.warnings = append(r.warnings, droppedAgentKeyWarnings(appConfig, r.consumed)...)
	return spec, authored, r.warnings, nil
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func rejectUnsavedModelKeys(componentName string, app builderApp) error {
	problems := schema.AgentModelKeyErrors(app.envelope, app.appConfig, 0)
	if len(problems) == 0 {
		return nil
	}
	messages := make([]string, 0, len(problems))
	for _, p := range problems {
		messages = append(messages, p.Path+": "+p.Message)
	}
	return &entplatform.ValidationError{
		Message: fmt.Sprintf("agent %q: %s", componentName, strings.Join(messages, "; ")),
		Details: map[string][]string{"agent " + componentName: messages},
	}
}

const agentModelKey = schema.AgentModelKey

var agentKeysManagedElsewhere = []string{
	"agent_card_publishing", "agent_discovery", "app_base_path", "app_exec",
	"app_module", "artifact_service", "authorization_service", "broker",
	"component_type", "data_tools_config", "display_name",
	"extract_content_from_artifact_config", "log", "name", "namespace",
	"observability", "session_service", "trust_manager",
	"skills_base_path", "skills_cache_path", "agent_type", "internal",
	"required_scopes", "volumes", "networks",
}

var agentKeysNotedWhenStated = []string{"volumes", "networks"}

func platformManagedSettingWarnings(appConfig map[string]any) []string {
	var stated []string
	for _, key := range agentKeysNotedWhenStated {
		if _, present := appConfig[key]; present {
			stated = append(stated, key)
		}
	}
	entries, _ := appConfig["tools"].([]any)
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		config, _ := entry[toolConfigSetting].(map[string]any)
		if _, has := config["allow_loopback"]; isBuiltinEntry(entry) && has {
			stated = append(stated, "tool_config.allow_loopback")
			break
		}
	}
	if len(stated) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%s %s set with `sam config apply`, not by the Builder, so %s not saved and any stored value was kept",
		strings.Join(stated, ", "), pluralVerb(len(stated), "is", "are"), pluralVerb(len(stated), "it was", "they were"))}
}

func pluralVerb(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func droppedAgentKeyWarnings(appConfig map[string]any, consumed map[string]struct{}) []string {
	var out []string
	if _, present := appConfig[agentModelKey]; present {
		out = append(out, droppedAgentKeyWarning(agentModelKey, "was not saved: the agent's model comes from its model_provider binding"))
	}
	for _, key := range slices.Sorted(maps.Keys(appConfig)) {
		_, wasRead := consumed[key]
		if wasRead ||
			slices.Contains(agentKeysManagedElsewhere, key) ||
			key == agentModelKey {
			continue
		}
		out = append(out, droppedAgentKeyWarning(key, "was not recognised and was not saved"))
	}
	return out
}

func droppedAgentKeyWarning(key, outcome string) string {
	return "agent config key " + key + " " + outcome
}

func builtinToolRefs(appConfig map[string]any) (groups, tools []string, readable bool, warnings []string) {
	readable = true
	switch v := appConfig["tools"].(type) {
	case nil, []any:
	default:
		readable = false
		warnings = append(warnings, fmt.Sprintf("tools is a %T, not a list of tool entries, and was ignored", v))
	}

	raw, _ := appConfig["tools"].([]any)
	unreadableEntries := 0
	ignored := func(format string, args ...any) {
		unreadableEntries++
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	for i, e := range raw {
		entry, isMapping := e.(map[string]any)
		if !isMapping {
			ignored("tools[%d] is %s, not a tool entry (a mapping with tool_type), and was ignored", i, describeEntry(e))
			continue
		}
		toolType := stringField(entry, "tool_type")
		switch toolType {
		case "builtin-group":
			if g := stringField(entry, "group_name"); g != "" {
				groups = append(groups, g)
				warnings = append(warnings, unstoredEntryKeyWarnings(i, entry)...)
				continue
			}
			ignored("tools[%d] is a builtin-group with no group_name and was ignored", i)
		case "builtin":
			if n := stringField(entry, "tool_name"); n != "" {
				tools = append(tools, n)
				warnings = append(warnings, unstoredEntryKeyWarnings(i, entry)...)
				continue
			}
			ignored("tools[%d] is a builtin with no tool_name and was ignored", i)
		case "mcp", "python", "openapi":
		default:
			ignored("tools[%d] has tool_type %q, which is not one this endpoint reads, and was ignored", i, toolType)
		}
	}
	if len(raw) > 0 && unreadableEntries == len(raw) {
		readable = false
	}
	return dedupe(groups), dedupe(tools), readable, warnings
}

func describeEntry(entry any) string {
	switch v := entry.(type) {
	case nil:
		return "empty"
	case string:
		if v == "" {
			return "empty"
		}
		return "text"
	case map[string]any, map[any]any:
		return "a mapping"
	case []any:
		return "a list"
	case bool:
		return "a boolean"
	case int, int64, float64:
		return "a number"
	default:
		return fmt.Sprintf("%T", entry)
	}
}

func loadableSkillNames(raw any) []string {
	entries, ok := raw.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range entries {
		switch v := e.(type) {
		case string:
			if v != "" {
				out = append(out, v)
			}
		case map[string]any:
			if n := stringField(v, "name"); n != "" {
				out = append(out, n)
			}
		}
	}
	return dedupe(out)
}

func advertisedCardSkills(card map[string]any) []any {
	cardSkills := mapSliceField(card, "skills")
	advertised := make([]any, 0, len(cardSkills))
	for _, s := range cardSkills {
		entry := maps.Clone(s)
		if stringField(entry, "name") == "" {
			entry["name"] = stringField(s, "id")
		}
		if entry["name"] == "" {
			continue
		}
		advertised = append(advertised, entry)
	}
	return advertised
}

func declaredToolsetNames(appConfig map[string]any) (declared []string, readable bool, warnings []string) {
	declared, readable = stringSliceField(appConfig, "toolsets"), true
	switch v := appConfig["toolsets"].(type) {
	case nil:
	case []any:
		for i, entry := range v {
			if name, isString := entry.(string); !isString || name == "" {
				warnings = append(warnings, fmt.Sprintf("toolsets[%d] is %s, not a toolset name, and was ignored", i, describeEntry(entry)))
			}
		}
		readable = len(v) == 0 || len(declared) > 0
	default:
		readable = false
		warnings = append(warnings, fmt.Sprintf("toolsets is a %T, not a list of toolset names, and was ignored", v))
	}
	return declared, readable, warnings
}

func modelProviders(raw any) []string {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

const (
	deckSkill   = "sam-pptx"
	deckToolset = "builtin_document_tools"
	deckTool    = "pptx_create_presentation"
)

func offersDeckTool(toolsets []string, excluded map[string][]string) bool {
	return slices.Contains(toolsets, deckToolset) && !slices.Contains(excluded[deckToolset], deckTool)
}

func builtinSkillNameSet() map[string]struct{} {
	names := builtinskills.Names()
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}

func unionStrings(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	return dedupe(slices.Concat(a, b))
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func firstNonEmpty(candidates ...[]string) []string {
	for _, c := range candidates {
		if len(c) > 0 {
			return c
		}
	}
	return nil
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
