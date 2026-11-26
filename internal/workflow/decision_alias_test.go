// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

var aliasedDecisionWorkflow = strings.Replace(routedDecisionWorkflow,
	"decision:\n  api_key: test-key\n  model: jev-1.13.0\n",
	"decision:\n  model_alias: jev\n", 1)

var aliasedEscalatingDecisionWorkflow = strings.Replace(escalatingDecisionWorkflow,
	"decision:\n  api_key: test-key\n  model: jev-1.13.0\n",
	"decision:\n  model_alias: jev\n", 1)

func registeredDecisionModel(apiBase, apiKey string) map[string]any {
	return map[string]any{
		"model":             "jev-1.13.0",
		"api_base":          apiBase,
		"api_key":           apiKey,
		samapp.ModelTypeKey: samapp.DecisionModelType,
	}
}

func withRegistry(t *testing.T, reg **bindings.BindingsRegistry, alias string, raw func(jevURL string) map[string]any) func(string, *stubServices) {
	return func(jevURL string, svc *stubServices) {
		r := bindings.New()
		if err := r.UpsertModel(t.Context(), alias, raw(jevURL)); err != nil {
			t.Fatalf("UpsertModel: %v", err)
		}
		svc.bindings = r
		if reg != nil {
			*reg = r
		}
	}
}

func TestDecisionNodeResolvesItsModelFromTheRegistry(t *testing.T) {
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, aliasedDecisionWorkflow, jev, true,
		withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredDecisionModel(url, "registry-key") }))
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	jev.mu.Lock()
	defer jev.mu.Unlock()
	if jev.lastAuth != "Bearer registry-key" {
		t.Errorf("provider was called with %q, want the registry's key", jev.lastAuth)
	}
	if jev.lastBody["model"] != "jev-1.13.0" {
		t.Errorf("model = %v, want the registered model", jev.lastBody["model"])
	}
}

func TestDecisionTimeoutOutlivesTheSharedClientTimeout(t *testing.T) {
	doc := strings.Replace(aliasedDecisionWorkflow, "model_alias: jev\n", "model_alias: jev\n  timeout_seconds: 5\n", 1)
	jev := &fakeJev{delay: 300 * time.Millisecond, respond: departmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true,
		withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredDecisionModel(url, "registry-key") }),
		func(_ string, svc *stubServices) { svc.httpClient = &http.Client{Timeout: 50 * time.Millisecond} })
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
}

func TestDecisionNodePicksUpARotatedKeyWithoutRedeploy(t *testing.T) {
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	var reg *bindings.BindingsRegistry
	jevURL := ""
	run := newDecisionRun(t, aliasedDecisionWorkflow, jev, true,
		withRegistry(t, &reg, "jev", func(url string) map[string]any {
			jevURL = url
			return registeredDecisionModel(url, "old-key")
		}))

	if _, err := run.e.instance.decisions.Evaluate(t.Context(), decisionRequestFor(run)); err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if err := reg.UpsertModel(t.Context(), "jev", registeredDecisionModel(jevURL, "rotated-key")); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := run.e.instance.decisions.Evaluate(t.Context(), decisionRequestFor(run)); err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	jev.mu.Lock()
	defer jev.mu.Unlock()
	if jev.lastAuth != "Bearer rotated-key" {
		t.Errorf("after rotation the provider saw %q, want the new key", jev.lastAuth)
	}
}

func decisionRequestFor(run *decisionRun) decision.Request {
	return decision.Request{State: map[string]any{"ticket": "x"}, Questions: run.e.cfg.Nodes[0].Questions}
}

