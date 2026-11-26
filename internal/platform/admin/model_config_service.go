// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const errGetModelConfigWrap = "get model config: %w"

const resourceModelConfig = "model configuration"

const (
	modelOutboxPrefix       = "model."
	modelOutboxEventCreated = modelOutboxPrefix + "created"
	modelOutboxEventUpdated = modelOutboxPrefix + "updated"
	modelOutboxEventDeleted = modelOutboxPrefix + "deleted"
)

type ModelConfigService struct {
	repo           *store.ModelConfigRepo
	outboxRepo     *store.OutboxRepo
	outboxNotifier platpkg.OutboxNotifier
	features       *features.Registry
	logger         *slog.Logger
	workflows      workflowModelRefs
	grants         llm.GrantTokens
	oauthGrants    platpkg.OAuthGrantReader
}

func (s *ModelConfigService) SetGrantTokens(t llm.GrantTokens) {
	s.grants = t
}

func (s *ModelConfigService) SetOAuthGrants(g platpkg.OAuthGrantReader) {
	s.oauthGrants = g
}

func (s *ModelConfigService) validateRuntimeConfig(ctx context.Context, mc *store.ModelConfiguration) error {
	raw := platpkg.ToRawLiteLLMConfig(mc)
	if err := platpkg.ValidateRuntimeModelConfig(raw); err != nil {
		return err
	}
	return platpkg.ValidateModelGrant(ctx, s.oauthGrants, raw)
}

func (s *ModelConfigService) GrantTokens() llm.GrantTokens {
	return s.grants
}

func (s *ModelConfigService) SetFeatureRegistry(r *features.Registry) {
	s.features = r
}

func (s *ModelConfigService) toResponse(ctx context.Context, mc *store.ModelConfiguration) ModelConfigResponse {
	resp := toModelConfigResponse(mc, true)
	if callerMayReadPricing(ctx) {
		price, err := platpkg.PricingResponse(mc)
		if err != nil {
			s.logger.Warn("stored model price is invalid; reading the model as unpriced", "modelID", mc.ID, "error", err)
		}
		resp.Pricing = price
	}
	return resp
}

func NewModelConfigService(repo *store.ModelConfigRepo, logger *slog.Logger) *ModelConfigService {
	return &ModelConfigService{repo: repo, logger: logger}
}

func (s *ModelConfigService) SetWorkflows(workflows *store.WorkflowRepo, deployments *store.WorkflowDeploymentRepo) {
	s.workflows = workflowModelRefs{workflows: workflows, deployments: deployments}
}

func (s *ModelConfigService) workflowReferenceConflict(ctx context.Context, alias string) error {
	if !s.workflows.enabled() {
		return nil
	}
	references, err := s.workflows.find(ctx, alias)
	if err != nil {
		return fmt.Errorf("check workflows referencing model %q: %w", alias, err)
	}
	if len(references) == 0 {
		return nil
	}
	return &ConflictError{Message: fmt.Sprintf("cannot rename model %q: %s.", alias, describeWorkflowReferences(references))}
}

func describeWorkflowReferences(references []workflowModelReference) string {
	var saved, deployedOnly []string
	savedAlsoDeployed := false
	for _, ref := range references {
		name := strconv.Quote(ref.workflow.Name)
		if ref.saved {
			saved = append(saved, name)
			savedAlsoDeployed = savedAlsoDeployed || ref.deployed
			continue
		}
		deployedOnly = append(deployedOnly, name+" (deployed version)")
	}
	var sentences []string
	if len(saved) > 0 {
		p := workflowPhraseFor(len(saved))
		redeploy := ""
		if savedAlsoDeployed {
			redeploy = " and redeploy " + p.object
		}
		sentences = append(sentences, fmt.Sprintf("the decision block of %s %s names it; point %s at another decision model%s first",
			p.noun, strings.Join(saved, ", "), p.pointer, redeploy))
	}
	if len(deployedOnly) > 0 {
		p := workflowPhraseFor(len(deployedOnly))
		sentences = append(sentences, fmt.Sprintf("%s %s still %s it; redeploy %s on another decision model first",
			p.noun, strings.Join(deployedOnly, ", "), p.verb, p.object))
	}
	for i := 1; i < len(sentences); i++ {
		sentences[i] = strings.ToUpper(sentences[i][:1]) + sentences[i][1:]
	}
	return strings.Join(sentences, ". ")
}

