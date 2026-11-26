// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type toolAuthControlCaller interface {
	Custom(ctx context.Context, name, method, customPath string, body json.RawMessage) (json.RawMessage, error)
}

const (
	toolAuthStateTTL = 30 * time.Minute

	toolAuthCallTimeout = 5 * time.Second

	msgAgentToolAuthUnavailable = "agent is not running or does not support tool authorization"
)

var toolAuthTargetAgentPattern = regexp.MustCompile(`^[\w\-.]{1,128}$`)

type agentToolAuthInfo struct {
	CredentialKey string    `json:"credentialKey"`
	DisplayName   string    `json:"displayName"`
	AuthType      string    `json:"authType"`
	Interactive   bool      `json:"interactive"`
	Status        string    `json:"status"`
	ExpiresAt     time.Time `json:"expiresAt"`
	GrantRef      string    `json:"grantRef"`
}

type ToolAuthCredential struct {
	CredentialKey    string     `json:"credentialKey"`
	DisplayName      string     `json:"displayName,omitempty"`
	AuthType         string     `json:"authType,omitempty"`
	Interactive      bool       `json:"interactive"`
	Status           string     `json:"status"`
	ExpiresAt        *Timestamp `json:"expiresAt,omitempty"`
	LastAuthorizedBy string     `json:"lastAuthorizedBy,omitempty"`
	LastAuthorizedAt *Timestamp `json:"lastAuthorizedAt,omitempty"`
}

type ToolAuthAgent struct {
	AgentName string               `json:"agentName"`
	IsTarget  bool                 `json:"isTarget"`
	AddedBy   string               `json:"addedBy,omitempty"`
	NodeIDs   []string             `json:"nodeIds,omitempty"`
	Reachable bool                 `json:"reachable"`
	Tools     []ToolAuthCredential `json:"tools"`
}

type ToolAuthChecklist struct {
	RunAs               string          `json:"runAs"`
	Agents              []ToolAuthAgent `json:"agents"`
	AllowListIsWildcard bool            `json:"allowListIsWildcard"`
	TotalAgents         int             `json:"totalAgents"`
	TotalRunningAgents  int             `json:"totalRunningAgents"`
	AgentScanLimit      int             `json:"agentScanLimit"`
}

type agentToolAuthRequest struct {
	UserID        string `json:"userId"`
	CredentialKey string `json:"credentialKey,omitempty"`
}

type ToolAuthBegun struct {
	AuthURI       string    `json:"authUri"`
	CredentialKey string    `json:"credentialKey"`
	ExpiresAt     Timestamp `json:"expiresAt"`
}

func toolAuthPrincipal(exp *store.EvalExperiment) string {
	if exp.RunAs != nil && *exp.RunAs != "" {
		return *exp.RunAs
	}
	return rbac.EvalSystemUserSub
}

func refuseReservedRunAsWithOwnRemedy(principal string) error {
	if principal == rbac.EvalSystemUserSub || !rbac.IsReservedSystemUserSub(principal) {
		return nil
	}
	return &ValidationError{
		Field:   "runAs",
		Message: fmt.Sprintf("this experiment runs as %q, a built-in service account that other Agent Mesh features run as, so its tool credentials belong to them and cannot be authorized or disconnected from here. Point the experiment's runAs at a service account of its own", principal),
	}
}

func toolAuthUnavailable(cause error) error {
	return &UnavailableError{Message: msgAgentToolAuthUnavailable, Cause: cause}
}

type AgentRefusedToolAuthError struct{ Unavailable *UnavailableError }

func (e *AgentRefusedToolAuthError) Error() string { return e.Unavailable.Error() }

func (e *AgentRefusedToolAuthError) Unwrap() error { return e.Unavailable }

