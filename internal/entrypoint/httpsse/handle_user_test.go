// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestHandleMe_ReturnsUserInfo(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	uh := &userHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user", uh.handleMe)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/user", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"username", "authenticated", "authMethod"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key: %s", key)
		}
	}

	username, _ := resp["username"].(string)
	if username == "" {
		t.Error("expected non-empty username")
	}
}

func TestHandleCapabilities_ManageRbacRequiresSuperAdmin(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	uh := &userHandler{core: adapter.core}

	tests := []struct {
		name     string
		identity *auth.Subject
		want     bool
	}{
		{"bare star", auth.NewTestSubject(auth.SubjectFields{Authenticated: true, Scopes: []string{"*"}}), true},
		{"three-segment star", auth.NewTestSubject(auth.SubjectFields{Authenticated: true, Scopes: []string{"*:*:*"}}), true},
		{"rbac write scopes are not enough", auth.NewTestSubject(auth.SubjectFields{Authenticated: true, Scopes: []string{"rbac:_:create", "rbac:_:update", "rbac:_:delete"}}), false},
		{"rbac category wildcard is not enough", auth.NewTestSubject(auth.SubjectFields{Authenticated: true, Scopes: []string{"rbac:*:*"}}), false},
		{"no scopes", auth.NewTestSubject(auth.SubjectFields{Authenticated: true}), false},
		{"unauthenticated holder of star", auth.NewTestSubject(auth.SubjectFields{Scopes: []string{"*"}}), false},
		{"no identity", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/user/capabilities", nil)
			if tc.identity != nil {
				req = req.WithContext(auth.ContextWithSubject(req.Context(), tc.identity))
			}
			rec := httptest.NewRecorder()
			uh.handleCapabilities(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var resp struct {
				Capabilities map[string]bool `json:"capabilities"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := resp.Capabilities["manageRbac"]; got != tc.want {
				t.Errorf("manageRbac = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandleCapabilities_ReturnsCapabilities(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	uh := &userHandler{core: adapter.core}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/user/capabilities", uh.handleCapabilities)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/user/capabilities?scopes=read,write", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	caps, ok := resp["capabilities"]
	if !ok {
		t.Fatal("missing 'capabilities' key")
	}

	capsMap, ok := caps.(map[string]any)
	if !ok {
		t.Fatalf("capabilities is not a map: %T", caps)
	}

	for _, scope := range []string{"read", "write"} {
		if _, ok := capsMap[scope]; !ok {
			t.Errorf("missing capability scope: %s", scope)
		}
	}
}