type workflowPhrase struct{ noun, pointer, object, verb string }

func workflowPhraseFor(n int) workflowPhrase {
	if n > 1 {
		return workflowPhrase{noun: "workflows", pointer: "those workflows", object: "them", verb: "name"}
	}
	return workflowPhrase{noun: "workflow", pointer: "that workflow", object: "it", verb: "names"}
}

func (s *ModelConfigService) WithOutbox(outboxRepo *store.OutboxRepo) {
	s.outboxRepo = outboxRepo
}

func (s *ModelConfigService) SetOutboxNotifier(n platpkg.OutboxNotifier) {
	s.outboxNotifier = n
}

type modelOutboxPayload struct {
	Alias         string `json:"alias"`
	PreviousAlias string `json:"previousAlias,omitempty"`
}

func (s *ModelConfigService) writeWithOutboxEvent(ctx context.Context, eventType string, mc *store.ModelConfiguration, previousAlias string, write func(tx *dbutil.Tx) error) error {
	if s.outboxRepo == nil {
		return s.repo.WithTx(ctx, write)
	}
	payload, err := json.Marshal(modelOutboxPayload{Alias: mc.Alias, PreviousAlias: previousAlias})
	if err != nil {
		return fmt.Errorf("marshal outbox payload %s: %w", eventType, err)
	}
	if err := s.repo.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := write(tx); err != nil {
			return err
		}
		if err := s.outboxRepo.CreateInTx(ctx, tx, &store.OutboxEvent{
			EventType:   eventType,
			AggregateID: mc.ID,
			Payload:     string(payload),
		}); err != nil {
			return fmt.Errorf("write outbox event %s: %w", eventType, err)
		}
		return nil
	}); err != nil {
		return err
	}
	if s.outboxNotifier != nil {
		s.outboxNotifier.Notify()
	}
	return nil
}

func (s *ModelConfigService) List(ctx context.Context) ([]ModelConfigResponse, error) {
	all, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list model configs: %w", err)
	}
	out := make([]ModelConfigResponse, len(all))
	for i, mc := range all {
		out[i] = s.toResponse(ctx, mc)
	}
	return out, nil
}

func (s *ModelConfigService) ListPage(ctx context.Context, offset, limit int, sortField string, desc bool) ([]*ModelConfigResponse, int, error) {
	rows, err := s.repo.GetPage(ctx, offset, limit, sortField, desc)
	if err != nil {
		return nil, 0, fmt.Errorf("list model configs page: %w", err)
	}
	total, err := s.repo.CountAll(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count model configs: %w", err)
	}
	out := make([]*ModelConfigResponse, len(rows))
	for i, row := range rows {
		resp := s.toResponse(ctx, row)
		out[i] = &resp
	}
	return out, total, nil
}

func (s *ModelConfigService) GetByAlias(ctx context.Context, alias string) (*ModelConfigResponse, error) {
	mc, err := s.repo.GetByAlias(ctx, alias)
	if err != nil {
		return nil, fmt.Errorf(errGetModelConfigWrap, err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: resourceModelConfig, ID: alias}
	}
	resp := s.toResponse(ctx, mc)
	return &resp, nil
}

func (s *ModelConfigService) GetByID(ctx context.Context, id string) (*ModelConfigResponse, error) {
	mc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetModelConfigWrap, err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: resourceModelConfig, ID: id}
	}
	resp := s.toResponse(ctx, mc)
	return &resp, nil
}

func (s *ModelConfigService) GetAuthConfigCleartext(ctx context.Context, id string) (string, map[string]any, error) {
	mc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return "", nil, fmt.Errorf(errGetModelConfigWrap, err)
	}
	if mc == nil {
		return "", nil, &NotFoundError{Resource: resourceModelConfig, ID: id}
	}
	return mc.ModelAuthType, parseJSONMap(mc.ModelAuthConfig), nil
}

func (s *ModelConfigService) GetByIDRaw(ctx context.Context, id string) (map[string]any, error) {
	mc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get model config raw: %w", err)
	}
	if !isConfigured(mc) {
		return nil, nil
	}
	return toRawLiteLLMConfig(mc), nil
}

