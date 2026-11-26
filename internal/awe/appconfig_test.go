// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestParseAgentConfigDefaultInputOutputModes(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"agent_card": map[string]any{
			"defaultInputModes":  []any{"text", "file"},
			"defaultOutputModes": []any{"text", "artifact"},
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(ac.DefaultInputModes) != 2 || ac.DefaultInputModes[0] != "text" || ac.DefaultInputModes[1] != "file" {
		t.Errorf("DefaultInputModes = %v, want [text file]", ac.DefaultInputModes)
	}
	if len(ac.DefaultOutputModes) != 2 || ac.DefaultOutputModes[0] != "text" || ac.DefaultOutputModes[1] != "artifact" {
		t.Errorf("DefaultOutputModes = %v, want [text artifact]", ac.DefaultOutputModes)
	}
}

func TestParseAgentConfigDeploymentID(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"deployment": map[string]any{
			"id": "deploy-abc-123",
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.DeploymentID != "deploy-abc-123" {
		t.Errorf("DeploymentID = %q, want deploy-abc-123", ac.DeploymentID)
	}

	cfg = config.FromMap(map[string]any{"agent_name": "test-agent"})
	ac, err = ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.DeploymentID != "" {
		t.Errorf("DeploymentID = %q, want empty", ac.DeploymentID)
	}
}

func TestParseAgentConfig_RetiredMaxConcurrentTasksWarns(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	cfg := config.FromMap(map[string]any{
		"agent_name":           "legacy-agent",
		"max_concurrent_tasks": 4,
	})
	if _, err := ParseAgentConfig(cfg); err != nil {
		t.Fatalf("a config with the retired key must still parse: %v", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "max_concurrent_tasks is ignored") {
		t.Fatalf("expected a WARN naming the ignored key; logs:\n%s", logs)
	}
}

func TestParseAgentConfigMaxSubTaskFanout(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":         "test-agent",
		"max_subtask_fanout": 4,
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.MaxSubTaskFanout != 4 {
		t.Errorf("MaxSubTaskFanout = %d, want 4", ac.MaxSubTaskFanout)
	}

	cfg = config.FromMap(map[string]any{"agent_name": "test-agent"})
	ac, err = ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.MaxSubTaskFanout != 16 {
		t.Errorf("MaxSubTaskFanout = %d, want 16 (default)", ac.MaxSubTaskFanout)
	}
}

func TestParseAgentConfigMaxMessageRedelivery(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":             "test-agent",
		"max_message_redelivery": 5,
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.MaxMessageRedelivery != 5 {
		t.Errorf("MaxMessageRedelivery = %d, want 5", ac.MaxMessageRedelivery)
	}

	cfg = config.FromMap(map[string]any{"agent_name": "test-agent"})
	ac, err = ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.MaxMessageRedelivery != 1 {
		t.Errorf("MaxMessageRedelivery = %d, want 1 (default)", ac.MaxMessageRedelivery)
	}

	cfg = config.FromMap(map[string]any{
		"agent_name":             "test-agent",
		"max_message_redelivery": 0,
	})
	ac, err = ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.MaxMessageRedelivery != 0 {
		t.Errorf("MaxMessageRedelivery = %d, want 0 (explicit opt-out)", ac.MaxMessageRedelivery)
	}
}

func TestParseAgentConfigRespectMessageTTL(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":          "test-agent",
		"respect_message_ttl": false,
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.RespectMessageTTL {
		t.Error("RespectMessageTTL = true, want false")
	}

	cfg = config.FromMap(map[string]any{"agent_name": "test-agent"})
	ac, err = ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !ac.RespectMessageTTL {
		t.Error("RespectMessageTTL = false, want true (default)")
	}

	cfg = config.FromMap(map[string]any{
		"agent_name":          "test-agent",
		"respect_message_ttl": true,
	})
	ac, err = ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !ac.RespectMessageTTL {
		t.Error("RespectMessageTTL = false, want true (explicit)")
	}
}

func TestParseAgentConfigDocumentationURL(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"agent_card": map[string]any{
			"documentationUrl": "https://example.com/docs",
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.DocumentationURL != "https://example.com/docs" {
		t.Errorf("DocumentationURL = %q, want %q", ac.DocumentationURL, "https://example.com/docs")
	}
}

func TestParseAgentConfigProvider(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"agent_card": map[string]any{
			"provider": map[string]any{
				"organization": "Test Org",
				"url":          "https://example.com",
			},
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.Provider == nil {
		t.Fatal("Provider should not be nil")
	}
	if ac.Provider.Organization != "Test Org" {
		t.Errorf("Provider.Organization = %q, want %q", ac.Provider.Organization, "Test Org")
	}
	if ac.Provider.URL != "https://example.com" {
		t.Errorf("Provider.URL = %q, want %q", ac.Provider.URL, "https://example.com")
	}
}

func TestParseAgentConfigPriority(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"agent_card": map[string]any{
			"priority": 100,
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.Priority != 100 {
		t.Errorf("Priority = %d, want 100", ac.Priority)
	}
}

func TestParseAgentConfigPriorityDefault(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.Priority != 0 {
		t.Errorf("Priority = %d, want 0 (default)", ac.Priority)
	}
}

func TestParseAgentConfigProviderMissing(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.Provider != nil {
		t.Errorf("Provider should be nil when not configured, got %+v", ac.Provider)
	}
}

func TestParseAgentConfigDiscoveryEnabled(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantNil bool
		wantVal bool
	}{
		{
			name:    "not set defaults to nil",
			cfg:     map[string]any{"agent_name": "test"},
			wantNil: true,
		},
		{
			name: "explicitly true",
			cfg: map[string]any{
				"agent_name":      "test",
				"agent_discovery": map[string]any{"enabled": true},
			},
			wantVal: true,
		},
		{
			name: "explicitly false",
			cfg: map[string]any{
				"agent_name":      "test",
				"agent_discovery": map[string]any{"enabled": false},
			},
			wantVal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac, err := ParseAgentConfig(config.FromMap(tt.cfg))
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNil {
				if ac.DiscoveryEnabled != nil {
					t.Errorf("DiscoveryEnabled should be nil, got %v", *ac.DiscoveryEnabled)
				}
			} else {
				if ac.DiscoveryEnabled == nil {
					t.Fatal("DiscoveryEnabled should not be nil")
				}
				if *ac.DiscoveryEnabled != tt.wantVal {
					t.Errorf("DiscoveryEnabled = %v, want %v", *ac.DiscoveryEnabled, tt.wantVal)
				}
			}
		})
	}
}

func TestParseAgentConfigCardPublishIntervalDefault(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.CardPublishIntervalSeconds != 60 {
		t.Errorf("CardPublishIntervalSeconds = %d, want 60 (default)", ac.CardPublishIntervalSeconds)
	}
}

func TestParseAgentConfigCardPublishIntervalOverride(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"agent_card_publishing": map[string]any{
			"interval_seconds": 30,
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.CardPublishIntervalSeconds != 30 {
		t.Errorf("CardPublishIntervalSeconds = %d, want 30", ac.CardPublishIntervalSeconds)
	}
}

func TestParseAgentConfigCardPublishIntervalDisable(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"agent_card_publishing": map[string]any{
			"interval_seconds": 0,
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.CardPublishIntervalSeconds != 0 {
		t.Errorf("CardPublishIntervalSeconds = %d, want 0 (disabled)", ac.CardPublishIntervalSeconds)
	}
}

func TestParseAgentConfigMCPThresholdsDefaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.MCPToolResponseSaveThresholdBytes != 2048 {
		t.Errorf("MCPToolResponseSaveThresholdBytes = %d, want 2048 (default)", ac.MCPToolResponseSaveThresholdBytes)
	}
	if ac.MCPToolLLMReturnMaxBytes != 4096 {
		t.Errorf("MCPToolLLMReturnMaxBytes = %d, want 4096 (default)", ac.MCPToolLLMReturnMaxBytes)
	}
	if ac.MCPIntelligentProcessing != nil {
		t.Errorf("MCPIntelligentProcessing should be nil when not configured, got %v", ac.MCPIntelligentProcessing)
	}
}

func TestParseAgentConfigMCPThresholdsOverride(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":                             "test-agent",
		"mcp_tool_response_save_threshold_bytes": 8192,
		"mcp_tool_llm_return_max_bytes":          16384,
		"mcp_intelligent_processing": map[string]any{
			"enable_intelligent_processing": false,
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.MCPToolResponseSaveThresholdBytes != 8192 {
		t.Errorf("MCPToolResponseSaveThresholdBytes = %d, want 8192", ac.MCPToolResponseSaveThresholdBytes)
	}
	if ac.MCPToolLLMReturnMaxBytes != 16384 {
		t.Errorf("MCPToolLLMReturnMaxBytes = %d, want 16384", ac.MCPToolLLMReturnMaxBytes)
	}
	if ac.MCPIntelligentProcessing == nil {
		t.Fatal("MCPIntelligentProcessing should not be nil")
	}
	if enable, ok := ac.MCPIntelligentProcessing["enable_intelligent_processing"].(bool); !ok || enable {
		t.Errorf("MCPIntelligentProcessing[enable_intelligent_processing] = %v, want false", ac.MCPIntelligentProcessing["enable_intelligent_processing"])
	}
}

func TestParseAgentConfigVolumeSlots(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"volumes": []any{
			map[string]any{
				"name":      "project",
				"provision": "static",
				"volume_id": "vol-abc123",
			},
			map[string]any{
				"name":      "scratch",
				"provision": "auto_create",
				"ttl":       "24h",
			},
			map[string]any{
				"name":           "dataset",
				"provision":      "prompt_user",
				"prompt_message": "Select dataset",
			},
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(ac.VolumeSlots) != 3 {
		t.Fatalf("expected 3 volume slots, got %d", len(ac.VolumeSlots))
	}

	if ac.VolumeSlots[0].Name != "project" {
		t.Errorf("slot[0].Name = %q", ac.VolumeSlots[0].Name)
	}
	if ac.VolumeSlots[0].Provision != "static" {
		t.Errorf("slot[0].Provision = %q", ac.VolumeSlots[0].Provision)
	}
	if ac.VolumeSlots[0].VolumeID != "vol-abc123" {
		t.Errorf("slot[0].VolumeID = %q", ac.VolumeSlots[0].VolumeID)
	}

	if ac.VolumeSlots[1].TTL != "24h" {
		t.Errorf("slot[1].TTL = %q", ac.VolumeSlots[1].TTL)
	}

	if ac.VolumeSlots[2].PromptMessage != "Select dataset" {
		t.Errorf("slot[2].PromptMessage = %q", ac.VolumeSlots[2].PromptMessage)
	}
}

func TestParseAgentConfigVolumeSlotsMissingName(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"volumes": []any{
			map[string]any{
				"provision": "static",
				"volume_id": "vol-123",
			},
		},
	})

	_, err := ParseAgentConfig(cfg)
	if err == nil {
		t.Fatal("expected error for volume slot missing name")
	}
}

func TestParseAgentConfigVolumeSlotsDuplicateName(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"volumes": []any{
			map[string]any{
				"name":      "data",
				"provision": "static",
				"volume_id": "vol-1",
			},
			map[string]any{
				"name":      "data",
				"provision": "static",
				"volume_id": "vol-2",
			},
		},
	})

	_, err := ParseAgentConfig(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate volume slot name")
	}
}

func TestParseAgentConfigVolumeSlotStaticMissingVolumeID(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"volumes": []any{
			map[string]any{
				"name":      "data",
				"provision": "static",
			},
		},
	})

	_, err := ParseAgentConfig(cfg)
	if err == nil {
		t.Fatal("expected error for static slot missing volume_id")
	}
}

