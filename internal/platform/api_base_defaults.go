// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/typesafe"
)

var httpURLPattern = regexp.MustCompile(`^(?i)https?://[^\s/?#][^\s]*$`)

func ValidAPIBaseStructure(apiBase string) bool {
	trimmed := strings.TrimSpace(apiBase)
	if trimmed == "" {
		return true
	}
	if strings.ContainsFunc(trimmed, unicode.IsSpace) {
		return false
	}
	return httpURLPattern.MatchString(trimmed)
}

func DefaultAPIBase(provider string, authConfig map[string]any) string {
	switch provider {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com"
	case "google_ai_studio":
		return "https://generativelanguage.googleapis.com/v1beta"
	case ProviderTypeSafe:
		return typesafe.DefaultBaseURL
	case "vertex_ai":
		if loc := trimmedAuthString(authConfig, "vertex_location"); loc != "" {
			return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1", loc)
		}
	case "bedrock":
		if region := trimmedAuthString(authConfig, "aws_region_name"); region != "" {
			return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", region)
		}
	}
	return ""
}

func trimmedAuthString(authConfig map[string]any, key string) string {
	if authConfig == nil {
		return ""
	}
	v, _ := authConfig[key].(string)
	return strings.TrimSpace(v)
}
