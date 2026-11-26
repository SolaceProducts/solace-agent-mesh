// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import "slices"

var sharedProxyFields = []string{
	"namespace",
	"artifact_service",
	"artifact_handling_mode",
	"discovery_interval_seconds",
	"default_request_timeout_seconds",
	"task_state_ttl_minutes",
	"task_cleanup_interval_minutes",
	"convert_progress_updates",
	"trusted_card_jwks_urls",
	"require_signed_card",
}

var cardTrustFields = []string{"trusted_card_jwks_urls", "require_signed_card"}

func ExpandProxyConfig(appConfig map[string]any) []ExpandedProxy {
	agents, ok := appConfig["proxied_agents"].([]any)
	if !ok || len(agents) == 0 {
		return nil
	}

	shared := make(map[string]any)
	for _, key := range sharedProxyFields {
		if v, exists := appConfig[key]; exists {
			shared[key] = v
		}
	}

	var result []ExpandedProxy
	for _, item := range agents {
		agentMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := agentMap["name"].(string)
		if name == "" {
			continue
		}
		result = append(result, ExpandedProxy{
			Name:      name,
			AppConfig: mergeAgentFields(shared, agentMap),
		})
	}

	return result
}

func mergeAgentFields(shared, agent map[string]any) map[string]any {
	_, staticCard := staticCardData(agent["agent_card_data"])
	flat := make(map[string]any, len(shared)+len(agent))
	for k, v := range shared {
		if !staticCard || !slices.Contains(cardTrustFields, k) {
			flat[k] = v
		}
	}
	for k, v := range agent {
		if k != "name" && !keepsSharedValue(shared, k, v) {
			flat[k] = v
		}
	}
	return flat
}

func keepsSharedValue(shared map[string]any, key string, agentValue any) bool {
	_, isShared := shared[key]
	return isShared && agentValue == nil && slices.Contains(cardTrustFields, key)
}

type ExpandedProxy struct {
	Name      string
	AppConfig map[string]any
}
