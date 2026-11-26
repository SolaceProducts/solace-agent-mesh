// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func parseWithStoredParams(t *testing.T, raw, params map[string]any) *llm.BifrostConfig {
	t.Helper()
	WithModelParams(raw, params)
	cfg, err := ParseModelConfig(raw, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestParseModelConfig_StoredParamsBecomeRequestDefaults(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, map[string]any{
		"temperature":      0.7,
		"max_tokens":       float64(4096),
		"stop_sequences":   []any{"A", "B"},
		"top_p":            0.9,
		"presence_penalty": 0.2,
	})

	got := cfg.RequestDefaults
	if got.Temperature == nil || *got.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", got.Temperature)
	}
	if got.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", got.MaxTokens)
	}
	if want := []string{"A", "B"}; !reflect.DeepEqual(got.StopSequences, want) {
		t.Errorf("StopSequences = %v, want %v", got.StopSequences, want)
	}
	if want := map[string]any{"top_p": 0.9, "presence_penalty": 0.2}; !reflect.DeepEqual(got.ExtraParams, want) {
		t.Errorf("ExtraParams = %v, want %v", got.ExtraParams, want)
	}
}

func TestParseModelConfig_StoredParamAliasesAndForms(t *testing.T) {
	cases := map[string]struct {
		params map[string]any
		want   llm.RequestDefaults
	}{
		"max_output_tokens names the limit":            {map[string]any{"max_output_tokens": 100}, llm.RequestDefaults{MaxTokens: 100}},
		"max_tokens wins over max_output_tokens":       {map[string]any{"max_tokens": 50, "max_output_tokens": 100}, llm.RequestDefaults{MaxTokens: 50}},
		"a null max_tokens yields to its alias":        {map[string]any{"max_tokens": nil, "max_output_tokens": 100}, llm.RequestDefaults{MaxTokens: 100}},
		"stop takes a single string":                   {map[string]any{"stop": "END"}, llm.RequestDefaults{StopSequences: []string{"END"}}},
		"stop wins over stop_sequences":                {map[string]any{"stop": "END", "stop_sequences": []any{"X"}}, llm.RequestDefaults{StopSequences: []string{"END"}}},
		"a null stop yields to stop_sequences":         {map[string]any{"stop": nil, "stop_sequences": []any{"X"}}, llm.RequestDefaults{StopSequences: []string{"X"}}},
		"a null param is not sent":                     {map[string]any{"top_p": nil}, llm.RequestDefaults{}},
		"an integer temperature is a temperature":      {map[string]any{"temperature": 1}, llm.RequestDefaults{Temperature: new(1.0)}},
		"no stored params leave the request untouched": {nil, llm.RequestDefaults{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, c.params)
			if !reflect.DeepEqual(cfg.RequestDefaults, c.want) {
				t.Errorf("RequestDefaults = %+v, want %+v", cfg.RequestDefaults, c.want)
			}
		})
	}
}

func TestParseModelConfig_UnusableStoredParamsAreNotSent(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, map[string]any{
		"temperature": "hot",
		"max_tokens":  -5,
		"stop":        123,
		"top_p":       0.9,
	})

	want := llm.RequestDefaults{ExtraParams: map[string]any{"top_p": 0.9}}
	if !reflect.DeepEqual(cfg.RequestDefaults, want) {
		t.Errorf("RequestDefaults = %+v, want %+v", cfg.RequestDefaults, want)
	}
}

func TestParseModelConfig_ClientSettingsStoredAsParamsStayOutOfTheRequest(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, map[string]any{
		"cache_strategy":              "5m",
		"thinking":                    map[string]any{"budget_tokens": 2048},
		"type":                        "apikey",
		"extra_headers":               map[string]any{"X-Correlation-ID": "abc"},
		"stream_idle_timeout_seconds": 600,
		"request_timeout_seconds":     30,
		"num_retries":                 2,
		"oauth_scope":                 "llm",
		"top_p":                       0.9,
	})

	if want := map[string]any{"top_p": 0.9}; !reflect.DeepEqual(cfg.RequestDefaults.ExtraParams, want) {
		t.Errorf("ExtraParams = %v, want %v", cfg.RequestDefaults.ExtraParams, want)
	}
}

