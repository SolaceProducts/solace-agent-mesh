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
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const modelByIDPath = "/models/{id}"

const signedInReasonModelCatalogRead = "model catalog read: chat, the agent builder and evaluations list models for non-admin users; authConfig secrets are redacted in the service, but modelParams is returned as stored, so a credential kept there (such as in extra_headers) is readable"

type Model struct {
	ID             string         `json:"id" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Type           string         `json:"type" example:"model"`
	Alias          string         `json:"alias" example:"support-chat"`
	Provider       *string        `json:"provider" example:"openai"`
	ModelName      *string        `json:"modelName" example:"gpt-4o"`
	APIBase        *string        `json:"apiBase" example:"https://api.openai.com/v1"`
	AuthType       string         `json:"authType" example:"apikey"`
	AuthConfig     map[string]any `json:"authConfig" example:"{\"type\":\"apikey\",\"api_key\":\"<REDACTED>\"}"`
	ModelParams    map[string]any `json:"modelParams" example:"{\"temperature\":0.2}"`
	ModelType      string         `json:"modelType" example:"chat"`
	Description    *string        `json:"description" example:"Fast model for support replies"`
	MaxInputTokens *int           `json:"maxInputTokens" example:"128000"`
	Pricing        *Pricing       `json:"pricing"`
	CreatedBy      *string        `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy      *string        `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime    Instant        `json:"createdTime"`
	UpdatedTime    Instant        `json:"updatedTime"`
}

type Pricing struct {
	Input      *string `json:"input" example:"2.5"`
	Output     *string `json:"output" example:"10"`
	CacheRead  *string `json:"cacheRead" example:"0.25"`
	CacheWrite *string `json:"cacheWrite" example:"3.125"`
}

type ModelStatus struct {
	Configured bool `json:"configured" example:"true"`
}

type ModelDependent struct {
	Type             string `json:"type" example:"modelDependent"`
	Key              string `json:"key" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Name             string `json:"name" example:"Support agent"`
	DependentType    string `json:"dependentType" example:"agent"`
	DeploymentStatus string `json:"deploymentStatus" example:"deployed"`
	MayStillRun      bool   `json:"mayStillRun" example:"true"`
}

