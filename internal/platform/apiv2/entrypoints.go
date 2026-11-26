// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Entrypoint struct {
	ID               string              `json:"id" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Type             string              `json:"type" example:"entrypoint"`
	EntrypointType   string              `json:"entrypointType" example:"slack"`
	Slug             *string             `json:"slug" example:"support-slack-bot"`
	Name             string              `json:"name" example:"Support Slack bot"`
	Description      string              `json:"description" example:"Answers customer support questions in Slack."`
	PublicURL        *string             `json:"publicUrl" example:"https://support.example.com"`
	Values           map[string]any      `json:"values" example:"{\"bot_token\":\"<REDACTED>\",\"app_token\":\"<REDACTED>\"}"`
	DeploymentStatus string              `json:"deploymentStatus" example:"deployed"`
	SyncStatus       *string             `json:"syncStatus" example:"in_sync"`
	Deployed         *DeployedEntrypoint `json:"deployed"`
	RuntimeStatus    RuntimeStatus       `json:"runtimeStatus"`
	CreatedBy        string              `json:"createdBy" example:"user@example.com"`
	UpdatedBy        string              `json:"updatedBy" example:"user@example.com"`
	CreatedTime      Instant             `json:"createdTime"`
	UpdatedTime      Instant             `json:"updatedTime"`
}

type DeployedEntrypoint struct {
	Name           string         `json:"name" example:"Support Slack bot"`
	Description    string         `json:"description" example:"Answers customer support questions in Slack."`
	EntrypointType string         `json:"entrypointType" example:"slack"`
	Values         map[string]any `json:"values" example:"{\"bot_token\":\"<REDACTED>\",\"app_token\":\"<REDACTED>\"}"`
	DeployedTime   Instant        `json:"deployedTime"`
	DeployedBy     string         `json:"deployedBy" example:"user@example.com"`
}

type CreateEntrypointRequest struct {
	Name           string         `json:"name" validate:"required,min=3,max=255,safename" example:"Support Slack bot"`
	Slug           string         `json:"slug" validate:"omitempty,min=3,max=63,gatewayslug" example:"support-slack-bot"`
	Description    string         `json:"description" validate:"required,min=10,max=1000,safetext" example:"Answers customer support questions in Slack."`
	EntrypointType string         `json:"entrypointType" validate:"required,min=2,max=50" example:"slack"`
	PublicURL      string         `json:"publicUrl" example:"https://support.example.com"`
	Values         map[string]any `json:"values" example:"{\"bot_token\":\"xoxb-0000000000-placeholder\",\"app_token\":\"xapp-1-placeholder\"}"`
}

type UpdateEntrypointRequest struct {
	ID             string                `path:"id" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Name           Patch[string]         `json:"name" example:"Support Slack bot"`
	Description    Patch[string]         `json:"description" example:"Answers customer support questions in Slack."`
	PublicURL      Patch[string]         `json:"publicUrl" clearable:"true" example:"https://support.example.com"`
	Values         Patch[map[string]any] `json:"values" example:"{\"bot_token\":\"xoxb-0000000000-placeholder\"}"`
	EntrypointType Patch[string]         `json:"entrypointType" example:"slack"`
	Slug           Patch[string]         `json:"slug" example:"support-slack-bot"`
}

type EntrypointSchema struct {
	ID              string         `json:"id" example:"slack"`
	Type            string         `json:"type" example:"entrypointSchema"`
	EntrypointType  string         `json:"entrypointType" example:"slack"`
	Name            string         `json:"name" example:"Slack"`
	Description     string         `json:"description" example:"Connect your AI agent to Slack workspace using Socket Mode"`
	Warning         *string        `json:"warning" example:"Requires a Slack app with Socket Mode enabled and appropriate OAuth scopes configured"`
	Schema          map[string]any `json:"schema" example:"{\"bot_token\":{\"label\":\"Bot Token\",\"type\":\"password\",\"required\":true,\"secret\":true,\"order\":2}}"`
	RequiredEnvVars []string       `json:"requiredEnvVars" example:"[\"SLACK_BOT_TOKEN\",\"SLACK_APP_TOKEN\"]"`
}

