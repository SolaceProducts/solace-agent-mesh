// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func probeAppConfig(t *testing.T, stored json.RawMessage) map[string]any {
	t.Helper()
	prompt := "You are a probe agent whose only job is to be deployed with no runtime knobs set."
	deployed, err := entplatform.NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfig(
		&store.Agent{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "probe", SystemPrompt: &prompt, Type: "standard", AdditionalConfigurations: stored},
		nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	apps, _ := deployed["apps"].([]map[string]any)
	if len(apps) == 0 {
		t.Fatalf("deployed config has no apps: %v", deployed)
	}
	appConfig, _ := apps[0]["app_config"].(map[string]any)
	delete(appConfig, "model_provider")
	return appConfig
}

func asDeployed(t *testing.T, appConfig map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(appConfig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func runtimeScalars(parsed *agent.AgentConfig) map[string]any {
	return map[string]any{
		"supports_streaming":                  parsed.SupportsStreaming,
		"inject_user_profile":                 parsed.InjectUserProfile,
		"artifact_handling_mode":              parsed.ArtifactHandlingMode,
		"max_llm_calls_per_task":              parsed.MaxLLMCalls,
		"enable_embed_resolution":             parsed.EmbedResolutionEnabled(),
		"enable_artifact_content_instruction": parsed.ArtifactContentInstructionEnabled(),
	}
}

var valueOtherThanTheDefault = map[string]any{
	"supports_streaming":                  true,
	"inject_user_profile":                 true,
	"artifact_handling_mode":              "reference",
	"max_llm_calls_per_task":              50,
	"enable_embed_resolution":             false,
	"enable_artifact_content_instruction": false,
}
