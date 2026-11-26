// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/providers"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const ProviderTypeSafe = decision.ProviderTypeSafe

const ProviderOpenAI = decision.ProviderOpenAI

const ProviderGoogleAIStudio = decision.ProviderGoogleAIStudio

var (
	decisionProviders     = providers.Supported()
	decisionOnlyProviders = []string{ProviderTypeSafe}
)

const (
	ModelTypeChat     = store.ModelTypeChat
	ModelTypeDecision = store.ModelTypeDecision
)

const RawModelTypeKey = samapp.ModelTypeKey

func ResolveModelType(modelType, provider string) (string, error) {
	switch modelType {
	case "":
		modelType = store.ModelTypeChat
	case store.ModelTypeChat, store.ModelTypeDecision:
	default:
		return "", fmt.Errorf("modelType must be %q or %q, got %q", store.ModelTypeChat, store.ModelTypeDecision, modelType)
	}
	if modelType == store.ModelTypeDecision && !slices.Contains(decisionProviders, provider) {
		return "", fmt.Errorf("a %s model must use provider %s, got %q", store.ModelTypeDecision, quotedOr(decisionProviders), provider)
	}
	if modelType == store.ModelTypeChat && slices.Contains(decisionOnlyProviders, provider) {
		return "", fmt.Errorf("provider %q serves decision models only; set modelType to %q", provider, store.ModelTypeDecision)
	}
	return modelType, nil
}

func quotedOr(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, " or ")
}

func RequireDecisionModelsEnabled(modelType string, registry *features.Registry) error {
	if modelType != store.ModelTypeDecision || DecisionModelsEnabled(registry) {
		return nil
	}
	msg := fmt.Sprintf("decision models require workflow decision nodes or decision-model evaluators, which are both turned off; enable one first (set %s=true or %s=true)",
		features.EnvVarName(features.KeyWorkflowDecisionNode), features.EnvVarName(features.KeyEvalDecisionEvaluator))
	return &ValidationError{Message: msg, Field: "modelType", Details: map[string][]string{"modelType": {msg}}}
}

func DecisionModelsEnabled(registry *features.Registry) bool {
	return registry != nil && (registry.IsEnabled(features.KeyWorkflowDecisionNode) ||
		registry.IsEnabled(features.KeyEvalDecisionEvaluator))
}

func ValidateModelTypeUpdate(storedType, storedProvider, newProvider string) error {
	if _, err := ResolveModelType(storedType, newProvider); err != nil {
		return err
	}
	if storedType == store.ModelTypeDecision && newProvider != storedProvider {
		return fmt.Errorf("a %s model's provider cannot change from %q to %q; create a new model configuration instead",
			store.ModelTypeDecision, storedProvider, newProvider)
	}
	return nil
}

const (
	authConfigTypeKey   = "type"
	authConfigAPIKeyKey = "api_key"
	authTypeAPIKey      = "apikey"
)

func RequireDecisionAPIKey(modelType string, mergedAuthConfig map[string]any) error {
	if modelType != store.ModelTypeDecision {
		return nil
	}
	authType, _ := mergedAuthConfig[authConfigTypeKey].(string)
	key, _ := mergedAuthConfig[authConfigAPIKeyKey].(string)
	key = strings.TrimSpace(key)
	if authType == authTypeAPIKey && key != "" && key != RedactedSentinel {
		return nil
	}
	return fmt.Errorf("a %s model requires authConfig type %q with a non-empty %s", store.ModelTypeDecision, authTypeAPIKey, authConfigAPIKeyKey)
}

const RawDecisionProviderKey = samapp.DecisionProviderKey

func AddModelTypeToRaw(raw map[string]any, modelType, provider string) {
	if modelType == store.ModelTypeDecision {
		raw[RawModelTypeKey] = modelType
		raw[RawDecisionProviderKey] = provider
		return
	}
	delete(raw, RawModelTypeKey)
	delete(raw, RawDecisionProviderKey)
}

func StoredTestTarget(storedType, storedProvider, requestedProvider string) (provider, modelType string, err error) {
	modelType = cmp.Or(storedType, store.ModelTypeChat)
	provider = cmp.Or(requestedProvider, storedProvider)
	if err := ValidateModelTypeUpdate(modelType, storedProvider, provider); err != nil {
		return "", "", &ValidationError{Message: err.Error(), Field: "provider"}
	}
	return provider, modelType, nil
}

func NewTestTarget(requestedType, provider string) (string, error) {
	if requestedType == "" && slices.Contains(decisionOnlyProviders, provider) {
		return store.ModelTypeDecision, nil
	}
	modelType, err := ResolveModelType(requestedType, provider)
	if err != nil {
		return "", &ValidationError{Message: err.Error(), Field: "modelType"}
	}
	return modelType, nil
}
