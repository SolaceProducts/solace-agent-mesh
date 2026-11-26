// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const classicAzureAPIVersion = "2024-10-21"

type azureGatewayRoute struct {
	active     bool
	apiVersion string
	basePrefix string
}

const azureChatRouteKey = "azure_chat_route"

func newAzureGatewayRoute(cfg BifrostConfig) azureGatewayRoute {
	if schemas.ModelProvider(cfg.Provider) != schemas.Azure {
		return azureGatewayRoute{}
	}
	providerCfg := normalizeForJSON(cfg.ProviderConfig)
	requested := ""
	if raw, ok := providerCfg[azureChatRouteKey]; ok {
		requested = strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", raw)))
	}
	if requested == "v1" {
		return azureGatewayRoute{}
	}
	if requested != "deployment" && schemas.IsAnthropicModel(cfg.Model) {
		return azureGatewayRoute{}
	}
	if cfg.AzureWorkloadIdentity != nil {
		return azureGatewayRoute{}
	}
	for _, key := range []string{"client_id", "tenant_id", "scopes", "client_secret"} {
		if raw, ok := providerCfg[key]; ok && hasProviderConfigValue(raw) {
			return azureGatewayRoute{}
		}
	}
	host := baseURLHost(AzureEndpoint(cfg))
	if host == "" {
		return azureGatewayRoute{}
	}
	if requested != "deployment" && isFirstPartyAzureHost(host) {
		return azureGatewayRoute{}
	}
	version := classicAzureAPIVersion
	if raw, ok := providerCfg["api_version"]; ok {
		if s := strings.TrimSpace(fmt.Sprintf("%v", raw)); s != "" {
			version = s
		}
	}
	route := azureGatewayRoute{active: true, apiVersion: version}
	if trimmed := TrimTrailingV1(AzureEndpoint(cfg)); trimmed != AzureEndpoint(cfg) {
		route.basePrefix = "/v1"
	}
	return route
}

func (r azureGatewayRoute) checkModel(model string) error {
	if !r.active || !schemas.IsAnthropicModel(model) {
		return nil
	}
	return fmt.Errorf("model %q is Anthropic-family, but this client is configured for the Azure "+
		"deployment chat route, which Azure does not serve for it: give this model its own model "+
		"config, or set %s: v1 to use Bifrost's current Azure routing",
		model, azureChatRouteKey)
}

func hasProviderConfigValue(raw any) bool {
	switch v := raw.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case []any:
		return len(v) > 0
	case []string:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v)) != ""
	}
}

func (r azureGatewayRoute) pathFor(deployment string) string {
	deployment = strings.TrimSpace(deployment)
	if !r.active || deployment == "" {
		return ""
	}
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		r.basePrefix, url.PathEscape(deployment), url.QueryEscape(r.apiVersion))
}

func (r azureGatewayRoute) apply(cfg BifrostConfig, envLookup func(string) string, logger *slog.Logger) BifrostConfig {
	if !r.active {
		return cfg
	}
	if cfg.APIKeyEnvVar == "" {
		azureEnv := defaultAPIKeyEnv(cfg.Provider)
		if envLookup(azureEnv) != "" {
			cfg.APIKeyEnvVar = azureEnv
		}
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = AzureEndpoint(cfg)
	}
	if cfg.OAuth2 == nil && cfg.GrantID == "" {
		if key := envLookup(cfg.APIKeyEnvVar); key != "" {
			headers := make(map[string]string, len(cfg.ExtraHeaders)+1)
			for k, v := range cfg.ExtraHeaders {
				headers[k] = v
			}
			if _, present := headers["api-key"]; !present {
				headers["api-key"] = key
			}
			cfg.ExtraHeaders = headers
		}
	}
	if logger != nil {
		logger.Info("azure endpoint is not a first-party Azure host; using the deployment chat route for gateway compatibility",
			"host", baseURLHost(AzureEndpoint(cfg)),
			"apiVersion", r.apiVersion)
	}
	cfg.Provider = string(schemas.OpenAI)
	return cfg
}

func isFirstPartyAzureHost(host string) bool {
	for _, suffix := range firstPartyAzureHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
