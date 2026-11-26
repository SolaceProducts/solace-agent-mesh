// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"fmt"
)

func MergeOperatorDecision(body *DecisionProviderConfig, operatorRaw map[string]any) (*DecisionProviderConfig, error) {
	operator, err := ParseDecisionProvider(operatorRaw)
	if err != nil {
		return nil, fmt.Errorf("app_config %w", err)
	}
	if body == nil {
		return operator, nil
	}
	namesProvider := getString(operatorRaw, "provider") != ""
	if !namesDecisionEndpoint(operator) {
		if namesProvider && operator.Provider != body.Provider {
			return nil, fmt.Errorf("the app_config decision block sets provider %q, but the workflow's decision block uses %q; an app_config block that changes the provider must also name its endpoint (api_key, api_base or model_alias)",
				operator.Provider, body.Provider)
		}
		return tuneDecisionProvider(body, operator)
	}
	if !namesProvider && namesInlineEndpoint(operator) && body.ModelAlias == "" && body.Provider != operator.Provider {
		return nil, fmt.Errorf("the app_config decision block sets an inline endpoint without a provider, which means %q, but the workflow's decision block uses provider %q; name the provider in the app_config block so the key goes to the vendor it belongs to",
			operator.Provider, body.Provider)
	}
	if operator.Model == "" && operator.ModelAlias == "" && operator.Provider == body.Provider {
		operator.Model = body.Model
	}
	operator.ReplacedWorkflowBlock = true
	return operator, nil
}

func namesDecisionEndpoint(p *DecisionProviderConfig) bool {
	return namesInlineEndpoint(p) || p.ModelAlias != ""
}

func namesInlineEndpoint(p *DecisionProviderConfig) bool {
	return p.APIBase != "" || p.APIKey != ""
}

func tuneDecisionProvider(body, operator *DecisionProviderConfig) (*DecisionProviderConfig, error) {
	merged := *body
	merged.TimeoutSeconds = cmp.Or(operator.TimeoutSeconds, body.TimeoutSeconds)
	merged.MaxStateBytes = cmp.Or(operator.MaxStateBytes, body.MaxStateBytes)
	merged.MaxImages = cmp.Or(operator.MaxImages, body.MaxImages)
	merged.MaxImageBytes = cmp.Or(operator.MaxImageBytes, body.MaxImageBytes)
	merged.MaxMedia = cmp.Or(operator.MaxMedia, body.MaxMedia)
	merged.MaxMediaBytes = cmp.Or(operator.MaxMediaBytes, body.MaxMediaBytes)
	if operator.Model != "" {
		if body.ModelAlias != "" {
			return nil, fmt.Errorf("the app_config decision block sets model %q, but the workflow's decision block uses model_alias %q, which carries its own model; drop model from the app_config block", operator.Model, body.ModelAlias)
		}
		merged.Model = operator.Model
	}
	return &merged, nil
}
