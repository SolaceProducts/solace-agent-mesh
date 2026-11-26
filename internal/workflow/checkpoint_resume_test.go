// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const (
	checkpointReplyTo     = "test/reply/checkpoint"
	checkpointStatusTopic = "test/status/checkpoint"
)

type checkpointAWE struct {
	w           *WorkflowInstance
	brk         *capturingBroker
	cancelStart context.CancelFunc
}

func flagsOn(keys ...string) *features.Registry {
	reg := features.NewRegistry()
	for _, key := range keys {
		reg.Register(features.Definition{Key: key, Default: true})
	}
	return reg
}

func sharedStoreURL(t *testing.T) string {
	t.Helper()
	return "sqlite:///" + filepath.Join(t.TempDir(), "workflow.db")
}

func startCheckpointAWE(t *testing.T, cfg *WorkflowConfig, dbURL string, reg *features.Registry) *checkpointAWE {
	t.Helper()
	return startCheckpointAWEWith(t, cfg, dbURL, &stubServices{brk: &capturingBroker{}, features: reg})
}

func startCheckpointAWEWith(t *testing.T, cfg *WorkflowConfig, dbURL string, svc *stubServices) *checkpointAWE {
	t.Helper()
	cfg.HILDatabaseURL = dbURL
	brk := svc.brk.(*capturingBroker)
	w := NewWorkflowInstance(cfg)
	if err := w.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	startCtx, cancelStart := context.WithCancel(t.Context())
	if err := w.Start(startCtx); err != nil {
		cancelStart()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = w.Stop(context.Background())
		cancelStart()
	})
	return &checkpointAWE{w: w, brk: brk, cancelStart: cancelStart}
}

func (a *checkpointAWE) kill() {
	a.w.processStopping.Store(true)
	a.cancelStart()
}

func (a *checkpointAWE) stopForUpgrade(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := a.w.StopForProcessExit(ctx); err != nil {
		t.Fatalf("StopForProcessExit: %v", err)
	}
	a.cancelStart()
}

func (a *checkpointAWE) deliver(t *testing.T, taskID string, acks *ackCounter, redelivered bool) {
	t.Helper()
	req, msg := checkpointRequest(taskID, acks, redelivered)
	a.w.handleTaskRequest(t.Context(), *req, msg)
}

func checkpointRequest(taskID string, acks *ackCounter, redelivered bool) (*a2a.Request, *broker.Message) {
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{
		UserID: "alice", ReplyTo: checkpointReplyTo, A2AStatusTopic: checkpointStatusTopic,
	})
	msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() { acks.n.Add(1) }, func() {})
	msg.UserProperties = bmsg.UserProperties
	msg.Redelivered = redelivered
	return req, msg
}

func (a *checkpointAWE) answer(t *testing.T, agentName, text string) string {
	t.Helper()
	subTaskID := lastRequestIDSentTo(t, a.brk.snapshot(), a2a.AgentRequestTopic("test-ns", agentName))
	deliverSubTaskText(t, a.w, subTaskID, a2a.TaskStateCompleted, text)
	return subTaskID
}

func (a *checkpointAWE) sendsTo(agentName string) int {
	return messageSendsTo(a.brk.snapshot(), a2a.AgentRequestTopic("test-ns", agentName))
}

func lastRequestIDSentTo(t *testing.T, msgs []capturedMessage, topic string) string {
	t.Helper()
	var id string
	for _, m := range msgs {
		if m.Topic != topic || !strings.Contains(string(m.Payload), `"message/send"`) {
			continue
		}
		var req a2a.Request
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("unmarshal agent request: %v", err)
		}
		id = req.ID
	}
	if id == "" {
		t.Fatalf("no message/send published to %q", topic)
	}
	return id
}

func lastRetryIdentitySentTo(t *testing.T, msgs []capturedMessage, topic string) (string, int) {
	t.Helper()
	var group string
	attempt := -1
	for _, m := range msgs {
		if m.Topic != topic || !strings.Contains(string(m.Payload), `"message/send"`) {
			continue
		}
		var req struct {
			Params struct {
				Message a2a.Message `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("unmarshal agent request: %v", err)
		}
		group, _ = req.Params.Message.Metadata["retry_group_id"].(string)
		if n, ok := req.Params.Message.Metadata["retry_attempt"].(float64); ok {
			attempt = int(n)
		}
	}
	return group, attempt
}

func lastInputSentTo(t *testing.T, msgs []capturedMessage, topic string) string {
	t.Helper()
	var input string
	for _, m := range msgs {
		if m.Topic != topic || !strings.Contains(string(m.Payload), `"message/send"`) {
			continue
		}
		var req struct {
			Params struct {
				Message a2a.Message `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("unmarshal agent request: %v", err)
		}
		for _, part := range req.Params.Message.Parts {
			if part.File == nil || part.File.Bytes == "" {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(part.File.Bytes)
			if err != nil {
				t.Fatalf("decode input part: %v", err)
			}
			input = string(raw)
		}
	}
	return input
}

func deliverSubTaskText(t *testing.T, w *WorkflowInstance, subTaskID string, state a2a.TaskState, text string) {
	t.Helper()
	task := a2a.Task{
		ID:   subTaskID,
		Kind: "task",
		Status: a2a.TaskStatus{
			State:   state,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart(text)}},
		},
	}
	payload, err := json.Marshal(a2a.NewResponse(subTaskID, task))
	if err != nil {
		t.Fatalf("marshal sub-task response: %v", err)
	}
	w.handleAgentResponse(t.Context(), a2a.Request{ID: subTaskID}, &broker.Message{Payload: payload})
}

