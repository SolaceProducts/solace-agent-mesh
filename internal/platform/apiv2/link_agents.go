// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const linkAgentType = "linkAgent"

type LinkAgent struct {
	ID               string             `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type             string             `json:"type" example:"linkAgent"`
	Name             string             `json:"name" example:"Claims Desk"`
	InstanceName     string             `json:"instanceName" example:"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e3f"`
	Description      *string            `json:"description" example:"Answers claims questions for the partner."`
	EntrypointID     string             `json:"entrypointId" example:"0190f5e8-3d4e-7f5a-8b6c-7d8e9f0a1b2c"`
	ConnectURL       *string            `json:"connectUrl" example:"wss://gw-abc12345.sam.example.com/v1/connect"`
	ConnectPath      string             `json:"connectPath" example:"/gw/partner-link/v1/connect"`
	TokenPrefix      *string            `json:"tokenPrefix" example:"Ab3dE-ex"`
	Enabled          bool               `json:"enabled" example:"true"`
	Owner            *string            `json:"owner" example:"alice@example.com"`
	Team             *string            `json:"team" example:"Claims Operations"`
	Peers            []string           `json:"peers" example:"[\"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e40\"]"`
	ShareUserProfile bool               `json:"shareUserProfile" example:"false"`
	Wake             *LinkAgentWake     `json:"wake"`
	DeploymentStatus string             `json:"deploymentStatus" example:"deployed"`
	SyncStatus       *string            `json:"syncStatus" example:"in_sync"`
	RuntimeStatus    RuntimeStatus      `json:"runtimeStatus"`
	Deployed         *DeployedLinkAgent `json:"deployed"`
	CreatedBy        string             `json:"createdBy" example:"alice@example.com"`
	UpdatedBy        string             `json:"updatedBy" example:"alice@example.com"`
	CreatedTime      Instant            `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime      Instant            `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type LinkAgentWithToken struct {
	ID                string             `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type              string             `json:"type" example:"linkAgent"`
	Name              string             `json:"name" example:"Claims Desk"`
	InstanceName      string             `json:"instanceName" example:"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e3f"`
	Description       *string            `json:"description" example:"Answers claims questions for the partner."`
	EntrypointID      string             `json:"entrypointId" example:"0190f5e8-3d4e-7f5a-8b6c-7d8e9f0a1b2c"`
	ConnectURL        *string            `json:"connectUrl" example:"wss://gw-abc12345.sam.example.com/v1/connect"`
	ConnectPath       string             `json:"connectPath" example:"/gw/partner-link/v1/connect"`
	Token             *string            `json:"token" example:"samk_Ab3dE-example-token-not-real"`
	TokenPrefix       *string            `json:"tokenPrefix" example:"Ab3dE-ex"`
	Enabled           bool               `json:"enabled" example:"true"`
	Owner             *string            `json:"owner" example:"alice@example.com"`
	Team              *string            `json:"team" example:"Claims Operations"`
	Peers             []string           `json:"peers" example:"[\"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e40\"]"`
	ShareUserProfile  bool               `json:"shareUserProfile" example:"false"`
	Wake              *LinkAgentWake     `json:"wake"`
	DeploymentStatus  string             `json:"deploymentStatus" example:"not_deployed"`
	SyncStatus        *string            `json:"syncStatus" example:"out_of_sync"`
	RuntimeStatus     RuntimeStatus      `json:"runtimeStatus"`
	Deployed          *DeployedLinkAgent `json:"deployed"`
	Deployment        *AgentDeployment   `json:"deployment"`
	DeploymentRefusal *string            `json:"deploymentRefusal" example:"the new token is stored and the link agent could not be deployed again; send an update deployment to make it take effect"`
	CreatedBy         string             `json:"createdBy" example:"alice@example.com"`
	UpdatedBy         string             `json:"updatedBy" example:"alice@example.com"`
	CreatedTime       Instant            `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime       Instant            `json:"updatedTime" example:"\"2026-01-02T03:04:05.678Z\""`
}

func (LinkAgentWithToken) ResponseHeaders() map[string]string {
	return map[string]string{"Cache-Control": "no-store"}
}

type LinkAgentWake struct {
	URL            string            `json:"url" example:"https://runtime.example.com/invocations"`
	TokenURL       *string           `json:"tokenUrl" example:"https://idp.example.com/oauth2/token"`
	ClientID       *string           `json:"clientId" example:"claims-client"`
	ClientSecret   *string           `json:"clientSecret" example:"<REDACTED>"`
	Scope          *string           `json:"scope" example:"https://runtime.example.com/.default"`
	Headers        map[string]string `json:"headers" example:"{\"X-Session\":\"<REDACTED>\"}"`
	TimeoutSeconds int               `json:"timeoutSeconds" example:"900"`
	OnStart        bool              `json:"onStart" example:"true"`
}

type DeployedLinkAgent struct {
	Name             string                 `json:"name" example:"Claims Desk"`
	Description      *string                `json:"description" example:"Answers claims questions for the partner."`
	EntrypointID     string                 `json:"entrypointId" example:"0190f5e8-3d4e-7f5a-8b6c-7d8e9f0a1b2c"`
	TokenPrefix      *string                `json:"tokenPrefix" example:"Ab3dE-ex"`
	Enabled          bool                   `json:"enabled" example:"true"`
	Owner            *string                `json:"owner" example:"alice@example.com"`
	Team             *string                `json:"team" example:"Claims Operations"`
	Peers            []string               `json:"peers" example:"[\"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e40\"]"`
	ShareUserProfile bool                   `json:"shareUserProfile" example:"false"`
	Wake             *DeployedLinkAgentWake `json:"wake"`
	DeployedTime     Instant                `json:"deployedTime" example:"\"2026-01-03T10:16:00.000Z\""`
	DeployedBy       string                 `json:"deployedBy" example:"alice@example.com"`
}

type DeployedLinkAgentWake struct {
	URL            string   `json:"url" example:"https://runtime.example.com/invocations"`
	TokenURL       *string  `json:"tokenUrl" example:"https://idp.example.com/oauth2/token"`
	ClientID       *string  `json:"clientId" example:"claims-client"`
	Scope          *string  `json:"scope" example:"https://runtime.example.com/.default"`
	HeaderNames    []string `json:"headerNames" example:"[\"X-Session\"]"`
	TimeoutSeconds int      `json:"timeoutSeconds" example:"900"`
	OnStart        bool     `json:"onStart" example:"true"`
}

type LinkAgentWakeRequest struct {
	URL            string            `json:"url" validate:"required,max=2048" example:"https://runtime.example.com/invocations"`
	TokenURL       string            `json:"tokenUrl" validate:"omitempty,max=2048" example:"https://idp.example.com/oauth2/token"`
	ClientID       string            `json:"clientId" validate:"omitempty,max=1024" example:"claims-client"`
	ClientSecret   string            `json:"clientSecret" validate:"omitempty,max=4096" example:"<client-secret>"`
	Scope          string            `json:"scope" validate:"omitempty,max=1024" example:"https://runtime.example.com/.default"`
	Headers        map[string]string `json:"headers" example:"{\"X-Session\":\"claims\"}"`
	TimeoutSeconds int               `json:"timeoutSeconds" validate:"omitempty,min=1,max=86400" example:"900"`
	OnStart        *bool             `json:"onStart" example:"true"`
}

type CreateLinkAgentRequest struct {
	Name             string                `json:"name" validate:"required,min=3,max=40,safename" example:"Claims Desk"`
	Description      string                `json:"description" validate:"omitempty,safetext" example:"Answers claims questions for the partner."`
	EntrypointID     string                `json:"entrypointId" validate:"required,uuid" example:"0190f5e8-3d4e-7f5a-8b6c-7d8e9f0a1b2c"`
	TokenSHA256      string                `json:"tokenSha256" validate:"omitempty,max=64" example:"54439c4b18692a66a4f04d4974367ba3cbfad68dc358e5e3c16e7c76ca35df3e"`
	Enabled          *bool                 `json:"enabled" example:"true"`
	Owner            string                `json:"owner" validate:"omitempty,max=256" example:"alice@example.com"`
	Team             string                `json:"team" validate:"omitempty,max=256" example:"Claims Operations"`
	Peers            []string              `json:"peers" validate:"omitempty,max=64" example:"[\"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e40\"]"`
	ShareUserProfile bool                  `json:"shareUserProfile" example:"false"`
	Wake             *LinkAgentWakeRequest `json:"wake"`
}

type UpdateLinkAgentRequest struct {
	ID               string                `path:"id" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Name             string                `json:"name" validate:"required,min=3,max=40,safename" example:"Claims Desk"`
	Description      string                `json:"description" validate:"omitempty,safetext" example:"Answers claims questions for the partner."`
	EntrypointID     string                `json:"entrypointId" validate:"required,uuid" example:"0190f5e8-3d4e-7f5a-8b6c-7d8e9f0a1b2c"`
	TokenSHA256      string                `json:"tokenSha256" validate:"omitempty,max=64" example:"54439c4b18692a66a4f04d4974367ba3cbfad68dc358e5e3c16e7c76ca35df3e"`
	Enabled          *bool                 `json:"enabled" example:"true"`
	Owner            string                `json:"owner" validate:"omitempty,max=256" example:"alice@example.com"`
	Team             string                `json:"team" validate:"omitempty,max=256" example:"Claims Operations"`
	Peers            []string              `json:"peers" validate:"omitempty,max=64" example:"[\"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e40\"]"`
	ShareUserProfile bool                  `json:"shareUserProfile" example:"false"`
	Wake             *LinkAgentWakeRequest `json:"wake"`
}

