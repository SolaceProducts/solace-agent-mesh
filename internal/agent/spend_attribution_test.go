// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSpendTrigger(t *testing.T) {
	cases := []struct {
		name     string
		props    a2a.UserProperties
		origin   SpendOrigin
		wantKind spend.TriggerKind
		wantID   string
	}{
		{"workflow dispatch wins over the delegating agent", a2a.UserProperties{DelegatingAgentName: "caller"}, SpendOrigin{WorkflowID: "wf-1"}, spend.TriggerWorkflow, "wf-1"},
		{"named workflow dispatch", a2a.UserProperties{DelegatingAgentName: "nightly"}, SpendOrigin{WorkflowName: "nightly"}, spend.TriggerWorkflow, "nightly"},
		{"plain-named peer", a2a.UserProperties{DelegatingAgentName: "caller"}, SpendOrigin{}, spend.TriggerAgent, "caller"},
		{"deployed peer is keyed by its id", a2a.UserProperties{DelegatingAgentName: deployedAgentInstance}, SpendOrigin{}, spend.TriggerAgent, deployedAgentID},
		{"peer wins over the gateway trigger", a2a.UserProperties{DelegatingAgentName: "caller", Trigger: a2a.TriggerScheduled}, SpendOrigin{ScheduledTaskID: "s-1"}, spend.TriggerAgent, "caller"},
		{"interactive", a2a.UserProperties{Trigger: a2a.TriggerInteractive}, SpendOrigin{}, spend.TriggerChat, ""},
		{"scheduled carries the scheduled task", a2a.UserProperties{Trigger: a2a.TriggerScheduled}, SpendOrigin{ScheduledTaskID: "s-1"}, spend.TriggerSchedule, "s-1"},
		{"scheduled from an older scheduler has no task id", a2a.UserProperties{Trigger: a2a.TriggerScheduled}, SpendOrigin{}, spend.TriggerSchedule, ""},
		{"eval", a2a.UserProperties{Trigger: a2a.TriggerEval}, SpendOrigin{}, spend.TriggerEval, ""},
		{"event", a2a.UserProperties{Trigger: a2a.TriggerEvent}, SpendOrigin{}, spend.TriggerEvent, ""},
		{"unknown trigger is left blank", a2a.UserProperties{Trigger: a2a.TriggerUnknown}, SpendOrigin{}, "", ""},
		{"no trigger is left blank", a2a.UserProperties{}, SpendOrigin{}, "", ""},
		{"a trigger newer than this agent is left blank", a2a.UserProperties{Trigger: "some_future_trigger"}, SpendOrigin{}, "", ""},
		{"a delegation whose origin went unrecorded is left blank", a2a.UserProperties{DelegatingAgentName: "caller", Trigger: a2a.TriggerInteractive}, SpendOrigin{Unrecorded: true}, "", ""},
		{"a gateway task whose origin went unrecorded keeps its trigger", a2a.UserProperties{Trigger: a2a.TriggerInteractive}, SpendOrigin{Unrecorded: true}, spend.TriggerChat, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, id := SpendTrigger(tc.props, tc.origin)
			if kind != tc.wantKind || id != tc.wantID {
				t.Errorf("SpendTrigger = %s/%q, want %s/%q", kind, id, tc.wantKind, tc.wantID)
			}
		})
	}
}

func TestTaskModelRefKey(t *testing.T) {
	override := &ResolvedModelOverride{Alias: "task-override", Config: &llm.BifrostConfig{}}
	cases := []struct {
		name       string
		agentAlias string
		override   *ResolvedModelOverride
		want       string
	}{
		{"agent alias", "agent-alias", nil, "agent-alias"},
		{"task override in force", "agent-alias", override, "task-override"},
		{"inline model has no key", "", nil, ""},
		{"override that did not re-resolve falls back to the agent alias", "agent-alias", &ResolvedModelOverride{Alias: "task-override"}, "agent-alias"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newSpendFixture(t, false).agent
			agent.cfg.ModelAlias = tc.agentAlias
			state := &TaskExecutionState{}
			state.setModelOverride(tc.override)
			if got, _ := agent.taskModelRef(t.Context(), state); got != tc.want {
				t.Errorf("taskModelRef key = %q, want %q", got, tc.want)
			}
		})
	}
}

func spendTask(agent *Agent) *TaskExecutionState {
	state := newTestTaskState(agent)
	state.UserProps = a2a.UserProperties{
		UserID: "user-1", ClientID: "entry-1", EntrypointName: "entry-1", TraceID: "trace-1", Trigger: a2a.TriggerInteractive,
		RootKind: a2a.RootKindAgent, RootID: "root-agent",
	}
	state.TaskID = "task-1"
	state.OriginalSessionID = "session-1"
	return state
}

