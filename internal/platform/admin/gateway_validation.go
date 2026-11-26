// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

const errTrustedAuthservIDsRequired = "at least one trusted_authserv_ids entry is required"

func eventMeshSystemBrokerConfigured() bool {
	return os.Getenv("SOLACE_BROKER_URL") != ""
}

var eventMeshBrokerFields = []string{"broker_url", "broker_vpn", "broker_username", "broker_password"}

func withoutDevBrokerPlaceholders(values map[string]any) map[string]any {
	if eventMeshSystemBrokerConfigured() {
		return values
	}
	if hasRealValue(values, "broker_url") || hasRealValue(values, "broker_password") {
		return values
	}
	var out map[string]any
	for _, field := range eventMeshBrokerFields {
		if v, _ := values[field].(string); isPlaceholder(v) {
			if out == nil {
				out = maps.Clone(values)
			}
			delete(out, field)
		}
	}
	if out == nil {
		return values
	}
	return out
}

func hasRealValue(values map[string]any, field string) bool {
	v, _ := values[field].(string)
	v = strings.TrimSpace(v)
	return v != "" && !isPlaceholder(v)
}

var guidRe = regexp.MustCompile(guidPattern)

var phoneNumberIDRe = regexp.MustCompile(phoneNumberIDPattern)

func validGatewayTypes(schemas []GatewaySchemaResponse) map[string]bool {
	types := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		types[s.Type] = true
	}
	return types
}

func gatewayRequiresObjectValues(gatewayType string) bool {
	switch gatewayType {
	case "email", "teams", "whatsapp", "event_mesh", "webhook", "google_chat":
		return true
	default:
		return false
	}
}

func mergeGatewayValuesForUpdate(gatewayType string, newValues, existingValues map[string]any, secretFields map[string]secretFieldInfo) (map[string]any, error) {
	merged, err := mergeSecretFields(newValues, existingValues, secretFields)
	if err != nil {
		return nil, err
	}

	conds := gatewayFieldConditions(gatewayType)

	effective := make(map[string]any, len(existingValues)+len(merged))
	for k, v := range existingValues {
		effective[k] = v
	}
	for k, v := range merged {
		effective[k] = v
	}
	applySchemaDefaults(effective, gatewayFieldDefaults(gatewayType))

	for k, v := range existingValues {
		if _, present := merged[k]; present {
			continue
		}
		if cond, ok := conds[k]; ok && !conditionalDisplayMet(cond, effective) {
			continue
		}
		merged[k] = v
	}
	return merged, nil
}

func applySchemaDefaults(values, defaults map[string]any) {
	for field, def := range defaults {
		switch v, present := values[field]; {
		case !present, v == nil, v == "":
			values[field] = def
		}
	}
}

func gatewayFieldDefaults(gatewayType string) map[string]any {
	out := map[string]any{}
	for _, schema := range getAllGatewaySchemas() {
		if schema.Type != gatewayType {
			continue
		}
		schemaMap, ok := schema.Schema.(map[string]any)
		if !ok {
			return out
		}
		for fieldName, fieldDef := range schemaMap {
			defMap, ok := fieldDef.(map[string]any)
			if !ok {
				continue
			}
			if def, ok := defMap["default"]; ok {
				out[fieldName] = def
			}
		}
	}
	return out
}

func gatewayFieldConditions(gatewayType string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, schema := range getAllGatewaySchemas() {
		if schema.Type != gatewayType {
			continue
		}
		schemaMap, ok := schema.Schema.(map[string]any)
		if !ok {
			return out
		}
		for fieldName, fieldDef := range schemaMap {
			defMap, ok := fieldDef.(map[string]any)
			if !ok {
				continue
			}
			if cd := schemaConditionalDisplay(defMap["conditionalDisplay"]); len(cd) > 0 {
				out[fieldName] = cd
			}
		}
	}
	return out
}

func schemaConditionalDisplay(raw any) map[string]any {
	switch cd := raw.(type) {
	case map[string]any:
		return cd
	case map[string]string:
		out := make(map[string]any, len(cd))
		for k, v := range cd {
			out[k] = v
		}
		return out
	default:
		return nil
	}
}

const UnfilledDeployFieldsMessage = "entrypoint " + unfilledDeploySuffix

func validateGatewayValuesForDeploy(values map[string]any) error {
	details := map[string][]string{}
	collectPlaceholderFields("", values, details)
	if len(details) > 0 {
		return &ValidationError{
			Message: UnfilledDeployFieldsMessage,
			Details: details,
		}
	}
	return nil
}

