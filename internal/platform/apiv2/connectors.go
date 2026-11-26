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
	"slices"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const connectorPath = "/connectors/{id}"

type Connector struct {
	ID                 string                       `json:"id" example:"3f1a9c7e-2b44-4f8a-9a1e-77c0d9e5b210"`
	Type               string                       `json:"type" example:"connector"`
	ConnectorType      string                       `json:"connectorType" example:"api"`
	ConnectorSubtype   string                       `json:"connectorSubtype" example:"openapi"`
	Name               string                       `json:"name" example:"Orders API"`
	Description        *string                      `json:"description" example:"The orders service used by the fulfilment agent"`
	Values             map[string]any               `json:"values" example:"{\"specification_url\":\"https://api.example.com/openapi.json\"}"`
	AgentCount         int                          `json:"agentCount" example:"2"`
	HasResource        bool                         `json:"hasResource" example:"true"`
	ContentHash        *string                      `json:"contentHash" example:"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"`
	Warnings           []string                     `json:"warnings" example:"[\"specification_url is not reachable from the platform\"]"`
	AgentRegistrations []ConnectorAgentRegistration `json:"agentRegistrations"`
	CreatedBy          *string                      `json:"createdBy" example:"user@example.com"`
	UpdatedBy          *string                      `json:"updatedBy" example:"user@example.com"`
	CreatedTime        Instant                      `json:"createdTime"`
	UpdatedTime        Instant                      `json:"updatedTime"`
}

type ConnectorAgentRegistration struct {
	Agent     ConnectorAgentRef `json:"agent"`
	Status    string            `json:"status" example:"ready"`
	Error     *string           `json:"error" example:"tool orders_create failed to register"`
	ToolCount int               `json:"toolCount" example:"7"`
}

type ConnectorAgentRef struct {
	ID   string `json:"id" example:"7c2e4411-8aa0-4a55-9f0d-6b2a1e3c4d5f"`
	Name string `json:"name" example:"fulfilment-agent"`
}

type CreateConnectorRequest struct {
	Name             string         `json:"name" validate:"required,min=3,max=255,safename" example:"Orders API"`
	Description      string         `json:"description" validate:"required,min=10,max=1000,safetext" example:"The orders service used by the fulfilment agent"`
	ConnectorType    string         `json:"connectorType" validate:"required,max=50" example:"api"`
	ConnectorSubtype string         `json:"connectorSubtype" validate:"required,max=50" example:"openapi"`
	Values           map[string]any `json:"values" example:"{\"specification_url\":\"https://api.example.com/openapi.json\"}"`
}

type UpdateConnectorRequest struct {
	ID               string                `path:"id" json:"-"`
	Name             Patch[string]         `json:"name" example:"Orders API"`
	Description      Patch[string]         `json:"description" clearable:"true" example:"The orders service used by the fulfilment agent"`
	Values           Patch[map[string]any] `json:"values" example:"{\"specification_url\":\"https://api.example.com/openapi.json\"}"`
	ConnectorType    Patch[string]         `json:"connectorType" example:"api"`
	ConnectorSubtype Patch[string]         `json:"connectorSubtype" example:"openapi"`
}

type listConnectorsRequest struct {
	Page             Page      `json:"-"`
	ConnectorType    string    `query:"connectorType" json:"-" example:"api"`
	ConnectorSubtype string    `query:"connectorSubtype" json:"-" example:"openapi"`
	Name             string    `query:"name" json:"-" example:"orders"`
	Sort             SortOrder `sort:"name,createdTime" json:"-"`
}

type connectorHandler struct {
	svc             *entplatform.ConnectorService
	mcp             *entplatform.MCPDiscoveryService
	authDetector    *entplatform.OpenAPIAuthDetector
	specReader      *entplatform.ConnectorSpecificationReader
	featureRegistry *features.Registry
	analytics       *platpkg.ResourceReporter
	logger          *slog.Logger
	callbackPrefix  string
}

func connectorRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) []Route {
	if deps.ConnectorSvc == nil || deps.MCPSvc == nil {
		panic("apiv2: connector or MCP discovery service missing; register the v1 platform routes first")
	}
	h := &connectorHandler{
		svc:             deps.ConnectorSvc,
		mcp:             deps.MCPSvc,
		authDetector:    deps.AuthDetector,
		specReader:      entplatform.NewConnectorSpecificationReader(),
		featureRegistry: deps.FeatureRegistry,
		analytics:       core.ResourceAnalytics(),
		logger:          logger,
		callbackPrefix:  platpkg.APIV2Prefix,
	}
	return slices.Concat(connectorCRUDRoutes(h), connectorResourceRoutes(h), connectorSchemaRoutes(h),
		connectorProbeRoutes(h), connectorMCPRoutes(h))
}

