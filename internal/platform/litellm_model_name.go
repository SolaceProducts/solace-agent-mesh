// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var litellmProviderPrefixes = map[string]string{
	"google_ai_studio": "gemini/",
	"vertex_ai":        "vertex_ai/",
	"bedrock":          "bedrock/",
	"ollama":           "ollama/",
	"azure_openai":     "azure/",
}

func ResolveLiteLLMModelName(provider, modelType, modelName string) string {
	if provider == "" || modelName == "" || isDecisionModelSentDirectToProvider(modelType) {
		return modelName
	}
	prefix, ok := litellmProviderPrefixes[provider]
	if !ok {
		return modelName
	}
	if strings.Contains(modelName, "/") {
		return modelName
	}
	return prefix + modelName
}

func isDecisionModelSentDirectToProvider(modelType string) bool {
	return modelType == store.ModelTypeDecision
}
