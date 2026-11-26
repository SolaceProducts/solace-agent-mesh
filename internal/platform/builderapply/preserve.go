// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func overlayUnauthored(create, stored any, authored map[string]struct{}) any {
	src := storedFieldsByJSONTag(stored)
	if len(src) == 0 {
		return create
	}

	dst := reflect.New(reflect.TypeOf(create)).Elem()
	dst.Set(reflect.ValueOf(create))

	t := dst.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := jsonFieldName(f.Tag.Get("json"))
		if tag == "" || tag == "-" || neverOverlaid[tag] {
			continue
		}
		if _, wasAuthored := authored[tag]; wasAuthored {
			continue
		}
		value, ok := src[tag]
		if !ok {
			continue
		}
		assignAcrossTypes(dst.Field(i), value)
	}
	return dst.Interface()
}

var neverOverlaid = map[string]bool{
	"deploy":            true,
	"id":                true,
	"agentAuthType":     true,
	"agentCardAuthType": true,
}

func storedFieldsByJSONTag(stored any) map[string]reflect.Value {
	v := reflect.ValueOf(stored)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	out := map[string]reflect.Value{}
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if tag := jsonFieldName(f.Tag.Get("json")); tag != "" && tag != "-" {
			out[tag] = v.Field(i)
		}
	}
	return out
}

func assignAcrossTypes(dst, src reflect.Value) {
	if !dst.CanSet() {
		return
	}
	if src.Type() == dst.Type() {
		dst.Set(src)
		return
	}
	if src.Type().AssignableTo(dst.Type()) {
		dst.Set(src)
		return
	}
	raw, err := json.Marshal(src.Interface())
	if err != nil {
		return
	}
	converted := reflect.New(dst.Type())
	if err := json.Unmarshal(raw, converted.Interface()); err != nil {
		return
	}
	dst.Set(converted.Elem())
}

func preserveUnauthoredFields(componentKind string, d *desiredComponent, stored storedResource) error {
	preserve, known := map[string]func(*desiredComponent, storedResource) error{
		kindAgent:     preserveAgentFields,
		kindA2AAgent:  preserveA2AAgentFields,
		kindWorkflow:  preserveWorkflowFields,
		kindGateway:   preserveGatewayFields,
		kindConnector: preserveConnectorFields,
	}[componentKind]
	if !known {
		return nil
	}
	return preserve(d, stored)
}

func preserveAgentFields(d *desiredComponent, stored storedResource) error {
	create := d.create.(entplatform.CreateAgentRequest)
	actual := stored.raw.(entplatform.AgentResponse)
	merged, _ := overlayUnauthored(create, actual, d.authored).(entplatform.CreateAgentRequest)
	merged.SkillConfigs = skillConfigsForBoundSkills(merged.SkillConfigs, merged.SkillIDs)
	carried, carryWarnings := carryBuiltinToolSettings(merged.ToolsetConfigs, merged.Toolsets, merged.ToolsetExcludedTools, d.builtinTools)
	for _, w := range carryWarnings {
		d.warnings = appendOnce(d.warnings, w)
	}
	merged.ToolsetConfigs = toolsetConfigsForBoundToolsets(carried, merged.Toolsets)
	if _, authoredConnectors := d.authored["connectors"]; authoredConnectors {
		if dropped := droppedNames(actual.Connectors, merged.Connectors); len(dropped) > 0 {
			d.warnings = append(d.warnings, fmt.Sprintf("detached %d connector(s) this build did not list (ids: %s); the connectors and their credentials stay on the connectors page, but this agent can no longer reach them",
				len(dropped), strings.Join(dropped, ", ")))
		}
	}
	if _, authoredToolsets := d.authored["toolsets"]; authoredToolsets {
		if dropped := droppedNames(actual.Toolsets, merged.Toolsets); len(dropped) > 0 {
			d.warnings = append(d.warnings, fmt.Sprintf("unbound %d toolset(s) this build did not list (%s); this agent can no longer use their tools",
				len(dropped), strings.Join(dropped, ", ")))
		}
	}
	d.skillSetUnchanged = sameStringSet(merged.SkillIDs, actual.SkillIDs)
	if _, authored := d.authored["skillIds"]; authored && !d.skillSetUnchanged {
		d.skillSetChanged = true
	}
	return d.carryAdditionalConfigurations(merged)
}

