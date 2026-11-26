// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContextWithSubject_RoundTrip(t *testing.T) {
	id := &Subject{userID: "alice", email: "alice@example.com", authenticated: true}
	ctx := ContextWithSubject(context.Background(), id)

	got, ok := SubjectFromContext(ctx)
	if !ok {
		t.Fatal("expected identity in context")
	}
	if got.userID != "alice" {
		t.Errorf("UserID = %q, want %q", got.userID, "alice")
	}
	if got.email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", got.email, "alice@example.com")
	}
}

func TestSubjectFromContext_NotSet(t *testing.T) {
	_, ok := SubjectFromContext(context.Background())
	if ok {
		t.Fatal("expected no identity in empty context")
	}
}

func TestUserIDFromRequest_WithIdentity(t *testing.T) {
	id := &Subject{userID: "alice"}
	ctx := ContextWithSubject(context.Background(), id)
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)

	got := UserIDFromRequest(r, "default")
	if got != "alice" {
		t.Errorf("got %q, want %q", got, "alice")
	}
}

func TestUserIDFromRequest_NoIdentity(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)

	got := UserIDFromRequest(r, "default_user")
	if got != "default_user" {
		t.Errorf("got %q, want %q", got, "default_user")
	}
}

func TestUserIDFromRequest_EmptyUserID(t *testing.T) {
	id := &Subject{userID: ""}
	ctx := ContextWithSubject(context.Background(), id)
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)

	got := UserIDFromRequest(r, "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}

func TestShouldSkipAuth_SkipPaths(t *testing.T) {
	for path := range SkipPaths {
		r := httptest.NewRequest("GET", path, nil)
		if !ShouldSkipAuth(r) {
			t.Errorf("ShouldSkipAuth(%q) = false, want true", path)
		}
	}
}

func TestShouldSkipAuth_OPTIONS(t *testing.T) {
	r := httptest.NewRequest("OPTIONS", "/api/v1/sessions", nil)
	if !ShouldSkipAuth(r) {
		t.Error("OPTIONS requests should always skip auth")
	}
}

func TestShouldSkipAuth_NormalPath(t *testing.T) {
	paths := []string{
		"/api/v1/sessions",
		"/api/v1/message:stream",
		"/api/v1/sse/subscribe/123",
	}
	for _, path := range paths {
		r := httptest.NewRequest("GET", path, nil)
		if ShouldSkipAuth(r) {
			t.Errorf("ShouldSkipAuth(%q) = true, want false", path)
		}
	}
}

func TestShouldSkipAuth_ExactMatchNotPrefix(t *testing.T) {
	subPaths := []string{
		"/api/v1/auth/login/foo",
		"/api/v1/auth/callback/foo",
		"/api/v1/auth/refresh/foo",
		"/api/v1/csrf-token/foo",
		"/api/v1/config/foo",
		"/api/v1/platform/health/foo",
	}
	for _, path := range subPaths {
		r := httptest.NewRequest("GET", path, nil)
		if ShouldSkipAuth(r) {
			t.Errorf("ShouldSkipAuth(%q) = true; sub-paths of SkipPaths entries must NOT skip auth", path)
		}
	}
}

func TestNoopProvider_Middleware(t *testing.T) {
	provider := NewNoopProvider("sam_dev_user", "")

	var captured *Subject
	handler := provider.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := SubjectFromContext(r.Context())
		if ok {
			captured = id
		}
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if captured == nil {
		t.Fatal("expected identity in context")
	}
	if captured.userID != "sam_dev_user" {
		t.Errorf("UserID = %q, want %q", captured.userID, "sam_dev_user")
	}
	if captured.email != "sam@dev.local" {
		t.Errorf("Email = %q, want %q", captured.email, "sam@dev.local")
	}
	if captured.name != "Sam Dev User" {
		t.Errorf("Name = %q, want %q", captured.name, "Sam Dev User")
	}
	if !captured.authenticated {
		t.Error("expected Authenticated = true")
	}
	if captured.authMethod != "development" {
		t.Errorf("AuthMethod = %q, want %q", captured.authMethod, "development")
	}
}

func TestNoopProvider_Middleware_ForceUser(t *testing.T) {
	provider := NewNoopProvider("default_user", "forced_user")

	var captured *Subject
	handler := provider.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := SubjectFromContext(r.Context())
		captured = id
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if captured == nil {
		t.Fatal("expected identity")
	}
	if captured.userID != "forced_user" {
		t.Errorf("UserID = %q, want %q", captured.userID, "forced_user")
	}
	if captured.email != "forced_user@dev.local" {
		t.Errorf("Email = %q, want %q", captured.email, "forced_user@dev.local")
	}
}

func TestNoopProvider_ProviderType(t *testing.T) {
	p := NewNoopProvider("user", "")
	if got := p.ProviderType(); got != "noop" {
		t.Errorf("ProviderType() = %q, want %q", got, "noop")
	}
}

func TestNoopProvider_CSRFToken(t *testing.T) {
	mux := http.NewServeMux()
	provider := NewNoopProvider("user", "")
	provider.RegisterRoutes(mux)

	r := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp["message"] != "CSRF token set" {
		t.Errorf("message = %v, want %q", resp["message"], "CSRF token set")
	}
	token, ok := resp["csrfToken"].(string)
	if !ok || token == "" {
		t.Error("expected non-empty csrfToken in response")
	}

	cookies := w.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "csrf_token" {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("expected csrf_token cookie")
	}
	if csrfCookie.Value != token {
		t.Errorf("cookie value = %q, want %q", csrfCookie.Value, token)
	}
	if csrfCookie.HttpOnly {
		t.Error("csrf_token cookie should not be HttpOnly")
	}
	if csrfCookie.MaxAge != 3600 {
		t.Errorf("MaxAge = %d, want 3600", csrfCookie.MaxAge)
	}
}

func TestNoopProvider_CSRFToken_SecureOnlyOverHTTPS(t *testing.T) {
	tests := []struct {
		name           string
		target         string
		forwardedProto string
		wantSecure     bool
	}{
		{name: "plain HTTP", target: "http://sam.example.test/api/v1/csrf-token", wantSecure: false},
		{name: "TLS terminated by the gateway", target: "https://sam.example.test/api/v1/csrf-token", wantSecure: true},
		{name: "TLS terminated by a load balancer", target: "http://sam.example.test/api/v1/csrf-token", forwardedProto: "https", wantSecure: true},
		{name: "load balancer forwarding plain HTTP", target: "http://sam.example.test/api/v1/csrf-token", forwardedProto: "http", wantSecure: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			NewNoopProvider("user", "").RegisterRoutes(mux)

			r := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if tt.forwardedProto != "" {
				r.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			var csrfCookie *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == "csrf_token" {
					csrfCookie = c
				}
			}
			if csrfCookie == nil {
				t.Fatal("expected csrf_token cookie")
			}
			if csrfCookie.Secure != tt.wantSecure {
				t.Errorf("Secure = %v, want %v", csrfCookie.Secure, tt.wantSecure)
			}
		})
	}
}

