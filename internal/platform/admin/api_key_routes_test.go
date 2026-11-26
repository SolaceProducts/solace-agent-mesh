// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func actorFields(scopes ...string) auth.SubjectFields {
	return auth.SubjectFields{
		UserID:        "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77",
		Email:         "actor@example.com",
		Authenticated: true,
		AuthMethod:    "sam_access_token",
		Scopes:        scopes,
	}
}

func interactiveIdentity(scopes ...string) *auth.Subject {
	return auth.NewTestSubject(actorFields(scopes...))
}

func keyIdentity(scopes ...string) *auth.Subject {
	f := actorFields(scopes...)
	f.AuthMethod = authMethodAPIKey
	return auth.NewTestSubject(f)
}
