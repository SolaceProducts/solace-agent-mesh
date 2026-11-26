// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var startOnlyToolContextFields = map[string]string{
	"IncomingInstruction": "the incoming message's text, which the task state does not keep: " +
		"after a resume sub_task no longer refuses an instruction that restates the task",
	"CallerVolumeIDs": "the incoming message's volume_context, which the task state does not keep: " +
		"after a resume a volume prompt no longer lists the caller's volumes first",
	"SkillState": "built from the session row, which resumeTaskExecution loads and sets after the builder returns: " +
		"a tool replayed after a sign-in runs without it",
}

var perCallToolContextFields = map[string]string{
	"ToolCallID":            "PrepareCall, when the call is dispatched",
	"RemoteSpend":           "BindToolCall, when the call is dispatched",
	"OAuthToken":            "the tool's auth wrapper",
	"AuthHeaders":           "the tool's auth wrapper",
	"ApprovalReason":        "the approval gate",
	"VolumeBindings":        "prepareToolCall, for a tool with volume parameters",
	"NetworkBindings":       "prepareToolCall, for a tool with network parameters",
	"AuthPromptUnavailable": "a workflow tool node, which builds its own context",
}

func TestResumedToolContextKeepsWhatAStartingOneHas(t *testing.T) {
	withDocsLoadedSessions(t)
	agent := newSkillCacheAgent(t)
	agent.cfg.MaxSubTaskFanout = 3
	svc := agent.svc.(*mockServices)
	svc.brk = newMockBroker()
	svc.authn = tokenAuthenticator{}
	svc.arts = &artifact.ScopedService{Inner: artmem.New(), Scope: artifact.ScopeNamespace, Namespace: "test-ns"}
	svc.featureRegistry = durableResumeRegistry(true)
	agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, newTestAgentBackstops(), agent.logger)
	if agent.checkpoints == nil {
		t.Fatal("this test's agent must checkpoint its pauses, or the fields only such an agent sets go unchecked")
	}

	msg := newUserMessage("Summarize the quarterly figures for Acme.")
	msg.Metadata = map[string]any{
		"volume_context":          []any{"vol-1"},
		"project_id":              "proj-42",
		"displaysTemplateSignals": true,
	}
	state, rc, earlyResp, err := agent.setupTask(t.Context(), "task-1", msg, a2a.UserProperties{
		UserID:              inlineTestUser,
		AuthToken:           callersToken,
		ClientID:            "gw-1",
		TraceID:             "trace-1",
		A2AStatusTopic:      "test-ns/a2a/v1/gateway/status/gw-1/task-1",
		A2AUserConfig:       `{"locale":"en"}`,
		Trigger:             a2a.TriggerInteractive,
		RootKind:            a2a.RootKindWorkflow,
		RootID:              "wf-1",
		RootNodeID:          "node-1",
		Origin:              checkpointedOrigin,
		CallDepth:           1,
		GatewayCapabilities: map[string]bool{tool.CapInteractiveUserInput: true},
	}, taskDelivery{})
	if err != nil || earlyResp != nil {
		t.Fatalf("setupTask: resp=%+v err=%v", earlyResp, err)
	}

	atStart := reflect.ValueOf(rc.toolCtx).Elem()
	resumed := reflect.ValueOf(agent.buildToolCtxFromState(t.Context(), state, rc.sp)).Elem()
	fields := atStart.Type()

	for _, listed := range []map[string]string{startOnlyToolContextFields, perCallToolContextFields} {
		for name := range listed {
			if _, ok := fields.FieldByName(name); !ok {
				t.Errorf("%s is listed but is not a tool.Context field; remove it from the list", name)
			}
		}
	}

	for i := range fields.NumField() {
		name := fields.Field(i).Name
		setAtStart, setOnResume := !atStart.Field(i).IsZero(), !resumed.Field(i).IsZero()
		_, startOnly := startOnlyToolContextFields[name]
		_, perCall := perCallToolContextFields[name]
		switch {
		case perCall:
			if setAtStart || setOnResume {
				t.Errorf("tool.Context.%s is listed as set per call, but a builder sets it (start=%t, resume=%t); remove it from perCallToolContextFields",
					name, setAtStart, setOnResume)
			}
		case startOnly:
			if !setAtStart || setOnResume {
				t.Errorf("tool.Context.%s is listed as start-only, but start=%t and resume=%t; remove it from startOnlyToolContextFields",
					name, setAtStart, setOnResume)
			}
		case !setAtStart:
			t.Errorf("tool.Context.%s is not set when this task starts, so nothing here compares it with a resumed task: "+
				"make this test's task set it, or list it in perCallToolContextFields if no builder should", name)
		case !setOnResume:
			t.Errorf("tool.Context.%s is set when a task starts and missing after it resumes: "+
				"wire a hook in wireTaskHooks or wireTaskStateHooks, which both builders call, and set a value in buildToolCtxFromState too", name)
		}
	}
}

const (
	callersToken          = "caller-token-1"
	platformQueryToolName = "ask_platform"
	agentsQueryTopic      = "test-ns/sam/v1/control/get/apps/platform/agents"
)

