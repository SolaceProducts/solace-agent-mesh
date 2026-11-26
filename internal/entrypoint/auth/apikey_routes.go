// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import "net/http"

const MsgAPIKeyRouteNotAllowed = "API keys can only call the Platform configuration API"

func RestrictAPIKeys(mux *http.ServeMux, unroutable http.Handler, allowed map[string]bool, onReject ...func(r *http.Request, status int)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAPIKeyCaller(r) {
				next.ServeHTTP(w, r)
				return
			}
			_, pattern := mux.Handler(r)
			if pattern == "" {
				unroutable.ServeHTTP(w, r)
				return
			}
			if !allowed[pattern] {
				WriteErrorReq(w, r, http.StatusForbidden, ErrorAuthorizationFailure, MsgAPIKeyRouteNotAllowed)
				for _, fn := range onReject {
					if fn != nil {
						fn(r, http.StatusForbidden)
					}
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isAPIKeyCaller(r *http.Request) bool {
	identity, _ := SubjectFromContext(r.Context())
	return identity.IsAPIKey()
}

func (id *Subject) IsAPIKey() bool {
	return id != nil && id.authMethod == APIKeyAuthMethod
}
