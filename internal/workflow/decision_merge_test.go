// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"
)

func TestMergeOperatorDecision_TuningOnlyBlockAppliesOverTheBody(t *testing.T) {
	tests := []struct {
		name     string
		body     DecisionProviderConfig
		operator map[string]any
		want     DecisionProviderConfig
	}{
		{
			name:     "model alias keeps the alias and takes the operator's limits",
			body:     DecisionProviderConfig{Provider: DecisionProviderTypeSafe, ModelAlias: "jev", TimeoutSeconds: 30},
			operator: map[string]any{"timeout_seconds": 5, "max_state_bytes": 4096},
			want:     DecisionProviderConfig{Provider: DecisionProviderTypeSafe, ModelAlias: "jev", TimeoutSeconds: 5, MaxStateBytes: 4096},
		},
		{
			name:     "inline endpoint keeps the author's key and base",
			body:     DecisionProviderConfig{Provider: DecisionProviderTypeSafe, APIBase: "https://jev.example", APIKey: "author-key", Model: "jev-1.12.0", MaxStateBytes: 2048},
			operator: map[string]any{"model": "jev-1.13.0"},
			want:     DecisionProviderConfig{Provider: DecisionProviderTypeSafe, APIBase: "https://jev.example", APIKey: "author-key", Model: "jev-1.13.0", MaxStateBytes: 2048},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeOperatorDecision(&tc.body, tc.operator)
			if err != nil {
				t.Fatalf("MergeOperatorDecision: %v", err)
			}
			if *got != tc.want {
				t.Errorf("merged = %+v, want %+v", *got, tc.want)
			}
		})
	}
}

func TestMergeOperatorDecision_NamesAnInvalidOperatorBlockAsAppConfig(t *testing.T) {
	_, err := MergeOperatorDecision(nil, map[string]any{"timeout_seconds": -1})
	if err == nil || !strings.HasPrefix(err.Error(), "app_config decision:") {
		t.Fatalf("err = %v, want it to start with %q", err, "app_config decision:")
	}
}

func TestMergeOperatorDecision_OperatorModelConflictsWithABodyAlias(t *testing.T) {
	body := &DecisionProviderConfig{Provider: DecisionProviderTypeSafe, ModelAlias: "jev"}
	_, err := MergeOperatorDecision(body, map[string]any{"model": "jev-1.13.0"})
	if err == nil || !strings.Contains(err.Error(), `model_alias "jev"`) {
		t.Fatalf("err = %v, want the model/model_alias conflict named", err)
	}
}

func TestMergeOperatorDecision_KeepsTheWorkflowsProvider(t *testing.T) {
	openAIBody := func() *DecisionProviderConfig {
		return &DecisionProviderConfig{Provider: DecisionProviderOpenAI, Model: "gpt-6-luna"}
	}
	t.Run("a provider-less key block is refused for an OpenAI workflow", func(t *testing.T) {
		for _, block := range []map[string]any{{"api_key": "ts-operator"}, {"api_base": "https://jev.example"}} {
			_, err := MergeOperatorDecision(openAIBody(), block)
			if err == nil || !strings.Contains(err.Error(), "name the provider in the app_config block") {
				t.Fatalf("block %v: err = %v, want the operator asked to name the provider", block, err)
			}
		}
	})
	t.Run("a key block that names openai applies to an OpenAI workflow", func(t *testing.T) {
		got, err := MergeOperatorDecision(openAIBody(), map[string]any{"provider": "openai", "api_key": "sk-operator"})
		if err != nil {
			t.Fatalf("MergeOperatorDecision: %v", err)
		}
		if got.Provider != DecisionProviderOpenAI || got.Model != "gpt-6-luna" || got.APIKey != "sk-operator" || !got.ReplacedWorkflowBlock {
			t.Errorf("merged = %+v, want the operator's key on the workflow's OpenAI provider and model", *got)
		}
	})
	t.Run("a provider-less key block stays TypeSafe for a TypeSafe workflow", func(t *testing.T) {
		got, err := MergeOperatorDecision(&DecisionProviderConfig{Provider: DecisionProviderTypeSafe, Model: "jev-1.13.0"}, map[string]any{"api_key": "ts-operator"})
		if err != nil {
			t.Fatalf("MergeOperatorDecision: %v", err)
		}
		if got.Provider != DecisionProviderTypeSafe || got.Model != "jev-1.13.0" {
			t.Errorf("merged = %+v, want the legacy provider-less block unchanged", *got)
		}
	})
	t.Run("a provider-less key block replaces an aliased workflow's block as TypeSafe", func(t *testing.T) {
		body := &DecisionProviderConfig{Provider: DecisionProviderOpenAI, ModelAlias: "luna"}
		got, err := MergeOperatorDecision(body, map[string]any{"api_key": "ts-operator"})
		if err != nil {
			t.Fatalf("MergeOperatorDecision: %v", err)
		}
		if got.Provider != DecisionProviderTypeSafe || got.ModelAlias != "" || got.APIKey != "ts-operator" {
			t.Errorf("merged = %+v, want the operator's TypeSafe block in place of the alias, whose provider the workflow never chose", *got)
		}
	})
	t.Run("a provider-less alias block replaces an OpenAI workflow's endpoint", func(t *testing.T) {
		got, err := MergeOperatorDecision(openAIBody(), map[string]any{"model_alias": "router"})
		if err != nil {
			t.Fatalf("MergeOperatorDecision: %v", err)
		}
		if got.ModelAlias != "router" || got.APIKey != "" {
			t.Errorf("merged = %+v, want the operator's alias, whose binding names its own provider", *got)
		}
	})
	t.Run("a block that switches provider drops the other provider's model pin", func(t *testing.T) {
		got, err := MergeOperatorDecision(openAIBody(), map[string]any{"provider": "typesafe", "api_key": "ts-operator"})
		if err != nil {
			t.Fatalf("MergeOperatorDecision: %v", err)
		}
		if got.Provider != DecisionProviderTypeSafe || got.Model != "" {
			t.Errorf("merged = %+v, want TypeSafe with no OpenAI model carried over", *got)
		}
	})
	t.Run("a tuning-only block that names another provider is refused", func(t *testing.T) {
		_, err := MergeOperatorDecision(openAIBody(), map[string]any{"provider": "typesafe", "timeout_seconds": 5})
		if err == nil || !strings.Contains(err.Error(), `sets provider "typesafe"`) {
			t.Fatalf("err = %v, want the provider switch without an endpoint refused", err)
		}
	})
	t.Run("a tuning-only block that names the same provider applies", func(t *testing.T) {
		got, err := MergeOperatorDecision(openAIBody(), map[string]any{"provider": "openai", "timeout_seconds": 5})
		if err != nil {
			t.Fatalf("MergeOperatorDecision: %v", err)
		}
		if got.Provider != DecisionProviderOpenAI || got.TimeoutSeconds != 5 {
			t.Errorf("merged = %+v", *got)
		}
	})
}

func TestMergeOperatorDecision_EndpointBlockReplacesTheBody(t *testing.T) {
	body := &DecisionProviderConfig{Provider: DecisionProviderTypeSafe, ModelAlias: "jev"}
	got, err := MergeOperatorDecision(body, map[string]any{"api_base": "https://jev.example"})
	if err != nil {
		t.Fatalf("MergeOperatorDecision: %v", err)
	}
	if got.ModelAlias != "" || got.APIBase != "https://jev.example" || !got.ReplacedWorkflowBlock {
		t.Errorf("merged = %+v, want the operator block in place of the body's, marked as a replacement", *got)
	}
}
