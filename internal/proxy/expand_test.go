// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestExpandProxyConfig_Basic(t *testing.T) {
	appConfig := map[string]any{
		"namespace":                       "test-ns",
		"default_request_timeout_seconds": 120,
		"proxied_agents": []any{
			map[string]any{
				"name": "agent1",
				"url":  "http://agent1.example.com",
			},
			map[string]any{
				"name": "agent2",
				"url":  "http://agent2.example.com",
			},
		},
	}

	expanded := ExpandProxyConfig(appConfig)
	if len(expanded) != 2 {
		t.Fatalf("got %d expanded, want 2", len(expanded))
	}

	if expanded[0].Name != "agent1" {
		t.Errorf("expanded[0].Name = %q, want agent1", expanded[0].Name)
	}
	if expanded[1].Name != "agent2" {
		t.Errorf("expanded[1].Name = %q, want agent2", expanded[1].Name)
	}

	if expanded[0].AppConfig["namespace"] != "test-ns" {
		t.Error("expected namespace to be inherited")
	}
	if expanded[0].AppConfig["default_request_timeout_seconds"] != 120 {
		t.Error("expected timeout to be inherited")
	}

	if expanded[0].AppConfig["url"] != "http://agent1.example.com" {
		t.Errorf("url = %v", expanded[0].AppConfig["url"])
	}
}

func TestExpandProxyConfig_SharedFieldOverride(t *testing.T) {
	appConfig := map[string]any{
		"namespace": "shared-ns",
		"proxied_agents": []any{
			map[string]any{
				"name":      "agent1",
				"url":       "http://agent1.example.com",
				"namespace": "agent1-ns",
			},
		},
	}

	expanded := ExpandProxyConfig(appConfig)
	if len(expanded) != 1 {
		t.Fatalf("got %d expanded, want 1", len(expanded))
	}

	if expanded[0].AppConfig["namespace"] != "agent1-ns" {
		t.Errorf("namespace = %v, want agent1-ns", expanded[0].AppConfig["namespace"])
	}
}

func TestExpandProxyConfig_NoProxiedAgents(t *testing.T) {
	expanded := ExpandProxyConfig(map[string]any{"namespace": "ns"})
	if expanded != nil {
		t.Errorf("expected nil for missing proxied_agents, got %v", expanded)
	}
}

func TestExpandProxyConfig_EmptyList(t *testing.T) {
	expanded := ExpandProxyConfig(map[string]any{
		"proxied_agents": []any{},
	})
	if expanded != nil {
		t.Errorf("expected nil for empty list, got %v", expanded)
	}
}

func TestExpandProxyConfig_SkipsInvalidEntries(t *testing.T) {
	appConfig := map[string]any{
		"proxied_agents": []any{
			"not-a-map",
			map[string]any{"url": "http://missing-name.com"},
			map[string]any{"name": "", "url": "http://empty-name.com"},
			map[string]any{"name": "valid", "url": "http://valid.com"},
		},
	}

	expanded := ExpandProxyConfig(appConfig)
	if len(expanded) != 1 {
		t.Fatalf("got %d expanded, want 1", len(expanded))
	}
	if expanded[0].Name != "valid" {
		t.Errorf("name = %q, want valid", expanded[0].Name)
	}
}

func TestExpandProxyConfig_NameNotInAppConfig(t *testing.T) {
	appConfig := map[string]any{
		"proxied_agents": []any{
			map[string]any{
				"name": "agent1",
				"url":  "http://example.com",
			},
		},
	}

	expanded := ExpandProxyConfig(appConfig)
	if _, ok := expanded[0].AppConfig["name"]; ok {
		t.Error("'name' should not be in AppConfig (it's used at top level)")
	}
}

func TestExpandProxyConfig_CardTrustIsShared(t *testing.T) {
	expanded := ExpandProxyConfig(map[string]any{
		"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"},
		"require_signed_card":    true,
		"proxied_agents": []any{
			map[string]any{"name": "inherits", "url": "https://a.example.com"},
			map[string]any{"name": "opts-out", "url": "https://b.example.com", "require_signed_card": false},
		},
	})
	if len(expanded) != 2 {
		t.Fatalf("expanded %d agents, want 2", len(expanded))
	}
	if got := expanded[0].AppConfig["require_signed_card"]; got != true {
		t.Errorf("inherits: require_signed_card = %v, want true", got)
	}
	if got, ok := expanded[0].AppConfig["trusted_card_jwks_urls"].([]any); !ok || len(got) != 1 {
		t.Errorf("inherits: trusted_card_jwks_urls = %v, want the shared list", expanded[0].AppConfig["trusted_card_jwks_urls"])
	}
	if got := expanded[1].AppConfig["require_signed_card"]; got != false {
		t.Errorf("opts-out: require_signed_card = %v, want its own false", got)
	}
}

