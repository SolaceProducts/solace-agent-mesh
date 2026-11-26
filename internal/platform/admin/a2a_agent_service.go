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
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const defaultRemoteAgentTimeoutSeconds = 120

const (
	errGetRemoteAgentWrap = "get remote agent: %w"
	resourceRemoteAgent   = "remote agent"
)

type A2AAgentService struct {
	a2aRepo         *store.A2AAgentRepo
	agentDeployRepo *store.AgentDeploymentRepo
	agentRepo       *store.AgentRepo
	agentRegistry   *platpkg.AgentRegistry
	controlClient   *control.Client
	httpClient      *http.Client
	outboundTimeout time.Duration
	namespace       string
	logger          *slog.Logger
}

const maxA2AOAuthResponseBytes = 1 << 20

const defaultA2AOutboundTimeout = 30 * time.Second

func NewA2AAgentService(
	a2aRepo *store.A2AAgentRepo,
	agentDeployRepo *store.AgentDeploymentRepo,
	agentRepo *store.AgentRepo,
	agentRegistry *platpkg.AgentRegistry,
	controlClient *control.Client,
	httpClient *http.Client,
	namespace string,
	logger *slog.Logger,
) *A2AAgentService {
	if httpClient == nil {
		httpClient = httpClientWithSSRFGuard(nil)
	}
	return &A2AAgentService{
		a2aRepo:         a2aRepo,
		agentDeployRepo: agentDeployRepo,
		agentRepo:       agentRepo,
		agentRegistry:   agentRegistry,
		controlClient:   controlClient,
		httpClient:      httpClient,
		namespace:       namespace,
		logger:          logger,
	}
}

func (s *A2AAgentService) Create(ctx context.Context, req CreateRemoteAgentRequest, userID string) (*RemoteAgentResponse, error) {
	if err := ValidateRemoteAgentCreate(req); err != nil {
		return nil, err
	}

	if err := s.CheckNameAvailable(ctx, req.Name, ""); err != nil {
		return nil, err
	}

	timeout := cmp.Or(req.RequestTimeoutSeconds, defaultRemoteAgentTimeoutSeconds)

	cardAuthType := effectiveAuthType(req.AgentCardAuthType, req.AgentCardAuthConfig)
	agentAuthType := effectiveAuthType(req.AgentAuthType, req.AgentAuthConfig)

	a := &store.A2AAgent{
		AgentURL:              req.AgentURL,
		Name:                  req.Name,
		Description:           strPtr(req.Description),
		AgentCardLocation:     req.AgentCardLocation,
		AgentCardURL:          strPtr(req.AgentCardURL),
		AgentCardAuthType:     strPtr(cardAuthType),
		AgentCardAuthConfig:   marshalStoredAuthConfig(cardAuthType, req.AgentCardAuthConfig),
		AgentCardHeaders:      marshalJSONPtr(req.AgentCardHeaders),
		AgentAuthType:         strPtrOrDefault(agentAuthType, "none"),
		AgentAuthConfig:       cmp.Or(marshalStoredAuthConfig(agentAuthType, req.AgentAuthConfig), strPtr("{}")),
		TaskHeaders:           marshalJSONPtr(req.TaskHeaders),
		RequestTimeoutSeconds: timeout,
		Skills:                marshalJSONPtr(req.Skills),
		InputModes:            marshalJSONPtr(req.InputModes),
		OutputModes:           marshalJSONPtr(req.OutputModes),
		CreatedBy:             userID,
		UpdatedBy:             userID,
	}
	applyCardTrustFields(a, req.ProtocolVersion, req.TrustedCardJWKSURLs, req.RequireSignedCard, true)

	if createErr := s.a2aRepo.CreateWithMirror(ctx, a); createErr != nil {
		return nil, nameConflictOr(createErr, req.Name, "create remote agent")
	}

	return s.buildResponse(a, nil, DisplayStatusNotDeployed), nil
}

func (s *A2AAgentService) CheckNameAvailable(ctx context.Context, name, excludeID string) error {
	byName, err := s.a2aRepo.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("check name uniqueness: %w", err)
	}
	if byName != nil && byName.ID != excludeID {
		return &ConflictError{Message: "a remote agent with this name already exists"}
	}
	return checkAgentNameFree(ctx, s.agentRepo, name, excludeID)
}

