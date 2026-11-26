// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type RequestBuildActivateTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func (t *RequestBuildActivateTool) Name() string { return "request_build_activate" }

func (t *RequestBuildActivateTool) Description() string {
	return "Deploy the current build to the platform on the user's behalf — the equivalent of the user clicking the \"Build & Activate\" button. " +
		"Call this ONLY when the user asks (in plain language) to deploy, activate, or ship the build AND the build has already been generated and validated. " +
		"It deploys whatever components are saved but not yet live. " +
		"Do NOT call it to approve a plan whose configs have not been generated yet, and do NOT call it for a standalone skill or a standalone connector — skills and connectors are saved/published and ride along with agents, they are not deployed on their own. For a connector-only or skill-only build the config is saved automatically; just tell the user it's saved and to add it to an agent."
}

func (t *RequestBuildActivateTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object", Properties: map[string]any{}}
}

func (t *RequestBuildActivateTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "type", typ: "string", description: "Always the literal \"request_build_activate\" — the signal the Builder web UI intercepts to run its save-and-deploy flow.", optional: true},
	)
}

func (t *RequestBuildActivateTool) Execute(_ context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx == nil || !toolCtx.GatewayCapabilities["web_ui_redirect"] {
		return tool.OK(
			"Deploying a build is only available in the Solace Agent Mesh web UI's Builder.",
		), nil
	}

	return tool.OK(
		"Deploying your build now — I'll let you know if anything needs your attention.",
		tool.WithData(map[string]any{"type": "request_build_activate"}),
	), nil
}
