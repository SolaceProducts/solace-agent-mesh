// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const emailGatewayFeatureKey = "email_gateway"

const whatsappGatewayFeatureKey = "whatsapp_gateway"

const defaultAgentLabel = "Default Agent"

const whatsappIdentitySection = "Identity Configuration"

const errGetGatewayWrap = "get gateway: %w"

const errMarshalValuesWrap = "marshal values: %w"

func errGatewayTypeDisabled(gatewayType string) error {
	return &ValidationError{Message: gatewayType + " entrypoint is not enabled on this deployment", Field: fieldEntrypointType}
}

type GatewayService struct {
	gatewayRepo     *store.GatewayRepo
	deploymentRepo  *store.GatewayDeploymentRepo
	gatewayRegistry *platpkg.GatewayRegistry
	controlClient   *control.Client
	featureRegistry *features.Registry
	logger          *slog.Logger
	inboundTestFn   func(ctx context.Context, values map[string]any) (*TestConnectionResponse, error)
	outboundTestFn  func(ctx context.Context, values map[string]any) (*TestConnectionResponse, error)
	bindings        *store.EntrypointIdentityBindingsRepo
}

func (s *GatewayService) SetIdentityBindings(bindings *store.EntrypointIdentityBindingsRepo) {
	s.bindings = bindings
}

func NewGatewayService(gatewayRepo *store.GatewayRepo, deploymentRepo *store.GatewayDeploymentRepo, gatewayRegistry *platpkg.GatewayRegistry, controlClient *control.Client, featureRegistry *features.Registry, logger *slog.Logger) *GatewayService {
	return &GatewayService{gatewayRepo: gatewayRepo, deploymentRepo: deploymentRepo, gatewayRegistry: gatewayRegistry, controlClient: controlClient, featureRegistry: featureRegistry, logger: logger}
}

var featureGatedGatewayTypes = map[string]string{
	"email":       emailGatewayFeatureKey,
	"whatsapp":    whatsappGatewayFeatureKey,
	"google_chat": features.KeyGoogleChatGateway,
	"agent_link":  features.KeyAgentLinkEntrypoint,
}

func gatewayTypeVisible(typ string, reg *features.Registry) bool {
	flag, gated := featureGatedGatewayTypes[typ]
	if !gated {
		return true
	}
	return reg != nil && reg.IsEnabled(flag)
}

func refuseDisabledTypeBeforeEgressProbe(gatewayType string, reg *features.Registry) error {
	if !gatewayTypeVisible(gatewayType, reg) {
		return errGatewayTypeDisabled(gatewayType)
	}
	return nil
}

func ValidateGatewayCreate(req CreateGatewayRequest, reg *features.Registry) error {
	_, err := prepareGatewayCreate(req, reg, nil)
	return err
}

func prepareGatewayCreate(req CreateGatewayRequest, reg *features.Registry, logger *slog.Logger) (CreateGatewayRequest, error) {
	if !gatewayTypeRegex.MatchString(req.Type) {
		return req, &ValidationError{Message: "type must be lowercase alphanumeric with underscores", Field: fieldEntrypointType}
	}

	if !gatewayTypeVisible(req.Type, reg) {
		return req, errGatewayTypeDisabled(req.Type)
	}

	if req.PublicURL != "" {
		if err := validatePublicURL(req.PublicURL); err != nil {
			return req, err
		}
	}

	if gatewayRequiresObjectValues(req.Type) {
		if _, ok := req.Values.(map[string]any); !ok {
			return req, &ValidationError{Message: fmt.Sprintf("%s entrypoint requires an object 'values' payload", req.Type), Field: fieldValues}
		}
	}

	knownTypes := validGatewayTypes(getAllGatewaySchemas())
	knownTypes["httpsse"] = true
	knownTypes["http"] = true
	if !knownTypes[req.Type] {
		return req, &ValidationError{Message: fmt.Sprintf("unknown entrypoint type %q", req.Type), Field: fieldEntrypointType}
	}

	if adapterName, ok := gatewayTypeToAdapter(req.Type); ok {
		registered := entrypoint.RegisteredAdapters()
		if !slices.Contains(registered, adapterName) {
			return req, &ValidationError{Message: fmt.Sprintf(
				"entrypoint type %q has no registered adapter on this GWE (available: %v)",
				req.Type, registered,
			), Field: fieldEntrypointType}
		}
	}

	if req.Type == "event_mesh" {
		if valuesMap, ok := req.Values.(map[string]any); ok {
			if err := rejectUnsupportedEventMeshAuthoring(valuesMap); err != nil {
				return req, err
			}
			normalized, warnings := normalizeEventMeshAuthoredValues(valuesMap)
			for _, w := range warnings {
				if logger != nil {
					logger.Warn("event_mesh gateway authoring migration", "gateway", req.Name, "warning", w)
				}
			}
			req.Values = normalized
		}
	}

	if valuesMap, ok := req.Values.(map[string]any); ok {
		req.Values = normalizeValuesForType(req.Type, valuesMap)
	}
	if valuesMap, ok := req.Values.(map[string]any); ok {
		if err := validateGatewayValues(req.Type, valuesMap); err != nil {
			return req, onField(err, fieldValues)
		}
	}
	return req, nil
}

func (s *GatewayService) Create(ctx context.Context, req CreateGatewayRequest, userID string) (*GatewayResponse, error) {
	req, err := prepareGatewayCreate(req, s.featureRegistry, s.logger)
	if err != nil {
		return nil, err
	}

	var valuesStr *string
	if req.Values != nil {
		b, marshalErr := json.Marshal(req.Values)
		if marshalErr != nil {
			return nil, fmt.Errorf(errMarshalValuesWrap, marshalErr)
		}
		vs := string(b)
		valuesStr = &vs

		if valuesMap, ok := req.Values.(map[string]any); ok {
			if req.Type == "email" {
				if err := s.runEmailSaveTest(ctx, req.Name, valuesMap); err != nil {
					return nil, onField(err, fieldValues)
				}
			}
		}
	}

	slug := req.Slug

	gw := &store.Gateway{
		Name:        req.Name,
		Slug:        slug,
		Description: strPtrOrEmpty(req.Description),
		Type:        req.Type,
		Values:      valuesStr,
		PublicURL:   strPtr(req.PublicURL),
		CreatedBy:   userID,
		UpdatedBy:   userID,
	}
	if gw.Values == nil {
		empty := "{}"
		gw.Values = &empty
	}

	if err := s.gatewayRepo.Create(ctx, gw); err != nil {
		if slugErr, ok := errors.AsType[*store.GatewaySlugConflictError](err); ok {
			return nil, &ConflictError{Message: fmt.Sprintf("an entrypoint with slug %q already exists", slugErr.Slug)}
		}
		if dbutil.IsUniqueViolation(err) {
			return nil, &ConflictError{Message: fmt.Sprintf("an entrypoint with name %q already exists", req.Name)}
		}
		return nil, fmt.Errorf("create gateway: %w", err)
	}

	return s.buildResponse(gw, DisplayStatusNotDeployed, nil), nil
}

func (s *GatewayService) GetByID(ctx context.Context, id string) (*GatewayResponse, error) {
	gw, err := s.gatewayRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetGatewayWrap, err)
	}
	if gw == nil {
		return nil, &NotFoundError{Resource: "entrypoint", ID: id}
	}

	status := s.getDeploymentStatus(ctx, id)
	var latestDeploy *store.GatewayDeployment
	if s.deploymentRepo != nil {
		deploys, _ := s.deploymentRepo.GetLatestSuccessfulDeploys(ctx, []string{id})
		latestDeploy = deploys[id]
	}
	return s.buildResponse(gw, status, latestDeploy), nil
}

func (s *GatewayService) GetValuesCleartext(ctx context.Context, id string) (string, map[string]any, error) {
	gw, err := s.gatewayRepo.GetByID(ctx, id)
	if err != nil {
		return "", nil, fmt.Errorf(errGetGatewayWrap, err)
	}
	if gw == nil {
		return "", nil, &NotFoundError{Resource: "entrypoint", ID: id}
	}
	values := map[string]any{}
	if gw.Values != nil && *gw.Values != "" {
		if err := json.Unmarshal([]byte(*gw.Values), &values); err != nil {
			return "", nil, fmt.Errorf("parse gateway values: %w", err)
		}
	}
	return gw.Type, values, nil
}

