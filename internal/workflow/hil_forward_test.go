// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	questionRequestID = "req-1"
	askerAgent        = "agent_asker"
)

func questionStatus(t *testing.T, subTaskID string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"id":      subTaskID,
		"jsonrpc": "2.0",
		"result": map[string]any{
			"taskId": subTaskID,
			"status": map[string]any{
				"state": "input-required",
				"message": map[string]any{
					"metadata": map[string]any{"agent_name": askerAgent},
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{"type": "user_input_request", "request_id": questionRequestID},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func delegatorAnswer(t *testing.T, taskID, userID string) *broker.Message {
	t.Helper()
	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: questionRequestID,
		Status:    "answered",
		Data:      map[string]any{"answers": map[string]any{"q0": "Blue"}},
	}
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": a2a.Message{
			Kind:   "message",
			Role:   a2a.RoleUser,
			Parts:  []a2a.Part{a2a.NewDataPart(resp)},
			TaskID: taskID,
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return &broker.Message{
		Payload: payload,
		UserProperties: map[string]string{
			"delegating_agent_name": "Orchestrator",
			"userId":                userID,
		},
	}
}

func newQuestionTestInstance(t *testing.T) (*WorkflowInstance, *capturingBroker, *WorkflowExecution, *broker.Message) {
	t.Helper()
	w, brk, subTaskID, exec := newForwardTestInstance(t, a2a.UserProperties{
		A2AStatusTopic: "ns/a2a/v1/agent/status/Orchestrator/parent-task",
		UserID:         "alice@example.com",
		ClientID:       "web-ui",
		AuthToken:      "alice-token",
		A2AUserConfig:  `{"model":"general"}`,
		TraceID:        "trace-xyz",
	})
	w.cfg.Namespace = "ns"
	exec.State.SetSubTaskTarget(subTaskID, askerAgent)
	original := &broker.Message{Payload: []byte(`{"original":true}`)}
	exec.Context.request = original

	w.forwardSubTaskStatus(context.Background(), &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/" + subTaskID,
		Payload: questionStatus(t, subTaskID),
	})
	brk.reset()
	return w, brk, exec, original
}

func (b *capturingBroker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = nil
}

func dispatchedTo(t *testing.T, brk *capturingBroker, topic string) []capturedMessage {
	t.Helper()
	var out []capturedMessage
	for _, m := range brk.snapshot() {
		if m.Topic == topic {
			out = append(out, m)
		}
	}
	return out
}

func TestNodeQuestion_AnswerFromDelegatorReachesTheAskingAgent(t *testing.T) {
	w, brk, exec, original := newQuestionTestInstance(t)

	answer := delegatorAnswer(t, "parent-task", "alice@example.com")
	req := mustParseRequest(t, answer.Payload)
	w.handleTaskRequest(context.Background(), req, answer)

	sent := dispatchedTo(t, brk, a2a.AgentRequestTopic("ns", askerAgent))
	if len(sent) != 1 {
		t.Fatalf("answers published to the asking agent = %d, want 1; all publishes: %+v", len(sent), brk.snapshot())
	}
	got := mustParseRequest(t, sent[0].Payload)
	if got.ID != "sub-1" {
		t.Errorf("forwarded request id = %q, want the node's sub-task id sub-1", got.ID)
	}
	if !json.Valid(sent[0].Payload) || !containsRequestID(t, sent[0].Payload, questionRequestID) {
		t.Errorf("forwarded payload does not carry request_id %q: %s", questionRequestID, sent[0].Payload)
	}
	for key, want := range map[string]string{
		"delegating_agent_name": "GreetWorkflow",
		"userId":                "alice@example.com",
		"authToken":             "alice-token",
		"clientId":              "web-ui",
		"a2aUserConfig":         `{"model":"general"}`,
		"traceId":               "trace-xyz",
	} {
		if sent[0].UserProperties[key] != want {
			t.Errorf("forwarded user property %s = %q, want %q", key, sent[0].UserProperties[key], want)
		}
	}
	if exec.Context.request != original {
		t.Error("the answer was adopted as the run's delivery; it must never replace the task request")
	}
	if n := len(w.executions); n != 1 {
		t.Errorf("executions = %d, want 1: an answer must not start a run", n)
	}
	if _, still := exec.Context.askerOf(questionRequestID); still {
		t.Error("the question stays recorded after its answer was delivered")
	}
}

func TestNodeQuestion_AnswerFromAnotherUserIsRefused(t *testing.T) {
	w, brk, exec, original := newQuestionTestInstance(t)

	answer := delegatorAnswer(t, "parent-task", "bob@example.com")
	w.handleTaskRequest(context.Background(), mustParseRequest(t, answer.Payload), answer)

	if sent := dispatchedTo(t, brk, a2a.AgentRequestTopic("ns", askerAgent)); len(sent) != 0 {
		t.Fatalf("another user's answer reached the asking agent: %+v", sent)
	}
	if _, still := exec.Context.askerOf(questionRequestID); !still {
		t.Error("a refused answer must leave the question open for its owner")
	}
	if exec.Context.request != original {
		t.Error("a refused answer was adopted as the run's delivery")
	}
}

func TestNodeQuestion_AnswerForARunHeldElsewhereIsPassedOnOnce(t *testing.T) {
	brk := &capturingBroker{}
	w := &WorkflowInstance{
		cfg:             &WorkflowConfig{Name: "GreetWorkflow", Namespace: "ns"},
		svc:             &capturingServices{brk: brk},
		logger:          slog.Default(),
		executions:      make(map[string]*WorkflowExecution),
		executionsByID:  make(map[string]*WorkflowExecution),
		subTaskIndex:    make(map[string]string),
		finalizedRoutes: runtime.NewMapCache(),
	}

	answer := delegatorAnswer(t, "parent-task", "alice@example.com")
	w.handleTaskRequest(context.Background(), mustParseRequest(t, answer.Payload), answer)

	passed := dispatchedTo(t, brk, w.answerForwardTopic())
	if len(passed) != 1 {
		t.Fatalf("answers passed on = %d, want 1; all publishes: %+v", len(passed), brk.snapshot())
	}
	if string(passed[0].Payload) != string(answer.Payload) {
		t.Error("a passed-on answer must be the delivered envelope, unchanged")
	}
	if !maps.Equal(passed[0].UserProperties, answer.UserProperties) {
		t.Errorf("passed-on user properties = %v, want the delivered ones %v: the holder authenticates and owner-checks them", passed[0].UserProperties, answer.UserProperties)
	}
	if n := len(w.executions); n != 0 {
		t.Errorf("executions = %d, want 0: an answer must not start a run", n)
	}

	brk.reset()
	w.handlePassedOnAnswer(context.Background(), &broker.Message{
		Topic:          w.answerForwardTopic(),
		Payload:        passed[0].Payload,
		UserProperties: passed[0].UserProperties,
	})
	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("an AWE not holding the run published %d messages for a passed-on answer; it must drop it", n)
	}
}

func TestNodeQuestion_PassedOnAnswerReachesTheAskingAgentFromTheHolder(t *testing.T) {
	w, brk, _, _ := newQuestionTestInstance(t)

	answer := delegatorAnswer(t, "parent-task", "alice@example.com")
	w.handlePassedOnAnswer(context.Background(), &broker.Message{
		Topic:          w.answerForwardTopic(),
		Payload:        answer.Payload,
		UserProperties: answer.UserProperties,
	})

	if sent := dispatchedTo(t, brk, a2a.AgentRequestTopic("ns", askerAgent)); len(sent) != 1 {
		t.Fatalf("answers published to the asking agent = %d, want 1", len(sent))
	}
	if passed := dispatchedTo(t, brk, w.answerForwardTopic()); len(passed) != 0 {
		t.Error("a passed-on answer was passed on again")
	}
}

func mustParseRequest(t *testing.T, payload []byte) a2a.Request {
	t.Helper()
	var req a2a.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func containsRequestID(t *testing.T, payload []byte, requestID string) bool {
	t.Helper()
	var env struct {
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatal(err)
	}
	for _, p := range env.Params.Message.Parts {
		if data, ok := p.Data.(map[string]any); ok && data["request_id"] == requestID {
			return true
		}
	}
	return false
}

type tokenAuthenticator struct {
	valid      string
	taskID     string
	rejections atomic.Int32
}

func (a *tokenAuthenticator) Authenticate(_ context.Context, token, taskID string) (*auth.Identity, error) {
	if token != a.valid || taskID != a.taskID {
		a.rejections.Add(1)
		return nil, errors.New("token rejected")
	}
	return &auth.Identity{UserID: "alice@example.com"}, nil
}

func newAnswerAWE(t *testing.T, brk broker.Broker) (*WorkflowInstance, *tokenAuthenticator) {
	t.Helper()
	authn := &tokenAuthenticator{valid: "alice-token", taskID: "parent-task"}
	svc := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(features.NewRegistry()),
		runtime.WithAuthenticator(authn),
	).Services()
	w := NewWorkflowInstance(&WorkflowConfig{
		Name:      "GreetWorkflow",
		Namespace: "ns",
		Nodes:     []*NodeConfig{{ID: "ask", Type: "agent", AgentName: askerAgent}},
	})
	if err := w.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return w, authn
}

func startAnswerAWE(t *testing.T, brk broker.Broker) (*WorkflowInstance, *tokenAuthenticator) {
	t.Helper()
	w, authn := newAnswerAWE(t, brk)
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	return w, authn
}

func seedRunAwaitingAnswer(t *testing.T, w *WorkflowInstance) *WorkflowExecution {
	t.Helper()
	state := NewExecutionState(w.cfg.Name, "exec-1")
	state.TrackSubTask("sub-1", "ask")
	state.SetSubTaskTarget("sub-1", askerAgent)
	exec := &WorkflowExecution{
		State: state,
		Context: &WorkflowExecutionContext{TaskID: "parent-task", UserProps: a2a.UserProperties{
			A2AStatusTopic: "ns/a2a/v1/agent/status/Orchestrator/parent-task",
			UserID:         "alice@example.com",
			AuthToken:      "alice-token",
		}},
		Executor: &DAGExecutor{state: state},
	}
	w.executionsMu.Lock()
	w.executions["parent-task"] = exec
	w.subTaskIndex["sub-1"] = "parent-task"
	w.executionsMu.Unlock()
	t.Cleanup(func() {
		w.executionsMu.Lock()
		delete(w.executions, "parent-task")
		delete(w.subTaskIndex, "sub-1")
		w.executionsMu.Unlock()
	})
	w.forwardSubTaskStatus(t.Context(), &broker.Message{
		Topic:   "ns/a2a/v1/agent/status/GreetWorkflow/sub-1",
		Payload: questionStatus(t, "sub-1"),
	})
	if _, ok := exec.Context.askerOf(questionRequestID); !ok {
		t.Fatal("the node's question was not recorded")
	}
	return exec
}

func listenOn(t *testing.T, brk broker.Broker, topic string) func(time.Duration) *broker.Message {
	t.Helper()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, topic); err != nil {
		t.Fatal(err)
	}
	return func(wait time.Duration) *broker.Message {
		ctx, cancel := context.WithTimeout(t.Context(), wait)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		if err != nil {
			t.Fatalf("receive on %s: %v", topic, err)
		}
		return msg
	}
}

func newConnectedDevBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func answerOnRequestTopic(t *testing.T, token string) *broker.Message {
	t.Helper()
	answer := delegatorAnswer(t, "parent-task", "alice@example.com")
	answer.UserProperties["authToken"] = token
	answer.Topic = "ns/a2a/v1/agent/request/GreetWorkflow"
	return answer
}

func TestNodeQuestion_AnswerReachingAnotherAWEReachesTheAskingAgentThroughTheHolder(t *testing.T) {
	brk := newConnectedDevBroker(t)
	elsewhere, _ := startAnswerAWE(t, brk)
	holder, _ := startAnswerAWE(t, brk)
	exec := seedRunAwaitingAnswer(t, holder)
	nodeInbox := listenOn(t, brk, "ns/a2a/v1/agent/request/"+askerAgent)

	elsewhere.routeMessage(t.Context(), answerOnRequestTopic(t, "alice-token"))

	got := nodeInbox(3 * time.Second)
	if got == nil {
		t.Fatal("the answer never reached the asking agent")
	}
	if req := mustParseRequest(t, got.Payload); req.ID != "sub-1" {
		t.Errorf("forwarded request id = %q, want sub-1", req.ID)
	}
	if extra := nodeInbox(300 * time.Millisecond); extra != nil {
		t.Errorf("the answer reached the asking agent more than once")
	}
	if _, still := exec.Context.askerOf(questionRequestID); still {
		t.Error("the holder kept the question open after delivering its answer")
	}
}

