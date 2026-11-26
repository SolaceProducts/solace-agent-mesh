// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

const webSearchConnectorType = "web_search"

const webSearchSTRBinary = "web_search"

var webSearchToolNamePattern = regexp.MustCompile(`^` + tool.WebSearchTurnKey + `_[a-z0-9_]{1,53}$`)

var webSearchToolTimeoutSeconds = int(websearch.MaxCallDuration().Seconds()) + 10

func webSearchToolNameAllowed(name string) bool {
	return webSearchToolNamePattern.MatchString(name) && toolinit.LookupBuiltinTool(name, nil, toolinit.Options{}) == nil
}

func buildWebSearchConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	search := webSearchProviderConfig(conn, connUUID)
	if search == nil {
		return nil
	}
	toolName, toolDescription := resolveBuiltinConnectorNaming(conn.Values, builtinConnectorNaming{
		suffix:   webSearchToolSuffix(conn.ID),
		baseVerb: tool.WebSearchTurnKey,
	})
	toolConfig := map[string]any{
		"_str_binary": webSearchSTRBinary,
		"tool_name":   toolName,
	}
	maps.Copy(toolConfig, search)
	if maxResults, set := toFloat64(conn.Values["max_results"]); set {
		toolConfig["max_results"] = int(maxResults)
	}
	if toolDescription != "" {
		toolConfig["tool_description"] = toolDescription
	}
	return withConnectorHIL(map[string]any{
		"tool_type":       "builtin",
		"tool_name":       toolName,
		"timeout_seconds": webSearchToolTimeoutSeconds,
		"tool_config":     toolConfig,
	}, conn.Values, tool.WebSearchTurnKey)
}

func webSearchProviderConfig(conn ConnectorConfig, connUUID string) map[string]any {
	provider := strings.ToLower(conn.Subtype)
	apiBase, known := webSearchAPIBase(provider, conn.Values)
	if !known {
		return nil
	}
	return map[string]any{
		"provider":    provider,
		"api_key":     stringOrEnvVar(conn.Values, "api_key", "API_KEY_"+connUUID),
		"api_base":    apiBase,
		"safe_search": webSearchSafeSearch(conn.Values),
	}
}

func webSearchToolSuffix(connID string) string {
	digits := strings.ReplaceAll(connID, "-", "")
	return digits[max(0, len(digits)-8):]
}

func toolNameSuffix(connType, connID string) string {
	if connType == webSearchConnectorType {
		return webSearchToolSuffix(connID)
	}
	return connectorSuffix(connID)
}

func webSearchAPIBase(provider string, values map[string]any) (string, bool) {
	fields, known := declaredConnectorFields(webSearchConnectorType, provider)
	if !known {
		return "", false
	}
	if base := strings.TrimSpace(stringFromValues(values, "api_base")); base != "" {
		return base, true
	}
	for _, f := range fields {
		if f.Name == "api_base" {
			base, _ := f.Default.(string)
			return base, true
		}
	}
	return "", true
}

func webSearchSafeSearch(values map[string]any) bool {
	return stringFromValues(values, "safe_search") == "yes"
}

func validateWebSearchValues(values map[string]any) error {
	if key := stringFromValues(values, "api_key"); key != "" && strings.TrimSpace(key) == "" {
		return &ValidationError{Field: "values.api_key", Message: "API key must not be blank"}
	}
	if base := stringFromValues(values, "api_base"); base != "" && !isPlaceholder(base) {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return &ValidationError{
				Field:   "values.api_base",
				Message: "API Base URL must be an absolute http:// or https:// URL with a host and no user name or password",
			}
		}
	}
	if name := stringFromValues(values, "tool_name"); name != "" && !isPlaceholder(name) && !webSearchToolNameAllowed(name) {
		return &ValidationError{
			Field: "values.tool_name",
			Message: fmt.Sprintf("tool_name must start with %q, contain only lowercase letters, digits and underscores, be at most 64 characters "+
				"and not be the name of a built-in tool such as web_search_google: only such a name gets its citations into the chat", tool.WebSearchTurnKey+"_"),
		}
	}
	return nil
}