func (h *evalHandler) mapToolAuthControlError(err error, targetAgent, credentialKey string) error {
	unavailable := func(cause error) error {
		h.logger.Warn("tool auth control call failed",
			"agentName", targetAgent,
			"credentialKey", credentialKey,
			"error", cause,
		)
		return toolAuthUnavailable(cause)
	}
	var ce *control.ClientError
	if !errors.As(err, &ce) {
		return unavailable(err)
	}
	switch {
	case ce.Code == control.ErrNotFound && strings.HasPrefix(ce.Message, "app not found"):
		return unavailable(err)
	case ce.Code == control.ErrNotFound && strings.Contains(ce.Message, control.InstanceNotRunning):
		return unavailable(err)
	case ce.Code == control.ErrNotFound && credentialKey != "" && strings.HasPrefix(ce.Message, "credential key"):
		return &NotFoundError{Resource: "credential", ID: credentialKey}
	case ce.Code == control.ErrNotFound:
		return &NotFoundError{Resource: "agent instance", ID: targetAgent}
	default:
		const (
			refusalOutdatedRuntime = "outdatedRuntime"
			refusalCannotManage    = "instanceCannotManage"
			refusalAgentReported   = "agentReported"
		)
		refusalClass := refusalAgentReported
		switch {
		case strings.HasPrefix(ce.Message, "unsupported"):
			refusalClass = refusalOutdatedRuntime
		case strings.Contains(ce.Message, "does not support tool auth management"):
			refusalClass = refusalCannotManage
		}
		h.logger.Warn("agent refused tool auth control request",
			"agentName", targetAgent,
			"credentialKey", credentialKey,
			"controlCode", ce.Code,
			"refusalClass", refusalClass,
		)
		switch refusalClass {
		case refusalOutdatedRuntime:
			return &AgentRefusedToolAuthError{Unavailable: &UnavailableError{
				Message: msgAgentToolAuthUnavailable +
					"; if the agent is running, upgrade its runtime to a version with delegated tool authorization",
				Cause: err,
			}}
		case refusalCannotManage:
			return &ValidationError{
				Field:   "agentName",
				Message: fmt.Sprintf("%q cannot manage tool credentials: %s", targetAgent, ce.Message),
			}
		default:
			return &AgentRefusedToolAuthError{Unavailable: &UnavailableError{Message: msgAgentToolAuthUnavailable, Cause: err}}
		}
	}
}

func (h *evalHandler) toolAuthControlReady() bool {
	return h.toolAuthControl != nil && h.agentResolver != nil
}

func (h *evalHandler) resolveToolAuthRouting(ctx context.Context, agentName string) (string, error) {
	if !h.toolAuthControlReady() {
		return "", toolAuthUnavailable(nil)
	}
	routing, err := h.agentResolver.ResolveAgentRoutingName(ctx, agentName)
	if err != nil {
		return "", fmt.Errorf("resolve agent routing name: %w", err)
	}
	return routing, nil
}

func (h *evalHandler) toolAuthAgentNames(ctx context.Context, agentName string) (canonical, routing string, err error) {
	matches := h.meshAgentMatches(agentName)
	if len(matches) > 1 {
		return "", "", &ValidationError{
			Field:   "agentName",
			Message: fmt.Sprintf("agentName %q matches more than one running agent; address the agent by its exact name", agentName),
		}
	}
	if len(matches) == 1 {
		return matches[0].DisplayName, matches[0].RoutingName, nil
	}
	routing, err = h.resolveToolAuthRouting(ctx, agentName)
	if err != nil {
		return "", "", err
	}
	return agentName, routing, nil
}

func (h *evalHandler) callTargetToolAuth(ctx context.Context, target toolAuthTarget, action string) (json.RawMessage, error) {
	raw, err := h.callAgentInstanceToolAuth(ctx, target.routingName, action, target.principal, target.credentialKey)
	if err != nil {
		return nil, h.mapToolAuthControlError(err, target.agentName, target.credentialKey)
	}
	return raw, nil
}