func TestAutoCompactionIsBookedOnTheAgentModelWhateverTheTaskOverride(t *testing.T) {
	f := newSpendFixture(t, true)
	f.agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	state := spendTask(f.agent)
	state.setModelOverride(&ResolvedModelOverride{Alias: "task-override", Config: &llm.BifrostConfig{}})
	sess := seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")
	rc := &taskRunContext{sess: sess, logger: f.agent.logger}

	if _, outcome, err := f.agent.handleContextLimitRetry(t.Context(), state, rc); err != nil || outcome != loopContinue {
		t.Fatalf("compaction retry = %v, %v", outcome, err)
	}

	events := f.recordedCount(t, 1)
	if len(events) != 1 {
		t.Fatalf("got %d events, want the one summarising call: %+v", len(events), events)
	}
	e := events[0]
	if e.Surface != spend.SurfaceCompaction || e.ModelKey != "agent-alias" || e.UserSub != "user-1" ||
		e.TaskID != "task-1" || e.TraceID != "trace-1" || e.SessionID != "session-1" ||
		e.EntrypointName != "entry-1" || e.TriggerKind != spend.TriggerChat ||
		e.RootKind != spend.RootAgent || e.RootName != "root-agent" || e.AgentName != "spend-agent" {
		t.Errorf("auto-compaction event misattributed: %+v", e)
	}
}

func TestSpendEventsCarryTheChannelTheTaskEnteredThrough(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channel     string
		wantChannel spend.Channel
	}{
		{"a known channel", httputil.SurfaceSlack, spend.Channel(httputil.SurfaceSlack)},
		{"no channel", "", ""},
		{"a stray channel is passed through verbatim", "carrier-pigeon", "carrier-pigeon"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			f.agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			state := spendTask(f.agent)
			state.UserProps.Channel = tc.channel
			sess := seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")
			rc := &taskRunContext{sess: sess, logger: f.agent.logger}

			if _, _, err := f.agent.handleContextLimitRetry(t.Context(), state, rc); err != nil {
				t.Fatalf("compaction retry: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 || events[0].Channel != tc.wantChannel {
				t.Fatalf("events = %+v, want one with channel %q", events, tc.wantChannel)
			}
			if _, err := events[0].Sanitize(time.Now()); err != nil {
				t.Errorf("event is not valid: %v", err)
			}
		})
	}
}

