// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const middlewareKeyPlaintext = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

type middlewareKeyAuthority struct {
	resolution apikey.Resolution
	err        error
	calls      int
	gotHash    string
}

func (m *middlewareKeyAuthority) Validate(_ context.Context, keyHash string) (apikey.Resolution, error) {
	m.calls++
	m.gotHash = keyHash
	return m.resolution, m.err
}

type countingAuthenticator struct {
	id    *samauth.Identity
	calls int
}

func (c *countingAuthenticator) Authenticate(context.Context, string, string) (*samauth.Identity, error) {
	c.calls++
	return c.id, nil
}

func resolvedKeyAuthority() *middlewareKeyAuthority {
	return &middlewareKeyAuthority{resolution: apikey.Resolution{
		KeyID:    "key-1",
		OwnerID:  "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77",
		OwnerSub: "system:ci-deployer",
		Scopes:   []string{"rbac:_:read"},
	}}
}

func keyRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+middlewareKeyPlaintext)
	return req
}

func TestPlatformAuthMiddleware_ValidAPIKeyAuthenticatesAsItsOwner(t *testing.T) {
	t.Parallel()
	authenticator := &countingAuthenticator{}
	mw := NewAuthMiddleware(authenticator, nil, resolvedKeyAuthority(), slog.Default())

	var got *auth.Subject
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	resp := httptest.NewRecorder()
	mw(next).ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if got == nil {
		t.Fatal("no identity reached the handler")
	}
	if got.AuthMethod() != authMethodAPIKey {
		t.Errorf("AuthMethod = %q, want %q", got.AuthMethod(), authMethodAPIKey)
	}
	if got.UserID() != "system:ci-deployer" {
		t.Errorf("UserID = %q, want the owner's subject", got.UserID())
	}
	if !slices.Equal(got.Scopes(), []string{"rbac:_:read"}) {
		t.Errorf("Scopes = %v, want the surviving set", got.Scopes())
	}
	if authenticator.calls != 0 {
		t.Errorf("the JWT authenticator ran %d times for a samk_ credential", authenticator.calls)
	}
}

func TestPlatformAuthMiddleware_StoredKeyAuthenticatesThroughTheBoundHolder(t *testing.T) {
	ctx := t.Context()
	f := newAuthorityFixture(t, []string{"agent_builder:_:create", "agent:*:invoke"})
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	key := f.seedKey(ctx, t, owner.ID, apikey.Hash(middlewareKeyPlaintext),
		[]string{"agent:*:invoke", "rbac:_:read"}, nil)

	holder := apikey.NewLazyAuthority()
	holder.SetAuthority(f.authority)
	authenticator := &countingAuthenticator{}
	mw := NewAuthMiddleware(authenticator, nil, holder, slog.Default())

	var got *auth.Subject
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	resp := httptest.NewRecorder()
	mw(next).ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
	if got == nil {
		t.Fatal("no identity reached the handler")
	}
	if got.UserID() != "system:ci-deployer" || got.AuthMethod() != authMethodAPIKey {
		t.Errorf("identity = %q/%q, want system:ci-deployer/%q", got.UserID(), got.AuthMethod(), authMethodAPIKey)
	}
	if !slices.Equal(got.Scopes(), []string{"agent:*:invoke"}) {
		t.Errorf("Scopes = %v, want [agent:*:invoke]: rbac:_:read is not held by the owner", got.Scopes())
	}
	if authenticator.calls != 0 {
		t.Errorf("the JWT authenticator ran %d times for a samk_ credential", authenticator.calls)
	}

	if err := f.keys.Delete(ctx, key.ID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	resp = httptest.NewRecorder()
	mw(next).ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))
	if resp.Code != http.StatusUnauthorized {
		t.Errorf("status after delete = %d, want 401", resp.Code)
	}
}

func TestPlatformAuthMiddleware_RejectedAPIKeyNeverReachesTheAuthenticator(t *testing.T) {
	t.Parallel()
	authenticator := &countingAuthenticator{
		id: &samauth.Identity{UserID: "someone-else", Scopes: []string{"*:*:*"}},
	}
	mw := NewAuthMiddleware(authenticator, nil,
		&middlewareKeyAuthority{err: apikey.ErrInvalidKey}, slog.Default())

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler ran for a rejected API key")
	})
	resp := httptest.NewRecorder()
	mw(next).ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.Code)
	}
	if authenticator.calls != 0 {
		t.Errorf("the JWT authenticator ran %d times; a failed key must not fall through", authenticator.calls)
	}
}

