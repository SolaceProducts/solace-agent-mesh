// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"math"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
)

func whatsappSchema(t *testing.T) GatewaySchemaResponse {
	t.Helper()
	for _, s := range getAllGatewaySchemas() {
		if s.Type == "whatsapp" {
			return s
		}
	}
	t.Fatal("whatsapp gateway schema not registered in getAllGatewaySchemas()")
	return GatewaySchemaResponse{}
}

func whatsappFieldMap(t *testing.T) map[string]map[string]any {
	t.Helper()
	schema := whatsappSchema(t)
	raw, ok := schema.Schema.(map[string]any)
	if !ok {
		t.Fatalf("whatsapp schema is %T, want map[string]any", schema.Schema)
	}
	out := make(map[string]map[string]any, len(raw))
	for name, def := range raw {
		m, ok := def.(map[string]any)
		if !ok {
			t.Fatalf("field %q is %T, want map[string]any", name, def)
		}
		out[name] = m
	}
	return out
}

func TestWhatsAppGatewayType_Properties(t *testing.T) {
	t.Parallel()
	schema := whatsappSchema(t)

	if schema.Type != "whatsapp" {
		t.Errorf("Type = %q, want %q", schema.Type, "whatsapp")
	}
	if schema.Name != "WhatsApp" {
		t.Errorf("Name = %q, want %q", schema.Name, "WhatsApp")
	}
	if !strings.Contains(schema.Description, "WhatsApp Business Cloud API") {
		t.Errorf("Description %q missing 'WhatsApp Business Cloud API'", schema.Description)
	}
	if !strings.Contains(schema.Warning, "WhatsApp Business Account") {
		t.Errorf("Warning %q missing 'WhatsApp Business Account'", schema.Warning)
	}
	if schema.YAMLTemplatePath != "whatsapp_gateway.yaml" {
		t.Errorf("YAMLTemplatePath = %q, want %q", schema.YAMLTemplatePath, "whatsapp_gateway.yaml")
	}
}

func TestWhatsAppGatewayType_FieldSchema_Structure(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)

	expected := []string{
		"phone_number_id",
		"access_token",
		"app_secret",
		"verify_token",
		"default_agent_name",
		"disable_file_uploads",
		SenderIdentityModeKey,
		"run_as",
		"identity_lookup_key",
		"identity_claim_key",
		"contact_request_template",
		"contact_request_template_language",
	}
	for _, name := range expected {
		if _, ok := fields[name]; !ok {
			t.Errorf("field %q missing from schema", name)
		}
	}
	if len(fields) != len(expected) {
		t.Errorf("schema has %d fields, want %d — unexpected fields change the create form", len(fields), len(expected))
	}
}

func TestWhatsAppGatewayType_RequiredFields(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)

	required := map[string]bool{
		"phone_number_id":                   true,
		"access_token":                      true,
		"app_secret":                        true,
		"verify_token":                      true,
		"default_agent_name":                false,
		"disable_file_uploads":              false,
		SenderIdentityModeKey:               false,
		"run_as":                            true,
		"identity_lookup_key":               false,
		"identity_claim_key":                false,
		"contact_request_template":          false,
		"contact_request_template_language": false,
	}
	for name, want := range required {
		got, _ := fields[name]["required"].(bool)
		if got != want {
			t.Errorf("%s required = %v, want %v", name, got, want)
		}
	}
}

func TestWhatsAppGatewayType_SecretFields(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)

	secret := map[string]bool{
		"phone_number_id":                   false,
		"access_token":                      true,
		"app_secret":                        true,
		"verify_token":                      true,
		"default_agent_name":                false,
		"disable_file_uploads":              false,
		SenderIdentityModeKey:               false,
		"run_as":                            false,
		"identity_lookup_key":               false,
		"identity_claim_key":                false,
		"contact_request_template":          false,
		"contact_request_template_language": false,
	}
	for name, want := range secret {
		got, _ := fields[name]["secret"].(bool)
		if got != want {
			t.Errorf("%s secret = %v, want %v", name, got, want)
		}
		if want {
			if typ, _ := fields[name]["type"].(string); typ != "password" {
				t.Errorf("%s type = %q, want password", name, typ)
			}
		}
	}

	reg := getSecretRegistry().GatewaySecretFields("whatsapp")
	for name, want := range secret {
		if _, registered := reg[name]; registered != want {
			t.Errorf("secret registry has %s = %v, want %v — this drives GET redaction", name, registered, want)
		}
	}
}

