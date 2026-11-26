// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aliased

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/openai"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/typesafe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type fakeJev struct {
	mu        sync.Mutex
	lastAuth  string
	lastModel string
}

func (f *fakeJev) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.lastAuth = r.Header.Get("Authorization")
		f.lastModel, _ = body["model"].(string)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"cheap_can":{"type":"noul","noul":0.8}},"usage":{"input_tokens":10,"output_tokens":0}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func registry(t *testing.T, alias string, raw map[string]any) *bindings.BindingsRegistry {
	t.Helper()
	r := bindings.New()
	if err := r.UpsertModel(t.Context(), alias, raw); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}
	return r
}

func decisionModel(apiBase, apiKey string) map[string]any {
	return map[string]any{
		"model":             "jev-1.13.0",
		"api_base":          apiBase,
		"api_key":           apiKey,
		samapp.ModelTypeKey: samapp.DecisionModelType,
	}
}

func cheapCanRequest() decision.Request {
	return decision.Request{
		State: map[string]any{"task": "summarize this"},
		Questions: []decision.Question{{
			Key: "cheap_can", Type: decision.TypeBoolean,
			Instructions: "Can a small model complete this task?",
		}},
	}
}

func mustNew(t *testing.T, cfg Config) *Evaluator {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func isConfigError(err error) bool {
	var derr *decision.Error
	return errors.As(err, &derr) && derr.Kind == decision.FailureConfig
}

func TestNewRejectsAMissingAliasOrRegistry(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no alias":    {Bindings: bindings.New()},
		"no registry": {Alias: "jev"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); !isConfigError(err) {
				t.Errorf("New = %v, want a FailureConfig error", err)
			}
		})
	}
}

func TestEvaluateCallsTheModelTheAliasNames(t *testing.T) {
	jev := &fakeJev{}
	srv := jev.serve(t)
	e := mustNew(t, Config{Alias: "jev", Bindings: registry(t, "jev", decisionModel(srv.URL, "k1"))})

	res, err := e.Evaluate(t.Context(), cheapCanRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := res.Answers["cheap_can"].Probability; got != 0.8 {
		t.Errorf("cheap_can = %v, want 0.8", got)
	}
	jev.mu.Lock()
	defer jev.mu.Unlock()
	if jev.lastAuth != "Bearer k1" || jev.lastModel != "jev-1.13.0" {
		t.Errorf("called with auth %q model %q, want the registry's key and model", jev.lastAuth, jev.lastModel)
	}
}

func TestEvaluateLetsTheRequestsModelWin(t *testing.T) {
	jev := &fakeJev{}
	srv := jev.serve(t)
	e := mustNew(t, Config{Alias: "jev", Bindings: registry(t, "jev", decisionModel(srv.URL, "k1"))})

	req := cheapCanRequest()
	req.Model = "jev-2"
	if _, err := e.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	jev.mu.Lock()
	defer jev.mu.Unlock()
	if jev.lastModel != "jev-2" {
		t.Errorf("called model %q, want the request's jev-2 over the registered model", jev.lastModel)
	}
}

func TestEvaluateRejectsAliasProblemsAsConfigErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		reg     *bindings.BindingsRegistry
		wantErr string
	}{
		"unbound alias": {reg: bindings.New(), wantErr: "resolve decision model"},
		"chat model": {
			reg:     registry(t, "jev", map[string]any{"model": "openai/gpt-4o", "api_key": "k"}),
			wantErr: "is a chat model",
		},
		"no api key": {reg: registry(t, "jev", decisionModel("https://api.example.test", "")), wantErr: "api_key"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := mustNew(t, Config{Alias: "jev", Bindings: tc.reg}).Evaluate(t.Context(), cheapCanRequest())
			if !isConfigError(err) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want a FailureConfig mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestEvaluateLeavesFailedRegistryFetchesTransient(t *testing.T) {
	reg := bindings.New(bindings.WithFetcher(func(context.Context, string) (map[string]any, error) {
		return nil, errors.New("platform model-configs lookup: 503 Service Unavailable")
	}))
	_, err := mustNew(t, Config{Alias: "jev", Bindings: reg}).Evaluate(t.Context(), cheapCanRequest())
	if err == nil || isConfigError(err) {
		t.Errorf("err = %v, want a non-config error a retry can clear", err)
	}
}

func TestEvaluateRebuildsTheClientOnlyWhenTheEndpointChanges(t *testing.T) {
	jev := &fakeJev{}
	srv := jev.serve(t)
	reg := registry(t, "jev", decisionModel(srv.URL, "k1"))
	e := mustNew(t, Config{Alias: "jev", Bindings: reg})

	if _, err := e.Evaluate(t.Context(), cheapCanRequest()); err != nil {
		t.Fatalf("first Evaluate: %v", err)
	}
	first := e.client
	if _, err := e.Evaluate(t.Context(), cheapCanRequest()); err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	if e.client != first {
		t.Error("an unchanged endpoint rebuilt the client")
	}

	if err := reg.UpsertModel(t.Context(), "jev", decisionModel(srv.URL, "k2")); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	if _, err := e.Evaluate(t.Context(), cheapCanRequest()); err != nil {
		t.Fatalf("Evaluate after rotation: %v", err)
	}
	jev.mu.Lock()
	defer jev.mu.Unlock()
	if jev.lastAuth != "Bearer k2" || e.client == first {
		t.Errorf("after a key rotation: auth %q, client rebuilt %v; want the new key on a new client", jev.lastAuth, e.client != first)
	}
}

func openAIDecisionModel(apiBase, apiKey string) map[string]any {
	raw := decisionModel(apiBase, apiKey)
	raw["model"] = "gpt-6-luna"
	raw[samapp.DecisionProviderKey] = decision.ProviderOpenAI
	return raw
}

func TestEvaluateRebuildsTheClientWhenTheProviderChanges(t *testing.T) {
	jev := &fakeJev{}
	srv := jev.serve(t)
	reg := registry(t, "jev", decisionModel(srv.URL, "k1"))
	e := mustNew(t, Config{Alias: "jev", Bindings: reg})

	if _, err := e.Evaluate(t.Context(), cheapCanRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if _, ok := e.client.(*typesafe.Client); !ok {
		t.Fatalf("a binding without decision_provider built %T, want the TypeSafe client", e.client)
	}
	if err := reg.UpsertModel(t.Context(), "jev", openAIDecisionModel(srv.URL, "k1")); err != nil {
		t.Fatalf("switch provider: %v", err)
	}
	_, _ = e.Evaluate(t.Context(), cheapCanRequest())
	if _, ok := e.client.(*openai.Client); !ok {
		t.Errorf("after the binding moved to openai the evaluator holds %T", e.client)
	}
}

func TestEvaluateReportsTheModelConfigurationItRanOn(t *testing.T) {
	jev := &fakeJev{}
	srv := jev.serve(t)
	const configID = "0190c0de-0000-7000-8000-00000000d001"
	model := decisionModel(srv.URL, "k1")
	model[samapp.ModelConfigIDKey] = configID
	e := mustNew(t, Config{Alias: "jev", Bindings: registry(t, "jev", model)})

	res, err := e.Evaluate(t.Context(), cheapCanRequest())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.ModelConfigID != configID {
		t.Errorf("ModelConfigID = %q, want the binding's %q", res.ModelConfigID, configID)
	}
}
