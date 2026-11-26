// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const GateCaller apicontract.Gate = "caller"

var userTag = apicontract.Tag{Name: "Chat User", Group: tagGroup, Text: "Read who the caller is and what the caller may do."}

type ChatUser struct {
	ID string `json:"id" example:"jane.doe"`

	Type string `json:"type" example:"user"`

	Email *string `json:"email" example:"jane.doe@example.com"`

	AuthMethod string `json:"authMethod" example:"oidc"`

	Scopes []string `json:"scopes" example:"[\"agent:*:invoke\",\"session:_:read\"]"`

	Roles []string `json:"roles" example:"[\"user\"]"`
}

type UserCapability struct {
	Scope string `json:"scope" example:"agent:*:invoke"`

	Granted bool `json:"granted" example:"true"`
}

type ChatUserCapabilities struct {
	Capabilities []UserCapability `json:"capabilities"`

	ManageRbac bool `json:"manageRbac" example:"false"`
}

type ReadUserCapabilitiesRequest struct {
	apicontract.StrictRequest

	Scopes string `query:"scopes" json:"-" example:"agent:*:invoke,session:_:read"`
}

const MaxCapabilityScopes = 100

func UserNotSignedIn() error {
	return &apicontract.CallerError{Status: http.StatusUnauthorized, Message: "Sign in to read this."}
}

func userUnavailable[Req, Resp any](fn func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	if fn == nil {
		return func(context.Context, Req) (resp Resp, err error) { return resp, UserNotSignedIn() }
	}
	return fn
}

func UserRoutes(deps Deps) []apicontract.Route {
	return []apicontract.Route{
		{Method: http.MethodGet, Path: "/user", Gate: GateCaller, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatUser", Summary: "Get the current user",
				Text: "Returns who the caller is and every scope the caller holds. " +
					"A client reads the scopes once after login and decides which parts of its interface to offer by matching a scope it needs against them, by the rules the scopes field describes. " +
					"A request with no signed-in caller answers 401.",
				Tag: userTag},
			Handler: apicontract.Handle(userUnavailable(deps.ReadUser), "Unable to get the current user")},
		{Method: http.MethodGet, Path: "/user/capabilities", Gate: GateCaller, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatUserCapabilities", Summary: "Get the capabilities of the current user",
				Text: "Answers, for each scope in the scopes query parameter, whether the caller holds it, so a client can check a few scopes without reading them all. " +
					"The manageRbac field is always present. A request with no signed-in caller answers 401.",
				Tag: userTag},
			Handler: apicontract.Handle(userUnavailable(deps.ReadUserCapabilities), "Unable to get the capabilities of the current user")},
	}
}
