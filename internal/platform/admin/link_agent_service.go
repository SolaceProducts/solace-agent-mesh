// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/egressguard"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	resourceLinkAgent         = "link agent"
	errGetLinkAgentWrap       = "get link agent: %w"
	maxLinkAgentTextField     = 256
	maxLinkAgentPeers         = 64
	maxLinkWakeHeaders        = 32
	maxLinkWakeTimeoutSeconds = 86400

	fieldLinkAgentName          = "name"
	fieldLinkAgentOwner         = "owner"
	fieldLinkAgentTeam          = "team"
	fieldLinkAgentPeers         = "peers"
	fieldLinkAgentTokenSHA256   = "tokenSha256"
	fieldLinkWakeURL            = "wake.url"
	fieldLinkWakeTokenURL       = "wake.tokenUrl"
	fieldLinkWakeClientID       = "wake.clientId"
	fieldLinkWakeClientSecret   = "wake.clientSecret"
	fieldLinkWakeHeaders        = "wake.headers"
	fieldLinkWakeTimeoutSeconds = "wake.timeoutSeconds"

	msgLinkAgentInvalid = "invalid link agent"

	logLinkAgentDeploysNotRead      = "link agents' latest deployments not read; their deployed details are left out"
	logLinkAgentSnapshotUndecodable = "failed to decode deployed link agent snapshot"
	logLinkAgentStatusesNotRead     = "link agents' deployment statuses not read; the latest deployments stand in, and an agent with none reads as not deployed"
)

var undecodableLinkSnapshots droppedLinkAgentReports

var linkAgentPeerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

var linkTokenDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const (
	msgLinkTokenDigestMalformed = "tokenSha256 must be the SHA-256 digest of the agent's token, as 64 lowercase hexadecimal characters"
	msgLinkTokenDigestOfNothing = "tokenSha256 is the digest of an empty token; check that the token was set when its digest was computed"
	msgLinkTokenDigestInUse     = "tokenSha256 is the digest of a token another link agent already uses; issue this link agent a token of its own"
)

type LinkAgentService struct {
	linkAgents    *store.LinkAgentRepo
	agentRepo     *store.AgentRepo
	gatewayRepo   *store.GatewayRepo
	deployments   *store.AgentDeploymentRepo
	agentRegistry *platpkg.AgentRegistry
	networking    *GatewayNetworkingService
	logger        *slog.Logger
}

func NewLinkAgentService(
	linkAgents *store.LinkAgentRepo,
	agentRepo *store.AgentRepo,
	gatewayRepo *store.GatewayRepo,
	deployments *store.AgentDeploymentRepo,
	agentRegistry *platpkg.AgentRegistry,
	networking *GatewayNetworkingService,
	logger *slog.Logger,
) *LinkAgentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &LinkAgentService{
		linkAgents:    linkAgents,
		agentRepo:     agentRepo,
		gatewayRepo:   gatewayRepo,
		deployments:   deployments,
		agentRegistry: agentRegistry,
		networking:    networking,
		logger:        logger,
	}
}

func (s *LinkAgentService) Create(ctx context.Context, req CreateLinkAgentRequest, userID string) (*LinkAgentResponse, string, error) {
	agent := &store.LinkAgent{ID: idgen.New(), CreatedBy: userID, UpdatedBy: userID}
	if err := s.applyRequest(ctx, agent, req, nil, false); err != nil {
		return nil, "", err
	}
	token := ""
	if agent.TokenSHA256 == "" {
		issued, err := issueLinkAgentToken(agent)
		if err != nil {
			return nil, "", err
		}
		token = issued
	}
	if err := s.checkAdapterAccepts(agent); err != nil {
		return nil, "", err
	}
	if err := checkAgentNameFree(ctx, s.agentRepo, agent.Name, ""); err != nil {
		return nil, "", err
	}
	if err := s.linkAgents.Create(ctx, agent); err != nil {
		return nil, "", linkAgentWriteError(err, agent.Name, "create link agent")
	}
	s.logger.Info("link agent created", "linkAgentID", agent.ID, "entrypointID", agent.EntrypointID)
	return s.buildResponse(ctx, agent, nil, DisplayStatusNotDeployed, map[string]linkAgentConnect{}), token, nil
}

func linkAgentWriteError(err error, name, op string) error {
	if store.IsLinkAgentTokenViolation(err) {
		return tokenDigestInUse()
	}
	return nameConflictOr(err, name, op)
}

func tokenDigestInUse() *ValidationError {
	return &ValidationError{
		Message: msgLinkAgentInvalid,
		Details: map[string][]string{fieldLinkAgentTokenSHA256: {msgLinkTokenDigestInUse}},
	}
}

