// Test Go tool fixture for STR config-schema integration tests.
//
// Declares a real config_schema in its --schema output and echoes the
// received tool_config map back in its result. Used to verify that values
// written into toolset_configs / agent_toolset_configs flow all the way
// into a tool process at invocation time.
package main

import (
	"context"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type EchoConfigParams struct {
	Text string `json:"text" desc:"Arbitrary text the caller wants echoed alongside the config"`
}

func echoConfig(_ context.Context, p EchoConfigParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	cfg := map[string]any{}
	for _, key := range []string{"api_key", "region", "endpoint", "verbose", "timeout"} {
		if v, ok := tc.GetConfig(key); ok {
			cfg[key] = v
		}
	}
	return sdk.OK("echoed", sdk.WithData(map[string]any{
		"text":        p.Text,
		"config_seen": cfg,
	})), nil
}

func main() {
	sdk.Run(
		sdk.NewTool("echo_config", "Echoes the received tool_config map back to the caller", echoConfig,
			sdk.WithConfigSchema(
				sdk.ConfigSchemaField{
					Key:         "api_key",
					Type:        "string",
					Description: "Required secret for the echo tool",
					Required:    true,
					Secret:      true,
				},
				sdk.ConfigSchemaField{
					Key:         "region",
					Type:        "string",
					Description: "Optional deployment region",
				},
				sdk.ConfigSchemaField{
					Key:         "endpoint",
					Type:        "string",
					Description: "Optional endpoint override",
					Default:     "https://api.example.com",
				},
			),
		),
	)
}