func (s *GatewayService) GetSecretsDiff(ctx context.Context, id string, candidate map[string]any) (map[string]SecretFieldDiffResult, error) {
	gwType, stored, err := s.GetValuesCleartext(ctx, id)
	if err != nil {
		return nil, err
	}
	fields := registryToSecretFields(getSecretRegistry().GatewaySecretFields(gwType))
	return compareSecrets(fields, candidate, stored), nil
}

func (s *GatewayService) ListPaginated(ctx context.Context, pageNumber, pageSize int, typeFilter string) (*PaginatedResponse[GatewayResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	gateways, total, err := s.gatewayRepo.ListPaginated(ctx, pageNumber, pageSize, typeFilter)
	if err != nil {
		return nil, fmt.Errorf("list gateways: %w", err)
	}

	gatewayIDs := make([]string, len(gateways))
	for i, gw := range gateways {
		gatewayIDs[i] = gw.ID
	}
	var deploys map[string]*store.GatewayDeployment
	if s.deploymentRepo != nil {
		deploys, _ = s.deploymentRepo.GetLatestSuccessfulDeploys(ctx, gatewayIDs)
	}

	responses := make([]GatewayResponse, len(gateways))
	for i, gw := range gateways {
		status := s.getDeploymentStatus(ctx, gw.ID)
		var latestDeploy *store.GatewayDeployment
		if deploys != nil {
			latestDeploy = deploys[gw.ID]
		}
		responses[i] = *s.buildResponse(gw, status, latestDeploy)
	}

	return &PaginatedResponse[GatewayResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *GatewayService) Update(ctx context.Context, id string, req UpdateGatewayRequest, userID string) (*GatewayResponse, error) {
	existing, err := s.gatewayRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetGatewayWrap, err)
	}
	if existing == nil {
		return nil, &NotFoundError{Resource: "entrypoint", ID: id}
	}

	if err := refuseDisabledTypeBeforeEgressProbe(existing.Type, s.featureRegistry); err != nil {
		return nil, err
	}

	if req.PublicURL != nil && *req.PublicURL != "" {
		if err := validatePublicURL(*req.PublicURL); err != nil {
			return nil, err
		}
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Description != "" {
		existing.Description = strPtr(req.Description)
	}
	if req.PublicURL != nil {
		if *req.PublicURL == "" {
			existing.PublicURL = nil
		} else {
			existing.PublicURL = req.PublicURL
		}
	}
	if req.Values != nil && gatewayRequiresObjectValues(existing.Type) {
		if _, ok := req.Values.(map[string]any); !ok {
			return nil, &ValidationError{Message: fmt.Sprintf("%s entrypoint requires an object 'values' payload", existing.Type), Field: fieldValues}
		}
	}
	if req.Values != nil {
		valuesJSON, valuesErr := s.resolveUpdatedValues(ctx, existing, req.Values)
		if valuesErr != nil {
			return nil, onField(valuesErr, fieldValues)
		}
		existing.Values = &valuesJSON
	}
	existing.UpdatedBy = userID

	if updateErr := s.gatewayRepo.Update(ctx, existing); updateErr != nil {
		if dbutil.IsUniqueViolation(updateErr) {
			return nil, &ConflictError{Message: fmt.Sprintf("an entrypoint with name %q already exists", existing.Name)}
		}
		return nil, fmt.Errorf("update gateway: %w", updateErr)
	}

	status := s.getDeploymentStatus(ctx, id)
	var latestDeploy *store.GatewayDeployment
	if s.deploymentRepo != nil {
		deploys, _ := s.deploymentRepo.GetLatestSuccessfulDeploys(ctx, []string{id})
		latestDeploy = deploys[id]
	}
	return s.buildResponse(existing, status, latestDeploy), nil
}

func (s *GatewayService) resolveUpdatedValues(ctx context.Context, existing *store.Gateway, incoming any) (string, error) {
	newValues, isObject := incoming.(map[string]any)
	if isObject {
		merged, err := s.mergeAndValidateValues(existing, newValues)
		if err != nil {
			return "", err
		}
		if existing.Type == "email" {
			if err := s.runEmailSaveTest(ctx, existing.Name, merged); err != nil {
				return "", err
			}
		}
		incoming = merged
	}

	b, err := json.Marshal(incoming)
	if err != nil {
		return "", fmt.Errorf(errMarshalValuesWrap, err)
	}
	return string(b), nil
}

func (s *GatewayService) ValidateUpdate(ctx context.Context, id string, req UpdateGatewayRequest) error {
	newValues, isObject := req.Values.(map[string]any)
	if !isObject {
		return nil
	}
	existing, err := s.gatewayRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetGatewayWrap, err)
	}
	if existing == nil {
		return &NotFoundError{Resource: "entrypoint", ID: id}
	}
	_, err = s.mergeAndValidateValues(existing, newValues)
	return err
}

func (s *GatewayService) mergeAndValidateValues(existing *store.Gateway, newValues map[string]any) (map[string]any, error) {
	merged, err := s.mergeWithStoredValues(existing, newValues)
	if err != nil {
		return nil, err
	}
	if err := validateGatewayValues(existing.Type, merged); err != nil {
		return nil, err
	}
	return merged, nil
}

func (s *GatewayService) mergeWithStoredValues(existing *store.Gateway, newValues map[string]any) (map[string]any, error) {
	if existing.Values != nil {
		var existingValues map[string]any
		if err := json.Unmarshal([]byte(*existing.Values), &existingValues); err != nil {
			if s.logger != nil {
				s.logger.Warn("failed to decode stored entrypoint values, patching against the incoming values alone",
					"gatewayID", existing.ID, "gatewayType", existing.Type, "error", err)
			}
		} else {
			existingValues = normalizeValuesForType(existing.Type, existingValues)
			secretFields := getSecretRegistry().GatewaySecretFields(existing.Type)
			merged, err := mergeGatewayValuesForUpdate(existing.Type, newValues, existingValues, secretFields)
			if err != nil {
				return nil, err
			}
			newValues = merged
		}
	}
	return normalizeValuesForType(existing.Type, newValues), nil
}

func (s *GatewayService) Delete(ctx context.Context, id string) error {
	gw, err := s.gatewayRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetGatewayWrap, err)
	}
	if gw == nil {
		return &NotFoundError{Resource: "entrypoint", ID: id}
	}
	if refusal := refuseWhileLinkAgentsAssigned(ctx, s.gatewayRepo.DB(), gw, s.logger); refusal != nil {
		return refusal
	}
	if refusal := refuseWhileEventCatalogEntriesPointAtIt(ctx, s.gatewayRepo.DB(), s.featureRegistry, gw); refusal != nil {
		return refusal
	}
	if deleteErr := s.gatewayRepo.Delete(ctx, id); deleteErr != nil {
		return fmt.Errorf("delete gateway: %w", deleteErr)
	}
	s.deleteIdentityBindings(ctx, gw)
	return nil
}

func (s *GatewayService) DeleteWithUndeploy(ctx context.Context, id, userID string, deploySvc *GatewayDeploymentService) error {
	gw, err := s.gatewayRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetGatewayWrap, err)
	}
	if refusal := refuseWhileLinkAgentsAssigned(ctx, s.gatewayRepo.DB(), gw, s.logger); refusal != nil {
		return refusal
	}
	if refusal := refuseWhileEventCatalogEntriesPointAtIt(ctx, s.gatewayRepo.DB(), s.featureRegistry, gw); refusal != nil {
		return refusal
	}
	if deploySvc != nil {
		deployed, err := s.everDeployed(ctx, id)
		if err != nil {
			return err
		}
		if deployed {
			if _, err := deploySvc.Deploy(ctx, CreateGatewayDeploymentRequest{
				GatewayID: id, Action: DeployActionUndeploy,
			}, userID); err != nil {
				return &ExternalServiceError{Message: "undeploy failed; see server logs for details", Cause: fmt.Errorf("undeploy before delete: %v", err)}
			}
		}
	}
	return s.Delete(ctx, id)
}