func (s *A2AAgentService) CheckUpdatedName(ctx context.Context, name, id string) error {
	err := s.CheckNameAvailable(ctx, name, id)
	conflict, isConflict := errors.AsType[*ConflictError](err)
	if !isConflict {
		return err
	}
	stored, getErr := s.a2aRepo.GetByID(ctx, id)
	if getErr != nil {
		if s.logger != nil {
			s.logger.Warn("could not read the stored name to explain a name conflict", "remoteAgentID", id, "error", getErr)
		}
		return err
	}
	if stored == nil || store.NormalizeName(stored.Name) != store.NormalizeName(name) {
		return err
	}
	return &ConflictError{
		Message:    fmt.Sprintf("another agent already holds the name %q that this remote agent uses; rename this remote agent to save changes", name),
		LogMessage: conflict.Message,
	}
}

func (s *A2AAgentService) GetByID(ctx context.Context, id string) (*RemoteAgentResponse, error) {
	return s.GetByIDWithCard(ctx, id, false)
}

func (s *A2AAgentService) GetByIDWithCard(ctx context.Context, id string, fetchCard bool) (*RemoteAgentResponse, error) {
	a, err := s.a2aRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetRemoteAgentWrap, err)
	}
	if a == nil {
		return nil, &NotFoundError{Resource: resourceRemoteAgent, ID: id}
	}
	latestDeploy := s.getLatestDeploy(ctx, a.ID)
	resp := s.buildResponse(a, latestDeploy, s.deploymentDisplayStatus(ctx, a.ID, latestDeploy))

	if fetchCard && a.AgentCardLocation != "none" {
		card, fetchErr := s.fetchCardUsingStored(ctx, a)
		if fetchErr != nil {
			resp.CardFetchError = fetchErr.Error()
			s.logger.Warn("fetch agent card using stored credentials failed",
				"remoteAgentID", id, "error", LogCause(fetchErr))
		} else {
			resp.CardData = card
		}
	}
	return resp, nil
}

func (s *A2AAgentService) fetchCardUsingStored(ctx context.Context, a *store.A2AAgent) (*FetchAgentCardResponse, error) {
	authType := derefStr(a.AgentCardAuthType)
	var stored map[string]any
	if a.AgentCardAuthConfig != nil {
		stored = unmarshalAuthConfig(*a.AgentCardAuthConfig)
	}
	wire := cardAuthConfigFromStored(authType, stored, s.logger)

	var headers map[string]string
	if a.AgentCardHeaders != nil {
		_ = json.Unmarshal([]byte(*a.AgentCardHeaders), &headers)
	}

	switch a.AgentCardLocation {
	case "well_known":
		var lastErr error
		for _, cardURL := range wellKnownCardURLs(a.AgentURL) {
			resp, status, err := s.fetchCardAt(ctx, cardURL, authType, wire, headers)
			if err == nil {
				return resp, nil
			}
			lastErr = err
			if status != http.StatusNotFound {
				break
			}
		}
		return nil, lastErr
	case "custom":
		if a.AgentCardURL == nil || *a.AgentCardURL == "" {
			return nil, &ValidationError{Message: "agent card URL not available"}
		}
		return s.FetchAgentCard(ctx, FetchAgentCardRequest{
			AgentCardURL:       *a.AgentCardURL,
			AuthenticationType: authType,
			AuthConfig:         wire,
			CustomHeaders:      headers,
		})
	default:
		return nil, &ValidationError{Message: "agent card URL not available"}
	}
}

func applyTaskAndCardFields(a *store.A2AAgent, req UpdateRemoteAgentRequest, replace bool) {
	if replace || req.TaskHeaders != nil {
		a.TaskHeaders = marshalJSONPtr(req.TaskHeaders)
	}
	if replace || req.RequestTimeoutSeconds != 0 {
		a.RequestTimeoutSeconds = cmp.Or(req.RequestTimeoutSeconds, defaultRemoteAgentTimeoutSeconds)
	}
	if replace || req.Skills != nil {
		a.Skills = marshalJSONPtr(req.Skills)
	}
	if replace || req.InputModes != nil {
		a.InputModes = marshalJSONPtr(req.InputModes)
	}
	if replace || req.OutputModes != nil {
		a.OutputModes = marshalJSONPtr(req.OutputModes)
	}
}

func refuseKeptSecretsSentElsewhere(prior, merged *store.A2AAgent, req UpdateRemoteAgentRequest, cardAuthType, agentAuthType string) error {
	cardURL := cardFetchURL(merged)
	resend := keptSecretsSentElsewhere("agentCardAuthConfig", cardAuthType, req.AgentCardAuthConfig, prior.AgentCardAuthConfig, merged.AgentCardAuthConfig, cardURL != "" && cardURL != cardFetchURL(prior))
	resend = append(resend, keptSecretsSentElsewhere("agentAuthConfig", agentAuthType, req.AgentAuthConfig, prior.AgentAuthConfig, merged.AgentAuthConfig, merged.AgentURL != prior.AgentURL)...)
	if len(resend) == 0 {
		return nil
	}
	details := make(map[string][]string, len(resend))
	for _, field := range resend {
		details[field] = []string{secretMustBeResentMessage}
	}
	return &ValidationError{Message: "a stored secret " + secretMustBeResentMessage, Details: details}
}