func (h *evalHandler) callAgentInstanceToolAuth(ctx context.Context, routing, action, principal, credentialKey string) (json.RawMessage, error) {
	body, err := json.Marshal(agentToolAuthRequest{UserID: principal, CredentialKey: credentialKey})
	if err != nil {
		return nil, fmt.Errorf("marshal toolAuth %s request: %w", action, err)
	}
	ctx, cancel := context.WithTimeout(ctx, toolAuthCallTimeout)
	defer cancel()
	return h.toolAuthControl.Custom(ctx, platpkg.AWEBindingApp, "post", "instances/"+routing+"/toolAuth/"+action, body)
}

func (h *evalHandler) queryAgentToolAuthItems(ctx context.Context, routing, principal string) (items []agentToolAuthInfo, ok bool) {
	raw, err := h.callAgentInstanceToolAuth(ctx, routing, "query", principal, "")
	if err != nil {
		if isExpectedToolAuthQueryFailure(err) {
			h.logger.Debug("tool auth query failed", "routingName", routing, "error", err)
		} else {
			h.logger.Warn("tool auth query failed", "routingName", routing, "error", err)
		}
		return nil, false
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		h.logger.Warn("failed to decode tool auth query response", "routingName", routing, "error", err)
		return nil, false
	}
	return items, true
}

func isExpectedToolAuthQueryFailure(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ce *control.ClientError
	return errors.As(err, &ce) && ce.Code == control.ErrNotFound
}

func (h *evalHandler) toolAuthChecklist(ctx context.Context, exp *store.EvalExperiment) (ToolAuthChecklist, error) {
	if !h.toolAuthControlReady() {
		return ToolAuthChecklist{}, toolAuthUnavailable(nil)
	}
	graph, err := h.toolAuthGraph(ctx, exp)
	if err != nil {
		return ToolAuthChecklist{}, err
	}
	attribution, err := store.ListToolAuthAttribution(ctx, h.db, exp.ID)
	if err != nil {
		h.logger.Warn("failed to read tool auth attribution", "experimentId", exp.ID, "error", err)
		attribution = nil
	}
	principal := toolAuthPrincipal(exp)
	nodes := graph.Agents
	if len(nodes) > toolAuthMaxAgentFanOut {
		h.logger.Warn("tool auth checklist fan-out truncated",
			"experimentId", exp.ID,
			"graphAgents", len(nodes),
			"queried", toolAuthMaxAgentFanOut,
		)
		nodes = nodes[:toolAuthMaxAgentFanOut]
	}
	totalAgents := len(graph.Agents)
	totalRunningAgents := 0
	if h.mesh != nil {
		totalRunningAgents = len(h.mesh.MeshAgents())
	}
	agents := make([]ToolAuthAgent, len(nodes))
	var group errgroup.Group
	group.SetLimit(toolAuthControlConcurrency)
	for i, node := range nodes {
		group.Go(func() error {
			agents[i] = h.queryAgentToolAuth(ctx, node, principal, attribution)
			return nil
		})
	}
	_ = group.Wait()
	return ToolAuthChecklist{
		RunAs:               principal,
		Agents:              agents,
		AllowListIsWildcard: graph.AllowListIsWildcard,
		TotalAgents:         totalAgents,
		TotalRunningAgents:  totalRunningAgents,
		AgentScanLimit:      toolAuthMaxAgentFanOut,
	}, nil
}

