// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type fakeConfig struct {
	toolsetID  string
	lookupKey  string
	configured bool
	err        error
	calls      atomic.Int32
}

func (f *fakeConfig) Get(_ context.Context) (string, string, bool, error) {
	f.calls.Add(1)
	return f.toolsetID, f.lookupKey, f.configured, f.err
}

type fakeScopes struct {
	scopes []string
	err    error
	calls  atomic.Int32
}

func (f *fakeScopes) Resolve(_ context.Context, _ map[string]any) ([]string, error) {
	f.calls.Add(1)
	return f.scopes, f.err
}

type fakeEnricher struct {
	profile        *Profile
	enrichErr      error
	enrichCalls    atomic.Int32
	searchProfiles []*Profile
	searchErr      error
	searchCalls    atomic.Int32
}

func (f *fakeEnricher) EnrichAuthenticatedUser(_ context.Context, _ string) (*Profile, error) {
	f.enrichCalls.Add(1)
	return f.profile, f.enrichErr
}
func (f *fakeEnricher) SearchUsers(_ context.Context, _ string, _ int) ([]*Profile, error) {
	f.searchCalls.Add(1)
	return f.searchProfiles, f.searchErr
}
func (f *fakeEnricher) GetUserProfile(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}
func (f *fakeEnricher) GetTimeOff(_ context.Context, _, _, _ string) ([]TimeOffEntry, error) {
	return nil, nil
}
func (f *fakeEnricher) GetUserProfilePicture(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}