func TestPlatformAuthMiddleware_APIKeyRefusalStatuses(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		authority  apikey.Authority
		wantStatus int
	}{
		"invalid":                     {&middlewareKeyAuthority{err: apikey.ErrInvalidKey}, http.StatusUnauthorized},
		"authorization mode disabled": {&middlewareKeyAuthority{err: apikey.ErrAuthorizationModeDisabled}, http.StatusUnauthorized},
		"authority unavailable":       {&middlewareKeyAuthority{err: apikey.ErrAuthorityUnavailable}, http.StatusServiceUnavailable},
		"keys not served here":        {nil, http.StatusUnauthorized},
		"holder never resolved":       {apikey.NewLazyAuthority(), http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mw := NewAuthMiddleware(&countingAuthenticator{}, nil, tc.authority, slog.Default())
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("handler ran for a rejected API key")
			})
			resp := httptest.NewRecorder()
			mw(next).ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

			if resp.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body=%s", resp.Code, tc.wantStatus, resp.Body.String())
			}
		})
	}
}

func TestPlatformAuthMiddleware_UnavailableAuthorityIsNotReportedAsABadKey(t *testing.T) {
	t.Parallel()
	mw := NewAuthMiddleware(&countingAuthenticator{}, nil,
		&middlewareKeyAuthority{err: apikey.ErrAuthorityUnavailable}, slog.Default())

	resp := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.Code)
	}
	if strings.Contains(strings.ToLower(resp.Body.String()), "expired") {
		t.Errorf("a 503 body blames the credential: %s", resp.Body.String())
	}
}

func TestPlatformAuthMiddleware_ResponseEchoesNeitherPlaintextNorHash(t *testing.T) {
	t.Parallel()
	mw := NewAuthMiddleware(&countingAuthenticator{}, nil,
		&middlewareKeyAuthority{err: apikey.ErrInvalidKey}, slog.Default())

	resp := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

	body := resp.Body.String()
	for label, needle := range map[string]string{
		"plaintext": middlewareKeyPlaintext,
		"hash":      apikey.Hash(middlewareKeyPlaintext),
		"marker":    apikey.Marker,
	} {
		if strings.Contains(body, needle) {
			t.Errorf("response echoes the credential %s: %s", label, body)
		}
	}
}

func TestPlatformAuthMiddleware_AuthorityErrorTextNeverEscapes(t *testing.T) {
	t.Parallel()
	leaky := fmt.Errorf("rejecting key %s (hash %s): %w",
		middlewareKeyPlaintext, apikey.Hash(middlewareKeyPlaintext), apikey.ErrInvalidKey)

	identity, rejection := auth.ResolveAPIKey(
		t.Context(), &middlewareKeyAuthority{err: leaky}, middlewareKeyPlaintext)
	if identity != nil {
		t.Fatal("a failing authority produced an identity")
	}
	for label, needle := range map[string]string{
		"plaintext": middlewareKeyPlaintext,
		"hash":      apikey.Hash(middlewareKeyPlaintext),
		"marker":    apikey.Marker,
	} {
		if strings.Contains(rejection.Reason, needle) {
			t.Errorf("the logged reason carries the credential %s: %q", label, rejection.Reason)
		}
		if strings.Contains(rejection.Message, needle) {
			t.Errorf("the client-facing message carries the credential %s: %q", label, rejection.Message)
		}
	}
	if rejection.Reason != auth.APIKeyReasonInvalid {
		t.Errorf("reason = %q, want the bounded constant %q", rejection.Reason, auth.APIKeyReasonInvalid)
	}

	mw := NewAuthMiddleware(&countingAuthenticator{}, nil,
		&middlewareKeyAuthority{err: leaky}, slog.Default())
	resp := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

	if strings.Contains(resp.Body.String(), middlewareKeyPlaintext) {
		t.Errorf("the authority's error text reached the response body: %s", resp.Body.String())
	}
}

func TestPlatformAuthMiddleware_HandsTheAuthorityOnlyTheHash(t *testing.T) {
	t.Parallel()
	authority := resolvedKeyAuthority()
	mw := NewAuthMiddleware(&countingAuthenticator{}, nil, authority, slog.Default())

	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(httptest.NewRecorder(), keyRequest("/api/v1/platform/agents"))

	if authority.gotHash != apikey.Hash(middlewareKeyPlaintext) {
		t.Errorf("authority got %q, want the digest of the complete plaintext", authority.gotHash)
	}
}