func TestParseModelConfig_LiteLLMSettingsStoredAsParamsStayOutOfTheRequest(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, map[string]any{
		"drop_params":   true,
		"timeout":       30,
		"organization":  "org-made-up",
		"metadata":      map[string]any{"team": "made-up"},
		"client_secret": "made-up-secret",
		"top_p":         0.9,
	})

	if want := map[string]any{"top_p": 0.9}; !reflect.DeepEqual(cfg.RequestDefaults.ExtraParams, want) {
		t.Errorf("ExtraParams = %v, want %v", cfg.RequestDefaults.ExtraParams, want)
	}
}

func TestParseModelConfig_AnUnusableOutputLimitOrStopGivesWayToTheNextName(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, map[string]any{
		"max_tokens":        "abc",
		"max_output_tokens": 2048,
		"stop":              123,
		"stop_sequences":    []any{"END"},
	})

	want := llm.RequestDefaults{MaxTokens: 2048, StopSequences: []string{"END"}}
	if !reflect.DeepEqual(cfg.RequestDefaults, want) {
		t.Errorf("RequestDefaults = %+v, want %+v", cfg.RequestDefaults, want)
	}
}

func TestParseModelConfig_ProviderSettingsStoredAsParamsStayOutOfTheRequest(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "azure/gpt-4o", "api_key": "k"}, map[string]any{
		"endpoint":    "https://llm.example.invalid",
		"api_version": "2024-10-21",
		"top_p":       0.9,
	})

	if want := map[string]any{"top_p": 0.9}; !reflect.DeepEqual(cfg.RequestDefaults.ExtraParams, want) {
		t.Errorf("ExtraParams = %v, want %v", cfg.RequestDefaults.ExtraParams, want)
	}
}

func TestParseModelConfig_OnlyTheStoredParamsEntryFeedsTheRequest(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{
		"model":                 "bedrock/anthropic.claude-sonnet",
		"aws_access_key_id":     "test-access-key",
		"aws_secret_access_key": "example-secret",
		"aws_region_name":       "us-east-1",
		"max_input_tokens":      200000,
		"temperature":           0.3,
		"top_p":                 0.5,
		ModelConfigIDKey:        "model-1",
	}, map[string]any{"top_k": 40})

	want := llm.RequestDefaults{ExtraParams: map[string]any{"top_k": 40}}
	if !reflect.DeepEqual(cfg.RequestDefaults, want) {
		t.Errorf("RequestDefaults = %+v, want only the stored params entry %+v", cfg.RequestDefaults, want)
	}
}

func TestParseModelConfig_StoredParamsEntryIsNotProviderConfig(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "bedrock/anthropic.claude-sonnet", "aws_region_name": "us-east-1"},
		map[string]any{"top_k": 40})

	if _, present := cfg.ProviderConfig[ModelParamsKey]; present {
		t.Errorf("ProviderConfig carries the stored params entry: %v", cfg.ProviderConfig)
	}
}

func TestForPythonAgent_DropsTheStoredParamsEntryAndKeepsTheFlattenedParams(t *testing.T) {
	raw := map[string]any{"model": "openai/gpt-4o", "temperature": 0.3}
	WithModelParams(raw, map[string]any{"temperature": 0.3})

	sent := ForPythonAgent(raw)
	if _, present := sent[ModelParamsKey]; present {
		t.Errorf("a Python agent would forward %s to the provider as a completion argument: %v", ModelParamsKey, sent)
	}
	if sent["temperature"] != 0.3 {
		t.Errorf("flattened temperature = %v, want 0.3", sent["temperature"])
	}
	if _, present := raw[ModelParamsKey]; !present {
		t.Error("the caller's config was changed")
	}
}

