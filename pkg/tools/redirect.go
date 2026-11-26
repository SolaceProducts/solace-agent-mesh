// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type RedirectUserTool struct {
	tool.ReadOnlyTool
	artifactToolBase
	defaultConfig map[string]any
}

func NewRedirectUserTool(defaultConfig map[string]any) *RedirectUserTool {
	return &RedirectUserTool{defaultConfig: defaultConfig}
}

func (t *RedirectUserTool) Name() string { return "redirect_user" }

func (t *RedirectUserTool) Description() string {
	return "Redirect the user to the SAM Builder page with context from the current conversation. " +
		"Use this when the user wants to create, build, or automate something that the Builder can help with."
}

func (t *RedirectUserTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"context": map[string]any{
				"type": "string",
				"description": "A detailed summary of the conversation context and user intent to carry over to the Builder. " +
					"Include what was discussed, what the user wants to build, and any relevant technical details " +
					"such as tools needed, data sources, integrations, or specific behaviors.",
			},
			"artifacts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Filenames of artifacts uploaded in this conversation that are relevant to what the user wants to build. These will be copied to the Builder session.",
			},
		},
		Required: []string{"context"},
	}
}

func (t *RedirectUserTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "type", typ: "string", description: "Always the literal \"redirect\"; marks the payload the web UI renders as a navigation button.", optional: true},
		resultField{name: "route", typ: "string", description: "Destination path in the Agent Mesh UI, from tool config, defaulting to /builder.", optional: true},
		resultField{name: "label", typ: "string", description: "Button text shown to the user, defaulting to \"Go to Builder\".", optional: true},
		resultField{name: "context", typ: "string", description: "The conversation summary carried to the destination page, verbatim from the context argument.", optional: true},
		resultField{name: "url_params", typ: "object", description: "Extra query parameters to append to the route, from tool config. Null when none are configured.", optional: true},
		resultField{name: "artifacts", typ: "array", description: "Filenames of this session's artifacts to copy into the destination session. Written only when at least one was supplied and the call has a session.", optional: true},
		resultField{name: "source_session_id", typ: "string", description: "Session the artifacts are copied from. Written together with artifacts.", optional: true},
	)
}

func (t *RedirectUserTool) Execute(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	ctxStr, _ := args["context"].(string)
	if ctxStr == "" {
		return tool.Error("context parameter is required"), nil
	}

	if toolCtx == nil || !toolCtx.GatewayCapabilities["web_ui_redirect"] {
		return tool.OK(
			"Building SAM components from this gateway isn't supported yet — please open the Solace Agent Mesh web UI and use the Builder to continue.",
		), nil
	}

	var artifacts []string
	if arr, ok := args["artifacts"].([]any); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				artifacts = append(artifacts, s)
			}
		}
	}

	cfg := t.resolveConfig(toolCtx)
	route, _ := cfg["route"].(string)
	label, _ := cfg["label"].(string)
	urlParams, _ := cfg["url_params"].(map[string]any)

	if route == "" {
		route = "/builder"
	}
	if label == "" {
		label = "Go to Builder"
	}

	requiredScope, _ := cfg["required_scope"].(string)
	if requiredScope == "" && route == "/builder" {
		requiredScope = rbac.ScopeBuilderUse
	}
	if requiredScope != "" && toolCtx.Identity != nil && !toolCtx.Identity.HasScope(requiredScope) {
		denied := "You don't currently have access to that area. Contact your administrator to request access."
		if requiredScope == rbac.ScopeBuilderUse {
			denied = "You don't currently have access to the Builder. Contact your administrator to request access."
		}
		return tool.OK(denied), nil
	}

	data := map[string]any{
		"type":       "redirect",
		"route":      route,
		"label":      label,
		"context":    ctxStr,
		"url_params": urlParams,
	}

	if len(artifacts) > 0 && toolCtx.SessionID != "" {
		data["artifacts"] = artifacts
		data["source_session_id"] = toolCtx.SessionID
	}

	return tool.OK(
		"I've provided a link to the Builder. Click the button below to continue there with the context from our conversation.",
		tool.WithData(data),
	), nil
}

func (t *RedirectUserTool) resolveConfig(toolCtx *tool.Context) map[string]any {
	merged := make(map[string]any)
	for k, v := range t.defaultConfig {
		merged[k] = v
	}
	if toolCtx != nil {
		for k, v := range toolCtx.ToolConfig {
			merged[k] = v
		}
	}
	return merged
}