func (s *ModelConfigService) GetByAliasRaw(ctx context.Context, alias string) (map[string]any, error) {
	mc, err := s.repo.GetByAlias(ctx, alias)
	if err != nil {
		return nil, fmt.Errorf("get model config raw: %w", err)
	}
	if !isConfigured(mc) {
		return nil, nil
	}
	return toRawLiteLLMConfig(mc), nil
}

func (s *ModelConfigService) ListAllRaw(ctx context.Context) (map[string]map[string]any, error) {
	all, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list model configs raw: %w", err)
	}
	out := make(map[string]map[string]any, len(all))
	for _, mc := range all {
		if mc == nil || mc.Alias == "" || !isConfigured(mc) {
			continue
		}
		out[mc.Alias] = toRawLiteLLMConfig(mc)
	}
	return out, nil
}

func (s *ModelConfigService) ListAllRawDualKey(ctx context.Context) (map[string]map[string]any, error) {
	all, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list model configs raw dual key: %w", err)
	}
	out := make(map[string]map[string]any, len(all)*2)
	for _, mc := range all {
		if mc == nil || mc.Alias == "" || !isConfigured(mc) {
			continue
		}
		raw := toRawLiteLLMConfig(mc)
		out[mc.Alias] = raw
		if mc.ID != "" {
			out[mc.ID] = raw
		}
	}
	return out, nil
}

func (s *ModelConfigService) GetByAliasOrIDRaw(ctx context.Context, aliasOrID string) (map[string]any, error) {
	mc, err := s.repo.GetByAliasOrID(ctx, aliasOrID)
	if err != nil {
		return nil, fmt.Errorf("get model config raw: %w", err)
	}
	if !isConfigured(mc) {
		return nil, nil
	}
	return toRawLiteLLMConfig(mc), nil
}

func (s *ModelConfigService) GetForSnapshot(ctx context.Context, aliasOrID string) (map[string]any, map[string]any, error) {
	mc, err := s.repo.GetByAliasOrID(ctx, aliasOrID)
	if err != nil {
		return nil, nil, fmt.Errorf("get model config snapshot: %w", err)
	}
	if !isConfigured(mc) {
		return nil, nil, nil
	}
	raw := toRawLiteLLMConfig(mc)
	resp := toModelConfigResponse(mc, true)
	var provider, modelName any
	if resp.Provider != nil {
		provider = *resp.Provider
	}
	if resp.ModelName != nil {
		modelName = *resp.ModelName
	}
	snapshot := map[string]any{
		"id":             resp.ID,
		"alias":          resp.Alias,
		"provider":       provider,
		"modelName":      modelName,
		"apiBase":        resp.APIBase,
		"modelParams":    resp.ModelParams,
		"maxInputTokens": resp.MaxInputTokens,
		"description":    resp.Description,
		"modelType":      resp.ModelType,
	}
	if snapshot["modelParams"] == nil {
		snapshot["modelParams"] = map[string]any{}
	}
	return raw, snapshot, nil
}

func (s *ModelConfigService) GetStatus(ctx context.Context) (*ModelConfigStatusResponse, error) {
	general, err := s.repo.GetByAlias(ctx, "general")
	if err != nil {
		return nil, fmt.Errorf("get status: %w", err)
	}
	planning, err := s.repo.GetByAlias(ctx, "planning")
	if err != nil {
		return nil, fmt.Errorf("get status: %w", err)
	}
	configured := isConfigured(general) && isConfigured(planning)
	return &ModelConfigStatusResponse{Configured: configured}, nil
}

func looksLikeUnresolvedRef(s string) bool {
	return strings.Contains(s, "${")
}

func isConfigured(mc *store.ModelConfiguration) bool {
	if mc == nil {
		return false
	}
	if strings.TrimSpace(mc.ModelName) == "" || mc.ModelName == placeholderValue || looksLikeUnresolvedRef(mc.ModelName) {
		return false
	}
	if strings.TrimSpace(mc.Provider) == "" || mc.Provider == placeholderValue {
		return false
	}
	if mc.APIBase == nil || strings.TrimSpace(*mc.APIBase) == "" {
		return false
	}
	return requireDecisionAPIKey(mc) == nil
}

