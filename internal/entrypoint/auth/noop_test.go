// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestNoopProvider_Middleware_IdentityOverride(t *testing.T) {
	tests := []struct {
		name         string
		defaultID    string
		forceID      string
		headerUserID string
		headerEmail  string
		wantUserID   string
		wantEmail    string
	}{
		{
			name:       "no overrides yields DevIdentity defaults",
			wantUserID: "sam_dev_user",
			wantEmail:  "sam@dev.local",
		},
		{
			name:       "default_user_identity applies when no header",
			defaultID:  "alice",
			wantUserID: "alice",
			wantEmail:  "alice@dev.local",
		},
		{
			name:       "force_user_identity wins over default",
			defaultID:  "alice",
			forceID:    "bob",
			wantUserID: "bob",
			wantEmail:  "bob@dev.local",
		},
		{
			name:         "X-SAM-User-ID header overrides default and derives email",
			defaultID:    "alice",
			headerUserID: "carol",
			wantUserID:   "carol",
			wantEmail:    "carol@dev.local",
		},
		{
			name:         "X-SAM-User-Email overrides derived email when paired with X-SAM-User-ID",
			headerUserID: "owner",
			headerEmail:  "owner@e2e.test",
			wantUserID:   "owner",
			wantEmail:    "owner@e2e.test",
		},
		{
			name:        "X-SAM-User-Email alone is ignored to keep ID and email aligned",
			headerEmail: "stranger@e2e.test",
			wantUserID:  "sam_dev_user",
			wantEmail:   "sam@dev.local",
		},
		{
			name:         "X-SAM-User-ID with empty X-SAM-User-Email falls back to derived email",
			headerUserID: "viewer",
			headerEmail:  "",
			wantUserID:   "viewer",
			wantEmail:    "viewer@dev.local",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewNoopProvider(tc.defaultID, tc.forceID)

			var got *Subject
			handler := provider.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if id, ok := SubjectFromContext(r.Context()); ok {
					got = id
				}
			}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
			if tc.headerUserID != "" {
				req.Header.Set("X-SAM-User-ID", tc.headerUserID)
			}
			if tc.headerEmail != "" {
				req.Header.Set("X-SAM-User-Email", tc.headerEmail)
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if got == nil {
				t.Fatalf("identity not set in request context")
			}
			if got.userID != tc.wantUserID {
				t.Errorf("UserID: got %q, want %q", got.userID, tc.wantUserID)
			}
			if got.email != tc.wantEmail {
				t.Errorf("Email: got %q, want %q", got.email, tc.wantEmail)
			}
		})
	}
}