func preserveA2AAgentFields(d *desiredComponent, stored storedResource) error {
	create, ok := d.create.(entplatform.CreateRemoteAgentRequest)
	if !ok {
		return nil
	}
	actual, ok := stored.raw.(entplatform.RemoteAgentResponse)
	if !ok {
		return nil
	}
	merged, _ := overlayUnauthored(create, actual, d.authored).(entplatform.CreateRemoteAgentRequest)
	for _, f := range []struct {
		name    string
		desired *string
		stored  string
	}{
		{"description", &merged.Description, actual.Description},
		{"agentCardUrl", &merged.AgentCardURL, actual.AgentCardURL},
		{"agentUrl", &merged.AgentURL, actual.AgentURL},
		{"agentCardLocation", &merged.AgentCardLocation, actual.AgentCardLocation},
		{"name", &merged.Name, actual.Name},
	} {
		_, authoredEmpty := d.authored[f.name]
		if authoredEmpty && *f.desired == "" && f.stored != "" {
			d.warnings = append(d.warnings, "kept the stored "+f.name+": this resource cannot be cleared to an empty value here")
		}
		emptyMeansPreserve(f.desired, f.stored)
	}

	agentAuth, agentNote, err := decideA2AAuthSurface("agentAuthConfig", create.AgentAuthConfig, create.AgentAuthType, actual.AgentAuthConfig, actual.AgentAuthType)
	if err != nil {
		return fmt.Errorf("agentAuthConfig: %w", err)
	}
	cardAuth, cardNote, err := decideA2AAuthSurface("agentCardAuthConfig", create.AgentCardAuthConfig, create.AgentCardAuthType, actual.AgentCardAuthConfig, actual.AgentCardAuthType)
	if err != nil {
		return fmt.Errorf("agentCardAuthConfig: %w", err)
	}
	d.warnings = slices.DeleteFunc(append(d.warnings, agentNote, cardNote), func(w string) bool { return w == "" })
	merged.AgentAuthConfig, merged.AgentAuthType = agentAuth.Config, agentAuth.Type
	merged.AgentCardAuthConfig, merged.AgentCardAuthType = cardAuth.Config, cardAuth.Type
	d.create = merged
	d.a2aRotatesCredential = agentAuth.Rotates || cardAuth.Rotates

	view := a2aViewFromCreate(merged)
	view.AgentAuthType = agentAuth.EffectiveType
	view.AgentCardAuthType = cardAuth.EffectiveType
	if merged.RequestTimeoutSeconds == 0 {
		view.RequestTimeoutSeconds = orDefaultTimeout(actual.RequestTimeoutSeconds)
	}
	if merged.AgentCardHeaders == nil {
		view.AgentCardHeaders = normalizeJSON(actual.AgentCardHeaders)
	}
	if merged.TaskHeaders == nil {
		view.TaskHeaders = normalizeJSON(actual.TaskHeaders)
	}
	if merged.Skills == nil {
		view.Skills = normalizeJSON(actual.Skills)
	}
	if merged.InputModes == nil {
		view.InputModes = normalizeJSON(actual.InputModes)
	}
	if merged.OutputModes == nil {
		view.OutputModes = normalizeJSON(actual.OutputModes)
	}
	if merged.ProtocolVersion == "" {
		view.ProtocolVersion = orDefaultProtocolPin(actual.ProtocolVersion)
	}
	if merged.TrustedCardJWKSURLs == nil {
		view.TrustedCardJWKSURLs = normalizeTrustedURLs(actual.TrustedCardJWKSURLs)
	}
	if merged.RequireSignedCard == nil {
		view.RequireSignedCard = actual.RequireSignedCard
	}
	storedAgentPublic := publicAuthFields(actual.AgentAuthConfig, actual.AgentAuthType)
	storedCardPublic := publicAuthFields(actual.AgentCardAuthConfig, actual.AgentCardAuthType)
	var keptAgent, keptCard []string
	if agentAuth.Config == nil {
		view.AgentAuthPublic = storedAgentPublic
	} else {
		view.AgentAuthPublic, keptAgent = mergePublicAuthFields(view.AgentAuthPublic, storedAgentPublic)
	}
	if cardAuth.Config == nil {
		view.AgentCardAuthPublic = storedCardPublic
	} else {
		view.AgentCardAuthPublic, keptCard = mergePublicAuthFields(view.AgentCardAuthPublic, storedCardPublic)
	}
	for _, kept := range []struct {
		field string
		keys  []string
	}{{"agentAuthConfig", keptAgent}, {"agentCardAuthConfig", keptCard}} {
		if len(kept.keys) > 0 {
			d.warnings = append(d.warnings, fmt.Sprintf(
				"kept these stored %s fields, which this config does not set: %s",
				kept.field, strings.Join(kept.keys, ", ")))
		}
	}
	d.planProto = view
	return nil
}

