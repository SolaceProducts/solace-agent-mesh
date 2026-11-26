// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type boundModels struct {
	registry *bindings.BindingsRegistry
	clients  map[string]*mockLLM
	ids      map[string]string
}

func bindModels(t *testing.T, f *spendFixture, keys map[string]string) *boundModels {
	t.Helper()
	b := &boundModels{clients: map[string]*mockLLM{}, ids: map[string]string{}}
	b.registry = bindings.New(bindings.WithClientBuilder(func(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
		model, _ := raw["model"].(string)
		client := newMockLLM()
		for range 4 {
			client.responses = append(client.responses, &llm.ChatResponse{Content: model, Usage: spendTestUsage})
		}
		b.clients[model] = client
		return client, nil
	}))
	for key, model := range keys {
		b.ids[key] = idgen.New()
		if err := b.registry.UpsertModel(t.Context(), key, map[string]any{"model": model, samapp.ModelConfigIDKey: b.ids[key]}); err != nil {
			t.Fatalf("bind %s: %v", key, err)
		}
	}
	f.agent.svc.(*mockServices).bindings = b.registry
	return b
}

func TestTaskSpendIsStampedWithTheConfigurationOfTheBindingThatServesIt(t *testing.T) {
	f := newSpendFixture(t, true)
	bound := bindModels(t, f, map[string]string{"agent-alias": "agent-model", "override-alias": "override-model"})
	state := spendTask(f.agent)

	if got := f.agent.taskSpendAttribution(t.Context(), state, spend.SurfaceAgent).ModelConfigID; got != bound.ids["agent-alias"] {
		t.Errorf("agent-turn model_config_id = %q, want the agent binding's %q", got, bound.ids["agent-alias"])
	}

	override, reason := f.agent.resolveTaskModelOverride(t.Context(),
		map[string]any{"model_override": map[string]any{"model_id": "override-alias"}}, f.agent.logger)
	if override == nil {
		t.Fatalf("override did not resolve: %s", reason)
	}
	state.setModelOverride(override)

	turn := f.agent.taskSpendAttribution(t.Context(), state, spend.SurfaceAgent)
	if turn.ModelKey != "override-alias" || turn.ModelConfigID != bound.ids["override-alias"] {
		t.Errorf("overridden agent turn = %s/%s, want override-alias/%s from the override's own binding",
			turn.ModelKey, turn.ModelConfigID, bound.ids["override-alias"])
	}
	compaction := f.agent.onAgentDefaultModel(t.Context(), f.agent.taskSpendAttribution(t.Context(), state, spend.SurfaceCompaction))
	if compaction.ModelKey != "agent-alias" || compaction.ModelConfigID != bound.ids["agent-alias"] {
		t.Errorf("compaction under an override = %s/%s, want agent-alias/%s",
			compaction.ModelKey, compaction.ModelConfigID, bound.ids["agent-alias"])
	}
}

func TestATaskIsStampedWithTheConfigurationItsAliasIsBoundToWhenTheCallIsMade(t *testing.T) {
	f := newSpendFixture(t, true)
	bound := bindModels(t, f, map[string]string{"agent-alias": "agent-model"})
	state := spendTask(f.agent)
	rebound := idgen.New()

	if err := bound.registry.UpsertModel(t.Context(), "agent-alias",
		map[string]any{"model": "agent-model", samapp.ModelConfigIDKey: rebound}); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	if got := f.agent.taskSpendAttribution(t.Context(), state, spend.SurfaceAgent).ModelConfigID; got != rebound {
		t.Errorf("model_config_id = %q, want %q, the configuration the alias now serves", got, rebound)
	}
}