func collectPlaceholderFields(prefix string, v any, details map[string][]string) {
	switch t := v.(type) {
	case string:
		if prefix != "" && isPlaceholder(t) {
			details[prefix] = append(details[prefix], prefix+" is required")
		}
	case map[string]any:
		for k, child := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			collectPlaceholderFields(key, child, details)
		}
	case []any:
		for i, child := range t {
			collectPlaceholderFields(fmt.Sprintf("%s[%d]", prefix, i), child, details)
		}
	}
}

func validateGatewayValues(gatewayType string, values map[string]any) error {
	switch gatewayType {
	case "slack":
		return validateSlackValues(values)
	case "event_mesh":
		return validateEventMeshValues(values)
	case "email":
		return validateEmailValues(values)
	case "mcp":
		return validateMCPGatewayValues(values)
	case "teams":
		return validateTeamsValues(values)
	case "whatsapp":
		return validateWhatsAppValues(values)
	case "webhook":
		return validateWebhookValues(values)
	case "google_chat":
		return validateGoogleChatValues(values)
	case platpkg.AgentLinkGatewayType:
		return validateAgentLinkValues(values)
	default:
		return nil
	}
}

func validateAgentLinkValues(values map[string]any) error {
	def, ok := platpkg.LookupGatewayType(platpkg.AgentLinkGatewayType)
	if !ok {
		return &ValidationError{Message: "agent_link entrypoint type not registered"}
	}
	details := map[string][]string{}
	known := make(map[string]bool, len(def.Fields))
	for _, f := range def.Fields {
		known[f.Name] = true
		if err := validateOneField("", f, values); err != nil {
			details[f.Name] = append(details[f.Name], err.Error())
			continue
		}
		if n, set := toFloat64(values[f.Name]); f.Type == gatewayFieldTypeNumber && set && n != math.Trunc(n) {
			details[f.Name] = append(details[f.Name], f.Name+": must be a whole number")
		}
	}
	for key := range values {
		if !known[key] {
			details[key] = append(details[key], key+" is not a setting of an agent_link entrypoint; link agents are managed on their own")
		}
	}
	if len(details) > 0 {
		return &ValidationError{Message: "invalid agent_link entrypoint values", Details: details}
	}
	return nil
}

var googleChatAdapterShapedFields = map[string]bool{
	"project_number":                true,
	"allowed_email_domains":         true,
	"use_user_identity_in_channels": true,
	"feedback_enabled":              true,
	"min_edit_interval_ms":          true,
	"thread_history_limit":          true,
	"artifact_drive_folder_id":      true,
	"artifact_drive_access_days":    true,
	"identity_claim_key":            true,
	"auth_audience_type":            true,
	"endpoint_url":                  true,
	"addon_service_account_email":   true,
}

func validateGoogleChatValues(values map[string]any) error {
	def, ok := platpkg.LookupGatewayType("google_chat")
	if !ok {
		return &ValidationError{Message: "google_chat entrypoint type not registered"}
	}
	details := map[string][]string{}
	effective := maps.Clone(values)
	if effective == nil {
		effective = map[string]any{}
	}
	if _, isString := values["delivery"].(string); isString || values["delivery"] == nil {
		effective["delivery"] = googleChatDelivery(values)
	}
	for _, f := range def.Fields {
		if googleChatAdapterShapedFields[f.Name] || !conditionalDisplayMet(f.ConditionalDisplay, effective) {
			continue
		}
		if err := validateOneField("", f, effective); err != nil {
			details[f.Name] = append(details[f.Name], err.Error())
		}
	}
	for _, p := range googleChatAudienceProblems(values) {
		addGoogleChatDetail(details, p.Field, p.Problem)
	}
	addGoogleChatDetail(details, "project_number", checkGoogleChatProjectNumber(values["project_number"]))
	addGoogleChatDetail(details, "allowed_email_domains", checkGoogleChatEmailDomains(values["allowed_email_domains"]))
	addGoogleChatDetail(details, "use_user_identity_in_channels", checkGoogleChatBool(values["use_user_identity_in_channels"]))
	addGoogleChatDetail(details, "feedback_enabled", checkGoogleChatBool(values["feedback_enabled"]))
	addGoogleChatDetail(details, "min_edit_interval_ms", checkGoogleChatInterval(values["min_edit_interval_ms"]))
	addGoogleChatDetail(details, "thread_history_limit", checkGoogleChatThreadHistoryLimit(values["thread_history_limit"]))
	addGoogleChatDetail(details, "artifact_drive_folder_id", checkGoogleChatDriveFolder(values["artifact_drive_folder_id"]))
	addGoogleChatDetail(details, "artifact_drive_access_days", checkGoogleChatDriveAccessDays(values["artifact_drive_access_days"]))
	addGoogleChatDetail(details, "identity_claim_key", checkGoogleChatClaimKey(values["identity_claim_key"]))
	if len(details["service_account_key"]) == 0 {
		if problem := checkGoogleChatServiceAccountKey(values["service_account_key"]); problem != "" {
			details["service_account_key"] = []string{problem}
		}
	}
	if len(details) > 0 {
		return &ValidationError{Message: "google_chat entrypoint configuration is invalid", Details: details}
	}
	return nil
}