func validateModelParamHeaders(modelParams map[string]any) error {
	if k, bad := samapp.ExtraHeadersCRLFKey(modelParams); bad {
		return &ValidationError{Message: fmt.Sprintf("extra_headers %q contains CR/LF — header injection blocked", k), Field: "modelParams"}
	}
	if _, named := modelParams[llm.GrantIDKey]; named {
		return &ValidationError{Message: platpkg.GrantIDInModelParamsMessage, Field: "modelParams"}
	}
	return nil
}

func (s *ModelConfigService) Create(ctx context.Context, req *CreateModelConfigRequest, userID string) (*ModelConfigResponse, error) {
	req.Alias = strings.ToLower(strings.TrimSpace(req.Alias))
	req.ModelName = strings.TrimSpace(req.ModelName)
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	if err := validateModelParamHeaders(req.ModelParams); err != nil {
		return nil, err
	}
	modelType, err := platpkg.ResolveModelType(req.ModelType, req.Provider)
	if err != nil {
		return nil, &ValidationError{Message: err.Error(), Field: "modelType"}
	}
	if flagErr := platpkg.RequireDecisionModelsEnabled(modelType, s.features); flagErr != nil {
		return nil, flagErr
	}

	exists, err := s.repo.ExistsByAlias(ctx, req.Alias)
	if err != nil {
		return nil, fmt.Errorf("check alias: %w", err)
	}
	if exists {
		return nil, &ConflictError{Message: fmt.Sprintf(resourceModelConfig+" with alias %q already exists", req.Alias)}
	}

	if strings.TrimSpace(req.APIBase) == "" {
		req.APIBase = platpkg.DefaultAPIBase(req.Provider, req.AuthConfig)
	}
	if err := platpkg.RequireAPIBase(req.APIBase); err != nil {
		return nil, err
	}
	apiBase := req.APIBase

	authType := "none"
	authConfigJSON := "{}"
	if len(req.AuthConfig) > 0 {
		if t, ok := req.AuthConfig["type"].(string); ok && t != "" {
			authType = t
		} else if req.AuthType != "" {
			authType = req.AuthType
			req.AuthConfig["type"] = req.AuthType
		}
		b, err := json.Marshal(req.AuthConfig)
		if err != nil {
			return nil, &ValidationError{Message: "invalid authConfig JSON", Field: "authConfig"}
		}
		authConfigJSON = string(b)
	} else if req.AuthType != "" {
		authType = req.AuthType
		authConfigJSON = fmt.Sprintf(`{"type":%q}`, req.AuthType)
	}

	modelParamsJSON := "{}"
	if len(req.ModelParams) > 0 {
		b, err := json.Marshal(req.ModelParams)
		if err != nil {
			return nil, &ValidationError{Message: "invalid modelParams JSON", Field: "modelParams"}
		}
		modelParamsJSON = string(b)
	}

	mc := &store.ModelConfiguration{
		Alias:           req.Alias,
		Provider:        req.Provider,
		ModelName:       req.ModelName,
		ModelAuthType:   authType,
		ModelAuthConfig: authConfigJSON,
		ModelParams:     modelParamsJSON,
		MaxInputTokens:  req.MaxInputTokens,
		ModelType:       modelType,
		CreatedBy:       userID,
		UpdatedBy:       userID,
	}
	if apiBase != "" {
		mc.APIBase = &apiBase
	}
	if req.Description != "" {
		mc.Description = &req.Description
	}
	if err := requireDecisionAPIKey(mc); err != nil {
		return nil, err
	}

	price, priceErr := platpkg.ParsePricing(req.Pricing)
	if priceErr != nil {
		return nil, priceErr
	}
	platpkg.ApplyPricing(mc, price)

	if err := s.validateRuntimeConfig(ctx, mc); err != nil {
		return nil, err
	}

	mc.ID = idgen.New()
	if err := s.writeWithOutboxEvent(ctx, modelOutboxEventCreated, mc, "", func(tx *dbutil.Tx) error {
		return s.repo.CreateInTx(ctx, tx, mc)
	}); err != nil {
		return nil, fmt.Errorf("create model config: %w", err)
	}

	resp := s.toResponse(ctx, mc)
	return &resp, nil
}

