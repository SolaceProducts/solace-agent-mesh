// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (h *userHandler) chatV2Routes() []apicontract.Route {
	return chatv2.UserRoutes(chatv2.Deps{ReadUser: h.readChatUser, ReadUserCapabilities: h.readChatUserCapabilities})
}

func signedInCaller(ctx context.Context) (*auth.Subject, error) {
	caller, ok := auth.SubjectFromContext(ctx)
	if !ok || !caller.Authenticated() {
		return nil, chatv2.UserNotSignedIn()
	}
	return caller, nil
}

func (h *userHandler) readChatUser(ctx context.Context, _ struct{}) (chatv2.ChatUser, error) {
	caller, err := signedInCaller(ctx)
	if err != nil {
		return chatv2.ChatUser{}, err
	}
	id := caller.UserID()
	if id == "" {
		id = caller.Email()
	}
	authMethod := caller.AuthMethod()
	if authMethod == "" {
		authMethod = "none"
	}
	return chatv2.ChatUser{
		ID:         id,
		Type:       "user",
		Email:      nonEmpty(caller.Email()),
		AuthMethod: authMethod,
		Scopes:     nonNilList(caller.Scopes()),
		Roles:      nonNilList(caller.Roles()),
	}, nil
}

func (h *userHandler) readChatUserCapabilities(ctx context.Context, req chatv2.ReadUserCapabilitiesRequest) (chatv2.ChatUserCapabilities, error) {
	caller, err := signedInCaller(ctx)
	if err != nil {
		return chatv2.ChatUserCapabilities{}, err
	}
	asked := make([]string, 0)
	seen := make(map[string]bool)
	for _, scope := range strings.Split(req.Scopes, ",") {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		asked = append(asked, scope)
		if len(asked) > chatv2.MaxCapabilityScopes {
			return chatv2.ChatUserCapabilities{}, &apicontract.CallerError{
				Status: http.StatusUnprocessableEntity, Message: apicontract.MsgValidationFailed,
				Details: map[string][]string{"scopes": {fmt.Sprintf("Ask about at most %d scopes at a time.", chatv2.MaxCapabilityScopes)}},
			}
		}
	}
	capabilities := make([]chatv2.UserCapability, 0, len(asked))
	for _, scope := range asked {
		capabilities = append(capabilities, chatv2.UserCapability{Scope: scope, Granted: caller.HasScope(scope)})
	}
	return chatv2.ChatUserCapabilities{Capabilities: capabilities, ManageRbac: rbac.IsSuperAdmin(caller.Scopes())}, nil
}