func googleChatDelivery(values map[string]any) string {
	return googlechat.ResolveDelivery(values)
}

func stampGoogleChatDelivery(values map[string]any) map[string]any {
	switch delivery := values["delivery"].(type) {
	case nil:
	case string:
		if strings.TrimSpace(delivery) != "" {
			return values
		}
	default:
		return values
	}
	stamped := make(map[string]any, len(values)+1)
	maps.Copy(stamped, values)
	stamped["delivery"] = googleChatDelivery(values)
	return stamped
}

func addGoogleChatDetail(details map[string][]string, field, problem string) {
	if problem == "" {
		return
	}
	if message := (googlechat.FieldProblem{Field: field, Problem: problem}).String(); !slices.Contains(details[field], message) {
		details[field] = append(details[field], message)
	}
}

func googleChatAudienceProblems(values map[string]any) []googlechat.FieldProblem {
	deployed := maps.Clone(values)
	if deployed == nil {
		deployed = map[string]any{}
	}
	endpointUnset := googleChatEndpointUnset(values)
	base := googleChatExternalBase()
	if endpointUnset {
		deployed["endpoint_url"] = base
	}
	problems := googlechat.HTTPSAudienceProblems(deployed)
	if !endpointUnset {
		return problems
	}
	for i, p := range problems {
		if p.Field != "endpoint_url" {
			continue
		}
		problems[i].Problem = "is required: this deployment's external web address (WEBUI_FRONTEND_SERVER_URL) is not https, so the endpoint cannot be built from it"
		if base == "" {
			problems[i].Problem = "is required: this deployment has no external web address (WEBUI_FRONTEND_SERVER_URL) to build it from"
		}
	}
	return problems
}

func googleChatEndpointUnset(values map[string]any) bool {
	switch v := values["endpoint_url"].(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	}
	return false
}

func checkGoogleChatProjectNumber(raw any) string {
	switch raw.(type) {
	case nil, string, float64, int, int64:
		return ""
	}
	return "must be a string or number"
}

func checkGoogleChatEmailDomains(raw any) string {
	var entries []string
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		if strings.TrimSpace(v) == "" {
			return ""
		}
		entries = strings.Split(v, ",")
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return "entries must be strings"
			}
			entries = append(entries, s)
		}
	default:
		return "must be a comma-separated string or a list of domains"
	}
	domains := 0
	for _, entry := range entries {
		domain, problem := googlechat.NormalizeDomain(entry)
		if problem != "" {
			return problem
		}
		if domain != "" {
			domains++
		}
	}
	if len(entries) > 0 && domains == 0 {
		return "names no domain; list at least one, or leave it empty to allow every domain"
	}
	return ""
}

func checkGoogleChatBool(raw any) string {
	if raw == nil {
		return ""
	}
	if _, ok := gatewayBoolValue(raw); !ok {
		return "must be true or false"
	}
	return ""
}

const msgNonNegativeInteger = "must be a non-negative integer"

var msgThreadHistoryRange = fmt.Sprintf("must be an integer from 0 to %d", googlechat.MaxThreadHistoryLimit)

var msgDriveAccessDaysRange = fmt.Sprintf("must be an integer from 1 to %d", googlechat.MaxArtifactDriveAccessDays)

const msgDriveFolderShape = "must be the id of a Drive folder or shared drive, not its link"

func googleChatWholeNumber(raw any) (float64, bool) {
	var n float64
	switch v := raw.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		n = float64(parsed)
	default:
		return 0, false
	}
	return n, n == math.Trunc(n)
}