func TestParseAgentConfigVolumeSlotAutoCreateMissingTTL(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"volumes": []any{
			map[string]any{
				"name":      "scratch",
				"provision": "auto_create",
			},
		},
	})

	_, err := ParseAgentConfig(cfg)
	if err == nil {
		t.Fatal("expected error for auto_create slot missing ttl")
	}
}

func TestParseAgentConfigVolumeSlotUnknownProvision(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"volumes": []any{
			map[string]any{
				"name":      "bad",
				"provision": "magic",
			},
		},
	})

	_, err := ParseAgentConfig(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provision strategy")
	}
}

func TestParseAgentConfigNoVolumes(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ac.VolumeSlots) != 0 {
		t.Errorf("expected 0 volume slots, got %d", len(ac.VolumeSlots))
	}
}

func TestParseAgentConfigCardOverridesTopLevel(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":  "test-agent",
		"description": "top-level desc",
		"version":     "1.0.0",
		"agent_card": map[string]any{
			"description": "card-level desc",
			"version":     "2.0.0",
		},
	})

	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if ac.Description != "card-level desc" {
		t.Errorf("Description = %q, want %q (card should override top-level)", ac.Description, "card-level desc")
	}
	if ac.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q (card should override top-level)", ac.Version, "2.0.0")
	}
}