func cardFetchURL(a *store.A2AAgent) string {
	switch a.AgentCardLocation {
	case "well_known":
		return a.AgentURL
	case "custom":
		return derefStr(a.AgentCardURL)
	}
	return ""
}

const (
	currentWellKnownCardPath = "/.well-known/agent-card.json"
	legacyWellKnownCardPath  = "/.well-known/agent.json"
)

func wellKnownCardURLs(agentURL string) []string {
	base := strings.TrimRight(agentURL, "/")
	return []string{base + currentWellKnownCardPath, base + legacyWellKnownCardPath}
}

func (s *A2AAgentService) ListPaginated(ctx context.Context, pageNumber, pageSize int) (*PaginatedResponse[RemoteAgentResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	agents, total, err := s.a2aRepo.ListPaginated(ctx, pageNumber, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list remote agents: %w", err)
	}

	agentIDs := make([]string, 0, len(agents))
	for _, a := range agents {
		agentIDs = append(agentIDs, a.ID)
	}
	deploys := s.getLatestDeploys(ctx, agentIDs)
	statuses := s.deploymentDisplayStatuses(ctx, agentIDs, deploys)

	responses := make([]RemoteAgentResponse, len(agents))
	for i, a := range agents {
		responses[i] = *s.buildResponse(a, deploys[a.ID], statuses[a.ID])
	}

	return &PaginatedResponse[RemoteAgentResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *A2AAgentService) Update(ctx context.Context, id string, req UpdateRemoteAgentRequest, userID string) (*RemoteAgentResponse, error) {
	return s.update(ctx, id, req, userID, false)
}

func (s *A2AAgentService) Replace(ctx context.Context, id string, req CreateRemoteAgentRequest, userID string) (*RemoteAgentResponse, error) {
	if err := validateRemoteAgentShape(req); err != nil {
		return nil, err
	}
	return s.update(ctx, id, UpdateRemoteAgentRequest(req), userID, true)
}

func (s *A2AAgentService) update(ctx context.Context, id string, req UpdateRemoteAgentRequest, userID string, replace bool) (*RemoteAgentResponse, error) {
	existing, err := s.a2aRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetRemoteAgentWrap, err)
	}
	if existing == nil {
		return nil, &NotFoundError{Resource: resourceRemoteAgent, ID: id}
	}

	if err := ValidateRemoteAgentUpdate(req); err != nil {
		return nil, err
	}
	prior := *existing
	if req.AgentURL != "" {
		existing.AgentURL = req.AgentURL
	}
	if req.Name != "" {
		existing.Name = req.Name
	}
	if replace || req.Description != "" {
		existing.Description = strPtr(req.Description)
	}
	if req.AgentCardLocation != "" {
		existing.AgentCardLocation = req.AgentCardLocation
	}
	if replace || req.AgentCardURL != "" {
		existing.AgentCardURL = strPtr(req.AgentCardURL)
	}
	if replace && req.AgentCardAuthConfig == nil {
		if err := validateRequiredAuthSecrets("agentCardAuth", req.AgentCardAuthType, nil); err != nil {
			return nil, err
		}
		existing.AgentCardAuthType = nil
		existing.AgentCardAuthConfig = nil
	}
	cardAuthType := cmp.Or(
		req.AgentCardAuthType,
		effectiveAuthType("", req.AgentCardAuthConfig),
		derefStr(existing.AgentCardAuthType),
	)
	agentAuthType := cmp.Or(
		req.AgentAuthType,
		effectiveAuthType("", req.AgentAuthConfig),
		derefStr(existing.AgentAuthType),
	)

	if req.AgentCardAuthConfig != nil && cardAuthType != derefStr(existing.AgentCardAuthType) {
		if err := validateRequiredAuthSecrets("agentCardAuth", cardAuthType, req.AgentCardAuthConfig); err != nil {
			return nil, err
		}
	}
	if req.AgentAuthConfig != nil && agentAuthType != derefStr(existing.AgentAuthType) {
		if err := validateRequiredAuthSecrets("agentAuth", agentAuthType, req.AgentAuthConfig); err != nil {
			return nil, err
		}
	}

	priorCardAuthType := derefStr(existing.AgentCardAuthType)

	existing.AgentCardAuthType = strPtr(cardAuthType)
	existing.AgentAuthType = strPtr(agentAuthType)

	if req.AgentCardAuthConfig != nil {
		incomingCardType := effectiveAuthType("", req.AgentCardAuthConfig)
		keepEmptySecrets := incomingCardType != "" && incomingCardType == priorCardAuthType
		if updated := mergeAuthConfigUpdate(cardAuthType, req.AgentCardAuthConfig, existing.AgentCardAuthConfig, keepEmptySecrets); updated != nil {
			existing.AgentCardAuthConfig = updated
		}
	}
	if replace || req.AgentCardHeaders != nil {
		existing.AgentCardHeaders = marshalJSONPtr(req.AgentCardHeaders)
	}
	if req.AgentAuthConfig != nil {
		if updated := mergeAuthConfigUpdate(agentAuthType, req.AgentAuthConfig, existing.AgentAuthConfig, true); updated != nil {
			existing.AgentAuthConfig = updated
		}
	}
	applyTaskAndCardFields(existing, req, replace)
	applyCardTrustFields(existing, req.ProtocolVersion, req.TrustedCardJWKSURLs, req.RequireSignedCard, replace)
	if err := validateCardTrust(existing.AgentCardLocation, existing.RequireSignedCard, storedTrustedCardJWKSURLs(existing)); err != nil {
		return nil, err
	}

	if err := refuseKeptSecretsSentElsewhere(&prior, existing, req, cardAuthType, agentAuthType); err != nil {
		return nil, err
	}

	if err := ValidateRemoteAgentCardSkills(existing.AgentCardLocation, existing.Skills != nil); err != nil {
		return nil, err
	}

	existing.UpdatedBy = userID

	if err := s.CheckUpdatedName(ctx, existing.Name, id); err != nil {
		return nil, err
	}

	if updateErr := s.a2aRepo.UpdateWithMirror(ctx, existing); updateErr != nil {
		return nil, nameConflictOr(updateErr, existing.Name, "update remote agent")
	}

	latestDeploy := s.getLatestDeploy(ctx, id)
	return s.buildResponse(existing, latestDeploy, s.deploymentDisplayStatus(ctx, id, latestDeploy)), nil
}

