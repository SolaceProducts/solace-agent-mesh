// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/llmtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type ModelTestConnectionRequest struct {
	ModelID     string         `json:"modelId" validate:"omitempty,uuid" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Provider    string         `json:"provider" example:"openai"`
	ModelName   string         `json:"modelName" example:"gpt-4o"`
	APIBase     string         `json:"apiBase" example:"https://api.openai.com/v1"`
	AuthType    string         `json:"authType" example:"apikey"`
	AuthConfig  map[string]any `json:"authConfig" example:"{\"type\":\"apikey\",\"api_key\":\"<your provider key>\"}"`
	ModelParams map[string]any `json:"modelParams" example:"{\"temperature\":0.2}"`
	ModelType   string         `json:"modelType" validate:"omitempty,oneof=chat decision" example:"chat"`
	Surface     string         `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

type ModelTestConnectionResult struct {
	Success    bool    `json:"success" example:"false"`
	Message    *string `json:"message" example:"The provider rejected the credentials. Check the credential fields and test again."`
	ErrorClass *string `json:"errorClass" example:"auth_credentials_invalid"`
}

type SupportedModel struct {
	Type     string `json:"type" example:"supportedModel"`
	Key      string `json:"key" example:"gpt-4o"`
	Label    string `json:"label" example:"gpt-4o"`
	Provider string `json:"provider" example:"openai"`
}

type ProviderParam struct {
	Type string `json:"type" example:"providerParam"`
	Key  string `json:"key" example:"temperature"`
}

type SupportedModelsRequest struct {
	Provider   string         `json:"provider" validate:"required,min=1,max=50,safename" example:"openai"`
	ModelID    string         `json:"modelId" validate:"omitempty,uuid" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	APIBase    string         `json:"apiBase" validate:"max=2048" example:"https://api.openai.com/v1"`
	AuthConfig map[string]any `json:"authConfig" example:"{\"type\":\"apikey\",\"api_key\":\"<your provider key>\"}"`
	ModelType  string         `json:"modelType" validate:"omitempty,oneof=chat decision" example:"chat"`
	Page       Page           `json:"-"`
}

type ProviderModelsRequest struct {
	Provider   string         `path:"provider,foldedkey" json:"-"`
	ModelID    string         `json:"modelId" validate:"omitempty,uuid" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	APIBase    string         `json:"apiBase" validate:"max=2048" example:"https://api.openai.com/v1"`
	AuthConfig map[string]any `json:"authConfig" example:"{\"type\":\"apikey\",\"api_key\":\"<your provider key>\"}"`
	ModelType  string         `json:"modelType" validate:"omitempty,oneof=chat decision" example:"chat"`
	Page       Page           `json:"-"`
}

type ProviderParamsRequest struct {
	Provider string `path:"provider,foldedkey" json:"-"`
	Page     Page   `json:"-"`
}