func checkpointOf(t *testing.T, a *checkpointAWE, taskID string) *wfstore.WorkflowRun {
	t.Helper()
	row, err := a.w.runOfTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	return row
}

func nodeStartPublishedFor(msgs []capturedMessage, subTaskID string) bool {
	for _, m := range msgs {
		payload := string(m.Payload)
		if m.Topic == checkpointStatusTopic && strings.Contains(payload, string(a2a.SignalWorkflowNodeExecStart)) && strings.Contains(payload, subTaskID) {
			return true
		}
	}
	return false
}

func workflowStartEvents(msgs []capturedMessage) int {
	n := 0
	for _, m := range msgs {
		if m.Topic == checkpointStatusTopic && strings.Contains(string(m.Payload), string(a2a.SignalWorkflowExecutionStart)) {
			n++
		}
	}
	return n
}

func fourStepWorkflow() *WorkflowConfig {
	return &WorkflowConfig{
		Name:      "four-steps",
		Namespace: "test-ns",
		Nodes: []*NodeConfig{
			{ID: "n1", Type: "agent", AgentName: "agent1"},
			{ID: "n2", Type: "agent", AgentName: "agent2", DependsOn: []string{"n1"}},
			{ID: "n3", Type: "agent", AgentName: "agent3", DependsOn: []string{"n2"}, Timeout: time.Hour,
				Input: map[string]any{"first": "{{n1.output}}", "second": "{{n2.output}}"}},
			{ID: "n4", Type: "agent", AgentName: "agent4", DependsOn: []string{"n3"},
				Input: map[string]any{"first": "{{n1.output}}", "third": "{{n3.output}}"}},
		},
	}
}

func TestCheckpointResume_SequenceContinuesAfterAKill(t *testing.T) {
	checkSequenceContinues(t, func(_ *testing.T, a *checkpointAWE) { a.kill() })
}

func TestCheckpointResume_SequenceContinuesAfterAnUpgradeStop(t *testing.T) {
	checkSequenceContinues(t, func(t *testing.T, a *checkpointAWE) { a.stopForUpgrade(t) })
}

func checkSequenceContinues(t *testing.T, stop func(*testing.T, *checkpointAWE)) {
	t.Helper()
	dbURL := sharedStoreURL(t)
	reg := flagsOn(features.KeyWorkflowCheckpointResume)
	first := startCheckpointAWE(t, fourStepWorkflow(), dbURL, reg)
	const taskID = "019e0000-0000-7000-8000-00000c0e0001"

	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "agent1", "output-of-n1")
	first.answer(t, "agent2", "output-of-n2")
	lostSubTask := lastRequestIDSentTo(t, first.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "agent3"))
	stop(t, first)

	second := startCheckpointAWE(t, fourStepWorkflow(), dbURL, reg)
	second.deliver(t, taskID, &secondAcks, true)

	if got := second.sendsTo("agent1") + second.sendsTo("agent2"); got != 0 {
		t.Fatalf("completed nodes dispatched %d times after the restart, want 0", got)
	}
	if got := second.sendsTo("agent3"); got != 1 {
		t.Fatalf("node in flight at the restart dispatched %d times, want 1", got)
	}
	n3Request := lastInputSentTo(t, second.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "agent3"))
	if !strings.Contains(n3Request, "output-of-n1") || !strings.Contains(n3Request, "output-of-n2") {
		t.Errorf("restored n3 request does not carry the outputs of n1 and n2: %s", n3Request)
	}
	if got := workflowStartEvents(second.brk.snapshot()); got != 0 {
		t.Errorf("restored run published %d workflow start events, want 0", got)
	}
	exec := executionFor(second.w, taskID)
	if got := exec.State.RetryCounts["n3"]; got != 0 {
		t.Errorf("n3 retry count after the restart = %d, want 0", got)
	}
	lostGroup, lostAttempt := lastRetryIdentitySentTo(t, first.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "agent3"))
	restoredGroup, restoredAttempt := lastRetryIdentitySentTo(t, second.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "agent3"))
	if restoredGroup != lostGroup || lostAttempt != 0 || restoredAttempt != 1 {
		t.Errorf("n3 retry identity: dead attempt %s/%d, restored %s/%d; want the same group, attempts 0 then 1",
			lostGroup, lostAttempt, restoredGroup, restoredAttempt)
	}

	assertSubTaskSuperseded(t, second, exec, lostSubTask, "agent3")
	restoredSubTask := lastRequestIDSentTo(t, second.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "agent3"))
	if !nodeStartPublishedFor(second.brk.snapshot(), restoredSubTask) {
		t.Error("restored n3 published no node start event for its new attempt")
	}

	second.answer(t, "agent3", "output-of-n3")
	n4Request := lastInputSentTo(t, second.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "agent4"))
	if !strings.Contains(n4Request, "output-of-n1") || !strings.Contains(n4Request, "output-of-n3") {
		t.Errorf("n4 request does not carry the outputs of n1 and n3: %s", n4Request)
	}
	second.answer(t, "agent4", "output-of-n4")

	if finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) == nil {
		t.Fatal("restored run did not answer its caller")
	}
	if finalTaskOn(t, first.brk.snapshot(), checkpointReplyTo) != nil {
		t.Error("the process that died answered the caller too")
	}
	if firstAcks.acks() != 0 || secondAcks.acks() != 1 {
		t.Errorf("acknowledgements: dead process %d, restoring process %d; want 0 and 1", firstAcks.acks(), secondAcks.acks())
	}
	if row := checkpointOf(t, second, taskID); !runAnswered(row) {
		t.Errorf("stored run of the finished run = %+v, want it marked answered", row)
	}
}
