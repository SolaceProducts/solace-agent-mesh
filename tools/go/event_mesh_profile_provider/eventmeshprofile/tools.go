// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"context"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type EnrichParams struct {
	LookupValue string `json:"lookup_value" desc:"Resolved JWT claim value (e.g. lowercased email) identifying the user in the identity directory the configured event-mesh responder fronts."`
}

const enrichAuthenticatedUserInstructions = `Resolves an authenticated user's lookup value (typically their email) to a canonical Profile.

This tool is called by the platform identity service over the event mesh: it publishes the lookup value to the configured request_topic, awaits the correlated reply on response_topic_prefix/<request_id>, and applies the operator-configured field_mapping_config to shape the response into the canonical Profile (id, workEmail, displayName, country, employeeGroup, jobGrade, etc.).

Responder contract: an empty-body reply (or an empty JSON object {}) is treated as "no profile in the directory" — the tool returns no profile and the platform falls back accordingly. A non-empty reply that has no recognizable canonical Profile fields after field_mapping_config (e.g. a responder error envelope like {"error": "..."}) is treated as an error so a transient upstream failure is NOT cached as a permanent miss.`

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("enrich_authenticated_user",
			"Resolves an authenticated user's lookup value to a canonical Profile via an event-mesh request/reply.",
			enrichAuthenticatedUser,
			sdk.WithInstructions(enrichAuthenticatedUserInstructions),
			sdk.WithConfigSchema(configSchema...),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func enrichAuthenticatedUser(ctx context.Context, p EnrichParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	cfg, errMsg := LoadConfig(tc)
	if errMsg != "" {
		return sdk.Error(errMsg), nil
	}
	profile, err := NewProvider(cfg).Lookup(ctx, p.LookupValue)
	if err != nil {
		return sdk.Error("event-mesh profile lookup failed: " + err.Error()), nil
	}
	if profile == nil {
		return sdk.OK("No profile found for the supplied lookup value.",
			sdk.WithData(map[string]any{"profile": nil}),
		), nil
	}
	return sdk.OK("Profile resolved.",
		sdk.WithData(map[string]any{"profile": profile}),
	), nil
}