func (s *A2AAgentService) Delete(ctx context.Context, id string) error {
	existing, err := s.a2aRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetRemoteAgentWrap, err)
	}
	if existing == nil {
		return &NotFoundError{Resource: resourceRemoteAgent, ID: id}
	}
	if s.agentDeployRepo != nil {
		latest, err := s.agentDeployRepo.GetLatestByAgentID(ctx, id)
		if err != nil {
			return fmt.Errorf("get latest remote agent deployment: %w", err)
		}
		if latest != nil && store.MayHaveLiveInstance(latest.Status, latest.Action) {
			return &ValidationError{Message: "cannot delete a deployed remote agent; undeploy it first"}
		}
	}
	if deleteErr := s.a2aRepo.DeleteWithMirror(ctx, id); deleteErr != nil {
		return fmt.Errorf("delete remote agent: %w", deleteErr)
	}
	return nil
}

func (s *A2AAgentService) DeleteWithUndeploy(ctx context.Context, id, userID string, deploySvc *DeploymentService) error {
	existing, err := s.a2aRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetRemoteAgentWrap, err)
	}
	if existing == nil {
		return &NotFoundError{Resource: resourceRemoteAgent, ID: id}
	}
	mayBeLive, err := s.agentRepo.HasPossiblyLiveDeployment(ctx, id)
	if err != nil {
		return err
	}
	if mayBeLive {
		if deploySvc == nil {
			return undeployBlocksDelete(s.logger, "remote agent delete refused: no deployment service to undeploy it", "remoteAgentID", id)
		}
		if err := deploySvc.Undeploy(ctx, id, userID); err != nil {
			return undeployBlocksDelete(s.logger, "remote agent undeploy before delete failed", "remoteAgentID", id)
		}
	}
	return s.Delete(ctx, id)
}

func (s *A2AAgentService) FetchAgentCard(ctx context.Context, req FetchAgentCardRequest) (*FetchAgentCardResponse, error) {
	if len(req.AgentCardURL) < 11 || len(req.AgentCardURL) > 2048 {
		return nil, &ValidationError{Message: "agentCardUrl must be between 11 and 2048 characters"}
	}
	if !strings.HasPrefix(req.AgentCardURL, "http://") && !strings.HasPrefix(req.AgentCardURL, "https://") {
		return nil, &ValidationError{Message: "agentCardUrl must start with http:// or https://"}
	}
	resp, _, err := s.fetchCardAt(ctx, req.AgentCardURL, req.AuthenticationType, req.AuthConfig, req.CustomHeaders)
	return resp, err
}