func issueLinkAgentToken(agent *store.LinkAgent) (string, error) {
	token, err := apikey.Generate()
	if err != nil {
		return "", fmt.Errorf("generate link agent token: %w", err)
	}
	agent.TokenSHA256 = apikey.Hash(token)
	agent.TokenPrefix = apikey.DisplayPrefix(token)
	return token, nil
}

func (s *LinkAgentService) GetByID(ctx context.Context, id string) (*LinkAgentResponse, error) {
	agent, err := s.existing(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.currentResponse(ctx, agent), nil
}

func (s *LinkAgentService) existing(ctx context.Context, id string) (*store.LinkAgent, error) {
	agent, err := s.linkAgents.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetLinkAgentWrap, err)
	}
	if agent == nil {
		return nil, &NotFoundError{Resource: resourceLinkAgent, ID: id}
	}
	return agent, nil
}

func (s *LinkAgentService) currentResponse(ctx context.Context, agent *store.LinkAgent) *LinkAgentResponse {
	deploys := s.latestSuccessfulDeploys(ctx, []string{agent.ID})
	statuses := s.deploymentStatuses(ctx, []string{agent.ID}, deploys)
	return s.buildResponse(ctx, agent, deploys[agent.ID], statuses[agent.ID], map[string]linkAgentConnect{})
}

func (s *LinkAgentService) ListPaginated(ctx context.Context, pageNumber, pageSize int) (*PaginatedResponse[LinkAgentResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	agents, total, err := s.linkAgents.ListPaginated(ctx, pageNumber, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list link agents: %w", err)
	}
	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.ID
	}
	deploys := s.latestSuccessfulDeploys(ctx, ids)
	statuses := s.deploymentStatuses(ctx, ids, deploys)
	connects := map[string]linkAgentConnect{}
	responses := make([]LinkAgentResponse, len(agents))
	for i, a := range agents {
		responses[i] = *s.buildResponse(ctx, a, deploys[a.ID], statuses[a.ID], connects)
	}
	return &PaginatedResponse[LinkAgentResponse]{Data: responses, Meta: NewPaginationMeta(pageNumber, pageSize, total)}, nil
}

func (s *LinkAgentService) Replace(ctx context.Context, id string, req UpdateLinkAgentRequest, userID string, actorScopes []string) (*LinkAgentResponse, error) {
	stored, err := s.existing(ctx, id)
	if err != nil {
		return nil, err
	}
	if !keepsStoredDigest(req.TokenSHA256, stored.TokenSHA256) && !auth.HasScope(actorScopes, rbac.ScopeAgentBuilderDeploy) {
		return nil, &ForbiddenError{Message: msgInsufficientPermissions}
	}
	mayBeLive, err := s.agentRepo.HasPossiblyLiveDeployment(ctx, id)
	if err != nil {
		return nil, err
	}
	updated := *stored
	if err := s.applyRequest(ctx, &updated, CreateLinkAgentRequest(req), stored, mayBeLive); err != nil {
		return nil, err
	}
	if err := s.checkAdapterAccepts(&updated); err != nil {
		return nil, err
	}
	if err := checkAgentNameFree(ctx, s.agentRepo, updated.Name, id); err != nil {
		return nil, err
	}
	updated.UpdatedBy = userID
	write := s.linkAgents.Update
	if updated.TokenSHA256 != stored.TokenSHA256 {
		write = s.linkAgents.UpdateWithToken
	}
	if err := write(ctx, &updated); err != nil {
		if errors.Is(err, store.ErrLinkAgentNotFound) {
			return nil, &NotFoundError{Resource: resourceLinkAgent, ID: id}
		}
		return nil, linkAgentWriteError(err, updated.Name, "update link agent")
	}
	if updated.TokenSHA256 != stored.TokenSHA256 {
		s.logger.Info("link agent token digest replaced", "linkAgentID", id)
	}
	return s.currentResponse(ctx, &updated), nil
}

func (s *LinkAgentService) GetSecretsDiff(ctx context.Context, id string, candidate LinkAgentSecretsCandidate) (map[string]SecretFieldDiffResult, error) {
	stored, err := s.existing(ctx, id)
	if err != nil {
		return nil, err
	}
	storedSecret := ""
	var storedHeaders map[string]string
	if stored.Wake != nil {
		storedSecret, storedHeaders = stored.Wake.ClientSecret, stored.Wake.Headers
	}
	diff := map[string]SecretFieldDiffResult{
		fieldLinkAgentTokenSHA256: {Matches: keepsStoredDigest(candidate.TokenSHA256, stored.TokenSHA256)},
		fieldLinkWakeClientSecret: {Matches: keepsStoredSecret(candidate.WakeClientSecret, storedSecret)},
	}
	if candidate.WakeHeaders != nil {
		diff[fieldLinkWakeHeaders] = SecretFieldDiffResult{Matches: keepsStoredHeaders(candidate.WakeHeaders, storedHeaders)}
	}
	return diff, nil
}