func TestNodeQuestion_PassedOnAnswerIsNeverPassedOnAgain(t *testing.T) {
	brk := newConnectedDevBroker(t)
	elsewhere, _ := startAnswerAWE(t, brk)
	_, _ = startAnswerAWE(t, brk)
	forwardTopic := listenOn(t, brk, "ns/sam/v1/agent/forward/GreetWorkflow")

	elsewhere.routeMessage(t.Context(), answerOnRequestTopic(t, "alice-token"))

	if first := forwardTopic(3 * time.Second); first == nil {
		t.Fatal("an answer for a run this AWE does not hold was not passed on")
	}
	for passes := 1; ; passes++ {
		if forwardTopic(500*time.Millisecond) == nil {
			if passes != 1 {
				t.Errorf("the answer was passed on %d times, want exactly once", passes)
			}
			return
		}
		if passes > 10 {
			t.Fatal("a passed-on answer keeps being passed on between AWEs")
		}
	}
}

func TestNodeQuestion_PassedOnAnswerThatDoesNotAuthenticateIsDropped(t *testing.T) {
	brk := newConnectedDevBroker(t)
	holder, authn := startAnswerAWE(t, brk)
	exec := seedRunAwaitingAnswer(t, holder)
	nodeInbox := listenOn(t, brk, "ns/a2a/v1/agent/request/"+askerAgent)

	forged := delegatorAnswer(t, "parent-task", "alice@example.com")
	forged.UserProperties["authToken"] = "forged-token"
	if err := brk.Publish(t.Context(), "ns/sam/v1/agent/forward/GreetWorkflow", forged); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for authn.rejections.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the holder never authenticated the passed-on answer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := nodeInbox(300 * time.Millisecond); got != nil {
		t.Fatalf("a passed-on answer that failed authentication reached the asking agent: %s", got.Payload)
	}
	if _, still := exec.Context.askerOf(questionRequestID); !still {
		t.Error("a rejected answer must leave the question open")
	}
}