func (s *ModelConfigService) applyAliasChange(ctx context.Context, mc *store.ModelConfiguration, alias string) error {
	if alias != mc.Alias {
		exists, err := s.repo.ExistsByAlias(ctx, alias)
		if err != nil {
			return fmt.Errorf("check alias: %w", err)
		}
		if exists {
			return &ConflictError{Message: fmt.Sprintf(resourceModelConfig+" with alias %q already exists", alias)}
		}
		if err := s.workflowReferenceConflict(ctx, mc.Alias); err != nil {
			return err
		}
	}
	mc.Alias = alias
	return nil
}

func updatedAPIBase(mc *store.ModelConfiguration, req *UpdateModelConfigRequest) (string, error) {
	apiBase := strings.TrimSpace(*req.APIBase)
	if apiBase == "" {
		provider := mc.Provider
		if req.Provider != nil {
			provider = *req.Provider
		}
		authConfig := req.AuthConfig
		if authConfig == nil {
			authConfig = parseJSONMap(mc.ModelAuthConfig)
		}
		apiBase = platpkg.DefaultAPIBase(provider, authConfig)
	}
	if err := platpkg.RequireAPIBase(apiBase); err != nil {
		return "", err
	}
	return apiBase, nil
}

func applyAuthUpdate(mc *store.ModelConfiguration, req *UpdateModelConfigRequest, providerSwitched bool) error {
	existing := parseJSONMap(mc.ModelAuthConfig)
	if providerSwitched {
		existing = onlyAuthType(existing)
	}
	incoming := req.AuthConfig
	if incoming == nil {
		incoming = map[string]any{}
	}
	if req.AuthType != nil && *req.AuthType != "" {
		if _, sentInline := incoming["type"]; !sentInline {
			withType := make(map[string]any, len(incoming)+1)
			maps.Copy(withType, incoming)
			withType["type"] = *req.AuthType
			incoming = withType
		}
	}
	merged := mergeModelAuthConfig(existing, incoming)
	if t, ok := merged["type"].(string); ok && t != "" {
		mc.ModelAuthType = t
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return &ValidationError{Message: "invalid authConfig JSON", Field: "authConfig"}
	}
	mc.ModelAuthConfig = string(b)
	return nil
}

func (s *ModelConfigService) Update(ctx context.Context, modelID string, req *UpdateModelConfigRequest, userID string) (*ModelConfigResponse, error) {
	if req.Alias != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*req.Alias))
		req.Alias = &trimmed
	}
	if req.ModelName != nil {
		trimmed := strings.TrimSpace(*req.ModelName)
		req.ModelName = &trimmed
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	if err := validateModelParamHeaders(req.ModelParams); err != nil {
		return nil, err
	}

	mc, err := s.repo.GetByID(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("get model config for update: %w", err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: resourceModelConfig, ID: modelID}
	}

	storedAlias := mc.Alias

	if req.Alias != nil {
		if err := s.applyAliasChange(ctx, mc, *req.Alias); err != nil {
			return nil, err
		}
	}
	storedProvider := mc.Provider
	if req.Provider != nil {
		mc.Provider = *req.Provider
	}
	if err := platpkg.ValidateModelTypeUpdate(mc.ModelType, storedProvider, mc.Provider); err != nil {
		return nil, &ValidationError{Message: err.Error(), Field: "provider"}
	}
	if req.ModelName != nil {
		mc.ModelName = *req.ModelName
	}
	if req.APIBase != nil {
		apiBase, err := updatedAPIBase(mc, req)
		if err != nil {
			return nil, err
		}
		mc.APIBase = &apiBase
	}
	applyClearableFields(mc, req)
	if err := s.applyPricingUpdate(mc, req); err != nil {
		return nil, err
	}
	if req.AuthConfig != nil || req.AuthType != nil {
		if err := applyAuthUpdate(mc, req, switchesProvider(storedProvider, mc.Provider)); err != nil {
			return nil, err
		}
	}

	if req.ModelParams != nil {
		b, err := json.Marshal(req.ModelParams)
		if err != nil {
			return nil, &ValidationError{Message: "invalid modelParams JSON", Field: "modelParams"}
		}
		mc.ModelParams = string(b)
	}

	if err := requireDecisionAPIKey(mc); err != nil {
		return nil, err
	}

	if err := s.validateRuntimeConfig(ctx, mc); err != nil {
		return nil, err
	}

	mc.UpdatedBy = userID

	var previousAlias string
	if storedAlias != mc.Alias {
		previousAlias = storedAlias
	}
	if err := s.writeWithOutboxEvent(ctx, modelOutboxEventUpdated, mc, previousAlias, func(tx *dbutil.Tx) error {
		return s.repo.UpdateInTx(ctx, tx, mc)
	}); err != nil {
		return nil, fmt.Errorf("update model config: %w", err)
	}

	resp := s.toResponse(ctx, mc)
	return &resp, nil
}

