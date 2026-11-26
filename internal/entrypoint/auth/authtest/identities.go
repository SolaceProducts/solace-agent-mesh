// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package authtest

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func Owner() *auth.Subject { return identity("owner@example.com") }

func Editor() *auth.Subject { return identity("editor@example.com") }

func Viewer() *auth.Subject { return identity("viewer@example.com") }

func Unrelated() *auth.Subject { return identity("unrelated@example.com") }

func With(req *http.Request, id *auth.Subject) *http.Request {
	return req.WithContext(auth.ContextWithSubject(req.Context(), id))
}

func identity(email string) *auth.Subject {
	return auth.NewTestSubject(auth.SubjectFields{
		UserID:        email,
		Email:         email,
		Authenticated: true,
		Scopes:        []string{rbac.ScopeAgentInvokeAny},
	})
}