func TestRegistryGroupsMatchRuntime(t *testing.T) {
	for group := range schema.ValidBuiltinGroups {
		names := lookupBuiltinGroup(group)
		if names == nil {
			t.Errorf("schema.ValidBuiltinGroups contains %q but lookupBuiltinGroup returns nil", group)
		}
	}
}

func TestSanitizeAgentName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"alphanumeric_unchanged", "MyAgent123", "MyAgent123"},
		{"underscore_unchanged", "my_agent", "my_agent"},
		{"dash_replaced", "test-agent", "test_agent"},
		{"multiple_dashes", "my-cool-agent", "my_cool_agent"},
		{"spaces_replaced", "my agent", "my_agent"},
		{"dots_replaced", "agent.v2", "agent_v2"},
		{"mixed_special_chars", "my-agent.v2 (test)", "my_agent_v2__test_"},
		{"all_valid", "abc_DEF_123", "abc_DEF_123"},
		{"all_invalid", "---", "___"},
		{"unicode_replaced", "agënt_naïve", "ag_nt_na_ve"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAgentName(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeAgentName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseAgentConfig_NameSanitized(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"dash_to_underscore", "test-agent", "test_agent"},
		{"already_valid", "test_agent", "test_agent"},
		{"multiple_invalid", "my-cool.agent", "my_cool_agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.FromMap(map[string]any{
				"agent_name": tt.input,
			})
			ac, err := ParseAgentConfig(cfg)
			if err != nil {
				t.Fatalf("ParseAgentConfig: %v", err)
			}
			if ac.Name != tt.expected {
				t.Errorf("Name = %q, want %q", ac.Name, tt.expected)
			}
		})
	}
}

func TestParseAgentConfig_CacheStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
		want     string
	}{
		{"none", "none", "none"},
		{"5m", "5m", "5m"},
		{"1h", "1h", "1h"},
		{"invalid defaults to 5m", "ephemeral", "5m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.FromMap(map[string]any{
				"agent_name": "test_agent",
				"model": map[string]any{
					"name":           "gpt-4",
					"cache_strategy": tt.strategy,
				},
			})
			ac, err := ParseAgentConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if ac.CacheStrategy != tt.want {
				t.Errorf("CacheStrategy = %q, want %q", ac.CacheStrategy, tt.want)
			}
		})
	}
}

