// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type userHandler struct {
	core *entrypoint.GatewayCore
}

func (h *userHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	id, ok := auth.SubjectFromContext(r.Context())

	username := "anonymous"
	authenticated := false
	authMethod := "none"
	email := ""
	scopes := []string{}
	roles := []string{}

	if ok {
		if id.UserID() != "" {
			username = id.UserID()
		} else if id.Email() != "" {
			username = id.Email()
		}
		authenticated = id.Authenticated()
		if id.AuthMethod() != "" {
			authMethod = id.AuthMethod()
		}
		if id.Authenticated() {
			email = id.Email()
		}
		if id.Authenticated() && id.Scopes() != nil {
			scopes = id.Scopes()
		}
		if id.Authenticated() && id.Roles() != nil {
			roles = id.Roles()
		}
	} else if cfg.DefaultUserIdentity != "" {
		username = cfg.DefaultUserIdentity
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"username":      username,
		"authenticated": authenticated,
		"authMethod":    authMethod,
		"email":         email,
		"scopes":        scopes,
		"roles":         roles,
	})
}

func (h *userHandler) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	scopesParam := r.URL.Query().Get("scopes")

	capabilities := make(map[string]bool)

	if scopesParam != "" {
		scopes := strings.Split(scopesParam, ",")
		id, ok := auth.SubjectFromContext(r.Context())

		for _, scope := range scopes {
			scope = strings.TrimSpace(scope)
			if scope == "" {
				continue
			}
			if ok && id.Authenticated() {
				capabilities[scope] = id.HasScope(scope)
			} else {
				capabilities[scope] = false
			}
		}
	}

	id, idOK := auth.SubjectFromContext(r.Context())
	capabilities["manageRbac"] = idOK && id.Authenticated() && rbac.IsSuperAdmin(id.Scopes())

	writeJSON(w, http.StatusOK, map[string]any{
		"capabilities": capabilities,
	})
}
