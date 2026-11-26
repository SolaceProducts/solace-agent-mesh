// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

// config-driven demonstrates WithConfigSchema for operator-supplied credentials
// and WithDynamicSchema for adjusting the tool description based on config.
//
// Operators declare tool_config in the agent YAML:
//
//	toolsetConfigs:
//	  - toolsetName: my-tools
//	    configValues:
//	      api_base: "https://api.example.com"
//	      api_key:  "${MY_API_KEY}"
//	      model:    "gpt-4o"
//
// The platform validates these against the ConfigSchemaField declarations below
// and makes them available via tc.GetConfig / tc.GetConfigString at runtime.
package main

import (
	"context"
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

// defaultModel is used when the operator leaves the "model" config unset.
const defaultModel = "gpt-4o-mini"

// configFields is the operator-facing config schema. Declaring it once avoids
// repetition between WithConfigSchema and the handler's GetConfigString calls.
var configFields = []sdk.ConfigSchemaField{
	{
		Key:         "api_base",
		Type:        "string",
		Description: "Base URL of the upstream API",
		Required:    true,
	},
	{
		Key:         "api_key",
		Type:        "string",
		Description: "API key for the upstream service",
		Required:    true,
		Secret:      true,
	},
	{
		Key:         "model",
		Type:        "string",
		Description: "Model identifier to use (e.g. gpt-4o)",
		Default:     defaultModel,
	},
}

// QueryParams is the LLM-facing input.
type QueryParams struct {
	Prompt string `json:"prompt" desc:"The question or instruction to send upstream"`
}

func query(_ context.Context, p QueryParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	apiBase := tc.GetConfigString("api_base", "")
	apiKey := tc.GetConfigString("api_key", "")
	model := tc.GetConfigString("model", defaultModel)

	if apiBase == "" || apiKey == "" {
		return sdk.Error("api_base and api_key must be set in tool_config"), nil
	}

	_ = tc.SendStatus(fmt.Sprintf("sending prompt to %s using model %s…", apiBase, model))

	// In a real tool this would make an HTTP request. Here we just echo
	// the resolved config so tests can verify the round-trip.
	return sdk.OK("prompt dispatched", sdk.WithData(map[string]any{
		"api_base": apiBase,
		"model":    model,
		"prompt":   p.Prompt,
	})), nil
}

// dynamicSchema returns a description that includes the configured model so
// the LLM knows which model it is talking to without reading config directly.
func dynamicSchema(config map[string]any) *sdk.SchemaOverride {
	model, _ := config["model"].(string)
	if model == "" {
		model = defaultModel
	}
	return &sdk.SchemaOverride{
		Description: fmt.Sprintf("Send a prompt to the upstream API (model: %s)", model),
	}
}

func main() {
	sdk.Run(sdk.NewTool(
		"upstream_query",
		"Send a prompt to the configured upstream API",
		query,
		sdk.WithConfigSchema(configFields...),
		sdk.WithDynamicSchema(dynamicSchema),
		sdk.WithInstructions("Use this tool when the user asks a question that requires calling an external API."),
	))
}