func (s *A2AAgentService) fetchTimeout() time.Duration {
	if s.outboundTimeout > 0 {
		return s.outboundTimeout
	}
	return defaultA2AOutboundTimeout
}

func outboundFailure(err error, subject, advice, failurePrefix string) error {
	if failure, ok := guardedFetchFailure(err, subject, advice); ok {
		return failure
	}
	return newUnreachableTargetError(failurePrefix, err)
}

func (s *A2AAgentService) fetchCardAt(ctx context.Context, cardURL, authType string, authConfig *CardAuthConfig, headers map[string]string) (*FetchAgentCardResponse, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, 0, &ValidationError{Message: "invalid URL"}
	}
	if authErr := s.addAuthHeaders(httpReq, authType, authConfig); authErr != nil {
		return nil, 0, authErr
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
	defer cancel()
	httpReq = httpReq.WithContext(ctx)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, outboundFailure(err, "agent card URL", "agent card URL", "failed to fetch agent card")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, &ValidationError{Message: fmt.Sprintf("agent card fetch returned status %d", resp.StatusCode)}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, newUnreachableTargetError("failed to read agent card response", err)
	}

	var card map[string]any
	if jsonErr := json.Unmarshal(body, &card); jsonErr != nil {
		return nil, resp.StatusCode, &ValidationError{Message: "agent card response is not valid JSON"}
	}

	name, _ := card["name"].(string)
	description, _ := card["description"].(string)
	if name == "" {
		return nil, resp.StatusCode, &ValidationError{Message: "agent card missing required field: name"}
	}
	if description == "" {
		return nil, resp.StatusCode, &ValidationError{Message: "agent card missing required field: description"}
	}

	skills := card["skills"]
	inputModes := coalesceAny(card["inputModes"], card["input_modes"], card["defaultInputModes"], card["default_input_modes"])
	outputModes := coalesceAny(card["outputModes"], card["output_modes"], card["defaultOutputModes"], card["default_output_modes"])

	return &FetchAgentCardResponse{
		Name:        name,
		Description: description,
		Skills:      skills,
		InputModes:  inputModes,
		OutputModes: outputModes,
		Interfaces:  advertisedInterfaces(body),
		Signed:      cardIsSigned(body),
	}, resp.StatusCode, nil
}

func (s *A2AAgentService) getLatestDeploy(ctx context.Context, a2aAgentID string) *store.AgentDeployment {
	if s.agentDeployRepo == nil {
		return nil
	}
	deploys, _ := s.agentDeployRepo.GetLatestSuccessfulDeploys(ctx, []string{a2aAgentID})
	return deploys[a2aAgentID]
}

func (s *A2AAgentService) deploymentDisplayStatus(ctx context.Context, id string, latestDeploy *store.AgentDeployment) string {
	if s.agentRepo != nil {
		if status, err := s.agentRepo.GetDeploymentStatus(ctx, id); err == nil && status != "" {
			return status
		}
	}
	if latestDeploy != nil {
		return store.MapDeploymentDisplayStatus(latestDeploy.Status, latestDeploy.Action)
	}
	return DisplayStatusNotDeployed
}

func (s *A2AAgentService) deploymentDisplayStatuses(ctx context.Context, ids []string, deploys map[string]*store.AgentDeployment) map[string]string {
	result := make(map[string]string, len(ids))
	if s.agentRepo != nil {
		if statuses, err := s.agentRepo.GetDeploymentStatuses(ctx, ids); err == nil {
			for id, st := range statuses {
				result[id] = st
			}
		}
	}
	for _, id := range ids {
		if _, ok := result[id]; ok {
			continue
		}
		if d := deploys[id]; d != nil {
			result[id] = store.MapDeploymentDisplayStatus(d.Status, d.Action)
		} else {
			result[id] = DisplayStatusNotDeployed
		}
	}
	return result
}

func (s *A2AAgentService) getLatestDeploys(ctx context.Context, a2aAgentIDs []string) map[string]*store.AgentDeployment {
	if s.agentDeployRepo == nil || len(a2aAgentIDs) == 0 {
		return map[string]*store.AgentDeployment{}
	}
	deploys, _ := s.agentDeployRepo.GetLatestSuccessfulDeploys(ctx, a2aAgentIDs)
	if deploys == nil {
		return map[string]*store.AgentDeployment{}
	}
	return deploys
}