func TestWhatsAppGatewayType_PhoneNumberIDField(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)
	field, ok := fields["phone_number_id"]
	if !ok {
		t.Fatal("phone_number_id field missing")
	}

	if got, _ := field["type"].(string); got != "string" {
		t.Errorf("type = %q, want %q", got, "string")
	}
	if req, _ := field["required"].(bool); !req {
		t.Error("required = false, want true")
	}
	if secret, _ := field["secret"].(bool); secret {
		t.Error("secret = true, want false")
	}

	validation, ok := field["validation"].(map[string]any)
	if !ok {
		t.Fatalf("validation is %T, want map[string]any", field["validation"])
	}
	if pattern, _ := validation["pattern"].(string); pattern != phoneNumberIDPattern {
		t.Errorf("validation.pattern = %q, want phoneNumberIDPattern", pattern)
	}
	if msg, _ := validation["errorMsg"].(string); !strings.Contains(msg, "digits only") {
		t.Errorf("validation.errorMsg = %q, want a digits-only explanation", validation["errorMsg"])
	}
}

func TestWhatsAppGatewayType_AssignedAgentField(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)
	field, ok := fields["default_agent_name"]
	if !ok {
		t.Fatal("default_agent_name field missing")
	}

	if got, _ := field["label"].(string); got != "Assigned Agent" {
		t.Errorf("label = %q, want %q", got, "Assigned Agent")
	}
	if got, _ := field["type"].(string); got != "agent_select" {
		t.Errorf("type = %q, want %q", got, "agent_select")
	}
	if hint, _ := field["hint"].(string); !strings.Contains(hint, "first discovered agent") {
		t.Errorf("hint = %q, want the left-empty routing explanation", hint)
	}
}

func TestWhatsAppGatewayType_RequiredMessagesIncludeGuidance(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)

	for _, name := range []string{"phone_number_id", "access_token", "app_secret", "verify_token"} {
		validation, ok := fields[name]["validation"].(map[string]any)
		if !ok {
			t.Errorf("%s has no validation map", name)
			continue
		}
		msg, _ := validation["requiredMsg"].(string)
		label, _ := fields[name]["label"].(string)
		prefix := label + " is required. "
		if !strings.HasPrefix(msg, prefix) || len(msg) <= len(prefix) {
			t.Errorf("%s requiredMsg = %q, want %q followed by find-it guidance", name, msg, prefix)
		}
	}
}

func TestWhatsAppGatewayType_HintsFreeOfEditOnlyGuidance(t *testing.T) {
	t.Parallel()
	for name, field := range whatsappFieldMap(t) {
		hint, _ := field["hint"].(string)
		if strings.Contains(strings.ToLower(hint), "placeholder") {
			t.Errorf("%s hint %q mentions the placeholder convention; that guidance is edit-only and appended by the UI", name, hint)
		}
	}
}

func TestWhatsAppGatewayType_ContactRequestHintStatesTheTemplateRequirements(t *testing.T) {
	t.Parallel()
	field := whatsappFieldMap(t)["contact_request_template"]
	hint, _ := field["hint"].(string)
	for _, want := range []string{"Share Contact Info", "no variables"} {
		if !strings.Contains(hint, want) {
			t.Errorf("contact_request_template hint does not mention %q\nhint: %s", want, hint)
		}
	}
	if placeholder, _ := field["placeholder"].(string); placeholder == "request_contact_info_1" {
		t.Error("placeholder names Meta's library template, which ships with a variable and is refused on every send")
	}
	if lookup, _ := field["showTemplateLookup"].(bool); !lookup {
		t.Error("contact_request_template lost its template lookup")
	}
}

func TestWhatsAppGatewayType_UploadToggleField(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)

	uploads, ok := fields["disable_file_uploads"]
	if !ok {
		t.Fatal("disable_file_uploads field missing")
	}
	if got, _ := uploads["type"].(string); got != "boolean" {
		t.Errorf("disable_file_uploads type = %q, want boolean", got)
	}
	if got, present := uploads["default"]; !present || got != false {
		t.Errorf("disable_file_uploads default = %v (present=%v), want false — uploads are on unless turned off", got, present)
	}
}

