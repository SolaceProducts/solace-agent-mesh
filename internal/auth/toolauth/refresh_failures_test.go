// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type tokenEndpoint struct {
	srv      *httptest.Server
	requests atomic.Int64
	status   atomic.Int64
	body     atomic.Value
}

func newTokenEndpoint(t *testing.T, status int, body string) *tokenEndpoint {
	t.Helper()
	e := &tokenEndpoint{}
	e.answer(status, body)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		e.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(e.status.Load()))
		_, _ = w.Write([]byte(e.body.Load().(string)))
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *tokenEndpoint) answer(status int, body string) {
	e.status.Store(int64(status))
	e.body.Store(body)
}

func (e *tokenEndpoint) answerWithFreshToken() {
	e.answer(http.StatusOK, `{"access_token":"fresh-at","refresh_token":"fresh-rt","token_type":"Bearer","expires_in":3600}`)
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newOutageHandler(endpoint *tokenEndpoint, clock *fakeClock) *AuthHandler {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: endpoint.srv.URL + "/token"},
	}, slog.Default())
	h.now = func() time.Time { return clock.now }
	return h
}

func storeExpiredToken(t *testing.T, credSvc *mockCredentialService) {
	t.Helper()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "valid-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	})
}

func storedCredential(t *testing.T, credSvc *mockCredentialService) string {
	t.Helper()
	stored, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")
	return stored
}

func TestGetOAuthToken_ProviderOutage_DefersTheNextRefresh(t *testing.T) {
	endpoint := newTokenEndpoint(t, http.StatusServiceUnavailable, `{"error":"server_error"}`)
	clock := &fakeClock{now: time.Now()}
	h := newOutageHandler(endpoint, clock)
	credSvc := newMockCredentialService()
	storeExpiredToken(t, credSvc)
	toolCtx := testToolCtx(credSvc)

	for range 3 {
		_, err := h.GetOAuthToken(t.Context(), toolCtx)
		if !errors.Is(err, ErrTransientRefresh) || errors.Is(err, ErrAuthRequired) {
			t.Fatalf("GetOAuthToken: got %v, want ErrTransientRefresh without ErrAuthRequired", err)
		}
	}
	if got := endpoint.requests.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want 1: retries within the backoff must not reach the token endpoint", got)
	}

	clock.advance(refreshRetryBackoff)
	endpoint.answerWithFreshToken()
	token, err := h.GetOAuthToken(t.Context(), toolCtx)
	if err != nil {
		t.Fatalf("GetOAuthToken after the backoff: %v", err)
	}
	if token.AccessToken != "fresh-at" {
		t.Errorf("AccessToken = %q, want the refreshed token", token.AccessToken)
	}
	if got := endpoint.requests.Load(); got != 2 {
		t.Errorf("refresh requests = %d, want 2", got)
	}
}

func TestGetOAuthToken_ProviderOutageThatNeverRecovers_AsksForSignInAndKeepsCredential(t *testing.T) {
	endpoint := newTokenEndpoint(t, http.StatusServiceUnavailable, `{"error":"server_error"}`)
	clock := &fakeClock{now: time.Now()}
	h := newOutageHandler(endpoint, clock)
	credSvc := newMockCredentialService()
	storeExpiredToken(t, credSvc)
	toolCtx := testToolCtx(credSvc)

	if _, err := h.GetOAuthToken(t.Context(), toolCtx); !errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("first GetOAuthToken: got %v, want ErrTransientRefresh", err)
	}
	clock.advance(refreshEscalateAfter)

	_, err := h.GetOAuthToken(t.Context(), toolCtx)
	if !errors.Is(err, ErrAuthRequired) || errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("GetOAuthToken after %s of failures: got %v, want ErrAuthRequired", refreshEscalateAfter, err)
	}
	if got := endpoint.requests.Load(); got != 2 {
		t.Errorf("refresh requests = %d, want 2: the escalation must still try the endpoint once more", got)
	}

	if err := h.ClearCredentialForReauth(t.Context(), toolCtx, ""); err != nil {
		t.Fatalf("ClearCredentialForReauth: %v", err)
	}
	if storedCredential(t, credSvc) == "" {
		t.Fatal("the sign-in prompt after an outage must keep the credential the provider never rejected")
	}
}

func TestGetOAuthToken_RefreshThatSucceeds_StartsTheOutageCountAgain(t *testing.T) {
	endpoint := newTokenEndpoint(t, http.StatusServiceUnavailable, `{"error":"server_error"}`)
	clock := &fakeClock{now: time.Now()}
	h := newOutageHandler(endpoint, clock)
	credSvc := newMockCredentialService()
	storeExpiredToken(t, credSvc)
	toolCtx := testToolCtx(credSvc)

	if _, err := h.GetOAuthToken(t.Context(), toolCtx); !errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("GetOAuthToken during the outage: got %v, want ErrTransientRefresh", err)
	}
	clock.advance(refreshRetryBackoff)
	endpoint.answerWithFreshToken()
	if _, err := h.GetOAuthToken(t.Context(), toolCtx); err != nil {
		t.Fatalf("GetOAuthToken after recovery: %v", err)
	}

	clock.advance(refreshEscalateAfter)
	storeExpiredToken(t, credSvc)
	endpoint.answer(http.StatusServiceUnavailable, `{"error":"server_error"}`)
	_, err := h.GetOAuthToken(t.Context(), toolCtx)
	if !errors.Is(err, ErrTransientRefresh) || errors.Is(err, ErrAuthRequired) {
		t.Fatalf("GetOAuthToken in a new outage: got %v, want ErrTransientRefresh, not an escalation carried over from the last one", err)
	}
}

