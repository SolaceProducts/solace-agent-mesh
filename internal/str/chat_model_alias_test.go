// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestToolChatModelPick(t *testing.T) {
	t.Parallel()
	configured := map[string]any{ToolConfigModelAliasKey: "general"}
	restricted := map[string]any{ToolConfigModelAliasKey: "general", ToolConfigModelAliasesKey: " opus , sonnet "}
	onlyOwn := func(v any) map[string]any { return map[string]any{ToolConfigModelAliasesKey: v} }
	tests := []struct {
		name     string
		enabled  bool
		tc       map[string]any
		strAlias string
		model    string
		want     toolModelChoice
		wantErr  error
	}{
		{name: "no model keeps the tool's model", enabled: true, tc: configured, want: toolModelChoice{alias: "general"}},
		{name: "a named alias runs on that alias", enabled: true, tc: configured, model: " opus ", want: toolModelChoice{alias: "opus", requested: true}},
		{name: "a listed alias", enabled: true, tc: restricted, model: "sonnet", want: toolModelChoice{alias: "sonnet", requested: true}},
		{name: "the configured alias is always allowed", enabled: true, tc: restricted, model: "general", want: toolModelChoice{alias: "general", requested: true}},
		{name: "an alias outside the list", enabled: true, tc: restricted, model: "haiku", wantErr: errChatModelNotPermitted},
		{name: "flag off ignores the named alias", enabled: false, tc: restricted, model: "haiku", want: toolModelChoice{alias: "general"}},
		{name: "an empty list allows only the tool's model", enabled: true, tc: onlyOwn(""), model: "opus", wantErr: errChatModelNotPermitted},
		{name: "a blank list allows only the tool's model", enabled: true, tc: onlyOwn(" "), model: "opus", wantErr: errChatModelNotPermitted},
		{name: "a list of only commas allows only the tool's model", enabled: true, tc: onlyOwn(","), model: "opus", wantErr: errChatModelNotPermitted},
		{name: "an empty list still allows the tool's model", enabled: true, tc: onlyOwn(""), model: "general", want: toolModelChoice{alias: "general", requested: true}},
		{name: "a list value", enabled: true, tc: onlyOwn([]any{"opus"}), model: "opus", want: toolModelChoice{alias: "opus", requested: true}},
		{name: "a string list value", enabled: true, tc: onlyOwn([]string{"sonnet"}), model: "opus", wantErr: errChatModelNotPermitted},
		{name: "the STR's tool model is the tool's model when no _model_alias is set", enabled: true, tc: onlyOwn("opus"), strAlias: "planning", model: "planning", want: toolModelChoice{alias: "planning", requested: true}},
		{name: "general is not the tool's model when the STR names another", enabled: true, tc: onlyOwn("opus"), strAlias: "planning", model: "general", wantErr: errChatModelNotPermitted},
		{name: "a configuration id is refused", enabled: true, tc: configured, model: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", wantErr: errChatModelNotPermitted},
		{name: "a configuration id in another uuid form is refused", enabled: true, tc: configured, model: "{0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b}", wantErr: errChatModelNotPermitted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			models, err := toolConfigChatModels(tc.tc, tc.enabled, tc.strAlias)
			if err != nil {
				t.Fatalf("toolConfigChatModels: %v", err)
			}
			got, err := models.pick(tc.model)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("pick(%q) error = %v, want %v", tc.model, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("pick(%q) = %+v, want %+v", tc.model, got, tc.want)
			}
		})
	}
}

func TestToolConfigChatModelsReadsTheAllowlistOnlyWithTheFlagOn(t *testing.T) {
	t.Parallel()
	for name, val := range map[string]any{"number": 42, "list with a number": []any{"opus", 7}} {
		mistyped := map[string]any{ToolConfigModelAliasesKey: val}
		if _, err := toolConfigChatModels(mistyped, false, ""); err != nil {
			t.Errorf("%s: toolConfigChatModels(flag off, %v) = %v, want the key ignored", name, mistyped, err)
		}
		if _, err := toolConfigChatModels(mistyped, true, ""); err == nil || !strings.Contains(err.Error(), "comma-separated string or a list") {
			t.Errorf("%s: toolConfigChatModels(flag on, %v) error = %v, want one naming the expected format", name, mistyped, err)
		}
	}
}

func TestParseWorkerConfig_ToolChatModelFollowsTheFeatureFlag(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{{env: "", want: false}, {env: "true", want: true}} {
		t.Run("SAM_FEATURE_TOOL_CHAT_MODEL="+tc.env, func(t *testing.T) {
			t.Setenv("SAM_FEATURE_TOOL_CHAT_MODEL", tc.env)
			wc, err := ParseWorkerConfig(config.FromMap(map[string]any{"namespace": "test-ns", "worker_id": "test-worker"}))
			if err != nil {
				t.Fatalf("ParseWorkerConfig: %v", err)
			}
			if wc.ToolChatModel != tc.want {
				t.Errorf("ToolChatModel = %v, want %v", wc.ToolChatModel, tc.want)
			}
		})
	}
}

func TestGoTool_ChatRunsOnTheModelAliasItNames(t *testing.T) {
	toolBinary := getGoTestTool(t)
	entry := &ManifestEntry{ToolName: "chat", Executable: toolBinary}
	tests := []struct {
		name          string
		enabled       bool
		toolConfig    map[string]any
		model         string
		want          toolModelChoice
		wantErrorCode string
	}{
		{name: "no model keeps the tool's model", enabled: true, toolConfig: map[string]any{ToolConfigModelAliasKey: "general"}, want: toolModelChoice{alias: "general"}},
		{name: "a named alias", enabled: true, toolConfig: map[string]any{ToolConfigModelAliasKey: "general"}, model: "opus", want: toolModelChoice{alias: "opus", requested: true}},
		{name: "an alias outside the toolset's list", enabled: true, toolConfig: map[string]any{ToolConfigModelAliasesKey: "sonnet"}, model: "opus", wantErrorCode: chatErrorModelNotPermitted},
		{name: "flag off ignores the alias", enabled: false, toolConfig: map[string]any{ToolConfigModelAliasKey: "general"}, model: "opus", want: toolModelChoice{alias: "general"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := newTestRunner(t)
			runner.chatModelChoice = tc.enabled
			runner.llmCall = func(context.Context, string, string, *remotetool.InvokeParams, string, string, float64) (string, error) {
				return "", errors.New("llm/call is not under test")
			}
			called := false
			var got toolModelChoice
			runner.llmChat = func(_ context.Context, choice toolModelChoice, _ string, _ *remotetool.InvokeParams, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
				called, got = true, choice
				return &llm.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
			}
			params := &remotetool.InvokeParams{
				TaskID: "task-chat-model", ToolName: "chat", ToolConfig: tc.toolConfig,
				Args: map[string]any{"text": "hi", "model": tc.model},
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()

			result, invokeErr := runner.Execute(ctx, params, entry, nil, func(string) {})
			if invokeErr != nil {
				t.Fatalf("Execute: %s: %s", invokeErr.Code, invokeErr.Message)
			}

			if tc.wantErrorCode != "" {
				if result.ToolResult["error_code"] != tc.wantErrorCode || called {
					t.Errorf("error_code %v, model called %v; want %s and no model called", result.ToolResult["error_code"], called, tc.wantErrorCode)
				}
				return
			}
			if result.ToolResult["status"] != "success" || got != tc.want {
				t.Errorf("status %v on %+v, want success on %+v (result %v)", result.ToolResult["status"], got, tc.want, result.ToolResult)
			}
		})
	}
}
