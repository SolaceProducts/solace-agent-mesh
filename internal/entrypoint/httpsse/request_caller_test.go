// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestRequestCaller(t *testing.T) {
	t.Run("identity in the context is used as-is", func(t *testing.T) {
		staged := auth.NewTestSubject(auth.SubjectFields{UserID: "alice@example.com"})
		r := httptest.NewRequest("POST", "/api/v1/message:send", nil)
		r = r.WithContext(auth.ContextWithSubject(r.Context(), staged))

		if got := requestCaller(r, "fallback-user"); got != staged {
			t.Errorf("got %+v, want the identity from the context itself", got)
		}
	})

	t.Run("nothing in the context carries only the user id", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/api/v1/message:send", nil)

		got := requestCaller(r, "fallback-user")

		if got.UserID() != "fallback-user" {
			t.Errorf("UserID = %q, want %q", got.UserID(), "fallback-user")
		}
		if got.FullIdentity() {
			t.Error("FullIdentity() = true; nothing vouched for this request")
		}
	})
}
