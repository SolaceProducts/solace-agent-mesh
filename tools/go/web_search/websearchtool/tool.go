// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearchtool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

var configFields = []sdk.ConfigSchemaField{
	{Key: websearch.CfgToolName, Type: "string", Description: "The name of the tool as the LLM invokes it."},
	{Key: websearch.CfgToolDescription, Type: "string", Description: "What the search covers, shown to the LLM."},
	{Key: "provider", Type: "string", Required: true, Options: []any{"tavily", "exa"}, Description: "The search provider."},
	{Key: "api_key", Type: "string", Required: true, Secret: true, Description: "The provider's API key."},
	{Key: "api_base", Type: "string", Required: true, Description: "The provider's API base URL."},
	{Key: "safe_search", Type: "boolean", Description: "Filter explicit results."},
	{Key: "max_results", Type: "integer", Description: "Results per search when the LLM does not ask for a number."},
}

type Params struct {
	Query          string    `json:"query" desc:"The search query"`
	MaxResults     *int      `json:"max_results,omitempty" desc:"Maximum number of results"`
	Recency        *string   `json:"recency,omitempty" desc:"Only return results published within this window"`
	SearchType     *string   `json:"search_type,omitempty" desc:"Set to 'image' for image search"`
	IncludeDomains *[]string `json:"include_domains,omitempty" desc:"Only return results from these domains"`
	ExcludeDomains *[]string `json:"exclude_domains,omitempty" desc:"Never return results from these domains"`
	DateRestrict   *string   `json:"date_restrict,omitempty" desc:"Not supported; use recency"`
	SafeSearch     any       `json:"safe_search,omitempty" desc:"Not supported; set by the operator"`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool(websearch.ToolName, websearch.Description(nil), search,
			sdk.WithConfigSchema(configFields...),
			sdk.WithDynamicSchema(dynamicSchema),
			sdk.WithTimeout(int(websearch.MaxCallDuration().Seconds())+10),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	}
}

func dynamicSchema(cfg map[string]any) *sdk.SchemaOverride {
	params := websearch.Parameters(cfg)
	return &sdk.SchemaOverride{
		Name:        websearch.Name(cfg),
		Description: websearch.Description(cfg),
		Parameters:  &sdk.JSONSchema{Type: params.Type, Properties: params.Properties, Required: params.Required},
	}
}

func search(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	args, err := argsOf(p)
	if err != nil {
		return nil, err
	}
	turn, numbered := tc.SearchTurn()
	outcome := websearch.Search(ctx, configOf(tc), args, websearch.Hooks{
		Searching: func(query string) { _ = tc.SendStatus(fmt.Sprintf("Searching the web for: %s", query)) },
		Turn:      func() int { return turn },
		QuotaWait: func(wait time.Duration) {
			_ = tc.SendStatus(quotaWaitStatus(wait))
		},
	})
	return resultOf(outcome, numbered), nil
}

func quotaWaitStatus(wait time.Duration) string {
	seconds := max(int(wait.Round(time.Second).Seconds()), 1)
	if seconds == 1 {
		return "The search provider is rate limiting; retrying in 1 second"
	}
	return fmt.Sprintf("The search provider is rate limiting; retrying in %d seconds", seconds)
}

func argsOf(p Params) (map[string]any, error) {
	encoded, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encoding web_search arguments: %w", err)
	}
	var args map[string]any
	if err := json.Unmarshal(encoded, &args); err != nil {
		return nil, fmt.Errorf("decoding web_search arguments: %w", err)
	}
	return args, nil
}

func configOf(tc *sdk.ToolContext) map[string]any {
	cfg := make(map[string]any, len(configFields))
	for _, f := range configFields {
		if v, ok := tc.GetConfig(f.Key); ok {
			cfg[f.Key] = v
		}
	}
	return cfg
}

func resultOf(outcome websearch.Outcome, numbered bool) *sdk.Result {
	r := outcome.Result
	if r.ValidationError != "" {
		return sdk.Error(r.ValidationError)
	}
	var opts []sdk.ResultOption
	if len(r.Data) > 0 {
		opts = append(opts, sdk.WithData(r.Data))
	}
	if r.ErrorCode != "" {
		opts = append(opts, sdk.WithErrorCode(r.ErrorCode))
	}
	if numbered && outcome.Citations != nil {
		opts = append(opts, sdk.WithSignals(sdk.Signal{Type: sdk.SignalRAGInfoUpdate, Data: outcome.Citations}))
	}
	if r.Status == tool.StatusSuccess {
		return sdk.OK(r.Message, opts...)
	}
	return sdk.Error(r.Message, opts...)
}
