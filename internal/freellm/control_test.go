// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/solaceid"
)

var testConfig = Config{BaseURL: "https://llm.example.test", GeneralModel: "free", PlanningModel: "free-planning", Audience: "https://llm.example.test"}

type recordedGrants struct {
	saved   map[string]tokenbroker.SaveRequest
	deleted []string
}

func (g *recordedGrants) Save(_ context.Context, id string, req tokenbroker.SaveRequest) error {
	if g.saved == nil {
		g.saved = map[string]tokenbroker.SaveRequest{}
	}
	g.saved[id] = req
	return nil
}

func (g *recordedGrants) Delete(_ context.Context, id string) error {
	g.deleted = append(g.deleted, id)
	return nil
}

type rotatingTokens struct {
	current  string
	rotation int
	asked    []string
}

func (r *rotatingTokens) GrantToken(_ context.Context, grantID, rejectedFingerprint string) (llm.GrantToken, error) {
	if grantID != GrantID {
		return llm.GrantToken{}, errors.New("unknown grant")
	}
	r.asked = append(r.asked, rejectedFingerprint)
	if rejectedFingerprint == llm.TokenFingerprint(r.current) {
		r.rotation++
		r.current = "access-refreshed"
	}
	return llm.GrantToken{AccessToken: r.current}, nil
}

func newTestPlatform(grants *recordedGrants, tokens llm.GrantTokens) *Platform {
	sid := solaceid.Config{Issuer: "https://id.example.test/", ClientID: "desktop", Audience: testConfig.Audience}
	tokenEndpoint := func(context.Context) (string, error) { return "https://id.example.test/oauth/token", nil }
	return NewPlatform(testConfig, sid, tokenEndpoint, grants, tokens)
}

func TestSaveGrantSendsTheSignInAsAGrantForTheFreeLLM(t *testing.T) {
	grants := &recordedGrants{}
	expires := time.Now().Add(time.Hour).Truncate(time.Millisecond)

	err := newTestPlatform(grants, &rotatingTokens{}).SaveGrant(t.Context(), activation.SignInResult{
		Subject: "auth0|ada", Email: "ada@example.com",
		Tokens: activation.FreeModelTokens{RefreshToken: "refresh-1", AccessToken: "access-1", ExpiresAt: expires},
	})

	require.NoError(t, err)
	assert.Equal(t, tokenbroker.SaveRequest{
		Name:           grantName,
		Flow:           store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer:         "https://id.example.test/",
		TokenEndpoint:  "https://id.example.test/oauth/token",
		ClientID:       "desktop",
		Audience:       testConfig.Audience,
		Scopes:         "openid email profile offline_access",
		APIBase:        testConfig.BaseURL,
		AccountSubject: "auth0|ada",
		AccountLabel:   "ada@example.com",
		RefreshToken:   "refresh-1",
		AccessToken:    "access-1",
		ExpiresAt:      expires,
	}, grants.saved[GrantID])
}

func TestRefreshMakesThePlatformRefreshTheGrant(t *testing.T) {
	tokens := &rotatingTokens{current: "access-at-sign-in"}

	require.NoError(t, newTestPlatform(&recordedGrants{}, tokens).Refresh(t.Context()))

	assert.Equal(t, 1, tokens.rotation)
	assert.Equal(t, []string{"", llm.TokenFingerprint("access-at-sign-in")}, tokens.asked)
}

func TestDeleteGrantDeletesTheFreeModelGrant(t *testing.T) {
	grants := &recordedGrants{}

	require.NoError(t, newTestPlatform(grants, &rotatingTokens{}).DeleteGrant(t.Context()))

	assert.Equal(t, []string{GrantID}, grants.deleted)
}

func TestSettingsPointTheModelsAtTheGrantsAPI(t *testing.T) {
	settings := testConfig.Settings()

	assert.Equal(t, activation.FreeModelSettings{GrantID: GrantID, Provider: "openai", APIBase: "https://llm.example.test/v1", GeneralModelName: "free", PlanningModelName: "free-planning"}, settings)
}
