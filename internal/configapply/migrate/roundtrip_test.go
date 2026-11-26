// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"maps"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

func TestRoundTrip_MigratedYamlIsLoadableByResolver(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, src, "agents/employee.yaml", legacyAgentYAML)
	writeFile(t, src, "gateways/slack.yaml", legacyGatewaySlackYAML)
	writeFile(t, src, "workflows/wf.yaml", legacyWorkflowYAML)
	writeFile(t, src, "shared_config.yaml", `
shared_config:
  - models:
      planning:
        model: claude-opus-4-7
        api_base: ${A}
        api_key: ${K}
        max_tokens: 8192
        max_input_tokens: 100000
`)

	report, err := Run(src, dst, Options{Force: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.HasFailures() {
		t.Fatalf("migrate errored: %+v", report.Errored)
	}

	m := &manifest.Manifest{
		Variables: map[string]string{
			"A":               "https://example/api",
			"K":               "stub-key",
			"SLACK_APP_TOKEN": "xapp-stub",
			"SLACK_BOT_TOKEN": "xoxb-stub",
		},
		Resources: map[string][]manifest.ResourceEntry{
			"agents":      {{Name: "Employee Agent"}},
			"entrypoints": {{Name: "SlackBot"}},
			"workflows":   {{Name: "SFDC Case Advisor"}},
			"models":      {{Name: "planning"}},
		},
	}

	t.Run("agents", func(t *testing.T) {
		got, err := resolve.ResolveAgents(dst, m)
		if err != nil {
			t.Fatalf("resolve agents: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d resolved agents; want 1", len(got))
		}
		if _, err := translate.ForwardAgentV2(got[0].Spec, translate.AgentV2Hooks{}); err != nil {
			t.Fatalf("ForwardAgentV2: %v", err)
		}
	})

	t.Run("gateways", func(t *testing.T) {
		got, err := resolve.ResolveGateways(dst, m)
		if err != nil {
			t.Fatalf("resolve gateways: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d resolved gateways; want 1", len(got))
		}
		if _, err := translate.ForwardEntrypointV2(got[0].Spec, translate.EntrypointV2Hooks{}); err != nil {
			t.Fatalf("ForwardEntrypointV2: %v", err)
		}
	})

	t.Run("workflows", func(t *testing.T) {
		got, err := resolve.ResolveWorkflows(dst, m)
		if err != nil {
			t.Fatalf("resolve workflows: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d resolved workflows; want 1", len(got))
		}
		flat := map[string]any{
			"name":        got[0].Name,
			"description": got[0].Description,
		}
		maps.Copy(flat, got[0].Spec)
		if _, err := translate.ForwardWorkflowV2(flat, translate.WorkflowV2Hooks{}); err != nil {
			t.Fatalf("ForwardWorkflowV2: %v", err)
		}
	})

	t.Run("models", func(t *testing.T) {
		got, err := resolve.ResolveModels(dst, m)
		if err != nil {
			t.Fatalf("resolve models: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d resolved models; want 1", len(got))
		}
		spec := map[string]any{}
		maps.Copy(spec, got[0].Spec)
		spec["alias"] = got[0].Name
		if got[0].Description != "" {
			spec["description"] = got[0].Description
		}
		if _, err := translate.ForwardModelV2(spec, translate.ModelV2Hooks{}); err != nil {
			t.Fatalf("ForwardModelV2: %v", err)
		}
	})
}