func keepsStoredHeaders(sent, stored map[string]string) bool {
	if len(sent) != len(stored) {
		return false
	}
	for name, value := range sent {
		storedValue, found := headerNamed(stored, name)
		if !found || (value != redactedSentinel && subtle.ConstantTimeCompare([]byte(value), []byte(storedValue)) != 1) {
			return false
		}
	}
	return true
}

func headerNamed(headers map[string]string, name string) (string, bool) {
	for stored, value := range headers {
		if strings.EqualFold(stored, name) {
			return value, true
		}
	}
	return "", false
}

func keepsStoredDigest(sent, stored string) bool {
	return sent == "" || subtle.ConstantTimeCompare([]byte(sent), []byte(stored)) == 1
}

func keepsStoredSecret(sent, stored string) bool {
	return isKeptSecret(sent) || subtle.ConstantTimeCompare([]byte(sent), []byte(stored)) == 1
}

type LinkAgentTokenRotation struct {
	Agent             *LinkAgentResponse
	Token             string
	Deployment        *AgentDeploymentResponse
	DeploymentRefusal string
}

func (s *LinkAgentService) RotateToken(ctx context.Context, id, userID string, deploySvc *DeploymentService) (*LinkAgentTokenRotation, error) {
	agent, err := s.existing(ctx, id)
	if err != nil {
		return nil, err
	}
	token, err := issueLinkAgentToken(agent)
	if err != nil {
		return nil, err
	}
	if err := s.linkAgents.SetToken(ctx, id, agent.TokenSHA256, agent.TokenPrefix, userID); err != nil {
		if errors.Is(err, store.ErrLinkAgentNotFound) {
			return nil, &NotFoundError{Resource: resourceLinkAgent, ID: id}
		}
		return nil, fmt.Errorf("rotate link agent token: %w", err)
	}
	agent = s.reread(ctx, agent, userID)
	s.logger.Info("link agent token rotated", "linkAgentID", id)
	rotation := &LinkAgentTokenRotation{Token: token}
	rotation.Deployment, rotation.DeploymentRefusal = s.activateRotatedToken(ctx, id, userID, deploySvc)
	rotation.Agent = s.currentResponse(ctx, agent)
	return rotation, nil
}

func (s *LinkAgentService) reread(ctx context.Context, rotated *store.LinkAgent, userID string) *store.LinkAgent {
	fresh, err := s.linkAgents.GetByID(ctx, rotated.ID)
	if err != nil || fresh == nil {
		s.logger.Warn("rotated link agent not read back; its update time may be stale in the response", "linkAgentID", rotated.ID, "error", err)
		rotated.UpdatedBy = userID
		return rotated
	}
	return fresh
}

const (
	msgRotatedTokenNotDeployed = "the new token is stored and the link agent could not be deployed again; send an update deployment to make it take effect"
	msgNoDeploymentService     = "the new token is stored and no deployment service is available; send an update deployment to make it take effect"
)

func (s *LinkAgentService) activateRotatedToken(ctx context.Context, id, userID string, deploySvc *DeploymentService) (*AgentDeploymentResponse, string) {
	delivered, err := s.isDelivered(ctx, id)
	if err != nil {
		s.logger.Error("rotated link agent token not activated: its deployed state could not be read", "linkAgentID", id, "error", err)
		return nil, msgRotatedTokenNotDeployed
	}
	if !delivered {
		return nil, ""
	}
	if deploySvc == nil {
		s.logger.Warn("rotated link agent token not activated: no deployment service", "linkAgentID", id)
		return nil, msgNoDeploymentService
	}
	deployment, err := deploySvc.Create(ctx, CreateAgentDeploymentRequest{AgentID: id, Action: s.redeployAction(ctx, id)}, userID)
	if err != nil {
		s.logger.Warn("rotated link agent token not activated: its update deployment did not start", "linkAgentID", id, "error", LogCause(err))
		return nil, refusalSentence(err)
	}
	return deployment, ""
}

func (s *LinkAgentService) isDelivered(ctx context.Context, id string) (bool, error) {
	deployment, err := deliveredLinkAgentDeployment(ctx, s.linkAgents.DB(), id)
	if err != nil {
		return false, err
	}
	return deployment != nil && deployment.Action != DeployActionUndeploy, nil
}