type CreateModelRequest struct {
	Alias          string         `json:"alias" validate:"required,min=1,max=100,safename" example:"support-chat"`
	Provider       string         `json:"provider" validate:"required,min=1,max=50,safename" example:"openai"`
	ModelName      string         `json:"modelName" validate:"required,min=1,max=255,safename" example:"gpt-4o"`
	APIBase        string         `json:"apiBase" validate:"max=2048" example:"https://api.openai.com/v1"`
	AuthType       string         `json:"authType" example:"apikey"`
	AuthConfig     map[string]any `json:"authConfig" example:"{\"type\":\"apikey\",\"api_key\":\"<your provider key>\"}"`
	ModelParams    map[string]any `json:"modelParams" example:"{\"temperature\":0.2}"`
	ModelType      string         `json:"modelType" validate:"omitempty,oneof=chat decision" example:"chat"`
	Description    string         `json:"description" example:"Fast model for support replies"`
	MaxInputTokens *int           `json:"maxInputTokens" validate:"omitempty,min=1" example:"128000"`
	Pricing        *Pricing       `json:"pricing"`
	Surface        string         `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

func (r CreateModelRequest) CrossFieldProblems() map[string][]string {
	problems := redactedValueDetails(r.AuthConfig)
	for field, found := range authTypeProblems(r.AuthConfig, r.AuthType) {
		problems[field] = append(problems[field], found...)
	}
	return problems
}

type UpdateModelRequest struct {
	ID             string                `path:"id" json:"-"`
	Alias          Patch[string]         `json:"alias" example:"support-chat"`
	Provider       Patch[string]         `json:"provider" example:"openai"`
	ModelName      Patch[string]         `json:"modelName" example:"gpt-4o"`
	APIBase        Patch[string]         `json:"apiBase" example:"https://api.openai.com/v1"`
	AuthType       Patch[string]         `json:"authType" example:"apikey"`
	AuthConfig     Patch[map[string]any] `json:"authConfig" example:"{\"api_key\":\"<your provider key>\"}"`
	ModelParams    Patch[map[string]any] `json:"modelParams" example:"{\"temperature\":0.2}"`
	Description    Patch[string]         `json:"description" clearable:"true" example:"Fast model for support replies"`
	MaxInputTokens Patch[int]            `json:"maxInputTokens" clearable:"true" example:"128000"`
	Pricing        Patch[map[string]any] `json:"pricing" clearable:"true" example:"{\"input\":\"2.5\",\"output\":\"10\"}"`
	ModelType      Patch[string]         `json:"modelType" example:"chat"`
	Surface        string                `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

type listModelsRequest struct {
	Page Page      `json:"-"`
	Sort SortOrder `sort:"alias,createdTime" json:"-"`
}

type listModelDependentsRequest struct {
	ID   string    `path:"id" json:"-"`
	Page Page      `json:"-"`
	Sort SortOrder `sort:"name" json:"-"`
}

type modelHandler struct {
	svc        *entplatform.ModelConfigService
	dependents entplatform.ModelDependentsService
	analytics  *platpkg.ResourceReporter
}

func modelRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, _ *slog.Logger) []Route {
	if deps.ModelConfigSvc == nil || deps.ModelDependentsSvc == nil {
		return nil
	}
	h := &modelHandler{svc: deps.ModelConfigSvc, dependents: deps.ModelDependentsSvc, analytics: core.ResourceAnalytics()}
	return []Route{
		{Method: http.MethodGet, Path: "/models", Gate: GateSignedIn, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listModels", Summary: "Get a list of models",
				Text: "Returns one page of the models agents and workflows can run on. Secret credential values are never returned.", Tag: modelsTag},
			Handler: Handle(h.list, "Unable to list models")},
		{Method: http.MethodPost, Path: "/models", Scope: rbac.ScopeModelConfigCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createModel", Summary: "Create a model",
				Text: "Creates a model configuration and returns it. If another model already has the alias, the request fails with 409.", Tag: modelsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.create, "Unable to create model")},
		{Method: http.MethodGet, Path: modelByIDPath, Gate: GateSignedIn, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getModel", Summary: "Get a model",
				Text: "Returns one model configuration. Secret credential values are never returned.", Tag: modelsTag},
			Handler: Handle(h.get, failReadModel)},
		{Method: http.MethodPatch, Path: modelByIDPath, Scope: rbac.ScopeModelConfigUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateModel", Summary: "Update a model",
				Text: "Changes the fields the body includes. A secret sent as <REDACTED> keeps the stored value, unless the body also changes the provider.", Tag: modelsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.update, "Unable to update model")},
		{Method: http.MethodDelete, Path: modelByIDPath, Scope: rbac.ScopeModelConfigDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteModel", Summary: "Delete a model",
				Text: "Undeploys every agent and workflow that may still be running on the model, then deletes it. If any of them cannot be undeployed, nothing is deleted and the request fails with 502. Deleting the general or planning alias, which Agent Mesh itself runs on, fails with 409.", Tag: modelsTag,
				Errors: []int{http.StatusConflict, http.StatusBadGateway}},
			Handler: Handle(h.delete, "Unable to delete model")},
		{Method: http.MethodGet, Path: "/modelStatus", Gate: GateSignedIn,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getModelStatus", Summary: "Get whether the required models are configured",
				Text: "Returns whether the models Agent Mesh itself runs on are configured. A new installation asks for these before anything else.", Tag: modelStatusTag},
			Handler: Handle(h.status, "Unable to read model status")},
		{Method: http.MethodGet, Path: "/models/{id}/dependents", Scope: rbac.ScopeModelConfigDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listModelDependents", Summary: "Get a list of the agents and workflows that use a model",
				Text: "Returns one page of the agents and workflows that use the model. Use it to see what deleting the model affects; for that reason it requires the permission to delete a model.", Tag: modelsTag},
			Handler: Handle(h.listDependents, "Unable to list model dependents")},
	}
}

func (h *modelHandler) list(ctx context.Context, req listModelsRequest) (List[Model], error) {
	rows, total, err := h.svc.ListPage(ctx, (req.Page.Number-1)*req.Page.Size, req.Page.Size, req.Sort.Field, req.Sort.Desc)
	if err != nil {
		return List[Model]{}, err
	}
	items := make([]Model, len(rows))
	for i, row := range rows {
		items[i] = toModel(row)
	}
	return List[Model]{Items: items, Total: total}, nil
}

