// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package target

import (
	"context"
	"errors"
	"testing"
)

func TestResolve_NoAuth(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	tg := Target{URL: "http://localhost:8080/"}
	r, err := tg.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.URL != "http://localhost:8080" {
		t.Errorf("URL = %q; want trailing slash trimmed", r.URL)
	}
	if r.Token != "" {
		t.Errorf("Token = %q; want empty", r.Token)
	}
}

func TestResolve_BearerToken_EnvSet(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "tok-xyz")
	tg := Target{URL: "http://x", Auth: AuthSpec{Type: AuthBearerToken}}
	r, err := tg.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Token != "tok-xyz" {
		t.Errorf("Token = %q; want tok-xyz", r.Token)
	}
}

func TestResolve_BearerToken_CustomEnv(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	t.Setenv("MY_TOKEN", "abc")
	tg := Target{URL: "http://x", Auth: AuthSpec{Type: AuthBearerToken, EnvVar: "MY_TOKEN"}}
	r, err := tg.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Token != "abc" {
		t.Errorf("Token = %q; want abc", r.Token)
	}
}

func TestResolve_BearerToken_EnvUnset(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	tg := Target{URL: "http://x", Auth: AuthSpec{Type: AuthBearerToken}}
	if _, err := tg.Resolve(context.Background(), nil); err == nil {
		t.Fatal("expected error when env var is unset")
	}
}

func TestResolve_EmptyURL(t *testing.T) {
	if _, err := (Target{}).Resolve(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestResolve_UnsupportedAuth(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	tg := Target{URL: "http://x", Auth: AuthSpec{Type: "oauth_pkce"}}
	if _, err := tg.Resolve(context.Background(), nil); err == nil {
		t.Fatal("expected error for unsupported auth type")
	}
}

type fakeOAuthResolver struct {
	token string
	err   error
}

func (f fakeOAuthResolver) ResolveSAMToken(_ context.Context, _ Target) (string, error) {
	return f.token, f.err
}

func TestResolve_OAuth_ResolverReturnsToken(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	tg := Target{Name: "dev", URL: "http://x", Auth: AuthSpec{Type: AuthOAuth}}
	r, err := tg.Resolve(context.Background(), fakeOAuthResolver{token: "sam-jwt"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Token != "sam-jwt" {
		t.Errorf("Token = %q; want sam-jwt", r.Token)
	}
}

func TestResolve_OAuth_NoResolver(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	tg := Target{Name: "dev", URL: "http://x", Auth: AuthSpec{Type: AuthOAuth}}
	if _, err := tg.Resolve(context.Background(), nil); err == nil {
		t.Fatal("expected error when oauth resolver is nil")
	}
}

func TestResolve_OAuth_ResolverPropagatesError(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "")
	tg := Target{Name: "dev", URL: "http://x", Auth: AuthSpec{Type: AuthOAuth}}
	_, err := tg.Resolve(context.Background(), fakeOAuthResolver{err: ErrNotLoggedIn})
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v; want ErrNotLoggedIn", err)
	}
}

func TestResolve_EnvVarOverridesOAuth(t *testing.T) {
	t.Setenv("SAM_PLATFORM_TOKEN", "ci-token")
	tg := Target{Name: "dev", URL: "http://x", Auth: AuthSpec{Type: AuthOAuth}}
	r, err := tg.Resolve(context.Background(), fakeOAuthResolver{err: errors.New("must not be called")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Token != "ci-token" {
		t.Errorf("Token = %q; want ci-token", r.Token)
	}
}

func TestCacheNameForURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://platform.dev.example.com", "platform.dev.example.com"},
		{"https://Platform.Dev.example.com:8443/foo", "platform.dev.example.com"},
		{"http://localhost:8080", "localhost"},
		{"", "default"},
	}
	for _, c := range cases {
		if got := CacheNameForURL(c.in); got != c.want {
			t.Errorf("CacheNameForURL(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestCacheName_PrefersExplicitName(t *testing.T) {
	tg := Target{Name: "DevCluster_01", URL: "https://x.example.com"}
	if got := tg.CacheName(); got != "devcluster_01" {
		t.Errorf("CacheName = %q; want devcluster_01", got)
	}
}
