// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const authMethodAPIKey = auth.APIKeyAuthMethod

var interactiveAuthMethods = map[string]struct{}{
	"oidc":             {},
	"sam_access_token": {},
}

type apiKeyHandler struct {
	svc    *APIKeyService
	logger *slog.Logger
}

func newAPIKeyHandler(svc *APIKeyService, logger *slog.Logger) *apiKeyHandler {
	return &apiKeyHandler{svc: svc, logger: withLoggerName(logger, "api-key-handler")}
}

var apiKeyReadRefusal = readRefusal{
	logMessage: "api key read refused",
	message:    "Reading API keys requires a super administrator",
}

func (h *apiKeyHandler) guardInteractiveSuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return h.requireInteractive(requireSuperAdmin(h.logger, auditResourceAPIKey, next))
}

func (h *apiKeyHandler) requireInteractive(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.SubjectFromContext(r.Context())
		if ok && identity != nil {
			_, interactive := interactiveAuthMethods[identity.AuthMethod()]
			if !interactive || rbac.IsSystemPrincipal(identity.UserID()) {
				logRBACAuditDenied(withAuditCredential(r.Context(), h.logger), auditResourceAPIKey, userIDFromRequest(r),
					"reason", "caller is not interactively authenticated",
					"authMethod", identity.AuthMethod())
				auth.WriteErrorReq(w, r, http.StatusForbidden, auth.ErrorAuthorizationFailure,
					"API keys can only be created, renamed or deleted by a signed-in super administrator")
				return
			}
		}
		next(w, r)
	}
}
