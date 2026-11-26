// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestScoped_EmptyScopesDenied(t *testing.T) {
	okHandler := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	h := scoped("connector:*:delete", okHandler)

	t.Run("empty scopes denied", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/x", nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID: "alice", Authenticated: true,
		})))
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (empty scopes must be denied)", rec.Code)
		}
	})

	t.Run("DevIdentity wildcard allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/x", nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.DevIdentity()))
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (DevIdentity carries \"*\")", rec.Code)
		}
	})
}