func (h *modelHandler) create(ctx context.Context, req CreateModelRequest) (Model, error) {
	create := entplatform.CreateModelConfigRequest{
		Alias:          req.Alias,
		Provider:       req.Provider,
		ModelName:      req.ModelName,
		APIBase:        req.APIBase,
		AuthType:       req.AuthType,
		AuthConfig:     req.AuthConfig,
		ModelParams:    req.ModelParams,
		ModelType:      req.ModelType,
		Description:    req.Description,
		MaxInputTokens: req.MaxInputTokens,
		Pricing:        toModelPricing(req.Pricing),
	}
	resp, err := h.svc.Create(ctx, &create, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Model{}, explainConflict(err, "A model with that alias already exists")
	}
	h.reportConfigured(ctx, resp, req.Surface)
	return toModel(resp), nil
}

func (h *modelHandler) get(ctx context.Context, req IDRequest) (Model, error) {
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Model{}, err
	}
	return toModel(resp), nil
}

func (h *modelHandler) update(ctx context.Context, req UpdateModelRequest) (Model, error) {
	update, changed, err := req.toUpdateModelConfigRequest()
	typeProblems, lookupErr := h.updateAuthTypeProblems(ctx, req)
	if lookupErr != nil {
		return Model{}, lookupErr
	}
	if len(typeProblems) > 0 {
		return Model{}, withProblems(err, typeProblems)
	}
	if err != nil {
		return Model{}, err
	}
	if !changed {
		var resp *entplatform.ModelConfigResponse
		resp, err = h.svc.GetByID(ctx, req.ID)
		if err != nil {
			return Model{}, err
		}
		return toModel(resp), nil
	}
	resp, err := h.svc.Update(ctx, req.ID, &update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Model{}, explainConflict(err, "The alias is already used by another model, or a workflow still refers to this model by its current alias")
	}
	h.reportConfigured(ctx, resp, req.Surface)
	return toModel(resp), nil
}

func (h *modelHandler) updateAuthTypeProblems(ctx context.Context, req UpdateModelRequest) (map[string][]string, error) {
	var sentType string
	if req.AuthType.Set && !req.AuthType.Null {
		sentType = req.AuthType.Value
	}
	patchAuth := withoutRedactedValues(req.AuthConfig.Value)
	problems := authTypeProblems(patchAuth, sentType)
	if len(problems) == 0 {
		return nil, nil
	}
	if effective, isString := effectiveAuthType(patchAuth, sentType); isString {
		stored, err := h.svc.GetByID(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		if stored.AuthType == effective {
			return nil, nil
		}
	}
	return problems, nil
}

func withProblems(err error, problems map[string][]string) error {
	merged := map[string][]string{}
	if ce, ok := errors.AsType[*CallerError](err); ok {
		for field, found := range ce.Details {
			merged[field] = slices.Clone(found)
		}
	}
	for field, found := range problems {
		merged[field] = append(merged[field], found...)
	}
	return &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: merged}
}

func explainConflict(err error, message string) error {
	if !IsErr[*apierr.ConflictError](err) {
		return err
	}
	return &CallerError{Status: http.StatusConflict, Message: message, Cause: err}
}

func (h *modelHandler) status(ctx context.Context, _ struct{}) (ModelStatus, error) {
	resp, err := h.svc.GetStatus(ctx)
	if err != nil {
		return ModelStatus{}, err
	}
	return ModelStatus{Configured: resp.Configured}, nil
}

func (h *modelHandler) listDependents(ctx context.Context, req listModelDependentsRequest) (List[ModelDependent], error) {
	model, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return List[ModelDependent]{}, err
	}
	rows, total, err := h.dependents.ListDependentsPage(ctx, model.Alias, (req.Page.Number-1)*req.Page.Size, req.Page.Size, req.Sort.Field, req.Sort.Desc)
	if err != nil {
		return List[ModelDependent]{}, err
	}
	items := make([]ModelDependent, len(rows))
	for i, row := range rows {
		items[i] = toModelDependent(row)
	}
	return List[ModelDependent]{Items: items, Total: total}, nil
}