func TestParseAgentConfig_ThinkingBudget(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"model": map[string]any{
			"name": "claude",
			"thinking": map[string]any{
				"budget_tokens": float64(5000),
			},
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ReasoningBudgetTokens != 5000 {
		t.Errorf("ReasoningBudgetTokens = %d, want 5000", ac.ReasoningBudgetTokens)
	}
}

func TestParseAgentConfig_ThinkingEffort(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"model": map[string]any{
			"name": "claude",
			"thinking": map[string]any{
				"effort": "high",
			},
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high", ac.ReasoningEffort)
	}
}

func TestParseAgentConfig_ThinkingEffortInvalidIsDropped(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"model": map[string]any{
			"name": "claude",
			"thinking": map[string]any{
				"effort": "extra-hard",
			},
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want empty for an unrecognised level",
			ac.ReasoningEffort)
	}
}

func TestParseAgentConfig_EnableBoolPointers(t *testing.T) {
	off := false
	fields := []struct {
		configKey   string
		getter      func(*agent.AgentConfig) *bool
		whenOmitted *bool
	}{
		{"enable_auto_continuation", func(ac *agent.AgentConfig) *bool { return ac.EnableAutoContinuation }, nil},
		{"inject_current_time", func(ac *agent.AgentConfig) *bool { return ac.InjectCurrentTime }, nil},
		{"enable_embed_resolution", func(ac *agent.AgentConfig) *bool { return ac.EnableEmbedResolution }, nil},
		{"enable_artifact_content_instruction", func(ac *agent.AgentConfig) *bool { return ac.EnableArtifactContentInstruction }, nil},
		{"enable_template_instructions", func(ac *agent.AgentConfig) *bool { return ac.EnableTemplateInstructions }, nil},
		{"enable_examples", func(ac *agent.AgentConfig) *bool { return ac.EnableExamples }, &off},
		{"headless", func(ac *agent.AgentConfig) *bool { return ac.Headless }, nil},
	}

	for _, f := range fields {
		t.Run(f.configKey+"_true", func(t *testing.T) {
			cfg := config.FromMap(map[string]any{"agent_name": "test", f.configKey: true})
			ac, err := ParseAgentConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ptr := f.getter(ac)
			if ptr == nil || !*ptr {
				t.Errorf("%s set to true: got %v", f.configKey, ptr)
			}
		})
		t.Run(f.configKey+"_false", func(t *testing.T) {
			cfg := config.FromMap(map[string]any{"agent_name": "test", f.configKey: false})
			ac, err := ParseAgentConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ptr := f.getter(ac)
			if ptr == nil || *ptr {
				t.Errorf("%s set to false: got %v", f.configKey, ptr)
			}
		})
		t.Run(f.configKey+"_omitted", func(t *testing.T) {
			cfg := config.FromMap(map[string]any{"agent_name": "test"})
			ac, err := ParseAgentConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ptr := f.getter(ac)
			if (ptr == nil) != (f.whenOmitted == nil) || (ptr != nil && *ptr != *f.whenOmitted) {
				t.Errorf("%s omitted: got %v, want %v", f.configKey, ptr, f.whenOmitted)
			}
		})
	}
}

