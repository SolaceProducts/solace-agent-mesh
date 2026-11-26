// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	msgCardFetchFailed       = "Unable to fetch agent card"
	msgStoredCardFetchFailed = "Unable to fetch the agent card with the stored credentials"
)

type RemoteAgent struct {
	ID                    string                 `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type                  string                 `json:"type" example:"remoteAgent"`
	Name                  string                 `json:"name" example:"Shipping tracker"`
	InstanceName          string                 `json:"instanceName" example:"agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e3f"`
	Description           *string                `json:"description" example:"Tracks parcels and reports delivery estimates."`
	AgentURL              string                 `json:"agentUrl" example:"https://agents.example.com/a2a/shipping"`
	AgentCardLocation     string                 `json:"agentCardLocation" example:"custom"`
	AgentCardURL          *string                `json:"agentCardUrl" example:"https://agents.example.com/a2a/shipping/agent-card.json"`
	AgentCardHeaders      map[string]string      `json:"agentCardHeaders" nullable:"true" example:"{\"X-Request-Source\":\"sam\"}"`
	AgentCardAuthType     *string                `json:"agentCardAuthType" example:"bearer"`
	AgentCardAuthConfig   *RemoteAgentAuthConfig `json:"agentCardAuthConfig"`
	AgentAuthType         string                 `json:"agentAuthType" example:"oauth2"`
	AgentAuthConfig       *RemoteAgentAuthConfig `json:"agentAuthConfig"`
	TaskHeaders           map[string]string      `json:"taskHeaders" nullable:"true" example:"{\"X-Request-Source\":\"sam\"}"`
	RequestTimeoutSeconds int                    `json:"requestTimeoutSeconds" example:"120"`
	Skills                []RemoteAgentSkill     `json:"skills"`
	InputModes            []string               `json:"inputModes" example:"[\"text\"]"`
	OutputModes           []string               `json:"outputModes" example:"[\"text\"]"`
	ProtocolVersion       string                 `json:"protocolVersion" example:"auto"`
	TrustedCardJwksUrls   []string               `json:"trustedCardJwksUrls" example:"[\"https://agents.example.com/.well-known/jwks.json\"]"`
	RequireSignedCard     bool                   `json:"requireSignedCard" example:"false"`
	RemoteProtocol        *RemoteAgentProtocol   `json:"remoteProtocol"`
	DeploymentStatus      string                 `json:"deploymentStatus" example:"deployed"`
	SyncStatus            *string                `json:"syncStatus" example:"in_sync"`
	RuntimeStatus         RuntimeStatus          `json:"runtimeStatus"`
	Deployed              *DeployedRemoteAgent   `json:"deployed"`
	CardData              *RemoteAgentCard       `json:"cardData"`
	CardFetchError        *string                `json:"cardFetchError" example:"Unable to fetch the agent card with the stored credentials"`
	CreatedBy             string                 `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy             string                 `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime           Instant                `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime           Instant                `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type DeployedRemoteAgent struct {
	AgentURL              string             `json:"agentUrl" example:"https://agents.example.com/a2a/shipping"`
	Name                  string             `json:"name" example:"Shipping tracker"`
	Description           *string            `json:"description" example:"Tracks parcels and reports delivery estimates."`
	AgentCardLocation     string             `json:"agentCardLocation" example:"custom"`
	AgentCardURL          *string            `json:"agentCardUrl" example:"https://agents.example.com/a2a/shipping/agent-card.json"`
	AgentCardAuthType     *string            `json:"agentCardAuthType" example:"bearer"`
	AgentAuthType         string             `json:"agentAuthType" example:"oauth2"`
	RequestTimeoutSeconds int                `json:"requestTimeoutSeconds" example:"120"`
	Skills                []RemoteAgentSkill `json:"skills"`
	InputModes            []string           `json:"inputModes" example:"[\"text\"]"`
	OutputModes           []string           `json:"outputModes" example:"[\"text\"]"`
	ProtocolVersion       string             `json:"protocolVersion" example:"auto"`
	TrustedCardJwksUrls   []string           `json:"trustedCardJwksUrls" example:"[\"https://agents.example.com/.well-known/jwks.json\"]"`
	RequireSignedCard     bool               `json:"requireSignedCard" example:"false"`
	DeployedTime          Instant            `json:"deployedTime" example:"\"2026-01-03T10:16:00.000Z\""`
	DeployedBy            string             `json:"deployedBy" example:"jane.doe@example.com"`
}

type RemoteAgentSkill struct {
	ID          *string  `json:"id" example:"track_parcel"`
	Name        string   `json:"name" example:"Track parcel"`
	Description string   `json:"description" example:"Reports where a parcel is and when it will arrive."`
	Tags        []string `json:"tags" example:"[\"shipping\",\"tracking\"]"`
	Examples    []string `json:"examples" example:"[\"Where is parcel 4471-0923?\"]"`
	InputModes  []string `json:"inputModes" example:"[\"text\"]"`
	OutputModes []string `json:"outputModes" example:"[\"text\"]"`
}

type RemoteAgentAuthConfig struct {
	Type          string   `json:"type" validate:"required,oneof=none bearer apikey basic oauth2" example:"oauth2"`
	Token         *string  `json:"token" example:"<token>"`
	APIKey        *string  `json:"apiKey" example:"<api-key>"`
	HeaderName    *string  `json:"headerName" example:"X-API-Key"`
	TokenEndpoint *string  `json:"tokenEndpoint" example:"https://auth.example.com/oauth2/token"`
	ClientID      *string  `json:"clientId" example:"sam-shipping-client"`
	ClientSecret  *string  `json:"clientSecret" example:"<client-secret>"`
	Scopes        []string `json:"scopes" example:"[\"shipping.read\"]"`
	Username      *string  `json:"username" example:"sam-service"`
	Password      *string  `json:"password" example:"<password>"`
}

type RemoteAgentCard struct {
	Name        string                 `json:"name" example:"Shipping tracker"`
	Description string                 `json:"description" example:"Tracks parcels and reports delivery estimates."`
	Skills      []RemoteAgentSkill     `json:"skills"`
	InputModes  []string               `json:"inputModes" example:"[\"text\"]"`
	OutputModes []string               `json:"outputModes" example:"[\"text\"]"`
	Interfaces  []RemoteAgentInterface `json:"interfaces" example:"[{\"version\":\"v1_0\",\"binding\":\"jsonrpc\"}]"`
	Signed      bool                   `json:"signed" example:"true"`
}

type RemoteAgentProtocol struct {
	SelectedVersion string `json:"selectedVersion" example:"v1_0"`
	Binding         string `json:"binding" example:"jsonrpc"`
	Deprecated      bool   `json:"deprecated" example:"false"`
	CardSignature   string `json:"cardSignature" example:"verified"`
}

type RemoteAgentInterface struct {
	Version string `json:"version" example:"v1_0"`
	Binding string `json:"binding" example:"jsonrpc"`
}

type CreateRemoteAgentRequest struct {
	Surface               string                 `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
	AgentURL              string                 `json:"agentUrl" validate:"required,min=1,max=2048" example:"https://agents.example.com/a2a/shipping"`
	Name                  string                 `json:"name" validate:"required,min=3,max=40,safename" example:"Shipping tracker"`
	AgentCardLocation     string                 `json:"agentCardLocation" validate:"required,oneof=well_known custom none" example:"custom"`
	Description           string                 `json:"description" validate:"omitempty,safetext" example:"Tracks parcels and reports delivery estimates."`
	AgentCardURL          string                 `json:"agentCardUrl" example:"https://agents.example.com/a2a/shipping/agent-card.json"`
	AgentCardHeaders      map[string]string      `json:"agentCardHeaders" example:"{\"X-Request-Source\":\"sam\"}"`
	AgentCardAuthType     string                 `json:"agentCardAuthType" validate:"omitempty,oneof=none bearer apikey basic oauth2" example:"bearer"`
	AgentCardAuthConfig   *RemoteAgentAuthConfig `json:"agentCardAuthConfig"`
	AgentAuthType         string                 `json:"agentAuthType" validate:"omitempty,oneof=none bearer apikey basic oauth2" example:"oauth2"`
	AgentAuthConfig       *RemoteAgentAuthConfig `json:"agentAuthConfig" validate:"required"`
	TaskHeaders           map[string]string      `json:"taskHeaders" example:"{\"X-Request-Source\":\"sam\"}"`
	RequestTimeoutSeconds int                    `json:"requestTimeoutSeconds" validate:"omitempty,min=10,max=600" example:"120"`
	Skills                []RemoteAgentSkill     `json:"skills"`
	InputModes            []string               `json:"inputModes" example:"[\"text\"]"`
	OutputModes           []string               `json:"outputModes" example:"[\"text\"]"`
	ProtocolVersion       string                 `json:"protocolVersion" validate:"omitempty,oneof=auto v1_0 v0_3" example:"auto"`
	TrustedCardJwksUrls   []string               `json:"trustedCardJwksUrls" example:"[\"https://agents.example.com/.well-known/jwks.json\"]"`
	RequireSignedCard     bool                   `json:"requireSignedCard" example:"false"`
}