func (s *GatewayService) everDeployed(ctx context.Context, id string) (bool, error) {
	if s.deploymentRepo == nil {
		return false, nil
	}
	latest, err := s.deploymentRepo.GetLatestByGatewayID(ctx, id)
	if err != nil {
		return false, fmt.Errorf("get latest deployment: %w", err)
	}
	return latest != nil, nil
}

func (s *GatewayService) deleteIdentityBindings(ctx context.Context, gw *store.Gateway) {
	if s.bindings == nil || gw == nil {
		return
	}
	wireID := gatewayWireID(gw)
	removed, err := s.bindings.DeleteForGateway(ctx, wireID)
	if err != nil {
		s.logger.Warn("could not delete the entrypoint's identity bindings; they will be reaped by the retention sweep",
			"gatewayID", wireID, "error", err)
		return
	}
	if removed > 0 {
		s.logger.Info("deleted the entrypoint's identity bindings", "gatewayID", wireID, "count", removed)
	}
}

func gatewayTypeToAdapter(schemaType string) (string, bool) {
	switch schemaType {
	case "http", "httpsse":
		return "httpsse", true
	case "event_mesh", "eventmesh":
		return "eventmesh", true
	case "mcp":
		return "mcp", true
	default:
		return schemaType, true
	}
}

const guidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`

const phoneNumberIDPattern = `^[0-9]+$`

func (s *GatewayService) GetSchemas() []GatewaySchemaResponse {
	visible := visibleGatewaySchemas(getAllGatewaySchemas(), s.featureRegistry)
	relaxEventMeshPasswordForDevBroker(visible)
	defaultMCPStateRedirectionOffWithoutDispatcher(visible)
	return visible
}

func defaultMCPStateRedirectionOffWithoutDispatcher(schemas []GatewaySchemaResponse) {
	if mcpDispatcherURL(nil) != "" {
		return
	}
	for i := range schemas {
		if schemas[i].Type != "mcp" {
			continue
		}
		fields, _ := schemas[i].Schema.(map[string]any)
		if sr, ok := fields["stateRedirectionEnabled"].(map[string]any); ok {
			sr["default"] = "false"
		}
	}
}

func stampMCPImplicitStateRedirection(gatewayType string, values map[string]any) {
	if gatewayType != "mcp" || !mcpEnableAuth(values) {
		return
	}
	if _, present := mcpBoolValue(values, "stateRedirectionEnabled"); present {
		return
	}
	if mcpDispatcherURL(nil) == "" && mcpDispatcherURL(values) != "" {
		values["stateRedirectionEnabled"] = "true"
	}
}

func stripStampedMCPStateRedirection(values map[string]any) {
	if !mcpEnableAuth(values) || strings.TrimSpace(stringOrDefault(values, "oauthDispatcherUrl", "")) == "" {
		return
	}
	if on, present := mcpBoolValue(values, "stateRedirectionEnabled"); present && on {
		delete(values, "stateRedirectionEnabled")
	}
}

func visibleGatewaySchemas(all []GatewaySchemaResponse, reg *features.Registry) []GatewaySchemaResponse {
	visible := make([]GatewaySchemaResponse, 0, len(all))
	for _, schema := range all {
		if gatewayTypeVisible(schema.Type, reg) {
			visible = append(visible, schema)
		}
	}
	return visible
}

func VisibleGatewaySchemas(reg *features.Registry) []GatewaySchemaResponse {
	return visibleGatewaySchemas(getAllGatewaySchemas(), reg)
}

func relaxEventMeshPasswordForDevBroker(schemas []GatewaySchemaResponse) {
	if eventMeshSystemBrokerConfigured() {
		return
	}
	for i := range schemas {
		if schemas[i].Type != "event_mesh" {
			continue
		}
		fields, ok := schemas[i].Schema.(map[string]any)
		if !ok {
			continue
		}
		if pw, ok := fields["broker_password"].(map[string]any); ok {
			pw["required"] = false
		}
	}
}

func GetAllGatewaySchemas() []GatewaySchemaResponse {
	return getAllGatewaySchemas()
}

func eventMeshGatewaySchemaResponse() GatewaySchemaResponse {
	def, ok := platpkg.LookupGatewayType("event_mesh")
	if !ok {
		return GatewaySchemaResponse{Type: "event_mesh"}
	}
	return GatewaySchemaResponse{
		Type:             def.Type,
		Name:             def.Name,
		Description:      def.Description,
		Warning:          def.Warning,
		Schema:           def.ToUISchemaMap(),
		YAMLTemplatePath: def.YAMLTemplatePath,
		RequiredEnvVars:  def.RequiredEnvVars,
	}
}

func mcpGatewaySchemaResponse() GatewaySchemaResponse {
	def, ok := platpkg.LookupGatewayType("mcp")
	if !ok {
		return GatewaySchemaResponse{Type: "mcp"}
	}
	return GatewaySchemaResponse{
		Type:             def.Type,
		Name:             def.Name,
		Description:      def.Description,
		Warning:          def.Warning,
		Schema:           def.ToUISchemaMap(),
		YAMLTemplatePath: def.YAMLTemplatePath,
		RequiredEnvVars:  def.RequiredEnvVars,
	}
}

func webhookGatewaySchemaResponse() GatewaySchemaResponse {
	def, ok := platpkg.LookupGatewayType("webhook")
	if !ok {
		return GatewaySchemaResponse{Type: "webhook"}
	}
	return GatewaySchemaResponse{
		Type:             def.Type,
		Name:             def.Name,
		Description:      def.Description,
		Warning:          def.Warning,
		Schema:           def.ToUISchemaMap(),
		YAMLTemplatePath: def.YAMLTemplatePath,
		RequiredEnvVars:  def.RequiredEnvVars,
	}
}

func googleChatGatewaySchemaResponse() GatewaySchemaResponse {
	def, ok := platpkg.LookupGatewayType("google_chat")
	if !ok {
		return GatewaySchemaResponse{Type: "google_chat"}
	}
	return GatewaySchemaResponse{
		Type:             def.Type,
		Name:             def.Name,
		Description:      def.Description,
		Warning:          def.Warning,
		Schema:           def.ToUISchemaMap(),
		YAMLTemplatePath: def.YAMLTemplatePath,
		RequiredEnvVars:  def.RequiredEnvVars,
	}
}

func agentLinkGatewaySchemaResponse() GatewaySchemaResponse {
	def, ok := platpkg.LookupGatewayType(platpkg.AgentLinkGatewayType)
	if !ok {
		return GatewaySchemaResponse{Type: platpkg.AgentLinkGatewayType}
	}
	return GatewaySchemaResponse{
		Type:             def.Type,
		Name:             def.Name,
		Description:      def.Description,
		Warning:          def.Warning,
		Schema:           def.ToUISchemaMap(),
		YAMLTemplatePath: def.YAMLTemplatePath,
		RequiredEnvVars:  def.RequiredEnvVars,
	}
}

func runAsChannelFields(useUserOrder float64) map[string]any {
	return map[string]any{
		"use_user_identity_in_channels": map[string]any{
			"label":    "Use real user identity in channels",
			"type":     "boolean",
			"required": false,
			"secret":   false,
			"order":    useUserOrder,
			"hint":     "When enabled, multi-party channel messages keep the sending user's identity instead of the run-as service account.",
		},
	}
}

func getAllGatewaySchemas() []GatewaySchemaResponse {
	schemas := []GatewaySchemaResponse{
		{
			Type:        "slack",
			Name:        "Slack",
			Description: "Connect your AI agent to Slack workspace using Socket Mode",
			Warning:     "Requires a Slack app with Socket Mode enabled and appropriate OAuth scopes configured",
			Schema: map[string]any{
				"default_agent_name": map[string]any{
					"label":    defaultAgentLabel,
					"type":     "agent_select",
					"required": false,
					"secret":   false,
					"order":    1,
					"default":  builtinOrchestratorName,
					"hint":     "The agent that handles messages when no specific agent is mentioned.",
				},
				"bot_token": map[string]any{
					"label":    "Bot Token",
					"type":     "password",
					"required": true,
					"secret":   true,
					"order":    2,
					"hint":     "Required. Starts with xoxb-. Find this in your Slack app's OAuth & Permissions settings.",
					"validation": map[string]any{
						"minLength": 1,
						"maxLength": 500,
					},
				},
				"app_token": map[string]any{
					"label":    "App-Level Token",
					"type":     "password",
					"required": true,
					"secret":   true,
					"order":    3,
					"hint":     "Required. Starts with xapp-. Generate in your Slack app's Basic Information settings.",
					"validation": map[string]any{
						"minLength": 1,
						"maxLength": 500,
					},
				},
			},
			YAMLTemplatePath: "slack_gateway.yaml",
			RequiredEnvVars:  []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"},
		},
		{
			Type:        "email",
			Name:        "Email",
			Description: "Trigger agents from incoming email via IMAP. Microsoft 365 with OAuth 2.0 (recommended) or personal Gmail with an App Password.",
			Warning:     "OAuth 2.0 against a Microsoft 365 / Entra ID tenant is the recommended production path. Basic auth (App Password) is provided for personal Gmail or legacy IMAP mailboxes during development.",
			Schema: map[string]any{
				"mailbox": map[string]any{
					"label":       "Mailbox address",
					"type":        "string",
					"required":    true,
					"order":       1,
					"hint":        "The email address whose inbox this entrypoint polls. e.g. support@example.com",
					"placeholder": "your-mailbox@example.com",
					"validation": map[string]any{
						"pattern":  `^[^\s@]+@[^\s@]+\.[^\s@]+$`,
						"errorMsg": "Must be a valid email address",
					},
				},
				"imap_host": map[string]any{
					"label":    "IMAP server host",
					"type":     "string",
					"required": true,
					"order":    2,
					"default":  "outlook.office365.com",
					"hint":     "Common values: outlook.office365.com (Microsoft 365, default), imap.gmail.com (Gmail). Auto-suggested based on mailbox domain.",
				},
				"imap_port": map[string]any{
					"label":    "IMAP port",
					"type":     "number",
					"required": false,
					"order":    3,
					"default":  float64(defaultIMAPPort),
					"hint":     "Default 993 (TLS). Use 143 only with STARTTLS.",
				},
				"auth_mode": map[string]any{
					"label":    "Authentication method",
					"type":     "radio",
					"required": true,
					"order":    4,
					"default":  "oauth2",
					"options": []map[string]any{
						{"value": "oauth2", "label": "OAuth 2.0 (Microsoft Entra ID) — recommended"},
						{"value": "basic", "label": "Basic auth (App Password / IMAP password)"},
					},
					"hint": "OAuth 2.0 (default) is the production-recommended path for Microsoft 365 mailboxes; basic auth (App Password) is for personal Gmail or legacy IMAP. OAuth 2.0 mode also routes replies through Microsoft Graph; basic auth uses SMTP.",
				},
				"imap_password": map[string]any{
					"label":              "IMAP password (App Password for Gmail)",
					"type":               "password",
					"required":           true,
					"secret":             true,
					"order":              5,
					"hint":               "For Gmail, generate an App Password at https://myaccount.google.com/apppasswords.",
					"conditionalDisplay": map[string]string{"auth_mode": "basic"},
					"showTestConnection": true,
				},
				"smtp_host": map[string]any{
					"label":              "SMTP server host (for replies)",
					"type":               "string",
					"required":           false,
					"order":              5.1,
					"default":            "smtp.gmail.com",
					"hint":               "Outbound SMTP host for agent replies. Defaults to Gmail. (OAuth 2.0 mode replies via Microsoft Graph and ignores this.)",
					"conditionalDisplay": map[string]string{"auth_mode": "basic"},
				},
				"smtp_port": map[string]any{
					"label":              "SMTP port",
					"type":               "number",
					"required":           false,
					"order":              5.2,
					"default":            float64(587),
					"hint":               "Default 587 (STARTTLS).",
					"conditionalDisplay": map[string]string{"auth_mode": "basic"},
				},
				"oauth2_tenant_id": map[string]any{
					"label":              "Entra ID Tenant ID",
					"type":               "string",
					"required":           true,
					"order":              6,
					"hint":               "The Microsoft Entra ID tenant the OAuth app is registered in.",
					"conditionalDisplay": map[string]string{"auth_mode": "oauth2"},
				},
				"oauth2_client_id": map[string]any{
					"label":              "OAuth Client ID",
					"type":               "string",
					"required":           true,
					"order":              7,
					"conditionalDisplay": map[string]string{"auth_mode": "oauth2"},
				},
				"oauth2_client_secret": map[string]any{
					"label":              "OAuth Client Secret",
					"type":               "password",
					"required":           true,
					"secret":             true,
					"order":              8,
					"conditionalDisplay": map[string]string{"auth_mode": "oauth2"},
					"showTestConnection": true,
				},

				"trusted_authserv_ids": map[string]any{
					"label":       "Trusted Authentication-Results identifiers",
					"type":        "string",
					"required":    true,
					"order":       9,
					"default":     "protection.outlook.com",
					"placeholder": "protection.outlook.com, mx.google.com",
					"hint":        "Comma-separated identities (first token) of your receiving mail server's Authentication-Results header. Verdicts from any other authservid are ignored. Common values: protection.outlook.com (Microsoft 365, default), mx.google.com (Gmail).",
				},
				"policy_mode": map[string]any{
					"label":    "DMARC policy mode",
					"type":     "select",
					"required": true,
					"order":    10,
					"default":  "require_dmarc",
					"options": []map[string]any{
						{"value": "log_only", "label": "Log only — accept all, audit verdict (initial-rollout opt-out)"},
						{"value": "require_dmarc", "label": "Require DMARC pass — reject otherwise (default)"},
						{"value": "strict", "label": "Strict — reject unless SPF, DKIM, and DMARC all pass"},
					},
					"hint": "Decides whether the entrypoint accepts a message based on its DMARC/SPF/DKIM verdicts. 'Require DMARC' (default, production-safe) drops any message whose DMARC did not pass. 'Strict' additionally requires SPF and DKIM to pass. 'Log only' accepts every message and just records what would have been rejected — use it only during an initial observation rollout, and pair it with 'Require DMARC pass to accept sender as the Solace Agent Mesh user' below so spoofed senders cannot impersonate real users while you observe.",
				},

				"require_dmarc_for_identity": map[string]any{
					"label":    "Require DMARC pass to accept sender as the Solace Agent Mesh user",
					"type":     "boolean",
					"required": false,
					"order":    11,
					"default":  true,
					"hint":     "When ON (recommended), the entrypoint only treats the message's From: address as the authenticated Solace Agent Mesh user when DMARC passes. This is the safety net for the case where 'DMARC policy mode' is set to 'Log only' — without this gate, log_only would let any spoofed From: address impersonate a real user. Leave ON unless you have intentionally allowlisted a sender pipeline that does not publish DMARC.",
				},

				"trusted_internal_tenant_id": map[string]any{
					"label":              "Microsoft Entra ID tenant ID for internal-auth trust",
					"type":               "string",
					"required":           false,
					"order":              12,
					"placeholder":        "00000000-0000-0000-0000-000000000000",
					"hint":               "Typically the same as the OAuth2 tenant ID above and pre-filled when that field is set. Differs only in cross-tenant access scenarios. When set AND the receiving MTA is Exchange Online (protection.outlook.com in trusted_authserv_ids), mail authenticated by Exchange Online inside this tenant is admitted even when DMARC is unstamped (the typical state for intra-tenant Office 365 mail). Leave blank for Gmail or to require DMARC for all mail.",
					"conditionalDisplay": map[string]string{"auth_mode": "oauth2"},
					"defaultFromField":   "oauth2_tenant_id",
				},

				"allowed_senders": map[string]any{
					"label":       "Allowed sender addresses",
					"type":        "string",
					"required":    false,
					"order":       13,
					"placeholder": "alice@example.org, bob@example.org",
					"hint":        "Comma-separated list of sender email addresses permitted to trigger the entrypoint. When empty, all senders are allowed (subject to the auth policy). When set, any sender not in the list is dropped before auth and identity evaluation.",
				},

				"mime_allowlist": map[string]any{
					"label":       "Attachment MIME allowlist",
					"type":        "string",
					"required":    false,
					"order":       14,
					"placeholder": "text/plain, application/pdf, image/*",
					"hint":        "Comma-separated MIME types permitted as attachments. When empty, every MIME type is allowed (subject to the denylist). When set, any attachment whose declared OR detected MIME is not in the list is dropped. Wildcards like image/* match the whole type.",
				},
				"mime_denylist": map[string]any{
					"label":    "Attachment MIME denylist",
					"type":     "string",
					"required": false,
					"order":    15,
					"default":  strings.Join(email.DefaultMIMEDenylist(), ", "),
					"hint":     "Comma-separated MIME types refused as attachments. Pre-populated with the recommended baseline (executables, .lnk, .hta, .iso, OneNote, Java archives). Edit or clear if your environment has different requirements. Deny takes precedence over allow on overlap.",
				},

				"delivery_mode": map[string]any{
					"label":    "Email delivery mode",
					"type":     "select",
					"required": false,
					"order":    15.9,
					"default":  "poll",
					"options": []map[string]any{
						{"value": "poll", "label": "Poll only — fixed-interval pull (recommended)"},
						{"value": "auto", "label": "Auto — use IDLE (push) when supported, else poll"},
						{"value": "idle", "label": "IDLE only — fail to start if the server doesn't advertise IDLE"},
					},
					"hint": "Default 'Poll only' fetches new mail on a fixed interval (configured below). Choose 'Auto' for near-zero inbound latency when the IMAP server advertises IDLE — falls back to polling otherwise. 'IDLE only' refuses to start when the server can't push (use when IDLE is required and you want a hard failure on misconfig).",
				},
				"poll_interval_seconds": map[string]any{
					"label":              "Polling interval (seconds)",
					"type":               "number",
					"required":           false,
					"order":              16,
					"default":            float64(30),
					"hint":               "How often to fetch new mail. Hidden in IDLE-only mode (mail arrives push-driven). Auto mode uses this only when it falls back to polling.",
					"conditionalDisplay": map[string]any{"delivery_mode": []string{"auto", "poll"}},
				},

				"max_message_size_mb": map[string]any{
					"label":    "Max message size (MB)",
					"type":     "number",
					"required": false,
					"order":    16.1,
					"default":  float64(25),
					"hint":     "Whole-message size cap. Messages larger than this are dropped before parse.",
				},
				"max_attachment_size_mb": map[string]any{
					"label":    "Max attachment size (MB)",
					"type":     "number",
					"required": false,
					"order":    16.2,
					"default":  float64(10),
					"hint":     "Per-attachment size cap. Any attachment larger than this drops the whole message.",
				},

				"default_agent_name": map[string]any{
					"label":    defaultAgentLabel,
					"type":     "agent_select",
					"required": false,
					"order":    17,
					"hint":     "The agent that receives dispatched email messages.",
				},
			},
			YAMLTemplatePath: "email_gateway.yaml",
			RequiredEnvVars:  []string{},
		},
		{
			Type:        "whatsapp",
			Name:        "WhatsApp",
			Description: "Connect your AI agent to WhatsApp via the WhatsApp Business Cloud API.",
			Warning:     "Requires a WhatsApp Business Account and Cloud API phone number.",
			Schema: map[string]any{
				"phone_number_id": map[string]any{
					"label":    "Phone Number ID",
					"type":     "string",
					"required": true,
					"secret":   false,
					"order":    1,
					"hint":     "The ID Meta assigns to a WhatsApp number. Find it in WhatsApp Manager after verifying the number.",
					"validation": map[string]any{
						"pattern":     phoneNumberIDPattern,
						"errorMsg":    "Must be the numeric phone number ID from Meta (digits only), not the phone number itself",
						"requiredMsg": "Phone Number ID is required. Find it in WhatsApp Manager after verifying the number.",
					},
				},
				"access_token": map[string]any{
					"label":    "Access Token",
					"type":     "password",
					"required": true,
					"secret":   true,
					"order":    2,
					"hint":     "Authenticates outbound messages sent on the business's behalf. Generate a permanent token in Meta Business Settings under System Users.",
					"validation": map[string]any{
						"minLength":   1,
						"maxLength":   1000,
						"requiredMsg": "Access Token is required. Generate a permanent token in Meta Business Settings under System Users.",
					},
				},
				"app_secret": map[string]any{
					"label":    "App Secret",
					"type":     "password",
					"required": true,
					"secret":   true,
					"order":    3,
					"hint":     "Verifies that incoming WhatsApp messages are genuine. Find it in the Meta app under Settings > Basic > App Secret.",
					"validation": map[string]any{
						"minLength":   1,
						"maxLength":   500,
						"requiredMsg": "App Secret is required. Find it in the Meta app under Settings > Basic > App Secret.",
					},
				},
				"verify_token": map[string]any{
					"label":    "Webhook Verify Token",
					"type":     "password",
					"required": true,
					"secret":   true,
					"order":    4,
					"hint":     "A string created and entered in both this form and Meta's webhook settings to confirm the connection.",
					"validation": map[string]any{
						"minLength":   1,
						"maxLength":   500,
						"requiredMsg": "Webhook Verify Token is required. Create and enter it in both this form and Meta's webhook settings to confirm the connection.",
					},
				},
				"default_agent_name": map[string]any{
					"label":    "Assigned Agent",
					"type":     "agent_select",
					"required": false,
					"secret":   false,
					"order":    5,
					"hint":     "Inbound WhatsApp messages route to the selected agent. If left empty, messages route to the first discovered agent.",
				},
				"disable_file_uploads": map[string]any{
					"label":    "Decline Inbound Files",
					"type":     "boolean",
					"required": false,
					"secret":   false,
					"order":    6,
					"default":  false,
					"hint":     "When selected, inbound images, documents, videos, and voice messages are declined, and the sender is prompted to send text instead. The agent can still send outbound files.",
				},
				SenderIdentityModeKey: map[string]any{
					"label":    "Sender Identity",
					"type":     "select",
					"required": false,
					"secret":   false,
					"order":    9,
					"section":  whatsappIdentitySection,
					"default":  SenderIdentityIDPLookup,
					"options": []map[string]any{
						{"value": SenderIdentityIDPLookup, "label": "Identity provider lookup"},
						{"value": SenderIdentitySystemUser, "label": "Service account"},
					},
					"hint": "How an inbound message is authorized. Identity provider lookup (idp_lookup) matches each sender against a claim the identity provider issues; service account (system_user) authorizes every sender as a single Solace Agent Mesh service account.",
				},
				"run_as": map[string]any{
					"label":              "Run As Service Account",
					"type":               "system_user_select",
					"required":           true,
					"secret":             false,
					"order":              10,
					"section":            whatsappIdentitySection,
					"conditionalDisplay": map[string]string{SenderIdentityModeKey: SenderIdentitySystemUser},
					"clearWhenHidden":    true,
					"hint":               "The Solace Agent Mesh service account that every inbound message authorizes as. Scope its roles to what this WhatsApp number should be able to do.",
				},
				"identity_lookup_key": map[string]any{
					"label":              "Identify Senders By",
					"type":               "select",
					"required":           false,
					"secret":             false,
					"order":              11,
					"section":            whatsappIdentitySection,
					"default":            "phone",
					"conditionalDisplay": map[string]string{SenderIdentityModeKey: SenderIdentityIDPLookup},
					"clearWhenHidden":    true,
					"options": []map[string]any{
						{"value": "phone", "label": "Phone number"},
						{"value": "wa_username", "label": "WhatsApp username"},
					},
					"hint": "What to read from an inbound message to identify the sender and match against the claim below. Phone number is recommended, as a WhatsApp username can be released and re-registered by a different user.",
				},
				"identity_claim_key": map[string]any{
					"label":              "Identity Provider Claim",
					"type":               "string",
					"required":           false,
					"secret":             false,
					"order":              12,
					"section":            whatsappIdentitySection,
					"conditionalDisplay": map[string]string{SenderIdentityModeKey: SenderIdentityIDPLookup},
					"clearWhenHidden":    true,
					"placeholder":        "phone_number",
					"hint":               "The identity provider claim the sender's value is matched against. The claim is recorded at sign-in, so affected users must sign in again for the match to take effect.",
				},
				"contact_request_template": map[string]any{
					"label":              "Contact Request Template",
					"type":               "string",
					"required":           false,
					"secret":             false,
					"order":              13,
					"section":            whatsappIdentitySection,
					"placeholder":        "share_contact_request",
					"showTemplateLookup": true,
					"conditionalDisplay": map[string]string{
						SenderIdentityModeKey: SenderIdentityIDPLookup,
						"identity_lookup_key": whatsapp.IdentityLookupPhone,
					},
					"clearWhenHidden": true,
					"hint":            "The name of an approved WhatsApp template with a Share Contact Info button, offered to senders whose phone number WhatsApp hides. The template body must have no variables. Leave empty to turn those senders away instead.",
				},
				"contact_request_template_language": map[string]any{
					"label":       "Contact Request Template Language",
					"type":        "string",
					"required":    false,
					"secret":      false,
					"order":       14,
					"section":     whatsappIdentitySection,
					"placeholder": "en_US",
					"conditionalDisplay": map[string]string{
						SenderIdentityModeKey: SenderIdentityIDPLookup,
						"identity_lookup_key": whatsapp.IdentityLookupPhone,
					},
					"clearWhenHidden": true,
					"hint":            "The language code of the approved translation to send, matched exactly. Defaults to en_US.",
				},
			},
			YAMLTemplatePath: "whatsapp_gateway.yaml",
			RequiredEnvVars:  []string{},
		},
		eventMeshGatewaySchemaResponse(),
		mcpGatewaySchemaResponse(),
		webhookGatewaySchemaResponse(),
		googleChatGatewaySchemaResponse(),
		agentLinkGatewaySchemaResponse(),
		{
			Type:        "teams",
			Name:        "Microsoft Teams",
			Description: "Connect your AI agent to Microsoft Teams using Bot Framework",
			Warning:     "Requires Azure Bot Service registration and a publicly accessible webhook endpoint",
			Schema: map[string]any{
				"default_agent_name": map[string]any{
					"label":    defaultAgentLabel,
					"type":     "agent_select",
					"required": false,
					"secret":   false,
					"order":    1,
					"default":  builtinOrchestratorName,
					"hint":     "The agent that handles messages when no specific agent is mentioned.",
				},
				"microsoft_app_id": map[string]any{
					"label":    "Microsoft App ID",
					"type":     "string",
					"required": true,
					"secret":   false,
					"order":    2,
					"hint":     "Microsoft Bot ID found in Azure Bot Service, in GUID format.",
					"validation": map[string]any{
						"pattern":  guidPattern,
						"errorMsg": "Must be a valid GUID format (e.g., 12345678-1234-1234-1234-123456789abc)",
					},
				},
				"microsoft_app_password": map[string]any{
					"label":    "Microsoft App Password",
					"type":     "password",
					"required": true,
					"secret":   true,
					"order":    3,
					"hint":     "Required. Client Secret from Azure AD App Registration.",
					"validation": map[string]any{
						"minLength": 1,
					},
				},
				"microsoft_app_tenant_id": map[string]any{
					"label":    "Microsoft App Tenant ID",
					"type":     "string",
					"required": false,
					"secret":   false,
					"order":    4,
					"hint":     "Azure AD Tenant ID for single-tenant bots. Leave empty for multi-tenant.",
					"validation": map[string]any{
						"pattern":  guidPattern,
						"errorMsg": "Must be a valid GUID format (e.g., 12345678-1234-1234-1234-123456789abc)",
					},
				},
				"initial_status_message": map[string]any{
					"label":    "Initial Status Message",
					"type":     "string",
					"required": false,
					"secret":   false,
					"order":    5,
					"default":  "Processing your request...",
					"hint":     "Message shown while processing request. Leave empty to disable.",
				},
				"enable_typing_indicator": map[string]any{
					"label":    "Enable Typing Indicator",
					"type":     "select",
					"required": false,
					"secret":   false,
					"order":    6,
					"default":  "true",
					"hint":     "Show 'bot is typing...' indicator while processing.",
					"options": []map[string]any{
						{"label": "Yes", "value": "true"},
						{"label": "No", "value": "false"},
					},
				},
				"max_download_file_size_mb": map[string]any{
					"label":   "Max File Download Size (MB)",
					"type":    "number",
					"order":   7,
					"default": float64(100),
					"hint":    "Maximum file size in MB that can be downloaded from Teams",
					"validation": map[string]any{
						"min": float64(1),
						"max": float64(500),
					},
				},
			},
			YAMLTemplatePath: "teams_gateway.yaml",
			RequiredEnvVars:  []string{"TEAMS_APP_PASSWORD"},
		},
	}
	for i := range schemas {
		schema, ok := schemas[i].Schema.(map[string]any)
		if !ok {
			continue
		}
		switch schemas[i].Type {
		case "slack":
			maps.Copy(schema, runAsChannelFields(1.5))
		case "teams":
			maps.Copy(schema, runAsChannelFields(1.6))
		}
	}
	return schemas
}

func (s *GatewayService) buildResponse(gw *store.Gateway, deploymentStatus string, latestDeploy *store.GatewayDeployment) *GatewayResponse {
	resp := &GatewayResponse{
		ID:               gw.ID,
		Slug:             gw.Slug,
		DNSID:            derefStr(gw.DNSID),
		Name:             gw.Name,
		Description:      derefStr(gw.Description),
		Type:             gw.Type,
		PublicURL:        derefStr(gw.PublicURL),
		DeploymentStatus: deploymentStatus,
		CreatedBy:        gw.CreatedBy,
		UpdatedBy:        gw.UpdatedBy,
		CreatedTime:      Timestamp(gw.CreatedTime),
		UpdatedTime:      Timestamp(gw.UpdatedTime),
	}

	cardKey := gatewayWireID(gw)
	var card *a2a.AgentCard
	if s.gatewayRegistry != nil {
		card = s.gatewayRegistry.Get(cardKey)
	}

	switch {
	case card != nil && s.gatewayRegistry.Serving(cardKey):
		resp.RuntimeStatus = RunningStatus(nil)
	case card != nil:
		resp.RuntimeStatus = StoppedStatus()
	case deploymentStatus == DisplayStatusDeployed && latestDeploy != nil:
		if time.Since(time.UnixMilli(latestDeploy.UpdatedTime)) < runtimeGracePeriod {
			resp.RuntimeStatus = StartingStatus()
		} else {
			resp.RuntimeStatus = DisconnectedStatus()
		}
	default:
		resp.RuntimeStatus = StoppedStatus()
	}

	if resp.RuntimeStatus.Status() == RuntimeStatusRunning && card != nil {
		resp.RuntimeStatus = RunningStatus(s.gatewayRegistry.RuntimeProblems(cardKey))
	}

	isDeployed := deploymentStatus == DisplayStatusDeployed || deploymentStatus == DisplayStatusDeployFailed

	if isDeployed && latestDeploy != nil && latestDeploy.ConfigHash != nil {
		currentHash := computeGatewayConfigHash(gw)
		if currentHash == *latestDeploy.ConfigHash {
			resp.SyncStatus = strPtr("in_sync")
		} else {
			resp.SyncStatus = strPtr("out_of_sync")
		}
	}

	if isDeployed && latestDeploy != nil {
		deployed, err := decodeDeployedSnapshot[DeployedGatewayInfo](latestDeploy.GatewayDataSnapshot)
		if err != nil && s.logger != nil {
			s.logger.Warn("failed to decode deployed snapshot", "gatewayID", gw.ID, "error", err)
		}
		if deployed != nil {
			deployed.DeployedAt = Timestamp(latestDeploy.CreatedTime)
			deployed.DeployedBy = latestDeploy.CreatedBy
			resp.Deployed = deployed
		}
	}

	if gw.Values != nil {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(*gw.Values), &parsed); err != nil {
			if s.logger != nil {
				s.logger.Warn("failed to decode stored entrypoint values, omitting them from the response",
					"gatewayID", gw.ID, "gatewayType", gw.Type, "error", err)
			}
		} else {
			parsed = normalizeValuesForType(gw.Type, parsed)
			stampMCPImplicitStateRedirection(gw.Type, parsed)
			secretFields := getSecretRegistry().GatewaySecretFields(gw.Type)
			resp.Values = redactSecretFields(parsed, secretFields)
		}
	}

	return resp
}

func computeGatewayConfigHash(gw *store.Gateway) string {
	data := map[string]any{
		"name":        gw.Name,
		"description": derefStr(gw.Description),
		"type":        gw.Type,
		"values":      derefStr(gw.Values),
	}
	b, _ := json.Marshal(data)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *GatewayService) resolveProbeValues(ctx context.Context, gatewayID, typeHint string, reqValues any) (map[string]any, string, error) {
	var storedValues, submittedValues map[string]any

	gatewayType := typeHint
	if gatewayID != "" {
		gw, err := s.gatewayRepo.GetByID(ctx, gatewayID)
		if err != nil {
			return nil, "", fmt.Errorf(errGetGatewayWrap, err)
		}
		if gw == nil {
			return nil, "", &NotFoundError{Resource: "entrypoint", ID: gatewayID}
		}
		if gw.Values != nil {
			if err := json.Unmarshal([]byte(*gw.Values), &storedValues); err != nil {
				return nil, "", fmt.Errorf("unmarshal gateway values: %w", err)
			}
		}
		gatewayType = gw.Type
	}

	if reqValues != nil {
		b, err := json.Marshal(reqValues)
		if err != nil {
			return nil, "", fmt.Errorf(errMarshalValuesWrap, err)
		}
		if err := json.Unmarshal(b, &submittedValues); err != nil {
			return nil, "", fmt.Errorf("unmarshal values: %w", err)
		}
	}

	values := submittedValues
	if storedValues != nil {
		if submittedValues != nil {
			secretFields := getSecretRegistry().GatewaySecretFields(gatewayType)
			merged, err := s.mergeProbeValues(ctx, gatewayID, gatewayType, submittedValues, normalizeValuesForType(gatewayType, storedValues), secretFields)
			if err != nil {
				return nil, "", fmt.Errorf("merge values for test connection: %w", err)
			}
			values = merged
		} else {
			values = storedValues
		}
	}
	return values, gatewayType, nil
}

func (s *GatewayService) mergeProbeValues(ctx context.Context, gatewayID, gatewayType string, submittedValues, normalizedStored map[string]any, secretFields map[string]secretFieldInfo) (map[string]any, error) {
	binding, bound := entrypointProbeBindings[cmp.Or(gatewayType, legacyProbeGatewayType)]
	if !bound {
		return mergeGatewayValuesForUpdate(gatewayType, submittedValues, normalizedStored, secretFields)
	}
	values, err := bindProbeValues(normalizedStored, submittedValues, binding)
	if err != nil {
		logProbeRefusal(ctx, s.logger, values.refusedField, "gatewayID", gatewayID, "gatewayType", gatewayType)
		return nil, err
	}
	if values.mergeable == nil {
		return values.submitted, nil
	}
	return mergeGatewayValuesForUpdate(gatewayType, values.submitted, values.mergeable, secretFields)
}

func (s *GatewayService) TestConnection(ctx context.Context, req TestConnectionRequest) (*TestConnectionResponse, error) {
	values, gatewayType, err := s.resolveProbeValues(ctx, req.GatewayID, req.GatewayType, req.Values)
	if err != nil {
		return nil, onField(err, fieldValues)
	}

	if values == nil {
		return nil, &ValidationError{Message: MsgEntrypointOrValuesRequired, Field: fieldValues}
	}

	if gatewayType == "" {
		gatewayType = legacyProbeGatewayType
	}

	if !gatewayTypeVisible(gatewayType, s.featureRegistry) {
		return nil, errGatewayTypeDisabled(gatewayType)
	}

	switch gatewayType {
	case "event_mesh":
		return s.testEventMeshConnection(ctx, values)
	case "email":
		return s.testEmailConnection(ctx, values)
	case "whatsapp":
		return s.testWhatsAppConnection(ctx, values)
	case "google_chat":
		return s.testGoogleChatConnection(ctx, values), nil
	default:
		return &TestConnectionResponse{Success: true}, nil
	}
}

func (s *GatewayService) testEventMeshConnection(ctx context.Context, values map[string]any) (*TestConnectionResponse, error) {
	brokerURL, _ := values["broker_url"].(string)
	vpnName, _ := values["broker_vpn"].(string)
	username, _ := values["broker_username"].(string)
	password, _ := values["broker_password"].(string)

	if brokerURL == "" {
		return nil, &ValidationError{Message: "broker_url is required in values", Field: fieldValues}
	}

	if err := testBrokerConnection(ctx, brokerURL, vpnName, username, password, probeTLSSkipVerify(values)); err != nil {
		return &TestConnectionResponse{
			Success: false,
			Message: classifyConnectionError(err),
		}, nil
	}

	return &TestConnectionResponse{Success: true}, nil
}

func (s *GatewayService) testEmailConnection(ctx context.Context, values map[string]any) (*TestConnectionResponse, error) {
	cfg := &email.EmailConfig{
		Mailbox:  asString(values, "mailbox"),
		IMAPHost: asString(values, "imap_host"),
		IMAPPort: probeIMAPPort(values),
	}

	authMode, _ := values["auth_mode"].(string)
	switch authMode {
	case "oauth2":
		cfg.OAuth2TenantID = asString(values, "oauth2_tenant_id")
		cfg.OAuth2ClientID = asString(values, "oauth2_client_id")
		cfg.OAuth2ClientSecret = asString(values, "oauth2_client_secret")
	default:
		cfg.IMAPPassword = asString(values, "imap_password")
	}

	if cfg.Mailbox == "" || cfg.IMAPHost == "" {
		return &TestConnectionResponse{
			Success: false,
			Message: "Mailbox and IMAP host are required.",
		}, nil
	}

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := connectIMAP(testCtx, cfg, s.logger); err != nil {
		class, hint := email.ClassifyConnectionError(err)
		s.logger.LogAttrs(ctx, slog.LevelWarn, "test-connection IMAP probe failed",
			slog.String("eventType", "email.test_connection_failed"),
			slog.String("errorClass", string(class)),
			slog.String("authMode", cfg.AuthMode()),
			slog.String("host", cfg.IMAPHost),
			slog.String("mailboxDomain", mailboxDomain(cfg.Mailbox)),
			slog.String("hint", hint),
			slog.String("error", email.SanitizeForLog(err.Error())),
		)
		return &TestConnectionResponse{
			Success:    false,
			Message:    hint,
			ErrorClass: string(class),
			Stage:      "inbound",
		}, nil
	}
	return &TestConnectionResponse{Success: true}, nil
}

func (s *GatewayService) testWhatsAppConnection(ctx context.Context, values map[string]any) (*TestConnectionResponse, error) {
	token := asScalarString(values, "access_token")
	phoneNumberID := asScalarString(values, "phone_number_id")

	if isPlaceholder(token) || isPlaceholder(phoneNumberID) {
		return &TestConnectionResponse{
			Success:    false,
			Message:    "This entrypoint uses substituted values, which only resolve where the entrypoint runs, so its credentials cannot be tested from here.",
			ErrorClass: string(whatsapp.ProbeClassIncomplete),
			Stage:      "outbound",
		}, nil
	}

	res := probeWhatsAppCredentials(ctx, token, phoneNumberID, whatsapp.ProbeOptions{})
	if res.Success {
		return &TestConnectionResponse{Success: true}, nil
	}

	s.logger.LogAttrs(ctx, slog.LevelWarn, "test-connection WhatsApp Graph probe failed",
		slog.String("eventType", "whatsapp.test_connection_failed"),
		slog.String("errorClass", string(res.ErrorClass)),
		slog.Int("metaErrorCode", res.MetaErrorCode),
		slog.Int("metaErrorSubcode", res.MetaErrorSubcode),
	)
	return &TestConnectionResponse{
		Success:    false,
		Message:    res.Hint,
		ErrorClass: string(res.ErrorClass),
		Stage:      "outbound",
	}, nil
}

func (s *GatewayService) testGoogleChatConnection(ctx context.Context, values map[string]any) *TestConnectionResponse {
	req := googlechat.ProbeRequest{
		ServiceAccountKey:  asScalarString(values, "service_account_key"),
		Delivery:           asScalarString(values, "delivery"),
		PubSubSubscription: asScalarString(values, "pubsub_subscription"),
	}
	if isPlaceholder(req.ServiceAccountKey) || isPlaceholder(req.PubSubSubscription) {
		return &TestConnectionResponse{
			Success:    false,
			Message:    "This entrypoint uses substituted values, which only resolve where the entrypoint runs, so its credentials cannot be tested from here.",
			ErrorClass: string(googlechat.ProbeClassIncomplete),
			Stage:      googlechat.ProbeStageOutbound,
		}
	}

	if problems := googleChatAudienceProblems(values); len(problems) > 0 {
		return &TestConnectionResponse{
			Success:    false,
			Message:    problems[0].String(),
			ErrorClass: string(googlechat.ProbeClassIncomplete),
			Stage:      googlechat.ProbeStageInbound,
		}
	}

	res := probeGoogleChat(ctx, req, googlechat.ProbeOptions{})
	if res.Success {
		return &TestConnectionResponse{Success: true, Message: res.Note}
	}
	s.logger.LogAttrs(ctx, slog.LevelWarn, "test-connection Google Chat probe failed",
		slog.String("eventType", "google_chat.test_connection_failed"),
		slog.String("errorClass", string(res.ErrorClass)),
		slog.String("stage", res.Stage),
		slog.Int("googleStatus", res.GoogleStatus),
	)
	return &TestConnectionResponse{
		Success:    false,
		Message:    res.Hint,
		ErrorClass: string(res.ErrorClass),
		Stage:      res.Stage,
	}
}

func emailReplyConfigFromValues(values map[string]any) *email.EmailConfig {
	return &email.EmailConfig{
		Mailbox:            asString(values, "mailbox"),
		IMAPPassword:       asString(values, "imap_password"),
		SMTPHost:           cmp.Or(asString(values, "smtp_host"), "smtp.gmail.com"),
		SMTPPort:           asIntOrDefault(values, "smtp_port", 587),
		OAuth2TenantID:     asString(values, "oauth2_tenant_id"),
		OAuth2ClientID:     asString(values, "oauth2_client_id"),
		OAuth2ClientSecret: asString(values, "oauth2_client_secret"),
	}
}

func (s *GatewayService) runEmailSaveTest(ctx context.Context, name string, values map[string]any) error {
	inboundFn := s.inboundTestFn
	if inboundFn == nil {
		inboundFn = s.testEmailConnection
	}
	resp, err := inboundFn(ctx, values)
	if err != nil {
		return err
	}
	if !resp.Success {
		return s.saveTestValidationError(ctx, name, resp, values)
	}

	if !replyTransportConfigured(values) {
		return nil
	}

	outboundFn := s.outboundTestFn
	if outboundFn == nil {
		outboundFn = s.testEmailReplyConnection
	}
	resp, err = outboundFn(ctx, values)
	if err != nil {
		return err
	}
	if !resp.Success {
		return s.saveTestValidationError(ctx, name, resp, values)
	}
	return nil
}

func replyTransportConfigured(values map[string]any) bool {
	return emailReplyConfigFromValues(values).HasReplyTransport()
}

func (s *GatewayService) saveTestValidationError(
	_ context.Context, name string, resp *TestConnectionResponse, values map[string]any,
) error {
	s.logger.Warn("email save-time test failed",
		"errorClass", resp.ErrorClass,
		"stage", resp.Stage,
		"authMode", asString(values, "auth_mode"),
		"mailboxDomain", mailboxDomain(asString(values, "mailbox")),
		"gatewayName", name,
		"hint", resp.Message,
	)
	var prefix string
	switch resp.Stage {
	case "outbound":
		prefix = "Email replying connection test failed before save"
	case "inbound":
		prefix = "Email fetching connection test failed before save"
	default:
		prefix = "Connection test failed before save"
	}
	msg := prefix + "."
	if resp.Message != "" {
		msg = prefix + ": " + resp.Message
	}
	return &ValidationError{Message: msg}
}

func (s *GatewayService) testEmailReplyConnection(
	ctx context.Context, values map[string]any,
) (*TestConnectionResponse, error) {
	r := email.ProbeReply(ctx, emailReplyConfigFromValues(values), email.ProbeOptions{})
	resp := &TestConnectionResponse{
		Success:    r.Success,
		Message:    r.Hint,
		ErrorClass: string(r.ErrorClass),
	}
	if !r.Success {
		resp.Stage = "outbound"
	}
	return resp, nil
}

func asString(values map[string]any, key string) string {
	v, _ := values[key].(string)
	return v
}

func asScalarString(values map[string]any, key string) string {
	switch v := values[key].(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func mailboxDomain(mailbox string) string {
	_, domain, found := strings.Cut(mailbox, "@")
	if !found {
		return ""
	}
	return domain
}

func asIntOrDefault(values map[string]any, key string, defaultVal int) int {
	switch v := values[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return defaultVal
}

func classifyConnectionError(err error) string {
	lower := strings.ToLower(err.Error())

	switch {
	case strings.Contains(lower, "authentication") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "login failure") || strings.Contains(lower, "invalid credentials"):
		return "Authentication failed. Please check the Client Username and Password."
	case strings.Contains(lower, "message vpn not found") || strings.Contains(lower, "unknown message vpn"):
		return "Message VPN not found. Please verify the Message VPN name."
	case strings.Contains(lower, "could not be resolved") || strings.Contains(lower, "no such host") || strings.Contains(lower, "name resolution") || strings.Contains(lower, "dns"):
		return "Could not resolve the broker host. Please check the Host URI."
	case strings.Contains(lower, "connection refused"):
		return "Connection refused. Please verify the Host URI and port, and that the broker is accepting connections."
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline exceeded"):
		return "Connection timed out. Please verify the Host URI is correct and reachable from this environment."
	case strings.Contains(lower, "unreachable"):
		return "The broker could not be reached. Please verify the Host URI and that the broker is reachable from this environment."
	case strings.Contains(lower, "certificate") || strings.Contains(lower, "x509") || strings.Contains(lower, "tls"):
		return "TLS/certificate error. Please verify the Host URI uses tcps:// and that the broker's certificate is valid."
	case strings.Contains(lower, "not available in this build"):
		return "Broker connection testing is not available in this build."
	default:
		return "Connection failed. Please verify the Host URI, Message VPN, Client Username, and Password."
	}
}

func (s *GatewayService) getDeploymentStatus(ctx context.Context, gatewayID string) string {
	if s.deploymentRepo == nil {
		return DisplayStatusNotDeployed
	}
	latest, err := s.deploymentRepo.GetLatestByGatewayID(ctx, gatewayID)
	if err != nil || latest == nil {
		return DisplayStatusNotDeployed
	}
	return store.MapDeploymentDisplayStatus(latest.Status, latest.Action)
}

var gatewayTypeRegex = regexp.MustCompile(`^[a-z0-9_]+$`)

const MsgEntrypointOrValuesRequired = "either an entrypoint id or values must be provided"

const (
	fieldEntrypointID   = "entrypointId"
	fieldEntrypointType = "entrypointType"
	fieldConnectorID    = "connectorId"
	fieldValues         = "values"
	fieldValuesPrefix   = fieldValues + "."
	fieldPublicURL      = "publicUrl"
	fieldServerURL      = "serverUrl"
	fieldCustomHeaders  = "customHeaders"
)

func onField(err error, field string) error {
	if ve, ok := errors.AsType[*ValidationError](err); ok && ve.Field == "" && len(ve.Details) == 0 {
		filed := &ValidationError{Message: ve.Message, Field: field}
		if withCause, hasCause := errors.AsType[validationErrorWith](err); hasCause {
			return validationErrorWith{filed, withCause.cause}
		}
		return filed
	}
	return err
}

func validatePublicURL(raw string) error {
	if !strings.HasPrefix(raw, "https://") {
		return &ValidationError{Message: "publicUrl must use https protocol", Field: fieldPublicURL}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return &ValidationError{Message: "publicUrl is not a valid URL", Field: fieldPublicURL}
	}
	if u.Host == "" {
		return &ValidationError{Message: "publicUrl is not a valid URL: missing host", Field: fieldPublicURL}
	}
	return nil
}
