// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
)

var (
	webhookRouteTransformKeys = []string{"name", "input_expression", "headers", "artifact_processing"}
	eventHandlerTransformKeys = []string{
		"name", "input_expression", "structured_invocation",
		"payload_format", "payload_encoding", "forward_context", "artifact_processing",
	}
)

func computeGatewayPromptTransformHash(config map[string]any) (string, error) {
	appConfig := firstAppConfig(config)
	adapter, _ := appConfig["gateway_adapter"].(map[string]any)
	adapterConfig, _ := adapter["adapter_config"].(map[string]any)

	routes, err := projectNamedTransforms(adapterConfig["routes"], webhookRouteTransformKeys)
	if err != nil {
		return "", fmt.Errorf("project webhook routes: %w", err)
	}
	handlers, err := projectNamedTransforms(appConfig["event_handlers"], eventHandlerTransformKeys)
	if err != nil {
		return "", fmt.Errorf("project event handlers: %w", err)
	}

	systemPurpose, _ := appConfig["system_purpose"].(string)
	responseFormat, _ := appConfig["response_format"].(string)
	hash, err := versionedHash(map[string]any{
		"system_purpose":  systemPurpose,
		"response_format": responseFormat,
		"webhook_routes":  routes,
		"event_handlers":  handlers,
	})
	if err != nil {
		return "", fmt.Errorf("hash prompt transform projection: %w", err)
	}
	return hash, nil
}

func projectNamedTransforms(value any, keys []string) ([]json.RawMessage, error) {
	entries, err := jsonObjectList(value)
	if err != nil {
		return nil, err
	}
	return sortedProjections(entries, func(entry map[string]any) (string, map[string]any) {
		projected := make(map[string]any, len(keys))
		for _, key := range keys {
			if v, ok := entry[key]; ok {
				projected[key] = v
			}
		}
		name, _ := entry["name"].(string)
		return name, projected
	})
}