func connectorCRUDRoutes(h *connectorHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: "/connectors", Scope: rbac.ScopeConnectorRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listConnectors", Summary: "Get a list of connectors",
				Text: "Returns one page of connectors, optionally only those of one kind. Secret settings are redacted.", Tag: connectorsTag},
			Handler: Handle(h.list, "Unable to list connectors")},
		{Method: http.MethodPost, Path: "/connectors", Scope: rbac.ScopeConnectorCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createConnector", Summary: "Create a connector",
				Text: "Creates a connector. If another connector has the same name, ignoring case, the request fails with 409.", Tag: connectorsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.create, "Unable to create connector")},
		{Method: http.MethodGet, Path: connectorPath, Scope: rbac.ScopeConnectorRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getConnector", Summary: "Get a connector",
				Text: "Returns one connector with its agent registrations. Secret settings are redacted.", Tag: connectorsTag},
			Handler: Handle(h.get, "Unable to get connector")},
		{Method: http.MethodPatch, Path: connectorPath, Scope: rbac.ScopeConnectorUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateConnector", Summary: "Update a connector",
				Text: "Changes the fields sent and keeps the rest. Send a null description to clear it. The values object replaces all settings, but a secret left out, null, or \"<REDACTED>\" keeps its stored value. The type and subtype cannot change. Bound agents and workflows redeploy.", Tag: connectorsTag},
			Handler: Handle(h.update, "Unable to update connector")},
		{Method: http.MethodDelete, Path: connectorPath, Scope: rbac.ScopeConnectorDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteConnector", Summary: "Delete a connector",
				Text: "Deletes the connector and unbinds it from every agent and workflow, which then redeploy.", Tag: connectorsTag},
			Handler: Handle(h.delete, "Unable to delete connector")},
	}
}

func (h *connectorHandler) list(ctx context.Context, req listConnectorsRequest) (List[Connector], error) {
	filter := store.ConnectorFilter{
		Type: req.ConnectorType, Subtype: req.ConnectorSubtype, Name: req.Name,
		ExcludeTypes: h.hiddenTypesExcludedInSQL(),
	}
	resp, err := h.svc.ListPaginated(ctx, req.Page.Number, req.Page.Size, filter, connectorOrder(req.Sort))
	if err != nil {
		return List[Connector]{}, err
	}
	return ListOf(resp, toConnector), nil
}

func (h *connectorHandler) hiddenTypesExcludedInSQL() []string {
	var hidden []string
	for _, connType := range connectorTypeNames() {
		if entplatform.ConnectorTypeHidden(h.featureRegistry, connType) {
			hidden = append(hidden, connType)
		}
	}
	return hidden
}

var connectorTypeNames = sync.OnceValue(func() []string {
	schemas := entplatform.GetConnectorSchemas()
	names := make([]string, len(schemas))
	for i, schema := range schemas {
		names[i] = schema.Type
	}
	return names
})

func connectorOrder(sort SortOrder) store.ConnectorOrder {
	field := store.ConnectorSortDefault
	switch sort.Field {
	case "name":
		field = store.ConnectorSortName
	case "createdTime":
		field = store.ConnectorSortCreatedTime
	}
	return store.ConnectorOrder{Field: field, Desc: sort.Desc}
}

func (h *connectorHandler) create(ctx context.Context, req CreateConnectorRequest) (Connector, error) {
	if entplatform.ConnectorTypeHidden(h.featureRegistry, req.ConnectorType) {
		return Connector{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"connectorType": {req.ConnectorType + " connectors are not enabled on this deployment"}}}
	}
	if err := entplatform.ValidateConnectorTypeSubtype(req.ConnectorType, req.ConnectorSubtype); err != nil {
		return Connector{}, entplatform.OnField(err, "connectorSubtype")
	}
	create := entplatform.CreateConnectorRequest{
		Name:        req.Name,
		Description: req.Description,
		Type:        req.ConnectorType,
		Subtype:     req.ConnectorSubtype,
		Values:      optionalValues(req.Values),
	}
	resp, err := h.svc.Create(ctx, create, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Connector{}, settingsRejection(err)
	}
	h.analytics.ConnectorCreated(ctx, resp.Type)
	return toConnector(resp), nil
}

func (h *connectorHandler) get(ctx context.Context, req IDRequest) (Connector, error) {
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Connector{}, err
	}
	regs, incomplete := h.svc.CollectAgentRegistrations(ctx, req.ID)
	resp.AgentRegistrations = regs
	if incomplete {
		resp.Warnings = append(resp.Warnings, entplatform.MsgAgentRegistrationsIncomplete)
	}
	return toConnector(resp), nil
}

