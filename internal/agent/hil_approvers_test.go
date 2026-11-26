// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	approverTaskOwner = "carol@example.com"
	approverAlice     = "alice@example.com"
	approverBob       = "bob@example.com"
)

func approverRegistry(notifyOn, approverSetsOn bool) *features.Registry {
	reg := features.NewRegistry()
	reg.Register(
		features.Definition{Key: features.KeyNotifyService, Default: notifyOn},
		features.Definition{Key: features.KeyHILApproverSets, Default: approverSetsOn},
	)
	return reg
}

func gateWithApprovers() HILToolConfig {
	return HILToolConfig{
		RequireApproval: true,
		ShowArgs:        true,
		Approvers:       tool.HILApprovers{Users: []string{approverAlice, approverBob}},
	}
}

func startApproverAgent(t *testing.T, brk broker.Broker, reg *features.Registry) *Agent {
	t.Helper()
	agent := New(AgentConfig{Name: "ops-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk = brk
	svc.featureRegistry = reg
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("agent Init: %v", err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("agent Start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	return agent
}

func connectedBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func startNotifyService(t *testing.T, brk broker.Broker, reg *features.Registry) {
	t.Helper()
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg))
	notifyCore := notify.NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + t.TempDir() + "/notify.db",
	}, notify.NotifyExtensions{})
	notifyCore.SetServices(rt.Services())
	if err := notifyCore.Init(t.Context(), nil); err != nil {
		t.Fatalf("notify Init: %v", err)
	}
	if err := notifyCore.Start(t.Context()); err != nil {
		t.Fatalf("notify Start: %v", err)
	}
	t.Cleanup(func() { _ = notifyCore.Stop(context.Background()) })
}