func (s *A2AAgentService) buildResponse(a *store.A2AAgent, latestDeploy *store.AgentDeployment, deploymentStatus string) *RemoteAgentResponse {
	if deploymentStatus == "" {
		deploymentStatus = DisplayStatusNotDeployed
	}

	instanceName := A2AAgentInstanceName(a)

	resp := &RemoteAgentResponse{
		ID:                    a.ID,
		AgentURL:              a.AgentURL,
		Name:                  a.Name,
		InstanceName:          instanceName,
		Description:           derefStr(a.Description),
		AgentCardLocation:     a.AgentCardLocation,
		AgentCardURL:          derefStr(a.AgentCardURL),
		AgentCardAuthType:     derefStr(a.AgentCardAuthType),
		AgentAuthType:         derefStr(a.AgentAuthType),
		RequestTimeoutSeconds: a.RequestTimeoutSeconds,
		DeploymentStatus:      deploymentStatus,
		CreatedBy:             a.CreatedBy,
		UpdatedBy:             a.UpdatedBy,
		CreatedTime:           Timestamp(a.CreatedTime),
		UpdatedTime:           Timestamp(a.UpdatedTime),
	}

	resp.AgentCardAuthConfig = redactStoredAuthConfig(a.AgentCardAuthConfig)
	resp.AgentAuthConfig = redactStoredAuthConfig(a.AgentAuthConfig)
	resp.AgentCardHeaders = unmarshalJSONAny(a.AgentCardHeaders)
	resp.TaskHeaders = unmarshalJSONAny(a.TaskHeaders)
	resp.Skills = unmarshalJSONAny(a.Skills)
	resp.InputModes = unmarshalJSONAny(a.InputModes)
	resp.OutputModes = unmarshalJSONAny(a.OutputModes)
	resp.ProtocolVersion = storedProtocolPin(a)
	resp.TrustedCardJWKSURLs = storedTrustedCardJWKSURLs(a)
	resp.RequireSignedCard = a.RequireSignedCard

	if s.agentRegistry != nil {
		if card := s.agentRegistry.Get(instanceName); card != nil {
			resp.RuntimeStatus = RunningStatus(s.agentRegistry.RuntimeProblems(instanceName))
			resp.RemoteProtocol = remoteProtocolStatus(card)
		} else if deploymentStatus == DisplayStatusDeployed {
			resp.RuntimeStatus = DisconnectedStatus()
		} else {
			resp.RuntimeStatus = StoppedStatus()
		}
	} else {
		resp.RuntimeStatus = StoppedStatus()
	}

	isDeployed := deploymentStatus == DisplayStatusDeployed || deploymentStatus == DisplayStatusDeployFailed

	if isDeployed && latestDeploy != nil && latestDeploy.ConfigHash != nil {
		currentHash := computeA2AAgentConfigHash(a)
		if currentHash == *latestDeploy.ConfigHash {
			resp.SyncStatus = strPtr("in_sync")
		} else {
			resp.SyncStatus = strPtr("out_of_sync")
		}
	}

	if isDeployed && latestDeploy != nil && latestDeploy.AgentConfigSnapshot != nil {
		deployed, err := decodeDeployedSnapshot[DeployedRemoteAgentInfo](latestDeploy.AgentConfigSnapshot)
		if err != nil && s.logger != nil {
			s.logger.Error("failed to decode deployed snapshot", "agentID", a.ID, "error", err)
		}
		if deployed != nil {
			deployed.Skills = jsonStringToValue(deployed.Skills)
			deployed.InputModes = jsonStringToValue(deployed.InputModes)
			deployed.OutputModes = jsonStringToValue(deployed.OutputModes)
			deployed.ProtocolVersion = cmp.Or(deployed.ProtocolVersion, ProtocolPinAuto)
			if deployed.TrustedCardJWKSURLs == nil {
				deployed.TrustedCardJWKSURLs = []string{}
			}
			deployed.DeployedAt = Timestamp(latestDeploy.CreatedTime)
			deployed.DeployedBy = latestDeploy.CreatedBy
			resp.Deployed = deployed
		}
	}

	return resp
}

func unmarshalAuthConfig(raw string) map[string]any {
	var result map[string]any
	if json.Unmarshal([]byte(raw), &result) == nil {
		return result
	}
	var inner string
	if json.Unmarshal([]byte(raw), &inner) == nil {
		if json.Unmarshal([]byte(inner), &result) == nil {
			return result
		}
	}
	return nil
}