type refusingForwardBroker struct {
	broker.Broker
}

func (b refusingForwardBroker) Subscribe(q broker.Queue, topic string) error {
	if topic == "ns/sam/v1/agent/forward/GreetWorkflow" {
		return errors.New("subscription refused by ACL")
	}
	return b.Broker.Subscribe(q, topic)
}

func TestNodeQuestion_WorkflowDoesNotStartWithoutThePassOnSubscription(t *testing.T) {
	w, _ := newAnswerAWE(t, refusingForwardBroker{Broker: newConnectedDevBroker(t)})

	err := w.Start(t.Context())
	if err == nil {
		_ = w.Stop(context.Background())
		t.Fatal("Start succeeded without the subscription for passed-on answers; it would report ready and lose them")
	}
	if !strings.Contains(err.Error(), "subscribe answer forward") {
		t.Errorf("Start error = %v, want it to name the answer forward subscription", err)
	}
}

func deliveredAnswer(t *testing.T, acks, nacks *atomic.Int32) *broker.Message {
	t.Helper()
	answer := delegatorAnswer(t, "parent-task", "alice@example.com")
	msg := broker.NewDeliveredMessage("m-1", "ns/a2a/v1/agent/request/GreetWorkflow", answer.Payload,
		func() { acks.Add(1) }, func() { nacks.Add(1) })
	msg.UserProperties = answer.UserProperties
	return msg
}

func failPublishesTo(brk *capturingBroker, failing string) {
	brk.publishHook = func(topic string) error {
		if topic == failing {
			return errors.New("broker unavailable")
		}
		return nil
	}
}

func captureLogs(w *WorkflowInstance) *bytes.Buffer {
	var buf bytes.Buffer
	w.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &buf
}

