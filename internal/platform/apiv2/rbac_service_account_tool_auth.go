// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ServiceAccountAgentToolAuth struct {
	ServiceAccountID string                         `json:"serviceAccountId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Sub              string                         `json:"sub" example:"system:nightly-sync"`
	AgentName        string                         `json:"agentName" example:"support_agent"`
	Credentials      []ServiceAccountToolCredential `json:"credentials"`
}

type ServiceAccountToolCredential struct {
	CredentialKey          string  `json:"credentialKey" example:"google_drive"`
	DisplayName            *string `json:"displayName" example:"Google Drive"`
	AuthType               *string `json:"authType" example:"oauth2"`
	Interactive            bool    `json:"interactive" example:"true"`
	Status                 string  `json:"status" example:"connected"`
	ExpiresTime            Instant `json:"expiresTime"`
	AuthorizationBegunBy   *string `json:"authorizationBegunBy" example:"jane.doe@example.com"`
	AuthorizationBegunTime Instant `json:"authorizationBegunTime"`
	LastAuthorizedBy       *string `json:"lastAuthorizedBy" example:"jane.doe@example.com"`
	LastAuthorizedTime     Instant `json:"lastAuthorizedTime"`
	LastDisconnectedBy     *string `json:"lastDisconnectedBy" example:"jane.doe@example.com"`
	LastDisconnectedTime   Instant `json:"lastDisconnectedTime"`
}

type ServiceAccountToolAuthorization struct {
	AuthURI       string  `json:"authUri" example:"https://accounts.example.com/authorize?client_id=sam&state=0190f5f1"`
	CredentialKey string  `json:"credentialKey" example:"google_drive"`
	ExpiresTime   Instant `json:"expiresTime"`
}

type ServiceAccountToolCredentialRequest struct {
	ServiceAccountID string `path:"serviceAccountId" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	AgentName        string `json:"agentName" validate:"required" example:"support_agent"`
	CredentialKey    string `json:"credentialKey" validate:"required" example:"google_drive"`
}

type serviceAccountAgentToolAuthRequest struct {
	ServiceAccountID string `path:"serviceAccountId" json:"-"`
	AgentName        string `path:"agentName,text" json:"-"`
}

type serviceAccountToolAuthHandler struct {
	svc *entplatform.SystemUserToolAuthService
}

const serviceAccountToolAuthPath = serviceAccountsPath + "/{serviceAccountId}/toolAuth"

const serviceAccountToolAuthErrors = " If the service account does not exist, the request fails with 404. If it is built in, the request fails with 409. If no agent executor runs the agent, the request fails with 404, and if none answers, with 503. If the agent refuses the call, the request fails with 502."

func serviceAccountToolAuthRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.SystemUserToolAuthSvc == nil {
		return nil
	}
	h := &serviceAccountToolAuthHandler{svc: deps.SystemUserToolAuthSvc}
	return []Route{
		{Method: http.MethodGet, Path: serviceAccountToolAuthPath + "/agents/{agentName}", Gate: GateSuperAdminRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getServiceAccountAgentToolAuth", Summary: "Get the tool credentials an agent holds for a service account",
				Text: "Returns every credential the agent can hold for the custom service account, whether the service account holds it, and who last authorized or disconnected it through this API. Reading the list is also how the platform learns that an authorization begun here completed." + serviceAccountToolAuthErrors, Tag: serviceAccountToolAuthTag,
				Errors: []int{http.StatusConflict, http.StatusServiceUnavailable, http.StatusBadGateway}},
			Handler: Handle(h.get, "Unable to get service account tool authorization")},
		{Method: http.MethodPut, Path: serviceAccountToolAuthPath + "/begin", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUserToolAuth,
			Stability: StabilityDraft,
			Doc: Doc{ID: "beginServiceAccountToolAuth", Summary: "Initiate the authorization of a tool credential for a service account",
				Text: "Starts the browser sign-in for one credential of one agent and returns the link to open. The account the person signs in with is stored for the custom service account, so every run as the service account acts with it, evaluation experiments that run as it included. A task running as a service account is never shown a sign-in prompt itself. If the agent does not have the credential, the request fails with 404. If the credential cannot be authorized from a browser, the request fails with 422." + serviceAccountToolAuthErrors, Tag: serviceAccountToolAuthTag,
				Errors: []int{http.StatusServiceUnavailable, http.StatusBadGateway}},
			Handler: Handle(h.begin, "Unable to begin service account tool authorization")},
		{Method: http.MethodPut, Path: serviceAccountToolAuthPath + "/disconnect", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUserToolAuth,
			Stability: StabilityDraft,
			Doc: Doc{ID: "disconnectServiceAccountToolAuth", Summary: "Update a service account to disconnect a tool credential",
				Text: "Wipes the credential one agent holds for the custom service account and records who wiped it. If the agent does not have the credential, the request fails with 404." + serviceAccountToolAuthErrors, Tag: serviceAccountToolAuthTag,
				Errors: []int{http.StatusServiceUnavailable, http.StatusBadGateway}},
			Handler: Handle(h.disconnect, "Unable to disconnect service account tool authorization")},
	}
}