type UpdateRemoteAgentRequest struct {
	ID                    string                 `path:"id" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	AgentURL              string                 `json:"agentUrl" validate:"required,min=1,max=2048" example:"https://agents.example.com/a2a/shipping"`
	Name                  string                 `json:"name" validate:"required,min=3,max=40,safename" example:"Shipping tracker"`
	AgentCardLocation     string                 `json:"agentCardLocation" validate:"required,oneof=well_known custom none" example:"custom"`
	Description           string                 `json:"description" validate:"omitempty,safetext" example:"Tracks parcels and reports delivery estimates."`
	AgentCardURL          string                 `json:"agentCardUrl" example:"https://agents.example.com/a2a/shipping/agent-card.json"`
	AgentCardHeaders      map[string]string      `json:"agentCardHeaders" example:"{\"X-Request-Source\":\"sam\"}"`
	AgentCardAuthType     string                 `json:"agentCardAuthType" validate:"omitempty,oneof=none bearer apikey basic oauth2" example:"bearer"`
	AgentCardAuthConfig   *RemoteAgentAuthConfig `json:"agentCardAuthConfig"`
	AgentAuthType         string                 `json:"agentAuthType" validate:"omitempty,oneof=none bearer apikey basic oauth2" example:"oauth2"`
	AgentAuthConfig       *RemoteAgentAuthConfig `json:"agentAuthConfig" validate:"required"`
	TaskHeaders           map[string]string      `json:"taskHeaders" example:"{\"X-Request-Source\":\"sam\"}"`
	RequestTimeoutSeconds int                    `json:"requestTimeoutSeconds" validate:"omitempty,min=10,max=600" example:"120"`
	Skills                []RemoteAgentSkill     `json:"skills"`
	InputModes            []string               `json:"inputModes" example:"[\"text\"]"`
	OutputModes           []string               `json:"outputModes" example:"[\"text\"]"`
	ProtocolVersion       string                 `json:"protocolVersion" validate:"omitempty,oneof=auto v1_0 v0_3" example:"auto"`
	TrustedCardJwksUrls   []string               `json:"trustedCardJwksUrls" example:"[\"https://agents.example.com/.well-known/jwks.json\"]"`
	RequireSignedCard     bool                   `json:"requireSignedCard" example:"false"`
}

type FetchRemoteAgentCardRequest struct {
	AgentCardURL  string                 `json:"agentCardUrl" validate:"required,min=11,max=2048" example:"https://agents.example.com/a2a/shipping/agent-card.json"`
	AuthConfig    *RemoteAgentAuthConfig `json:"authConfig"`
	CustomHeaders map[string]string      `json:"customHeaders" example:"{\"X-Request-Source\":\"sam\"}"`
}

type listRemoteAgentsRequest struct {
	Page Page `json:"-"`
}

type getRemoteAgentRequest struct {
	ID     string `path:"id" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Expand string `query:"expand" json:"-" example:"card"`
}