func TestAToolNamedModelThatIsABoundKeyRunsOnThatBindingAndIsKeyedToIt(t *testing.T) {
	f := newSpendFixture(t, true)
	bound := bindModels(t, f, map[string]string{"agent-alias": "agent-model", "phase-alias": "phase-provider-model"})
	tool := &llm.SpendAttribution{UserSub: "user-1", Surface: string(spend.SurfaceTool), ToolName: "deep_research", TaskID: "task-1"}

	for _, model := range []string{"", "phase-alias", "provider-model"} {
		if _, err := f.agent.internalLLMCall(t.Context(), internalCallSinks{attribution: tool}, model, "sys", "user", 0.1); err != nil {
			t.Fatalf("internal call on %q: %v", model, err)
		}
	}

	phase := bound.clients["phase-provider-model"]
	if len(phase.calls) != 1 || phase.calls[0].Model != "" {
		t.Errorf("phase binding calls = %+v, want the one phase-alias call with no model, so the binding's own model is sent", phase.calls)
	}
	var unbound []string
	for _, req := range f.agent.svc.LLM("").(*mockLLM).calls {
		unbound = append(unbound, req.Model)
	}
	if len(unbound) != 1 || unbound[0] != "provider-model" {
		t.Errorf("Services.LLM served %v, want only the name no binding holds", unbound)
	}

	type booked struct{ key, configID, modelName string }
	got := map[booked]int{}
	for _, e := range f.recordedCount(t, 3) {
		got[booked{e.ModelKey, e.ModelConfigID, e.ModelName}]++
	}
	for _, want := range []booked{
		{"agent-alias", bound.ids["agent-alias"], "openai/agent-model"},
		{"phase-alias", bound.ids["phase-alias"], "openai/phase-provider-model"},
		{"", "", "openai/provider-model"},
	} {
		if got[want] != 1 {
			t.Errorf("events %v, want exactly one %+v", got, want)
		}
	}
}

func TestAManualCompactionIsStampedWithTheAgentBindingsConfiguration(t *testing.T) {
	f := newSpendFixture(t, true)
	bound := bindModels(t, f, map[string]string{"agent-alias": "agent-model"})
	seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")

	if _, err := f.agent.CompactSession(t.Context(), extensions.RequestInfo{UserID: "user-1", ClientID: "entry-1"},
		sessioncontrol.CompactParams{SessionID: "session-1", TargetPercent: 50, TaskID: "compact-task", TraceID: "compact-trace"}); err != nil {
		t.Fatalf("CompactSession: %v", err)
	}

	events := f.recordedCount(t, 1)
	if len(events) != 1 || events[0].ModelKey != "agent-alias" || events[0].ModelConfigID != bound.ids["agent-alias"] {
		t.Errorf("events = %+v, want one compaction on agent-alias stamped %s", events, bound.ids["agent-alias"])
	}
}

type unreadableBindings struct {
	*bindings.BindingsRegistry
	failing map[string]error
}

func (u unreadableBindings) ResolveRawOrFetch(ctx context.Context, alias string) (map[string]any, error) {
	if err, ok := u.failing[alias]; ok {
		return nil, err
	}
	return u.BindingsRegistry.ResolveRawOrFetch(ctx, alias)
}

const configIDUnavailableLog = "model configuration id unavailable; spend is filed without it"

func TestAnUnreadableBindingIsWarnedOfButAnUnboundAliasIsNot(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		warn bool
	}{
		"unreadable": {errors.New("platform unreachable"), true},
		"unbound":    {fmt.Errorf("%w: %q", bindings.ErrAliasUnbound, "agent-alias"), false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			bound := bindModels(t, f, map[string]string{"agent-alias": "agent-model"})
			f.agent.svc.(*mockServices).bindings = unreadableBindings{bound.registry, map[string]error{"agent-alias": tc.err}}
			logs := captureAgentLogs(f.agent)

			if got := f.agent.taskSpendAttribution(t.Context(), spendTask(f.agent), spend.SurfaceAgent).ModelConfigID; got != "" {
				t.Errorf("model_config_id = %q, want none when the binding cannot be read", got)
			}
			warnings := logs.records(configIDUnavailableLog)
			if warned := len(warnings) > 0 && warnings[0]["level"] == "WARN"; warned != tc.warn {
				t.Errorf("warned = %v, want %v:\n%s", warned, tc.warn, logs)
			}
			if tc.warn && warnings[0]["alias"] != "agent-alias" {
				t.Errorf("warning does not name the alias:\n%s", logs)
			}
		})
	}
}

