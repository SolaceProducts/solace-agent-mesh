// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func RequireScope(scope string, handler http.HandlerFunc) http.HandlerFunc {
	return requireScope(scope, handler)
}

func KeepSkillRef(ref string, hasM2M bool) bool {
	return keepSkillRef(ref, hasM2M)
}

func RequireAnyOfScope(scopes []string, handler http.HandlerFunc) http.HandlerFunc {
	return requireAnyOfScope(scopes, handler)
}

func RequireIdentity(handler http.HandlerFunc) http.HandlerFunc {
	return requireIdentity(handler)
}

func ModelTestSpendAttribution(ctx context.Context, logger *slog.Logger, clientSurface, configID string) *llm.SpendAttribution {
	return directActionSpend(ctx, logger, clientSurface, spend.SurfaceModelTest, "", configID)
}

func RequireSuperAdmin(logger *slog.Logger, resource string, handler http.HandlerFunc) http.HandlerFunc {
	return requireSuperAdmin(logger, resource, handler)
}

func RefuseUnderMiniIDP(miniIDPMode bool, handler http.HandlerFunc) http.HandlerFunc {
	return guardMiniIDPWrite(miniIDPMode, handler)
}

func RBACConflictSentence(err error) (string, bool) {
	for _, conflict := range []error{errYAMLImmutable, errBuiltinImmutable, errBuiltinSystemUserImmutable, errRoleIsDefault} {
		if errors.Is(err, conflict) {
			return conflict.Error(), true
		}
	}
	if sayable, ok := errors.AsType[sayableConflict](err); ok {
		return sayable.Message, true
	}
	c, ok := errors.AsType[*ConflictError](err)
	if !ok {
		return "", false
	}
	for _, resource := range []string{auditResourceRole, resourceClaimMapping, auditResourceGrant, auditResourceSystemUser, ""} {
		if c.Message == uniqueConflictMessage(resource) {
			return c.Message, true
		}
	}
	return "", false
}

const (
	AuditResourceRole               = auditResourceRole
	AuditResourceGrant              = auditResourceGrant
	AuditResourceClaimMapping       = auditResourceClaimMapping
	AuditResourceDefaultRoles       = auditResourceDefaultRoles
	AuditResourceSystemUser         = auditResourceSystemUser
	AuditResourceIdentityClaims     = auditResourceIdentityClaims
	AuditResourceSystemUserToolAuth = auditResourceSystemUserToolAuth
)

func DecodeJSON(r *http.Request, v any) error {
	return decodeJSON(r, v)
}

func BadRequestFromDecode(w http.ResponseWriter, err error) {
	badRequestFromDecode(w, err)
}

func HandleServiceError(w http.ResponseWriter, err error) {
	handleServiceError(w, err)
}

func HandleServiceErrorWithCause(w http.ResponseWriter, err error) {
	handleServiceErrorWithCause(w, err)
}

func WriteErrorWithCause(w http.ResponseWriter, status int, message string, details map[string][]string, cause error) {
	writeErrorWithCause(w, status, message, details, cause)
}

func WriteErrorWithLoggedID(w http.ResponseWriter, status int, message string, details map[string][]string, errorID string) {
	writeErrorWithLoggedID(w, status, message, details, errorID)
}

func ValidateRequest(req any) error {
	return validateRequest(req)
}

func UserIDFromContext(ctx context.Context) string {
	return userIDFromContext(ctx)
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, v)
}

func ValidateAgentRequest(req any, systemPrompt string, minChars, maxChars int) error {
	return validateAgentRequest(req, systemPrompt, minChars, maxChars)
}

func ValidateAgentInstruction(systemPrompt string, minChars, maxChars int) error {
	if msg := checkInstructionLength(systemPrompt, minChars, maxChars); msg != "" {
		return &ValidationError{Message: msg, Details: map[string][]string{"systemPrompt": {msg}}}
	}
	return nil
}

func ValidateAgentConfigurations(raw json.RawMessage) ([]string, error) {
	return ValidateAgentConfigurationsUpdate(raw, nil)
}

func ValidateAgentConfigurationsShape(raw json.RawMessage) ([]string, error) {
	if err := validateAdditionalConfigurations(raw); err != nil {
		return nil, err
	}
	return tier2WithOptionalBounds(raw, nil, false, false)
}

func ValidateAgentConfigurationsUpdate(raw json.RawMessage, storedFor func() (json.RawMessage, error)) ([]string, error) {
	if err := validateAdditionalConfigurations(raw); err != nil {
		return nil, err
	}
	var stored json.RawMessage
	if storedFor != nil {
		var err error
		if stored, err = storedFor(); err != nil {
			return nil, err
		}
	}
	return validateAdditionalConfigurationsTier2(raw, stored, false)
}

func ConnectorTypeHidden(reg *features.Registry, connType string) bool {
	key, gated := connectorTypeFeatureKeys[connType]
	if !gated {
		return false
	}
	return reg == nil || !reg.IsEnabled(key)
}

func ValidateConnectorTypeSubtype(connType, subtype string) error {
	return validateConnectorTypeSubtype(connType, subtype)
}

func OnField(err error, field string) error {
	return onField(err, field)
}

