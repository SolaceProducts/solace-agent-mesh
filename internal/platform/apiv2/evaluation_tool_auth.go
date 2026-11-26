// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type ExperimentToolAuth struct {
	RunAs               string          `json:"runAs" example:"system:nightly-evals"`
	Agents              []ToolAuthAgent `json:"agents"`
	AllowListIsWildcard bool            `json:"allowListIsWildcard" example:"false"`
	TotalAgents         int             `json:"totalAgents" example:"3"`
	TotalRunningAgents  int             `json:"totalRunningAgents" example:"12"`
	AgentScanLimit      int             `json:"agentScanLimit" example:"60"`
}

type ToolAuthAgent struct {
	AgentName string               `json:"agentName" example:"support_agent"`
	IsTarget  bool                 `json:"isTarget" example:"true"`
	AddedBy   *string              `json:"addedBy" example:"jane.doe@example.com"`
	NodeIDs   []string             `json:"nodeIds" example:"[\"classify\",\"triage/review\"]"`
	Reachable bool                 `json:"reachable" example:"true"`
	Tools     []ToolAuthCredential `json:"tools"`
}

type ToolAuthCredential struct {
	CredentialKey      string  `json:"credentialKey" example:"google_drive"`
	DisplayName        *string `json:"displayName" example:"Google Drive"`
	AuthType           *string `json:"authType" example:"oauth2"`
	Interactive        bool    `json:"interactive" example:"true"`
	Status             string  `json:"status" example:"connected"`
	ExpiresTime        Instant `json:"expiresTime"`
	LastAuthorizedBy   *string `json:"lastAuthorizedBy" example:"jane.doe@example.com"`
	LastAuthorizedTime Instant `json:"lastAuthorizedTime"`
}

type ToolAuthAuthorization struct {
	AuthURI       string  `json:"authUri" example:"https://accounts.example.com/authorize?client_id=sam&state=0190f5f1"`
	CredentialKey string  `json:"credentialKey" example:"google_drive"`
	ExpiresTime   Instant `json:"expiresTime"`
}

type ToolAuthCandidateAgent struct {
	AgentName string `json:"agentName" example:"support_agent"`
}

type PinnedToolAuthAgent struct {
	AgentName string  `json:"agentName" locationKey:"" example:"support_agent"`
	AddedBy   string  `json:"addedBy" example:"jane.doe@example.com"`
	AddedTime Instant `json:"addedTime"`
}

type ToolAuthCredentialRequest struct {
	ExperimentID  string `path:"experimentId" json:"-" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	AgentName     string `json:"agentName" validate:"required" example:"support_agent"`
	CredentialKey string `json:"credentialKey" validate:"required" example:"google_drive"`
}

type AddToolAuthAgentRequest struct {
	ExperimentID string `path:"experimentId" json:"-" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	AgentName    string `json:"agentName" validate:"required" example:"support_agent"`
}

type experimentToolAuthRequest struct {
	ExperimentID string `path:"experimentId" json:"-"`
}

type listToolAuthCandidatesRequest struct {
	ExperimentID string `path:"experimentId" json:"-"`
	Page         Page   `json:"-"`
}

type removeToolAuthAgentRequest struct {
	ExperimentID string `path:"experimentId" json:"-"`
	AgentName    string `path:"agentName,text" json:"-"`
}

const toolAuthPath = experimentsPath + "/{experimentId}/toolAuth"

func toolAuthRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: toolAuthPath, Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getExperimentToolAuth", Summary: "Get the tool authorization of an evaluation experiment",
				Text: "Returns, for the target agent and every agent it delegates to, which tool credentials the experiment's service account holds. An agent that cannot be asked is listed with reachable false. If the platform has no connection to the agents, the request fails with 503.", Tag: toolAuthTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.getToolAuth, "Unable to get tool authorization")},
		{Method: http.MethodPut, Path: toolAuthPath + "/begin", Scope: rbac.ScopeEvaluationInvoke,
			Stability: StabilityDraft,
			Doc: Doc{ID: "beginExperimentToolAuth", Summary: "Initiate the authorization of a tool credential of an evaluation experiment",
				Text: "Starts the browser sign-in for one credential of one agent and returns the link to open. The credential is stored for the experiment's service account. If the experiment does not reach the agent, no agent executor runs the agent, or the agent does not have the credential, the request fails with 404. If the request is invalid or the credential cannot be managed from here, the request fails with 422. If no agent executor answers, the request fails with 503. If the agent refuses the call, the request fails with 502.", Tag: toolAuthTag,
				Errors: []int{http.StatusServiceUnavailable, http.StatusBadGateway}},
			Handler: Handle(h.beginToolAuth, "Unable to begin tool authorization")},
		{Method: http.MethodPut, Path: toolAuthPath + "/disconnect", Scope: rbac.ScopeEvaluationInvoke,
			Stability: StabilityDraft,
			Doc: Doc{ID: "disconnectExperimentToolAuth", Summary: "Update an evaluation experiment to disconnect a tool credential",
				Text: "Wipes the credential one agent holds for the experiment's service account. If the experiment does not reach the agent, no agent executor runs the agent, or the agent does not have the credential, the request fails with 404. If the request is invalid or the credential cannot be managed from here, the request fails with 422. If no agent executor answers, the request fails with 503. If the agent refuses the call, the request fails with 502.", Tag: toolAuthTag,
				Errors: []int{http.StatusServiceUnavailable, http.StatusBadGateway}},
			Handler: Handle(h.disconnectToolAuth, "Unable to disconnect tool authorization")},
		{Method: http.MethodGet, Path: toolAuthPath + "/candidateAgents", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listExperimentToolAuthCandidateAgents", Summary: "Get a list of agents that can be added to an evaluation experiment's checklist",
				Text: "Returns one page of the running agents that hold a credential a person can authorize from a browser. Only the first 60 running agents are checked. If the platform has no connection to the agents, the request fails with 503.", Tag: toolAuthTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.listToolAuthCandidates, "Unable to list candidate agents")},
		{Method: http.MethodPost, Path: toolAuthPath + "/agents", Scope: rbac.ScopeEvaluationCreate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "addExperimentToolAuthAgent", Summary: "Create an entry for an agent on an evaluation experiment's checklist",
				Text: "Adds a running agent to the checklist so its credentials are listed for everyone who opens the experiment. If the agent is already on the checklist, the request fails with 409.", Tag: toolAuthTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.addToolAuthAgent, "Unable to add tool authorization agent")},
		{Method: http.MethodDelete, Path: toolAuthPath + "/agents/{agentName}", Scope: rbac.ScopeEvaluationDelete,
			Stability: StabilityDraft,
			Doc: Doc{ID: "removeExperimentToolAuthAgent", Summary: "Delete an agent from an evaluation experiment's checklist",
				Text: "Removes an agent added by hand from the checklist. The agent keeps the credentials it holds. If the agent was not added, the request fails with 404.", Tag: toolAuthTag},
			Handler: Handle(h.removeToolAuthAgent, "Unable to remove tool authorization agent")},
	}
}

func (h *evaluationHandler) getToolAuth(ctx context.Context, req experimentToolAuthRequest) (ExperimentToolAuth, error) {
	checklist, err := h.toolAuth.Checklist(ctx, req.ExperimentID)
	if err != nil {
		return ExperimentToolAuth{}, evaluationError(err)
	}
	return toExperimentToolAuth(checklist), nil
}

func (h *evaluationHandler) beginToolAuth(ctx context.Context, req ToolAuthCredentialRequest) (ToolAuthAuthorization, error) {
	begun, err := h.toolAuth.Begin(ctx, req.ExperimentID, req.AgentName, req.CredentialKey, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return ToolAuthAuthorization{}, toolAuthCallError(err)
	}
	return ToolAuthAuthorization{
		AuthURI:       begun.AuthURI,
		CredentialKey: begun.CredentialKey,
		ExpiresTime:   Instant(begun.ExpiresAt),
	}, nil
}