func (h *evalHandler) queryAgentToolAuth(
	ctx context.Context,
	node toolAuthGraphNode,
	principal string,
	attribution map[store.ToolAuthAttributionKey]store.ToolAuthAttribution,
) ToolAuthAgent {
	resp := ToolAuthAgent{
		AgentName: node.AgentName,
		IsTarget:  node.IsTarget,
		AddedBy:   node.AddedBy,
		NodeIDs:   node.NodeIDs,
		Tools:     []ToolAuthCredential{},
	}
	_, routing, err := h.toolAuthAgentNames(ctx, node.AgentName)
	if err != nil {
		h.logger.Warn("tool auth routing lookup failed for delegation graph agent", "agentName", node.AgentName, "error", err)
		return resp
	}
	items, ok := h.queryAgentToolAuthItems(ctx, routing, principal)
	if !ok {
		return resp
	}
	resp.Reachable = true
	for _, item := range items {
		out := ToolAuthCredential{
			CredentialKey: item.CredentialKey,
			DisplayName:   item.DisplayName,
			AuthType:      item.AuthType,
			Interactive:   item.Interactive,
			Status:        item.Status,
		}
		if !item.ExpiresAt.IsZero() {
			ts := ToTimestamp(item.ExpiresAt.UnixMilli())
			out.ExpiresAt = &ts
		}
		if att, ok := attribution[store.ToolAuthAttributionKey{AgentName: node.AgentName, CredentialKey: item.CredentialKey}]; ok {
			out.LastAuthorizedBy = att.AuthorizedBy
			ts := ToTimestamp(att.AuthorizedTime)
			out.LastAuthorizedAt = &ts
		}
		resp.Tools = append(resp.Tools, out)
	}
	return resp
}

type toolAuthTarget struct {
	experimentID  string
	agentName     string
	routingName   string
	credentialKey string
	principal     string
}

const (
	toolAuthControlConcurrency = 8
	toolAuthMaxAgentFanOut     = 60
)

func (h *evalHandler) toolAuthCandidateAgents(ctx context.Context, exp *store.EvalExperiment) ([]string, error) {
	if !h.toolAuthControlReady() {
		return nil, toolAuthUnavailable(nil)
	}
	principal := toolAuthPrincipal(exp)

	var mesh []toolAuthMeshAgent
	if h.mesh != nil {
		mesh = h.mesh.MeshAgents()
	}
	if len(mesh) > toolAuthMaxAgentFanOut {
		h.logger.Warn("tool auth candidate scan truncated",
			"experimentId", exp.ID,
			"meshAgents", len(mesh),
			"scanned", toolAuthMaxAgentFanOut,
		)
		mesh = mesh[:toolAuthMaxAgentFanOut]
	}

	interactive := make([]bool, len(mesh))
	var group errgroup.Group
	group.SetLimit(toolAuthControlConcurrency)
	for i, agent := range mesh {
		group.Go(func() error {
			interactive[i] = h.agentHasInteractiveCredential(ctx, agent, principal)
			return nil
		})
	}
	_ = group.Wait()

	candidates := make([]string, 0, len(mesh))
	for i, agent := range mesh {
		if interactive[i] {
			candidates = append(candidates, agent.DisplayName)
		}
	}
	return candidates, nil
}

func (h *evalHandler) agentHasInteractiveCredential(ctx context.Context, agent toolAuthMeshAgent, principal string) bool {
	items, ok := h.queryAgentToolAuthItems(ctx, agent.RoutingName, principal)
	if !ok {
		return false
	}
	return slices.ContainsFunc(items, func(item agentToolAuthInfo) bool { return item.Interactive })
}

func (h *evalHandler) resolveToolAuthTarget(ctx context.Context, exp *store.EvalExperiment, agentName, credentialKey string) (toolAuthTarget, error) {
	credentialKey = strings.TrimSpace(credentialKey)
	agentName = strings.TrimSpace(agentName)
	missing := map[string][]string{}
	if agentName == "" {
		missing["agentName"] = []string{"agentName is required"}
	}
	if credentialKey == "" {
		missing["credentialKey"] = []string{"credentialKey is required"}
	}
	if len(missing) > 0 {
		return toolAuthTarget{}, &ValidationError{Message: "invalid tool auth request", Details: missing}
	}
	principal := toolAuthPrincipal(exp)
	if err := refuseReservedRunAsWithOwnRemedy(principal); err != nil {
		return toolAuthTarget{}, err
	}
	if _, err := eval.NormalizeRunAs(principal); err != nil {
		return toolAuthTarget{}, err
	}
	canonical, routing, err := h.toolAuthAgentNames(ctx, agentName)
	if err != nil {
		return toolAuthTarget{}, err
	}
	if err := h.authorizeToolAuthAgent(ctx, exp, canonical); err != nil {
		return toolAuthTarget{}, err
	}
	return toolAuthTarget{
		experimentID:  exp.ID,
		agentName:     canonical,
		routingName:   routing,
		credentialKey: credentialKey,
		principal:     principal,
	}, nil
}