func (s *LinkAgentService) redeployAction(ctx context.Context, id string) string {
	status, err := s.agentRepo.GetDeploymentStatus(ctx, id)
	if err == nil && (status == DisplayStatusDeployed || status == DisplayStatusDeployFailed) {
		return DeployActionUpdate
	}
	return DeployActionDeploy
}

func refusalSentence(err error) string {
	if v, ok := errors.AsType[*ValidationError](err); ok {
		return v.Message
	}
	if v, ok := errors.AsType[*UnavailableError](err); ok {
		return v.Message
	}
	if v, ok := errors.AsType[*ConflictError](err); ok {
		return v.Message
	}
	return msgRotatedTokenNotDeployed
}

func (s *LinkAgentService) Delete(ctx context.Context, id string) error {
	if _, err := s.existing(ctx, id); err != nil {
		return err
	}
	mayBeLive, err := s.agentRepo.HasPossiblyLiveDeployment(ctx, id)
	if err != nil {
		return err
	}
	if mayBeLive {
		return &ValidationError{Message: "cannot delete a deployed link agent; undeploy it first"}
	}
	if err := s.linkAgents.Delete(ctx, id); err != nil {
		if errors.Is(err, store.ErrLinkAgentNotFound) {
			return &NotFoundError{Resource: resourceLinkAgent, ID: id}
		}
		return fmt.Errorf("delete link agent: %w", err)
	}
	s.logger.Info("link agent deleted", "linkAgentID", id)
	return nil
}

func (s *LinkAgentService) DeleteWithUndeploy(ctx context.Context, id, userID string, deploySvc *DeploymentService) error {
	if _, err := s.existing(ctx, id); err != nil {
		return err
	}
	mayBeLive, err := s.agentRepo.HasPossiblyLiveDeployment(ctx, id)
	if err != nil {
		return err
	}
	if mayBeLive {
		if deploySvc == nil {
			return undeployBlocksDelete(s.logger, "link agent delete refused: no deployment service to undeploy it", "linkAgentID", id)
		}
		if err := deploySvc.Undeploy(ctx, id, userID); err != nil {
			return undeployBlocksDelete(s.logger, "link agent undeploy before delete failed", "linkAgentID", id, "error", LogCause(err))
		}
	}
	return s.Delete(ctx, id)
}

func (s *LinkAgentService) applyRequest(ctx context.Context, agent *store.LinkAgent, req CreateLinkAgentRequest, stored *store.LinkAgent, mayBeLive bool) error {
	details := map[string][]string{}
	problem := func(field, message string) { details[field] = append(details[field], message) }

	agent.Name = strings.TrimSpace(req.Name)
	switch {
	case agent.Name == "":
		problem(fieldLinkAgentName, "name is required")
	case len(agent.Name) > maxLinkAgentTextField:
		problem(fieldLinkAgentName, fmt.Sprintf("name is longer than %d bytes", maxLinkAgentTextField))
	}
	agent.Description = req.Description
	agent.Enabled = req.Enabled == nil || *req.Enabled
	agent.ShareUserProfile = req.ShareUserProfile
	agent.Owner = boundedLinkText(fieldLinkAgentOwner, req.Owner, problem)
	agent.Team = boundedLinkText(fieldLinkAgentTeam, req.Team, problem)
	agent.Peers = linkAgentPeers(agent.ID, req.Peers, problem)
	if digest := suppliedLinkTokenDigest(req.TokenSHA256, problem); digest != "" && digest != agent.TokenSHA256 {
		agent.TokenSHA256, agent.TokenPrefix = digest, ""
		if err := s.checkTokenDigestFree(ctx, agent, problem); err != nil {
			return err
		}
	}

	entrypointErr := s.checkEntrypoint(ctx, req.EntrypointID, stored, mayBeLive, problem)
	if entrypointErr != nil {
		return entrypointErr
	}
	agent.EntrypointID = req.EntrypointID

	var storedWake *store.LinkAgentWake
	if stored != nil {
		storedWake = stored.Wake
	}
	agent.Wake = linkAgentWake(req.Wake, storedWake, problem)

	if len(details) > 0 {
		return &ValidationError{Message: msgLinkAgentInvalid, Details: details}
	}
	return nil
}

func IsLinkTokenDigest(digest string) bool {
	return linkTokenDigestPattern.MatchString(digest)
}

var DigestOfAnEmptyToken = apikey.Hash("")

func suppliedLinkTokenDigest(sent string, problem func(field, message string)) string {
	if sent == "" {
		return ""
	}
	switch {
	case !IsLinkTokenDigest(sent):
		problem(fieldLinkAgentTokenSHA256, msgLinkTokenDigestMalformed)
		return ""
	case sent == DigestOfAnEmptyToken:
		problem(fieldLinkAgentTokenSHA256, msgLinkTokenDigestOfNothing)
		return ""
	}
	return sent
}

