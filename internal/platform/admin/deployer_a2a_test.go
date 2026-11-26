// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func TestBuildA2AProxyAppConfig_CardAuthTheProxyUses(t *testing.T) {
	t.Parallel()
	none, bearer, unset := authTypeNone, authTypeBearer, ""
	empty, typedNone, cardToken := `{}`, `{"type":"none"}`, `{"token":"card-token"}`
	taskToken := `{"token":"task-token"}`
	tests := []struct {
		name                 string
		cardType, cardConfig *string
		taskAuth, wellKnown  bool
		wantType, wantToken  string
	}{
		{name: "none is never sent, since the proxy has no none auth type", cardType: &none, cardConfig: &empty},
		{name: "none with its type in the config", cardType: &none, cardConfig: &typedNone},
		{name: "none with task auth falls back to the task credential", cardType: &none, cardConfig: &empty, taskAuth: true, wantType: "static_bearer", wantToken: "task-token"},
		{name: "none with no config and task auth", cardType: &none, taskAuth: true, wantType: "static_bearer", wantToken: "task-token"},
		{name: "none with task auth on a well-known card", cardType: &none, cardConfig: &empty, taskAuth: true, wellKnown: true, wantType: "static_bearer", wantToken: "task-token"},
		{name: "no stored card auth falls back to the task credential", taskAuth: true, wantType: "static_bearer", wantToken: "task-token"},
		{name: "an empty card type falls back to the task credential", cardType: &unset, cardConfig: &empty, taskAuth: true, wantType: "static_bearer", wantToken: "task-token"},
		{name: "no stored card auth and no task auth", wellKnown: true},
		{name: "bearer", cardType: &bearer, cardConfig: &cardToken, wantType: "static_bearer", wantToken: "card-token"},
		{name: "bearer with task auth uses the card's own", cardType: &bearer, cardConfig: &cardToken, taskAuth: true, wantType: "static_bearer", wantToken: "card-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &store.A2AAgent{ID: "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f", AgentURL: "https://example.com/agent", Name: "a",
				AgentCardLocation: "custom", AgentCardURL: strPtr("https://cards.example.net/agent.json"),
				AgentCardAuthType: tt.cardType, AgentCardAuthConfig: tt.cardConfig}
			if tt.wellKnown {
				a.AgentCardLocation, a.AgentCardURL = "well_known", nil
			}
			if tt.taskAuth {
				a.AgentAuthType, a.AgentAuthConfig = &bearer, &taskToken
			}
			pc, err := parseAsTheProxyAfterAJSONRoundTrip(t, buildA2AProxyAppConfig(a, "test-ns"))
			if err != nil {
				t.Fatalf("the proxy refused the deployed config: %v", err)
			}
			cardAuth := pc.EffectiveCardAuth()
			gotType, gotToken := "", ""
			if cardAuth != nil {
				gotType, gotToken = cardAuth.Type, cardAuth.Token
			}
			if gotType != tt.wantType || gotToken != tt.wantToken {
				t.Errorf("proxy card auth = %q with token %q, want %q with token %q", gotType, gotToken, tt.wantType, tt.wantToken)
			}
		})
	}
}

func parseAsTheProxyAfterAJSONRoundTrip(t *testing.T, appConfig map[string]any) (*proxy.ProxyConfig, error) {
	t.Helper()
	raw, err := json.Marshal(appConfig)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped := map[string]any{}
	if err = json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatal(err)
	}
	return proxy.ParseProxyConfig("p", config.FromMap(roundTripped), nil)
}