func TestDecisionNodeRejectsAliasProblems(t *testing.T) {
	cases := map[string]struct {
		raw     func(url string) map[string]any
		alias   string
		wantErr string
	}{
		"alias names a chat model": {
			alias:   "jev",
			raw:     func(url string) map[string]any { return map[string]any{"model": "openai/gpt-4o", "api_key": "k"} },
			wantErr: "is a chat model",
		},
		"alias is not registered": {
			alias:   "someone-else",
			raw:     func(url string) map[string]any { return registeredDecisionModel(url, "k") },
			wantErr: `resolve decision model "jev"`,
		},
		"registered model has no key": {
			alias:   "jev",
			raw:     func(url string) map[string]any { return registeredDecisionModel(url, "") },
			wantErr: "api_key is required",
		},
	}
	docs := map[string]string{
		"fail":     aliasedDecisionWorkflow,
		"escalate": aliasedEscalatingDecisionWorkflow,
	}
	for name, tc := range cases {
		for mode, doc := range docs {
			t.Run(name+"/on_provider_error "+mode, func(t *testing.T) {
				jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
				run := newDecisionRun(t, doc, jev, true, withRegistry(t, nil, tc.alias, tc.raw))
				run.execute(t)

				if msg := run.errorMessage(); !strings.Contains(msg, tc.wantErr) {
					t.Fatalf("workflow error = %q, want it to mention %q", msg, tc.wantErr)
				}
				run.escalationNeverRan(t)
				if jev.calls.Load() != 0 {
					t.Errorf("the provider was called %d times for a bad alias", jev.calls.Load())
				}
			})
		}
	}
}

func TestAliasResolutionFailuresAreConfigErrors(t *testing.T) {
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, aliasedDecisionWorkflow, jev, true,
		withRegistry(t, nil, "someone-else", func(url string) map[string]any { return registeredDecisionModel(url, "k") }))

	_, err := run.e.instance.decisions.Evaluate(t.Context(), decisionRequestFor(run))
	if !errors.Is(err, bindings.ErrAliasUnbound) || decision.Classify(err) != decision.FailureConfig {
		t.Fatalf("err = %v (kind %q), want an unbound alias classified as %q", err, decision.Classify(err), decision.FailureConfig)
	}
}

func withFailingRegistryFetch(fetchErr error) func(string, *stubServices) {
	return func(_ string, svc *stubServices) {
		svc.bindings = bindings.New(bindings.WithFetcher(func(context.Context, string) (map[string]any, error) {
			return nil, fmt.Errorf("platform model-configs lookup: %w", fetchErr)
		}))
	}
}

func TestDecisionNodeRegistryFetchFailures(t *testing.T) {
	for name, fetchErr := range map[string]*control.ClientError{
		"an authentication failure": {Code: control.ErrAuthDenied, Message: "Authentication failed"},
		"a failed store read":       {Code: control.ErrOperationFailed, Message: "store unavailable"},
	} {
		t.Run(name+" stays transient and escalates", func(t *testing.T) {
			jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
			run := newDecisionRun(t, aliasedEscalatingDecisionWorkflow, jev, true, withFailingRegistryFetch(fetchErr))
			run.execute(t)

			run.assertBranches(t, "fallback", "billing", "tech")
			if out := run.output(t, "triage"); out["provider_error"] != string(decision.FailureUnreachable) {
				t.Errorf("triage output = %v, want a transient %q escalation", out, decision.FailureUnreachable)
			}
		})
	}
}

func TestDecisionModelAliasExcludesInlineEndpoint(t *testing.T) {
	_, err := ParseDecisionProvider(map[string]any{"model_alias": "jev", "api_key": "k"})
	if err == nil || !strings.Contains(err.Error(), "remove api_base, api_key and model") {
		t.Fatalf("err = %v, want the alias/inline conflict", err)
	}
}

func TestInitDecisionsWithAliasNeedsTheRegistry(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, aliasedDecisionWorkflow)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	svc := &stubServices{brk: stubBroker{}, features: decisionFeatureRegistry(t, true)}
	w := newTestWorkflowInstance(cfg, svc)
	err = w.initDecisions(svc)
	if err == nil || !strings.Contains(err.Error(), "needs the platform model registry") {
		t.Fatalf("err = %v, want the missing-registry error", err)
	}
}