func (h *evalHandler) toolAuthGraph(ctx context.Context, exp *store.EvalExperiment) (toolAuthGraph, error) {
	pinnedRows, err := store.ListToolAuthAgents(ctx, h.db, exp.ID)
	if err != nil {
		return toolAuthGraph{}, err
	}
	pinned := make([]toolAuthGraphNode, 0, len(pinnedRows))
	for _, row := range pinnedRows {
		pinned = append(pinned, toolAuthGraphNode{AgentName: row.AgentName, AddedBy: row.AddedBy})
	}
	if exp.IsWorkflowTarget() {
		return h.workflowToolAuthGraph(ctx, exp, pinned)
	}
	return discoverToolAuthGraph(ctx, h.canonicalToolAuthName(ctx, exp.TargetAgent), pinned, h.peerFilters, h.mesh)
}

func (h *evalHandler) workflowToolAuthGraph(ctx context.Context, exp *store.EvalExperiment, pinned []toolAuthGraphNode) (toolAuthGraph, error) {
	seeds := toolAuthGraphSeeds{target: h.canonicalToolAuthName(ctx, exp.TargetAgent), incomplete: h.workflowNodes == nil}
	if h.workflowNodes != nil {
		nodeAgents, complete, err := h.workflowNodes.WorkflowNodeAgents(ctx, exp.TargetAgent)
		if err != nil {
			return toolAuthGraph{}, fmt.Errorf("list workflow node agents: %w", err)
		}
		seeds.incomplete = !complete
		for _, agent := range nodeAgents {
			seeds.agents = append(seeds.agents, toolAuthGraphNode{AgentName: h.canonicalToolAuthName(ctx, agent.AgentName), NodeIDs: agent.NodeIDs})
		}
	}
	return discoverToolAuthGraphFrom(ctx, seeds, pinned, h.peerFilters, h.mesh)
}

func (h *evalHandler) canonicalToolAuthName(ctx context.Context, name string) string {
	if canonical, _, err := h.toolAuthAgentNames(ctx, name); err == nil {
		return canonical
	}
	return name
}

func (h *evalHandler) authorizeToolAuthAgent(ctx context.Context, exp *store.EvalExperiment, agentName string) error {
	if store.NormalizeName(agentName) == store.NormalizeName(exp.TargetAgent) {
		return nil
	}
	graph, err := h.toolAuthGraph(ctx, exp)
	if err != nil {
		return err
	}
	if !h.graphAllows(graph, agentName) {
		return errToolAuthAgentOutsideGraph
	}
	return nil
}

var errToolAuthAgentOutsideGraph = &ValidationError{
	Field:   "agentName",
	Message: "agentName must be the experiment's target agent or an agent it delegates to",
}

func (h *evalHandler) graphAllows(graph toolAuthGraph, agentName string) bool {
	if containsToolAuthAgent(graph.Agents, agentName) {
		return true
	}
	if !graph.AllowListIsWildcard {
		return false
	}
	agent, running := h.meshAgent(agentName)
	return running && graph.AdmitsUnenumerated(agent)
}

func containsToolAuthAgent(nodes []toolAuthGraphNode, agentName string) bool {
	wanted := store.NormalizeName(agentName)
	for _, node := range nodes {
		if store.NormalizeName(node.AgentName) == wanted {
			return true
		}
	}
	return false
}

func (h *evalHandler) meshAgent(agentName string) (toolAuthMeshAgent, bool) {
	matches := h.meshAgentMatches(agentName)
	if len(matches) == 0 {
		return toolAuthMeshAgent{}, false
	}
	return matches[0], true
}