func (s *LinkAgentService) checkTokenDigestFree(ctx context.Context, agent *store.LinkAgent, problem func(field, message string)) error {
	inUse, err := s.linkAgents.TokenDigestHeldByAnother(ctx, agent.TokenSHA256, agent.ID)
	if err != nil {
		return err
	}
	if !inUse {
		if inUse, err = s.tokenDigestServedForAnother(ctx, agent.TokenSHA256, agent.ID); err != nil {
			return err
		}
	}
	if inUse {
		problem(fieldLinkAgentTokenSHA256, msgLinkTokenDigestInUse)
	}
	return nil
}

func (s *LinkAgentService) tokenDigestServedForAnother(ctx context.Context, digest, exceptID string) (bool, error) {
	ids, err := s.linkAgents.IDs(ctx)
	if err != nil {
		return false, err
	}
	delivered, err := deliveredLinkAgentDeployments(ctx, s.linkAgents.DB(), ids)
	if err != nil {
		return false, err
	}
	for agentID, deployment := range delivered {
		if agentID == exceptID {
			continue
		}
		if snapshot, ok := readLinkAgentSnapshot(deployment.AgentConfigSnapshot); ok && snapshot.Agent[linkRuntimeTokenDigestKey] == digest {
			return true, nil
		}
	}
	return false, nil
}

func boundedLinkText(field, value string, problem func(field, message string)) string {
	value = strings.TrimSpace(value)
	if len(value) > maxLinkAgentTextField {
		problem(field, fmt.Sprintf("%s is longer than %d bytes", field, maxLinkAgentTextField))
	}
	return value
}

func linkAgentPeers(agentID string, requested []string, problem func(field, message string)) []string {
	peers := make([]string, 0, len(requested))
	if len(requested) > maxLinkAgentPeers {
		problem(fieldLinkAgentPeers, fmt.Sprintf("peers lists more than %d agents", maxLinkAgentPeers))
		return peers
	}
	self := linkAgentMeshName(agentID)
	for _, peer := range requested {
		switch {
		case !linkAgentPeerPattern.MatchString(peer):
			problem(fieldLinkAgentPeers, fmt.Sprintf("peer %q is not an agent's name on the mesh: letters, digits, '-' and '_', starting with a letter or digit", peer))
		case peer == self:
			problem(fieldLinkAgentPeers, "a link agent cannot list itself as a peer")
		case slices.Contains(peers, peer):
			problem(fieldLinkAgentPeers, fmt.Sprintf("peer %q is listed more than once", peer))
		default:
			peers = append(peers, peer)
		}
	}
	return peers
}

func (s *LinkAgentService) checkEntrypoint(ctx context.Context, entrypointID string, stored *store.LinkAgent, mayBeLive bool, problem func(field, message string)) error {
	if _, err := uuid.Parse(entrypointID); err != nil || len(entrypointID) != canonicalUUIDLen {
		problem(fieldEntrypointID, "entrypointId must be the id of an agent_link entrypoint")
		return nil
	}
	gw, err := s.gatewayRepo.GetByID(ctx, entrypointID)
	if err != nil {
		return fmt.Errorf("get the link agent's entrypoint: %w", err)
	}
	switch {
	case gw == nil || gw.Type != platpkg.AgentLinkGatewayType:
		problem(fieldEntrypointID, "entrypointId must be the id of an agent_link entrypoint")
	case stored != nil && stored.EntrypointID != entrypointID && mayBeLive:
		problem(fieldEntrypointID, "undeploy the link agent before moving it to another entrypoint")
	}
	return nil
}

