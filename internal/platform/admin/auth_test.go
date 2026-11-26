// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestRequireScope_Allowed(t *testing.T) {
	t.Parallel()
	handler := requireScope("agent_builder:*:read", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID: "user1",
		Scopes: []string{"agent_builder:*:read"},
	})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestRequireScope_Denied(t *testing.T) {
	t.Parallel()
	handler := requireScope("agent_builder:*:delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("DELETE", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID: "user1",
		Scopes: []string{"agent_builder:*:read"},
	})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestRequireScope_WildcardAllowed(t *testing.T) {
	t.Parallel()
	handler := requireScope("connector:_:create", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID: "admin",
		Scopes: []string{"*"},
	})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestRequireScope_PrefixWildcard(t *testing.T) {
	t.Parallel()
	handler := requireScope("agent_builder:*:update", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("PUT", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID: "dev",
		Scopes: []string{"agent_builder:*:*"},
	})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestRequireScope_NoIdentity_Returns401(t *testing.T) {
	t.Parallel()
	handler := requireScope("agent_builder:*:read", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (unauthenticated requests must be rejected)", rr.Code)
	}
}

func TestRequireScope_EmptyScopesDenied(t *testing.T) {
	t.Parallel()
	handler := requireScope("connector:*:delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("DELETE", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID: "dev-user",
		Scopes: nil,
	})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (empty scopes must be denied)", rr.Code)
	}
}

func TestRequireScope_DevIdentityAllowsAll(t *testing.T) {
	t.Parallel()
	handler := requireScope("connector:*:delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("DELETE", "/test", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.DevIdentity()))

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (DevIdentity carries \"*\")", rr.Code)
	}
}

func TestRequireScope_WrongResourceDenied(t *testing.T) {
	t.Parallel()
	handler := requireScope("connector:*:read", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID: "user1",
		Scopes: []string{"agent_builder:*:read"},
	})
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}