func (h *evaluationHandler) disconnectToolAuth(ctx context.Context, req ToolAuthCredentialRequest) (struct{}, error) {
	if err := h.toolAuth.Disconnect(ctx, req.ExperimentID, req.AgentName, req.CredentialKey, entplatform.UserIDFromContext(ctx)); err != nil {
		return struct{}{}, toolAuthCallError(err)
	}
	return struct{}{}, nil
}

func toolAuthCallError(err error) error {
	if refused, ok := errors.AsType[*entplatform.AgentRefusedToolAuthError](err); ok {
		return &apierr.ExternalServiceError{Message: refused.Unavailable.Message, Cause: refused.Unavailable.Cause}
	}
	return evaluationError(err)
}

func (h *evaluationHandler) listToolAuthCandidates(ctx context.Context, req listToolAuthCandidatesRequest) (List[ToolAuthCandidateAgent], error) {
	names, err := h.toolAuth.CandidateAgents(ctx, req.ExperimentID)
	if err != nil {
		return List[ToolAuthCandidateAgent]{}, evaluationError(err)
	}
	candidates := make([]ToolAuthCandidateAgent, len(names))
	for i, name := range names {
		candidates[i] = ToolAuthCandidateAgent{AgentName: name}
	}
	return PageOf(candidates, req.Page), nil
}

func (h *evaluationHandler) addToolAuthAgent(ctx context.Context, req AddToolAuthAgentRequest) (PinnedToolAuthAgent, error) {
	pinned, err := h.toolAuth.PinAgent(ctx, req.ExperimentID, req.AgentName, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return PinnedToolAuthAgent{}, evaluationError(err)
	}
	return PinnedToolAuthAgent{AgentName: pinned.AgentName, AddedBy: pinned.AddedBy, AddedTime: Instant(pinned.AddedAt)}, nil
}

func (h *evaluationHandler) removeToolAuthAgent(ctx context.Context, req removeToolAuthAgentRequest) (struct{}, error) {
	if err := h.toolAuth.UnpinAgent(ctx, req.ExperimentID, req.AgentName, entplatform.UserIDFromContext(ctx)); err != nil {
		return struct{}{}, evaluationError(err)
	}
	return struct{}{}, nil
}

func toExperimentToolAuth(checklist entplatform.ToolAuthChecklist) ExperimentToolAuth {
	agents := make([]ToolAuthAgent, len(checklist.Agents))
	for i, agent := range checklist.Agents {
		tools := make([]ToolAuthCredential, len(agent.Tools))
		for j, tool := range agent.Tools {
			tools[j] = ToolAuthCredential{
				CredentialKey:      tool.CredentialKey,
				DisplayName:        NullIfZero(tool.DisplayName),
				AuthType:           NullIfZero(tool.AuthType),
				Interactive:        tool.Interactive,
				Status:             tool.Status,
				ExpiresTime:        InstantOf(tool.ExpiresAt),
				LastAuthorizedBy:   NullIfZero(tool.LastAuthorizedBy),
				LastAuthorizedTime: InstantOf(tool.LastAuthorizedAt),
			}
		}
		agents[i] = ToolAuthAgent{
			AgentName: agent.AgentName,
			IsTarget:  agent.IsTarget,
			AddedBy:   NullIfZero(agent.AddedBy),
			NodeIDs:   nonNilList(agent.NodeIDs),
			Reachable: agent.Reachable,
			Tools:     tools,
		}
	}
	return ExperimentToolAuth{
		RunAs:               checklist.RunAs,
		Agents:              agents,
		AllowListIsWildcard: checklist.AllowListIsWildcard,
		TotalAgents:         checklist.TotalAgents,
		TotalRunningAgents:  checklist.TotalRunningAgents,
		AgentScanLimit:      checklist.AgentScanLimit,
	}
}