func (h *connectorHandler) update(ctx context.Context, req UpdateConnectorRequest) (Connector, error) {
	current, err := h.storedName(ctx, req)
	if err != nil {
		return Connector{}, err
	}
	update, opts, err := req.toUpdateConnectorRequest(current)
	if err != nil {
		return Connector{}, err
	}
	resp, err := h.svc.UpdateWithRedeploy(ctx, req.ID, update, entplatform.UserIDFromContext(ctx), opts)
	if err != nil {
		return Connector{}, settingsRejection(err)
	}
	return toConnector(resp), nil
}

func (h *connectorHandler) storedName(ctx context.Context, req UpdateConnectorRequest) (string, error) {
	if !req.Name.Set || req.Name.Null || req.Name.Value == "" {
		return "", nil
	}
	stored, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return "", err
	}
	return stored.Name, nil
}

func settingsRejection(err error) error {
	return entplatform.OnField(err, "values")
}

func (req UpdateConnectorRequest) toUpdateConnectorRequest(storedName string) (entplatform.UpdateConnectorRequest, entplatform.ConnectorUpdateOptions, error) {
	var update entplatform.UpdateConnectorRequest
	var opts entplatform.ConnectorUpdateOptions
	details := map[string][]string{}
	if req.Name.Set && (req.Name.Null || req.Name.Value == "") {
		details["name"] = []string{"cannot be empty"}
	}
	if err := entplatform.ValidateConnectorRename(storedName, req.Name.Value); err != nil {
		if ve, ok := errors.AsType[*apierr.ValidationError](err); ok {
			details["name"] = append(details["name"], ve.Details["name"]...)
		}
	}
	update.Name = req.Name.Value
	switch {
	case req.Description.Null:
		opts.ClearDescription = true
	case req.Description.Set && req.Description.Value == "":
		details["description"] = []string{"cannot be empty; send null to clear it"}
	default:
		update.Description = req.Description.Value
	}
	switch {
	case req.Values.Null:
		details["values"] = []string{"cannot be null"}
	case req.Values.Set:
		update.Values = req.Values.Value
	}
	for name, patch := range map[string]Patch[string]{"connectorType": req.ConnectorType, "connectorSubtype": req.ConnectorSubtype} {
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
		return update, opts, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	return update, opts, nil
}

func (h *connectorHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.svc.DeleteWithRedeploy(ctx, req.ID, entplatform.UserIDFromContext(ctx)); err != nil {
		return struct{}{}, err
	}
	h.analytics.ConnectorDeleted(ctx)
	return struct{}{}, nil
}

func toConnector(c *entplatform.ConnectorResponse) Connector {
	return Connector{
		ID:                 c.ID,
		Type:               "connector",
		ConnectorType:      c.Type,
		ConnectorSubtype:   c.Subtype,
		Name:               c.Name,
		Description:        NullIfZero(c.Description),
		Values:             redactedValuesFailClosed(c.Type, c.Subtype, c.Values),
		AgentCount:         c.AgentCount,
		HasResource:        c.HasResource,
		ContentHash:        NullIfZero(c.ContentHash),
		Warnings:           nonNilList(c.Warnings),
		AgentRegistrations: toAgentRegistrations(c.AgentRegistrations),
		CreatedBy:          NullIfZero(c.CreatedBy),
		UpdatedBy:          NullIfZero(c.UpdatedBy),
		CreatedTime:        Instant(c.CreatedTime),
		UpdatedTime:        Instant(c.UpdatedTime),
	}
}

func toAgentRegistrations(regs []entplatform.ConnectorAgentRegistration) []ConnectorAgentRegistration {
	out := make([]ConnectorAgentRegistration, len(regs))
	for i, r := range regs {
		out[i] = ConnectorAgentRegistration{
			Agent:     ConnectorAgentRef{ID: r.Agent.ID, Name: r.Agent.Name},
			Status:    r.Status,
			Error:     NullIfZero(r.Error),
			ToolCount: r.ToolCount,
		}
	}
	return out
}

func redactedValuesFailClosed(connType, subtype string, raw any) map[string]any {
	values := valuesObjectOnWire(raw)
	if entplatform.ConnectorSecretFieldsKnown(connType, subtype) {
		return values
	}
	redacted := make(map[string]any, len(values))
	for k, v := range values {
		if v == nil {
			redacted[k] = nil
			continue
		}
		redacted[k] = entplatform.RedactedSentinel
	}
	return redacted
}

func valuesObjectOnWire(raw any) map[string]any {
	values, ok := raw.(map[string]any)
	if !ok || values == nil {
		return map[string]any{}
	}
	return values
}