func linkAgentWake(req *LinkAgentWakeRequest, stored *store.LinkAgentWake, problem func(field, message string)) *store.LinkAgentWake {
	if req == nil {
		return nil
	}
	wake := &store.LinkAgentWake{
		URL:            strings.TrimSpace(req.URL),
		TokenURL:       strings.TrimSpace(req.TokenURL),
		ClientID:       strings.TrimSpace(req.ClientID),
		Scope:          strings.TrimSpace(req.Scope),
		TimeoutSeconds: req.TimeoutSeconds,
		OnStart:        req.OnStart == nil || *req.OnStart,
	}
	wake.Headers = linkWakeHeaders(req.Headers, wake.URL, stored, problem)
	if reason := insecureURLReason(wake.URL, "wake.headers", wakeURLQueryParameter); reason != "" {
		problem(fieldLinkWakeURL, "wake.url "+reason)
	}
	usesOAuth := wake.TokenURL != "" || wake.ClientID != "" || !isKeptSecret(req.ClientSecret)
	if usesOAuth {
		wake.ClientSecret = linkWakeClientSecret(req.ClientSecret, wake, stored, problem)
		if reason := insecureURLReason(wake.TokenURL, "wake.clientId and wake.clientSecret", ""); reason != "" {
			problem(fieldLinkWakeTokenURL, "wake.tokenUrl "+reason)
		}
		if wake.ClientID == "" {
			problem(fieldLinkWakeClientID, "wake.clientId is required with wake.tokenUrl")
		}
	}
	checkLinkWakeHeaders(wake.Headers, wake.TokenURL != "", problem)
	if wake.TimeoutSeconds < 0 || wake.TimeoutSeconds > maxLinkWakeTimeoutSeconds {
		problem(fieldLinkWakeTimeoutSeconds, fmt.Sprintf("wake.timeoutSeconds must be between 1 and %d, or left out for the default", maxLinkWakeTimeoutSeconds))
	}
	return wake
}

func isKeptSecret(sent string) bool {
	return sent == "" || sent == redactedSentinel
}

func linkWakeClientSecret(sent string, wake, stored *store.LinkAgentWake, problem func(field, message string)) string {
	if !isKeptSecret(sent) {
		return sent
	}
	if stored == nil || stored.ClientSecret == "" {
		problem(fieldLinkWakeClientSecret, "wake.clientSecret is required with wake.tokenUrl")
		return ""
	}
	if stored.TokenURL != wake.TokenURL || stored.URL != wake.URL {
		problem(fieldLinkWakeClientSecret, secretMustBeResentMessage)
		return ""
	}
	return stored.ClientSecret
}

func linkWakeHeaders(sent map[string]string, wakeURL string, stored *store.LinkAgentWake, problem func(field, message string)) map[string]string {
	if len(sent) == 0 {
		return nil
	}
	headers := make(map[string]string, len(sent))
	for _, name := range slices.Sorted(maps.Keys(sent)) {
		value := sent[name]
		if value == redactedSentinel {
			value = keptLinkWakeHeader(name, wakeURL, stored, problem)
		}
		headers[name] = value
	}
	return headers
}

func keptLinkWakeHeader(name, wakeURL string, stored *store.LinkAgentWake, problem func(field, message string)) string {
	value, ok := storedLinkWakeHeader(stored, name)
	switch {
	case !ok:
		problem(fieldLinkWakeHeaders, fmt.Sprintf("wake.headers: the value of %q is %s but none is stored; send its value", name, redactedSentinel))
	case stored.URL != wakeURL:
		problem(fieldLinkWakeHeaders, fmt.Sprintf("wake.headers: the value of %q %s", name, secretMustBeResentMessage))
	default:
		return value
	}
	return redactedSentinel
}

func storedLinkWakeHeader(stored *store.LinkAgentWake, name string) (string, bool) {
	if stored == nil {
		return "", false
	}
	for storedName, value := range stored.Headers {
		if strings.EqualFold(storedName, name) {
			return value, true
		}
	}
	return "", false
}

func insecureURLReason(raw, credentialsGoIn, versionParameter string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "must be an absolute URL"
	}
	if u.User != nil {
		return "must not carry a user name or password; send credentials in " + credentialsGoIn
	}
	if reason := queryReason(u, credentialsGoIn, versionParameter); reason != "" {
		return reason
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	isLoopbackHTTP := u.Scheme == "http" && (host == "localhost" || ip.IsLoopback())
	if u.Scheme != "https" && !isLoopbackHTTP {
		return "must use https"
	}
	if ip == nil {
		return ""
	}
	if blocked, why := egressguard.BlockedAddress(ip, true); blocked {
		return "must not point at " + why
	}
	return ""
}

const wakeURLQueryParameter = "api-version"

var versionShaped = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func queryReason(u *url.URL, credentialsGoIn, versionParameter string) string {
	if u.RawQuery == "" {
		if u.ForceQuery {
			return "must not end with an empty query string"
		}
		return ""
	}
	noQuery := "must not carry a query string; send credentials in " + credentialsGoIn
	if versionParameter == "" {
		return noQuery
	}
	onlyVersion := "must carry no query parameter other than " + versionParameter + "; send credentials in " + credentialsGoIn
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "has a query string that cannot be read"
	}
	var versions []string
	for _, name := range slices.Sorted(maps.Keys(params)) {
		if !strings.EqualFold(name, versionParameter) {
			return onlyVersion
		}
		versions = append(versions, params[name]...)
	}
	switch {
	case len(versions) == 0:
		return "must not carry an empty query string"
	case len(versions) > 1:
		return "may carry " + versionParameter + " only once"
	case !versionShaped.MatchString(versions[0]):
		return "must carry " + versionParameter + " as a version such as 2025-05-01 or v1"
	}
	return ""
}