func checkGoogleChatInterval(raw any) string {
	if raw == nil {
		return ""
	}
	ms, ok := googleChatWholeNumber(raw)
	if !ok || ms < 0 {
		return msgNonNegativeInteger
	}
	if ms > googlechat.MaxMinEditIntervalMillis {
		return fmt.Sprintf("must be at most %d", googlechat.MaxMinEditIntervalMillis)
	}
	return ""
}

func checkGoogleChatThreadHistoryLimit(raw any) string {
	if raw == nil {
		return ""
	}
	if limit, ok := googleChatWholeNumber(raw); !ok || limit < 0 || limit > googlechat.MaxThreadHistoryLimit {
		return msgThreadHistoryRange
	}
	return ""
}

func checkGoogleChatDriveFolder(raw any) string {
	if raw == nil {
		return ""
	}
	s, ok := gatewayStringValue(raw)
	if !ok {
		return "must be a string"
	}
	if s = strings.TrimSpace(s); s != "" && !googlechat.ValidDriveFolderID(s) {
		return msgDriveFolderShape
	}
	return ""
}

func checkGoogleChatDriveAccessDays(raw any) string {
	if raw == nil {
		return ""
	}
	if days, ok := googleChatWholeNumber(raw); !ok || days < 1 || days > googlechat.MaxArtifactDriveAccessDays {
		return msgDriveAccessDaysRange
	}
	return ""
}

func checkGoogleChatClaimKey(raw any) string {
	if raw == nil {
		return ""
	}
	s, ok := gatewayStringValue(raw)
	if !ok {
		return "must be a string"
	}
	if s != "" && strings.TrimSpace(s) == "" {
		return "must not be blank; omit it to match on email"
	}
	return ""
}

func checkGoogleChatServiceAccountKey(raw any) string {
	key, _ := raw.(string)
	key = strings.TrimSpace(key)
	if key == "" || isPlaceholder(key) {
		return ""
	}
	return googlechat.ServiceAccountKeyProblem(key)
}

func validateWebhookValues(values map[string]any) error {
	def, ok := platpkg.LookupGatewayType("webhook")
	if !ok {
		return &ValidationError{Message: "webhook entrypoint type not registered"}
	}
	agent := strings.TrimSpace(stringOrDefault(values, "targetAgentName", ""))
	workflow := strings.TrimSpace(stringOrDefault(values, "targetWorkflowName", ""))
	switch {
	case agent == "" && workflow == "":
		return &ValidationError{
			Message: "exactly one of targetAgentName or targetWorkflowName is required",
		}
	case agent != "" && workflow != "":
		return &ValidationError{
			Message: "targetAgentName and targetWorkflowName are mutually exclusive — set exactly one",
		}
	case webhookTargetType(values) == webhookTargetAgent && agent == "":
		return &ValidationError{
			Message: fmt.Sprintf("targetType is agent but targetAgentName is empty — targetWorkflowName is set to %q, so either set targetType to workflow or move the name to targetAgentName", workflow),
		}
	case webhookTargetType(values) == webhookTargetWorkflow && workflow == "":
		return &ValidationError{
			Message: fmt.Sprintf("targetType is workflow but targetWorkflowName is empty — targetAgentName is set to %q, so either set targetType to agent or move the name to targetWorkflowName", agent),
		}
	}

	return validateAgainstRegistry("", def.Fields, normalizeWebhookValues(values))
}

