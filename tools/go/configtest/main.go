// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

var configTestKeys = []string{
	"api_base",
	"api_key",
	"verbose",
	"extra_field",
}

type EchoParams struct {
	Message string `json:"message" desc:"Arbitrary text to echo back in the response."`
}

type AddNumbersParams struct {
	A float64 `json:"a" desc:"First number."`
	B float64 `json:"b" desc:"Second number."`
}

func main() {
	sdk.Run(
		sdk.NewTool(
			"echo",
			"Test-only echo tool: returns its input message, the merged tool_config, and execution-environment metadata. Used by configapply runtime tests.",
			echo,
			sdk.WithConfigSchema(
				sdk.ConfigSchemaField{
					Key:         "api_base",
					Type:        "string",
					Description: "Base URL the tool would call. Non-secret. Tests assert this round-trips through spec.config.",
				},
				sdk.ConfigSchemaField{
					Key:         "api_key",
					Type:        "string",
					Description: "Credential. Secret so the platform redacts it on GET.",
					Secret:      true,
				},
				sdk.ConfigSchemaField{
					Key:         "verbose",
					Type:        "boolean",
					Description: "Sample boolean field so tests exercise type coercion through the apply path.",
				},
				sdk.ConfigSchemaField{
					Key:         "extra_field",
					Type:        "string",
					Description: "Free-form string field used by tests that need an additional non-secret value.",
				},
			),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool(
			"add_numbers",
			"Test-only second tool: returns the sum of two numbers. Lets tests exercise the multi-tool STR manifest expansion path.",
			addNumbers,
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	)
}

func addNumbers(_ context.Context, p AddNumbersParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	sum := p.A + p.B
	return &sdk.Result{
		Status:  "success",
		Message: fmt.Sprintf("%v + %v = %v", p.A, p.B, sum),
		Data: map[string]any{
			"sum": sum,
			"a":   p.A,
			"b":   p.B,
		},
	}, nil
}

func echo(_ context.Context, p EchoParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.Message == "" {
		return nil, fmt.Errorf("message is required")
	}
	cfg := make(map[string]any, len(configTestKeys))
	for _, k := range configTestKeys {
		if v, ok := tc.GetConfig(k); ok {
			cfg[k] = v
		}
	}
	return &sdk.Result{
		Status:  "success",
		Message: "echoed: " + p.Message,
		Data: map[string]any{
			"echoed_message": p.Message,
			"config":         cfg,
			"user_id":        tc.UserID,
			"session_id":     tc.SessionID,
			"app_name":       tc.AppName,
		},
	}, nil
}