var linkWakeHeadersSetByTheWakeCall = []string{"Host", "Content-Length", "Transfer-Encoding", "Content-Type"}

func checkLinkWakeHeaders(headers map[string]string, usesTokenURL bool, problem func(field, message string)) {
	if len(headers) > maxLinkWakeHeaders {
		problem(fieldLinkWakeHeaders, fmt.Sprintf("wake.headers has more than %d headers", maxLinkWakeHeaders))
		return
	}
	seen := make(map[string]bool, len(headers))
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		canonical := strings.ToLower(name)
		switch {
		case !httpguts.ValidHeaderFieldName(name):
			problem(fieldLinkWakeHeaders, fmt.Sprintf("wake.headers: %q is not a valid header name", name))
		case seen[canonical]:
			problem(fieldLinkWakeHeaders, fmt.Sprintf("wake.headers: %q repeats another header name; header names are case-insensitive", name))
		case slices.ContainsFunc(linkWakeHeadersSetByTheWakeCall, func(reserved string) bool { return strings.EqualFold(reserved, name) }):
			problem(fieldLinkWakeHeaders, fmt.Sprintf("wake.headers: %q is reserved for the wake call and cannot be sent", name))
		case usesTokenURL && strings.EqualFold(name, "Authorization"):
			problem(fieldLinkWakeHeaders, "wake.headers: \"Authorization\" cannot be sent with wake.tokenUrl, which sends its own bearer token")
		case !httpguts.ValidHeaderFieldValue(headers[name]):
			problem(fieldLinkWakeHeaders, fmt.Sprintf("wake.headers: the value of %q is not a valid header value", name))
		}
		seen[canonical] = true
	}
}

func (s *LinkAgentService) checkAdapterAccepts(agent *store.LinkAgent) error {
	_, err := agentlink.ParseConfig(map[string]any{
		agentLinkPlatformManagedKey: true,
		agentLinkAgentsKey:          []any{linkAgentRuntimeConfig(agent)},
	})
	if err == nil {
		return nil
	}
	s.logger.Warn("link agent settings passed the platform's checks and were refused by the agent link", "linkAgentID", agent.ID, "error", err)
	return &ValidationError{Message: "the agent link does not accept these settings"}
}

func (s *LinkAgentService) latestSuccessfulDeploys(ctx context.Context, ids []string) map[string]*store.AgentDeployment {
	if s.deployments == nil || len(ids) == 0 {
		return map[string]*store.AgentDeployment{}
	}
	deploys, err := s.deployments.GetLatestSuccessfulDeploys(ctx, ids)
	if err != nil {
		s.logger.Warn(logLinkAgentDeploysNotRead, "linkAgentCount", len(ids), "error", err)
		return map[string]*store.AgentDeployment{}
	}
	if deploys == nil {
		return map[string]*store.AgentDeployment{}
	}
	return deploys
}