func TestNoopProvider_Logout(t *testing.T) {
	mux := http.NewServeMux()
	provider := NewNoopProvider("user", "")
	provider.RegisterRoutes(mux)

	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}
}

func TestCommunityAuthRoutes_RequireJSONOnStateChanging(t *testing.T) {
	mux := http.NewServeMux()
	RegisterAuthRoutes(mux)

	cases := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/auth/logout"},
		{"POST", "/api/v1/auth/refresh"},
	}
	for _, tt := range cases {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{"x":1}`))
			r.Header.Set("Content-Type", "text/plain")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d, want 415 (text/plain body must not reach handler)", w.Code)
			}
		})
	}

	for _, tt := range cases {
		t.Run("json "+tt.method+" "+tt.path, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code == http.StatusUnsupportedMediaType {
				t.Fatalf("application/json must reach the handler, got 415")
			}
		})
	}
}

func TestNoopProvider_EnterpriseStubs(t *testing.T) {
	mux := http.NewServeMux()
	provider := NewNoopProvider("user", "")
	provider.RegisterRoutes(mux)

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/auth/login"},
		{"GET", "/api/v1/auth/callback"},
		{"POST", "/api/v1/auth/refresh"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)

			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
			}

			body, _ := io.ReadAll(w.Body)
			var resp map[string]any
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatal(err)
			}
			msg, _ := resp["message"].(string)
			if msg == "" {
				t.Error("expected non-empty message")
			}
		})
	}
}

func TestRegistry_RegisterAndCreate(t *testing.T) {
	defer func() {
		registryMu.Lock()
		delete(factories, "test-provider")
		registryMu.Unlock()
	}()

	RegisterProviderFactory("test-provider", func(cfg map[string]any) (AuthProvider, error) {
		return NewNoopProvider("from-factory", ""), nil
	})

	provider, err := CreateProvider("test-provider", nil)
	if err != nil {
		t.Fatal(err)
	}
	if provider.ProviderType() != "noop" {
		t.Errorf("ProviderType() = %q, want %q", provider.ProviderType(), "noop")
	}
}

func TestRegistry_UnknownProvider(t *testing.T) {
	_, err := CreateProvider("nonexistent-provider", nil)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRegistry_RegisteredProviders(t *testing.T) {
	defer func() {
		registryMu.Lock()
		delete(factories, "alpha")
		delete(factories, "beta")
		registryMu.Unlock()
	}()

	RegisterProviderFactory("beta", func(cfg map[string]any) (AuthProvider, error) {
		return nil, nil
	})
	RegisterProviderFactory("alpha", func(cfg map[string]any) (AuthProvider, error) {
		return nil, nil
	})

	names := RegisteredProviders()
	if len(names) < 2 {
		t.Fatalf("expected at least 2 providers, got %d", len(names))
	}
	found := 0
	for i, name := range names {
		if name == "alpha" || name == "beta" {
			found++
			if i > 0 && names[i-1] > name {
				t.Error("providers should be sorted")
			}
		}
	}
	if found < 2 {
		t.Errorf("expected both alpha and beta in %v", names)
	}
}

func TestHasScope_EmptyScopes(t *testing.T) {
	id := &Subject{scopes: nil}
	if id.HasScope("anything") {
		t.Error("empty scopes must grant nothing — a de-roled RBAC user is denied")
	}
}

func TestHasScope_DevIdentityGrantsAll(t *testing.T) {
	if !DevIdentity().HasScope("anything:at:all") {
		t.Error("DevIdentity carries \"*\" and must satisfy any scope")
	}
}

func TestHasScope_WildcardGrant(t *testing.T) {
	id := &Subject{scopes: []string{"*"}}
	if !id.HasScope("tool:basic:read") {
		t.Error("wildcard should match any scope")
	}
}

func TestHasScope_ExactMatch(t *testing.T) {
	id := &Subject{scopes: []string{"tool:basic:read", "tool:basic:update"}}
	if !id.HasScope("tool:basic:read") {
		t.Error("exact match should work")
	}
	if id.HasScope("tool:admin:delete") {
		t.Error("non-matching scope should not pass")
	}
}

func TestHasScope_HierarchicalWildcard(t *testing.T) {
	id := &Subject{scopes: []string{"tool:basic:*"}}
	if !id.HasScope("tool:basic:read") {
		t.Error("trailing wildcard should match")
	}
	if id.HasScope("tool:admin:read") {
		t.Error("wildcard should not match different prefix")
	}
}

func TestHasScope_NoMatch(t *testing.T) {
	id := &Subject{scopes: []string{"tool:basic:read"}}
	if id.HasScope("tool:basic:update") {
		t.Error("different scope should not match")
	}
}

func TestIsSoftAuthPath_ShareView(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/share/ABCDEFGHIJKLMNOPQRSTu", nil)
	if !IsSoftAuthPath(r) {
		t.Error("expected soft auth for share view path")
	}
}

func TestIsSoftAuthPath_ShareArtifact(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/share/ABCDEFGHIJKLMNOPQRSTu/artifacts/file.txt", nil)
	if !IsSoftAuthPath(r) {
		t.Error("expected soft auth for share artifact path")
	}
}

func TestIsSoftAuthPath_NotGET(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/share/ABCDEFGHIJKLMNOPQRSTu", nil)
	if IsSoftAuthPath(r) {
		t.Error("POST should not be soft auth")
	}
}

func TestIsSoftAuthPath_SubRoutes(t *testing.T) {
	paths := []string{
		"/api/v1/share/",
		"/api/v1/share/sharedWithMe",
		"/api/v1/share/link/sess-1",
		"/api/v1/share/ABCDEFGHIJKLMNOPQRSTu/users",
		"/api/v1/share/ABCDEFGHIJKLMNOPQRSTu/fork",
		"/api/v1/share/short",
		"/api/v1/share/ABCDEFGHIJKLMNOPQRSTuXX",
	}
	for _, path := range paths {
		r := httptest.NewRequest("GET", path, nil)
		if IsSoftAuthPath(r) {
			t.Errorf("IsSoftAuthPath(%q) = true, want false", path)
		}
	}
}

func TestDevMiddleware_ScopesOverride(t *testing.T) {
	runDevWithHeader := func(t *testing.T, scopesHeader string, setHeader bool) *Subject {
		t.Helper()
		var got *Subject
		handler := DevMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if id, ok := SubjectFromContext(r.Context()); ok {
				got = id
			}
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil)
		if setHeader {
			req.Header.Set("X-SAM-Scopes", scopesHeader)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if got == nil {
			t.Fatalf("identity not set in request context")
		}
		return got
	}

	t.Run("no header keeps DevIdentity wildcard (allow-all)", func(t *testing.T) {
		id := runDevWithHeader(t, "", false)
		if len(id.scopes) != 1 || id.scopes[0] != "*" {
			t.Fatalf("Scopes: got %v, want [*] (DevIdentity wildcard)", id.scopes)
		}
		if id.userID != "sam_dev_user" {
			t.Errorf("UserID: got %q, want sam_dev_user", id.userID)
		}
	})

	t.Run("header injects scopes and HasScope enforces them", func(t *testing.T) {
		id := runDevWithHeader(t, "workflow_builder:*:read", true)
		if len(id.scopes) != 1 || id.scopes[0] != "workflow_builder:*:read" {
			t.Fatalf("Scopes: got %v, want [workflow_builder:*:read]", id.scopes)
		}
		if !id.HasScope("workflow_builder:my-flow:read") {
			t.Error("wildcard instance grant should satisfy per-instance check")
		}
		if id.HasScope("workflow_builder:_:create") {
			t.Error("scope outside the injected set must be denied")
		}
	})
}

var (
	_ AuthProvider = (*NoopProvider)(nil)
)