func registerPlatformQueryTool(t *testing.T, agent *Agent) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:        platformQueryToolName,
		description: "lists the agents on the platform",
		execFn: func(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
			if toolCtx.ControlQuerier == nil {
				return tool.Error("no control plane client on this task"), nil
			}
			if _, err := toolCtx.ControlQuerier.Custom(ctx, "platform", "get", "agents", nil); err != nil {
				return tool.Error(err.Error()), nil
			}
			return tool.OK("listed"), nil
		},
	})
	if err != nil {
		t.Fatalf("register %s: %v", platformQueryToolName, err)
	}
}

func askPlatformThenAnswer() []*llm.ChatResponse {
	return []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-platform", Name: platformQueryToolName, Arguments: `{}`}}},
		{Content: "Done.", FinishReason: "stop"},
	}
}

func requireOnePlatformQueryAs(t *testing.T, brk *mockBroker, wantToken string) {
	t.Helper()
	requests := brk.getRequested()
	if len(requests) != 1 {
		t.Fatalf("the task sent %d control requests, want 1: %+v", len(requests), requests)
	}
	wantProps := map[string]string{"authToken": wantToken}
	if got := requests[0]; got.topic != agentsQueryTopic || !maps.Equal(got.userProps, wantProps) {
		t.Errorf("control request = topic %q with properties %v, want topic %q with properties %v",
			got.topic, got.userProps, agentsQueryTopic, wantProps)
	}
}

func TestStartingTaskQueriesThePlatformAsItsCaller(t *testing.T) {
	agent, _, _ := newTestAgent(askPlatformThenAnswer()...)
	brk := newMockBroker()
	svc := agent.svc.(*mockServices)
	svc.brk = brk
	svc.authn = tokenAuthenticator{}
	registerPlatformQueryTool(t, agent)

	resp, err := agent.HandleTask(t.Context(), "task-1", newUserMessage("List the agents on the platform."),
		a2a.UserProperties{UserID: inlineTestUser, AuthToken: callersToken})
	if err != nil || resp == nil || resp.Error != nil {
		t.Fatalf("HandleTask: resp=%+v err=%v", resp, err)
	}

	requireOnePlatformQueryAs(t, brk, callersToken)
}

func TestTakenOverTaskQueriesThePlatformAsItsCaller(t *testing.T) {
	const identityKey = "primary-key-1234567890ab"
	sharesIdentityKey := func(a *Agent) { a.checkpoints.sealer = identityKeySealer(t, identityKey) }
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner", sharesIdentityKey)
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor", sharesIdentityKey,
		func(a *Agent) { registerPlatformQueryTool(t, a) })
	survivorLLM.mu.Lock()
	survivorLLM.responses = askPlatformThenAnswer()
	survivorLLM.mu.Unlock()

	const taskID, subTaskID = "task-takeover-token", "sub-takeover-token"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	state.UserProps.AuthToken = callersToken
	state.Identity.AuthToken = callersToken
	if err := owner.checkpoints.save(t.Context(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)

	survivor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished the work"))

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "peer finished the work")
	requireOnePlatformQueryAs(t, survivorBrk, callersToken)
}

const (
	researchPlanID     = "plan-1"
	researchPlanAnswer = `{"event_type":"plan_response","data":{"plan_id":"plan-1","agent_name":"test-agent","user_id":"user-1","action":"start","steps":["compare the two quarters"]}}`
)

func resumedPlanAwaiter(t *testing.T, agent *Agent) tool.PlanResponseAwaiter {
	t.Helper()
	state := newTestTaskState(agent)
	await := agent.buildToolCtxFromState(t.Context(), state, defaultPauseCallArgs(agent, state).sp).AwaitDeepResearchPlanResponse
	if await == nil {
		t.Fatal("a resumed task has no way to wait for the answer to a deep-research plan")
	}
	return await
}

func deliverResearchPlanAnswer(agent *Agent) {
	agent.dispatchDeepResearchEvent(broker.NewDeliveredMessage("id", "test-ns/sam/events/deep_research/plan_response",
		[]byte(researchPlanAnswer), func() {}, func() {}))
}

func TestResumedTaskGetsTheUsersAnswerToItsResearchPlan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agent, _, _ := newTestAgent()
		await := resumedPlanAwaiter(t, agent)

		var got *tool.PlanResponse
		var err error
		go func() { got, err = await(t.Context(), researchPlanID, inlineTestUser) }()
		synctest.Wait()
		deliverResearchPlanAnswer(agent)
		synctest.Wait()

		want := &tool.PlanResponse{Action: "start", Steps: []string{"compare the two quarters"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("plan answer = %+v, %v, want %+v", got, err, want)
		}
	})
}

func TestWaitForAResearchPlanAnswerEndsWhenItsTaskIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agent, _, _ := newTestAgent()
		await := resumedPlanAwaiter(t, agent)
		ctx, cancel := context.WithCancel(t.Context())

		var got *tool.PlanResponse
		var err error
		go func() { got, err = await(ctx, researchPlanID, inlineTestUser) }()
		synctest.Wait()
		cancel()
		synctest.Wait()

		if got != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled wait = %+v, %v, want no answer and context.Canceled", got, err)
		}
		lateAnswerTaken := agent.planWaiters.Resolve(researchPlanID, inlineTestUser, &DeepResearchPlanResponse{Action: "start"})
		if lateAnswerTaken {
			t.Error("the cancelled wait is still registered, so an answer that arrives late is handed to nobody")
		}
	})
}