type remoteAgentHandler struct {
	svc       *entplatform.A2AAgentService
	deploySvc *entplatform.DeploymentService
	analytics *platpkg.ResourceReporter
}

const remoteAgentPath = "/remoteAgents/{id}"

func remoteAgentRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, _ *slog.Logger) []Route {
	if deps.A2AAgentSvc == nil {
		return nil
	}
	h := &remoteAgentHandler{svc: deps.A2AAgentSvc, deploySvc: deps.AgentDeploySvc, analytics: core.ResourceAnalytics()}
	return []Route{
		{Method: http.MethodGet, Path: "/remoteAgents", Scope: rbac.ScopeAgentBuilderRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listRemoteAgents", Summary: "Get a list of remote agents",
				Text: "Returns one page of remote agents. Stored auth secrets are redacted.", Tag: remoteAgentsTag},
			Handler: Handle(h.list, "Unable to list remote agents")},
		{Method: http.MethodPost, Path: "/remoteAgents", Scope: rbac.ScopeAgentBuilderCreate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "createRemoteAgent", Summary: "Create a remote agent",
				Text: "Connects an agent that Agent Mesh reaches over A2A, without deploying it. Deploy it with POST /agentDeployments.", Tag: remoteAgentsTag},
			Handler: Handle(h.create, "Unable to create remote agent")},
		{Method: http.MethodGet, Path: remoteAgentPath, Scope: rbac.ScopeAgentBuilderRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getRemoteAgent", Summary: "Get a remote agent",
				Text: "Returns one remote agent. Stored auth secrets are redacted. With expand=card, it also reads the agent's card with the stored credentials.", Tag: remoteAgentsTag},
			Handler: Handle(h.get, "Unable to get remote agent")},
		{Method: http.MethodPut, Path: remoteAgentPath, Scope: rbac.ScopeAgentBuilderUpdate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "replaceRemoteAgent", Summary: "Replace a remote agent",
				Text: "Replaces the remote agent with the fields sent. An optional field left out is cleared or reset to its default. An authentication secret left out, sent empty, or sent as <REDACTED> is kept.", Tag: remoteAgentsTag},
			Handler: Handle(h.update, "Unable to update remote agent")},
		{Method: http.MethodDelete, Path: remoteAgentPath, Scope: rbac.ScopeAgentBuilderDelete,
			Stability: StabilityDraft,
			Doc: Doc{ID: "deleteRemoteAgent", Summary: "Delete a remote agent",
				Text: "Undeploys the remote agent if it is deployed, then deletes it. If the undeploy fails, the remote agent is kept and the request fails with 502.", Tag: remoteAgentsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.delete, "Unable to delete remote agent")},
		{Method: http.MethodPut, Path: "/remoteAgents/fetchCard", Scope: rbac.ScopeAgentBuilderCreate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "fetchRemoteAgentCard", Summary: "Get an agent card from a URL",
				Text: "Reads the A2A agent card at the URL sent, with the headers and auth sent, before the remote agent is saved. If the card cannot be fetched or read, the request fails with 502.", Tag: remoteAgentsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.fetchCard, msgCardFetchFailed)},
	}
}