func TestSpendEventsAreBookedAgainstThePlatformRowsTheGatewayStamped(t *testing.T) {
	const userUUID, entrypointUUID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40", "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c41"
	for _, tc := range []struct {
		name   string
		origin a2a.Origin
		want   spend.Event
	}{
		{"both stamped", a2a.Origin{UserUUID: userUUID, EntrypointUUID: entrypointUUID},
			spend.Event{UserID: userUUID, EntrypointID: entrypointUUID}},
		{"none stamped falls back to the identity and the client", a2a.Origin{},
			spend.Event{UserSub: "user-1", EntrypointName: "entry-1"}},
		{"a stamp that is no canonical uuid falls back", a2a.Origin{UserUUID: "alice@example.com", EntrypointUUID: "{" + entrypointUUID + "}"},
			spend.Event{UserSub: "user-1", EntrypointName: "entry-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			f.agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			state := spendTask(f.agent)
			state.UserProps.Origin = tc.origin
			sess := seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")
			rc := &taskRunContext{sess: sess, logger: f.agent.logger}

			if _, _, err := f.agent.handleContextLimitRetry(t.Context(), state, rc); err != nil {
				t.Fatalf("compaction retry: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 || !sameUserAndEntrypoint(events[0], tc.want) {
				t.Fatalf("events = %+v, want one booked to user %q/%q via entrypoint %q/%q",
					events, tc.want.UserID, tc.want.UserSub, tc.want.EntrypointID, tc.want.EntrypointName)
			}
		})
	}
}

func TestSpendEventsCarryTheTaskThatDelegatedTheRun(t *testing.T) {
	for _, tc := range []struct {
		name         string
		origin       SpendOrigin
		wantParentID string
	}{
		{"delegated task", SpendOrigin{ParentTaskID: "parent-task"}, "parent-task"},
		{"root task", SpendOrigin{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			f.agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			state := spendTask(f.agent)
			state.SpendOrigin = tc.origin
			sess := seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")
			rc := &taskRunContext{sess: sess, logger: f.agent.logger}

			if _, _, err := f.agent.handleContextLimitRetry(t.Context(), state, rc); err != nil {
				t.Fatalf("compaction retry: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 || events[0].ParentTaskID != tc.wantParentID || events[0].TaskID != "task-1" {
				t.Errorf("events = %+v, want one for task-1 with parent %q", events, tc.wantParentID)
			}
		})
	}
}

type probeCall struct {
	run func(*tool.Context) error
}

func TestToolInternalLLMCallsAreBookedToTheInvokingTool(t *testing.T) {
	f := newSpendFixture(t, true)
	state := spendTask(f.agent)
	toolCtx := &tool.Context{AgentName: f.agent.cfg.Name, UserID: "user-1", SessionID: "session-1"}
	f.agent.wireInternalLLMCallbacks(toolCtx, nil, &TaskExecutionState{}, f.agent.taskSpendAttribution(t.Context(), state, spend.SurfaceTool))

	probes := map[string]probeCall{
		"alpha": {func(c *tool.Context) error {
			if _, err := c.InternalLLMCall(t.Context(), "sys", "user", 0.1); err != nil {
				return err
			}
			_, err := c.InternalLLMCallWithModel(t.Context(), "phase-model", "sys", "user", 0.1)
			return err
		}},
		"beta": {func(c *tool.Context) error {
			_, err := c.InternalLLMCallMultimodal(t.Context(), "sys", []tool.MultimodalPart{tool.TextPart{Text: "hi"}}, tool.InternalLLMCallMultimodalOpts{})
			return err
		}},
	}
	var failures sync.Map
	for name, probe := range probes {
		if err := f.agent.Tools().Register(&testBuiltinTool{name: name, description: "probe",
			execFn: func(_ context.Context, _ map[string]any, c *tool.Context) (*tool.Result, error) {
				if err := probe.run(c); err != nil {
					failures.Store(name, err)
				}
				return tool.OK("done"), nil
			}}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	f.agent.dispatchToolCallsWithStatus(t.Context(), []llm.ToolCall{
		{ID: "c1", Name: "alpha", Arguments: `{}`}, {ID: "c2", Name: "beta", Arguments: `{}`},
	}, toolCtx, NewStatusPublisher(f.broker, StatusPublisherConfig{TaskID: "task-1"}, f.agent.logger), "")
	failures.Range(func(name, err any) bool { t.Errorf("tool %v: %v", name, err); return true })

	type booked struct{ tool, modelKey, modelName string }
	var got []booked
	for _, e := range f.recordedCount(t, 3) {
		if e.Surface != spend.SurfaceTool || e.TaskID != "task-1" || e.UserSub != "user-1" || e.AgentName != "spend-agent" {
			t.Errorf("tool event misattributed: %+v", e)
		}
		got = append(got, booked{e.ToolName, e.ModelKey, e.ModelName})
	}
	sort.Slice(got, func(i, j int) bool { return got[i].tool+got[i].modelName < got[j].tool+got[j].modelName })
	want := []booked{
		{"alpha", "agent-alias", "openai/agent-model"},
		{"alpha", "", "openai/phase-model"},
		{"beta", "agent-alias", "openai/agent-model"},
	}
	if len(got) != len(want) {
		t.Fatalf("booked %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("booked[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAReplayedToolsInternalLLMCallIsBookedToThatTool(t *testing.T) {
	f := newSpendFixture(t, true)
	if err := f.agent.Tools().Register(&testBuiltinTool{name: "replayed", description: "probe",
		execFn: func(_ context.Context, _ map[string]any, c *tool.Context) (*tool.Result, error) {
			if _, err := c.InternalLLMCall(t.Context(), "sys", "user", 0.1); err != nil {
				return tool.Error(err.Error()), nil
			}
			return tool.OK("done"), nil
		}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	state := spendTask(f.agent)
	pc := &PendingToolCall{ToolCallID: "call-r1", ToolName: "replayed", Kind: PendingCallAuth}

	result, err := f.agent.dispatchToolSingle(t.Context(), state, pc)
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("replay: result=%+v err=%v", result, err)
	}

	events := f.recordedCount(t, 1)
	if len(events) != 1 {
		t.Fatalf("got %d events, want the tool's one internal call: %+v", len(events), events)
	}
	if e := events[0]; e.Surface != spend.SurfaceTool || e.ToolName != "replayed" || e.TaskID != "task-1" || e.UserSub != "user-1" {
		t.Errorf("event = %+v, want surface tool on tool replayed for task-1", e)
	}
}

func TestStructuredFinalizerCallsAreBookedOnTheModelEachRequestRuns(t *testing.T) {
	const savedArtifactTurn = "«««save_artifact: filename=\"summary.json\" mime_type=\"application/json\"\n{\"summary\": \"ok\"}\n»»»\n"
	usageOf := func(completion int) *llm.Usage {
		return &llm.Usage{PromptTokens: 100, CompletionTokens: completion, TotalTokens: 100 + completion}
	}
	f := newSpendFixtureAnswering(t, true,
		&llm.ChatResponse{Content: "an answer without the result embed", Usage: usageOf(11)},
		&llm.ChatResponse{Content: savedArtifactTurn, Usage: usageOf(22)},
		&llm.ChatResponse{Content: "still no result embed", Usage: usageOf(33)},
	)
	f.agent.cfg.MaxLLMCalls = 1
	f.agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: artmem.New()}
	f.agent.svc.(*mockServices).modelResolver = &stubResolver{resp: map[string]any{"model": "openai/override-model"}}
	msg := a2a.Message{
		Kind: "message", MessageID: "msg-1", Role: a2a.RoleUser, ContextID: "ctx-1",
		Metadata: map[string]any{"model_override": map[string]any{"model_id": "override-alias"}},
		Parts: []a2a.Part{
			a2a.NewTextPart("summarize the quarter"),
			a2a.NewDataPart(map[string]any{"type": "structured_invocation_request", "workflow_name": "wf", "node_id": "step_1"}),
		},
	}

	if _, err := f.agent.HandleTask(t.Context(), "task-1", msg, a2a.UserProperties{ClientID: "client", UserID: "user-1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	modelKeyByCall := map[int64]string{}
	events := f.recordedCount(t, 3)
	for _, e := range events {
		if _, dup := modelKeyByCall[e.Tokens.Output]; dup {
			t.Errorf("the call with %d output tokens was booked twice", e.Tokens.Output)
		}
		modelKeyByCall[e.Tokens.Output] = e.ModelKey
		if e.Surface != spend.SurfaceAgent {
			t.Errorf("the call with %d output tokens was booked on surface %q, want the agent surface", e.Tokens.Output, e.Surface)
		}
	}
	if len(events) != 3 {
		t.Errorf("booked %d calls, want the 3 the finalizer made", len(events))
	}
	want := map[int64]string{
		11: "override-alias",
		22: "override-alias",
		33: "override-alias",
	}
	for output, key := range want {
		if got, ok := modelKeyByCall[output]; !ok || got != key {
			t.Errorf("call with %d output tokens booked on %q (recorded=%v), want %q", output, got, ok, key)
		}
	}
}

func TestAnInternalLLMCallOnAContextNoDispatchBoundIsBookedWithoutAToolName(t *testing.T) {
	f := newSpendFixture(t, true)
	toolCtx := &tool.Context{}
	f.agent.wireInternalLLMCallbacks(toolCtx, nil, &TaskExecutionState{}, f.agent.taskSpendAttribution(t.Context(), spendTask(f.agent), spend.SurfaceTool))

	if _, err := toolCtx.InternalLLMCall(t.Context(), "sys", "user", 0.1); err != nil {
		t.Fatalf("call: %v", err)
	}

	events := f.recordedCount(t, 1)
	if len(events) != 1 {
		t.Fatalf("got %d events, want the one call: %+v", len(events), events)
	}
	if e := events[0]; e.Surface != spend.SurfaceTool || e.ToolName != "" || e.TaskID != "task-1" || e.UserSub != "user-1" {
		t.Errorf("event = %+v, want task-1's call on the tool surface with no tool name", e)
	}
}

func TestManualCompactionIsBookedOnlyWhenTheGatewayMintedTheIDs(t *testing.T) {
	cases := []struct {
		name            string
		taskID, traceID string
		wantRecorded    bool
	}{
		{"gateway minted both", "compact-task", "compact-trace", true},
		{"older gateway sent neither", "", "", false},
		{"task id only", "compact-task", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")

			_, err := f.agent.CompactSession(t.Context(),
				extensions.RequestInfo{UserID: "user-1", ClientID: "entry-1"},
				sessioncontrol.CompactParams{SessionID: "session-1", TargetPercent: 50, TaskID: tc.taskID, TraceID: tc.traceID})
			if err != nil {
				t.Fatalf("CompactSession: %v", err)
			}

			want := 0
			if tc.wantRecorded {
				want = 1
			}
			events := f.recordedCount(t, want)
			if !tc.wantRecorded {
				if len(events) != 0 {
					t.Errorf("recorded %+v without the ids the ledger needs", events)
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1: %+v", len(events), events)
			}
			e := events[0]
			if e.Surface != spend.SurfaceCompaction || e.TaskID != "compact-task" || e.TraceID != "compact-trace" ||
				e.UserSub != "user-1" || e.EntrypointName != "entry-1" || e.SessionID != "session-1" ||
				e.TriggerKind != spend.TriggerChat || e.RootKind != spend.RootAgent || e.RootName != "spend-agent" ||
				e.AgentName != "spend-agent" || e.ModelKey != "agent-alias" || e.ParentTaskID != "" {
				t.Errorf("manual compaction event misattributed: %+v", e)
			}
		})
	}
}

func TestWithRootAdoptsOnlyAWholeRoot(t *testing.T) {
	agent := New(AgentConfig{Name: "self"})
	cases := []struct {
		name             string
		in               a2a.UserProperties
		wantKind, wantID string
		wantNode         string
	}{
		{"no root", a2a.UserProperties{}, a2a.RootKindAgent, "self", ""},
		{"whole root", a2a.UserProperties{RootKind: a2a.RootKindWorkflow, RootID: "wf", RootNodeID: "n1"}, a2a.RootKindWorkflow, "wf", "n1"},
		{"kind without an id", a2a.UserProperties{RootKind: a2a.RootKindWorkflow, RootNodeID: "n1"}, a2a.RootKindAgent, "self", ""},
		{"id without a kind", a2a.UserProperties{RootID: "wf", RootNodeID: "n1"}, a2a.RootKindAgent, "self", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agent.withRoot(tc.in)
			if got.RootKind != tc.wantKind || got.RootID != tc.wantID || got.RootNodeID != tc.wantNode {
				t.Errorf("root = %s/%s/%s, want %s/%s/%s", got.RootKind, got.RootID, got.RootNodeID, tc.wantKind, tc.wantID, tc.wantNode)
			}
		})
	}
}

func TestAnAnonymousManualCompactionIsBookedToTheUserWhoseSessionItCompacts(t *testing.T) {
	f := newSpendFixture(t, true)
	seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "anonymous", "session-1")

	if _, err := f.agent.CompactSession(t.Context(), extensions.RequestInfo{ClientID: "entry-1"},
		sessioncontrol.CompactParams{SessionID: "session-1", TargetPercent: 50, TaskID: "compact-task", TraceID: "compact-trace"}); err != nil {
		t.Fatalf("CompactSession: %v", err)
	}

	events := f.recordedCount(t, 1)
	if len(events) != 1 || events[0].UserSub != "anonymous" {
		t.Errorf("events = %+v, want the compaction booked to anonymous, whose session it compacted", events)
	}
}

func TestAToolsInternalLLMCallOnATasksFirstTurnIsBookedToTheTaskOnTheToolSurface(t *testing.T) {
	usage := func(completion int) *llm.Usage {
		return &llm.Usage{PromptTokens: 100, CompletionTokens: completion, TotalTokens: 100 + completion}
	}
	f := newSpendFixtureAnswering(t, true,
		&llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "summarizer", Arguments: `{}`}}, FinishReason: "tool_calls", Usage: usage(11)},
		&llm.ChatResponse{Content: "summary", Usage: usage(22)},
		&llm.ChatResponse{Content: "final answer", FinishReason: "stop", Usage: usage(33)},
	)
	if err := f.agent.Tools().Register(&testBuiltinTool{name: "summarizer", description: "probe",
		execFn: func(_ context.Context, _ map[string]any, c *tool.Context) (*tool.Result, error) {
			if _, err := c.InternalLLMCall(t.Context(), "sys", "user", 0.1); err != nil {
				return tool.Error(err.Error()), nil
			}
			return tool.OK("done"), nil
		}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	msg := a2a.Message{Kind: "message", MessageID: "msg-1", Role: a2a.RoleUser, ContextID: "ctx-1", Parts: []a2a.Part{a2a.NewTextPart("summarize")}}

	if _, err := f.agent.HandleTask(t.Context(), "task-1", msg, a2a.UserProperties{
		ClientID: "entry-1", UserID: "user-1", TraceID: "trace-1", Trigger: a2a.TriggerInteractive,
	}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	var toolRows []spend.Event
	surfaceByOutput := map[int64]spend.Surface{}
	for _, e := range f.recordedCount(t, 3) {
		surfaceByOutput[e.Tokens.Output] = e.Surface
		if e.Surface == spend.SurfaceTool {
			toolRows = append(toolRows, e)
		}
	}
	if want := map[int64]spend.Surface{11: spend.SurfaceAgent, 22: spend.SurfaceTool, 33: spend.SurfaceAgent}; !maps.Equal(surfaceByOutput, want) {
		t.Errorf("surface by call = %v, want the loop's two turns on the agent surface and the tool's call on the tool surface", surfaceByOutput)
	}
	if len(toolRows) != 1 {
		t.Fatalf("tool-surface events = %+v, want the summarizer's one call", toolRows)
	}
	if e := toolRows[0]; e.ToolName != "summarizer" || e.UserSub != "user-1" || e.TaskID != "task-1" ||
		e.TraceID != "trace-1" || e.EntrypointName != "entry-1" || e.TriggerKind != spend.TriggerChat ||
		e.RootKind != spend.RootAgent || e.RootName != "spend-agent" || e.Tokens.Output != 22 {
		t.Errorf("tool event = %+v, want the summarizer's call booked to task-1's user, trace and root", e)
	}
}

func TestADeployedAgentsTaskIsBookedAgainstItsRows(t *testing.T) {
	const workflowID = "0190c7a2-5f4e-7b3a-9d21-3c8e5a1b7f61"
	agent := New(AgentConfig{Name: deployedAgentInstance, ModelAlias: "agent-alias"})
	if err := agent.Init(t.Context(), nil, newMockServices(newMockLLM(), newMemSessionStore())); err != nil {
		t.Fatalf("Init: %v", err)
	}
	state := spendTask(agent)
	state.UserProps.RootKind, state.UserProps.RootID = a2a.RootKindWorkflow, workflowID
	state.SpendOrigin = SpendOrigin{WorkflowID: workflowID, WorkflowNodeID: "node-1"}

	got := agent.taskSpendAttribution(t.Context(), state, spend.SurfaceAgent)

	if got.AgentID != deployedAgentID || got.AgentName != "" ||
		got.WorkflowID != workflowID || got.WorkflowName != "" ||
		got.RootID != workflowID || got.RootName != "" ||
		got.UserSub != "user-1" || got.UserID != "" || got.EntrypointName != "entry-1" || got.EntrypointID != "" {
		t.Errorf("attribution = %+v, want the deployed rows by id and the user and entrypoint by name", got)
	}
}

func TestTaskSpendIsBookedToTheVerifiedUser(t *testing.T) {
	cases := []struct {
		name     string
		identity *auth.Identity
		want     string
	}{
		{"verified identity outranks the unsigned userId", &auth.Identity{UserID: "verified-user"}, "verified-user"},
		{"no authenticator falls back to the userId", nil, "user-1"},
		{"a token without a subject falls back to the userId", &auth.Identity{}, "user-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			f.agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			state := spendTask(f.agent)
			state.Identity = tc.identity
			sess := seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")

			if _, _, err := f.agent.handleContextLimitRetry(t.Context(), state, &taskRunContext{sess: sess, logger: f.agent.logger}); err != nil {
				t.Fatalf("compaction retry: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 || events[0].UserSub != tc.want {
				t.Errorf("events = %+v, want one booked to %q", events, tc.want)
			}
		})
	}
}

func TestTaskSpendIsBookedToTheSignedUserRowNotTheUnsignedStamp(t *testing.T) {
	const signedRow, stampedRow = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"
	cases := []struct {
		name     string
		stamped  string
		identity *auth.Identity
		wantID   string
		wantSub  string
	}{
		{"a verified identity books its signed row", stampedRow, &auth.Identity{UserID: "user-1", PlatformUserID: signedRow}, signedRow, ""},
		{"a stamp naming another user's row is not booked over the verified subject", stampedRow, &auth.Identity{UserID: "user-1"}, "", "user-1"},
		{"a system-initiated task books its signed system row though it runs for another user",
			rbac.DefaultSystemUserID, &auth.Identity{UserID: "sentinel-monitor", PlatformUserID: rbac.DefaultSystemUserID}, rbac.DefaultSystemUserID, ""},
		{"a signed row that is not a UUID books the verified subject", "", &auth.Identity{UserID: "verified-user", PlatformUserID: "not-a-uuid"}, "", "verified-user"},
		{"a token without the claim books the verified subject by name", "", &auth.Identity{UserID: "verified-user"}, "", "verified-user"},
		{"no authenticator books the unsigned stamp", stampedRow, nil, stampedRow, ""},
		{"no authenticator and no stamp books the userId", "", nil, "", "user-1"},
		{"no authenticator and a stamp that is not a UUID books the userId", "not-a-uuid", nil, "", "user-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := spendTask(newSpendFixture(t, false).agent)
			state.UserProps.UserUUID = tc.stamped
			state.Identity = tc.identity

			id, sub := spend.SpendUser(state.UserProps, state.Identity)

			if id != tc.wantID || sub != tc.wantSub {
				t.Errorf("SpendUser = %q/%q, want %q/%q", id, sub, tc.wantID, tc.wantSub)
			}
		})
	}
}

func TestAResumedTaskIsBookedToTheSignedUserRowItsCheckpointRecorded(t *testing.T) {
	const signedRow = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	cases := []struct {
		name       string
		checkpoint func(t *testing.T, blob []byte) []byte
		want       spend.Event
	}{
		{"checkpoint that records the signed row", func(_ *testing.T, blob []byte) []byte { return blob },
			spend.Event{UserID: signedRow, EntrypointName: "entry-1"}},
		{"checkpoint written before the signed row was recorded", withoutIdentityPlatformUserID,
			spend.Event{UserSub: "verified-user", EntrypointName: "entry-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			f.agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			original := spendTask(f.agent)
			original.Identity = &auth.Identity{UserID: "verified-user", PlatformUserID: signedRow}
			state := resumeTwice(t, original, tc.checkpoint)
			sess := seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")

			if _, _, err := f.agent.handleContextLimitRetry(t.Context(), state, &taskRunContext{sess: sess, logger: f.agent.logger}); err != nil {
				t.Fatalf("compaction retry: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 || !sameUserAndEntrypoint(events[0], tc.want) {
				t.Errorf("events = %+v, want one booked to user %q/%q", events, tc.want.UserID, tc.want.UserSub)
			}
		})
	}
}

func TestATaskResumedFromACheckpointKeepsOnlyTheSpendOriginItRecorded(t *testing.T) {
	recordedOrigin := SpendOrigin{WorkflowName: "nightly", WorkflowNodeID: "node-1", ParentTaskID: "parent-task"}
	cases := []struct {
		name       string
		checkpoint func(t *testing.T, blob []byte) []byte
		wantKind   spend.TriggerKind
		wantID     string
		wantOrigin SpendOrigin
	}{
		{"checkpoint that records the origin", func(_ *testing.T, blob []byte) []byte { return blob }, spend.TriggerWorkflow, "nightly", recordedOrigin},
		{"checkpoint written before the origin was recorded", withoutSpendOrigin, "", "", SpendOrigin{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			f.agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			original := spendTask(f.agent)
			original.UserProps.DelegatingAgentName = "nightly"
			original.SpendOrigin = recordedOrigin
			state := resumeTwice(t, original, tc.checkpoint)
			sess := seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")

			if _, _, err := f.agent.handleContextLimitRetry(t.Context(), state, &taskRunContext{sess: sess, logger: f.agent.logger}); err != nil {
				t.Fatalf("compaction retry: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1: %+v", len(events), events)
			}
			e := events[0]
			if e.TriggerKind != tc.wantKind || e.TriggerID != tc.wantID || e.WorkflowName != tc.wantOrigin.WorkflowName ||
				e.WorkflowNodeID != tc.wantOrigin.WorkflowNodeID || e.ParentTaskID != tc.wantOrigin.ParentTaskID {
				t.Errorf("event = %+v, want trigger %q/%q and origin %+v", e, tc.wantKind, tc.wantID, tc.wantOrigin)
			}
		})
	}
}

func TestManualCompactionIsBookedAgainstTheOriginTheGatewaySent(t *testing.T) {
	const (
		userRow    = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
		gatewayRow = "0197a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"
	)
	cases := []struct {
		name   string
		origin a2a.Origin
		want   spend.Event
	}{
		{"origin with row ids", a2a.Origin{Channel: "ui", UserUUID: userRow, EntrypointUUID: gatewayRow},
			spend.Event{UserID: userRow, EntrypointID: gatewayRow, Channel: "ui"}},
		{"older gateway sent no origin", a2a.Origin{},
			spend.Event{UserSub: "user-1", EntrypointName: "entry-1"}},
		{"ids that are not canonical UUIDs", a2a.Origin{Channel: "api", UserUUID: "not-a-uuid", EntrypointUUID: "also-not"},
			spend.Event{UserSub: "user-1", EntrypointName: "entry-1", Channel: "api"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixture(t, true)
			seedSession(f.agent.svc.Sessions().(*memSessionStore), f.agent.cfg.Name, "user-1", "session-1")

			_, err := f.agent.CompactSession(t.Context(),
				extensions.RequestInfo{UserID: "user-1", ClientID: "entry-1"},
				sessioncontrol.CompactParams{SessionID: "session-1", TargetPercent: 50, TaskID: "compact-task", TraceID: "compact-trace", Origin: tc.origin})
			if err != nil {
				t.Fatalf("CompactSession: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1: %+v", len(events), events)
			}
			if e := events[0]; !sameUserAndEntrypoint(e, tc.want) || e.Channel != tc.want.Channel {
				t.Errorf("booked user=%q/%q entrypoint=%q/%q channel=%q, want %q/%q %q/%q %q",
					e.UserID, e.UserSub, e.EntrypointID, e.EntrypointName, e.Channel,
					tc.want.UserID, tc.want.UserSub, tc.want.EntrypointID, tc.want.EntrypointName, tc.want.Channel)
			}
		})
	}
}

func resumeTwice(t *testing.T, original *TaskExecutionState, checkpoint func(*testing.T, []byte) []byte) *TaskExecutionState {
	t.Helper()
	blob, err := marshalTaskState(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	state, err := unmarshalTaskState(checkpoint(t, blob))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if blob, err = marshalTaskState(state); err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if state, err = unmarshalTaskState(blob); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	return state
}

func withoutSpendOrigin(t *testing.T, blob []byte) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	if _, ok := fields["spend_origin"]; !ok {
		t.Fatal("checkpoint has no spend_origin to remove")
	}
	delete(fields, "spend_origin")
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode checkpoint: %v", err)
	}
	return out
}

func TestADelegatedTaskIsBookedToTheChainsEntrypoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata map[string]any
		props    a2a.UserProperties
	}{
		{"peer-delegated", nil, a2a.UserProperties{DelegatingAgentName: "parent-agent"}},
		{"workflow-dispatched", map[string]any{"workflow_name": "nightly", "node_id": "n1"}, a2a.UserProperties{DelegatingAgentName: "nightly"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSpendFixtureAnswering(t, true, &llm.ChatResponse{Content: "done", FinishReason: "stop",
				Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}})
			msg := a2a.Message{Kind: "message", MessageID: "msg-1", Role: a2a.RoleUser, ContextID: "ctx-1",
				Metadata: tc.metadata, Parts: []a2a.Part{a2a.NewTextPart("go")}}
			props := tc.props
			props.ClientID, props.EntrypointName = "delegator-client", "webui"
			props.UserID, props.TraceID, props.Trigger = "user-1", "trace-1", a2a.TriggerInteractive

			if _, err := f.agent.HandleTask(t.Context(), "task-1", msg, props); err != nil {
				t.Fatalf("HandleTask: %v", err)
			}

			events := f.recordedCount(t, 1)
			if len(events) != 1 || events[0].EntrypointName != "webui" {
				t.Errorf("events = %+v, want one booked to the webui entrypoint", events)
			}
		})
	}
}

func withoutIdentityPlatformUserID(t *testing.T, blob []byte) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	var identity map[string]json.RawMessage
	if err := json.Unmarshal(fields["identity"], &identity); err != nil {
		t.Fatalf("decode checkpoint identity: %v", err)
	}
	if _, ok := identity["platform_user_id"]; !ok {
		t.Fatal("checkpoint identity has no platform_user_id to remove")
	}
	delete(identity, "platform_user_id")
	var err error
	if fields["identity"], err = json.Marshal(identity); err != nil {
		t.Fatalf("encode checkpoint identity: %v", err)
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode checkpoint: %v", err)
	}
	return out
}

func sameUserAndEntrypoint(got, want spend.Event) bool {
	return got.UserID == want.UserID && got.UserSub == want.UserSub &&
		got.EntrypointID == want.EntrypointID && got.EntrypointName == want.EntrypointName
}

func TestADispatchedToolCarriesTheAttributionForItsCallsInTheSTR(t *testing.T) {
	f := newSpendFixture(t, true)
	state := spendTask(f.agent)
	state.SpendOrigin = SpendOrigin{ParentTaskID: "orchestrator-task"}
	toolCtx := &tool.Context{AgentName: f.agent.cfg.Name, UserID: "user-1", ParentTaskID: "task-1"}
	f.agent.wireInternalLLMCallbacks(toolCtx, nil, &TaskExecutionState{}, f.agent.taskSpendAttribution(t.Context(), state, spend.SurfaceTool))

	var forwarded *remotetool.SpendAttribution
	if err := f.agent.Tools().Register(&testBuiltinTool{name: "remote", description: "probe",
		execFn: func(_ context.Context, _ map[string]any, c *tool.Context) (*tool.Result, error) {
			forwarded = c.RemoteSpend
			return tool.OK("done"), nil
		}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	f.agent.dispatchToolCallsWithStatus(t.Context(), []llm.ToolCall{{ID: "c1", Name: "remote", Arguments: `{}`}},
		toolCtx, NewStatusPublisher(f.broker, StatusPublisherConfig{TaskID: "task-1"}, f.agent.logger), "")

	if forwarded == nil {
		t.Fatal("the dispatched tool got no attribution to forward")
	}
	if forwarded.TaskID != "task-1" || forwarded.ParentTaskID != "orchestrator-task" {
		t.Errorf("forwarded task %q parent %q, want task-1 delegated by orchestrator-task", forwarded.TaskID, forwarded.ParentTaskID)
	}
	if forwarded.UserSub != "user-1" || forwarded.AgentName != "spend-agent" || forwarded.TraceID != "trace-1" ||
		forwarded.TriggerKind != string(spend.TriggerChat) || forwarded.SessionID != "session-1" {
		t.Errorf("forwarded %+v, want user-1's chat task on spend-agent in session-1", forwarded)
	}
	if toolCtx.RemoteSpend != nil {
		t.Error("the shared tool context kept a per-call attribution")
	}
}