func preserveWorkflowFields(d *desiredComponent, stored storedResource) error {
	create, ok := d.create.(entplatform.CreateWorkflowRequest)
	if !ok {
		return nil
	}
	actual, ok := stored.raw.(entplatform.WorkflowResponse)
	if !ok {
		return nil
	}
	merged, _ := overlayUnauthored(create, actual, d.authored).(entplatform.CreateWorkflowRequest)
	if _, stated := d.authored["description"]; stated && create.Description == "" && actual.Description != "" {
		d.warnings = append(d.warnings, "kept the stored description: this resource cannot be cleared to an empty value here")
	}
	emptyMeansPreserve(&merged.Description, actual.Description)
	d.create = merged
	d.planProto = newWorkflowHashView("", merged.Name, merged.Description, merged.AppConfig)
	return nil
}

func preserveGatewayFields(d *desiredComponent, stored storedResource) error {
	create, ok := d.create.(entplatform.CreateGatewayRequest)
	if !ok {
		return nil
	}
	actual, ok := stored.raw.(entplatform.GatewayResponse)
	if !ok {
		return nil
	}
	merged, _ := overlayUnauthored(create, actual, d.authored).(entplatform.CreateGatewayRequest)
	values, kept := mergeStoredValues(create.Values, actual.Values,
		entplatform.GatewaySecretFieldNames(create.Type))
	merged.Values = values
	d.warnings = append(d.warnings, kept...)
	d.create, d.planProto = merged, merged
	return nil
}

func preserveConnectorFields(d *desiredComponent, stored storedResource) error {
	create, ok := d.create.(entplatform.CreateConnectorRequest)
	if !ok {
		return nil
	}
	actual, ok := stored.raw.(entplatform.ConnectorResponse)
	if !ok {
		return nil
	}
	merged, _ := overlayUnauthored(create, actual, d.authored).(entplatform.CreateConnectorRequest)
	values, kept := mergeStoredValues(clearAuthoredNullHIL(create.Values, actual.Values), storedValuesToCarry(create.Type, actual.Values),
		entplatform.ConnectorSecretFieldNames(create.Type, create.Subtype))
	merged.Values = values
	d.warnings = append(d.warnings, kept...)
	d.create, d.planProto = merged, withStoredMCPManifest(merged, actual.Values)
	return nil
}

const mcpManifestSetting = "manifest"

func storedValuesToCarry(connType string, stored any) any {
	storedMap, ok := stored.(map[string]any)
	if connType != "mcp" || !ok {
		return stored
	}
	if _, has := storedMap[mcpManifestSetting]; !has {
		return stored
	}
	carried := maps.Clone(storedMap)
	delete(carried, mcpManifestSetting)
	return carried
}

func withStoredMCPManifest(req entplatform.CreateConnectorRequest, stored any) entplatform.CreateConnectorRequest {
	storedMap, _ := stored.(map[string]any)
	manifest, has := storedMap[mcpManifestSetting]
	if req.Type != "mcp" || !has {
		return req
	}
	values, isMap := req.Values.(map[string]any)
	if req.Values != nil && !isMap {
		return req
	}
	if _, authored := values[mcpManifestSetting]; authored {
		return req
	}
	withManifest := maps.Clone(values)
	if withManifest == nil {
		withManifest = map[string]any{}
	}
	withManifest[mcpManifestSetting] = manifest
	req.Values = withManifest
	return req
}

func clearAuthoredNullHIL(values, stored any) any {
	authored, ok := values.(map[string]any)
	if !ok {
		return values
	}
	if held, present := authored[hilSetting]; !present || held != nil {
		return values
	}
	out := maps.Clone(authored)
	if storedMap, _ := stored.(map[string]any); storedMap != nil {
		if _, gated := storedMap[hilSetting]; gated {
			out[hilSetting] = map[string]any{}
			return out
		}
	}
	delete(out, hilSetting)
	return out
}