func normalizeAuthConfig(cfg map[string]any, authType string) {
	switch authType {
	case "apikey":
		cfg["type"] = "static_apikey"
		if v, ok := cfg["api_key"]; ok {
			cfg["token"] = v
			delete(cfg, "api_key")
		}
		if v, ok := cfg["header_name"]; ok {
			cfg["header"] = v
			delete(cfg, "header_name")
		}
	case "bearer":
		cfg["type"] = "static_bearer"
	case "oauth2":
		cfg["type"] = "oauth2_client_credentials"
		if v, ok := cfg["token_endpoint"]; ok {
			cfg["token_url"] = v
			delete(cfg, "token_endpoint")
		}
	case "basic":
		cfg["type"] = "basic"
	default:
		cfg["type"] = authType
	}
}

func computeA2AAgentConfigHash(a *store.A2AAgent) string {
	data := map[string]any{
		"agentUrl":              a.AgentURL,
		"name":                  a.Name,
		"description":           derefStr(a.Description),
		"agentCardLocation":     a.AgentCardLocation,
		"agentCardUrl":          derefStr(a.AgentCardURL),
		"agentAuthType":         derefStr(a.AgentAuthType),
		"requestTimeoutSeconds": a.RequestTimeoutSeconds,
		"skills":                derefStr(a.Skills),
		"inputModes":            derefStr(a.InputModes),
		"outputModes":           derefStr(a.OutputModes),
	}
	addCardTrustToConfigHash(data, a)
	b, _ := json.Marshal(data)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *A2AAgentService) addAuthHeaders(req *http.Request, authType string, config *CardAuthConfig) error {
	switch authType {
	case "none", "":
		return nil
	case "bearer":
		if config == nil || config.BearerToken == "" {
			return &ValidationError{Message: "bearer authentication requires token"}
		}
		req.Header.Set("Authorization", "Bearer "+config.BearerToken)
	case "apikey":
		if config == nil || config.ApikeyValue == "" {
			return &ValidationError{Message: "apikey authentication requires apiKey"}
		}
		header := config.ApikeyHeaderName
		if header == "" {
			header = "X-API-Key"
		}
		req.Header.Set(header, config.ApikeyValue)
	case "oauth2":
		if config == nil || config.OAuth2TokenURL == "" || config.OAuth2ClientID == "" || config.OAuth2ClientSecret == "" {
			return &ValidationError{Message: "oauth2 authentication requires tokenEndpoint, clientId, and clientSecret"}
		}
		token, err := s.fetchOAuth2Token(req.Context(), config)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	case "basic":
		if config == nil || config.BasicUsername == "" || config.BasicPassword == "" {
			return &ValidationError{Message: "basic authentication requires username and password"}
		}
		req.SetBasicAuth(config.BasicUsername, config.BasicPassword)
	default:
		return &ValidationError{Message: fmt.Sprintf("unsupported authentication type: %s", authType)}
	}
	return nil
}

func (s *A2AAgentService) fetchOAuth2Token(ctx context.Context, config *CardAuthConfig) (string, error) {
	data := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {config.OAuth2ClientID},
		"client_secret": {config.OAuth2ClientSecret},
	}
	if config.OAuth2Scope != "" {
		data.Set("scope", config.OAuth2Scope)
	}

	if err := validateOutboundURLField("OAuth2 token URL", config.OAuth2TokenURL); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.OAuth2TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", &ValidationError{Message: "invalid oauth2 token URL"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", outboundFailure(err, "OAuth2 token endpoint", "token endpoint", "oauth2 token request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", &ValidationError{Message: fmt.Sprintf("oauth2 token request returned status %d", resp.StatusCode)}
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxA2AOAuthResponseBytes+1))
	if readErr != nil {
		return "", newUnreachableTargetError("oauth2 token request failed", readErr)
	}
	if len(body) > maxA2AOAuthResponseBytes {
		return "", &ValidationError{Message: "oauth2 token response is too large"}
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(body, &tokenResp) != nil {
		return "", &ValidationError{Message: "failed to parse oauth2 token response"}
	}
	if tokenResp.AccessToken == "" {
		return "", &ValidationError{Message: "oauth2 token response missing access_token"}
	}

	return tokenResp.AccessToken, nil
}

func ValidateRemoteAgentCreate(req CreateRemoteAgentRequest) error {
	return validateRemoteAgentCreate(req)
}

func ValidateRemoteAgentCardSkills(agentCardLocation string, hasSkills bool) error {
	if agentCardLocation == "none" && !hasSkills {
		return &ValidationError{Message: "skills are required when agentCardLocation is 'none'", Field: fieldRemoteAgentSkills}
	}
	return nil
}

