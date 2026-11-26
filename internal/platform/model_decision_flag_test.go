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

func TestDecisionModelsFollowTheDecisionNodeFlag(t *testing.T) {
	off := features.NewRegistry()
	off.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: false})
	decisionKey := map[string]any{"type": "apikey", "api_key": "ts-secret"}
	newService := func(t *testing.T, registry *features.Registry) *ModelConfigService {
		t.Helper()
		svc := NewModelConfigService(store.NewModelConfigRepo(store.TestDB(t)), slog.Default())
		svc.SetFeatureRegistry(registry)
		return svc
	}

	for name, registry := range map[string]*features.Registry{"flag off": off, "no registry": nil} {
		t.Run(name+": a new decision model is refused and a chat model is not", func(t *testing.T) {
			svc := newService(t, registry)

			_, err := svc.Create(t.Context(), decisionCreateRequest(decisionKey), "tester")
			want := features.EnvVarName(features.KeyWorkflowDecisionNode)
			verr, ok := errors.AsType[*ValidationError](err)
			if !ok || verr.Field != "modelType" || len(verr.Details["modelType"]) == 0 || !strings.Contains(verr.Message, want) {
				t.Fatalf("create decision model err = %v, want a modelType validation error naming %s", err, want)
			}
			if _, err := svc.Create(t.Context(), CreateModelConfigRequest{
				Alias: "general-chat", Provider: "openai", ModelName: "gpt-4o", APIBase: "https://llm.example.com/v1",
				AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-chat-secret"},
			}, "tester"); err != nil {
				t.Fatalf("create chat model: %v", err)
			}
		})
	}

	t.Run("decision-model evaluators alone allow a new decision model", func(t *testing.T) {
		evalOnly := features.NewRegistry()
		evalOnly.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: false})
		evalOnly.Register(features.Definition{Key: features.KeyEvalDecisionEvaluator, Default: true})
		svc := newService(t, evalOnly)
		if _, err := svc.Create(t.Context(), decisionCreateRequest(decisionKey), "tester"); err != nil {
			t.Fatalf("create decision model with only the eval flag on: %v", err)
		}
	})

	t.Run("a decision model created while the flag was on stays editable after it is turned off", func(t *testing.T) {
		svc := newService(t, decisionNodesOn())
		created, err := svc.Create(t.Context(), decisionCreateRequest(decisionKey), "tester")
		if err != nil {
			t.Fatalf("create with the flag on: %v", err)
		}

		svc.SetFeatureRegistry(off)
		description := "Routes support tickets"
		if _, err := svc.Update(t.Context(), created.ID, UpdateModelConfigRequest{Description: &description, DescriptionSet: true}, "tester"); err != nil {
			t.Fatalf("update with the flag off: %v", err)
		}
	})
}
