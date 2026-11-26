// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestRenderAgentUpdateVerbose_AdditionalConfigurationsPathLevelDiff(t *testing.T) {
	op := agentOp{
		Name: "orchestrator",
		ID:   "id-1",
		Create: wire.CreateAgentRequest{
			Name:                     "orchestrator",
			Description:              "desc",
			AdditionalConfigurations: map[string]any{"agent_card_publishing": map[string]any{"interval_seconds": 30}, "supports_streaming": true},
		},
		Actual: &wire.AgentResponse{
			Name:                     "orchestrator",
			Description:              "desc",
			AdditionalConfigurations: map[string]any{"agent_card_publishing": map[string]any{"interval_seconds": 5}, "supports_streaming": true},
		},
	}
	var buf strings.Builder
	renderAgentUpdateVerbose(&buf, op, FormatOptions{NoColor: true})
	out := buf.String()

	pathLine := "additionalConfigurations.agent_card_publishing.interval_seconds:"
	if !strings.Contains(out, pathLine) {
		t.Errorf("verbose output should name nested path %q\n--- output ---\n%s", pathLine, out)
	}
	for _, lit := range []string{" 5 ", " 30"} {
		if !strings.Contains(out, lit) {
			t.Errorf("verbose output missing literal %q (expected old/new pair)\n--- output ---\n%s", lit, out)
		}
	}
	if strings.Contains(out, "supports_streaming") {
		t.Errorf("verbose output incorrectly mentions unchanged sibling supports_streaming\n--- output ---\n%s", out)
	}
}

func TestRenderAgentUpdateVerbose_SliceAndSliceMapFieldsRendered(t *testing.T) {
	op := agentOp{
		Name: "agent",
		ID:   "id",
		Create: wire.CreateAgentRequest{
			Name:                 "agent",
			Description:          "desc",
			Toolsets:             []string{"a", "b"},
			ToolsetExcludedTools: map[string][]string{"a": {"x"}},
		},
		Actual: &wire.AgentResponse{
			Name:                 "agent",
			Description:          "desc",
			Toolsets:             []string{"a"},
			ToolsetExcludedTools: map[string][]string{"a": {"y"}},
		},
	}
	var buf strings.Builder
	renderAgentUpdateVerbose(&buf, op, FormatOptions{NoColor: true})
	out := buf.String()

	for _, want := range []string{"toolsets:", "toolsetExcludedTools:"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderAgentUpdateVerbose_NoActualNoOp(t *testing.T) {
	op := agentOp{Name: "orchestrator"}
	var buf strings.Builder
	renderAgentUpdateVerbose(&buf, op, FormatOptions{NoColor: true})
	if got := buf.String(); got != "" {
		t.Errorf("expected empty output when Actual is nil, got %q", got)
	}
}

func TestRenderModelUpdateVerbose_ModelType(t *testing.T) {
	cases := []struct {
		name        string
		desired     string
		actual      string
		wantMention bool
	}{
		{name: "omitted desired equals chat actual", desired: "", actual: "chat", wantMention: false},
		{name: "omitted desired equals omitted actual", desired: "", actual: "", wantMention: false},
		{name: "decision desired differs from chat actual", desired: "decision", actual: "chat", wantMention: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := modelOp{
				Create: wire.CreateModelRequest{
					Alias:     "judge",
					Provider:  "anthropic",
					ModelName: "claude",
					ModelType: tc.desired,
				},
				Actual: &wire.ModelResponse{
					Alias:     "judge",
					Provider:  sp("anthropic"),
					ModelName: sp("claude"),
					ModelType: tc.actual,
				},
			}
			var buf strings.Builder
			renderModelUpdateVerbose(&buf, op, FormatOptions{NoColor: true})
			out := buf.String()

			if got := strings.Contains(out, "modelType:"); got != tc.wantMention {
				t.Errorf("modelType diff line present = %v, want %v\n--- output ---\n%s", got, tc.wantMention, out)
			}
			if tc.wantMention && !strings.Contains(out, "decision") {
				t.Errorf("modelType diff should name the desired value decision\n--- output ---\n%s", out)
			}
		})
	}
}

const verboseSecretValue = "s3cr3t-value-do-not-print"

var modelAuthTypesForVerbose = []string{
	"none", "apikey", "oauth2", "bearer", "basic", "aws_iam", "gcp_service_account", "azure_workload_identity",
}

var modelSecretKeysForVerbose = []string{
	"api_key", "client_secret", "oauth_client_secret", "token", "password",
	"aws_secret_access_key", "aws_session_token", "service_account_json", "vertex_credentials",
}

func renderModelAuthDiff(t *testing.T, actual, desired map[string]any) string {
	t.Helper()
	op := modelOp{
		Create: wire.CreateModelRequest{
			Alias:      "m",
			Provider:   "vertex_ai",
			ModelName:  "gemini",
			AuthConfig: desired,
		},
		Actual: &wire.ModelResponse{
			Alias:      "m",
			Provider:   sp("vertex_ai"),
			ModelName:  sp("gemini"),
			AuthConfig: actual,
		},
	}
	var buf strings.Builder
	renderModelUpdateVerbose(&buf, op, FormatOptions{NoColor: true})
	return buf.String()
}

func assertSecretMasked(t *testing.T, out, key string) {
	t.Helper()
	if strings.Contains(out, verboseSecretValue) {
		t.Errorf("verbose plan output leaked the secret\n--- output ---\n%s", out)
	}
	if want := "authConfig." + key + ": (changed)"; !strings.Contains(out, want) {
		t.Errorf("expected %q in output\n--- output ---\n%s", want, out)
	}
}

func TestRenderModelUpdateVerbose_EveryAuthTransitionMasksSecrets(t *testing.T) {
	for _, fromType := range modelAuthTypesForVerbose {
		for _, toType := range modelAuthTypesForVerbose {
			for _, key := range modelSecretKeysForVerbose {
				t.Run(fromType+"_to_"+toType+"_"+key, func(t *testing.T) {
					actual := map[string]any{"type": fromType}
					out := renderModelAuthDiff(t, actual, map[string]any{"type": toType, key: verboseSecretValue})
					assertSecretMasked(t, out, key)
				})
			}
		}
	}
}

func TestRenderModelUpdateVerbose_SecretChangedOnSameType(t *testing.T) {
	for _, key := range modelSecretKeysForVerbose {
		t.Run(key, func(t *testing.T) {
			actual := map[string]any{"type": "apikey", key: redactedSentinel}
			out := renderModelAuthDiff(t, actual, map[string]any{"type": "apikey", key: verboseSecretValue})
			assertSecretMasked(t, out, key)
		})
	}
}

func TestRenderModelUpdateVerbose_StraySecretOnNone(t *testing.T) {
	out := renderModelAuthDiff(t, map[string]any{"type": "none"}, map[string]any{"type": "none", "api_key": verboseSecretValue})
	assertSecretMasked(t, out, "api_key")
}

func TestRenderModelUpdateVerbose_NilAndEmptyActualAuthConfig(t *testing.T) {
	desired := map[string]any{"type": "gcp_service_account", "service_account_json": verboseSecretValue}
	for name, actual := range map[string]map[string]any{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			assertSecretMasked(t, renderModelAuthDiff(t, actual, desired), "service_account_json")
		})
	}
}

