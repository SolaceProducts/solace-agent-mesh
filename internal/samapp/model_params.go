// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"log/slog"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const ModelParamsKey = "model_params"

func WithModelParams(raw, params map[string]any) {
	if len(params) == 0 {
		delete(raw, ModelParamsKey)
		return
	}
	raw[ModelParamsKey] = params
}

var requestShapeParams = []string{"model", "messages", "stream", "stream_options", "tools", "tool_choice"}

const parallelToolCallsKey = "parallel_tool_calls"

var (
	outputLimitNames = []string{"max_tokens", "max_output_tokens", "max_completion_tokens"}
	stopNames        = []string{"stop", "stop_sequences"}
)

func requestDefaults(raw map[string]any, provider string, logger *slog.Logger) llm.RequestDefaults {
	params, _ := raw[ModelParamsKey].(map[string]any)
	defaults := llm.RequestDefaults{
		MaxTokens:     firstUsable(params, outputLimitNames, outputLimit, logger),
		StopSequences: firstUsable(params, stopNames, stopSequences, logger),
	}
	for key, value := range params {
		if value == nil || slices.Contains(outputLimitNames, key) || slices.Contains(stopNames, key) {
			continue
		}
		used := true
		switch key {
		case "temperature":
			var temperature float64
			if temperature, used = llm.FloatParam(value); used {
				defaults.Temperature = &temperature
			}
		case parallelToolCallsKey:
			var enabled bool
			if enabled, used = value.(bool); used {
				defaults.ParallelToolCalls = &enabled
			}
		default:
			switch {
			case slices.Contains(requestShapeParams, key):
				logger.Warn("stored model parameter names a field SAM sets on every request; not sending it", "key", key)
			case IsClientConfigKey(key), llm.IsProviderConfigKey(provider, key):
			case liteLLMParams[key]:
				logger.Warn("stored model parameter is a LiteLLM setting, which has no effect here; not sending it", "key", key)
			default:
				addExtraParam(&defaults, key, value)
			}
		}
		if !used {
			warnUnusable(logger, key)
		}
	}
	return defaults
}

func firstUsable[T any](params map[string]any, names []string, read func(any) (T, bool), logger *slog.Logger) T {
	for _, name := range names {
		if params[name] == nil {
			continue
		}
		if value, ok := read(params[name]); ok {
			return value
		}
		warnUnusable(logger, name)
	}
	var none T
	return none
}

func warnUnusable(logger *slog.Logger, key string) {
	logger.Warn("stored model parameter has an unusable value; not sending it", "key", key)
}

func addExtraParam(defaults *llm.RequestDefaults, key string, value any) {
	if defaults.ExtraParams == nil {
		defaults.ExtraParams = map[string]any{}
	}
	defaults.ExtraParams[key] = value
}

func outputLimit(value any) (int, bool) {
	limit, ok := llm.IntParam(value)
	return limit, ok && limit > 0
}

func stopSequences(value any) ([]string, bool) {
	stops := stringsParam(value)
	return stops, len(stops) > 0
}

func stringsParam(value any) []string {
	switch s := value.(type) {
	case string:
		if s == "" {
			return nil
		}
		return []string{s}
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func ModelReasoning(raw map[string]any) *llm.ReasoningConfig {
	if effort, _, _ := ExtractReasoningEffort(raw); effort != "" {
		return &llm.ReasoningConfig{Effort: effort}
	}
	if budget := ExtractReasoningBudgetTokens(raw); budget > 0 {
		return &llm.ReasoningConfig{BudgetTokens: budget}
	}
	return nil
}