type NetworkingDetails struct {
	DNSName    *string             `json:"dnsName" example:"whatsapp-support.example.com"`
	Port       *int                `json:"port" example:"443"`
	URL        *string             `json:"url" example:"https://whatsapp-support.example.com"`
	WebhookURL *string             `json:"webhookUrl" example:"https://whatsapp-support.example.com/webhook"`
	APIPaths   []EntrypointAPIPath `json:"apiPaths"`
	PublicURL  *string             `json:"publicUrl" example:"https://support.example.com"`
}

type EntrypointAPIPath struct {
	Path        string `json:"path" example:"/webhook"`
	Description string `json:"description" example:"WhatsApp Business Cloud API webhook endpoint (GET verify + POST messages)"`
}

type TestConnectionRequest struct {
	EntrypointType string         `json:"entrypointType" example:"whatsapp"`
	Values         map[string]any `json:"values" example:"{\"phone_number_id\":\"100000000000000\",\"access_token\":\"EAAG-placeholder-token\"}"`
	EntrypointID   string         `json:"entrypointId" validate:"omitempty,uuid" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
}

type TestConnectionResult struct {
	Success    bool    `json:"success" example:"false"`
	Message    *string `json:"message" example:"The access token has expired. Generate a new permanent System User token in Meta Business Settings and paste it here."`
	ErrorClass *string `json:"errorClass" example:"auth_credentials_expired"`
	Stage      *string `json:"stage" example:"outbound"`
}

type SecretsDiffRequest struct {
	ID     string         `path:"id" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Values map[string]any `json:"values" validate:"required" example:"{\"bot_token\":\"<REDACTED>\",\"app_token\":\"xapp-1-placeholder\"}"`
}

type SecretsDiffResult struct {
	Secrets map[string]SecretMatch `json:"secrets" example:"{\"bot_token\":{\"matches\":true},\"app_token\":{\"matches\":false}}"`
}

type SecretMatch struct {
	Matches bool `json:"matches" example:"true"`
}

