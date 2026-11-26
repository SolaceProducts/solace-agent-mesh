// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"net/http/httptest"
	"slices"
	"testing"
)

func TestIdentityCaller(t *testing.T) {
	got := (&Subject{userID: "sub-1", email: "a@example.com", name: "Alice", scopes: []string{"agent:x:invoke"}, credentialID: "key-1"}).Caller()
	if got.Sub != "sub-1" || got.Email != "a@example.com" || got.Name != "Alice" || len(got.Scopes) != 1 || got.CredentialID != "key-1" {
		t.Errorf("request-side caller wrong: %+v", got)
	}
	if got.Resolved() {
		t.Error("no canonical ID expected in the request path yet")
	}

	var nilID *Subject
	if c := nilID.Caller(); c.Sub != "" || c.Email != "" {
		t.Errorf("nil identity should yield zero caller, got %+v", c)
	}
}

func TestCallerFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(ContextWithSubject(r.Context(), &Subject{userID: "sub-1", email: "a@example.com"}))
	got, ok := CallerFromRequest(r)
	if !ok || got.Email != "a@example.com" {
		t.Fatalf("CallerFromRequest ok=%v got=%+v", ok, got)
	}

	if _, ok := CallerFromRequest(httptest.NewRequest("GET", "/", nil)); ok {
		t.Error("expected ok=false when no identity is in context")
	}
}

func TestIsZero(t *testing.T) {
	for name, tc := range map[string]struct {
		s    *Subject
		want bool
	}{
		"nil":           {nil, true},
		"empty":         {&Subject{}, true},
		"userID":        {&Subject{userID: "alice"}, false},
		"authenticated": {&Subject{authenticated: true}, false},
		"authMethod":    {&Subject{authMethod: "oidc"}, false},
		"scopes":        {&Subject{scopes: []string{"agent:x:invoke"}}, false},
		"roles":         {&Subject{roles: []string{"viewer"}}, false},
		"email":         {&Subject{email: "a@example.com"}, false},
		"name":          {&Subject{name: "Alice"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.s.IsZero(); got != tc.want {
				t.Errorf("IsZero() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadPathsDoNotExposeTheSealedFields(t *testing.T) {
	s := &Subject{
		roles:    []string{"viewer"},
		scopes:   []string{"agent:x:invoke"},
		metadata: map[string]any{"source": "web"},
	}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(ContextWithSubject(r.Context(), s))

	s.Roles()[0] = "admin"
	s.Scopes()[0] = "*"
	s.Metadata()["source"] = "forged"
	caller, _ := CallerFromRequest(r)
	caller.Scopes[0] = "*"

	if got, _ := SubjectFromContext(r.Context()); !slices.Equal(got.roles, []string{"viewer"}) ||
		!slices.Equal(got.scopes, []string{"agent:x:invoke"}) || got.metadata["source"] != "web" {
		t.Errorf("a returned value wrote through to the Subject: roles=%v scopes=%v metadata=%v",
			got.roles, got.scopes, got.metadata)
	}
}