func answerAsApprover(t *testing.T, brk broker.Broker, requestID, approver string, done <-chan []*tool.Result) []*tool.Result {
	t.Helper()
	answer, _ := json.Marshal(a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := brk.PublishGuaranteed(t.Context(), a2a.NotifyReplyTopic("test-ns", requestID), &broker.Message{
			Payload: answer, UserProperties: map[string]string{a2a.PropUserID: approver, a2a.PropChannel: "slack"},
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case results := <-done:
			return results
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("the approver's answer never reached the gate")
	return nil
}

func TestApproverGate_RunWithNoInteractiveChannelAsksItsApprovers(t *testing.T) {
	brk := connectedBroker(t)
	reg := approverRegistry(true, true)
	startNotifyService(t, brk, reg)
	agent := startApproverAgent(t, brk, reg)
	var ran atomic.Int32
	if err := agent.Tools().Register(&testBuiltinTool{
		name: "reroute_shipment", description: "stub gated tool",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			ran.Add(1)
			return tool.OK("rerouted"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{{ID: "call-1", Name: "reroute_shipment", Arguments: `{"shipment":"S-1"}`}}
	nonInteractive := func() *tool.Context {
		return &tool.Context{
			AgentName:           "ops-agent",
			UserID:              "system:ops-agent",
			SessionID:           "run-1",
			GatewayCapabilities: map[string]bool{tool.CapInteractiveUserInput: false},
		}
	}

	t.Run("a gate naming no approver is refused", func(t *testing.T) {
		agent.SetHILToolConfigs(map[string]HILToolConfig{"reroute_shipment": {RequireApproval: true, ShowArgs: true}})
		results := agent.dispatchToolCallsWithStatus(t.Context(), calls, nonInteractive(), &StatusPublisher{}, "")
		if ran.Load() != 0 || results[0] == nil || results[0].Status != tool.StatusError {
			t.Fatalf("result = %+v (ran %d), want a refusal without running the tool", results[0], ran.Load())
		}
	})

	t.Run("a gate naming an approver asks them and runs on their approval", func(t *testing.T) {
		agent.SetHILToolConfigs(map[string]HILToolConfig{"reroute_shipment": gateWithApprovers()})
		done := make(chan []*tool.Result, 1)
		go func() {
			done <- agent.dispatchToolCallsWithStatus(t.Context(), calls, nonInteractive(), &StatusPublisher{}, "")
		}()
		requestID, pending := pendingApprovalID(t, agent)
		if !pending.approverSet {
			t.Fatal("the gate registered an ordinary prompt")
		}
		results := answerAsApprover(t, brk, requestID, approverBob, done)
		if ran.Load() != 1 || results[0] == nil || results[0].Status == tool.StatusError {
			t.Fatalf("result = %+v (ran %d), want the tool run after bob approved", results[0], ran.Load())
		}
	})
}

func TestApproverGate_ACheckpointedTaskStillAsksItsApproversInsteadOfPausingForItsOwner(t *testing.T) {
	brk := connectedBroker(t)
	reg := approverRegistry(true, true)
	reg.Register(features.Definition{Key: features.KeyDurableTaskResume, Default: true})
	startNotifyService(t, brk, reg)
	agent := New(AgentConfig{Name: "ops-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), session.NewMemoryStore())
	svc.brk, svc.featureRegistry = brk, reg
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("agent Init: %v", err)
	}
	if agent.checkpoints == nil {
		t.Fatal("expected durable resume to be enabled")
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("agent Start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	var ran atomic.Int32
	if err := agent.Tools().Register(&testBuiltinTool{
		name: "reroute_shipment", description: "stub gated tool",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			ran.Add(1)
			return tool.OK("rerouted"), nil
		},
	}); err != nil {
		t.Fatalf("register reroute_shipment: %v", err)
	}
	agent.SetHILToolConfigs(map[string]HILToolConfig{"reroute_shipment": gateWithApprovers()})
	calls := []llm.ToolCall{{ID: "call-1", Name: "reroute_shipment", Arguments: `{"shipment":"S-1"}`}}
	pausable := &tool.Context{AgentName: "ops-agent", UserID: approverTaskOwner, SessionID: "session-1", PauseResume: true}
	done := make(chan []*tool.Result, 1)
	go func() {
		done <- agent.dispatchToolCallsWithStatus(t.Context(), calls, pausable, &StatusPublisher{}, "")
	}()
	requestID, pending := pendingApprovalID(t, agent)
	if !pending.approverSet {
		t.Fatal("the gate registered an ordinary prompt for the task owner")
	}
	results := answerAsApprover(t, brk, requestID, approverBob, done)
	if ran.Load() != 1 || results[0] == nil || results[0].Status == tool.StatusPending || results[0].Status == tool.StatusError {
		t.Fatalf("result = %+v (ran %d), want the tool run after bob approved, not left pending", results[0], ran.Load())
	}
}

type lockedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *lockedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lockedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestApproverGate_AnAnswerGivenToAnOrdinaryGateInTheSameBatchSurvivesALongApproverWait(t *testing.T) {
	brk := connectedBroker(t)
	reg := approverRegistry(true, true)
	reg.Register(features.Definition{Key: features.KeyDurableTaskResume, Default: true})
	startNotifyService(t, brk, reg)
	agent := New(AgentConfig{Name: "ops-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), session.NewMemoryStore())
	svc.brk, svc.featureRegistry = brk, reg
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("agent Init: %v", err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("agent Start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	clock := &lockedClock{now: time.Now()}
	agent.early.now = clock.Now
	for _, name := range []string{"notify_customer", "reroute_shipment"} {
		if err := agent.Tools().Register(&testBuiltinTool{
			name: name, description: "stub gated tool",
			execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return tool.OK("done"), nil
			},
		}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	approverGate := gateWithApprovers()
	approverGate.Timeout = 24 * time.Hour
	agent.SetHILToolConfigs(map[string]HILToolConfig{
		"notify_customer":  {RequireApproval: true, Timeout: 5 * time.Minute},
		"reroute_shipment": approverGate,
	})
	calls := []llm.ToolCall{
		{ID: "call-a", Name: "notify_customer", Arguments: `{"customer":"C-1"}`},
		{ID: "call-b", Name: "reroute_shipment", Arguments: `{"shipment":"S-1"}`},
	}
	pausable := &tool.Context{
		AgentName: "ops-agent", UserID: approverTaskOwner, SessionID: "session-1", PauseResume: true,
		GatewayCapabilities: map[string]bool{tool.CapInteractiveUserInput: true},
	}
	done := make(chan []*tool.Result, 1)
	go func() {
		done <- agent.dispatchToolCallsWithStatus(t.Context(), calls, pausable, &StatusPublisher{}, "")
	}()
	approverRequestID, _ := pendingApprovalID(t, agent)
	var key earlySlotKey
	if !waitUntil(func() bool {
		agent.early.mu.Lock()
		defer agent.early.mu.Unlock()
		for k := range agent.early.slots {
			key = k
			return true
		}
		return false
	}) {
		t.Fatal("the ordinary gate never opened an early answer slot")
	}
	answered := a2a.UserInputResponseData{Type: "user_input_response", RequestID: key.requestID, Status: "answered", Data: map[string]any{"approved": true}}
	if got := agent.stashEarlyAnswer(&answered, hilSenderUser(approverTaskOwner)); got != hilDelivered {
		t.Fatalf("stashEarlyAnswer() = %v, want the owner's answer held", got)
	}
	clock.Advance(2 * time.Hour)
	agent.expectEarlyAnswer("task-elsewhere", "req-elsewhere", approverTaskOwner, time.Hour)

	answerAsApprover(t, brk, approverRequestID, approverBob, done)

	taken := agent.early.takeRequests(key.taskID, []string{key.requestID})
	if len(taken) != 1 || taken[0].slot.answer == nil {
		t.Errorf("slots taken for the ordinary gate after the approver wait = %+v, want the answer its owner gave in time", taken)
	}
}

func TestApproverGate_AnEvalRunNamingApproversPagesNobodyAndIsDenied(t *testing.T) {
	brk := connectedBroker(t)
	reg := approverRegistry(true, true)
	startNotifyService(t, brk, reg)
	requests, err := brk.CreateQueue("eval-approver-requests", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(requests, a2a.NotifyRequestSubscription("test-ns")); err != nil {
		t.Fatal(err)
	}
	audit := &toolAuditRecorder{}
	agent := New(AgentConfig{Name: "ops-agent", Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.brk, svc.featureRegistry, svc.auditLog = brk, reg, audit
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("agent Init: %v", err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("agent Start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	var ran atomic.Int32
	if err := agent.Tools().Register(&testBuiltinTool{
		name: "reroute_shipment", description: "stub gated tool",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			ran.Add(1)
			return tool.OK("rerouted"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	gate := gateWithApprovers()
	gate.Timeout = 2 * time.Second
	agent.SetHILToolConfigs(map[string]HILToolConfig{"reroute_shipment": gate})
	calls := []llm.ToolCall{{ID: "call-1", Name: "reroute_shipment", Arguments: `{"shipment":"S-1"}`}}
	evalCtx := &tool.Context{
		AgentName:           "ops-agent",
		UserID:              "system:ops-agent",
		SessionID:           "eval-1",
		Trigger:             a2a.TriggerEval,
		GatewayCapabilities: map[string]bool{tool.CapInteractiveUserInput: false},
	}

	results := agent.dispatchToolCallsWithStatus(t.Context(), calls, evalCtx, &StatusPublisher{}, "")

	if ran.Load() != 0 || results[0] == nil || results[0].Status != tool.StatusError {
		t.Fatalf("result = %+v (ran %d), want a denial that does not run the tool", results[0], ran.Load())
	}
	if !strings.Contains(results[0].Message, "evaluation") {
		t.Errorf("message = %q, want the model told that an evaluation run has no human to approve", results[0].Message)
	}
	if got := audit.only(t).ApprovalRefusal; got != string(tool.RefusalNoHumanInEvalRun) {
		t.Errorf("approvalRefusal = %q, want %q", got, tool.RefusalNoHumanInEvalRun)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if msg, err := brk.Receive(ctx, requests); err == nil && msg != nil {
		t.Errorf("a notify request was published for an evaluation run: %s", msg.Payload)
	}
	agent.pendingHILMu.Lock()
	pending := len(agent.pendingHIL)
	agent.pendingHILMu.Unlock()
	if pending != 0 {
		t.Errorf("%d approval requests are pending for an evaluation run", pending)
	}
}

func pendingApprovalID(t *testing.T, agent *Agent) (string, pendingHILRequest) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		agent.pendingHILMu.Lock()
		for id, p := range agent.pendingHIL {
			agent.pendingHILMu.Unlock()
			return id, p
		}
		agent.pendingHILMu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the approval gate never registered a pending request")
	return "", pendingHILRequest{}
}

type approvalOutcome struct {
	result  *tool.Result
	refusal tool.ApprovalRefusal
}

func TestApproverGate_OnlyAnApproverRoutedByNotifyCanApprove(t *testing.T) {
	brk := connectedBroker(t)
	reg := approverRegistry(true, true)
	startNotifyService(t, brk, reg)
	agent := startApproverAgent(t, brk, reg)

	done := make(chan approvalOutcome, 1)
	go func() {
		result, refusal := agent.checkToolApproval(t.Context(), &StatusPublisher{}, "reroute_shipment",
			map[string]any{"shipment": "S-1"}, gateWithApprovers(), &tool.Context{UserID: approverTaskOwner})
		done <- approvalOutcome{result, refusal}
	}()
	requestID, pending := pendingApprovalID(t, agent)
	if !pending.approverSet {
		t.Fatal("the gate registered its request as an ordinary prompt")
	}

	ownerAnswer := &a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}
	if got := agent.deliverHILResponse(ownerAnswer, hilSenderUser(approverTaskOwner)); got != hilRefused {
		t.Fatalf("the task owner's answer from the task stream = %v, want refused", got)
	}

	outsider, _ := json.Marshal(a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}})
	if err := brk.PublishGuaranteed(t.Context(), a2a.NotifyReplyTopic("test-ns", requestID), &broker.Message{
		Payload: outsider, UserProperties: map[string]string{a2a.PropUserID: approverTaskOwner, a2a.PropChannel: "slack"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		t.Fatalf("the gate resolved on the owner's answer through notify: %+v", out)
	case <-time.After(500 * time.Millisecond):
	}

	answer, _ := json.Marshal(a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := brk.PublishGuaranteed(t.Context(), a2a.NotifyReplyTopic("test-ns", requestID), &broker.Message{
			Payload: answer, UserProperties: map[string]string{a2a.PropUserID: approverBob, a2a.PropChannel: "slack"},
		}); err != nil {
			t.Fatal(err)
		}
		select {
		case out := <-done:
			if out.result != nil {
				t.Fatalf("bob's approval did not let the call run: %+v (%s)", out.result, out.refusal)
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("bob's approval never reached the gate")
}

func TestApproverGate_ACancelledRunWithdrawsItsApproval(t *testing.T) {
	brk := connectedBroker(t)
	reg := approverRegistry(true, true)
	agent := startApproverAgent(t, brk, reg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan approvalOutcome, 1)
	go func() {
		result, refusal := agent.checkToolApproval(ctx, &StatusPublisher{}, "reroute_shipment",
			map[string]any{"shipment": "S-1"}, gateWithApprovers(), &tool.Context{UserID: approverTaskOwner})
		done <- approvalOutcome{result, refusal}
	}()
	requestID, _ := pendingApprovalID(t, agent)
	replies, err := brk.CreateQueue("withdrawals-"+requestID, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(replies, a2a.NotifyReplyTopic("test-ns", requestID)); err != nil {
		t.Fatal(err)
	}

	cancel()
	select {
	case out := <-done:
		if out.result == nil {
			t.Fatal("a cancelled run was treated as approved")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cancelled run kept waiting for its approvers")
	}

	rctx, rcancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer rcancel()
	msg, err := brk.Receive(rctx, replies)
	if err != nil {
		t.Fatalf("the cancelled run withdrew nothing: %v", err)
	}
	var withdrawal a2a.UserInputResponseData
	if err = json.Unmarshal(msg.Payload, &withdrawal); err != nil {
		t.Fatal(err)
	}
	if withdrawal.RequestID != requestID || withdrawal.Status != a2a.HILStatusWithdrawn ||
		msg.UserProperties[a2a.PropReplyTo] != a2a.NotifyResponseTopic("test-ns", "ops-agent", requestID) {
		t.Errorf("withdrawal = %+v %v, want the request withdrawn from its own response address", withdrawal, msg.UserProperties)
	}
}

func TestApproverGate_RefusesWithoutAskingTheOwnerWhenApproversCannotBeAsked(t *testing.T) {
	cases := map[string]*features.Registry{
		"approver sets off":  approverRegistry(true, false),
		"notify service off": approverRegistry(false, true),
	}
	for name, reg := range cases {
		t.Run(name, func(t *testing.T) {
			brk := connectedBroker(t)
			q, err := brk.CreateQueue("approver-requests", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
				t.Fatal(err)
			}
			agent := startApproverAgent(t, brk, reg)

			gate := gateWithApprovers()
			gate.Timeout = 2 * time.Second
			result, refusal := agent.checkToolApproval(t.Context(), &StatusPublisher{}, "reroute_shipment",
				map[string]any{}, gate, &tool.Context{UserID: approverTaskOwner})
			if result == nil || refusal != tool.RefusalNoApprovalRoute {
				t.Fatalf("got (%+v, %q), want a refusal with no approval route", result, refusal)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			if msg, err := brk.Receive(ctx, q); err == nil && msg != nil {
				t.Errorf("a notify request was published although the approvers cannot be asked: %s", msg.Payload)
			}
		})
	}
}

func TestApproverSetNotifyRequest_NamesNoSingleRecipientSoAnOlderServiceDropsIt(t *testing.T) {
	req, err := approverSetNotifyRequest("test-ns", "ops-agent", hilRequest{
		requestID: "hil-approval-1", source: "tool_approval", recipientUserID: approverTaskOwner,
		traceID: "trace-1", sessionID: "session-of-carol", surface: json.RawMessage(`{"surfaceId":"s1"}`),
	}, tool.HILApprovers{
		Users:  []string{approverAlice, approverBob},
		Places: []tool.HILPlace{{ChannelType: "slack", Address: map[string]any{"channel_id": "C0OPS"}}},
	}, "2026-11-11T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if req.RecipientUserID != "" || req.RecipientPlatformUserID != "" {
		t.Errorf("recipient = %q / %q, want none so a service that predates approver sets drops the request instead of asking one approver",
			req.RecipientUserID, req.RecipientPlatformUserID)
	}
	if req.Approvers == nil || len(req.Approvers.UserIDs) != 2 || string(req.Approvers.Places[0].Address) != `{"channel_id":"C0OPS"}` {
		t.Errorf("approvers = %+v", req.Approvers)
	}
	if _, linked := req.Metadata["session_id"]; linked {
		t.Error("the prompt links the task owner's session, which an approver cannot open")
	}
	if req.RequesterAddr != a2a.NotifyResponseTopic("test-ns", "ops-agent", "hil-approval-1") || req.Mode != a2a.NotifyModeInput {
		t.Errorf("request = %+v", req)
	}
}

func TestMayAnswerRequest_ApproverSetOnlyFromNotify(t *testing.T) {
	set := pendingHILRequest{ownerUserID: approverTaskOwner, approverSet: true}
	if hilSenderUser(approverTaskOwner).mayAnswerRequest(set) {
		t.Error("the task owner answered an approver-set request")
	}
	if !hilSenderNotifyService.mayAnswerRequest(set) {
		t.Error("the notification service could not answer an approver-set request")
	}
	single := pendingHILRequest{ownerUserID: approverTaskOwner}
	if !hilSenderUser(approverTaskOwner).mayAnswerRequest(single) || hilSenderUser(approverBob).mayAnswerRequest(single) {
		t.Error("a single-recipient request changed who may answer it")
	}
}