func TestParseAgentConfig_AutoSummarization(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"auto_summarization": map[string]any{
			"enabled":               true,
			"compaction_percentage": 0.5,
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.AutoSummarization == nil {
		t.Fatal("AutoSummarization should not be nil")
	}
	if !ac.AutoSummarization.Enabled {
		t.Error("expected enabled=true")
	}
	if ac.AutoSummarization.CompactionPercentage != 0.5 {
		t.Errorf("CompactionPercentage = %f, want 0.5", ac.AutoSummarization.CompactionPercentage)
	}
}

func TestParseAgentConfig_AutoSummarizationDefaultEnabled(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.AutoSummarization == nil {
		t.Fatal("AutoSummarization should not be nil when section is absent")
	}
	if !ac.AutoSummarization.Enabled {
		t.Error("expected default enabled=true to match Python SAM")
	}
	if ac.AutoSummarization.CompactionPercentage != 0.25 {
		t.Errorf("default CompactionPercentage = %f, want 0.25", ac.AutoSummarization.CompactionPercentage)
	}
}

func TestParseAgentConfig_AutoSummarizationExplicitDisable(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"auto_summarization": map[string]any{
			"enabled": false,
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.AutoSummarization == nil {
		t.Fatal("AutoSummarization should not be nil")
	}
	if ac.AutoSummarization.Enabled {
		t.Error("explicit enabled=false should be honored")
	}
}

func TestParseAgentConfig_AutoSummarizationDefaultPercentage(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"auto_summarization": map[string]any{
			"enabled": true,
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.AutoSummarization.CompactionPercentage != 0.25 {
		t.Errorf("default CompactionPercentage = %f, want 0.25", ac.AutoSummarization.CompactionPercentage)
	}
}

func TestParseAgentConfig_SchemaSettings(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
		"output_schema": map[string]any{
			"type": "object",
		},
		"validation_max_retries": float64(5),
		"schema_max_keys":        float64(500),
		"schema_inference_depth": float64(8),
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.InputSchema == nil {
		t.Error("InputSchema should not be nil")
	}
	if ac.OutputSchema == nil {
		t.Error("OutputSchema should not be nil")
	}
	if ac.ValidationMaxRetries != 5 {
		t.Errorf("ValidationMaxRetries = %d, want 5", ac.ValidationMaxRetries)
	}
	if ac.SchemaMaxKeys != 500 {
		t.Errorf("SchemaMaxKeys = %d, want 500", ac.SchemaMaxKeys)
	}
	if ac.SchemaInferenceDepth != 8 {
		t.Errorf("SchemaInferenceDepth = %d, want 8", ac.SchemaInferenceDepth)
	}
}

func TestParseAgentConfig_SchemaDefaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{"agent_name": "test"})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ValidationMaxRetries != 2 {
		t.Errorf("default ValidationMaxRetries = %d, want 2", ac.ValidationMaxRetries)
	}
	if ac.SchemaMaxKeys != 200 {
		t.Errorf("default SchemaMaxKeys = %d, want 300", ac.SchemaMaxKeys)
	}
	if ac.SchemaInferenceDepth != 4 {
		t.Errorf("default SchemaInferenceDepth = %d, want 4", ac.SchemaInferenceDepth)
	}
}

func TestParseAgentConfig_InterAgentCommunication(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"inter_agent_communication": map[string]any{
			"allow_list":              []any{"agent-a", "agent-b"},
			"deny_list":               []any{"agent-x"},
			"request_timeout_seconds": float64(60),
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ac.PeerAllowList) != 2 {
		t.Errorf("PeerAllowList = %v, want 2 entries", ac.PeerAllowList)
	}
	if len(ac.PeerDenyList) != 1 {
		t.Errorf("PeerDenyList = %v, want 1 entry", ac.PeerDenyList)
	}
	if ac.PeerRequestTimeoutSeconds != 60 {
		t.Errorf("PeerRequestTimeoutSeconds = %d, want 60", ac.PeerRequestTimeoutSeconds)
	}
}

func TestParseAgentConfig_InterAgentDefaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":                "test-agent",
		"inter_agent_communication": map[string]any{},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.PeerRequestTimeoutSeconds != config.DefaultPeerRequestTimeoutSecs {
		t.Errorf("default PeerRequestTimeoutSeconds = %d, want %d",
			ac.PeerRequestTimeoutSeconds, config.DefaultPeerRequestTimeoutSecs)
	}
}

func TestParseAgentConfig_AllowListNilVsEmpty(t *testing.T) {
	t.Run("section absent", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{"agent_name": "test"})
		ac, err := ParseAgentConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if ac.PeerAllowList != nil {
			t.Errorf("PeerAllowList = %#v, want nil (no inter_agent_communication section)", ac.PeerAllowList)
		}
	})

	t.Run("section present but allow_list key absent", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{
			"agent_name": "test",
			"inter_agent_communication": map[string]any{
				"request_timeout_seconds": float64(30),
			},
		})
		ac, err := ParseAgentConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if ac.PeerAllowList != nil {
			t.Errorf("PeerAllowList = %#v, want nil (allow_list key absent)", ac.PeerAllowList)
		}
	})

	t.Run("explicit empty list", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{
			"agent_name": "test",
			"inter_agent_communication": map[string]any{
				"allow_list": []any{},
			},
		})
		ac, err := ParseAgentConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if ac.PeerAllowList == nil {
			t.Fatal("PeerAllowList = nil, want non-nil empty slice (explicit allow_list: [])")
		}
		if len(ac.PeerAllowList) != 0 {
			t.Errorf("PeerAllowList = %#v, want empty slice", ac.PeerAllowList)
		}
	})

	t.Run("explicit non-empty list", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{
			"agent_name": "test",
			"inter_agent_communication": map[string]any{
				"allow_list": []any{"peer-a"},
			},
		})
		ac, err := ParseAgentConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(ac.PeerAllowList) != 1 || ac.PeerAllowList[0] != "peer-a" {
			t.Errorf("PeerAllowList = %#v, want [peer-a]", ac.PeerAllowList)
		}
	})
}

func TestParseAgentConfig_DiscoveryHealthCheck(t *testing.T) {
	boolTrue := true
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"agent_discovery": map[string]any{
			"enabled":                       boolTrue,
			"health_check_ttl_seconds":      float64(600),
			"health_check_interval_seconds": float64(120),
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.DiscoveryEnabled == nil || !*ac.DiscoveryEnabled {
		t.Error("DiscoveryEnabled should be true")
	}
	if ac.HealthCheckTTLSeconds != 600 {
		t.Errorf("HealthCheckTTLSeconds = %d, want 600", ac.HealthCheckTTLSeconds)
	}
	if ac.HealthCheckIntervalSeconds != 120 {
		t.Errorf("HealthCheckIntervalSeconds = %d, want 120", ac.HealthCheckIntervalSeconds)
	}
}

func TestParseAgentConfig_DiscoveryDefaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":      "test-agent",
		"agent_discovery": map[string]any{},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.HealthCheckTTLSeconds != 300 {
		t.Errorf("default HealthCheckTTLSeconds = %d, want 300", ac.HealthCheckTTLSeconds)
	}
	if ac.HealthCheckIntervalSeconds != 60 {
		t.Errorf("default HealthCheckIntervalSeconds = %d, want 60", ac.HealthCheckIntervalSeconds)
	}
}