func TestRenderModelUpdateVerbose_MissingDesiredType(t *testing.T) {
	out := renderModelAuthDiff(t, map[string]any{"type": "none"}, map[string]any{"service_account_json": verboseSecretValue})
	assertSecretMasked(t, out, "service_account_json")
}

func TestRenderModelUpdateVerbose_UnknownTypeTransitions(t *testing.T) {
	t.Run("known to unknown", func(t *testing.T) {
		out := renderModelAuthDiff(t,
			map[string]any{"type": "apikey", "api_key": redactedSentinel},
			map[string]any{"type": "future_auth", "signing_key": verboseSecretValue})
		assertSecretMasked(t, out, "signing_key")
	})
	t.Run("unknown to known", func(t *testing.T) {
		out := renderModelAuthDiff(t,
			map[string]any{"type": "future_auth", "signing_key": verboseSecretValue},
			map[string]any{"type": "none"})
		assertSecretMasked(t, out, "signing_key")
	})
}

func TestRenderModelUpdateVerbose_NestedSecretValueMasked(t *testing.T) {
	t.Run("nested map on both sides", func(t *testing.T) {
		out := renderModelAuthDiff(t,
			map[string]any{"type": "gcp_service_account", "vertex_credentials": map[string]any{"private_key": "old-" + verboseSecretValue}},
			map[string]any{"type": "gcp_service_account", "vertex_credentials": map[string]any{"private_key": verboseSecretValue}})
		if strings.Contains(out, verboseSecretValue) {
			t.Errorf("verbose plan output leaked the secret\n--- output ---\n%s", out)
		}
		assertSecretMasked(t, out, "vertex_credentials.private_key")
	})
	t.Run("nested map under a visible key", func(t *testing.T) {
		out := renderModelAuthDiff(t,
			map[string]any{"type": "oauth2"},
			map[string]any{"type": "oauth2", "scope": map[string]any{"k": verboseSecretValue}})
		if strings.Contains(out, verboseSecretValue) {
			t.Errorf("verbose plan output leaked the secret\n--- output ---\n%s", out)
		}
		if !strings.Contains(out, "authConfig.scope: (changed)") {
			t.Errorf("expected masked scope line\n--- output ---\n%s", out)
		}
	})
}

func TestRenderModelUpdateVerbose_IdentifiersStayVisible(t *testing.T) {
	out := renderModelAuthDiff(t,
		map[string]any{"type": "oauth2", "client_id": "old-client", "client_secret": redactedSentinel},
		map[string]any{"type": "oauth2", "client_id": "new-client", "client_secret": verboseSecretValue})
	if !strings.Contains(out, "authConfig.client_id: old-client  →  new-client") {
		t.Errorf("client_id should print as a literal diff\n--- output ---\n%s", out)
	}
	assertSecretMasked(t, out, "client_secret")
}

func TestRenderModelUpdateVerbose_AuthTypeSwitchMasksNewSecret(t *testing.T) {
	const clientSecret = "new-client-secret-value"
	op := modelOp{
		Create: wire.CreateModelRequest{
			Alias:      "switching",
			Provider:   "openai",
			ModelName:  "gpt-4o",
			AuthConfig: map[string]any{"type": "oauth2", "client_id": "marker-client", "client_secret": clientSecret},
		},
		Actual: &wire.ModelResponse{
			Alias:      "switching",
			Provider:   sp("openai"),
			ModelName:  sp("gpt-4o"),
			AuthConfig: map[string]any{"type": "apikey", "api_key": platform.RedactedSentinel},
		},
	}
	var buf strings.Builder
	renderModelUpdateVerbose(&buf, op, FormatOptions{NoColor: true})
	out := buf.String()

	if strings.Contains(out, clientSecret) {
		t.Errorf("plan printed the client_secret first set by the auth-type switch\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "authConfig.client_secret: (changed)") {
		t.Errorf("plan should report client_secret as (changed)\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "marker-client") {
		t.Errorf("plan should still show the non-secret client_id\n--- output ---\n%s", out)
	}
}