func TestExpandProxyConfig_BlankAgentValueKeepsTheShared(t *testing.T) {
	expanded := ExpandProxyConfig(map[string]any{
		"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"},
		"require_signed_card":    true,
		"proxied_agents": []any{
			map[string]any{"name": "blank", "url": "https://a.example.com", "require_signed_card": nil, "trusted_card_jwks_urls": nil},
		},
	})
	if got := expanded[0].AppConfig["require_signed_card"]; got != true {
		t.Errorf("require_signed_card = %v, want the shared true", got)
	}
	if got, ok := expanded[0].AppConfig["trusted_card_jwks_urls"].([]any); !ok || len(got) != 1 {
		t.Errorf("trusted_card_jwks_urls = %v, want the shared list", expanded[0].AppConfig["trusted_card_jwks_urls"])
	}
}

func TestExpandProxyConfig_StaticCardDoesNotInheritCardTrust(t *testing.T) {
	expanded := ExpandProxyConfig(map[string]any{
		"namespace":              "ns",
		"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"},
		"require_signed_card":    true,
		"proxied_agents": []any{
			map[string]any{"name": "fetched", "url": "https://a.example.com"},
			map[string]any{"name": "static", "agent_card_data": map[string]any{"name": "static"}},
		},
	})
	if _, ok := expanded[0].AppConfig["require_signed_card"]; !ok {
		t.Error("the fetched-card agent lost the shared require_signed_card")
	}
	for _, key := range []string{"trusted_card_jwks_urls", "require_signed_card"} {
		if _, ok := expanded[1].AppConfig[key]; ok {
			t.Errorf("the static-card agent inherited %s", key)
		}
	}
	if expanded[1].AppConfig["namespace"] != "ns" {
		t.Error("the static-card agent lost an unrelated shared field")
	}
	if _, err := ParseProxyConfig("static", config.FromMap(expanded[1].AppConfig), discardLogger()); err != nil {
		t.Errorf("the static-card agent does not load: %v", err)
	}
}

func TestExpandProxyConfig_StaticCardMeansAMap(t *testing.T) {
	for _, data := range []any{"not a card", []any{"x"}, ""} {
		expanded := ExpandProxyConfig(map[string]any{
			"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"},
			"proxied_agents": []any{
				map[string]any{"name": "a", "url": "https://a.example.com", "agent_card_data": data},
			},
		})
		if _, ok := expanded[0].AppConfig["trusted_card_jwks_urls"]; !ok {
			t.Errorf("agent_card_data %#v: the agent lost the shared trusted_card_jwks_urls", data)
		}
	}
}

func TestExpandProxyConfig_BlankKeepsOnlyCardTrust(t *testing.T) {
	expanded := ExpandProxyConfig(map[string]any{
		"default_request_timeout_seconds": 120,
		"proxied_agents": []any{
			map[string]any{"name": "a", "url": "https://a.example.com", "default_request_timeout_seconds": nil},
		},
	})
	if v, ok := expanded[0].AppConfig["default_request_timeout_seconds"]; !ok || v != nil {
		t.Errorf("default_request_timeout_seconds = %v (present %v), want the agent's blank value", v, ok)
	}
}

func TestExpandProxyConfig_BlankWithoutSharedFailsToLoad(t *testing.T) {
	for _, key := range []string{"require_signed_card", "trusted_card_jwks_urls"} {
		expanded := ExpandProxyConfig(map[string]any{
			"namespace": "ns",
			"proxied_agents": []any{
				map[string]any{"name": "a", "url": "https://a.example.com", key: nil},
			},
		})
		if _, err := ParseProxyConfig("a", config.FromMap(expanded[0].AppConfig), discardLogger()); err == nil || !strings.Contains(err.Error(), "present but blank") {
			t.Errorf("%s blank with no app-level value: err = %v, want the blank-value error", key, err)
		}
	}
}