func TestParseAgentConfig_SkillsAndPaths(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":        "test-agent",
		"skills_base_path":  "/opt/skills",
		"skills_cache_path": "/tmp/skills-cache",
		"skills": []any{
			map[string]any{"name": "manim"},
			map[string]any{"name": "research", "auto_load": true},
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.SkillsBasePath != "/opt/skills" {
		t.Errorf("SkillsBasePath = %q", ac.SkillsBasePath)
	}
	if ac.SkillsCachePath != "/tmp/skills-cache" {
		t.Errorf("SkillsCachePath = %q", ac.SkillsCachePath)
	}
	if len(ac.SkillRefs) != 2 {
		t.Errorf("SkillRefs = %v, want 2 entries", ac.SkillRefs)
	}
}

func TestParseAgentConfig_RequiredScopes(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":      "test-agent",
		"required_scopes": []any{"scope:read", "scope:write"},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(ac.RequiredScopes) != 2 {
		t.Errorf("RequiredScopes = %v, want 2", ac.RequiredScopes)
	}
	if ac.RequiredScopes[0] != "scope:read" {
		t.Errorf("RequiredScopes[0] = %q", ac.RequiredScopes[0])
	}
}

func TestParseAgentConfig_StreamingThresholds(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":                      "test-agent",
		"stream_batching_threshold_bytes": float64(200),
		"max_message_size_bytes":          float64(5000000),
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.StreamBatchingThresholdBytes != 200 {
		t.Errorf("StreamBatchingThresholdBytes = %d, want 300", ac.StreamBatchingThresholdBytes)
	}
	if ac.MaxMessageSizeBytes != 5000000 {
		t.Errorf("MaxMessageSizeBytes = %d, want 5000000", ac.MaxMessageSizeBytes)
	}
}

func TestParseAgentConfig_StreamingThresholdDefaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{"agent_name": "test"})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.StreamBatchingThresholdBytes != 1024 {
		t.Errorf("default StreamBatchingThresholdBytes = %d, want 1024", ac.StreamBatchingThresholdBytes)
	}
	if ac.StreamBatchingFlushIntervalMs != 300 {
		t.Errorf("default StreamBatchingFlushIntervalMs = %d, want 300", ac.StreamBatchingFlushIntervalMs)
	}
	if ac.MaxMessageSizeBytes != 10_000_000 {
		t.Errorf("default MaxMessageSizeBytes = %d, want 10000000", ac.MaxMessageSizeBytes)
	}
}

func TestParseAgentConfig_ArtifactThresholds(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":                       "test-agent",
		"tool_output_save_threshold_bytes": float64(4096),
		"tool_output_llm_return_max_bytes": float64(8192),
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ToolOutputSaveThresholdBytes != 4096 {
		t.Errorf("ToolOutputSaveThresholdBytes = %d, want 4096", ac.ToolOutputSaveThresholdBytes)
	}
	if ac.ToolOutputLLMReturnMaxBytes != 8192 {
		t.Errorf("ToolOutputLLMReturnMaxBytes = %d, want 8192", ac.ToolOutputLLMReturnMaxBytes)
	}
}

func TestParseAgentConfig_ArtifactThresholdDefaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{"agent_name": "test"})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ToolOutputSaveThresholdBytes != 8192 {
		t.Errorf("default ToolOutputSaveThresholdBytes = %d, want 8192", ac.ToolOutputSaveThresholdBytes)
	}
	if ac.ToolOutputLLMReturnMaxBytes != 65536 {
		t.Errorf("default ToolOutputLLMReturnMaxBytes = %d, want 65536", ac.ToolOutputLLMReturnMaxBytes)
	}
	if ac.ToolResultPreviewLengthChars != 500 {
		t.Errorf("default ToolResultPreviewLengthChars = %d, want 500", ac.ToolResultPreviewLengthChars)
	}
	if ac.ToolResultProcessingEnabled != nil {
		t.Errorf("ToolResultProcessingEnabled should be nil (unset) by default, got %v", *ac.ToolResultProcessingEnabled)
	}
}

