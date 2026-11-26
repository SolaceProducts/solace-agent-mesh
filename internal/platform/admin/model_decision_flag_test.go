// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func decisionNodesOff() *features.Registry {
	registry := features.NewRegistry()
	registry.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: false})
	return registry
}

func assertDecisionFlagRefusal(t *testing.T, err error) {
	t.Helper()
	want := features.EnvVarName(features.KeyWorkflowDecisionNode)
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok || verr.Field != "modelType" || len(verr.Details["modelType"]) == 0 || !strings.Contains(verr.Message, want) {
		t.Fatalf("err = %v, want a modelType validation error naming %s", err, want)
	}
}

func TestDecisionModelsFollowTheDecisionNodeFlag(t *testing.T) {
	t.Parallel()
	for name, registry := range map[string]*features.Registry{"flag off": decisionNodesOff(), "no registry": nil} {
		t.Run(name+": a new decision model is refused and a chat model is not", func(t *testing.T) {
			t.Parallel()
			svc := NewModelConfigService(store.NewModelConfigRepo(testOpenDB(t)), slog.Default())
			svc.SetFeatureRegistry(registry)

			_, err := svc.Create(t.Context(), decisionModelRequest("jev"), "tester")
			assertDecisionFlagRefusal(t, err)
			if _, err := svc.Create(t.Context(), &CreateModelConfigRequest{
				Alias: "general-chat", Provider: "openai", ModelName: "gpt-4o", APIBase: "https://llm.example.com/v1",
				AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-chat-secret"},
			}, "tester"); err != nil {
				t.Fatalf("create chat model: %v", err)
			}
		})
	}

	t.Run("a decision model created while the flag was on stays editable after it is turned off", func(t *testing.T) {
		t.Parallel()
		svc := NewModelConfigService(store.NewModelConfigRepo(testOpenDB(t)), slog.Default())
		svc.SetFeatureRegistry(decisionNodesOn())
		created, err := svc.Create(t.Context(), decisionModelRequest("jev"), "tester")
		if err != nil {
			t.Fatalf("create with the flag on: %v", err)
		}

		svc.SetFeatureRegistry(decisionNodesOff())
		description := "Routes support tickets"
		if _, err := svc.Update(t.Context(), created.ID, &UpdateModelConfigRequest{Description: &description}, "tester"); err != nil {
			t.Fatalf("update with the flag off: %v", err)
		}
	})
}