func (h *remoteAgentHandler) list(ctx context.Context, req listRemoteAgentsRequest) (List[RemoteAgent], error) {
	resp, err := h.svc.ListPaginated(ctx, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[RemoteAgent]{}, err
	}
	return ListOf(resp, toRemoteAgent), nil
}

func (h *remoteAgentHandler) create(ctx context.Context, req CreateRemoteAgentRequest) (RemoteAgent, error) {
	resp, err := h.svc.Create(ctx, req.toService(), entplatform.UserIDFromContext(ctx))
	if err != nil {
		return RemoteAgent{}, err
	}
	h.analytics.RemoteAgentCreated(ctx, httputil.ParseClientSurface(req.Surface))
	return toRemoteAgent(resp), nil
}

func (r CreateRemoteAgentRequest) toService() entplatform.CreateRemoteAgentRequest {
	return entplatform.CreateRemoteAgentRequest{
		AgentURL:              r.AgentURL,
		Name:                  r.Name,
		AgentCardLocation:     r.AgentCardLocation,
		Description:           r.Description,
		AgentCardURL:          r.AgentCardURL,
		AgentCardHeaders:      r.AgentCardHeaders,
		AgentCardAuthType:     r.AgentCardAuthType,
		AgentCardAuthConfig:   authConfigValue(r.AgentCardAuthConfig),
		AgentAuthType:         r.AgentAuthType,
		AgentAuthConfig:       authConfigValue(r.AgentAuthConfig),
		TaskHeaders:           r.TaskHeaders,
		RequestTimeoutSeconds: r.RequestTimeoutSeconds,
		Skills:                skillsValue(r.Skills),
		InputModes:            r.InputModes,
		OutputModes:           r.OutputModes,
		ProtocolVersion:       r.ProtocolVersion,
		TrustedCardJWKSURLs:   r.TrustedCardJwksUrls,
		RequireSignedCard:     &r.RequireSignedCard,
	}
}