func TestNodeQuestion_AnswerTheNodeCouldNotBeSentIsReturnedToTheQueue(t *testing.T) {
	w, brk, exec, _ := newQuestionTestInstance(t)
	failPublishesTo(brk, "ns/a2a/v1/agent/request/"+askerAgent)
	logs := captureLogs(w)

	var acks, nacks atomic.Int32
	msg := deliveredAnswer(t, &acks, &nacks)
	w.handleTaskRequest(t.Context(), mustParseRequest(t, msg.Payload), msg)

	if nacks.Load() != 1 || acks.Load() != 0 {
		t.Errorf("acks=%d nacks=%d, want the undelivered answer returned to the queue (0 acks, 1 nack)", acks.Load(), nacks.Load())
	}
	if _, still := exec.Context.askerOf(questionRequestID); !still {
		t.Error("an undelivered answer must leave the question open for the redelivery")
	}
	if !strings.Contains(logs.String(), "level=WARN") || strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a first failed attempt must log one WARN and no ERROR; logs:\n%s", logs)
	}
}

func TestNodeQuestion_RedeliveredAnswerThatStillCannotBeSentIsLoggedAsLost(t *testing.T) {
	w, brk, _, _ := newQuestionTestInstance(t)
	failPublishesTo(brk, "ns/a2a/v1/agent/request/"+askerAgent)
	logs := captureLogs(w)

	var acks, nacks atomic.Int32
	msg := deliveredAnswer(t, &acks, &nacks)
	msg.Redelivered = true
	w.handleTaskRequest(t.Context(), mustParseRequest(t, msg.Payload), msg)

	if nacks.Load() != 1 || acks.Load() != 0 {
		t.Errorf("acks=%d nacks=%d, want 0 acks, 1 nack", acks.Load(), nacks.Load())
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "gave up on a user_input_response") {
		t.Errorf("the give-up after the redelivery must log at ERROR; logs:\n%s", logs)
	}
}

func TestNodeQuestion_AnswerThatCouldNotBePassedOnIsReturnedToTheQueue(t *testing.T) {
	brk := &capturingBroker{}
	failPublishesTo(brk, "ns/sam/v1/agent/forward/GreetWorkflow")
	w := &WorkflowInstance{
		cfg:             &WorkflowConfig{Name: "GreetWorkflow", Namespace: "ns"},
		svc:             &capturingServices{brk: brk},
		logger:          slog.Default(),
		executions:      make(map[string]*WorkflowExecution),
		executionsByID:  make(map[string]*WorkflowExecution),
		subTaskIndex:    make(map[string]string),
		finalizedRoutes: runtime.NewMapCache(),
	}

	var acks, nacks atomic.Int32
	msg := deliveredAnswer(t, &acks, &nacks)
	w.handleTaskRequest(t.Context(), mustParseRequest(t, msg.Payload), msg)

	if nacks.Load() != 1 || acks.Load() != 0 {
		t.Errorf("acks=%d nacks=%d, want the answer returned to the queue (0 acks, 1 nack)", acks.Load(), nacks.Load())
	}
}

func TestNodeQuestion_UnknownAnswerForAHeldRunIsSettledHere(t *testing.T) {
	w, brk, _, _ := newForwardTestInstance(t, a2a.UserProperties{UserID: "alice@example.com"})
	w.cfg.Namespace = "ns"

	var acks, nacks atomic.Int32
	msg := deliveredAnswer(t, &acks, &nacks)
	w.handleTaskRequest(t.Context(), mustParseRequest(t, msg.Payload), msg)

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("an answer to no recorded question on the AWE holding its run published %d messages; it must be dropped, not passed on", n)
	}
	if acks.Load() != 1 || nacks.Load() != 0 {
		t.Errorf("acks=%d nacks=%d, want it settled (1 ack)", acks.Load(), nacks.Load())
	}
}

func TestNodeQuestion_PassedOnAnswerFromAnotherUserIsRefused(t *testing.T) {
	w, brk, exec, _ := newQuestionTestInstance(t)

	answer := delegatorAnswer(t, "parent-task", "bob@example.com")
	w.handlePassedOnAnswer(t.Context(), &broker.Message{
		Topic:          "ns/sam/v1/agent/forward/GreetWorkflow",
		Payload:        answer.Payload,
		UserProperties: answer.UserProperties,
	})

	if n := len(brk.snapshot()); n != 0 {
		t.Errorf("another user's passed-on answer published %d messages", n)
	}
	if _, still := exec.Context.askerOf(questionRequestID); !still {
		t.Error("a refused answer must leave the question open for its owner")
	}
}