func hiddenConnectorTypes(reg *features.Registry) []string {
	hidden := make([]string, 0, len(connectorTypeFeatureKeys))
	for _, connType := range slices.Sorted(maps.Keys(connectorTypeFeatureKeys)) {
		if ConnectorTypeHidden(reg, connType) {
			hidden = append(hidden, connType)
		}
	}
	return hidden
}

func RejectUnsupportedEventMeshAuthoring(values map[string]any) error {
	return rejectUnsupportedEventMeshAuthoring(values)
}

func NormalizeEventMeshAuthoredValues(values map[string]any) (map[string]any, []string) {
	return normalizeEventMeshAuthoredValues(values)
}

func LogSecretsDiff(ctx context.Context, logger *slog.Logger, kind, id string, result map[string]SecretFieldDiffResult) {
	if logger == nil {
		return
	}
	outcomes := make(map[string]bool, len(result))
	for k, v := range result {
		outcomes[k] = v.Matches
	}
	logger.Info("secrets diff",
		"kind", kind,
		"resourceID", id,
		"userID", userIDFromContext(ctx),
		"fields", outcomes,
	)
}

func CompareModelSecrets(storedAuthType string, candidate, stored map[string]any) map[string]SecretFieldDiffResult {
	return compareSecrets(modelSecretFields(storedAuthType, candidate), candidate, stored)
}

type ModelProbe struct {
	Raw         map[string]any
	ModelParams map[string]any
	Provider    string
	ModelType   string
	ConfigID    string
}

func BuildModelProbe(ctx context.Context, svc *ModelConfigService, logger *slog.Logger, req *ModelConfigTestRequest) (ModelProbe, error) {
	if err := validateModelParamHeaders(req.ModelParams); err != nil {
		return ModelProbe{}, err
	}
	h := &modelConfigHandler{svc: svc, logger: logger}
	cfg, err := h.buildProbeConfig(ctx, req, probeAuthConfig(req))
	if err != nil {
		return ModelProbe{}, err
	}
	return ModelProbe{Raw: cfg.raw, ModelParams: cfg.modelParams, Provider: cfg.provider, ModelType: cfg.modelType, ConfigID: cfg.configID}, nil
}

func APIKeyReadGate(logger *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return requireSuperAdminToRead(logger, apiKeyReadRefusal, next)
}

var superAdminReadRefusal = readRefusal{
	logMessage: "super administrator read refused",
	message:    "This read requires a super administrator",
}

func SuperAdminReadGate(logger *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return requireSuperAdminToRead(logger, superAdminReadRefusal, next)
}

func BudgetAssignGate(next http.HandlerFunc) http.HandlerFunc {
	return requireScope(rbac.ScopeBudgetAssign, requireScope(rbac.ScopeBudgetRead, next))
}

func BudgetDeleteGate(next http.HandlerFunc) http.HandlerFunc {
	return requireScope(rbac.ScopeBudgetDelete, requireScope(rbac.ScopeBudgetAssign, next))
}

func LinkAgentRotateTokenGate(next http.HandlerFunc) http.HandlerFunc {
	return requireScope(rbac.ScopeAgentBuilderDeploy, requireScope(rbac.ScopeAgentBuilderUpdate, next))
}

func UsageReadGate(logger *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return requireScopeToRead(logger, rbac.ScopeReadUsage, spendReadRefusal, next)
}

func APIKeyWriteGate(logger *slog.Logger, next http.HandlerFunc) http.HandlerFunc {
	return newAPIKeyHandler(nil, logger).guardInteractiveSuperAdmin(next)
}

type AgentExtraConfigKey struct {
	Runtime string
	Fields  map[string]AgentExtraConfigKey
	Items   map[string]AgentExtraConfigKey
}

func AgentExtraConfigKeys() map[string]AgentExtraConfigKey {
	return exportExtraConfigKeys(agentExtraConfigSchema)
}

func exportExtraConfigKeys(schema map[string]agentExtraConfigKey) map[string]AgentExtraConfigKey {
	out := make(map[string]AgentExtraConfigKey, len(schema))
	for canonical, entry := range schema {
		key := AgentExtraConfigKey{Runtime: runtimeKeyOf(canonical, entry)}
		switch {
		case entry.Nested == nil:
		case entry.Type == extraTypeObject:
			key.Fields = exportExtraConfigKeys(entry.Nested)
		case entry.Type == extraTypeList:
			key.Items = exportExtraConfigKeys(entry.Nested)
		}
		out[canonical] = key
	}
	return out
}

func ReverseTranslateAgentExtraConfig(blob map[string]any) map[string]any {
	return reverseTranslateAgentExtraConfig(blob, agentExtraConfigSchema)
}

func EvalGenConfiguredGatewayURL(r *http.Request) (string, error) {
	return evalGenConfiguredGatewayURL(r)
}

type ErrorBody = errorEnvelope

type AuthError struct {
	Message   string `json:"message" example:"Authentication required"`
	ErrorID   string `json:"errorId" example:"6f1b8c1e-2c7a-4d0e-9a53-1d7f4c2b9e10"`
	ErrorType string `json:"errorType,omitempty" example:"authentication_required"`
}
