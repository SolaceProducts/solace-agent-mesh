// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "testing"

func fieldValidation(t *testing.T, entry map[string]any) map[string]any {
	t.Helper()
	v, ok := entry["validation"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return v
}

func TestToUIFieldEntry_OptionLabelDefaultsToValue(t *testing.T) {
	f := FieldDef{
		Name:  "method",
		Type:  "select",
		Label: "HTTP method",
		OneOf: []FieldOption{{Value: "POST"}, {Value: "PUT", Label: "Put"}, {}},
	}
	opts, ok := f.toUIFieldEntry()["options"].([]map[string]any)
	if !ok || len(opts) != 3 {
		t.Fatalf("options = %v, want 3 entries", f.toUIFieldEntry()["options"])
	}
	if opts[0]["value"] != "POST" || opts[0]["label"] != "POST" {
		t.Errorf("value-only option = %v, want label defaulted to value POST", opts[0])
	}
	if opts[1]["label"] != "Put" {
		t.Errorf("explicit label = %v, want Put preserved", opts[1]["label"])
	}
	if opts[2]["value"] != "" || opts[2]["label"] != "" {
		t.Errorf("empty-value option = %v, want {value:\"\", label:\"\"}", opts[2])
	}
}

func TestToUIFieldEntry_ZeroLengthOmitted(t *testing.T) {
	f := FieldDef{Name: "includeTools", Type: fieldTypeArrayObject, Label: "Include tools", UIVisible: true}
	v := fieldValidation(t, f.toUIFieldEntry())
	if _, present := v["maxLength"]; present {
		t.Error("unset MaxLen (0) must not emit maxLength")
	}
	if _, present := v["minLength"]; present {
		t.Error("unset MinLen (0) must not emit minLength")
	}
}

func TestToUIFieldEntry_NoLenCapOmitted(t *testing.T) {
	f := FieldDef{Name: "serverDescription", Type: "string", Label: "Description", UIVisible: true, MinLen: noLenCap, MaxLen: noLenCap}
	v := fieldValidation(t, f.toUIFieldEntry())
	if _, present := v["maxLength"]; present {
		t.Error("noLenCap MaxLen must not emit maxLength")
	}
	if _, present := v["minLength"]; present {
		t.Error("noLenCap MinLen must not emit minLength")
	}
}

func TestToUIFieldEntry_PositiveLengthsEmitted(t *testing.T) {
	f := FieldDef{Name: "serverName", Type: "string", Label: "MCP server name", UIVisible: true, MinLen: 1, MaxLen: 255}
	v := fieldValidation(t, f.toUIFieldEntry())
	if v["minLength"] != 1 {
		t.Errorf("minLength = %v, want 1", v["minLength"])
	}
	if v["maxLength"] != 255 {
		t.Errorf("maxLength = %v, want 255", v["maxLength"])
	}
}

func TestToUIFieldEntry_SingleStringItemEmitsItemKey(t *testing.T) {
	f := FieldDef{
		Name:      "includeTools",
		Type:      fieldTypeArrayObject,
		Label:     "Include tools",
		UIVisible: true,
		Inner: []FieldDef{
			{Name: "pattern", Type: "string", Label: "Pattern", MinLen: 1, MaxLen: 255},
		},
	}
	entry := f.toUIFieldEntry()
	if entry["itemKey"] != "pattern" {
		t.Errorf("itemKey = %v, want \"pattern\"", entry["itemKey"])
	}
	if entry["itemLabel"] != "Pattern" {
		t.Errorf("itemLabel = %v, want \"Pattern\"", entry["itemLabel"])
	}
	iv, ok := entry["itemValidation"].(map[string]any)
	if !ok {
		t.Fatalf("itemValidation missing or wrong type: %v", entry["itemValidation"])
	}
	if iv["minLength"] != 1 || iv["maxLength"] != 255 {
		t.Errorf("itemValidation = %v, want minLength 1 / maxLength 255", iv)
	}
}

func TestToUIFieldEntry_ScalarFieldOmitsItemKey(t *testing.T) {
	f := FieldDef{Name: "serverName", Type: "string", Label: "MCP server name", UIVisible: true, MinLen: 1, MaxLen: 255}
	if _, present := f.toUIFieldEntry()["itemKey"]; present {
		t.Error("scalar field must not emit itemKey")
	}
}

func TestGatewaySchemas_ArrayObjectFieldsExposeItemKey(t *testing.T) {
	for _, def := range AllGatewayTypeDefs() {
		for name, raw := range def.ToUISchemaMap() {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if entry["type"] != fieldTypeArrayObject {
				continue
			}
			if _, present := entry["itemKey"]; !present {
				t.Errorf("%s field %q is array<object> but emits no itemKey", def.Type, name)
			}
		}
	}
}

func TestGatewaySchemas_NoZeroLengthCaps(t *testing.T) {
	for _, def := range AllGatewayTypeDefs() {
		for name, raw := range def.ToUISchemaMap() {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			v := fieldValidation(t, entry)
			if max, present := v["maxLength"]; present && max == 0 {
				t.Errorf("%s field %q emits maxLength:0", def.Type, name)
			}
			if min, present := v["minLength"]; present && min == 0 {
				t.Errorf("%s field %q emits minLength:0", def.Type, name)
			}
		}
	}
}

func TestMCPSchema_ExposesStateRedirectionFields(t *testing.T) {
	def, ok := LookupGatewayType("mcp")
	if !ok {
		t.Fatal("mcp gateway type not registered")
	}
	schema := def.ToUISchemaMap()

	sr, ok := schema["stateRedirectionEnabled"].(map[string]any)
	if !ok {
		t.Fatal("stateRedirectionEnabled missing from MCP UI schema (UIVisible must be true)")
	}
	if v, _ := condDisplayValue(sr, "enableAuth"); v != "true" {
		t.Errorf("stateRedirectionEnabled conditionalDisplay[enableAuth] = %q, want \"true\"", v)
	}

	url, ok := schema["oauthDispatcherUrl"].(map[string]any)
	if !ok {
		t.Fatal("oauthDispatcherUrl missing from MCP UI schema (UIVisible must be true)")
	}
	if v, _ := condDisplayValue(url, "stateRedirectionEnabled"); v != "true" {
		t.Errorf("oauthDispatcherUrl conditionalDisplay[stateRedirectionEnabled] = %q, want \"true\"", v)
	}
	if v, _ := condDisplayValue(url, "enableAuth"); v != "true" {
		t.Errorf("oauthDispatcherUrl conditionalDisplay[enableAuth] = %q, want \"true\"", v)
	}
}

func TestWebhookSchema_TargetIsAGatedRequiredChoice(t *testing.T) {
	def, ok := LookupGatewayType("webhook")
	if !ok {
		t.Fatal("webhook gateway type not registered")
	}
	schema := def.ToUISchemaMap()

	targetType, ok := schema["targetType"].(map[string]any)
	if !ok {
		t.Fatal("targetType missing from webhook UI schema (UIVisible must be true)")
	}
	if targetType["required"] != true {
		t.Error("targetType is not required — the form would render no asterisk on the target choice")
	}
	if targetType["default"] != "agent" {
		t.Errorf("targetType default = %v, want agent", targetType["default"])
	}
	if targetType["rowGroup"] != "target" {
		t.Errorf("targetType rowGroup = %v, want target — the choice and the chosen target render as one row", targetType["rowGroup"])
	}
	if targetType["type"] != "select" {
		t.Errorf("targetType type = %v, want select", targetType["type"])
	}

	for _, tc := range []struct {
		field, gate, fieldType string
		order                  float64
	}{
		{field: "targetAgentName", gate: "agent", fieldType: "agent_select", order: 20},
		{field: "targetWorkflowName", gate: "workflow", fieldType: "workflow_select", order: 21},
	} {
		entry, ok := schema[tc.field].(map[string]any)
		if !ok {
			t.Fatalf("%s missing from webhook UI schema", tc.field)
		}
		if entry["type"] != tc.fieldType {
			t.Errorf("%s type = %v, want %s", tc.field, entry["type"], tc.fieldType)
		}
		if entry["order"] != tc.order {
			t.Errorf("%s order = %v, want %v — must stay adjacent to targetType (19) or the row splits", tc.field, entry["order"], tc.order)
		}
		if v, _ := condDisplayValue(entry, "targetType"); v != tc.gate {
			t.Errorf("%s conditionalDisplay[targetType] = %q, want %q", tc.field, v, tc.gate)
		}
		if entry["required"] != true {
			t.Errorf("%s is not required — nothing marks the selected target as mandatory", tc.field)
		}
		if entry["clearWhenHidden"] != true {
			t.Errorf("%s is not cleared when hidden — a stale value would trip the target mutex", tc.field)
		}
		if entry["rowGroup"] != "target" {
			t.Errorf("%s rowGroup = %v, want target", tc.field, entry["rowGroup"])
		}
	}
}

func condDisplayValue(entry map[string]any, key string) (string, bool) {
	switch cd := entry["conditionalDisplay"].(type) {
	case map[string]string:
		v, ok := cd[key]
		return v, ok
	case map[string]any:
		v, ok := cd[key].(string)
		return v, ok
	}
	return "", false
}

func TestEnableAuthDeclaredOnlyByMCP(t *testing.T) {
	var declaring []string
	for _, def := range AllGatewayTypeDefs() {
		for _, f := range def.Fields {
			if f.Name == "enableAuth" {
				declaring = append(declaring, def.Type)
				break
			}
		}
	}
	if len(declaring) != 1 || declaring[0] != "mcp" {
		t.Errorf("entrypoint types declaring enableAuth = %v, want [mcp] only.\n"+
			"If a new type legitimately needs it, confirm applyClusterAuth's authn/authz split is right for that type "+
			"(internal/platform/admin/yaml_service.go) and widen this test.", declaring)
	}
}

func TestAssistantContextDeclaredByEveryTypeThatSubmitsTasks(t *testing.T) {
	want := map[string]string{
		"systemPurpose":  "system_purpose",
		"responseFormat": "response_format",
	}
	for _, def := range AllGatewayTypeDefs() {
		if def.Type == AgentLinkGatewayType {
			continue
		}
		got := map[string]string{}
		for _, f := range def.Fields {
			if _, ok := want[f.Name]; !ok {
				continue
			}
			got[f.Name] = f.RuntimeKey
			if f.UIVisible {
				t.Errorf("%s field %q is UIVisible; the pair is administered instance-wide for the web UI "+
					"and a second per-entrypoint control would read as a competing source of truth",
					def.Type, f.Name)
			}
		}
		for name, runtimeKey := range want {
			if got[name] != runtimeKey {
				t.Errorf("%s field %q RuntimeKey = %q, want %q", def.Type, name, got[name], runtimeKey)
			}
		}
	}
}

func TestTheAgentLinkTypeDeclaresNoAssistantContextBecauseItSubmitsNoTasks(t *testing.T) {
	def, ok := LookupGatewayType(AgentLinkGatewayType)
	if !ok {
		t.Fatal("agent_link is not registered")
	}
	for _, f := range def.Fields {
		if f.Name == "systemPurpose" || f.Name == "responseFormat" {
			t.Errorf("agent_link declares %q, which nothing in the agent link applies", f.Name)
		}
	}
}

func TestGoogleChatSchema_IdentityToggleFollowsDefaultAgent(t *testing.T) {
	def, ok := LookupGatewayType("google_chat")
	if !ok {
		t.Fatal("google_chat is not a registered gateway type")
	}
	var names []string
	for _, f := range def.SortedFields() {
		if f.UIVisible {
			names = append(names, f.Name)
		}
	}
	at := func(name string) int {
		for i, n := range names {
			if n == name {
				return i
			}
		}
		t.Fatalf("%s is not a visible field of the Google Chat form: %v", name, names)
		return -1
	}
	if got, want := at("use_user_identity_in_channels"), at("default_agent_name")+1; got != want {
		t.Errorf("identity toggle is field %d of %v, want %d, right after default_agent_name", got, names, want)
	}
}

func TestGoogleChatSchema_FeedbackToggleIsNotOnTheForm(t *testing.T) {
	def, ok := LookupGatewayType("google_chat")
	if !ok {
		t.Fatal("google_chat is not a registered gateway type")
	}
	if _, onForm := def.ToUISchemaMap()["feedback_enabled"]; onForm {
		t.Error("feedback_enabled is on the Google Chat form; the other chat entrypoints offer it through YAML or the API only")
	}
	for _, f := range def.Fields {
		if f.Name == "feedback_enabled" {
			return
		}
	}
	t.Error("feedback_enabled left the Google Chat authoring schema; YAML and API users could no longer set it")
}