func TestWhatsAppGatewayType_ContactRequestHiddenUnderUsernameLookup(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)
	for _, name := range []string{"contact_request_template", "contact_request_template_language"} {
		cond, ok := fields[name]["conditionalDisplay"].(map[string]string)
		if !ok {
			t.Errorf("%s declares no conditionalDisplay, so it shows under a username lookup", name)
			continue
		}
		if got := cond["identity_lookup_key"]; got != whatsapp.IdentityLookupPhone {
			t.Errorf("%s conditionalDisplay identity_lookup_key = %q, want %q", name, got, whatsapp.IdentityLookupPhone)
		}
		if clear, _ := fields[name]["clearWhenHidden"].(bool); !clear {
			t.Errorf("%s does not clear when hidden; a stale value 422s a save on an invisible field", name)
		}
	}
}

func TestWhatsAppGatewayType_IdentityFieldsShareASection(t *testing.T) {
	t.Parallel()
	fields := whatsappFieldMap(t)
	identityFields := map[string]bool{
		SenderIdentityModeKey:               true,
		"run_as":                            true,
		"identity_lookup_key":               true,
		"identity_claim_key":                true,
		"contact_request_template":          true,
		"contact_request_template_language": true,
	}
	firstIdentityOrder := math.Inf(1)
	lastConnectionOrder := math.Inf(-1)
	for name, field := range fields {
		section, _ := field["section"].(string)
		order, ok := toFloat64(field["order"])
		if !ok {
			t.Fatalf("%s has no order; the form sorts it last, opening a second default section", name)
		}
		if identityFields[name] {
			if section != whatsappIdentitySection {
				t.Errorf("%s section = %q, want %q", name, section, whatsappIdentitySection)
			}
			firstIdentityOrder = math.Min(firstIdentityOrder, order)
			continue
		}
		if section != "" {
			t.Errorf("%s section = %q, want the default section", name, section)
		}
		lastConnectionOrder = math.Max(lastConnectionOrder, order)
	}
	if lastConnectionOrder >= firstIdentityOrder {
		t.Errorf("a connection field (order %v) sorts after the first identity field (order %v), splitting the identity section", lastConnectionOrder, firstIdentityOrder)
	}
}

func TestContactRequestSurvivesAPatchWithNoExplicitLookupKey(t *testing.T) {
	t.Parallel()
	existing := map[string]any{
		"phone_number_id":          "555111",
		"contact_request_template": "request_contact_info_1",
	}
	incoming := map[string]any{"default_agent_name": "WeatherAgent"}

	merged, err := mergeGatewayValuesForUpdate("whatsapp", incoming, existing, getSecretRegistry().GatewaySecretFields("whatsapp"))
	if err != nil {
		t.Fatalf("mergeGatewayValuesForUpdate: %v", err)
	}
	if got := merged["contact_request_template"]; got != "request_contact_info_1" {
		t.Errorf("contact_request_template = %v, want it preserved — an absent identity_lookup_key means phone, not no lookup", got)
	}
}

func TestContactRequestDroppedWhenTheLookupKeyBecomesUsername(t *testing.T) {
	t.Parallel()
	existing := map[string]any{
		"phone_number_id":          "555111",
		"identity_lookup_key":      whatsapp.IdentityLookupPhone,
		"contact_request_template": "request_contact_info_1",
	}
	incoming := map[string]any{
		"identity_lookup_key": whatsapp.IdentityLookupWAUsername,
		"identity_claim_key":  "extension_deadbeef_waUsername",
	}

	merged, err := mergeGatewayValuesForUpdate("whatsapp", incoming, existing, getSecretRegistry().GatewaySecretFields("whatsapp"))
	if err != nil {
		t.Fatalf("mergeGatewayValuesForUpdate: %v", err)
	}
	if got, present := merged["contact_request_template"]; present {
		t.Errorf("contact_request_template = %v, want it dropped — a shared number cannot answer a username lookup, and the validator refuses the pairing", got)
	}
}

func TestContactRequestSurvivesABlankLookupKey(t *testing.T) {
	t.Parallel()
	existing := map[string]any{
		"phone_number_id":          "555111",
		"identity_lookup_key":      "",
		"contact_request_template": "request_contact_info_1",
	}
	incoming := map[string]any{"default_agent_name": "WeatherAgent"}

	merged, err := mergeGatewayValuesForUpdate("whatsapp", incoming, existing, getSecretRegistry().GatewaySecretFields("whatsapp"))
	if err != nil {
		t.Fatalf("mergeGatewayValuesForUpdate: %v", err)
	}
	if got := merged["contact_request_template"]; got != "request_contact_info_1" {
		t.Errorf("contact_request_template = %v, want it preserved", got)
	}
}
