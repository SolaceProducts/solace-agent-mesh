// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

const remoteReportTool = "slow_report"

func strBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	return brk
}

func registerRemoteTool(t *testing.T, a *Agent, brk *broker.DevBroker) *remote.Executor {
	t.Helper()
	exe := remote.NewExecutor(brk, "test-ns", a.cfg.Name, remote.WithInvokeBuffer(2*time.Second))
	if err := exe.Start(context.Background()); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(exe.Stop)
	registerRemoteToolOn(t, a, exe, remoteReportTool)
	return exe
}

func registerRemoteToolOn(t *testing.T, a *Agent, exe *remote.Executor, name string) {
	t.Helper()
	if err := a.tools.Register(remote.NewTool(&remote.ToolConfig{ToolName: name, TimeoutSeconds: 30}, exe)); err != nil {
		t.Fatalf("register remote tool %s: %v", name, err)
	}
}

func heldSTR(t *testing.T, brk *broker.DevBroker, answer string) (corrIDs <-chan string, release func()) {
	t.Helper()
	return heldSTRReplying(t, brk, func(corrID string) []byte {
		payload, _ := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{"message": answer}}))
		return payload
	})
}

func heldSTRReplying(t *testing.T, brk *broker.DevBroker, reply func(corrID string) []byte) (corrIDs <-chan string, release func()) {
	t.Helper()
	return heldSTRForTool(t, brk, remoteReportTool, reply)
}

func heldSTRForTool(t *testing.T, brk *broker.DevBroker, toolName string, reply func(corrID string) []byte) (corrIDs <-chan string, release func()) {
	t.Helper()
	q, err := brk.CreateQueue("str-"+idgen.New(), broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create STR queue: %v", err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic("test-ns", toolName)); err != nil {
		t.Fatalf("subscribe STR queue to %s invokes: %v", toolName, err)
	}
	ids := make(chan string, 1)
	released := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		msg, recvErr := brk.Receive(ctx, q)
		if recvErr != nil {
			return
		}
		msg.Ack()
		ids <- msg.CorrelationID
		select {
		case <-released:
		case <-ctx.Done():
			return
		}
		if err := brk.Publish(context.Background(), msg.UserProperties[remotetool.PropReplyTo], &broker.Message{Payload: reply(msg.CorrelationID)}); err != nil {
			t.Errorf("publish STR reply: %v", err)
		}
	}()
	return ids, func() { close(released) }
}

func publishSTRReply(t *testing.T, brk *broker.DevBroker, replyTo, corrID, answer string) {
	t.Helper()
	payload, _ := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{"message": answer}}))
	if err := brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload}); err != nil {
		t.Errorf("publish STR reply: %v", err)
	}
}

func waitForCorrID(t *testing.T, ids <-chan string) string {
	t.Helper()
	select {
	case id := <-ids:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("the STR never received the invocation")
		return ""
	}
}

func TestRemoteToolCallPausesAndResumesOnTheReply(t *testing.T) {
	forEachDurableStore(t, func(t *testing.T, store session.Store) {
		agent, mock, _ := replicaOf(t, store, "owner")
		strBrk := strBroker(t)
		registerRemoteTool(t, agent, strBrk)
		mock.responses = []*llm.ChatResponse{
			singleToolCallResponse("call-r1", remoteReportTool),
			{Content: "The report is ready."},
		}
		ids, release := heldSTR(t, strBrk, "report ready")

		resp, err := agent.HandleTask(t.Context(), "task-remote", concUserMessage("build the report", idgen.New()), concurrentUser)
		if err != nil || resp != nil {
			t.Fatalf("the task should pause on the remote tool: resp=%v err=%v", resp, err)
		}
		corrID := waitForCorrID(t, ids)
		state := agent.pausedTasks.Load("task-remote")
		if state == nil {
			t.Fatal("the task is not paused")
		}
		pc := state.PendingCalls["call-r1"]
		if pc == nil || pc.Kind != PendingCallRemoteTool || pc.SubTaskID != corrID || pc.Timeout <= 0 {
			t.Fatalf("pending call = %+v, want a remote_tool call under %s with a timeout", pc, corrID)
		}
		if row, err := store.LookupPendingCall(context.Background(), agent.cfg.Name, corrID); err != nil || row == nil || row.ToolName != remoteReportTool {
			t.Fatalf("correlation row = %+v (err %v), want one naming the remote tool", row, err)
		}

		release()
		if !waitUntil(func() bool { return len(mock.getCalls()) == 2 && agent.pausedTasks.Load("task-remote") == nil }) {
			t.Fatalf("the task did not resume and finish: %d LLM calls", len(mock.getCalls()))
		}
		if !requestContains(mock.getCalls()[1], "report ready") {
			t.Error("the resumed turn did not see the tool's reply")
		}
	})
}

func TestCancellingATaskPausedOnARemoteToolCancelsTheCall(t *testing.T) {
	store := session.NewMemoryStore()
	agent, mock, _ := replicaOf(t, store, "owner")
	strBrk := strBroker(t)
	registerRemoteTool(t, agent, strBrk)
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool)}
	ids, _ := heldSTR(t, strBrk, "never sent")
	cancels, err := strBrk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create cancel subscription: %v", err)
	}
	if err := strBrk.Subscribe(cancels, "test-ns/>"); err != nil {
		t.Fatalf("subscribe to test-ns/>: %v", err)
	}

	if _, err := agent.HandleTask(t.Context(), "task-remote", concUserMessage("build the report", idgen.New()), concurrentUser); err != nil {
		t.Fatalf("task: %v", err)
	}
	corrID := waitForCorrID(t, ids)
	state := agent.pausedTasks.Load("task-remote")
	if state == nil {
		t.Fatal("the task is not paused")
	}
	agent.cancelPausedTask(state, cancelFromElsewhere)

	cancelTopic := remotetool.CancelTopic("test-ns", corrID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		msg, recvErr := strBrk.Receive(ctx, cancels)
		cancel()
		if recvErr == nil && msg != nil {
			msg.Ack()
			if msg.Topic == cancelTopic {
				return
			}
		}
	}
	t.Error("the STR was never told to cancel the paused call")
}

func TestRemoteToolReplyResumesATaskPausedOnAnotherReplica(t *testing.T) {
	forEachDurableStore(t, func(t *testing.T, store session.Store) {
		owner, ownerLLM, _ := replicaOf(t, store, "owner")
		survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
		strBrk := strBroker(t)
		registerRemoteTool(t, survivor, strBrk)
		survivor.installRemoteToolOrphanHandlers()

		const taskID = "task-remote-cross"
		corrID := idgen.New()
		state := pausedOnPeerCall(t, store, taskID, corrID)
		state.Messages[1] = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: remoteReportTool}}}
		state.PausedAssistantMsg = state.Messages[1]
		state.PendingCalls["tc-1"].ToolName = remoteReportTool
		state.PendingCalls["tc-1"].Kind = PendingCallRemoteTool
		state.PendingCalls["tc-1"].Timeout = time.Minute
		if err := owner.checkpoints.save(context.Background(), state); err != nil {
			t.Fatalf("owner checkpoint: %v", err)
		}
		loseOwner(t, store, state, 1)

		publishSTRReply(t, strBrk, remotetool.ResponseTopic("test-ns", "test-agent", corrID), corrID, "report ready")

		assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "report ready")
		if len(ownerLLM.getCalls()) != 0 {
			t.Errorf("the dead owner ran %d LLM calls", len(ownerLLM.getCalls()))
		}
	})
}
