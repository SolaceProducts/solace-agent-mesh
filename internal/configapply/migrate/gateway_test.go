// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

const legacyGatewaySlackYAML = `
apps:
  - name: SlackBot__app
    app_module: solace_agent_mesh.gateway.generic.app
    app_config:
      namespace: prod
      default_agent_name: Orchestrator
      gateway_adapter: sam_slack_gateway_adapter.adapter.SlackAdapter
      adapter_config:
        slack_bot_token: "${SLACK_BOT_TOKEN}"
        slack_app_token: "${SLACK_APP_TOKEN}"
        feedback_enabled: false
      artifact_service:
        type: s3
      enable_embed_resolution: true
      gateway_artifact_content_limit_bytes: 10000000
      authorization_service:
        type: none
      identity_service:
        type: bamboohr
      trust_manager:
        enabled: false
`

func TestMigrateGateway_Slack(t *testing.T) {
	clean, warnings, err := MigrateGateway(parseDoc(t, legacyGatewaySlackYAML))
	if err != nil {
		t.Fatalf("MigrateGateway: %v", err)
	}
	if clean.Kind != "entrypoint" || clean.Name != "SlackBot" {
		t.Errorf("identity = %s/%s; want entrypoint/SlackBot", clean.Kind, clean.Name)
	}
	if clean.Description == "" {
		t.Errorf("description should be synthesised, got empty")
	}
	if got := clean.Spec["type"]; got != "slack" {
		t.Errorf("type = %v; want slack", got)
	}
	if _, leaked := clean.Spec["gatewayRuntimeId"]; leaked {
		t.Errorf("gatewayRuntimeId should not appear in migrated spec (legacy field, removed)")
	}

	values, ok := clean.Spec["values"].(map[string]any)
	if !ok {
		t.Fatalf("values is not a map: %T", clean.Spec["values"])
	}
	if values["slack_bot_token"] != "${SLACK_BOT_TOKEN}" {
		t.Errorf("slack_bot_token = %v", values["slack_bot_token"])
	}
	if _, ok := values["enable_embed_resolution"]; !ok {
		t.Errorf("expected enable_embed_resolution preserved under values")
	}

	hasDescriptionWarning := false
	hasNonStandardWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "synthesised") {
			hasDescriptionWarning = true
		}
		if strings.Contains(w, "non-standard gateway field") {
			hasNonStandardWarning = true
		}
	}
	if !hasDescriptionWarning {
		t.Errorf("expected description warning; got %v", warnings)
	}
	if !hasNonStandardWarning {
		t.Errorf("expected non-standard-field warning; got %v", warnings)
	}

	if _, err := translate.ForwardEntrypointV2(clean.Spec, translate.EntrypointV2Hooks{}); err != nil {
		t.Fatalf("ForwardEntrypointV2 on migrated spec: %v", err)
	}
}

func TestMigrateGateway_UnknownAdapterFallsBackToGeneric(t *testing.T) {
	src := `
apps:
  - name: ZBot__app
    app_module: solace_agent_mesh.gateway.generic.app
    app_config:
      gateway_adapter: my.custom.SomeAdapter
      adapter_config: {}
`
	clean, warnings, err := MigrateGateway(parseDoc(t, src))
	if err != nil {
		t.Fatalf("MigrateGateway: %v", err)
	}
	if clean.Spec["type"] != "generic" {
		t.Errorf("type = %v; want generic", clean.Spec["type"])
	}
	hasUnknownWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown gateway_adapter") {
			hasUnknownWarning = true
		}
	}
	if !hasUnknownWarning {
		t.Errorf("expected unknown-adapter warning; got %v", warnings)
	}
}

func TestMigrateGateway_NoAppsErrors(t *testing.T) {
	src := `apps: []`
	if _, _, err := MigrateGateway(parseDoc(t, src)); err == nil {
		t.Fatalf("expected error for empty apps list")
	}
}
