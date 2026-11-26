// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (AuthError) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AuthError is the error body of a 401 or 403.",
		Fields: map[string]apidoc.Field{
			"Message": {Doc: "Message describes what went wrong."},
			"ErrorID": {Doc: "ErrorID identifies this failure in the server log; quote it when reporting a problem."},
			"ErrorType": {Doc: `ErrorType says why the sign-in or permission check refused the request: authentication_required,
invalid_token or session_expired on a 401, and authorization_failure on a 403. A 403 the operation
itself returns, after those checks passed, has none.`},
		},
	}
}