func (h *evalHandler) meshAgentMatches(agentName string) []toolAuthMeshAgent {
	if h.mesh == nil {
		return nil
	}
	wanted := store.NormalizeName(agentName)
	var matches []toolAuthMeshAgent
	for _, agent := range h.mesh.MeshAgents() {
		if store.NormalizeName(agent.DisplayName) != wanted && store.NormalizeName(agent.RoutingName) != wanted {
			continue
		}
		if agent.RoutingName == agentName {
			return []toolAuthMeshAgent{agent}
		}
		if agent.DisplayName == agentName {
			matches = append([]toolAuthMeshAgent{agent}, matches...)
			continue
		}
		matches = append(matches, agent)
	}
	return matches
}

type ToolAuthPinnedAgent struct {
	AgentName string    `json:"agentName"`
	AddedBy   string    `json:"addedBy"`
	AddedAt   Timestamp `json:"addedAt"`
}

func (h *evalHandler) pinToolAuthAgent(ctx context.Context, exp *store.EvalExperiment, agentName, userID string) (ToolAuthPinnedAgent, error) {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return ToolAuthPinnedAgent{}, &ValidationError{Field: "agentName", Message: "agentName is required"}
	}
	graph, err := h.toolAuthGraph(ctx, exp)
	if err != nil {
		return ToolAuthPinnedAgent{}, err
	}
	if !graph.AllowListIsWildcard {
		return ToolAuthPinnedAgent{}, &ValidationError{Field: "agentName", Message: "agents can only be added when the target agent's delegates could not all be listed automatically"}
	}
	agent, running := h.meshAgent(agentName)
	if !running {
		return ToolAuthPinnedAgent{}, &ValidationError{Field: "agentName", Message: "agentName must name a running agent"}
	}
	if !graph.AdmitsUnenumerated(agent) {
		return ToolAuthPinnedAgent{}, &ValidationError{Field: "agentName", Message: "agentName names an agent the target agent is configured not to delegate to"}
	}
	if containsToolAuthAgent(graph.Agents, agent.DisplayName) || containsToolAuthAgent(graph.Agents, agent.RoutingName) {
		return ToolAuthPinnedAgent{}, &ConflictError{Message: fmt.Sprintf("agent %q is already on the checklist", agent.DisplayName)}
	}
	addedTime := time.Now().UnixMilli()
	if err := store.AddToolAuthAgent(ctx, h.db, exp.ID, agent.DisplayName, userID, addedTime); err != nil {
		return ToolAuthPinnedAgent{}, err
	}
	h.logToolAuthAgentAudit(userID, "pinAgent", exp.ID, agent.DisplayName)
	return ToolAuthPinnedAgent{
		AgentName: agent.DisplayName,
		AddedBy:   userID,
		AddedAt:   ToTimestamp(addedTime),
	}, nil
}

func (h *evalHandler) unpinToolAuthAgent(ctx context.Context, exp *store.EvalExperiment, agentName, userID string) error {
	agentName = strings.TrimSpace(agentName)
	if agent, running := h.meshAgent(agentName); running {
		agentName = agent.DisplayName
	}
	removed, err := store.RemoveToolAuthAgent(ctx, h.db, exp.ID, agentName)
	if err != nil {
		return err
	}
	if !removed {
		return &NotFoundError{Resource: "pinned agent", ID: agentName}
	}
	h.logToolAuthAgentAudit(userID, "unpinAgent", exp.ID, agentName)
	return nil
}

func (h *evalHandler) beginToolAuth(ctx context.Context, target toolAuthTarget, userID string) (ToolAuthBegun, error) {
	raw, err := h.callTargetToolAuth(ctx, target, "begin")
	if err != nil {
		return ToolAuthBegun{}, err
	}
	var authData a2a.AuthenticationRequiredData
	if err := json.Unmarshal(raw, &authData); err != nil {
		return ToolAuthBegun{}, fmt.Errorf("decode toolAuth begin response: %w", err)
	}
	if err := h.registerToolAuthState(ctx, &authData, target.principal, target.routingName); err != nil {
		return ToolAuthBegun{}, err
	}
	h.logToolAuthAudit(userID, "begin", target)
	h.recordToolAuthAttribution(ctx, target.experimentID, target.agentName, target.credentialKey, userID)
	return ToolAuthBegun{
		AuthURI:       authData.AuthURI,
		CredentialKey: authData.CredentialKey,
		ExpiresAt:     ToTimestamp(time.Now().Add(toolAuthStateTTL).UnixMilli()),
	}, nil
}