func (h *remoteAgentHandler) get(ctx context.Context, req getRemoteAgentRequest) (RemoteAgent, error) {
	resp, err := h.svc.GetByIDWithCard(ctx, req.ID, slices.Contains(strings.Split(req.Expand, ","), "card"))
	if err != nil {
		return RemoteAgent{}, err
	}
	return toRemoteAgent(resp), nil
}

func (h *remoteAgentHandler) update(ctx context.Context, req UpdateRemoteAgentRequest) (RemoteAgent, error) {
	resp, err := h.svc.Replace(ctx, req.ID, req.toService(), entplatform.UserIDFromContext(ctx))
	if err != nil {
		return RemoteAgent{}, err
	}
	return toRemoteAgent(resp), nil
}

func (r UpdateRemoteAgentRequest) toService() entplatform.CreateRemoteAgentRequest {
	return entplatform.CreateRemoteAgentRequest{
		AgentURL:              r.AgentURL,
		Name:                  r.Name,
		AgentCardLocation:     r.AgentCardLocation,
		Description:           r.Description,
		AgentCardURL:          r.AgentCardURL,
		AgentCardHeaders:      r.AgentCardHeaders,
		AgentCardAuthType:     r.AgentCardAuthType,
		AgentCardAuthConfig:   authConfigValue(r.AgentCardAuthConfig),
		AgentAuthType:         r.AgentAuthType,
		AgentAuthConfig:       authConfigValue(r.AgentAuthConfig),
		TaskHeaders:           r.TaskHeaders,
		RequestTimeoutSeconds: r.RequestTimeoutSeconds,
		Skills:                skillsValue(r.Skills),
		InputModes:            r.InputModes,
		OutputModes:           r.OutputModes,
		ProtocolVersion:       r.ProtocolVersion,
		TrustedCardJWKSURLs:   r.TrustedCardJwksUrls,
		RequireSignedCard:     &r.RequireSignedCard,
	}
}

func (h *remoteAgentHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.svc.DeleteWithUndeploy(ctx, req.ID, entplatform.UserIDFromContext(ctx), h.deploySvc); err != nil {
		return struct{}{}, err
	}
	h.analytics.RemoteAgentDeleted(ctx)
	return struct{}{}, nil
}

