// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func bindingsFor(bindClient llm.Client, aliases ...string) *bindings.BindingsRegistry {
	known := make(map[string]struct{}, len(aliases))
	for _, a := range aliases {
		known[a] = struct{}{}
	}
	return bindings.New(
		bindings.WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
			return bindClient, nil
		}),
		bindings.WithFetcher(func(_ context.Context, alias string) (map[string]any, error) {
			if _, ok := known[alias]; !ok {
				return nil, nil
			}
			return map[string]any{"model": alias}, nil
		}),
	)
}

func coreWithServices(t *testing.T, opts ...runtime.Option) *entrypoint.GatewayCore {
	t.Helper()
	core := entrypoint.NewGatewayCore(NewHTTPSSEAdapter(), map[string]any{"gateway_id": "test-gw"})
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, append([]runtime.Option{runtime.WithLogger(slog.Default())}, opts...)...)
	core.SetServices(rt.Services())
	return core
}

func TestResolveGeneralModel_PrefersBindings(t *testing.T) {
	bindClient := &mockLLMClient{response: "from-bindings"}
	defaultClient := &mockLLMClient{response: "from-default"}
	core := coreWithServices(t,
		runtime.WithBindings(bindingsFor(bindClient, "general")),
		runtime.WithDefaultLLM(defaultClient),
	)

	got := resolveGeneralModel(t.Context(), core).client
	if got == nil || got == llm.Client(defaultClient) {
		t.Fatalf("expected the general binding, got %v", got)
	}
	resp, err := got.ChatCompletion(t.Context(), &llm.ChatRequest{})
	if err != nil || resp.Content != "from-bindings" {
		t.Fatalf("call through the resolved client = %v, %v; want the bindings client's answer", resp, err)
	}
}

func TestResolveGeneralModel_FallsBackToDefault(t *testing.T) {
	defaultClient := &mockLLMClient{response: "from-default"}
	core := coreWithServices(t,
		runtime.WithBindings(bindingsFor(&mockLLMClient{}, "planning")),
		runtime.WithDefaultLLM(defaultClient),
	)

	got := resolveGeneralModel(t.Context(), core).client
	if got == nil {
		t.Fatal("expected the default client fallback, got nil")
	}
	resp, err := got.ChatCompletion(t.Context(), &llm.ChatRequest{})
	if err != nil || resp.Content != "from-default" {
		t.Fatalf("call through the resolved client = %v, %v; want the default client's answer", resp, err)
	}
}

func TestResolveGeneralModel_NoLLMReturnsNil(t *testing.T) {
	core := coreWithServices(t)

	if got := resolveGeneralModel(t.Context(), core).client; got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	if gatewayHasLLM(core) {
		t.Fatal("expected gatewayHasLLM to report false when no LLM configured")
	}
}

func TestGatewayHasLLM_BindingsOnly(t *testing.T) {
	core := coreWithServices(t, runtime.WithBindings(bindingsFor(&mockLLMClient{}, "general")))
	if !gatewayHasLLM(core) {
		t.Fatal("expected gatewayHasLLM true when only a bindings registry is present")
	}
}

func TestTaskBuilderAssistant_NilClient(t *testing.T) {
	a := &taskBuilderAssistant{logger: slog.Default()}

	if _, err := a.processMessage(t.Context(), &TaskBuilderChatRequest{Message: "schedule something"}, generalModel{}, nil); err == nil {
		t.Fatal("expected processMessage to error when no LLM is available")
	}

	resp := a.validateConflict(t.Context(), &ValidateConflictRequest{
		Instructions:       "run the daily report",
		ScheduleType:       "cron",
		ScheduleExpression: "0 9 * * *",
	}, generalModel{}, nil)
	if resp.Conflict {
		t.Fatal("expected validateConflict to fail open (Conflict=false) with no LLM")
	}
}