func TestParseAgentConfig_PromptFlagDefaults(t *testing.T) {
	ac, err := ParseAgentConfig(config.FromMap(map[string]any{"agent_name": "test"}))
	if err != nil {
		t.Fatal(err)
	}
	if ac.EnableExamples == nil || *ac.EnableExamples {
		t.Errorf("EnableExamples = %v by default, want false", ac.EnableExamples)
	}
	for name, got := range map[string]*bool{
		"EnableTemplateInstructions":       ac.EnableTemplateInstructions,
		"EnableEmbedResolution":            ac.EnableEmbedResolution,
		"EnableArtifactContentInstruction": ac.EnableArtifactContentInstruction,
		"AutoInjectArtifactTools":          ac.AutoInjectArtifactTools,
		"AutoInjectDataAnalysisTools":      ac.AutoInjectDataAnalysisTools,
	} {
		if got != nil {
			t.Errorf("%s = %v by default, want unset so it follows the agent's tools", name, *got)
		}
	}
	if ac.Headless != nil {
		t.Errorf("Headless = %v by default, want unset so an agent keeps the chat rules", *ac.Headless)
	}
}

func TestParseAgentConfig_KeepsExplicitPromptFlags(t *testing.T) {
	ac, err := ParseAgentConfig(config.FromMap(map[string]any{
		"agent_name":                       "test",
		"enable_examples":                  true,
		"auto_inject_artifact_tools":       true,
		"headless":                         false,
		"tool_output_save_threshold_bytes": float64(4096),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if ac.EnableExamples == nil || !*ac.EnableExamples {
		t.Errorf("EnableExamples = %v, want the explicit true", ac.EnableExamples)
	}
	if ac.AutoInjectArtifactTools == nil || !*ac.AutoInjectArtifactTools {
		t.Errorf("AutoInjectArtifactTools = %v, want the explicit true", ac.AutoInjectArtifactTools)
	}
	if ac.Headless == nil || *ac.Headless {
		t.Errorf("Headless = %v, want the explicit false", ac.Headless)
	}
	if ac.ToolOutputSaveThresholdBytes != 4096 {
		t.Errorf("ToolOutputSaveThresholdBytes = %d, want the explicit 4096", ac.ToolOutputSaveThresholdBytes)
	}
}

func TestAgentSchemaThresholdDefaultsMatchParser(t *testing.T) {
	raw, err := schema.SchemaContent("agent")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]struct {
			Default any `yaml:"default"`
		} `yaml:"properties"`
	}
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	ac, err := ParseAgentConfig(config.FromMap(map[string]any{"agent_name": "test"}))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{
		"tool_result_auto_artifact_threshold_bytes": ac.ToolOutputSaveThresholdBytes,
		"tool_output_save_threshold_bytes":          ac.ToolOutputSaveThresholdBytes,
		"tool_result_inline_truncation_bytes":       ac.ToolOutputLLMReturnMaxBytes,
		"tool_output_llm_return_max_bytes":          ac.ToolOutputLLMReturnMaxBytes,
	} {
		prop, ok := doc.Properties[key]
		if !ok {
			t.Errorf("agent schema has no %q property", key)
			continue
		}
		if prop.Default != want {
			t.Errorf("agent schema default for %q = %v, parser applies %d", key, prop.Default, want)
		}
	}
}

func TestParseAgentConfig_PythonKnobNames(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"tool_result_auto_artifact_threshold_bytes": float64(16384),
		"tool_result_inline_truncation_bytes":       float64(32768),
		"tool_result_preview_length_chars":          float64(250),
		"tool_result_processing_enabled":            false,
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ToolOutputSaveThresholdBytes != 16384 {
		t.Errorf("ToolOutputSaveThresholdBytes = %d, want 16384 (from Python knob)", ac.ToolOutputSaveThresholdBytes)
	}
	if ac.ToolOutputLLMReturnMaxBytes != 32768 {
		t.Errorf("ToolOutputLLMReturnMaxBytes = %d, want 32768 (from Python knob)", ac.ToolOutputLLMReturnMaxBytes)
	}
	if ac.ToolResultPreviewLengthChars != 250 {
		t.Errorf("ToolResultPreviewLengthChars = %d, want 250", ac.ToolResultPreviewLengthChars)
	}
	if ac.ToolResultProcessingEnabled == nil || *ac.ToolResultProcessingEnabled {
		t.Errorf("ToolResultProcessingEnabled should be explicitly false, got %v", ac.ToolResultProcessingEnabled)
	}
}

func TestParseAgentConfig_PythonKnobNameWinsOverLegacy(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test",
		"tool_result_auto_artifact_threshold_bytes": float64(100),
		"tool_output_save_threshold_bytes":          float64(999),
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.ToolOutputSaveThresholdBytes != 100 {
		t.Errorf("expected Python knob (100) to win over legacy (999), got %d", ac.ToolOutputSaveThresholdBytes)
	}
}

func stubModelAliases() map[string]any {
	return map[string]any{
		"general": map[string]any{
			"model":    "general-model",
			"api_base": "https://llm.example/general",
		},
		"planning": map[string]any{
			"model":    "planning-model",
			"api_base": "https://llm.example/planning",
		},
	}
}

func TestParseAgentConfig_ModelProvider_ListResolvesAlias(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model_provider": []any{"general"},
	})
	ac, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "general-model" {
		t.Errorf("Model = %q, want %q", ac.Model, "general-model")
	}
	if ac.ModelAlias != "general" {
		t.Errorf("ModelAlias = %q, want %q (populated from model_provider first entry)", ac.ModelAlias, "general")
	}
}

