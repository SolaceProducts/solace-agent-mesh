// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"net/http"
)

func StubAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), userContextKey, &UserInfo{
			ID:    "stub_user",
			Email: "stub@example.com",
			Name:  "Stub User",
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type userKey int

const userContextKey userKey = 0

type UserInfo struct {
	ID    string
	Email string
	Name  string
}

func UserFromContext(ctx context.Context) *UserInfo {
	u, _ := ctx.Value(userContextKey).(*UserInfo)
	return u
}
