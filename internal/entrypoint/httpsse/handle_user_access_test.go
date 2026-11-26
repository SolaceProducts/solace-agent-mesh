// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestHandleCapabilities_ScopeDenied(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	uh := &userHandler{core: adapter.core}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user/capabilities", uh.handleCapabilities)

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice@example.com",
		Authenticated: true,
		Scopes:        []string{"rbac:_:create"},
	})
	req := httptest.NewRequest("GET", "/api/v1/user/capabilities?scopes=rbac:_:delete", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, present := body.Capabilities["rbac:_:delete"]
	if !present {
		t.Fatal("expected 'rbac:_:delete' key in capabilities")
	}
	if got {
		t.Error("expected rbac:_:delete=false for identity that does not hold the scope")
	}
}

func TestHandleCapabilities_ScopeGranted(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	uh := &userHandler{core: adapter.core}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user/capabilities", uh.handleCapabilities)

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "bob@example.com",
		Authenticated: true,
		Scopes:        []string{"rbac:_:create"},
	})
	req := httptest.NewRequest("GET", "/api/v1/user/capabilities?scopes=rbac:_:create", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, present := body.Capabilities["rbac:_:create"]
	if !present {
		t.Fatal("expected 'rbac:_:create' key in capabilities")
	}
	if !got {
		t.Error("expected rbac:_:create=true for identity that holds the scope")
	}
}

func TestHandleMe_WithAuthenticatedIdentity(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	uh := &userHandler{core: adapter.core}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user", uh.handleMe)

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "carol@example.com",
		Email:         "carol@example.com",
		Authenticated: true,
		AuthMethod:    "oidc",
		Scopes:        []string{"rbac:_:create"},
		Roles:         []string{"sam_admin"},
	})
	req := httptest.NewRequest("GET", "/api/v1/user", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := body["username"]; got != "carol@example.com" {
		t.Errorf("username: got %v, want carol@example.com", got)
	}
	if got := body["authenticated"]; got != true {
		t.Errorf("authenticated: got %v, want true", got)
	}
	if got := body["authMethod"]; got != "oidc" {
		t.Errorf("authMethod: got %v, want oidc", got)
	}
	if got := body["email"]; got != "carol@example.com" {
		t.Errorf("email: got %v, want carol@example.com", got)
	}
	gotScopes, ok := body["scopes"].([]any)
	if !ok {
		t.Fatalf("scopes: got %T, want []any", body["scopes"])
	}
	if len(gotScopes) != 1 || gotScopes[0] != "rbac:_:create" {
		t.Errorf("scopes: got %v, want [rbac:_:create]", gotScopes)
	}
	gotRoles, ok := body["roles"].([]any)
	if !ok {
		t.Fatalf("roles: got %T, want []any", body["roles"])
	}
	if len(gotRoles) != 1 || gotRoles[0] != "sam_admin" {
		t.Errorf("roles: got %v, want [sam_admin]", gotRoles)
	}
}

func TestHandleMe_WithheldScopesWhenUnauthenticated(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	uh := &userHandler{core: adapter.core}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user", uh.handleMe)

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "dave@example.com",
		Email:         "dave@example.com",
		Authenticated: false,
		AuthMethod:    "oidc",
		Scopes:        []string{"rbac:_:create"},
		Roles:         []string{"sam_admin"},
	})
	req := httptest.NewRequest("GET", "/api/v1/user", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := body["authenticated"]; got != false {
		t.Errorf("authenticated: got %v, want false", got)
	}
	if got := body["email"]; got != "" {
		t.Errorf("email: got %v, want \"\" (withheld while unauthenticated)", got)
	}
	gotScopes, ok := body["scopes"].([]any)
	if !ok {
		t.Fatalf("scopes: got %T, want []any", body["scopes"])
	}
	if len(gotScopes) != 0 {
		t.Errorf("scopes: got %v, want []", gotScopes)
	}
	gotRoles, ok := body["roles"].([]any)
	if !ok {
		t.Fatalf("roles: got %T, want []any", body["roles"])
	}
	if len(gotRoles) != 0 {
		t.Errorf("roles: got %v, want [] (withheld while unauthenticated)", gotRoles)
	}
}

func TestHandleMe_EmptyScopesWhenNoIdentity(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	uh := &userHandler{core: adapter.core}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user", uh.handleMe)

	req := httptest.NewRequest("GET", "/api/v1/user", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"scopes":[]`) {
		t.Errorf("expected empty scopes array, got body: %s", body)
	}
}