func (h *modelHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	model, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return struct{}{}, err
	}
	if samapp.IsProtectedModelAlias(model.Alias) {
		return struct{}{}, &CallerError{
			Status:  http.StatusConflict,
			Message: "The model aliases SAM itself runs on cannot be deleted",
			Cause:   errors.New("delete refused for protected model alias"),
		}
	}
	failed, err := h.dependents.UndeployDependents(ctx, model.Alias)
	if err != nil {
		return struct{}{}, err
	}
	if len(failed) > 0 {
		return struct{}{}, modelKeptUndeployFailed(req.ID, failed)
	}
	return struct{}{}, h.svc.Delete(ctx, req.ID)
}

func modelKeptUndeployFailed(modelID string, failed []entplatform.ModelDependentResponse) error {
	names := make([]string, len(failed))
	for i, dep := range failed {
		names[i] = dep.Name
	}
	return &LocatedError{
		Err: &CallerError{
			Status:  http.StatusBadGateway,
			Message: "SAM could not undeploy every agent and workflow that uses this model, so the model was not deleted. Try again, and contact your administrator with the errorId if it fails again.",
			Cause:   fmt.Errorf("model %s kept: %d dependent(s) could not be undeployed: %s", modelID, len(failed), strings.Join(names, ", ")),
		},
		Location: platpkg.APIV2Prefix + "/models/" + modelID,
	}
}

func (h *modelHandler) reportConfigured(ctx context.Context, resp *entplatform.ModelConfigResponse, surface string) {
	h.analytics.ModelConfigured(ctx, orEmpty(resp.Provider), orEmpty(resp.ModelName), orEmpty(resp.APIBase), resp.ModelType, httputil.ParseClientSurface(surface))
}