func validateWhatsAppValues(values map[string]any) error {
	details := map[string][]string{}
	for _, field := range []string{"phone_number_id", "access_token", "app_secret", "verify_token"} {
		if v, _ := values[field].(string); strings.TrimSpace(v) == "" {
			details[field] = append(details[field], field+" is required")
		}
	}
	if v, _ := values["phone_number_id"].(string); strings.TrimSpace(v) != "" && !isPlaceholder(v) && !phoneNumberIDRe.MatchString(strings.TrimSpace(v)) {
		details["phone_number_id"] = append(details["phone_number_id"],
			"phone_number_id must be the numeric phone number ID from Meta (digits only), not the phone number itself")
	}
	if v, present := values["disable_file_uploads"]; present {
		if _, ok := gatewayBoolValue(v); !ok {
			details["disable_file_uploads"] = append(details["disable_file_uploads"], "disable_file_uploads must be true or false")
		}
	}
	lookupKey := whatsAppIdentityKey(values, "identity_lookup_key", details)
	claimKey := whatsAppIdentityKey(values, "identity_claim_key", details)
	switch lookupKey {
	case "", "phone":
		if claimKey != "" && !identity.SameClaimKey(claimKey, ClaimKeyPhoneNumber) {
			details["identity_claim_key"] = append(details["identity_claim_key"],
				`identity_claim_key must be "`+ClaimKeyPhoneNumber+`" when identity_lookup_key is "phone" — an inbound WhatsApp number is only compared as a phone number against that claim; map the attribute onto `+ClaimKeyPhoneNumber+` in your identity provider if it carries the number elsewhere`)
		}
	case "wa_username":
		if claimKey == "" {
			details["identity_claim_key"] = append(details["identity_claim_key"],
				"identity_claim_key is required when identity_lookup_key is wa_username — name the identity-provider claim holding each user's WhatsApp handle")
		}
		if identity.SameClaimKey(claimKey, ClaimKeyPhoneNumber) {
			details["identity_claim_key"] = append(details["identity_claim_key"],
				`identity_claim_key cannot be "`+ClaimKeyPhoneNumber+`" when identity_lookup_key is wa_username — a WhatsApp handle would be canonicalized as a phone number, so a handle spelled from somebody else's digits would match their claim; name the claim holding each user's WhatsApp handle instead`)
		}
	default:
		details["identity_lookup_key"] = append(details["identity_lookup_key"],
			`identity_lookup_key must be "phone" or "wa_username"`)
	}
	contactTemplateRaw, _ := values["contact_request_template"].(string)
	contactLanguageRaw, _ := values["contact_request_template_language"].(string)
	contactTemplate, contactLanguage := strings.TrimSpace(contactTemplateRaw), strings.TrimSpace(contactLanguageRaw)
	if contactTemplate == "" && contactLanguage != "" {
		details["contact_request_template"] = append(details["contact_request_template"],
			"contact_request_template is required when contact_request_template_language is set — name the approved template, or clear the language")
	}
	if contactTemplate != "" && lookupKey == "wa_username" {
		details["contact_request_template"] = append(details["contact_request_template"],
			`contact_request_template requires identity_lookup_key "phone" — a shared phone number cannot answer a wa_username lookup`)
	}
	if runAs, _ := values["run_as"].(string); strings.TrimSpace(runAs) != "" {
		for field, v := range map[string]string{
			"identity_lookup_key":               lookupKey,
			"identity_claim_key":                claimKey,
			"contact_request_template":          contactTemplate,
			"contact_request_template_language": contactLanguage,
		} {
			if v != "" {
				details[field] = append(details[field],
					field+" cannot be combined with run_as — a run_as entrypoint authorizes as its service account and never identifies the sender")
			}
		}
	}
	for _, field := range []string{"contact_request_template", "contact_request_template_language"} {
		if v, present := values[field]; present {
			if _, ok := gatewayStringValue(v); !ok {
				recordNotAString(details, field)
			}
		}
	}
	if v, present := values["run_as"]; present {
		if _, ok := gatewayStringValue(v); !ok {
			details["run_as"] = append(details["run_as"], "run_as must be a service account name string")
		}
	}
	validateWhatsAppSenderIdentityMode(values, details)
	if len(details) > 0 {
		return &ValidationError{Message: "whatsapp entrypoint configuration is invalid", Details: details}
	}
	return nil
}

func recordNotAString(details map[string][]string, field string) {
	details[field] = append(details[field], field+" must be a string")
}

func whatsAppIdentityKey(values map[string]any, field string, details map[string][]string) string {
	raw, present := values[field]
	if !present {
		return ""
	}
	s, ok := gatewayStringValue(raw)
	if !ok {
		recordNotAString(details, field)
		return ""
	}
	return strings.TrimSpace(s)
}

func validateWhatsAppSenderIdentityMode(values map[string]any, details map[string][]string) {
	raw, present := values[SenderIdentityModeKey]
	if !present {
		return
	}
	mode, ok := gatewayStringValue(raw)
	if !ok {
		recordNotAString(details, SenderIdentityModeKey)
		return
	}
	mode = strings.TrimSpace(mode)
	runAs, _ := values["run_as"].(string)
	runAs = strings.TrimSpace(runAs)
	switch mode {
	case "":
	case SenderIdentityIDPLookup:
		if runAs != "" {
			details["run_as"] = append(details["run_as"],
				`run_as cannot be set when `+SenderIdentityModeKey+` is "`+SenderIdentityIDPLookup+`" — the runtime reads run_as alone, so it would authorize every sender as that service account rather than looking them up; clear one of the two`)
		}
	case SenderIdentitySystemUser:
		if runAs == "" {
			details["run_as"] = append(details["run_as"],
				`run_as is required when `+SenderIdentityModeKey+` is "`+SenderIdentitySystemUser+`" — name the service account inbound messages authorize as, or switch to "`+SenderIdentityIDPLookup+`"`)
		}
	default:
		details[SenderIdentityModeKey] = append(details[SenderIdentityModeKey],
			SenderIdentityModeKey+` must be "`+SenderIdentityIDPLookup+`" or "`+SenderIdentitySystemUser+`", got "`+mode+`"`)
	}
}