func TestModelReasoning(t *testing.T) {
	cases := map[string]struct {
		thinking any
		want     *llm.ReasoningConfig
	}{
		"effort alone":            {map[string]any{"effort": "high"}, &llm.ReasoningConfig{Effort: "high"}},
		"budget alone":            {map[string]any{"budget_tokens": 2048}, &llm.ReasoningConfig{BudgetTokens: 2048}},
		"effort wins over budget": {map[string]any{"effort": "low", "budget_tokens": 2048}, &llm.ReasoningConfig{Effort: "low"}},
		"neither":                 {map[string]any{}, nil},
		"not a map":               {"on", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ModelReasoning(map[string]any{"thinking": c.thinking}); !reflect.DeepEqual(got, c.want) {
				t.Errorf("ModelReasoning = %+v, want %+v", got, c.want)
			}
		})
	}
	if got := ModelReasoning(nil); got != nil {
		t.Errorf("ModelReasoning(nil) = %+v, want nil", got)
	}
}

func TestParseModelConfig_StoredParamsNeverReplaceTheFieldsSAMSetsOnEveryRequest(t *testing.T) {
	cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, map[string]any{
		"model":          "other-model",
		"messages":       []any{},
		"stream":         false,
		"stream_options": map[string]any{"include_usage": false},
		"tools":          []any{},
		"tool_choice":    "none",
		"top_p":          0.9,
	})

	want := llm.RequestDefaults{ExtraParams: map[string]any{"top_p": 0.9}}
	if !reflect.DeepEqual(cfg.RequestDefaults, want) {
		t.Errorf("RequestDefaults = %+v, want %+v", cfg.RequestDefaults, want)
	}
}

func TestParseModelConfig_MaxCompletionTokensIsAnotherNameForTheOutputLimit(t *testing.T) {
	cases := map[string]struct {
		params map[string]any
		want   llm.RequestDefaults
	}{
		"on its own":                  {map[string]any{"max_completion_tokens": 2048}, llm.RequestDefaults{MaxTokens: 2048}},
		"max_tokens wins":             {map[string]any{"max_tokens": 100, "max_completion_tokens": 2048}, llm.RequestDefaults{MaxTokens: 100}},
		"max_output_tokens wins":      {map[string]any{"max_output_tokens": 300, "max_completion_tokens": 2048}, llm.RequestDefaults{MaxTokens: 300}},
		"null preferred names yield":  {map[string]any{"max_tokens": nil, "max_output_tokens": nil, "max_completion_tokens": 2048}, llm.RequestDefaults{MaxTokens: 2048}},
		"never also sent as an extra": {map[string]any{"max_completion_tokens": 2048, "top_p": 0.9}, llm.RequestDefaults{MaxTokens: 2048, ExtraParams: map[string]any{"top_p": 0.9}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := parseWithStoredParams(t, map[string]any{"model": "openai/gpt-4o"}, c.params)
			if !reflect.DeepEqual(cfg.RequestDefaults, c.want) {
				t.Errorf("RequestDefaults = %+v, want %+v", cfg.RequestDefaults, c.want)
			}
		})
	}
}

func TestParseModelConfig_ParallelToolCallsIsSentInItsOwnFieldWhenItIsABoolean(t *testing.T) {
	cases := map[string]struct {
		value any
		want  llm.RequestDefaults
	}{
		"a boolean":      {false, llm.RequestDefaults{ParallelToolCalls: new(false)}},
		"not a boolean":  {"no", llm.RequestDefaults{}},
		"a null literal": {nil, llm.RequestDefaults{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := parseWithStoredParams(t, map[string]any{"model": "anthropic/claude-sonnet-4-5"}, map[string]any{parallelToolCallsKey: c.value})
			if !reflect.DeepEqual(cfg.RequestDefaults, c.want) {
				t.Errorf("RequestDefaults = %+v, want %+v", cfg.RequestDefaults, c.want)
			}
		})
	}
}