func TestNoopProvider_Middleware_ScopesOverride(t *testing.T) {
	runNoopWithHeader := func(t *testing.T, scopesHeader string, setHeader bool) *Subject {
		t.Helper()
		var got *Subject
		handler := NewNoopProvider("", "").Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if id, ok := SubjectFromContext(r.Context()); ok {
				got = id
			}
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		if setHeader {
			req.Header.Set("X-SAM-Scopes", scopesHeader)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if got == nil {
			t.Fatalf("identity not set in request context")
		}
		return got
	}

	t.Run("no header keeps DevIdentity wildcard so HasScope allows all", func(t *testing.T) {
		id := runNoopWithHeader(t, "", false)
		if len(id.scopes) != 1 || id.scopes[0] != "*" {
			t.Fatalf("Scopes: got %v, want [*] (DevIdentity wildcard)", id.scopes)
		}
		if !id.HasScope("agent_builder:_:create") {
			t.Error("DevIdentity \"*\" must remain allow-all for existing specs")
		}
	})

	t.Run("header injects scopes and HasScope enforces them", func(t *testing.T) {
		id := runNoopWithHeader(t, "agent_builder:*:read, evaluation:_:read", true)
		want := []string{"agent_builder:*:read", "evaluation:_:read"}
		if len(id.scopes) != len(want) || id.scopes[0] != want[0] || id.scopes[1] != want[1] {
			t.Fatalf("Scopes: got %v, want %v", id.scopes, want)
		}
		if !id.HasScope("evaluation:_:read") {
			t.Error("injected scope should be granted")
		}
		if id.HasScope("agent_builder:_:create") {
			t.Error("scope outside the injected set must be denied")
		}
	})

	t.Run("blank and empty entries are dropped", func(t *testing.T) {
		id := runNoopWithHeader(t, " , rbac:_:read ,,", true)
		if len(id.scopes) != 1 || id.scopes[0] != "rbac:_:read" {
			t.Fatalf("Scopes: got %v, want [rbac:_:read]", id.scopes)
		}
	})

	t.Run("identity and scopes headers combine like an e2e persona", func(t *testing.T) {
		var got *Subject
		handler := NewNoopProvider("", "").Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if id, ok := SubjectFromContext(r.Context()); ok {
				got = id
			}
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		req.Header.Set("X-SAM-User-ID", "viewer")
		req.Header.Set("X-SAM-User-Email", "viewer@e2e.test")
		req.Header.Set("X-SAM-Scopes", "agent_builder:*:read, evaluation:_:read")
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if got == nil {
			t.Fatalf("identity not set in request context")
		}
		if got.userID != "viewer" {
			t.Errorf("UserID: got %q, want %q", got.userID, "viewer")
		}
		if got.email != "viewer@e2e.test" {
			t.Errorf("Email: got %q, want %q", got.email, "viewer@e2e.test")
		}
		want := []string{"agent_builder:*:read", "evaluation:_:read"}
		if len(got.scopes) != len(want) || got.scopes[0] != want[0] || got.scopes[1] != want[1] {
			t.Fatalf("Scopes: got %v, want %v", got.scopes, want)
		}
		if !got.HasScope("evaluation:_:read") {
			t.Error("injected scope should be granted")
		}
		if got.HasScope("agent_builder:_:create") {
			t.Error("scope outside the injected set must be denied")
		}
	})
}

func TestNoopProvider_Middleware_ScopeResolution(t *testing.T) {
	tests := []struct {
		name     string
		resolver func(string, []string) ([]string, bool)
		want     []string
	}{
		{
			name: "no resolver keeps DevIdentity wildcard",
			want: []string{"*"},
		},
		{
			name:     "resolver present overrides wildcard with resolved scopes",
			resolver: func(string, []string) ([]string, bool) { return []string{"agent:hr-bot:invoke"}, true },
			want:     []string{"agent:hr-bot:invoke"},
		},
		{
			name:     "resolver present with empty result denies (deny_all)",
			resolver: func(string, []string) ([]string, bool) { return []string{}, true },
			want:     []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewNoopProvider("", "")
			if tc.resolver != nil {
				provider.SetScopeResolver(tc.resolver)
			}

			var got *Subject
			handler := provider.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				if id, ok := SubjectFromContext(r.Context()); ok {
					got = id
				}
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))

			if got == nil {
				t.Fatalf("identity not set in request context")
			}
			if !slices.Equal(got.scopes, tc.want) {
				t.Errorf("Scopes: got %v, want %v", got.scopes, tc.want)
			}
		})
	}
}

func TestNoopProvider_Middleware_HeaderWinsOverResolver(t *testing.T) {
	provider := NewNoopProvider("", "")
	provider.SetScopeResolver(func(string, []string) ([]string, bool) { return []string{"*"}, true })

	var got *Subject
	handler := provider.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if id, ok := SubjectFromContext(r.Context()); ok {
			got = id
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-SAM-Scopes", "agent_builder:*:read,connector:*:read")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got == nil {
		t.Fatalf("identity not set in request context")
	}
	want := []string{"agent_builder:*:read", "connector:*:read"}
	if !slices.Equal(got.scopes, want) {
		t.Errorf("Scopes: got %v, want %v (persona header must win over the auth-off \"*\" resolver)", got.scopes, want)
	}
}