func gatewayStringValue(v any) (value string, ok bool) {
	s, isString := v.(string)
	return s, isString
}

func gatewayBoolValue(v any) (value, ok bool) {
	switch v := v.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return parsed, err == nil
	}
	return false, false
}

func validateTeamsValues(values map[string]any) error {
	details := map[string][]string{}

	appID, _ := values["microsoft_app_id"].(string)
	switch {
	case appID == "":
		details["microsoft_app_id"] = append(details["microsoft_app_id"], "microsoft_app_id is required")
	case !isPlaceholder(appID) && !guidRe.MatchString(appID):
		details["microsoft_app_id"] = append(details["microsoft_app_id"],
			"microsoft_app_id must be a valid GUID (e.g. 12345678-1234-1234-1234-123456789abc)")
	}

	password, _ := values["microsoft_app_password"].(string)
	if password == "" {
		details["microsoft_app_password"] = append(details["microsoft_app_password"], "microsoft_app_password is required")
	}

	tenantID, _ := values["microsoft_app_tenant_id"].(string)
	if tenantID != "" && !isPlaceholder(tenantID) && !guidRe.MatchString(tenantID) {
		details["microsoft_app_tenant_id"] = append(details["microsoft_app_tenant_id"],
			"microsoft_app_tenant_id must be a valid GUID (e.g. 12345678-1234-1234-1234-123456789abc)")
	}

	if len(details) > 0 {
		return &ValidationError{Message: "teams entrypoint configuration is invalid", Details: details}
	}
	return nil
}

func validateSlackValues(values map[string]any) error {
	botToken, _ := values["bot_token"].(string)
	if botToken != "" && !isPlaceholder(botToken) && !strings.HasPrefix(botToken, "xoxb-") {
		return &ValidationError{Message: "bot_token must start with 'xoxb-'", Field: fieldValuesPrefix + "bot_token"}
	}
	appToken, _ := values["app_token"].(string)
	if appToken != "" && !isPlaceholder(appToken) && !strings.HasPrefix(appToken, "xapp-") {
		return &ValidationError{Message: "app_token must start with 'xapp-'", Field: fieldValuesPrefix + "app_token"}
	}
	return nil
}

func validateEmailValues(values map[string]any) error {
	mailbox, _ := values["mailbox"].(string)
	if mailbox == "" {
		return &ValidationError{Message: "mailbox is required"}
	}
	if !strings.Contains(mailbox, "@") {
		return &ValidationError{Message: "mailbox must be a valid email address"}
	}

	authMode, _ := values["auth_mode"].(string)
	switch authMode {
	case "basic":
		if password, _ := values["imap_password"].(string); password == "" {
			return &ValidationError{Message: "imap_password is required when auth_mode=basic"}
		}
		for _, field := range []string{"oauth2_tenant_id", "oauth2_client_id", "oauth2_client_secret"} {
			if v, _ := values[field].(string); v != "" && !isPlaceholder(v) {
				return &ValidationError{Message: fmt.Sprintf("%s must be empty when auth_mode=basic", field)}
			}
		}
	case "oauth2":
		for _, field := range []string{"oauth2_tenant_id", "oauth2_client_id", "oauth2_client_secret"} {
			if v, _ := values[field].(string); v == "" {
				return &ValidationError{Message: fmt.Sprintf("%s is required when auth_mode=oauth2", field)}
			}
		}
		if v, _ := values["imap_password"].(string); v != "" && !isPlaceholder(v) {
			return &ValidationError{Message: "imap_password must be empty when auth_mode=oauth2"}
		}
	default:
		return &ValidationError{Message: "auth_mode must be 'basic' or 'oauth2'"}
	}

	if mode, _ := values["policy_mode"].(string); mode != "" {
		valid := map[string]bool{"log_only": true, "require_dmarc": true, "strict": true}
		if !valid[mode] {
			return &ValidationError{Message: "policy_mode must be one of: log_only, require_dmarc, strict"}
		}
	}

	if mode, _ := values["delivery_mode"].(string); mode != "" {
		valid := map[string]bool{"auto": true, "idle": true, "poll": true}
		if !valid[mode] {
			return &ValidationError{Message: "delivery_mode must be one of: auto, idle, poll"}
		}
	}

	switch v := values["trusted_authserv_ids"].(type) {
	case string:
		hasEntry := false
		for _, part := range strings.Split(v, ",") {
			if strings.TrimSpace(part) != "" {
				hasEntry = true
				break
			}
		}
		if !hasEntry {
			return &ValidationError{Message: errTrustedAuthservIDsRequired}
		}
	case []any:
		if len(v) == 0 {
			return &ValidationError{Message: errTrustedAuthservIDsRequired}
		}
	default:
		return &ValidationError{Message: errTrustedAuthservIDsRequired}
	}

	return nil
}