func TestResolve_HappyPath(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{toolsetID: "tset", lookupKey: "email", configured: true}
	scopes := &fakeScopes{scopes: []string{"agent_builder:*:read"}}
	wantProfile := &Profile{ID: "alice@example.com", DisplayName: "Alice"}
	enr := &fakeEnricher{profile: wantProfile}

	svc := New(cfg, scopes, enr, nil)

	res, err := svc.Resolve(context.Background(), map[string]any{
		"sub":   "alice-sub",
		"email": "Alice@Example.com",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.EnrichmentStatus != EnrichmentOK {
		t.Fatalf("status = %q, want ok", res.EnrichmentStatus)
	}
	if res.Profile == nil || res.Profile.ID != "alice@example.com" {
		t.Errorf("profile = %+v, want alice@example.com", res.Profile)
	}
	if len(res.Scopes) != 1 || res.Scopes[0] != "agent_builder:*:read" {
		t.Errorf("scopes = %v, want [agent_builder:*:read]", res.Scopes)
	}
}

func TestResolve_NotConfigured(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{configured: false}
	svc := New(cfg, &fakeScopes{}, &fakeEnricher{}, nil)

	res, err := svc.Resolve(context.Background(), map[string]any{"sub": "x", "email": "x@e"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.EnrichmentStatus != EnrichmentNotConfigured {
		t.Errorf("status = %q, want not_configured", res.EnrichmentStatus)
	}
	if res.Profile != nil {
		t.Errorf("expected nil profile when unconfigured, got %+v", res.Profile)
	}
}

func TestResolve_MissingLookupClaim(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{toolsetID: "tset", lookupKey: "email", configured: true}
	svc := New(cfg, &fakeScopes{}, &fakeEnricher{profile: &Profile{ID: "x"}}, nil)

	res, err := svc.Resolve(context.Background(), map[string]any{"sub": "x"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.EnrichmentStatus != EnrichmentUnavailable {
		t.Errorf("status = %q, want unavailable", res.EnrichmentStatus)
	}
	if res.EnrichmentReason == "" {
		t.Error("expected non-empty EnrichmentReason when claim is missing")
	}
}

func TestResolve_EnricherError(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{toolsetID: "tset", lookupKey: "email", configured: true}
	enr := &fakeEnricher{enrichErr: errors.New("transport blew up")}
	svc := New(cfg, &fakeScopes{}, enr, nil)

	res, err := svc.Resolve(context.Background(), map[string]any{"email": "a@b"})
	if err != nil {
		t.Fatalf("Resolve should not error on enricher failure: %v", err)
	}
	if res.EnrichmentStatus != EnrichmentUnavailable {
		t.Errorf("status = %q, want unavailable", res.EnrichmentStatus)
	}
	if res.Profile != nil {
		t.Errorf("expected nil profile when enricher errs, got %+v", res.Profile)
	}
}

func TestResolve_ScopeResolverErrorPropagates(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{toolsetID: "tset", lookupKey: "email", configured: true}
	scopes := &fakeScopes{err: errors.New("rbac down")}
	svc := New(cfg, scopes, &fakeEnricher{profile: &Profile{ID: "x"}}, nil)

	_, err := svc.Resolve(context.Background(), map[string]any{"sub": "s", "email": "a@b"})
	if err == nil {
		t.Fatal("expected error when scope resolution fails (gateway must surface 503)")
	}
}

func TestResolve_CachesScopesPerUser(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{toolsetID: "tset", lookupKey: "email", configured: true}
	scopes := &fakeScopes{scopes: []string{"a", "b"}}
	svc := New(cfg, scopes, &fakeEnricher{profile: &Profile{ID: "x"}}, nil)

	claims := map[string]any{"sub": "alice", "email": "a@b"}
	for range 3 {
		if _, err := svc.Resolve(context.Background(), claims); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if got := scopes.calls.Load(); got != 1 {
		t.Errorf("scope resolver calls = %d, want 1 (scope cache broken)", got)
	}
}

func TestResolve_CachesProfileByLookupValue(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{toolsetID: "tset", lookupKey: "email", configured: true}
	enr := &fakeEnricher{profile: &Profile{ID: "alice@example.com"}}
	svc := New(cfg, &fakeScopes{}, enr, nil)

	calls := []map[string]any{
		{"sub": "a", "email": "alice@example.com"},
		{"sub": "b", "email": "Alice@Example.com"},
		{"sub": "c", "email": "ALICE@EXAMPLE.COM"},
	}
	for _, claims := range calls {
		if _, err := svc.Resolve(context.Background(), claims); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if got := enr.enrichCalls.Load(); got != 1 {
		t.Errorf("enricher calls = %d, want 1 (profile cache broken)", got)
	}
}

func TestSearchUsers_CachedByQueryAndLimit(t *testing.T) {
	t.Parallel()
	enr := &fakeEnricher{searchProfiles: []*Profile{{ID: "u"}}}
	svc := New(&fakeConfig{}, &fakeScopes{}, enr, nil)

	for range 3 {
		if _, err := svc.SearchUsers(context.Background(), "alice", 10); err != nil {
			t.Fatalf("search: %v", err)
		}
	}
	if got := enr.searchCalls.Load(); got != 1 {
		t.Errorf("search calls = %d, want 1", got)
	}
}

func TestSearchUsers_NilEnricherReturnsEmpty(t *testing.T) {
	t.Parallel()
	svc := New(&fakeConfig{}, nil, nil, nil)
	got, err := svc.SearchUsers(context.Background(), "alice", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice with nil enricher, got %v", got)
	}
}

func TestExtractLookupValue_LowercaseAndTrim(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		claims    map[string]any
		lookupKey string
		want      string
	}{
		{"happy", map[string]any{"email": "  Alice@Example.com "}, "email", "alice@example.com"},
		{"missing", map[string]any{"email": ""}, "email", ""},
		{"non-string", map[string]any{"email": 42}, "email", ""},
		{"absent key", map[string]any{"sub": "x"}, "email", ""},
		{"empty key", map[string]any{"email": "x"}, "", ""},
		{"nil claims", nil, "email", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractLookupValue(tc.claims, tc.lookupKey)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolve_NilScopeResolverReturnsEmptyScopes(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{toolsetID: "tset", lookupKey: "email", configured: true}
	svc := New(cfg, nil, &fakeEnricher{profile: &Profile{ID: "x"}}, nil)

	res, err := svc.Resolve(context.Background(), map[string]any{"email": "a@b"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Scopes) != 0 {
		t.Errorf("scopes = %v, want empty (nil resolver should yield empty without erroring)", res.Scopes)
	}
}

type fakeToolEnricher struct {
	*fakeEnricher
	hasTool  bool
	err      error
	lastTool string
}

func (f *fakeToolEnricher) HasTool(_ context.Context, _, bareName string) (bool, error) {
	f.lastTool = bareName
	return f.hasTool, f.err
}

func TestService_SearchConfigured_Unconfigured(t *testing.T) {
	t.Parallel()
	svc := New(&fakeConfig{configured: false}, nil, &fakeToolEnricher{fakeEnricher: &fakeEnricher{}, hasTool: true}, nil)

	ok, err := svc.SearchConfigured(context.Background())
	if err != nil || ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestService_SearchConfigured_NilEnricher(t *testing.T) {
	t.Parallel()
	svc := New(&fakeConfig{configured: true, toolsetID: "ts1"}, nil, nil, nil)

	ok, err := svc.SearchConfigured(context.Background())
	if err != nil || ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestService_SearchConfigured_ConfigError(t *testing.T) {
	t.Parallel()
	svc := New(&fakeConfig{err: errors.New("boom")}, nil, &fakeToolEnricher{fakeEnricher: &fakeEnricher{}, hasTool: true}, nil)

	ok, err := svc.SearchConfigured(context.Background())
	if err == nil || ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (false, error)", ok, err)
	}
}

func TestService_SearchConfigured_ToolPresent(t *testing.T) {
	t.Parallel()
	enr := &fakeToolEnricher{fakeEnricher: &fakeEnricher{}, hasTool: true}
	svc := New(&fakeConfig{configured: true, toolsetID: "ts1"}, nil, enr, nil)

	ok, err := svc.SearchConfigured(context.Background())
	if err != nil || !ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (true, nil)", ok, err)
	}
	if enr.lastTool != "search_users" {
		t.Errorf("checked tool %q, want search_users", enr.lastTool)
	}
}

func TestService_SearchConfigured_ToolMissing(t *testing.T) {
	t.Parallel()
	enr := &fakeToolEnricher{fakeEnricher: &fakeEnricher{}, hasTool: false}
	svc := New(&fakeConfig{configured: true, toolsetID: "ts1"}, nil, enr, nil)

	ok, err := svc.SearchConfigured(context.Background())
	if err != nil || ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestService_SearchConfigured_EnricherWithoutPresenceCheck(t *testing.T) {
	t.Parallel()
	svc := New(&fakeConfig{configured: true, toolsetID: "ts1"}, nil, &fakeEnricher{}, nil)

	ok, err := svc.SearchConfigured(context.Background())
	if err != nil || !ok {
		t.Fatalf("SearchConfigured = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestService_SearchConfigured_CachesResult(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{configured: true, toolsetID: "ts1"}
	svc := New(cfg, nil, &fakeToolEnricher{fakeEnricher: &fakeEnricher{}, hasTool: true}, nil)

	for range 3 {
		if ok, err := svc.SearchConfigured(context.Background()); err != nil || !ok {
			t.Fatalf("SearchConfigured = (%v, %v), want (true, nil)", ok, err)
		}
	}
	if got := cfg.calls.Load(); got != 1 {
		t.Errorf("config read %d times, want 1", got)
	}
}

func TestService_SearchConfigured_DoesNotCacheErrors(t *testing.T) {
	t.Parallel()
	cfg := &fakeConfig{configured: true, toolsetID: "ts1", err: errors.New("db down")}
	svc := New(cfg, nil, &fakeToolEnricher{fakeEnricher: &fakeEnricher{}, hasTool: true}, nil)

	if _, err := svc.SearchConfigured(context.Background()); err == nil {
		t.Fatal("first call: want error")
	}
	cfg.err = nil
	ok, err := svc.SearchConfigured(context.Background())
	if err != nil || !ok {
		t.Fatalf("after recovery: SearchConfigured = (%v, %v), want (true, nil)", ok, err)
	}
}