type WhatsAppMessageTemplatesRequest struct {
	BusinessAccountID string         `json:"businessAccountId" validate:"required" example:"100000000000001"`
	Values            map[string]any `json:"values" example:"{\"access_token\":\"EAAG-placeholder-token\"}"`
	EntrypointID      string         `json:"entrypointId" validate:"omitempty,uuid" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
}

type MessageTemplateLookup struct {
	Templates []WhatsAppMessageTemplate `json:"templates"`
	HasMore   bool                      `json:"hasMore" example:"false"`
}

type WhatsAppMessageTemplate struct {
	Name     string   `json:"name" example:"contact_request"`
	Language string   `json:"language" example:"en_US"`
	Status   string   `json:"status" example:"approved"`
	Body     string   `json:"body" example:"Share your contact details so our team can follow up."`
	Usable   bool     `json:"usable" example:"false"`
	Blockers []string `json:"blockers" example:"[\"no_contact_button\"]"`
}

type listEntrypointsRequest struct {
	Page           Page   `json:"-"`
	EntrypointType string `query:"entrypointType" json:"-" example:"slack"`
}

type getEntrypointSchemaRequest struct {
	ID string `path:"id,key" json:"-" example:"slack"`
}

type listEntrypointSchemasRequest struct {
	Page Page `json:"-"`
}

type entrypointHandler struct {
	svc        *entplatform.GatewayService
	deploySvc  *entplatform.GatewayDeploymentService
	networking *entplatform.GatewayNetworkingService
	analytics  *platpkg.ResourceReporter
	logger     *slog.Logger
}

const entrypointPath = "/entrypoints/{id}"

func entrypointRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) []Route {
	if deps.GatewaySvc == nil || deps.GatewayDeploySvc == nil || deps.GatewayNetworkingSvc == nil {
		panic("apiv2: entrypoint services missing; register the v1 platform routes first")
	}
	h := &entrypointHandler{
		svc:        deps.GatewaySvc,
		deploySvc:  deps.GatewayDeploySvc,
		networking: deps.GatewayNetworkingSvc,
		analytics:  core.ResourceAnalytics(),
		logger:     logger,
	}
	return []Route{
		{Method: http.MethodGet, Path: "/entrypoints", Scope: rbac.ScopeEntrypointRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listEntrypoints", Summary: "Get a list of entrypoints",
				Text: "Returns one page of entrypoints, optionally only those of one entrypoint type. Secret values are redacted.", Tag: entrypointsTag},
			Handler: Handle(h.list, "Unable to list entrypoints")},
		{Method: http.MethodPost, Path: "/entrypoints", Scope: rbac.ScopeEntrypointCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createEntrypoint", Summary: "Create an entrypoint",
				Text: "Creates an entrypoint without deploying it. Deploy it with POST /entrypointDeployments.", Tag: entrypointsTag},
			Handler: Handle(h.create, "Unable to create entrypoint")},
		{Method: http.MethodGet, Path: entrypointPath, Scope: rbac.ScopeEntrypointRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getEntrypoint", Summary: "Get an entrypoint",
				Text: "Returns one entrypoint with its deployment and runtime status. Secret values are redacted.", Tag: entrypointsTag},
			Handler: Handle(h.get, "Unable to get entrypoint")},
		{Method: http.MethodPatch, Path: entrypointPath, Scope: rbac.ScopeEntrypointUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateEntrypoint", Summary: "Update an entrypoint",
				Text: "Changes the fields sent and keeps the rest. Only publicUrl can be cleared. The entrypoint type and slug cannot change. A deployed entrypoint runs its new settings after an update through POST /entrypointDeployments.", Tag: entrypointsTag},
			Handler: Handle(h.update, "Unable to update entrypoint")},
		{Method: http.MethodDelete, Path: entrypointPath, Scope: rbac.ScopeEntrypointDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteEntrypoint", Summary: "Delete an entrypoint",
				Text: "Undeploys the entrypoint if it is deployed, then deletes it. If the undeploy fails, the entrypoint is kept and the request fails with 502.", Tag: entrypointsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.delete, "Unable to delete entrypoint")},
		{Method: http.MethodGet, Path: "/entrypoints/{id}/networkingDetails", Scope: rbac.ScopeEntrypointRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getEntrypointNetworkingDetails", Summary: "Get an entrypoint's networking details",
				Text: "Returns the addresses the entrypoint receives traffic on. For a type that receives none, every field is null. If the entrypoint needs its own DNS name and has none, the request fails with 409. An entrypoint gets a DNS name only when it is created.", Tag: entrypointsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.networkingDetails, "Unable to get entrypoint networking details")},
		{Method: http.MethodPut, Path: "/entrypoints/{id}/secretsDiff", Scope: rbac.ScopeEntrypointUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "compareEntrypointSecrets", Summary: "Validate values against an entrypoint's stored secrets",
				Text: "Returns, for each secret setting of the entrypoint, whether saving the values sent would keep the stored secret. Secret values are never returned.", Tag: entrypointsTag},
			Handler: Handle(h.secretsDiff, "Unable to compare entrypoint secrets")},
		{Method: http.MethodPut, Path: "/entrypoints/testConnection", Scope: rbac.ScopeEntrypointRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "testEntrypointConnection", Summary: "Validate an entrypoint's connection",
				Text: "Probes the service an entrypoint connects to, with the settings sent or a saved entrypoint's. A probe that fails returns 200 with success false and a message. Saved secrets are used only for the saved destination. Changing the broker URL or TLS verification of an event_mesh entrypoint, or the host or port of an email entrypoint, without sending the secret again fails with 422.", Tag: entrypointsTag},
			Handler: Handle(h.testConnection, "Unable to test the entrypoint connection")},
		{Method: http.MethodPut, Path: "/entrypoints/whatsapp/messageTemplates", Scope: rbac.ScopeEntrypointRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listWhatsAppMessageTemplates", Summary: "Get a WhatsApp Business Account's message templates",
				Text: "Returns the message templates of a WhatsApp Business Account and says which can be sent as a contact request. The access token comes from the values sent or a saved entrypoint. If Meta refuses the lookup, the request fails with 502.", Tag: entrypointsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.messageTemplates, "Unable to list WhatsApp message templates")},
		{Method: http.MethodGet, Path: "/entrypointSchemas", Scope: rbac.ScopeEntrypointRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listEntrypointSchemas", Summary: "Get a list of entrypoint schemas",
				Text: "Returns one page of the entrypoint types this server offers, each with the definition of every value it takes.", Tag: entrypointSchemasTag},
			Handler: Handle(h.schemas, "Unable to list entrypoint schemas")},
		{Method: http.MethodGet, Path: "/entrypointSchemas/{id}", Scope: rbac.ScopeEntrypointRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getEntrypointSchema", Summary: "Get an entrypoint schema",
				Text: "Returns one entrypoint type with the definition of every value it takes, such as its label, input type, and whether it is required or secret. The id is the entrypoint type, such as slack.", Tag: entrypointSchemasTag},
			Handler: Handle(h.schema, "Unable to get entrypoint schema")},
		{Method: http.MethodPost, Path: "/entrypointDeployments", Scope: rbac.ScopeEntrypointDeploy, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createEntrypointDeployment", Summary: "Initiate an entrypoint deployment",
				Text: "Deploys, updates, or undeploys an entrypoint and returns the deployment record. If the deployment runs and fails, the request fails with 502. If it is refused while deploying, the request fails with 422. In both cases the Location header links the kept record. If it is refused before it starts, such as when settings are still to be filled in, the request fails with 422 and keeps no record. If the server has no event broker connection, the request fails with 503.", Tag: entrypointDeploymentsTag,
				Errors: []int{http.StatusBadGateway, http.StatusServiceUnavailable}},
			Handler: Handle(h.deploy, msgDeployFailed)},
		{Method: http.MethodGet, Path: "/entrypointDeployments", Scope: rbac.ScopeDeploymentRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listEntrypointDeployments", Summary: "Get a list of entrypoint deployments",
				Text: "Returns one page of deployment records, newest first, optionally only those of one entrypoint.", Tag: entrypointDeploymentsTag},
			Handler: Handle(h.listDeployments, "Unable to list entrypoint deployments")},
		{Method: http.MethodGet, Path: "/entrypointDeployments/{id}", Scope: rbac.ScopeDeploymentRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getEntrypointDeployment", Summary: "Get an entrypoint deployment",
				Text: "Returns one deployment record, including why it failed when it did.", Tag: entrypointDeploymentsTag},
			Handler: Handle(h.getDeployment, "Unable to get entrypoint deployment")},
	}
}

func (h *entrypointHandler) list(ctx context.Context, req listEntrypointsRequest) (List[Entrypoint], error) {
	resp, err := h.svc.ListPaginated(ctx, req.Page.Number, req.Page.Size, req.EntrypointType)
	if err != nil {
		return List[Entrypoint]{}, err
	}
	items := make([]Entrypoint, len(resp.Data))
	for i := range resp.Data {
		items[i] = toEntrypoint(&resp.Data[i])
	}
	return List[Entrypoint]{Items: items, Total: resp.Meta.Pagination.Count}, nil
}

func (h *entrypointHandler) create(ctx context.Context, req CreateEntrypointRequest) (Entrypoint, error) {
	create := entplatform.CreateGatewayRequest{
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Type:        req.EntrypointType,
		PublicURL:   req.PublicURL,
		Values:      optionalValues(req.Values),
	}
	resp, err := h.svc.Create(ctx, create, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Entrypoint{}, UnderValues(err)
	}
	h.analytics.EntrypointCreated(ctx, resp.Type)
	return toEntrypoint(resp), nil
}

func (h *entrypointHandler) get(ctx context.Context, req IDRequest) (Entrypoint, error) {
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Entrypoint{}, err
	}
	return toEntrypoint(resp), nil
}

func (h *entrypointHandler) update(ctx context.Context, req UpdateEntrypointRequest) (Entrypoint, error) {
	update, err := req.toUpdateGatewayRequest()
	if err != nil {
		return Entrypoint{}, err
	}
	resp, err := h.svc.Update(ctx, req.ID, update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Entrypoint{}, UnderValues(err)
	}
	return toEntrypoint(resp), nil
}

func (req UpdateEntrypointRequest) toUpdateGatewayRequest() (entplatform.UpdateGatewayRequest, error) {
	var update entplatform.UpdateGatewayRequest
	details := map[string][]string{}
	for _, f := range []struct {
		name  string
		patch Patch[string]
		dst   *string
	}{
		{"name", req.Name, &update.Name},
		{"description", req.Description, &update.Description},
	} {
		if f.patch.Set && (f.patch.Null || f.patch.Value == "") {
			details[f.name] = []string{"cannot be empty"}
		}
		*f.dst = f.patch.Value
	}
	if req.PublicURL.Set {
		update.PublicURL = &req.PublicURL.Value
	}
	switch {
	case req.Values.Null:
		details["values"] = []string{"cannot be null"}
	case req.Values.Set:
		update.Values = req.Values.Value
	}
	for name, patch := range map[string]Patch[string]{"entrypointType": req.EntrypointType, "slug": req.Slug} {
		if patch.Set {
			details[name] = []string{"cannot be changed"}
		}
	}
	if err := entplatform.ValidateRequest(&update); err != nil {
		if ve, ok := errors.AsType[*apierr.ValidationError](err); ok {
			for field, problems := range ve.Details {
				details[field] = append(details[field], problems...)
			}
		}
	}
	if len(details) > 0 {
		return update, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	return update, nil
}

func (h *entrypointHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.svc.DeleteWithUndeploy(ctx, req.ID, entplatform.UserIDFromContext(ctx), h.deploySvc); err != nil {
		return struct{}{}, err
	}
	h.analytics.EntrypointDeleted(ctx)
	return struct{}{}, nil
}

func (h *entrypointHandler) networkingDetails(ctx context.Context, req IDRequest) (NetworkingDetails, error) {
	resp, err := h.networking.GetNetworking(ctx, req.ID)
	switch {
	case errors.Is(err, entplatform.ErrGatewayDNSNotProvisioned):
		return NetworkingDetails{}, &CallerError{Status: http.StatusConflict, Message: "The entrypoint has no DNS name; recreate it to get one", Cause: err}
	case err != nil:
		return NetworkingDetails{}, err
	case resp == nil:
		return NetworkingDetails{APIPaths: []EntrypointAPIPath{}}, nil
	}
	paths := make([]EntrypointAPIPath, len(resp.APIPaths))
	for i, p := range resp.APIPaths {
		paths[i] = EntrypointAPIPath{Path: p.Path, Description: p.Description}
	}
	return NetworkingDetails{
		DNSName:    NullIfZero(resp.DNSName),
		Port:       NullIfZero(resp.Port),
		URL:        NullIfZero(resp.URL),
		WebhookURL: NullIfZero(resp.WebhookURL),
		APIPaths:   paths,
		PublicURL:  NullIfZero(resp.PublicURL),
	}, nil
}

func (h *entrypointHandler) secretsDiff(ctx context.Context, req SecretsDiffRequest) (SecretsDiffResult, error) {
	diff, err := h.svc.GetSecretsDiff(ctx, req.ID, req.Values)
	if err != nil {
		return SecretsDiffResult{}, err
	}
	entplatform.LogSecretsDiff(ctx, h.logger, "gateway", req.ID, diff)
	result := SecretsDiffResult{Secrets: make(map[string]SecretMatch, len(diff))}
	for field, d := range diff {
		result.Secrets[field] = SecretMatch{Matches: d.Matches}
	}
	return result, nil
}

func (h *entrypointHandler) testConnection(ctx context.Context, req TestConnectionRequest) (TestConnectionResult, error) {
	if req.EntrypointID == "" && req.Values == nil {
		return TestConnectionResult{}, errNoEntrypointOrValues()
	}
	probe := entplatform.TestConnectionRequest{GatewayType: req.EntrypointType, GatewayID: req.EntrypointID, Values: optionalValues(req.Values)}
	resp, err := h.svc.TestConnection(ctx, probe)
	if err != nil {
		return TestConnectionResult{}, UnderValues(err)
	}
	return TestConnectionResult{
		Success:    resp.Success,
		Message:    NullIfZero(resp.Message),
		ErrorClass: NullIfZero(resp.ErrorClass),
		Stage:      NullIfZero(resp.Stage),
	}, nil
}

func (h *entrypointHandler) messageTemplates(ctx context.Context, req WhatsAppMessageTemplatesRequest) (MessageTemplateLookup, error) {
	if req.EntrypointID == "" && req.Values == nil {
		return MessageTemplateLookup{}, errNoEntrypointOrValues()
	}
	lookup := entplatform.WhatsAppMessageTemplatesRequest{BusinessAccountID: req.BusinessAccountID, GatewayID: req.EntrypointID, Values: optionalValues(req.Values)}
	resp, err := h.svc.ListWhatsAppMessageTemplates(ctx, lookup)
	if err != nil {
		return MessageTemplateLookup{}, UnderValues(err)
	}
	if resp.ErrorClass != "" {
		status := http.StatusBadGateway
		if resp.ErrorClass == string(whatsapp.ProbeClassIncomplete) {
			status = http.StatusUnprocessableEntity
		}
		return MessageTemplateLookup{}, &CallerError{Status: status, Message: resp.Message}
	}
	templates := make([]WhatsAppMessageTemplate, len(resp.Data))
	for i, t := range resp.Data {
		templates[i] = toWhatsAppMessageTemplate(t)
	}
	return MessageTemplateLookup{Templates: templates, HasMore: resp.HasMore}, nil
}

func errNoEntrypointOrValues() error {
	reason := []string{entplatform.MsgEntrypointOrValuesRequired}
	return &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
		Details: map[string][]string{"entrypointId": reason, "values": reason}}
}

func (h *entrypointHandler) schemas(_ context.Context, req listEntrypointSchemasRequest) (List[EntrypointSchema], error) {
	all := h.svc.GetSchemas()
	items := make([]EntrypointSchema, len(all))
	for i := range all {
		items[i] = toEntrypointSchema(&all[i])
	}
	return PageOf(items, req.Page), nil
}

func (h *entrypointHandler) schema(_ context.Context, req getEntrypointSchemaRequest) (EntrypointSchema, error) {
	for _, s := range h.svc.GetSchemas() {
		if s.Type == req.ID {
			return toEntrypointSchema(&s), nil
		}
	}
	return EntrypointSchema{}, &apierr.NotFoundError{Resource: "entrypoint schema", ID: req.ID}
}

func toEntrypointSchema(s *entplatform.GatewaySchemaResponse) EntrypointSchema {
	schema, _ := s.Schema.(map[string]any)
	return EntrypointSchema{
		ID:              s.Type,
		Type:            "entrypointSchema",
		EntrypointType:  s.Type,
		Name:            s.Name,
		Description:     s.Description,
		Warning:         NullIfZero(s.Warning),
		Schema:          schema,
		RequiredEnvVars: nonNilList(s.RequiredEnvVars),
	}
}

func toWhatsAppMessageTemplate(t entplatform.WhatsAppMessageTemplate) WhatsAppMessageTemplate {
	template := WhatsAppMessageTemplate(t)
	template.Status = strings.ToLower(t.Status)
	return template
}

func toEntrypoint(resp *entplatform.GatewayResponse) Entrypoint {
	values, _ := resp.Values.(map[string]any)
	return Entrypoint{
		ID:               resp.ID,
		Type:             "entrypoint",
		EntrypointType:   resp.Type,
		Slug:             NullIfZero(resp.Slug),
		Name:             resp.Name,
		Description:      resp.Description,
		PublicURL:        NullIfZero(resp.PublicURL),
		Values:           values,
		DeploymentStatus: resp.DeploymentStatus,
		SyncStatus:       resp.SyncStatus,
		Deployed:         toDeployedEntrypoint(resp.Deployed),
		RuntimeStatus:    toRuntimeStatus(resp.RuntimeStatus),
		CreatedBy:        resp.CreatedBy,
		UpdatedBy:        resp.UpdatedBy,
		CreatedTime:      Instant(resp.CreatedTime),
		UpdatedTime:      Instant(resp.UpdatedTime),
	}
}

func toDeployedEntrypoint(deployed any) *DeployedEntrypoint {
	info, ok := deployed.(*entplatform.DeployedGatewayInfo)
	if !ok || info == nil {
		return nil
	}
	values, _ := info.Values.(map[string]any)
	return &DeployedEntrypoint{
		Name:           info.Name,
		Description:    info.Description,
		EntrypointType: info.Type,
		Values:         values,
		DeployedTime:   Instant(info.DeployedAt),
		DeployedBy:     info.DeployedBy,
	}
}

func optionalValues(values map[string]any) any {
	if values == nil {
		return nil
	}
	return values
}

func nonNilList[T any](items []T) []T { return apicontract.NonNilList(items) }