func validateEventMeshValues(values map[string]any) error {
	def, ok := platpkg.LookupGatewayType("event_mesh")
	if !ok {
		return &ValidationError{Message: "event_mesh entrypoint type not registered"}
	}

	if eventMeshSystemBrokerConfigured() {
		var missing []string
		for _, field := range eventMeshBrokerFields {
			v, _ := values[field].(string)
			if v == "" && !isPlaceholder(v) {
				missing = append(missing, field)
			}
		}
		if len(missing) > 0 {
			return &ValidationError{
				Message: fmt.Sprintf("broker connection requires fields: %s", strings.Join(missing, ", ")),
			}
		}
	}

	fields := def.Fields
	if !eventMeshSystemBrokerConfigured() {
		fields = make([]platpkg.FieldDef, len(def.Fields))
		copy(fields, def.Fields)
		for i := range fields {
			if slices.Contains(eventMeshBrokerFields, fields[i].Name) {
				fields[i].Required = false
			}
		}
	}
	if err := validateAgainstRegistry("", fields, values); err != nil {
		return err
	}

	return validateEventRulesCrossFieldOf(values)
}

func validateEventRulesCrossFieldOf(values map[string]any) error {
	if rules, ok := values["event_rules"].([]any); ok {
		return validateEventRulesCrossField(rules)
	}
	return nil
}

func ValidateGatewayEventRules(values map[string]any) error {
	def, ok := platpkg.LookupGatewayType("event_mesh")
	if !ok {
		return &ValidationError{Message: "event_mesh entrypoint type not registered"}
	}
	var rulesFields []platpkg.FieldDef
	for _, f := range def.Fields {
		if f.Name == "event_rules" {
			rulesFields = append(rulesFields, f)
		}
	}
	if err := validateAgainstRegistry("", rulesFields, values); err != nil {
		return err
	}
	return validateEventRulesCrossFieldOf(values)
}