func TestPlatformAuthMiddleware_NonKeyCredentialSkipsTheKeyAuthority(t *testing.T) {
	t.Parallel()
	authority := resolvedKeyAuthority()
	mw := NewAuthMiddleware(
		&countingAuthenticator{id: &samauth.Identity{UserID: "sub-1", Scopes: []string{"rbac:_:read"}}},
		nil, authority, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil)
	req.Header.Set("Authorization", "Bearer an-ordinary-jwt")
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(httptest.NewRecorder(), req)

	if authority.calls != 0 {
		t.Errorf("key authority consulted %d times for a non-key credential", authority.calls)
	}
}

func TestPlatformAuthMiddleware_MalformedKeyNeverReachesTheAuthority(t *testing.T) {
	t.Parallel()
	authority := &middlewareKeyAuthority{}
	mw := NewAuthMiddleware(&countingAuthenticator{}, nil, authority, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil)
	req.Header.Set("Authorization", "Bearer "+apikey.Marker+"too-short")
	resp := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler ran for a malformed key")
	})).ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.Code)
	}
	if authority.calls != 0 {
		t.Errorf("the authority was asked %d times about a string that cannot be a key", authority.calls)
	}
}

func TestPlatformAuthMiddleware_EveryRefusalReadsTheSameOnTheWire(t *testing.T) {
	t.Parallel()
	disabled := apikey.NewLazyAuthority()
	disabled.Disable()

	cases := map[string]struct {
		authority apikey.Authority
		key       string
	}{
		"unknown key":                 {&middlewareKeyAuthority{err: apikey.ErrInvalidKey}, middlewareKeyPlaintext},
		"authorization mode disabled": {&middlewareKeyAuthority{err: apikey.ErrAuthorizationModeDisabled}, middlewareKeyPlaintext},
		"keys not enabled":            {disabled, middlewareKeyPlaintext},
		"keys not served here":        {nil, middlewareKeyPlaintext},
		"malformed key":               {&middlewareKeyAuthority{}, apikey.Marker + "x"},
	}
	bodies := make(map[string]string, len(cases))
	for name, tc := range cases {
		mw := NewAuthMiddleware(&countingAuthenticator{}, nil, tc.authority, slog.Default())
		req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil)
		req.Header.Set("Authorization", "Bearer "+tc.key)
		resp := httptest.NewRecorder()
		mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(resp, req)

		if resp.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, resp.Code)
		}
		bodies[name] = bodyWithoutErrorID(t, resp.Body.Bytes())
	}
	want := bodies["unknown key"]
	for name, got := range bodies {
		if got != want {
			t.Errorf("%s reads differently from an unknown key:\n got %s\nwant %s", name, got, want)
		}
	}
}

func bodyWithoutErrorID(t *testing.T, body []byte) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, body)
	}
	delete(decoded, "errorId")
	masked, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return string(masked)
}

func TestPlatformAuthMiddleware_CallerWhoHasGoneGetsNoResponse(t *testing.T) {
	t.Parallel()
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	authority := &middlewareKeyAuthority{
		err: fmt.Errorf("%w: %w", apikey.ErrAuthorityUnavailable, context.Canceled),
	}
	mw := NewAuthMiddleware(&countingAuthenticator{}, nil, authority, slog.Default())

	resp := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler ran")
	})).ServeHTTP(resp, keyRequest("/api/v1/platform/agents").WithContext(gone))

	if resp.Body.Len() != 0 {
		t.Errorf("a caller who has gone was written to: %s", resp.Body.String())
	}
}

func TestPlatformAuthMiddleware_WhitespaceBeforeTheMarkerStillMakesItAKey(t *testing.T) {
	t.Parallel()
	for name, header := range map[string]string{
		"two spaces":     "Bearer  " + middlewareKeyPlaintext,
		"a tab":          "Bearer \t" + middlewareKeyPlaintext,
		"spaces and tab": "Bearer  \t " + middlewareKeyPlaintext,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			authenticator := &countingAuthenticator{}
			authority := resolvedKeyAuthority()
			mw := NewAuthMiddleware(authenticator, nil, authority, slog.Default())

			req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil)
			req.Header.Set("Authorization", header)
			resp := httptest.NewRecorder()
			mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(resp, req)

			if authenticator.calls != 0 {
				t.Errorf("the JWT authenticator saw a key behind whitespace %d times", authenticator.calls)
			}
			if authority.calls != 1 {
				t.Errorf("the key authority was asked %d times, want 1: the value was not routed as a key", authority.calls)
			}
			if resp.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}
