// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
)

type fakeFreeLLM struct {
	mu       sync.Mutex
	accepted string
	status   int
	body     string
	seen     []string
}

func (f *fakeFreeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != allowancePath {
		http.NotFound(w, r)
		return
	}
	f.seen = append(f.seen, r.Header.Get("Authorization"))
	if r.Header.Get("Authorization") != "Bearer "+f.accepted {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.status)
	_, _ = w.Write([]byte(f.body))
}

func (f *fakeFreeLLM) authorizations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func newFreeLLM(t *testing.T, accepted string, status int, body string) (*fakeFreeLLM, Config) {
	t.Helper()
	llmServer := &fakeFreeLLM{accepted: accepted, status: status, body: body}
	srv := httptest.NewServer(llmServer)
	t.Cleanup(srv.Close)
	return llmServer, Config{BaseURL: srv.URL, GeneralModel: "free", PlanningModel: "free-planning", Audience: "https://llm.example.test"}
}

type grantTokens struct {
	tokens []string
	err    error
}

func (g *grantTokens) GrantToken(_ context.Context, grantID, rejectedFingerprint string) (llm.GrantToken, error) {
	if g.err != nil {
		return llm.GrantToken{}, g.err
	}
	if grantID != GrantID {
		return llm.GrantToken{}, fmt.Errorf("unknown grant %s", grantID)
	}
	if rejectedFingerprint != "" && rejectedFingerprint == llm.TokenFingerprint(g.tokens[0]) && len(g.tokens) > 1 {
		g.tokens = g.tokens[1:]
	}
	return llm.GrantToken{AccessToken: g.tokens[0]}, nil
}

func percent(v float64) *float64 { return &v }

func TestAllowanceReportsWhatIsLeft(t *testing.T) {
	_, cfg := newFreeLLM(t, "access-1", http.StatusOK, `{"spend":2.5,"maxBudget":10,"remaining":7.5,"remainingPercent":75}`)

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}}).Get(t.Context())

	require.NoError(t, err)
	assert.Equal(t, Allowance{Status: AllowanceAvailable, RemainingPercent: percent(75)}, allowance)
}

func TestAllowanceWithNoLimitHasNoPercent(t *testing.T) {
	_, cfg := newFreeLLM(t, "access-1", http.StatusOK, `{"spend":0,"maxBudget":null,"remaining":null,"remainingPercent":null}`)

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}}).Get(t.Context())

	require.NoError(t, err)
	assert.Equal(t, Allowance{Status: AllowanceAvailable}, allowance)
}

func TestAllowanceIsAllLeftForAnAccountWithNoRequestsYet(t *testing.T) {
	_, cfg := newFreeLLM(t, "access-1", http.StatusNotFound, `{"error":"not found"}`)

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}}).Get(t.Context())

	require.NoError(t, err)
	assert.Equal(t, Allowance{Status: AllowanceAvailable, RemainingPercent: percent(100)}, allowance)
}

func TestAllowanceKeepsThePercentBetweenNoneAndAll(t *testing.T) {
	for body, want := range map[string]float64{`{"remainingPercent":-3}`: 0, `{"remainingPercent":120}`: 100} {
		_, cfg := newFreeLLM(t, "access-1", http.StatusOK, body)
		allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}}).Get(t.Context())
		require.NoError(t, err)
		assert.Equal(t, percent(want), allowance.RemainingPercent, body)
	}
}

func TestAllowanceRefreshesARejectedTokenOnce(t *testing.T) {
	freeLLM, cfg := newFreeLLM(t, "access-2", http.StatusOK, `{"remainingPercent":40}`)

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1", "access-2"}}).Get(t.Context())

	require.NoError(t, err)
	assert.Equal(t, percent(40), allowance.RemainingPercent)
	assert.Equal(t, []string{"Bearer access-1", "Bearer access-2"}, freeLLM.authorizations())
}

func TestAllowanceGivesUpWhenTheRefreshedTokenIsRejectedToo(t *testing.T) {
	freeLLM, cfg := newFreeLLM(t, "access-3", http.StatusOK, `{"remainingPercent":40}`)

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1", "access-2"}}).Get(t.Context())

	require.Error(t, err)
	assert.Equal(t, AllowanceUnavailable, allowance.Status)
	assert.Len(t, freeLLM.authorizations(), 2)
}

func TestAllowanceAsksForASignInWhenTheGrantNeedsOne(t *testing.T) {
	for name, grantErr := range map[string]error{
		"refused": fmt.Errorf("grant: %w", llm.ErrReauthorizationRequired),
		"gone":    fmt.Errorf("grant: %w", tokenbroker.ErrNotFound),
	} {
		_, cfg := newFreeLLM(t, "access-1", http.StatusOK, `{"remainingPercent":40}`)

		allowance, err := NewAllowances(cfg, &grantTokens{err: grantErr}).Get(t.Context())

		require.NoError(t, err, name)
		assert.Equal(t, Allowance{Status: AllowanceReauthorizationRequired}, allowance, name)
	}
}

func TestAllowanceReportsThePause(t *testing.T) {
	_, cfg := newFreeLLM(t, "access-1", http.StatusServiceUnavailable, `{"error":{"message":"paused","type":"free_tier_paused"}}`)

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}}).Get(t.Context())

	require.NoError(t, err)
	assert.Equal(t, Allowance{Status: AllowancePaused}, allowance)
}

func TestAllowanceIsUnavailableWhenTheFreeLLMFails(t *testing.T) {
	_, cfg := newFreeLLM(t, "access-1", http.StatusServiceUnavailable, `{"error":{"message":"allowance unavailable","type":"allowance_unavailable"}}`)

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}}).Get(t.Context())

	require.Error(t, err)
	assert.Equal(t, Allowance{Status: AllowanceUnavailable, UpstreamStatus: http.StatusServiceUnavailable}, allowance)
}

func TestAllowanceIsUnavailableWhenThePlatformCannotHandOutAToken(t *testing.T) {
	_, cfg := newFreeLLM(t, "access-1", http.StatusOK, `{"remainingPercent":40}`)

	allowance, err := NewAllowances(cfg, &grantTokens{err: errors.New("platform did not answer")}).Get(t.Context())

	require.Error(t, err)
	assert.Equal(t, AllowanceUnavailable, allowance.Status)
}

func TestAllowanceDoesNotFollowARedirect(t *testing.T) {
	var followed sync.Once
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Do(func() { t.Error("the bearer token followed a redirect") })
	}))
	t.Cleanup(elsewhere.Close)
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+allowancePath, http.StatusFound)
	}))
	t.Cleanup(redirecting.Close)
	cfg := Config{BaseURL: redirecting.URL, GeneralModel: "free", PlanningModel: "free-planning", Audience: "https://llm.example.test"}

	allowance, err := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}}).Get(t.Context())

	require.Error(t, err)
	assert.Equal(t, AllowanceUnavailable, allowance.Status)
}