func validateEventRulesCrossField(rules []any) error {
	namesSeen := make(map[string]bool, len(rules))
	queueNamesSeen := make(map[string]int, len(rules))
	for i, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		prefix := fmt.Sprintf("event_rules[%d]", i)

		name, _ := rule["name"].(string)
		lower := strings.ToLower(name)
		if namesSeen[lower] {
			return &ValidationError{
				Message: fmt.Sprintf("%s: duplicate rule name %q (case-insensitive)", prefix, name),
			}
		}
		namesSeen[lower] = true

		existingQueueName, _ := rule["existingQueueName"].(string)
		existingQueueName = strings.TrimSpace(existingQueueName)
		subscriptions, _ := rule["subscriptions"].([]any)
		if existingQueueName != "" && len(subscriptions) > 0 {
			return &ValidationError{
				Message: fmt.Sprintf("%s: existingQueueName and subscriptions are mutually exclusive — set existingQueueName (consume from a queue that already exists on the broker) or subscriptions (Agent Mesh creates the queue), not both", prefix),
			}
		}
		if existingQueueName == "" && len(subscriptions) == 0 {
			return &ValidationError{
				Message: fmt.Sprintf("%s: subscriptions is required (or set existingQueueName to bind to a queue that already exists on the broker)", prefix),
			}
		}
		if existingQueueName != "" {
			if strings.Contains(existingQueueName, "/q/eventmesh/") {
				return &ValidationError{
					Message: fmt.Sprintf("%s: existingQueueName %q names an Agent Mesh-managed event-mesh queue path; point this rule at a customer-provisioned queue instead", prefix, existingQueueName),
				}
			}
			if prev, dup := queueNamesSeen[existingQueueName]; dup {
				return &ValidationError{
					Message: fmt.Sprintf("%s: existingQueueName %q is also set on event_rules[%d]; two rules cannot bind the same queue — one queue delivers each message to only one consumer", prefix, existingQueueName, prev),
				}
			}
			queueNamesSeen[existingQueueName] = i
		}

		if filter := rule["eventFilter"]; filter != nil {
			if _, err := sacexpr.CompilePredicate(prefix+".eventFilter", filter); err != nil {
				return &ValidationError{Message: err.Error()}
			}
		}

		targetAgent, _ := rule["targetAgent"].(string)
		targetWorkflow, _ := rule["targetWorkflowName"].(string)
		if targetAgent != "" && targetWorkflow != "" {
			return &ValidationError{
				Message: fmt.Sprintf("%s: targetAgent and targetWorkflowName are mutually exclusive", prefix),
			}
		}

		isWorkflowTarget := targetWorkflow != ""
		promptTemplate, _ := rule["promptTemplate"].(string)
		if !isWorkflowTarget && promptTemplate == "" {
			return &ValidationError{
				Message: fmt.Sprintf("%s: promptTemplate is required for agent targets", prefix),
			}
		}

		for _, outputKey := range []string{"successOutput", "errorOutput"} {
			outputMap, _ := rule[outputKey].(map[string]any)
			if outputMap == nil {
				continue
			}
			if err := validateEventMeshOutputDelivery(prefix+"."+outputKey, outputMap); err != nil {
				return err
			}
			respType, _ := outputMap["responseType"].(string)
			if respType != "custom" {
				continue
			}
			customExpr, _ := outputMap["customExpression"].(string)
			if customExpr == "" {
				return &ValidationError{
					Message: fmt.Sprintf("%s.%s: customExpression is required when responseType=custom", prefix, outputKey),
				}
			}
		}
	}
	return nil
}

func validateMCPGatewayValues(values map[string]any) error {
	def, ok := platpkg.LookupGatewayType("mcp")
	if !ok {
		return &ValidationError{Message: "mcp entrypoint type not registered"}
	}

	if err := validateAgainstRegistry("", def.Fields, values); err != nil {
		return err
	}

	enableAuth := mcpEnableAuth(values)
	transport, _ := values["transport"].(string)
	if transport == "" {
		transport = "sse"
	}

	defaultIdentity, _ := values["defaultUserIdentity"].(string)

	if enableAuth && defaultIdentity != "" && transport != "stdio" {
		return &ValidationError{
			Message: "defaultUserIdentity is incompatible with enableAuth on an HTTP transport (the Bearer token IS the identity source) — set one or the other",
		}
	}
	if rbac.IsSystemPrincipal(defaultIdentity) {
		return &ValidationError{
			Message: fmt.Sprintf("defaultUserIdentity must not use the reserved %q prefix — set runAs to name a service account instead", rbac.SystemSubPrefix),
		}
	}

	if enableAuth {
		if sr, explicit := mcpBoolValue(values, "stateRedirectionEnabled"); explicit && sr && mcpDispatcherURL(values) == "" {
			return &ValidationError{
				Message: "stateRedirectionEnabled is on but no OAuth dispatcher URL is available — set oauthDispatcherUrl to the dispatcher's callback (typically <webui-base>/api/v1/auth/callback), ensure WEBUI_FRONTEND_SERVER_URL is configured so it can be derived, or set stateRedirectionEnabled to false to use the legacy per-gateway callback",
			}
		}
	}

	if u := strings.TrimSpace(stringOrDefault(values, "oauthDispatcherUrl", "")); u != "" {
		for i := range def.Fields {
			if def.Fields[i].Name == "oauthDispatcherUrl" {
				if err := validateStringValue("oauthDispatcherUrl", def.Fields[i], u); err != nil {
					return err
				}
				break
			}
		}
	}

	return nil
}

func mcpBoolValue(values map[string]any, key string) (val bool, present bool) {
	switch v := values[key].(type) {
	case bool:
		return v, true
	case string:
		return strings.EqualFold(v, "true"), true
	default:
		return false, false
	}
}

func mcpEnableAuth(values map[string]any) bool {
	v, ok := boolFromValue(values["enableAuth"])
	return ok && v
}
