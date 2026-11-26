// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const maxLLMCallsLine = "max_llm_calls_per_task: "

func TestApply_SavesMaxLLMCallsPerTask(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		stored string
		line   string
		want   map[string]any
	}{
		{name: "new agent", line: maxLLMCallsLine + "60",
			want: map[string]any{"maxLlmCallsPerTask": 60.0}},
		{name: "existing agent with nothing stored", stored: "-", line: maxLLMCallsLine + "60",
			want: map[string]any{"maxLlmCallsPerTask": 60.0}},
		{name: "existing agent keeps its other keys", stored: `{"maxLlmCallsPerTask":30,"injectCurrentTime":true}`, line: maxLLMCallsLine + "80",
			want: map[string]any{"maxLlmCallsPerTask": 80.0, "injectCurrentTime": true}},
		{name: "a stored runtime spelling is kept", stored: `{"max_llm_calls_per_task":30}`, line: maxLLMCallsLine + "80",
			want: map[string]any{"max_llm_calls_per_task": 80.0}},
		{name: "both spellings stored and disagreeing are both set", stored: `{"maxLlmCallsPerTask":30,"max_llm_calls_per_task":99}`, line: maxLLMCallsLine + "80",
			want: map[string]any{"maxLlmCallsPerTask": 80.0, "max_llm_calls_per_task": 80.0}},
		{name: "the same value changes nothing", stored: `{"maxLlmCallsPerTask":60}`, line: maxLLMCallsLine + "60",
			want: map[string]any{"maxLlmCallsPerTask": 60.0}},
		{name: "beside a stored peer block", stored: `{"interAgentCommunication":{"allowList":["a"]}}`, line: maxLLMCallsLine + "45",
			want: map[string]any{"maxLlmCallsPerTask": 45.0, "interAgentCommunication": map[string]any{"allowList": []any{"a"}}}},
		{name: "silence keeps the stored value", stored: `{"maxLlmCallsPerTask":45}`,
			want: map[string]any{"maxLlmCallsPerTask": 45.0}},
		{name: "silence keeps both stored spellings", stored: `{"maxLlmCallsPerTask":45,"max_llm_calls_per_task":46}`,
			want: map[string]any{"maxLlmCallsPerTask": 45.0, "max_llm_calls_per_task": 46.0}},
		{name: "null removes the stored value", stored: `{"maxLlmCallsPerTask":45,"injectCurrentTime":true}`, line: "max_llm_calls_per_task:",
			want: map[string]any{"injectCurrentTime": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			switch tc.stored {
			case "":
			case "-":
				if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("reporter")}}, testActor); err != nil {
					t.Fatalf("seed apply: %v", err)
				}
			default:
				seedStoredRuntimeConfig(t, svc, "reporter", json.RawMessage(tc.stored))
			}
			component := agentComponent("reporter")
			if tc.line != "" {
				component = agentComponentWithKey(t, "reporter", tc.line)
			}
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{component}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if op := opFor(t, resp, kindAgent, "reporter"); mentions(op.Warnings, "max_llm_calls_per_task") {
				t.Errorf("warnings = %q, want none about max_llm_calls_per_task", op.Warnings)
			}
			assertStoredBlob(t, svc, "reporter", tc.want)
		})
	}
}

func TestApply_PeerAccessAndMaxLLMCallsStatedTogether(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	for _, tc := range []struct {
		phase string
		calls int
	}{{"create", 60}, {"update", 70}} {
		lines := fmt.Sprintf("%s%d\n      inter_agent_communication:\n        request_timeout_seconds: 90", maxLLMCallsLine, tc.calls)
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{agentComponentWithKey(t, "reporter", lines)},
		}, testActor); err != nil {
			t.Fatalf("%s: Apply: %v", tc.phase, err)
		}
		assertStoredBlob(t, svc, "reporter", map[string]any{
			"maxLlmCallsPerTask": float64(tc.calls), "interAgentCommunication": map[string]any{"requestTimeoutSeconds": 90.0},
		})
	}
}

func TestApply_MaxLLMCallsRefusesAValueThatIsNotAPositiveWholeNumber(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0", "-1", "1.5", "lots", `"60"`, "true"} {
		for _, dryRun := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/dryRun=%v", value, dryRun), func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				seedStoredRuntimeConfig(t, svc, "reporter", json.RawMessage(`{"maxLlmCallsPerTask":45}`))
				before := string(rawStoredAgent(t.Context(), t, svc, "reporter").AdditionalConfigurations)

				_, err := svc.Apply(t.Context(), ApplyBuildRequest{
					DryRun:     dryRun,
					Components: []BuildComponentInput{agentComponentWithKey(t, "reporter", maxLLMCallsLine+value)},
				}, testActor)

				ve := refusalOf(t, fmt.Sprintf("dryRun=%v", dryRun), err)
				if !mentions(ve.Details[ve.Field], "maxLlmCallsPerTask") {
					t.Errorf("details = %v, want a problem naming maxLlmCallsPerTask", ve.Details)
				}
				if after := string(rawStoredAgent(t.Context(), t, svc, "reporter").AdditionalConfigurations); after != before {
					t.Errorf("a refused save wrote: %s -> %s", before, after)
				}
			})
		}
	}
}

func TestMaxLLMCalls_ReachesTheDeployedConfig(t *testing.T) {
	t.Parallel()
	prompt := "You are a probe agent whose only job is to be deployed with its call limit set."
	for name, blob := range map[string]string{
		"stored spelling":                `{"maxLlmCallsPerTask":61}`,
		"runtime spelling":               `{"max_llm_calls_per_task":61}`,
		"both spellings, in step":        `{"maxLlmCallsPerTask":61,"max_llm_calls_per_task":61}`,
		"stored spelling beside a block": `{"maxLlmCallsPerTask":61,"interAgentCommunication":{"requestTimeoutSeconds":90}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deployed, err := entplatform.NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfig(
				&store.Agent{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "probe", SystemPrompt: &prompt, Type: "standard", AdditionalConfigurations: json.RawMessage(blob)},
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
			parsed, err := awe.ParseAgentConfig(config.FromMap(appConfig))
			if err != nil {
				t.Fatalf("ParseAgentConfig: %v", err)
			}
			if parsed.MaxLLMCalls != 61 {
				t.Errorf("the runtime read max_llm_calls_per_task = %d, want 61 (app_config %v)", parsed.MaxLLMCalls, appConfig["max_llm_calls_per_task"])
			}
		})
	}
}

func assertStoredBlob(t *testing.T, svc *Service, name string, want map[string]any) {
	t.Helper()
	got := storedAdditional(t.Context(), t, svc, name)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored additionalConfigurations = %v, want %v", got, want)
	}
}