type LinkAgentSecretsDiffRequest struct {
	ID     string                    `path:"id" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Values *LinkAgentSecretsDiffForm `json:"values" validate:"required"`
}

type LinkAgentSecretsDiffForm struct {
	TokenSHA256 string                        `json:"tokenSha256" validate:"omitempty,max=64" example:"54439c4b18692a66a4f04d4974367ba3cbfad68dc358e5e3c16e7c76ca35df3e"`
	Wake        *LinkAgentWakeSecretsDiffForm `json:"wake"`
}

type LinkAgentWakeSecretsDiffForm struct {
	ClientSecret string            `json:"clientSecret" validate:"omitempty,max=4096" example:"<client-secret>"`
	Headers      map[string]string `json:"headers" example:"{\"X-Session\":\"claims\"}"`
}

type listLinkAgentsRequest struct {
	Page Page `json:"-"`
}

type linkAgentHandler struct {
	svc       *entplatform.LinkAgentService
	deploySvc *entplatform.DeploymentService
	logger    *slog.Logger
}

const linkAgentPath = "/linkAgents/{id}"

func linkAgentRoutes(deps *entplatform.RouteDeps, _ *platpkg.PlatformCore, logger *slog.Logger) []Route {
	if deps.LinkAgentSvc == nil {
		return nil
	}
	h := &linkAgentHandler{svc: deps.LinkAgentSvc, deploySvc: deps.AgentDeploySvc, logger: logger}
	return []Route{
		{Method: http.MethodGet, Path: "/linkAgents", Scope: rbac.ScopeAgentBuilderRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listLinkAgents", Summary: "Get a list of link agents",
				Text: "Returns one page of link agents, newest first. Link agents are in Early Access. No token or token digest is returned, and a stored wake client secret and wake header values are redacted.", Tag: linkAgentsTag},
			Handler: Handle(h.list, "Unable to list link agents")},
		{Method: http.MethodPost, Path: "/linkAgents", Scope: rbac.ScopeAgentBuilderCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createLinkAgent", Summary: "Create a link agent",
				Text: "Registers a third-party agent that joins the mesh through an agent_link entrypoint, without deploying it. Link agents are in Early Access. Leave tokenSha256 out and the response carries a new token, which no other response shows. Send tokenSha256, the SHA-256 digest of a token you issued, and no token is generated or returned. Deploy the link agent with POST /agentDeployments.", Tag: linkAgentsTag},
			Handler: Handle(h.create, "Unable to create link agent")},
		{Method: http.MethodGet, Path: linkAgentPath, Scope: rbac.ScopeAgentBuilderRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getLinkAgent", Summary: "Get a link agent",
				Text: "Returns one link agent with what it needs to connect, except its token. A stored wake client secret and wake header values are redacted.", Tag: linkAgentsTag},
			Handler: Handle(h.get, "Unable to get link agent")},
		{Method: http.MethodPut, Path: linkAgentPath, Scope: rbac.ScopeAgentBuilderUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceLinkAgent", Summary: "Replace a link agent",
				Text: "Replaces the link agent's settings with the fields sent. An optional field left out is cleared or reset to its default. A wake client secret left out, sent empty, or sent as <REDACTED> is kept, and so is a wake header value sent as <REDACTED>, while the wake URL is unchanged. The token is kept unless tokenSha256 carries the digest of another token, and a replace that changes tokenSha256 also needs `agent_builder:*:deploy`. A deployed link agent keeps running with the settings and the token it was deployed with until it is deployed again.", Tag: linkAgentsTag},
			Handler: Handle(h.update, "Unable to update link agent")},
		{Method: http.MethodDelete, Path: linkAgentPath, Scope: rbac.ScopeAgentBuilderDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteLinkAgent", Summary: "Delete a link agent",
				Text: "Undeploys the link agent if it is deployed, then deletes it and its token. If the undeploy fails, the link agent is kept and the request fails with 502.", Tag: linkAgentsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.delete, "Unable to delete link agent")},
		{Method: http.MethodPut, Path: linkAgentPath + "/rotateToken", Gate: GateLinkAgentRotateToken,
			Stability: StabilityDraft,
			Doc: Doc{ID: "rotateLinkAgentToken", Summary: "Replace a link agent's token",
				Text: "Issues a new token for the link agent and returns it once. If the link agent is deployed, it is deployed again before the response is sent, so its previous token stops working. The response carries that deployment. If the deployment failed or could not start, the new token is still returned and stored, the previous token keeps working, and an update deployment sent with POST /agentDeployments makes the new one take effect.", Tag: linkAgentsTag},
			Handler: Handle(h.rotateToken, "Unable to rotate the link agent's token")},
		{Method: http.MethodPut, Path: linkAgentPath + "/secretsDiff", Scope: rbac.ScopeAgentBuilderUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "compareLinkAgentSecrets", Summary: "Validate values against a link agent's stored token digest and wake secret",
				Text: "Returns whether saving the token digest, the wake client secret and the wake header values sent would keep the stored ones. No stored value is returned.", Tag: linkAgentsTag},
			Handler: Handle(h.secretsDiff, "Unable to compare link agent secrets")},
	}
}

func (h *linkAgentHandler) list(ctx context.Context, req listLinkAgentsRequest) (List[LinkAgent], error) {
	resp, err := h.svc.ListPaginated(ctx, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[LinkAgent]{}, err
	}
	return ListOf(resp, toLinkAgent), nil
}

func (h *linkAgentHandler) create(ctx context.Context, req CreateLinkAgentRequest) (LinkAgentWithToken, error) {
	resp, token, err := h.svc.Create(ctx, entplatform.CreateLinkAgentRequest{
		Name:             req.Name,
		Description:      req.Description,
		EntrypointID:     req.EntrypointID,
		TokenSHA256:      req.TokenSHA256,
		Enabled:          req.Enabled,
		Owner:            req.Owner,
		Team:             req.Team,
		Peers:            req.Peers,
		ShareUserProfile: req.ShareUserProfile,
		Wake:             req.Wake.toService(),
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return LinkAgentWithToken{}, err
	}
	return toLinkAgentWithToken(resp, token, nil, ""), nil
}

func (w *LinkAgentWakeRequest) toService() *entplatform.LinkAgentWakeRequest {
	if w == nil {
		return nil
	}
	return &entplatform.LinkAgentWakeRequest{
		URL:            w.URL,
		TokenURL:       w.TokenURL,
		ClientID:       w.ClientID,
		ClientSecret:   w.ClientSecret,
		Scope:          w.Scope,
		Headers:        w.Headers,
		TimeoutSeconds: w.TimeoutSeconds,
		OnStart:        w.OnStart,
	}
}

func (h *linkAgentHandler) get(ctx context.Context, req IDRequest) (LinkAgent, error) {
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return LinkAgent{}, err
	}
	return toLinkAgent(resp), nil
}

func (h *linkAgentHandler) update(ctx context.Context, req UpdateLinkAgentRequest) (LinkAgent, error) {
	resp, err := h.svc.Replace(ctx, req.ID, entplatform.UpdateLinkAgentRequest{
		Name:             req.Name,
		Description:      req.Description,
		EntrypointID:     req.EntrypointID,
		TokenSHA256:      req.TokenSHA256,
		Enabled:          req.Enabled,
		Owner:            req.Owner,
		Team:             req.Team,
		Peers:            req.Peers,
		ShareUserProfile: req.ShareUserProfile,
		Wake:             req.Wake.toService(),
	}, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return LinkAgent{}, err
	}
	return toLinkAgent(resp), nil
}

func (h *linkAgentHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, h.svc.DeleteWithUndeploy(ctx, req.ID, entplatform.UserIDFromContext(ctx), h.deploySvc)
}

func (h *linkAgentHandler) secretsDiff(ctx context.Context, req LinkAgentSecretsDiffRequest) (SecretsDiffResult, error) {
	candidate := entplatform.LinkAgentSecretsCandidate{TokenSHA256: req.Values.TokenSHA256}
	if req.Values.Wake != nil {
		candidate.WakeClientSecret, candidate.WakeHeaders = req.Values.Wake.ClientSecret, req.Values.Wake.Headers
	}
	diff, err := h.svc.GetSecretsDiff(ctx, req.ID, candidate)
	if err != nil {
		return SecretsDiffResult{}, err
	}
	entplatform.LogSecretsDiff(ctx, h.logger, "link agent", req.ID, diff)
	return toSecretsDiffResult(diff), nil
}

func (h *linkAgentHandler) rotateToken(ctx context.Context, req IDRequest) (LinkAgentWithToken, error) {
	rotation, err := h.svc.RotateToken(ctx, req.ID, entplatform.UserIDFromContext(ctx), h.deploySvc)
	if err != nil {
		return LinkAgentWithToken{}, err
	}
	return toLinkAgentWithToken(rotation.Agent, rotation.Token, rotation.Deployment, rotation.DeploymentRefusal), nil
}

func toLinkAgent(resp *entplatform.LinkAgentResponse) LinkAgent {
	return LinkAgent{
		ID:               resp.ID,
		Type:             linkAgentType,
		Name:             resp.Name,
		InstanceName:     resp.InstanceName,
		Description:      NullIfZero(resp.Description),
		EntrypointID:     resp.EntrypointID,
		ConnectURL:       NullIfZero(resp.ConnectURL),
		ConnectPath:      resp.ConnectPath,
		TokenPrefix:      NullIfZero(resp.TokenPrefix),
		Enabled:          resp.Enabled,
		Owner:            NullIfZero(resp.Owner),
		Team:             NullIfZero(resp.Team),
		Peers:            nonNilList(resp.Peers),
		ShareUserProfile: resp.ShareUserProfile,
		Wake:             toLinkAgentWake(resp.Wake),
		DeploymentStatus: resp.DeploymentStatus,
		SyncStatus:       resp.SyncStatus,
		RuntimeStatus:    toRuntimeStatus(resp.RuntimeStatus),
		Deployed:         toDeployedLinkAgent(resp.Deployed),
		CreatedBy:        resp.CreatedBy,
		UpdatedBy:        resp.UpdatedBy,
		CreatedTime:      Instant(resp.CreatedTime),
		UpdatedTime:      Instant(resp.UpdatedTime),
	}
}

func toLinkAgentWithToken(resp *entplatform.LinkAgentResponse, token string, deployment *entplatform.AgentDeploymentResponse, refusal string) LinkAgentWithToken {
	a := toLinkAgent(resp)
	var rolledOut *AgentDeployment
	if deployment != nil {
		rolledOut = new(toAgentDeployment(deployment))
	}
	return LinkAgentWithToken{
		Deployment:        rolledOut,
		DeploymentRefusal: NullIfZero(refusal),
		ID:                a.ID,
		Type:              a.Type,
		Name:              a.Name,
		InstanceName:      a.InstanceName,
		Description:       a.Description,
		EntrypointID:      a.EntrypointID,
		ConnectURL:        a.ConnectURL,
		ConnectPath:       a.ConnectPath,
		Token:             NullIfZero(token),
		TokenPrefix:       a.TokenPrefix,
		Enabled:           a.Enabled,
		Owner:             a.Owner,
		Team:              a.Team,
		Peers:             a.Peers,
		ShareUserProfile:  a.ShareUserProfile,
		Wake:              a.Wake,
		DeploymentStatus:  a.DeploymentStatus,
		SyncStatus:        a.SyncStatus,
		RuntimeStatus:     a.RuntimeStatus,
		Deployed:          a.Deployed,
		CreatedBy:         a.CreatedBy,
		UpdatedBy:         a.UpdatedBy,
		CreatedTime:       a.CreatedTime,
		UpdatedTime:       a.UpdatedTime,
	}
}

func toLinkAgentWake(w *entplatform.LinkAgentWakeResponse) *LinkAgentWake {
	if w == nil {
		return nil
	}
	headers := w.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	return &LinkAgentWake{
		URL:            w.URL,
		TokenURL:       NullIfZero(w.TokenURL),
		ClientID:       NullIfZero(w.ClientID),
		ClientSecret:   NullIfZero(w.ClientSecret),
		Scope:          NullIfZero(w.Scope),
		Headers:        headers,
		TimeoutSeconds: w.TimeoutSeconds,
		OnStart:        w.OnStart,
	}
}

func toDeployedLinkAgent(d *entplatform.DeployedLinkAgentInfo) *DeployedLinkAgent {
	if d == nil {
		return nil
	}
	out := &DeployedLinkAgent{
		Name:             d.Name,
		Description:      NullIfZero(d.Description),
		EntrypointID:     d.EntrypointID,
		TokenPrefix:      NullIfZero(d.TokenPrefix),
		Enabled:          d.Enabled,
		Owner:            NullIfZero(d.Owner),
		Team:             NullIfZero(d.Team),
		Peers:            nonNilList(d.Peers),
		ShareUserProfile: d.ShareUserProfile,
		DeployedTime:     Instant(d.DeployedAt),
		DeployedBy:       d.DeployedBy,
	}
	if w := d.Wake; w != nil {
		out.Wake = &DeployedLinkAgentWake{
			URL:            w.URL,
			TokenURL:       NullIfZero(w.TokenURL),
			ClientID:       NullIfZero(w.ClientID),
			Scope:          NullIfZero(w.Scope),
			HeaderNames:    nonNilList(w.HeaderNames),
			TimeoutSeconds: w.TimeoutSeconds,
			OnStart:        w.OnStart,
		}
	}
	return out
}