func applyClearableFields(mc *store.ModelConfiguration, req *UpdateModelConfigRequest) {
	if req.Description != nil || req.DescriptionSet {
		mc.Description = req.Description
	}
	if req.MaxInputTokens != nil || req.MaxInputTokensSet {
		mc.MaxInputTokens = req.MaxInputTokens
	}
}

func (s *ModelConfigService) applyPricingUpdate(mc *store.ModelConfiguration, req *UpdateModelConfigRequest) error {
	if !req.PricingSet && req.Pricing == nil {
		return nil
	}
	price, err := platpkg.ParsePricing(req.Pricing)
	if err != nil {
		return err
	}
	platpkg.ApplyPricing(mc, price)
	return nil
}

func (s *ModelConfigService) isProtectedAlias(alias string) bool {
	return samapp.IsProtectedModelAlias(alias)
}

func (s *ModelConfigService) Delete(ctx context.Context, modelID string) error {
	mc, err := s.repo.GetByID(ctx, modelID)
	if err != nil {
		return fmt.Errorf("get model config for delete: %w", err)
	}
	if mc == nil {
		return &NotFoundError{Resource: resourceModelConfig, ID: modelID}
	}
	if s.isProtectedAlias(mc.Alias) {
		return &ValidationError{Message: fmt.Sprintf("cannot delete protected model alias %q", mc.Alias)}
	}
	if err := s.writeWithOutboxEvent(ctx, modelOutboxEventDeleted, mc, "", func(tx *dbutil.Tx) error {
		return s.repo.DeleteInTx(ctx, tx, modelID)
	}); err != nil {
		if err.Error() == "model configuration not found" {
			return &NotFoundError{Resource: resourceModelConfig, ID: modelID}
		}
		return fmt.Errorf("delete model config: %w", err)
	}
	return nil
}

func toModelConfigResponse(mc *store.ModelConfiguration, redact bool) ModelConfigResponse {
	authConfig := parseJSONMap(mc.ModelAuthConfig)
	if redact {
		authConfig = redactModelAuthConfig(authConfig)
	}
	modelParams := parseJSONMap(mc.ModelParams)

	return ModelConfigResponse{
		ID:             mc.ID,
		Alias:          mc.Alias,
		Provider:       stripPlaceholder(mc.Provider),
		ModelName:      stripPlaceholder(mc.ModelName),
		APIBase:        mc.APIBase,
		AuthType:       mc.ModelAuthType,
		AuthConfig:     authConfig,
		ModelParams:    modelParams,
		Description:    mc.Description,
		MaxInputTokens: mc.MaxInputTokens,
		ModelType:      mc.ModelType,
		CreatedBy:      mc.CreatedBy,
		UpdatedBy:      mc.UpdatedBy,
		CreatedTime:    Timestamp(mc.CreatedTime),
		UpdatedTime:    Timestamp(mc.UpdatedTime),
	}
}

func stripPlaceholder(s string) *string {
	if s == placeholderValue || looksLikeUnresolvedRef(s) {
		return nil
	}
	return &s
}

func toRawLiteLLMConfig(mc *store.ModelConfiguration) map[string]any {
	raw := map[string]any{
		"model": platpkg.ResolveLiteLLMModelName(mc.Provider, mc.ModelType, mc.ModelName),
	}
	if mc.APIBase != nil {
		raw["api_base"] = *mc.APIBase
	}

	platpkg.WriteAuthConfigToRaw(raw, parseJSONMap(mc.ModelAuthConfig))

	modelParams := parseJSONMap(mc.ModelParams)
	for k, v := range modelParams {
		raw[k] = v
	}
	samapp.WithModelParams(raw, modelParams)

	if mc.MaxInputTokens != nil {
		raw["max_input_tokens"] = *mc.MaxInputTokens
	}
	platpkg.AddModelTypeToRaw(raw, mc.ModelType, mc.Provider)
	raw[samapp.ModelConfigIDKey] = mc.ID

	return raw
}