type ModelSecretsDiffRequest struct {
	ID         string         `path:"id" json:"-" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	AuthConfig map[string]any `json:"authConfig" validate:"required" example:"{\"api_key\":\"<REDACTED>\"}"`
}

type ModelSecretsDiffResult struct {
	Secrets map[string]SecretMatch `json:"secrets" example:"{\"api_key\":{\"matches\":true}}"`
}

const (
	classAuthCredentialsInvalid = "auth_credentials_invalid"
	classAuthPermissionDenied   = "auth_permission_denied"
	classModelNotFound          = "model_not_found"
	classRequestRejected        = "request_rejected"
	classRateLimited            = "rate_limited"
	classTimeout                = "timeout"
	classNetworkUnreachable     = "network_unreachable"
	classProviderError          = "provider_error"
	classConfigIncomplete       = "config_incomplete"
	classNoResponse             = "no_response"
	classInvalidResponse        = "invalid_response"
	classUnknown                = "unknown"
)

var probeMessages = map[string]string{
	classAuthCredentialsInvalid: "The provider rejected the credentials. Check the credential fields and test again.",
	classAuthPermissionDenied:   "The credentials were accepted, but this model is not available to them. Check with the provider what the credentials are entitled to, including any account quota or credit.",
	classModelNotFound:          "The endpoint serves no model by this name. Check the model name and the API base.",
	classRequestRejected:        "The provider refused the request SAM sent. Check the model name and the generation parameters.",
	classRateLimited:            "The provider is rate limiting these credentials. Wait a moment and test again.",
	classTimeout:                "The provider did not answer in time. Check the API base, then test again.",
	classNetworkUnreachable:     "SAM could not reach the provider's endpoint. Check the API base, and that this installation can reach it.",
	classProviderError:          "The provider reported a fault of its own. The configuration may be fine; test again shortly.",
	classConfigIncomplete:       "SAM could not build a complete request from this configuration. Check the provider, model name and credential fields.",
	classNoResponse:             "The model answered with nothing. Check that the model name names a chat model.",
	classInvalidResponse:        "The model answered, but not in the form a decision model must answer in. Check that the model name names a model that supports structured output.",
	classUnknown:                "The connection test failed for a reason SAM could not identify. The cause is in the platform log.",
}

const (
	bifrostConnectionFailed = "provider_connection_failed"
	bifrostRequestTimedOut  = "request_timed_out"
	bifrostNoKeyForModel    = "no_key_supports_model"
)

func classifyProbeError(err error) (class string, message string) {
	class = probeErrorClass(err)
	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		if cause, fix, ok := llmErr.AzureSetupProblem(); ok {
			return class, cause + ". " + fix
		}
	}
	return class, probeMessages[class]
}

func probeErrorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, llmtest.ErrNoResponse):
		return classNoResponse
	case errors.Is(err, llmtest.ErrNoConfig), errors.Is(err, llmtest.ErrClientInit), errors.Is(err, llmtest.ErrInvalidConfig):
		return classConfigIncomplete
	case errors.Is(err, context.DeadlineExceeded):
		return classTimeout
	}
	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		return llmErrorClass(llmErr)
	}
	if decisionErr, ok := errors.AsType[*decision.Error](err); ok {
		return decisionErrorClass(decisionErr)
	}
	return classUnknown
}

func llmErrorClass(err *llm.Error) string {
	switch err.Type {
	case llm.ErrorTypeAzureSignIn:
		return azureSignInClass(err.Code)
	case bifrostConnectionFailed:
		return classNetworkUnreachable
	case bifrostRequestTimedOut:
		return classTimeout
	case bifrostNoKeyForModel:
		return classConfigIncomplete
	}
	return upstreamStatusClass(err.StatusCode)
}

func upstreamStatusClass(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return classAuthCredentialsInvalid
	case http.StatusForbidden, http.StatusPaymentRequired:
		return classAuthPermissionDenied
	case http.StatusNotFound:
		return classModelNotFound
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return classTimeout
	case http.StatusTooManyRequests:
		return classRateLimited
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return classRequestRejected
	}
	if status >= http.StatusInternalServerError {
		return classProviderError
	}
	return classUnknown
}

func azureSignInClass(code string) string {
	switch code {
	case llm.SignInEntraUnreachable:
		return classNetworkUnreachable
	case llm.SignInTokenFileUnreadable:
		return classConfigIncomplete
	}
	return classAuthCredentialsInvalid
}

func decisionOutOfCredit(err *decision.Error) bool {
	return err.Kind == decision.FailureConfig && err.Status == http.StatusTooManyRequests
}

func decisionStatusClass(err *decision.Error) string {
	if decisionOutOfCredit(err) {
		return classAuthPermissionDenied
	}
	switch err.Kind {
	case decision.FailureUnauthorized:
		if err.Status == http.StatusForbidden {
			return classAuthPermissionDenied
		}
	case decision.FailureConfig, decision.FailureUnreachable, decision.FailureRateLimited:
		return statusClassOrEmpty(err.Status)
	}
	return ""
}

func statusClassOrEmpty(status int) string {
	if class := upstreamStatusClass(status); class != classUnknown {
		return class
	}
	return ""
}

func decisionErrorClass(err *decision.Error) string {
	if class := decisionStatusClass(err); class != "" {
		return class
	}
	switch err.Kind {
	case decision.FailureUnauthorized:
		return classAuthCredentialsInvalid
	case decision.FailureRateLimited:
		return classRateLimited
	case decision.FailureTimeout:
		return classTimeout
	case decision.FailureUnreachable:
		return classNetworkUnreachable
	case decision.FailureConfig:
		return classConfigIncomplete
	case decision.FailureInputTooLarge:
		return classRequestRejected
	case decision.FailureInvalidResponse, decision.FailureRefused:
		return classInvalidResponse
	}
	return classUnknown
}

func probeRequestProblems(req ModelTestConnectionRequest) map[string][]string {
	if req.ModelID != "" {
		return nil
	}
	const without = "required when modelId is not sent"
	problems := map[string][]string{}
	if req.Provider == "" {
		problems["provider"] = []string{without}
	}
	if req.ModelName == "" {
		problems["modelName"] = []string{without}
	}
	if probeAuthType(req) == "" {
		problems["authConfig.type"] = []string{without + "; send it, or send authType"}
	}
	if len(problems) == 0 {
		return nil
	}
	return problems
}

func probeAuthType(req ModelTestConnectionRequest) string {
	if t, ok := req.AuthConfig["type"].(string); ok && t != "" {
		return t
	}
	return req.AuthType
}

const msgUnknownProvider = "SAM does not support an LLM provider by that name"

const failReadModel = "Unable to read model"

const failListModels = "Unable to list models from the provider"

type AzureWorkloadIdentity struct {
	Type           string   `json:"type" example:"azureWorkloadIdentity"`
	Issuer         string   `json:"issuer" example:"https://oidc.eks.ca-central-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE"`
	Namespace      string   `json:"namespace" example:"sam"`
	ServiceAccount string   `json:"serviceAccount" example:"solace-agent-mesh"`
	Audiences      []string `json:"audiences" example:"[\"api://AzureADTokenExchange\"]"`
}

const azureWorkloadIdentityType = "azureWorkloadIdentity"

const msgAzureWorkloadIdentityOff = platpkg.AuthTypeAzureWorkloadIdentity + " auth is not enabled on this deployment"

const failReadAzureWorkloadIdentity = "Unable to read the service account token"

type modelLookupHandler struct {
	svc    *entplatform.ModelConfigService
	models *entplatform.ModelListService
	logger *slog.Logger
	spend  *spend.Publisher
	gate   *budget.Gate
}

func modelLookupRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) []Route {
	if deps.ModelConfigSvc == nil {
		return nil
	}
	h := &modelLookupHandler{svc: deps.ModelConfigSvc, models: deps.ModelListSvc, logger: logger, spend: core.SpendPublisher(), gate: core.BudgetGate()}
	routes := []Route{
		{Method: http.MethodPut, Path: "/models/testConnection",
			AnyOfScopes: []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate},
			Stability:   StabilityDraft,
			Doc: Doc{ID: "testModelConnection", Summary: "Validate a model's connection",
				Text: "Sends one short request to the provider, with the configuration sent or a saved model's, and reports whether the model answered. A chat model gets a completion request, and a decision model a single structured question. A failed probe returns 200 with success false, a message from Agent Mesh, and the kind of failure. The provider's own error text is never returned. If modelId names no model, the request fails with 404. While the mesh's usage budget or the caller's own is spent, the request fails with 429 with Retry-After, and nothing is sent to the provider.",
				Tag:  modelsTag, Errors: []int{http.StatusNotFound, http.StatusTooManyRequests}},
			Handler: Handle(h.testConnection, "Unable to test the model connection")},
		{Method: http.MethodPut, Path: "/models/{id}/secretsDiff", Scope: rbac.ScopeModelConfigUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "compareModelSecrets", Summary: "Validate values against a model's stored secrets",
				Text: "Returns, for each secret credential field of the model's auth type, whether saving the values sent would keep the stored secret. Secret values are never returned.", Tag: modelsTag},
			Handler: Handle(h.secretsDiff, "Unable to compare model secrets")},
		{Method: http.MethodGet, Path: "/models/azureWorkloadIdentity",
			AnyOfScopes: []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate},
			Stability:   StabilityDraft,
			Doc: Doc{ID: "getAzureWorkloadIdentity", Summary: "Get the Azure identity this deployment signs in as",
				Text: "Returns the issuer, namespace, service account, and audiences that a federated credential on your Entra identity must match for azure_workload_identity auth. If that auth type is not enabled, the request fails with 404.",
				Tag:  modelsTag, Errors: []int{http.StatusNotFound}},
			Handler: Handle(h.azureWorkloadIdentity, failReadAzureWorkloadIdentity)},
	}
	if h.models == nil {
		return routes
	}
	return append(routes, []Route{
		{Method: http.MethodPut, Path: "/supportedModels",
			AnyOfScopes: []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate},
			Stability:   StabilityDraft,
			Doc: Doc{ID: "listSupportedModels", Summary: "Get a list of the models a provider serves, with the credentials sent",
				Text: "Asks the provider, with the credentials sent, which models it serves, and returns one page of them. If Agent Mesh does not support the provider or modelId names no model, the request fails with 404. If the provider could not be asked or refused the credentials, the request fails with 502. The provider's own error text is never returned.",
				Tag:  supportedModelsTag, Errors: []int{http.StatusNotFound, http.StatusBadGateway}},
			Handler: Handle(h.supportedModels, failListModels)},
		{Method: http.MethodPut, Path: "/providers/{provider}/models",
			AnyOfScopes: []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate},
			Stability:   StabilityDraft,
			Doc: Doc{ID: "listProviderModels", Summary: "Get a list of the models a provider serves, with a saved model's stored credentials",
				Text: "Asks the provider which models it serves, and returns one page of them. With modelId, it uses that saved model's stored credentials with the authConfig sent laid over them. Without modelId, it uses the authConfig sent, which must carry a type. If Agent Mesh does not support the provider or modelId names no model, the request fails with 404. If the provider could not be asked or refused the credentials, the request fails with 502. The provider's own error text is never returned.",
				Tag:  providersTag, Errors: []int{http.StatusNotFound, http.StatusBadGateway}},
			Handler: Handle(h.providerModels, failListModels)},
		{Method: http.MethodPut, Path: "/providers/{provider}/params",
			AnyOfScopes: []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate},
			Stability:   StabilityDraft,
			Doc: Doc{ID: "listProviderParams", Summary: "Get a list of the generation parameters a provider's models accept",
				Text: "Returns one page of the generation parameters, such as temperature, that a model's modelParams can set for the provider. Agent Mesh holds the list, so no request is made to the provider. If Agent Mesh does not support the provider, the request fails with 404.",
				Tag:  providersTag, Errors: []int{http.StatusNotFound}},
			Handler: Handle(h.providerParams, "Unable to list provider parameters")},
	}...)
}

func stableSubject(ctx context.Context) string {
	if identity, ok := auth.SubjectFromContext(ctx); ok && identity != nil {
		return identity.UserID()
	}
	return ""
}

func listingFailure(provider string, err error) error {
	if IsErr[*apierr.NotFoundError](err) {
		return &CallerError{Status: http.StatusNotFound, Message: failReadModel, Cause: err}
	}
	if status, _ := ClassifyServiceError(err); status != http.StatusInternalServerError {
		return err
	}
	if !IsErr[*entplatform.ProviderListError](err) {
		return err
	}
	return &CallerError{Status: http.StatusBadGateway, Message: failListModels, Cause: withheldUpstreamCause{provider: provider, err: err}}
}

const maxCauseChain = 8

func errorTypeChain(err error) string {
	return strings.Join(appendErrorTypes(nil, err), " > ")
}

func appendErrorTypes(types []string, err error) []string {
	if err == nil || len(types) >= maxCauseChain {
		return types
	}
	types = append(types, reflect.TypeOf(err).String())
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		for _, branch := range e.Unwrap() {
			types = appendErrorTypes(types, branch)
		}
		return types
	case interface{ Unwrap() error }:
		return appendErrorTypes(types, e.Unwrap())
	}
	return types
}

type withheldUpstreamCause struct {
	provider string
	err      error
}

func (c withheldUpstreamCause) Error() string {
	return "the " + c.provider + " listing call failed; its text is withheld because the provider's response can carry the submitted credential"
}

func (c withheldUpstreamCause) Unwrap() error { return c.err }

func (h *modelLookupHandler) provider(raw string) (string, error) {
	if err := h.models.ValidateProvider(raw); err != nil {
		return "", &CallerError{Status: http.StatusNotFound, Message: msgUnknownProvider, Cause: err}
	}
	return strings.ToLower(raw), nil
}

func toSupportedModels(models []entplatform.SupportedModel, p Page) List[SupportedModel] {
	items := make([]SupportedModel, len(models))
	for i, m := range models {
		items[i] = SupportedModel{Type: "supportedModel", Key: m.ID, Label: m.Label, Provider: m.Provider}
	}
	return PageOf(items, p)
}

func (h *modelLookupHandler) supportedModels(ctx context.Context, req SupportedModelsRequest) (List[SupportedModel], error) {
	provider, err := h.provider(req.Provider)
	if err != nil {
		return List[SupportedModel]{}, err
	}
	listReq := entplatform.BuildSupportedModelsRequest(provider, req.APIBase, req.AuthConfig)
	listReq.ModelType = req.ModelType
	if req.ModelID != "" {
		var saved *entplatform.ModelConfigResponse
		saved, err = h.svc.GetByID(ctx, req.ModelID)
		if err != nil {
			return List[SupportedModel]{}, listingFailure(provider, err)
		}
		if listReq.APIBase == "" && saved.APIBase != nil {
			listReq.APIBase = *saved.APIBase
		}
		listReq.ModelType = saved.ModelType
	}
	models, err := h.models.ListModels(ctx, listReq)
	if err != nil {
		return List[SupportedModel]{}, listingFailure(provider, &entplatform.ProviderListError{Err: err})
	}
	return toSupportedModels(models, req.Page), nil
}

func (h *modelLookupHandler) providerModels(ctx context.Context, req ProviderModelsRequest) (List[SupportedModel], error) {
	provider, err := h.provider(req.Provider)
	if err != nil {
		return List[SupportedModel]{}, err
	}
	var models []entplatform.SupportedModel
	if req.ModelID != "" {
		models, err = h.svc.GetModelsFromProviderByID(ctx, req.ModelID, h.models, provider, withoutRedactedValues(req.AuthConfig), req.APIBase)
	} else {
		if authType, _ := req.AuthConfig["type"].(string); authType == "" {
			return List[SupportedModel]{}, &CallerError{
				Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
				Details: map[string][]string{"authConfig.type": {"required when modelId is not sent"}},
			}
		}
		listReq := entplatform.BuildSupportedModelsRequest(provider, req.APIBase, withoutRedactedValues(req.AuthConfig))
		listReq.ModelType = req.ModelType
		models, err = h.models.ListModels(ctx, listReq)
		if err != nil {
			err = &entplatform.ProviderListError{Err: err}
		}
	}
	if err != nil {
		return List[SupportedModel]{}, listingFailure(provider, err)
	}
	return toSupportedModels(models, req.Page), nil
}

func (h *modelLookupHandler) providerParams(ctx context.Context, req ProviderParamsRequest) (List[ProviderParam], error) {
	provider, err := h.provider(req.Provider)
	if err != nil {
		return List[ProviderParam]{}, err
	}
	keys := h.models.GetSupportedParams(ctx, provider, "")
	items := make([]ProviderParam, len(keys))
	for i, key := range keys {
		items[i] = ProviderParam{Type: "providerParam", Key: key}
	}
	return PageOf(items, req.Page), nil
}

func (h *modelLookupHandler) azureWorkloadIdentity(_ context.Context, _ struct{}) (AzureWorkloadIdentity, error) {
	if !llm.AzureWorkloadIdentityAvailable() {
		return AzureWorkloadIdentity{}, &CallerError{Status: http.StatusNotFound, Message: msgAzureWorkloadIdentityOff}
	}
	subject, err := llm.ReadAzureWorkloadIdentitySubject()
	if err != nil {
		return AzureWorkloadIdentity{}, fmt.Errorf("read azure workload identity subject: %w", err)
	}
	return AzureWorkloadIdentity{
		Type:           azureWorkloadIdentityType,
		Issuer:         subject.Issuer,
		Namespace:      subject.Namespace,
		ServiceAccount: subject.ServiceAccount,
		Audiences:      nonNilList(subject.Audiences),
	}, nil
}

func (h *modelLookupHandler) secretsDiff(ctx context.Context, req ModelSecretsDiffRequest) (ModelSecretsDiffResult, error) {
	storedAuthType, stored, err := h.svc.GetAuthConfigCleartext(ctx, req.ID)
	if err != nil {
		return ModelSecretsDiffResult{}, err
	}
	diff := entplatform.CompareModelSecrets(storedAuthType, req.AuthConfig, stored)
	entplatform.LogSecretsDiff(ctx, h.logger, "model", req.ID, diff)
	result := ModelSecretsDiffResult{Secrets: make(map[string]SecretMatch, len(diff))}
	for field, d := range diff {
		result.Secrets[field] = SecretMatch{Matches: d.Matches}
	}
	return result, nil
}

func (h *modelLookupHandler) testConnection(ctx context.Context, req ModelTestConnectionRequest) (ModelTestConnectionResult, error) {
	if problems := probeRequestProblems(req); problems != nil {
		return ModelTestConnectionResult{}, &CallerError{
			Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: problems,
		}
	}
	probe, err := entplatform.BuildModelProbe(ctx, h.svc, h.logger, &entplatform.ModelConfigTestRequest{
		ModelID:     req.ModelID,
		Provider:    req.Provider,
		ModelName:   req.ModelName,
		APIBase:     req.APIBase,
		AuthType:    req.AuthType,
		AuthConfig:  withoutRedactedValues(req.AuthConfig),
		ModelParams: req.ModelParams,
		ModelType:   req.ModelType,
	})
	if err != nil {
		return ModelTestConnectionResult{}, err
	}
	if validationErr := platpkg.ValidateRuntimeModelConfig(probe.Raw); validationErr != nil {
		if validErr, ok := errors.AsType[*platpkg.ValidationError](validationErr); ok {
			class, message := classConfigIncomplete, validErr.Message
			return ModelTestConnectionResult{Success: false, ErrorClass: &class, Message: &message}, nil
		}
		return ModelTestConnectionResult{}, validationErr
	}
	probeErr := h.runProbe(ctx, probe, req.Surface)
	if text := entrypoint.BudgetRefusalText(probeErr); text != "" {
		return ModelTestConnectionResult{}, &CallerError{Status: http.StatusTooManyRequests, Message: text, Cause: probeErr}
	}
	return h.result(ctx, probe, probeErr), nil
}

func (h *modelLookupHandler) probeSpend(ctx context.Context, probe entplatform.ModelProbe, clientSurface string) *llmtest.SpendRecording {
	return &llmtest.SpendRecording{
		Publisher: h.spend,
		Gate:      h.gate,
		Attribution: entplatform.ModelTestSpendAttribution(ctx, h.logger,
			httputil.ParseClientSurface(clientSurface), probe.ConfigID),
	}
}

func (h *modelLookupHandler) runProbe(ctx context.Context, probe entplatform.ModelProbe, clientSurface string) error {
	rec := h.probeSpend(ctx, probe, clientSurface)
	if probe.ModelType == platpkg.ModelTypeDecision {
		return llmtest.ProbeDecisionError(ctx, probe.Provider, probe.Raw, http.DefaultClient, h.logger, rec)
	}
	return llmtest.ProbeError(ctx, probe.Raw, h.logger, rec)
}

func (h *modelLookupHandler) result(ctx context.Context, probe entplatform.ModelProbe, err error) ModelTestConnectionResult {
	if err == nil {
		return ModelTestConnectionResult{Success: true}
	}
	class, message := classifyProbeError(err)
	h.logProbeFailure(ctx, probe, class, err)
	return ModelTestConnectionResult{Success: false, Message: &message, ErrorClass: &class}
}

func (h *modelLookupHandler) logProbeFailure(ctx context.Context, probe entplatform.ModelProbe, class string, err error) {
	fields := []any{
		"errorClass", class,
		"provider", llm.ProviderLabel(probe.Provider),
		"modelType", probe.ModelType,
		"userID", stableSubject(ctx),
		"causeChain", errorTypeChain(err),
	}
	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		fields = append(fields, "upstreamStatus", llmErr.StatusCode, "errorType", llmErr.Type, "errorCode", llmErr.Code)
	}
	if decisionErr, ok := errors.AsType[*decision.Error](err); ok {
		fields = append(fields, "failureKind", string(decisionErr.Kind))
	}
	h.logger.Warn("model connection test failed", fields...)
}