func TestParseAgentConfig_ModelAlias_PopulatedWithoutAliasesMap(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "alias-only-agent",
		"model_provider": []any{"planning", "general"},
	})
	ac, err := ParseAgentConfig(cfg, WithDynamicBindings(true))
	if err != nil {
		t.Fatal(err)
	}
	if ac.ModelAlias != "planning" {
		t.Errorf("ModelAlias = %q, want %q (first entry wins)", ac.ModelAlias, "planning")
	}
}

func TestParseAgentConfig_ModelProvider_ListFallsBackToNextAlias(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model_provider": []any{"image_gen", "general"},
	})
	ac, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "general-model" {
		t.Errorf("Model = %q, want %q (fallback)", ac.Model, "general-model")
	}
}

func TestParseAgentConfig_ModelProvider_PlanningFirstWins(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model_provider": []any{"planning", "general"},
	})
	ac, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "planning-model" {
		t.Errorf("Model = %q, want %q", ac.Model, "planning-model")
	}
}

func TestParseAgentConfig_ModelProvider_ModelFieldWins(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model":          map[string]any{"model": "explicit-model"},
		"model_provider": []any{"planning"},
	})
	ac, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "explicit-model" {
		t.Errorf("Model = %q, want %q (model field should override model_provider)", ac.Model, "explicit-model")
	}
	if ac.ModelAlias != "" {
		t.Errorf("ModelAlias = %q, want empty (model_provider was discarded in favor of static model:)", ac.ModelAlias)
	}
}

func TestParseAgentConfig_ModelProvider_NeitherSet_Errors(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
	})
	_, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err == nil {
		t.Fatal("expected error when neither 'model' nor 'model_provider' is set")
	}
}

func TestParseAgentConfig_ModelProvider_NoMatchingAlias_Errors(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model_provider": []any{"nonexistent"},
	})
	_, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err == nil {
		t.Fatal("expected error when no alias resolves")
	}
}

func TestParseAgentConfig_ModelProvider_InvalidShape_Errors(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model_provider": "general",
	})
	_, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err == nil {
		t.Fatal("expected error when model_provider is not a list")
	}
}

func TestParseAgentConfig_ModelProvider_EmptyEntry_Errors(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model_provider": []any{""},
	})
	_, err := ParseAgentConfig(cfg, WithModelAliases(stubModelAliases()))
	if err == nil {
		t.Fatal("expected error when model_provider contains an empty string")
	}
}

func TestParseAgentConfig_ModelProvider_NoAliasesProvided_Ignored(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "test-agent",
		"model_provider": []any{"general"},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "" {
		t.Errorf("Model = %q, expected empty (list-form ignored without aliases)", ac.Model)
	}
}

func TestParseAgentConfig_ModelProvider_NoWarnWhenBindingsEnabled(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	cfg := config.FromMap(map[string]any{
		"agent_name":     "bindings-agent",
		"model_provider": []any{"general"},
	})
	ac, err := ParseAgentConfig(cfg, WithDynamicBindings(true))
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "" {
		t.Errorf("Model = %q, expected empty (bindings resolves later)", ac.Model)
	}

	logs := buf.String()
	if strings.Contains(logs, "level=WARN") && strings.Contains(logs, "model not resolved") {
		t.Fatalf("expected no 'model not resolved' WARN when bindings are enabled; logs:\n%s", logs)
	}
}

func TestParseAgentConfig_ModelProvider_WarnWhenBindingsDisabled(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	cfg := config.FromMap(map[string]any{
		"agent_name":     "no-bindings-agent",
		"model_provider": []any{"general"},
	})
	if _, err := ParseAgentConfig(cfg); err != nil {
		t.Fatal(err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "model not resolved") {
		t.Fatalf("expected WARN 'model not resolved' when bindings are not enabled; logs:\n%s", logs)
	}
}

func TestParseAgentConfig_ModelProvider_BindingsBeatStaticAliases(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name":     "orchestrator-agent",
		"model_provider": []any{"planning"},
	})
	ac, err := ParseAgentConfig(cfg,
		WithModelAliases(stubModelAliases()),
		WithDynamicBindings(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "" {
		t.Errorf("Model = %q, want empty (bindings registry must resolve at runtime, not static aliases)", ac.Model)
	}
	if ac.ModelAlias != "planning" {
		t.Errorf("ModelAlias = %q, want %q", ac.ModelAlias, "planning")
	}
}

func TestParseAgentConfig_MCPIntelligentProcessing(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_name": "test-agent",
		"mcp_intelligent_processing": map[string]any{
			"enabled":       true,
			"summary_model": "gpt-4o-mini",
		},
	})
	ac, err := ParseAgentConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ac.MCPIntelligentProcessing == nil {
		t.Fatal("MCPIntelligentProcessing should not be nil")
	}
	if ac.MCPIntelligentProcessing["enabled"] != true {
		t.Errorf("enabled = %v", ac.MCPIntelligentProcessing["enabled"])
	}
	if ac.MCPIntelligentProcessing["summary_model"] != "gpt-4o-mini" {
		t.Errorf("summary_model = %v", ac.MCPIntelligentProcessing["summary_model"])
	}
}