func ValidateRemoteAgentCardTrust(agentCardLocation string, requireSignedCard bool, trustedURLs []string) error {
	return validateCardTrust(agentCardLocation, requireSignedCard, trustedURLs)
}

func ValidateRemoteAgentUpdate(req UpdateRemoteAgentRequest) error {
	if err := validateCardTrustRequest(req.ProtocolVersion, req.TrustedCardJWKSURLs); err != nil {
		return err
	}
	if req.AgentURL == "" {
		return nil
	}
	return validateAgentURL(req.AgentURL)
}

func validateRemoteAgentCreate(req CreateRemoteAgentRequest) error {
	if err := validateRemoteAgentShape(req); err != nil {
		return err
	}
	if err := validateRequiredAuthSecrets("agentCardAuth",
		effectiveAuthType(req.AgentCardAuthType, req.AgentCardAuthConfig), req.AgentCardAuthConfig); err != nil {
		return err
	}
	return validateRequiredAuthSecrets("agentAuth",
		effectiveAuthType(req.AgentAuthType, req.AgentAuthConfig), req.AgentAuthConfig)
}

func validateRemoteAgentShape(req CreateRemoteAgentRequest) error {
	if err := validateAgentURL(req.AgentURL); err != nil {
		return err
	}
	if req.AgentCardLocation == "custom" && req.AgentCardURL == "" {
		return &ValidationError{Message: "agentCardUrl is required when agentCardLocation is 'custom'", Field: fieldAgentCardURL}
	}
	if err := ValidateRemoteAgentCardSkills(req.AgentCardLocation, req.Skills != nil); err != nil {
		return err
	}
	if req.AgentAuthConfig == nil {
		return &ValidationError{Message: "agentAuthConfig is required", Field: fieldAgentAuthConfig}
	}
	if err := validateCardTrustRequest(req.ProtocolVersion, req.TrustedCardJWKSURLs); err != nil {
		return err
	}
	return validateCardTrust(req.AgentCardLocation, req.RequireSignedCard != nil && *req.RequireSignedCard, req.TrustedCardJWKSURLs)
}

func validateRequiredAuthSecrets(prefix, authType string, raw any) error {
	if err := missingAuthSecrets(prefix, authType, raw); err != nil {
		return &ValidationError{Message: err.Error(), Field: prefix + "Config"}
	}
	return nil
}

func missingAuthSecrets(prefix, authType string, raw any) error {
	if authType == "" || authType == "none" {
		return nil
	}
	wire := coerceWireAuthConfig(raw)
	if wire == nil {
		return fmt.Errorf("%s: config is required for authType=%q", prefix, authType)
	}
	switch authType {
	case authTypeBearer:
		if wire.BearerToken == "" {
			return fmt.Errorf("%s: token is required for bearer auth", prefix)
		}
	case authTypeAPIKey:
		if wire.ApikeyValue == "" {
			return fmt.Errorf("%s: apiKey is required for apikey auth", prefix)
		}
	case authTypeOAuth2:
		if wire.OAuth2TokenURL == "" || wire.OAuth2ClientID == "" || wire.OAuth2ClientSecret == "" {
			return fmt.Errorf("%s: tokenEndpoint, clientId, and clientSecret are required for oauth2 auth", prefix)
		}
	case authTypeBasic:
		if wire.BasicUsername == "" || wire.BasicPassword == "" {
			return fmt.Errorf("%s: username and password are required for basic auth", prefix)
		}
	default:
		return fmt.Errorf("%s: unknown authType %q; must be one of %s",
			prefix, authType, strings.Join(A2AAuthTypes(), ", "))
	}
	return nil
}

func validateAgentURL(u string) error {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return &ValidationError{Message: "agentUrl must start with http:// or https://", Field: fieldAgentURL}
	}
	return nil
}

func marshalJSONPtr(v any) *string {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	s := string(b)
	if s == "null" {
		return nil
	}
	return &s
}

func decodeDeployedSnapshot[T any](raw *string) (*T, error) {
	if raw == nil {
		return nil, nil
	}
	var d T
	if err := json.Unmarshal([]byte(*raw), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func jsonStringToValue(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	var parsed any
	if json.Unmarshal([]byte(s), &parsed) != nil {
		return v
	}
	return parsed
}

func unmarshalJSONAny(s *string) any {
	if s == nil {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(*s), &v); err != nil {
		return nil
	}
	return v
}

func coalesceAny(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}