func skillConfigsForBoundSkills(configs []entplatform.SkillConfigBinding, skillIDs []string) []entplatform.SkillConfigBinding {
	if len(configs) == 0 {
		return configs
	}
	bound := make(map[string]struct{}, len(skillIDs))
	for _, id := range skillIDs {
		bound[id] = struct{}{}
	}
	kept := make([]entplatform.SkillConfigBinding, 0, len(configs))
	for _, c := range configs {
		if _, ok := bound[c.SkillID]; ok {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func droppedNames(before, after []string) []string {
	keep := make(map[string]struct{}, len(after))
	for _, name := range after {
		keep[name] = struct{}{}
	}
	var dropped []string
	for _, name := range before {
		if _, ok := keep[name]; !ok {
			dropped = append(dropped, name)
		}
	}
	slices.Sort(dropped)
	return dropped
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, v := range a {
		seen[v] = struct{}{}
	}
	for _, v := range b {
		if _, ok := seen[v]; !ok {
			return false
		}
	}
	return true
}

func toolsetConfigsForBoundToolsets(configs []entplatform.ToolsetConfigBinding, toolsets []string) []entplatform.ToolsetConfigBinding {
	if len(configs) == 0 {
		return configs
	}
	bound := make(map[string]struct{}, len(toolsets))
	for _, name := range toolsets {
		bound[name] = struct{}{}
	}
	kept := make([]entplatform.ToolsetConfigBinding, 0, len(configs))
	for _, c := range configs {
		if _, ok := bound[c.ToolsetName]; ok {
			kept = append(kept, c)
		}
	}
	return kept
}

func mergeStoredValues(authored, stored any, secretNames []string) (any, []string) {
	storedMap, ok := stored.(map[string]any)
	if !ok || len(storedMap) == 0 {
		return authored, nil
	}

	authoredMap, isMap := authored.(map[string]any)
	switch {
	case authored == nil, isMap && authoredMap == nil:
		return stored, nil
	case !isMap:
		return authored, nil
	}

	skip := make(map[string]struct{}, len(secretNames))
	for _, n := range secretNames {
		skip[n] = struct{}{}
	}

	out := maps.Clone(authoredMap)
	overridden := keepStoredOverPlaceholders(out, storedMap, skip)
	preserved := addUnsetStoredValues(out, storedMap, skip)
	return out, keptWarnings(overridden, preserved)
}

func addUnsetStoredValues(out, stored map[string]any, secretNames map[string]struct{}) []string {
	var preserved []string
	for key, value := range stored {
		if _, taken := out[key]; taken {
			continue
		}
		if _, isSecret := secretNames[key]; isSecret {
			continue
		}
		if s, isString := value.(string); isString &&
			(entplatform.IsSecretPlaceholder(s) || entplatform.IsRedactedSecret(s)) {
			continue
		}
		out[key] = value
		if m, isMap := value.(map[string]any); isMap && len(m) == 0 {
			continue
		}
		preserved = append(preserved, key)
	}
	slices.Sort(preserved)
	return preserved
}

func keptWarnings(overridden, preserved []string) []string {
	var warnings []string
	if len(overridden) > 0 {
		warnings = append(warnings, "kept these stored values over a <<__SAM_REQUIRED__>> placeholder: "+strings.Join(overridden, ", "))
	}
	if len(preserved) > 0 {
		warnings = append(warnings, keptUnsetValuesWarning+strings.Join(preserved, ", "))
	}
	return warnings
}

const keptUnsetValuesWarning = "kept these stored values, which this config does not set: "

func withSaveWarnings(planned, saved []string, plannedHIL any) []string {
	if !slices.ContainsFunc(saved, entplatform.IsRemovedGateWarning) || entplatform.ConnectorHILSurvivesRemovedGates(plannedHIL, saved) {
		return append(planned, saved...)
	}
	out := make([]string, 0, len(planned)+len(saved))
	for _, w := range planned {
		keys, isKept := strings.CutPrefix(w, keptUnsetValuesWarning)
		if !isKept {
			out = append(out, w)
			continue
		}
		remaining := slices.DeleteFunc(strings.Split(keys, ", "), func(k string) bool { return k == hilSetting })
		if len(remaining) > 0 {
			out = append(out, keptUnsetValuesWarning+strings.Join(remaining, ", "))
		}
	}
	return append(out, saved...)
}

func plannedConnectorHIL(d desiredComponent) any {
	create, ok := d.create.(entplatform.CreateConnectorRequest)
	if !ok {
		return nil
	}
	values, _ := create.Values.(map[string]any)
	return values[hilSetting]
}

func keepStoredOverPlaceholders(authored, stored map[string]any, secretNames map[string]struct{}) []string {
	var kept []string
	for key, value := range authored {
		if _, isSecret := secretNames[key]; isSecret {
			continue
		}
		if s, isString := value.(string); !isString || !entplatform.IsSecretPlaceholder(s) {
			continue
		}
		held, present := stored[key]
		if !present || held == nil {
			continue
		}
		if s, isString := held.(string); isString && (s == "" || entplatform.IsSecretPlaceholder(s) || entplatform.IsRedactedSecret(s)) {
			continue
		}
		authored[key] = held
		kept = append(kept, key)
	}
	slices.Sort(kept)
	return kept
}