func (h *evalHandler) disconnectToolAuth(ctx context.Context, target toolAuthTarget, userID string) error {
	if _, err := h.callTargetToolAuth(ctx, target, "disconnect"); err != nil {
		return err
	}
	h.logToolAuthAudit(userID, "disconnect", target)
	if err := store.DeleteToolAuthAttribution(ctx, h.db, target.experimentID, target.agentName, target.credentialKey); err != nil {
		h.logger.Warn("failed to clear tool auth attribution", "experimentId", target.experimentID, "error", err)
	}
	return nil
}

func (h *evalHandler) registerToolAuthState(ctx context.Context, authData *a2a.AuthenticationRequiredData, principal, routingName string) error {
	if h.toolAuthBroker == nil {
		return toolAuthUnavailable(nil)
	}
	state, err := a2a.StateFromAuthURI(authData.AuthURI)
	if err != nil {
		return fmt.Errorf("extract state from agent auth URI: %w", err)
	}
	if schemeErr := requireWebScheme(authData.AuthURI); schemeErr != nil {
		return schemeErr
	}
	targetAgent := authData.TargetAgent
	if targetAgent == "" {
		targetAgent = routingName
	}
	if !toolAuthTargetAgentPattern.MatchString(targetAgent) {
		return fmt.Errorf("agent returned unregisterable target agent name (len %d)", len(targetAgent))
	}
	registration := a2a.ToolAuthStateRegistration{
		State:          state,
		TargetAgent:    targetAgent,
		TaskID:         authData.TargetAgentTaskID,
		UserID:         principal,
		ClientID:       authData.ClientID,
		FunctionCallID: authData.AuthRequestFunctionCallID,
		CredentialKey:  authData.CredentialKey,
		CodeVerifier:   authData.CodeVerifier,
		TTLMs:          toolAuthStateTTL.Milliseconds(),
	}
	payload, err := json.Marshal(registration)
	if err != nil {
		return fmt.Errorf("marshal tool auth state registration: %w", err)
	}
	topic := a2a.ToolAuthRegisterTopic(h.namespace)
	msg := &broker.Message{Payload: payload, TTL: toolAuthStateTTL}
	if err := h.toolAuthBroker.PublishGuaranteed(ctx, topic, msg); err != nil {
		return fmt.Errorf("publish tool auth state registration: %w", err)
	}
	return nil
}

func requireWebScheme(rawURI string) error {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("parse agent auth URI: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("agent returned auth URI with unsupported scheme %q", parsed.Scheme)
	}
	return nil
}

func (h *evalHandler) logToolAuthAudit(userID, action string, target toolAuthTarget) {
	if h.logger == nil {
		return
	}
	h.logger.Info("eval tool auth",
		"action", action,
		"userID", userID,
		"experimentId", target.experimentID,
		"agentName", target.agentName,
		"credentialKey", target.credentialKey,
		"principal", target.principal,
	)
}

func (h *evalHandler) logToolAuthAgentAudit(userID, action, experimentID, agentName string) {
	if h.logger == nil {
		return
	}
	h.logger.Info("eval tool auth",
		"action", action,
		"userID", userID,
		"experimentId", experimentID,
		"agentName", agentName,
	)
}

func (h *evalHandler) recordToolAuthAttribution(ctx context.Context, experimentID, agentName, credentialKey, authorizedBy string) {
	if err := store.UpsertToolAuthAttribution(ctx, h.db, experimentID, agentName, credentialKey, authorizedBy, time.Now().UnixMilli()); err != nil {
		h.logger.Warn("failed to record tool auth attribution", "experimentId", experimentID, "error", err)
	}
}
