// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type capturingAuthzClient struct {
	gatewayContext map[string]any
}

func (c *capturingAuthzClient) Lookup(_ context.Context, _ string, gctx map[string]any) (auth.AuthorizationResolution, error) {
	c.gatewayContext = gctx
	return auth.AuthorizationResolution{Roles: []string{"role"}, Scopes: []string{"scope"}}, nil
}

func mintAndCaptureLookupContext(t *testing.T, upsert *fakeUpsertClient) map[string]any {
	t.Helper()
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:         "user-123",
			Email:       "alice@example.com",
			Name:        "Alice",
			RawResponse: map[string]any{"sub": "user-123", "email": "alice@example.com", "name": "Alice"},
		},
		tokenResponse: &TokenResponse{AccessToken: "idp-at", TokenType: "Bearer", ExpiresIn: 3600},
	}
	p, _ := newAuditingProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000"
	p.userUpsertClient = upsert

	svc, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	p.authzSvc = svc
	cap := &capturingAuthzClient{}
	p.SetAuthorizationClient(cap)

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", w.Code)
	}
	if cap.gatewayContext == nil {
		t.Fatal("authz Lookup was never called")
	}
	return cap.gatewayContext
}

func TestMintFromOIDC_InjectsSamUserIDIntoLookup(t *testing.T) {
	gwCtx := mintAndCaptureLookupContext(t, &fakeUpsertClient{ch: make(chan auth.UserRecord, 1)})
	if got := gwCtx["sam_user_id"]; got != "user-id-fake" {
		t.Errorf("gatewayContext[sam_user_id] = %v, want the provisioned id user-id-fake", got)
	}
	if _, ok := gwCtx["roles_changed"]; ok {
		t.Error("gatewayContext carries roles_changed although the upsert materialized nothing")
	}
}

func TestMintFromOIDC_ForwardsRolesChangedToLookup(t *testing.T) {
	gwCtx := mintAndCaptureLookupContext(t, &fakeUpsertClient{ch: make(chan auth.UserRecord, 1), rolesChanged: true})
	if got := gwCtx["roles_changed"]; got != true {
		t.Errorf("gatewayContext[roles_changed] = %v, want true", got)
	}
}
