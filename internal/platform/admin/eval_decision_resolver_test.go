// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestDecisionEvaluatorModelLookups(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()
	if _, err := svc.Create(ctx, decisionModelRequest("eval-judge"), "tester"); err != nil {
		t.Fatalf("create decision model: %v", err)
	}
	if _, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias: "chatty", Provider: "openai", ModelName: "gpt-4o",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-test"},
	}, "tester"); err != nil {
		t.Fatalf("create chat model: %v", err)
	}

	lookup := newEvaluatorModelTypeLookup(svc)
	for alias, want := range map[string]string{"eval-judge": store.ModelTypeDecision, "chatty": store.ModelTypeChat} {
		got, found, err := lookup(ctx, alias)
		if err != nil || !found || got != want {
			t.Errorf("lookup(%q) = (%q, %v, %v), want (%q, true, nil)", alias, got, found, err, want)
		}
	}
	if _, found, err := lookup(ctx, "not-registered"); found || err != nil {
		t.Errorf("an unknown alias must be not-found without error, got found=%v err=%v", found, err)
	}

	resolver := newDecisionModelResolver(svc)
	raw, err := resolver.ResolveDecision(ctx, "eval-judge")
	if err != nil {
		t.Fatalf("resolve decision model: %v", err)
	}
	if raw["api_key"] != "ts-secret" || raw["model"] == "" {
		t.Fatalf("resolved raw config lacks the credentials or model: %v", raw)
	}
	if _, err := resolver.ResolveDecision(ctx, "chatty"); !errors.Is(err, eval.ErrDecisionModelUnavailable) || !strings.Contains(err.Error(), "chat model") {
		t.Errorf("resolving a chat model: err = %v, want a chat-model refusal wrapping ErrDecisionModelUnavailable", err)
	}
	if _, err := resolver.ResolveDecision(ctx, "not-registered"); !errors.Is(err, eval.ErrDecisionModelUnavailable) || !strings.Contains(err.Error(), "no configured model") {
		t.Errorf("resolving an unknown alias: err = %v, want it to wrap ErrDecisionModelUnavailable", err)
	}

	if newDecisionModelResolver(nil) != nil || newEvaluatorModelTypeLookup(nil) != nil {
		t.Error("without a model-config service both constructors must return nil")
	}
}

func TestDecisionModelLookupsReportStoreFailures(t *testing.T) {
	t.Parallel()
	svc, _, db := setupModelConfigSvcWithOutbox(t)
	ctx := t.Context()
	if _, err := svc.Create(ctx, decisionModelRequest("eval-judge"), "tester"); err != nil {
		t.Fatalf("create decision model: %v", err)
	}
	trigger := newEvalDecisionModelLookup(svc)
	if raw, _, err := trigger(ctx, "not-registered"); raw != nil || err != nil {
		t.Fatalf("trigger lookup of an unknown alias = (%v, %v), want (nil, nil)", raw, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	_, err := newDecisionModelResolver(svc).ResolveDecision(ctx, "eval-judge")
	if err == nil || errors.Is(err, eval.ErrDecisionModelUnavailable) {
		t.Errorf("resolve with the store down: err = %v, want a store error that is not ErrDecisionModelUnavailable", err)
	}
	if _, _, err := trigger(ctx, "eval-judge"); err == nil {
		t.Error("trigger lookup with the store down returned no error")
	}
	if newEvalDecisionModelLookup(nil) != nil {
		t.Error("without a model-config service the trigger lookup must be nil")
	}
}