func (s *ModelConfigService) GetModelsFromProviderByID(
	ctx context.Context,
	modelID string,
	listSvc *ModelListService,
	providerOverride string,
	authConfigOverrides map[string]any,
	apiBaseOverride string,
) ([]SupportedModel, error) {
	mc, err := s.repo.GetByID(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf(errGetModelConfigWrap, err)
	}
	if mc == nil {
		return nil, &NotFoundError{Resource: resourceModelConfig, ID: modelID}
	}

	storedProvider := mc.Provider
	storedAuthConfig := parseJSONMap(mc.ModelAuthConfig)
	storedAuthType, _ := storedAuthConfig["type"].(string)

	providerChanged := switchesProvider(storedProvider, providerOverride)

	if providerChanged {
		req := BuildSupportedModelsRequest(providerOverride, apiBaseOverride, authConfigOverrides)
		req.ModelType = mc.ModelType
		return listProviderModels(ctx, listSvc, req)
	}

	provider := cmp.Or(providerOverride, storedProvider)
	authConfig := storedAuthConfig
	apiBase := ""
	if mc.APIBase != nil {
		apiBase = *mc.APIBase
	}

	if len(authConfigOverrides) > 0 {
		overrideAuthType, _ := authConfigOverrides["type"].(string)
		if overrideAuthType == "" || overrideAuthType == storedAuthType {
			nonEmpty := make(map[string]any, len(authConfigOverrides))
			for k, v := range authConfigOverrides {
				if v == nil || v == "" {
					continue
				}
				nonEmpty[k] = v
			}
			authConfig = mergeModelAuthConfig(authConfig, nonEmpty)
		} else {
			authConfig = authConfigOverrides
		}
	}

	if apiBaseOverride != "" {
		apiBase = apiBaseOverride
	}
	if !platpkg.ValidAPIBaseStructure(apiBase) {
		apiBase = platpkg.DefaultAPIBase(provider, authConfig)
	}

	req := BuildSupportedModelsRequest(provider, apiBase, authConfig)
	req.ModelType = mc.ModelType
	return listProviderModels(ctx, listSvc, req)
}

type ProviderListError struct{ Err error }

func (e *ProviderListError) Error() string { return e.Err.Error() }
func (e *ProviderListError) Unwrap() error { return e.Err }

func listProviderModels(ctx context.Context, listSvc *ModelListService, req *SupportedModelsRequest) ([]SupportedModel, error) {
	models, err := listSvc.ListModels(ctx, req)
	if err != nil {
		return nil, &ProviderListError{Err: err}
	}
	return models, nil
}

func BuildSupportedModelsRequest(provider, apiBase string, authConfig map[string]any) *SupportedModelsRequest {
	req := &SupportedModelsRequest{
		Provider: provider,
		APIBase:  apiBase,
	}
	if authConfig == nil {
		return req
	}
	if v, ok := authConfig["api_key"].(string); ok {
		req.APIKey = v
	}
	if v, ok := authConfig["client_id"].(string); ok {
		req.ClientID = v
	}
	if v, ok := authConfig["client_secret"].(string); ok {
		req.ClientSecret = v
	}
	if v, ok := authConfig["token_url"].(string); ok {
		req.TokenURL = v
	}
	if v, ok := authConfig["aws_access_key_id"].(string); ok {
		req.AWSAccessKeyID = v
	}
	if v, ok := authConfig["aws_secret_access_key"].(string); ok {
		req.AWSSecretAccessKey = v
	}
	if v, ok := authConfig["aws_session_token"].(string); ok {
		req.AWSSessionToken = v
	}
	if v, ok := authConfig["aws_region_name"].(string); ok {
		req.AWSRegionName = v
	}
	if v, ok := authConfig["vertex_credentials"].(string); ok {
		req.VertexCredentials = v
	}
	if v, ok := authConfig["vertex_location"].(string); ok {
		req.VertexLocation = v
	}
	return req
}

func parseJSONMap(s string) map[string]any {
	if s == "" || s == "{}" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{}
	}
	return m
}