func (h *serviceAccountToolAuthHandler) get(ctx context.Context, req serviceAccountAgentToolAuthRequest) (ServiceAccountAgentToolAuth, error) {
	held, err := h.svc.AgentCredentials(ctx, req.ServiceAccountID, req.AgentName)
	if err != nil {
		return ServiceAccountAgentToolAuth{}, serviceAccountToolAuthError(err)
	}
	credentials := make([]ServiceAccountToolCredential, len(held.Credentials))
	for i, c := range held.Credentials {
		credentials[i] = toServiceAccountToolCredential(c)
	}
	return ServiceAccountAgentToolAuth{
		ServiceAccountID: held.SystemUserID,
		Sub:              held.Principal,
		AgentName:        held.AgentName,
		Credentials:      credentials,
	}, nil
}

func (h *serviceAccountToolAuthHandler) begin(ctx context.Context, req ServiceAccountToolCredentialRequest) (ServiceAccountToolAuthorization, error) {
	begun, err := h.svc.Begin(ctx, req.ServiceAccountID, req.AgentName, req.CredentialKey, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return ServiceAccountToolAuthorization{}, serviceAccountToolAuthError(err)
	}
	return ServiceAccountToolAuthorization{
		AuthURI:       begun.AuthURI,
		CredentialKey: begun.CredentialKey,
		ExpiresTime:   Instant(begun.ExpiresAt),
	}, nil
}

func (h *serviceAccountToolAuthHandler) disconnect(ctx context.Context, req ServiceAccountToolCredentialRequest) (struct{}, error) {
	err := h.svc.Disconnect(ctx, req.ServiceAccountID, req.AgentName, req.CredentialKey, entplatform.UserIDFromContext(ctx))
	return struct{}{}, serviceAccountToolAuthError(err)
}

func toServiceAccountToolCredential(c entplatform.SystemUserToolCredential) ServiceAccountToolCredential {
	out := ServiceAccountToolCredential{
		CredentialKey: c.CredentialKey,
		DisplayName:   NullIfZero(c.DisplayName),
		AuthType:      NullIfZero(c.AuthType),
		Interactive:   c.Interactive,
		Status:        c.Status,
	}
	if !c.ExpiresAt.IsZero() {
		out.ExpiresTime = Instant(c.ExpiresAt.UnixMilli())
	}
	record := c.Record
	if record == nil {
		return out
	}
	if record.State == store.SystemUserToolAuthPending {
		out.AuthorizationBegunBy = NullIfEmpty(record.BegunBy)
		out.AuthorizationBegunTime = InstantOf(record.BegunTime)
	}
	out.LastAuthorizedBy = NullIfEmpty(record.AuthorizedBy)
	out.LastAuthorizedTime = InstantOf(record.AuthorizedTime)
	out.LastDisconnectedBy = NullIfEmpty(record.DisconnectedBy)
	out.LastDisconnectedTime = InstantOf(record.DisconnectedTime)
	return out
}

func serviceAccountToolAuthError(err error) error {
	return rbacError(toolAuthCallError(err))
}