func TestAToolNamedBindingWhoseConfigCannotBeReadRunsOnItWithoutAConfigurationID(t *testing.T) {
	f := newSpendFixture(t, true)
	bound := bindModels(t, f, map[string]string{"agent-alias": "agent-model", "phase-alias": "phase-provider-model"})
	f.agent.svc.(*mockServices).bindings = unreadableBindings{bound.registry, map[string]error{"phase-alias": errors.New("platform unreachable")}}
	logs := captureAgentLogs(f.agent)
	tool := &llm.SpendAttribution{UserSub: "user-1", Surface: string(spend.SurfaceTool), ToolName: "deep_research", TaskID: "task-1"}

	if _, err := f.agent.internalLLMCall(t.Context(), internalCallSinks{attribution: tool}, "phase-alias", "sys", "user", 0.1); err != nil {
		t.Fatalf("internal call: %v", err)
	}

	if calls := bound.clients["phase-provider-model"].calls; len(calls) != 1 {
		t.Errorf("phase binding calls = %+v, want the one call", calls)
	}
	events := f.recordedCount(t, 1)
	if len(events) != 1 || events[0].ModelKey != "phase-alias" || events[0].ModelConfigID != "" {
		t.Errorf("events = %+v, want one keyed to phase-alias with no configuration id", events)
	}
	if warnings := logs.records(configIDUnavailableLog); len(warnings) == 0 || warnings[0]["alias"] != "phase-alias" {
		t.Errorf("no warning names the binding whose configuration id was lost:\n%s", logs)
	}
}

func TestAToolNamedModelRoutedToABindingIsLoggedAtDebug(t *testing.T) {
	f := newSpendFixture(t, true)
	bindModels(t, f, map[string]string{"agent-alias": "agent-model", "phase-alias": "phase-provider-model"})
	logs := captureAgentLogs(f.agent)
	tool := &llm.SpendAttribution{UserSub: "user-1", Surface: string(spend.SurfaceTool), ToolName: "deep_research", TaskID: "task-1"}

	if _, err := f.agent.internalLLMCall(t.Context(), internalCallSinks{attribution: tool}, "phase-alias", "sys", "user", 0.1); err != nil {
		t.Fatalf("internal call: %v", err)
	}

	routed := logs.records("tool-named model routed to its binding")
	if len(routed) != 1 || routed[0]["level"] != "DEBUG" || routed[0]["alias"] != "phase-alias" || routed[0]["boundModel"] != "phase-provider-model" {
		t.Errorf("routing log = %+v, want one DEBUG record naming the alias and its bound model:\n%s", routed, logs)
	}
}

func TestAResumedTaskIsStampedWithTheConfigurationOfItsModelOverride(t *testing.T) {
	f := newSpendFixture(t, true)
	bound := bindModels(t, f, map[string]string{"agent-alias": "agent-model", "override-alias": "override-model"})
	state := spendTask(f.agent)
	override, reason := f.agent.resolveTaskModelOverride(t.Context(),
		map[string]any{"model_override": map[string]any{"model_id": "override-alias"}}, f.agent.logger)
	if override == nil {
		t.Fatalf("override did not resolve: %s", reason)
	}
	state.setModelOverride(override)
	checkpoint, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	rebound := idgen.New()
	if err = bound.registry.UpsertModel(t.Context(), "override-alias",
		map[string]any{"model": "override-model", samapp.ModelConfigIDKey: rebound}); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	resumed, err := unmarshalTaskState(checkpoint)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	f.agent.rehydrateModelOverride(t.Context(), resumed, f.agent.logger)

	turn := f.agent.taskSpendAttribution(t.Context(), resumed, spend.SurfaceAgent)
	if turn.ModelKey != "override-alias" || turn.ModelConfigID != rebound {
		t.Errorf("resumed agent turn = %s/%s, want override-alias/%s, the configuration the alias is bound to on resume",
			turn.ModelKey, turn.ModelConfigID, rebound)
	}
}