func (req UpdateModelRequest) toUpdateModelConfigRequest() (entplatform.UpdateModelConfigRequest, bool, error) {
	var update entplatform.UpdateModelConfigRequest
	details := map[string][]string{}
	changed := false

	for _, f := range []struct {
		name       string
		emptyKeeps bool
		patch      Patch[string]
		dst        **string
	}{
		{"alias", false, req.Alias, &update.Alias},
		{"provider", false, req.Provider, &update.Provider},
		{"modelName", false, req.ModelName, &update.ModelName},
		{"apiBase", true, req.APIBase, &update.APIBase},
		{"authType", false, req.AuthType, &update.AuthType},
	} {
		if !f.patch.Set {
			continue
		}
		changed = true
		if f.patch.Null || (!f.emptyKeeps && f.patch.Value == "") {
			details[f.name] = []string{"cannot be empty"}
			continue
		}
		value := f.patch.Value
		*f.dst = &value
	}

	for _, f := range []struct {
		name  string
		patch Patch[map[string]any]
		dst   *map[string]any
	}{
		{"authConfig", req.AuthConfig, &update.AuthConfig},
		{"modelParams", req.ModelParams, &update.ModelParams},
	} {
		if !f.patch.Set {
			continue
		}
		if f.name != "authConfig" {
			changed = true
		}
		if f.patch.Null {
			details[f.name] = []string{"cannot be null"}
			continue
		}
		*f.dst = f.patch.Value
	}
	if update.AuthConfig != nil {
		update.AuthConfig = withoutRedactedValues(update.AuthConfig)
	}
	if req.AuthConfig.Set && (update.AuthConfig == nil || len(update.AuthConfig) > 0 || len(req.AuthConfig.Value) == 0) {
		changed = true
	}

	if req.Description.Set {
		changed, update.DescriptionSet = true, true
		if !req.Description.Null {
			update.Description = &req.Description.Value
		}
	}
	if req.MaxInputTokens.Set {
		changed, update.MaxInputTokensSet = true, true
		if !req.MaxInputTokens.Null {
			update.MaxInputTokens = &req.MaxInputTokens.Value
		}
	}
	if req.ModelType.Set {
		changed = true
		details["modelType"] = []string{"cannot be changed"}
	}
	if req.Pricing.Set {
		changed, update.PricingSet = true, true
		if !req.Pricing.Null {
			price, problems := toModelPricingPatch(req.Pricing.Value)
			for field, found := range problems {
				details[field] = append(details[field], found...)
			}
			update.Pricing = price
		}
	}

	if invalid, ok := errors.AsType[*apierr.ValidationError](entplatform.ValidateRequest(&update)); ok {
		for field, problems := range invalid.Details {
			details[field] = append(details[field], problems...)
		}
	}
	if len(details) > 0 {
		return update, changed, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	return update, changed, nil
}

func toModelPricingPatch(rates map[string]any) (*platpkg.ModelPricing, map[string][]string) {
	price := &platpkg.ModelPricing{}
	known := map[string]**string{
		"input": &price.Input, "output": &price.Output,
		"cacheRead": &price.CacheRead, "cacheWrite": &price.CacheWrite,
	}
	details := map[string][]string{}
	for key, value := range rates {
		dst, isRate := known[key]
		text, isString := value.(string)
		switch {
		case !isRate:
			details["pricing."+key] = []string{"unknown rate; the rates are input, output, cacheRead and cacheWrite"}
		case value == nil:
		case !isString:
			details["pricing."+key] = []string{"must be a string"}
		default:
			*dst = &text
		}
	}
	return price, details
}

func effectiveAuthType(authConfig map[string]any, authType string) (effective string, isString bool) {
	switch t := authConfig["type"].(type) {
	case nil:
		return authType, true
	case string:
		if t != "" {
			return t, true
		}
		return authType, true
	default:
		return "", false
	}
}

func authTypeProblems(authConfig map[string]any, authType string) map[string][]string {
	effective, isString := effectiveAuthType(authConfig, authType)
	if !isString {
		return map[string][]string{"authConfig.type": {"must be a string naming a supported auth type (allowed: " + strings.Join(platpkg.SupportedModelAuthTypes, ", ") + ")"}}
	}
	return supportedAuthTypeProblems(effective)
}

func supportedAuthTypeProblems(authType string) map[string][]string {
	if authType == "" || slices.Contains(platpkg.SupportedModelAuthTypes, authType) {
		return nil
	}
	return map[string][]string{"authConfig.type": {
		fmt.Sprintf("unsupported auth type %q (allowed: %s)", authType, strings.Join(platpkg.SupportedModelAuthTypes, ", ")),
	}}
}

func redactedValueDetails(authConfig map[string]any) map[string][]string {
	details := map[string][]string{}
	for key, value := range authConfig {
		if value == redactedSentinel {
			details["authConfig."+key] = []string{"cannot be " + redactedSentinel + ", which a read sends in place of a stored secret"}
		}
	}
	return details
}

func withoutRedactedValues(authConfig map[string]any) map[string]any {
	out := make(map[string]any, len(authConfig))
	for key, value := range authConfig {
		if value == redactedSentinel {
			continue
		}
		out[key] = value
	}
	return out
}

func toModel(resp *entplatform.ModelConfigResponse) Model {
	return Model{
		ID:             resp.ID,
		Type:           "model",
		Alias:          resp.Alias,
		Provider:       resp.Provider,
		ModelName:      resp.ModelName,
		APIBase:        resp.APIBase,
		AuthType:       resp.AuthType,
		AuthConfig:     redactModelAuthConfig(resp.AuthConfig),
		ModelParams:    resp.ModelParams,
		ModelType:      resp.ModelType,
		Description:    resp.Description,
		MaxInputTokens: resp.MaxInputTokens,
		Pricing:        toPricing(resp.Pricing),
		CreatedBy:      NullIfZero(resp.CreatedBy),
		UpdatedBy:      NullIfZero(resp.UpdatedBy),
		CreatedTime:    Instant(resp.CreatedTime),
		UpdatedTime:    Instant(resp.UpdatedTime),
	}
}

func toModelDependent(dep entplatform.ModelDependentResponse) ModelDependent {
	kind := entplatform.ComponentKindAgent
	if dep.Type == entplatform.ComponentKindWorkflow {
		kind = entplatform.ComponentKindWorkflow
	}
	return ModelDependent{
		Type:             "modelDependent",
		Key:              dep.ID,
		Name:             dep.Name,
		DependentType:    kind,
		DeploymentStatus: dep.DeploymentStatus,
		MayStillRun:      dep.MayStillRun,
	}
}

func toPricing(price *platpkg.ModelPricingResponse) *Pricing {
	if price == nil {
		return nil
	}
	return &Pricing{Input: price.Input, Output: price.Output, CacheRead: price.CacheRead, CacheWrite: price.CacheWrite}
}

func toModelPricing(price *Pricing) *platpkg.ModelPricing {
	if price == nil {
		return nil
	}
	return &platpkg.ModelPricing{Input: price.Input, Output: price.Output, CacheRead: price.CacheRead, CacheWrite: price.CacheWrite}
}
