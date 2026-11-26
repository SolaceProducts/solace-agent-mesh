// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const validEntrypointConfigYAML = `apps:
  - name: order_events_entrypoint
    app_config:
      event_mesh_broker_config:
        broker_url: "<<__SAM_REQUIRED__>>"
        broker_vpn: "<<__SAM_REQUIRED__>>"
        broker_username: "<<__SAM_REQUIRED__>>"
        broker_password: "<<__SAM_REQUIRED__>>"
      event_handlers:
        - name: new_orders
          subscriptions:
            - topic: "orders/created/>"
              qos: 1
          input_expression: "input.payload:"
          target_agent_name: OrderProcessor
`

const entrypointManifestYAML = `name: test-build
components:
  - filename: order_events_entrypoint.yaml
    type: gateway
    gateway_type: event_mesh
    name: order_events_entrypoint
    status: draft
    origin: session
`

func TestValidateComponentConfig_NamesAnEntrypointAnEntrypoint(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, entrypointManifestYAML, SAMBuildManifestMIMEType)

	res := validateConfigIn(t, toolCtx, validEntrypointConfigYAML, SAMGatewayConfigMIMEType)

	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %q: %s", res.Status, res.Message)
	}
	if !strings.HasPrefix(res.Message, "Configuration is valid (entrypoint)") {
		t.Errorf("message = %q, want it to name an entrypoint", res.Message)
	}
	if res.Data["component_type"] != "gateway" {
		t.Errorf("component_type = %v, want the wire value gateway unchanged", res.Data["component_type"])
	}
}

func TestPlatformTools_FailureMessagesSayEntrypointForTheGatewayType(t *testing.T) {
	brokenQuery := &mockControlQuerier{err: errors.New("boom")}
	badJSON := &mockControlQuerier{responses: map[string]json.RawMessage{"gateways": json.RawMessage(`not json`)}}
	tests := []struct {
		name string
		run  func() (*tool.Result, error)
		want string
	}{
		{"list query fails", func() (*tool.Result, error) {
			return (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "gateway"}, &tool.Context{ControlQuerier: brokenQuery})
		}, "Failed to query entrypoint"},
		{"list response is not JSON", func() (*tool.Result, error) {
			return (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "gateway"}, &tool.Context{ControlQuerier: badJSON})
		}, "Failed to parse entrypoint response"},
		{"get-config query fails", func() (*tool.Result, error) {
			return (&GetComponentConfigTool{}).Execute(t.Context(), map[string]any{"component_type": "gateway", "component_id": "g1"}, &tool.Context{ControlQuerier: brokenQuery})
		}, "Failed to retrieve entrypoint g1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.run()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(res.Message, tt.want) {
				t.Errorf("message = %q, want prefix %q", res.Message, tt.want)
			}
		})
	}
}

func TestFormatValidationResult_NamesATypeTheUserKnows(t *testing.T) {
	tests := []struct {
		componentType string
		want          string
	}{
		{"gateway", "Configuration is valid (entrypoint)"},
		{"skill", "Configuration is valid (skill)"},
	}
	for _, tt := range tests {
		t.Run(tt.componentType, func(t *testing.T) {
			res, err := (&ValidateComponentConfigTool{}).formatValidationResult(&schema.ValidationResult{Valid: true, ComponentType: tt.componentType}, nil, "config", "")
			if err != nil {
				t.Fatal(err)
			}
			if res.Message != tt.want {
				t.Errorf("message = %q, want %q", res.Message, tt.want)
			}
			if res.Data["component_type"] != tt.componentType {
				t.Errorf("component_type = %v, want %q", res.Data["component_type"], tt.componentType)
			}
		})
	}
}