func (h *remoteAgentHandler) fetchCard(ctx context.Context, req FetchRemoteAgentCardRequest) (RemoteAgentCard, error) {
	if details := cardFetchProblems(req); len(details) > 0 {
		return RemoteAgentCard{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	svcReq := entplatform.FetchAgentCardRequest{AgentCardURL: req.AgentCardURL, CustomHeaders: req.CustomHeaders}
	if c := req.AuthConfig; c != nil {
		svcReq.AuthenticationType = c.Type
		svcReq.AuthConfig = &entplatform.CardAuthConfig{
			BearerToken:        orEmpty(c.Token),
			ApikeyValue:        orEmpty(c.APIKey),
			ApikeyHeaderName:   orEmpty(c.HeaderName),
			OAuth2TokenURL:     orEmpty(c.TokenEndpoint),
			OAuth2ClientID:     orEmpty(c.ClientID),
			OAuth2ClientSecret: orEmpty(c.ClientSecret),
			OAuth2Scope:        strings.Join(c.Scopes, " "),
			BasicUsername:      orEmpty(c.Username),
			BasicPassword:      orEmpty(c.Password),
		}
	}
	resp, err := h.svc.FetchAgentCard(ctx, svcReq)
	if err != nil {
		return RemoteAgentCard{}, &CallerError{Status: http.StatusBadGateway, Message: msgCardFetchFailed, Cause: redactedCause{entplatform.LogCause(err)}}
	}
	return *toRemoteAgentCard(resp), nil
}

type redactedCause struct{ err error }

func (c redactedCause) Error() string { return logging.RedactURLQueries(c.err.Error()) }
func (c redactedCause) Unwrap() error { return c.err }

func cardFetchProblems(req FetchRemoteAgentCardRequest) map[string][]string {
	details := map[string][]string{}
	if u, err := url.Parse(req.AgentCardURL); err != nil || u.Host == "" ||
		!strings.HasPrefix(req.AgentCardURL, "http://") && !strings.HasPrefix(req.AgentCardURL, "https://") {
		details["agentCardUrl"] = []string{"must be an http:// or https:// URL"}
	}
	var c RemoteAgentAuthConfig
	if req.AuthConfig != nil {
		c = *req.AuthConfig
	}
	var required map[string]*string
	switch c.Type {
	case "bearer":
		required = map[string]*string{"token": c.Token}
	case "apikey":
		required = map[string]*string{"apiKey": c.APIKey}
	case "oauth2":
		required = map[string]*string{"tokenEndpoint": c.TokenEndpoint, "clientId": c.ClientID, "clientSecret": c.ClientSecret}
	case "basic":
		required = map[string]*string{"username": c.Username, "password": c.Password}
	}
	for name, value := range required {
		if orEmpty(value) == "" {
			details["authConfig."+name] = []string{"is required for " + c.Type + " authentication"}
		}
	}
	return details
}

func authConfigValue(c *RemoteAgentAuthConfig) any {
	if c == nil {
		return nil
	}
	m := withoutUnset(reshape[map[string]any](c))
	m["scopes"] = nonNilList(c.Scopes)
	m["headerName"] = orEmpty(c.HeaderName)
	return m
}

func skillsValue(skills []RemoteAgentSkill) any {
	if skills == nil {
		return nil
	}
	out := reshape[[]map[string]any](skills)
	for _, s := range out {
		withoutUnset(s)
	}
	return out
}

func withoutUnset(m map[string]any) map[string]any {
	maps.DeleteFunc(m, func(_ string, v any) bool {
		list, isList := v.([]any)
		return v == nil || isList && len(list) == 0
	})
	return m
}

func reshape[T any](v any) T {
	var out T
	if v == nil {
		return out
	}
	raw, _ := json.Marshal(v)
	_ = json.Unmarshal(raw, &out)
	return out
}

func toRemoteAgent(resp *entplatform.RemoteAgentResponse) RemoteAgent {
	out := RemoteAgent{
		ID:                    resp.ID,
		Type:                  "remoteAgent",
		Name:                  resp.Name,
		InstanceName:          resp.InstanceName,
		Description:           NullIfZero(resp.Description),
		AgentURL:              resp.AgentURL,
		AgentCardLocation:     resp.AgentCardLocation,
		AgentCardURL:          NullIfZero(resp.AgentCardURL),
		AgentCardHeaders:      nilIfEmpty(reshape[map[string]string](resp.AgentCardHeaders)),
		AgentCardAuthType:     NullIfZero(resp.AgentCardAuthType),
		AgentCardAuthConfig:   toRemoteAgentAuthConfig(resp.AgentCardAuthConfig),
		AgentAuthType:         resp.AgentAuthType,
		AgentAuthConfig:       toRemoteAgentAuthConfig(resp.AgentAuthConfig),
		TaskHeaders:           nilIfEmpty(reshape[map[string]string](resp.TaskHeaders)),
		RequestTimeoutSeconds: resp.RequestTimeoutSeconds,
		Skills:                toRemoteAgentSkills(resp.Skills),
		InputModes:            nonNilList(reshape[[]string](resp.InputModes)),
		OutputModes:           nonNilList(reshape[[]string](resp.OutputModes)),
		ProtocolVersion:       resp.ProtocolVersion,
		TrustedCardJwksUrls:   nonNilList(resp.TrustedCardJWKSURLs),
		RequireSignedCard:     resp.RequireSignedCard,
		RemoteProtocol:        toRemoteAgentProtocol(resp.RemoteProtocol),
		DeploymentStatus:      resp.DeploymentStatus,
		SyncStatus:            resp.SyncStatus,
		RuntimeStatus:         toRuntimeStatus(resp.RuntimeStatus),
		Deployed:              toDeployedRemoteAgent(resp.Deployed),
		CreatedBy:             resp.CreatedBy,
		UpdatedBy:             resp.UpdatedBy,
		CreatedTime:           Instant(resp.CreatedTime),
		UpdatedTime:           Instant(resp.UpdatedTime),
		CardData:              toRemoteAgentCard(resp.CardData),
	}
	if resp.CardFetchError != "" {
		out.CardFetchError = NullIfZero(msgStoredCardFetchFailed)
	}
	return out
}

func toRemoteAgentAuthConfig(v any) *RemoteAgentAuthConfig {
	c := reshape[RemoteAgentAuthConfig](v)
	if c.Type == "" {
		return nil
	}
	c.Scopes = nonNilList(c.Scopes)
	return &c
}

func toRemoteAgentSkills(v any) []RemoteAgentSkill {
	skills := nonNilList(reshape[[]RemoteAgentSkill](v))
	for i := range skills {
		s := &skills[i]
		s.ID = NullIfZero(orEmpty(s.ID))
		s.Tags, s.Examples = nonNilList(s.Tags), nonNilList(s.Examples)
		s.InputModes, s.OutputModes = nonNilList(s.InputModes), nonNilList(s.OutputModes)
	}
	return skills
}

func toDeployedRemoteAgent(v any) *DeployedRemoteAgent {
	d, ok := v.(*entplatform.DeployedRemoteAgentInfo)
	if !ok || d == nil {
		return nil
	}
	return &DeployedRemoteAgent{
		AgentURL:              d.AgentURL,
		Name:                  d.Name,
		Description:           NullIfZero(d.Description),
		AgentCardLocation:     d.AgentCardLocation,
		AgentCardURL:          NullIfZero(d.AgentCardURL),
		AgentCardAuthType:     NullIfZero(d.AgentCardAuthType),
		AgentAuthType:         d.AgentAuthType,
		RequestTimeoutSeconds: d.RequestTimeoutSeconds,
		Skills:                toRemoteAgentSkills(d.Skills),
		InputModes:            nonNilList(reshape[[]string](d.InputModes)),
		OutputModes:           nonNilList(reshape[[]string](d.OutputModes)),
		ProtocolVersion:       d.ProtocolVersion,
		TrustedCardJwksUrls:   nonNilList(d.TrustedCardJWKSURLs),
		RequireSignedCard:     d.RequireSignedCard,
		DeployedTime:          Instant(d.DeployedAt),
		DeployedBy:            d.DeployedBy,
	}
}

func toRemoteAgentCard(c *entplatform.FetchAgentCardResponse) *RemoteAgentCard {
	if c == nil {
		return nil
	}
	return &RemoteAgentCard{
		Name:        c.Name,
		Description: c.Description,
		Skills:      toRemoteAgentSkills(c.Skills),
		InputModes:  nonNilList(reshape[[]string](c.InputModes)),
		OutputModes: nonNilList(reshape[[]string](c.OutputModes)),
		Interfaces:  toRemoteAgentInterfaces(c.Interfaces),
		Signed:      c.Signed,
	}
}

func toRemoteAgentProtocol(p *entplatform.RemoteProtocolStatus) *RemoteAgentProtocol {
	if p == nil {
		return nil
	}
	return &RemoteAgentProtocol{SelectedVersion: p.SelectedVersion, Binding: p.Binding, Deprecated: p.Deprecated, CardSignature: p.CardSignature}
}

func toRemoteAgentInterfaces(ifaces []entplatform.AdvertisedInterface) []RemoteAgentInterface {
	out := make([]RemoteAgentInterface, 0, len(ifaces))
	for _, ai := range ifaces {
		out = append(out, RemoteAgentInterface{Version: ai.Version, Binding: ai.Binding})
	}
	return out
}
