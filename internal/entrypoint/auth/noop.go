// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import "net/http"

type NoopProvider struct {
	defaultUserID string
	forceUserID   string
	resolveScopes func(userID string, roles []string) ([]string, bool)
}

func (p *NoopProvider) SetScopeResolver(fn func(userID string, roles []string) ([]string, bool)) {
	p.resolveScopes = fn
}

func NewNoopProvider(defaultUserID, forceUserID string) *NoopProvider {
	return &NoopProvider{
		defaultUserID: defaultUserID,
		forceUserID:   forceUserID,
	}
}

func (p *NoopProvider) Middleware() func(http.Handler) http.Handler {
	effectiveID := p.defaultUserID
	if p.forceUserID != "" {
		effectiveID = p.forceUserID
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := ForNoopDevCaller(effectiveID, DevHeaders{
				UserID: r.Header.Get("X-SAM-User-ID"),
				Email:  r.Header.Get("X-SAM-User-Email"),
				Scopes: scopesFromHeader(r),
			}, p.resolveScopes)
			ctx := ContextWithSubject(r.Context(), identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (p *NoopProvider) RegisterRoutes(mux *http.ServeMux) {
	RegisterAuthRoutes(mux)
}

func (p *NoopProvider) ProviderType() string {
	return ProviderTypeNoop
}
