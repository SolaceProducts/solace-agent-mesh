// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedChatAndDecisionModels(t *testing.T) *ModelConfigService {
	t.Helper()
	svc, _ := setupModelConfigSvc(t)
	if _, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias: "chatty", Provider: "openai", ModelName: "gpt-4o",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk"},
	}, "tester"); err != nil {
		t.Fatalf("create chat model: %v", err)
	}
	if _, err := svc.Create(t.Context(), decisionModelRequest("jev"), "tester"); err != nil {
		t.Fatalf("create decision model: %v", err)
	}
	return svc
}

func TestAgentCannotUseADecisionModel(t *testing.T) {
	t.Parallel()
	svc := seedChatAndDecisionModels(t)
	agents := &AgentService{modelConfigSvc: svc}

	err := agents.validateModelProvider(t.Context(), "jev")
	if err == nil || !strings.Contains(err.Error(), "is a decision model") {
		t.Fatalf("err = %v, want the decision-model refusal", err)
	}
	if err := agents.validateModelProvider(t.Context(), "chatty"); err != nil {
		t.Errorf("a chat model must stay assignable: %v", err)
	}
}

func TestAIAssistantDoesNotSuggestDecisionModels(t *testing.T) {
	t.Parallel()
	svc := seedChatAndDecisionModels(t)
	assistant := &AIAssistantService{modelConfigSvc: svc, logger: slog.Default()}

	var offeredChat bool
	for _, hint := range assistant.listConfiguredModels(t.Context()) {
		if hint.Alias == "jev" {
			t.Fatal("the AI assistant offered a decision model for an agent")
		}
		offeredChat = offeredChat || hint.Alias == "chatty"
	}
	if !offeredChat {
		t.Error("the AI assistant must still offer the chat model chatty")
	}
}

func TestEvalJudgeRejectsADecisionModel(t *testing.T) {
	t.Parallel()
	svc := seedChatAndDecisionModels(t)
	resolver := newJudgeModelResolver(svc, "general", slog.Default())

	if _, _, _, err := resolver.Resolve(t.Context(), "jev"); err == nil || !strings.Contains(err.Error(), "decision model") {
		t.Fatalf("err = %v, want the decision-model refusal", err)
	}
}

func TestTypeSafeModelListFollowsTheEndpoint(t *testing.T) {
	t.Parallel()
	list := func(apiBase string) []string {
		var ids []string
		for _, m := range typeSafeModels(apiBase) {
			ids = append(ids, m.ID)
		}
		return ids
	}
	if got := list("https://api.typesafe.ai"); !strings.Contains(strings.Join(got, ","), "jev-1.13.0") {
		t.Errorf("TypeSafe ids = %v", got)
	}
	if got := list("https://openrouter.ai/api"); !strings.Contains(strings.Join(got, ","), "typesafe/jev-1.13") {
		t.Errorf("OpenRouter ids = %v", got)
	}
}

func TestOpenAIDecisionModelListSkipsTheProvider(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
	}))
	t.Cleanup(srv.Close)
	listSvc := NewModelListService(srv.Client(), slog.Default())

	decisionIDs, err := listSvc.ListModels(t.Context(), &SupportedModelsRequest{
		Provider: platpkg.ProviderOpenAI, APIBase: srv.URL, APIKey: "sk", ModelType: store.ModelTypeDecision,
	})
	if err != nil || len(decisionIDs) != 1 || decisionIDs[0].ID != "gpt-6-luna" {
		t.Fatalf("decision models = %v, %v; want only gpt-6-luna", decisionIDs, err)
	}
	if calls.Load() != 0 {
		t.Errorf("listing decision models called the provider %d times", calls.Load())
	}

	chatIDs, err := listSvc.ListModels(t.Context(), &SupportedModelsRequest{Provider: platpkg.ProviderOpenAI, APIBase: srv.URL, APIKey: "sk"})
	if err != nil || len(chatIDs) != 1 || chatIDs[0].ID != "gpt-4o" {
		t.Errorf("chat models = %v, %v; want the provider's list", chatIDs, err)
	}

	svc, _ := setupModelConfigSvc(t)
	created, err := svc.Create(t.Context(), openAIDecisionModelRequest("luna-list"), "tester")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := svc.GetModelsFromProviderByID(t.Context(), created.ID, listSvc, "", nil, "")
	if err != nil || len(stored) != 1 || stored[0].ID != "gpt-6-luna" {
		t.Errorf("edit-mode models = %v, %v; want the stored decision model's list", stored, err)
	}
}

func TestWorkflowDecisionModelAliasIsValidatedOnSave(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	if _, err := svc.Create(t.Context(), decisionModelRequest("jev"), "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias: "chatty", Provider: "openai", ModelName: "gpt-4o",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk"},
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	workflows := &WorkflowService{modelConfigs: repo}

	cases := map[string]struct {
		appConfig map[string]any
		wantErr   string
	}{
		"flat body naming a decision model":    {appConfig: map[string]any{"decision": map[string]any{"model_alias": "jev"}}},
		"nested block naming a decision model": {appConfig: map[string]any{"workflow": map[string]any{"decision": map[string]any{"modelAlias": "jev"}}}},
		"no decision block":                    {appConfig: map[string]any{"nodes": []any{}}},
		"unknown alias": {
			appConfig: map[string]any{"decision": map[string]any{"model_alias": "nope"}},
			wantErr:   "is not a configured model",
		},
		"chat model alias": {
			appConfig: map[string]any{"decision": map[string]any{"model_alias": "chatty"}},
			wantErr:   "is a chat model",
		},
		"inline api_key in the flat form": {
			appConfig: map[string]any{"decision": map[string]any{"model_alias": "jev", "api_key": "ts-secret"}},
			wantErr:   "decision.api_key is for file-loaded configs only",
		},
		"inline apiKey in the nested form": {
			appConfig: map[string]any{"workflow": map[string]any{"decision": map[string]any{"apiKey": "ts-secret"}}},
			wantErr:   "decision.apiKey is for file-loaded configs only",
		},
		"inline model alone": {
			appConfig: map[string]any{"decision": map[string]any{"model": "jev-1.13.0"}},
			wantErr:   "decision.model is for file-loaded configs only",
		},
		"alias with tuning keys": {
			appConfig: map[string]any{"decision": map[string]any{"model_alias": "jev", "timeout_seconds": 5}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := workflows.validateDecisionModel(t.Context(), tc.appConfig)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestWorkflowInlineDecisionKeyIsRejectedWithoutAModelRegistry(t *testing.T) {
	t.Parallel()
	workflows := &WorkflowService{}

	err := workflows.validateDecisionModel(t.Context(), map[string]any{
		"decision": map[string]any{"api_key": "ts-secret"},
	})
	if _, ok := errors.AsType[*ValidationError](err); !ok || !strings.Contains(err.Error(), "file-loaded configs only") {
		t.Fatalf("err = %v, want the inline-key ValidationError", err)
	}
}
