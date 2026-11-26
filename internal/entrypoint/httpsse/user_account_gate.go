// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	msgScheduledTasksNeedUserAccount  = "scheduled tasks require a user account"
	msgProjectSharingNeedsUserAccount = "project sharing requires a user account"
	msgShareLinksNeedUserAccount      = "share links require a user account"
)

func requireUserAccount(message string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isMachineCaller(r) {
			writeError(w, http.StatusForbidden, message)
			return
		}
		next(w, r)
	}
}

func isMachineCaller(r *http.Request) bool {
	identity, ok := auth.SubjectFromContext(r.Context())
	return ok && identity != nil &&
		(identity.IsAPIKey() || rbac.IsSystemPrincipal(identity.UserID()))
}

func writeEmailRequired(w http.ResponseWriter, userID string) {
	if rbac.IsSystemPrincipal(userID) {
		writeError(w, http.StatusForbidden, msgShareLinksNeedUserAccount)
		return
	}
	writeError(w, http.StatusUnauthorized, "Email required")
}
