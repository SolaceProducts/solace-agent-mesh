// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func scoped(scope string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.SubjectFromContext(r.Context())
		if ok && !id.HasScope(scope) {
			writeError(w, http.StatusForbidden, "insufficient scope: "+scope+" required")
			return
		}
		h(w, r)
	}
}