func TestGetOAuthToken_RejectedRefreshAfterOutage_ClearsCredential(t *testing.T) {
	endpoint := newTokenEndpoint(t, http.StatusServiceUnavailable, `{"error":"server_error"}`)
	clock := &fakeClock{now: time.Now()}
	h := newOutageHandler(endpoint, clock)
	credSvc := newMockCredentialService()
	storeExpiredToken(t, credSvc)
	toolCtx := testToolCtx(credSvc)

	if _, err := h.GetOAuthToken(t.Context(), toolCtx); !errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("GetOAuthToken during the outage: got %v, want ErrTransientRefresh", err)
	}
	clock.advance(refreshEscalateAfter)
	endpoint.answer(http.StatusBadRequest, `{"error":"invalid_grant"}`)

	if _, err := h.GetOAuthToken(t.Context(), toolCtx); !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("GetOAuthToken: got %v, want ErrAuthRequired", err)
	}
	if stored := storedCredential(t, credSvc); stored != "" {
		t.Errorf("a refresh token the provider rejected must be cleared, got %q", stored)
	}
}

func TestSignInProviderUnavailableResult_ReportsARateLimitAsRateLimited(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "503 server_error", status: http.StatusServiceUnavailable, body: `{"error":"server_error"}`, want: tool.HTTPFailureCode(http.StatusServiceUnavailable)},
		{name: "429", status: http.StatusTooManyRequests, body: `{}`, want: tool.HTTPFailureCode(http.StatusTooManyRequests)},
		{name: "400 slow_down", status: http.StatusBadRequest, body: `{"error":"slow_down"}`, want: tool.HTTPFailureCode(http.StatusTooManyRequests)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := newTokenEndpoint(t, tc.status, tc.body)
			h := newOutageHandler(endpoint, &fakeClock{now: time.Now()})
			credSvc := newMockCredentialService()
			storeExpiredToken(t, credSvc)
			toolCtx := testToolCtx(credSvc)

			_, err := h.GetOAuthToken(t.Context(), toolCtx)
			if got := NewSignInProviderUnavailableResult("tool", err).ErrorCode; got != tc.want {
				t.Errorf("ErrorCode = %q, want %q", got, tc.want)
			}
			_, deferred := h.GetOAuthToken(t.Context(), toolCtx)
			if got := NewSignInProviderUnavailableResult("tool", deferred).ErrorCode; got != tc.want {
				t.Errorf("ErrorCode for a deferred refresh = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetOAuthToken_CancelledCall_IsNotAProviderOutage(t *testing.T) {
	endpoint := newTokenEndpoint(t, http.StatusServiceUnavailable, `{"error":"server_error"}`)
	h := newOutageHandler(endpoint, &fakeClock{now: time.Now()})
	credSvc := newMockCredentialService()
	storeExpiredToken(t, credSvc)
	toolCtx := testToolCtx(credSvc)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := h.GetOAuthToken(cancelled, toolCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetOAuthToken: got %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrTransientRefresh) || errors.Is(err, ErrAuthRequired) {
		t.Errorf("a cancelled call must not read as an outage or a sign-in request: %v", err)
	}
	if storedCredential(t, credSvc) == "" {
		t.Fatal("a cancelled call must keep the credential")
	}

	if _, err = h.GetOAuthToken(t.Context(), toolCtx); !errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("GetOAuthToken after the cancel: got %v, want ErrTransientRefresh", err)
	}
	if got := endpoint.requests.Load(); got != 1 {
		t.Errorf("refresh requests = %d, want 1: the cancel must not defer the next refresh", got)
	}
}

func TestRefreshFailureLogAttrs_OmitsTheProviderAnswer(t *testing.T) {
	const providerText = "internal hostname db-7.idp.corp is down"
	endpoint := newTokenEndpoint(t, http.StatusServiceUnavailable, `{"error":"server_error","error_description":"`+providerText+`"}`)
	h := newOutageHandler(endpoint, &fakeClock{now: time.Now()})
	credSvc := newMockCredentialService()
	storeExpiredToken(t, credSvc)

	_, err := h.GetOAuthToken(t.Context(), testToolCtx(credSvc))
	if !errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("GetOAuthToken: got %v, want ErrTransientRefresh", err)
	}
	attrs := fmt.Sprint(RefreshFailureLogAttrs(err)...)
	if strings.Contains(attrs, providerText) {
		t.Errorf("log fields carry the provider's error description: %s", attrs)
	}
	if !strings.Contains(attrs, "server_error") || !strings.Contains(attrs, "503") {
		t.Errorf("log fields = %s, want the OAuth code and HTTP status", attrs)
	}
}
