// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"fmt"
	"strings"
)

var providerHints = []struct {
	needle   string
	provider string
}{
	{"claude", "anthropic"},
	{"gpt", "openai"},
	{"openai/", "openai"},
	{"gemini", "google"},
	{"google/", "google"},
	{"vertex_ai/", "google"},
	{"bedrock/", "bedrock"},
	{"azure/", "azure"},
	{"mistral", "mistral"},
	{"llama", "meta"},
}

func MigrateModel(legacy any, name string) (CleanSpec, []string, error) {
	if name == "" {
		return CleanSpec{}, nil, fmt.Errorf("model migration requires a name")
	}

	out := CleanSpec{
		Kind:        "model",
		Name:        name,
		Description: fmt.Sprintf("Model: %s", name),
		Spec:        map[string]any{},
	}
	var warnings []string

	switch lg := legacy.(type) {
	case string:
		out.Spec["modelName"] = lg
		out.Spec["provider"] = guessProvider(lg)
		warnings = append(warnings,
			"legacy entry was a bare model id with no api_base or api_key — provider/authConfig need manual completion",
		)
		return out, warnings, nil

	case map[string]any:
		modelStr, _ := lg["model"].(string)
		if modelStr == "" {
			return CleanSpec{}, nil, fmt.Errorf("model %q has no `model:` field", name)
		}
		out.Spec["modelName"] = modelStr
		out.Spec["provider"] = guessProvider(modelStr)
		if out.Spec["provider"] == "unknown" {
			warnings = append(warnings, fmt.Sprintf(
				"could not infer provider from model %q; set spec.provider manually", modelStr,
			))
		}

		if v, ok := lg["api_base"].(string); ok && v != "" {
			out.Spec["apiBase"] = v
		}
		if v, ok := lg["api_key"].(string); ok && v != "" {
			out.Spec["authConfig"] = map[string]any{"apiKey": v}
		}
		params := map[string]any{}
		for k, v := range lg {
			switch k {
			case "model", "api_base", "api_key", "max_input_tokens":
				continue
			}
			params[k] = v
		}
		if len(params) > 0 {
			out.Spec["modelParams"] = params
		}
		if v, ok := coerceInt(lg["max_input_tokens"]); ok {
			out.Spec["maxInputTokens"] = v
		}
		return out, warnings, nil

	default:
		return CleanSpec{}, nil, fmt.Errorf("model %q has unsupported legacy shape %T", name, legacy)
	}
}

func guessProvider(modelID string) string {
	lower := strings.ToLower(modelID)
	for _, h := range providerHints {
		if strings.Contains(lower, h.needle) {
			return h.provider
		}
	}
	return "unknown"
}

func coerceInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	}
	return 0, false
}

func ExtractModelsFromSharedConfig(doc map[string]any) (docs []CleanSpec, warnings map[string][]string, skipped map[string]string) {
	warnings = map[string][]string{}
	skipped = map[string]string{}

	rawList, ok := doc["shared_config"].([]any)
	if !ok {
		return nil, warnings, skipped
	}
	for _, entry := range rawList {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		modelsAny, ok := m["models"]
		if !ok {
			continue
		}
		modelsMap, ok := modelsAny.(map[string]any)
		if !ok {
			continue
		}
		for name, legacy := range modelsMap {
			doc, w, err := MigrateModel(legacy, name)
			if err != nil {
				skipped[name] = err.Error()
				continue
			}
			docs = append(docs, doc)
			if len(w) > 0 {
				warnings[name] = w
			}
		}
	}
	return docs, warnings, skipped
}