func (s *LinkAgentService) deploymentStatuses(ctx context.Context, ids []string, deploys map[string]*store.AgentDeployment) map[string]string {
	result := make(map[string]string, len(ids))
	statuses, err := s.agentRepo.GetDeploymentStatuses(ctx, ids)
	if err != nil {
		s.logger.Warn(logLinkAgentStatusesNotRead, "linkAgentCount", len(ids), "error", err)
	}
	maps.Copy(result, statuses)
	for _, id := range ids {
		if _, known := result[id]; known {
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

type linkAgentConnect struct {
	url  string
	path string
}

func (s *LinkAgentService) connectDetails(ctx context.Context, entrypointID string, known map[string]linkAgentConnect) linkAgentConnect {
	if connect, ok := known[entrypointID]; ok {
		return connect
	}
	connect := s.readConnectDetails(ctx, entrypointID)
	known[entrypointID] = connect
	return connect
}

func (s *LinkAgentService) readConnectDetails(ctx context.Context, entrypointID string) linkAgentConnect {
	gw, err := s.gatewayRepo.GetByID(ctx, entrypointID)
	if err != nil || gw == nil {
		if err != nil {
			s.logger.Warn("link agent's entrypoint not read; its connect details are left out", "entrypointID", entrypointID, "error", err)
		}
		return linkAgentConnect{}
	}
	connect := linkAgentConnect{path: "/gw/" + gatewayWireID(gw) + AgentLinkConnectPath}
	if s.networking != nil && s.networking.domain != "" {
		if resp, netErr := s.networking.networkingResponse(gw, getAPIPaths(gw.Type, ""), ""); netErr == nil {
			connect.url = webSocketURL(resp.WebhookURL)
		}
		return connect
	}
	if base := strings.TrimRight(configbuilder.ResolveEnv("WEBUI_FRONTEND_SERVER_URL", ""), "/"); base != "" {
		connect.url = webSocketURL(base + connect.path)
	}
	return connect
}

func webSocketURL(httpURL string) string {
	if rest, ok := strings.CutPrefix(httpURL, "https://"); ok {
		return "wss://" + rest
	}
	if rest, ok := strings.CutPrefix(httpURL, "http://"); ok {
		return "ws://" + rest
	}
	return ""
}

func (s *LinkAgentService) buildResponse(ctx context.Context, a *store.LinkAgent, latestDeploy *store.AgentDeployment, deploymentStatus string, connects map[string]linkAgentConnect) *LinkAgentResponse {
	if deploymentStatus == "" {
		deploymentStatus = DisplayStatusNotDeployed
	}
	connect := s.connectDetails(ctx, a.EntrypointID, connects)
	resp := &LinkAgentResponse{
		ID:               a.ID,
		Name:             a.Name,
		InstanceName:     linkAgentMeshName(a.ID),
		Description:      a.Description,
		EntrypointID:     a.EntrypointID,
		ConnectURL:       connect.url,
		ConnectPath:      connect.path,
		TokenPrefix:      a.TokenPrefix,
		Enabled:          a.Enabled,
		Owner:            a.Owner,
		Team:             a.Team,
		Peers:            append([]string{}, a.Peers...),
		ShareUserProfile: a.ShareUserProfile,
		Wake:             redactedLinkWake(a.Wake),
		DeploymentStatus: deploymentStatus,
		RuntimeStatus:    s.runtimeStatus(a.ID, deploymentStatus),
		CreatedBy:        a.CreatedBy,
		UpdatedBy:        a.UpdatedBy,
		CreatedTime:      Timestamp(a.CreatedTime),
		UpdatedTime:      Timestamp(a.UpdatedTime),
	}
	isDeployed := deploymentStatus == DisplayStatusDeployed || deploymentStatus == DisplayStatusDeployFailed
	if !isDeployed || latestDeploy == nil {
		return resp
	}
	resp.SyncStatus = linkAgentSyncStatus(a, latestDeploy)
	deployed, err := decodeDeployedSnapshot[DeployedLinkAgentInfo](latestDeploy.AgentDataSnapshot)
	if err != nil {
		if undecodableLinkSnapshots.due(a.ID, latestDeploy.ID, time.Now()) {
			s.logger.Error(logLinkAgentSnapshotUndecodable, "linkAgentID", a.ID, "deploymentID", latestDeploy.ID, "error", err)
		}
	}
	if deployed != nil {
		if deployed.Peers == nil {
			deployed.Peers = []string{}
		}
		deployed.DeployedAt = Timestamp(latestDeploy.CreatedTime)
		deployed.DeployedBy = latestDeploy.CreatedBy
		resp.Deployed = deployed
	}
	return resp
}

func linkAgentSyncStatus(a *store.LinkAgent, deployed *store.AgentDeployment) *string {
	if deployed.ConfigHash == nil {
		return nil
	}
	if computeLinkAgentConfigHash(a) == *deployed.ConfigHash {
		return new("in_sync")
	}
	return new("out_of_sync")
}

func (s *LinkAgentService) runtimeStatus(agentID, deploymentStatus string) RuntimeStatus {
	if s.agentRegistry != nil {
		instance := linkAgentMeshName(agentID)
		if s.agentRegistry.Get(instance) != nil {
			return RunningStatus(s.agentRegistry.RuntimeProblems(instance))
		}
	}
	if deploymentStatus == DisplayStatusDeployed {
		return DisconnectedStatus()
	}
	return StoppedStatus()
}

func redactedLinkWake(w *store.LinkAgentWake) *LinkAgentWakeResponse {
	if w == nil {
		return nil
	}
	resp := &LinkAgentWakeResponse{
		URL:            w.URL,
		TokenURL:       w.TokenURL,
		ClientID:       w.ClientID,
		Scope:          w.Scope,
		Headers:        map[string]string{},
		TimeoutSeconds: EffectiveLinkWakeTimeout(w.TimeoutSeconds),
		OnStart:        w.OnStart,
	}
	for name := range w.Headers {
		resp.Headers[name] = redactedSentinel
	}
	if w.ClientSecret != "" {
		resp.ClientSecret = redactedSentinel
	}
	return resp
}
