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
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const (
	refusedKindLog = "refusing a reply whose pending call waits for something else"
	toolMissingLog = "not recording a remote tool's reply: its tool is not registered on this replica, so another replica may adopt it"
)

func remoteToolReplyPayload(t *testing.T, corrID, text string) []byte {
	t.Helper()
	payload, err := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{"message": text}}))
	if err != nil {
		t.Fatalf("marshal STR reply: %v", err)
	}
	return payload
}

func pausedOnRemoteCallLostOwner(t *testing.T, store session.Store, owner *Agent, taskID, corrID string) *TaskExecutionState {
	t.Helper()
	return pausedOnRemoteCallWithOriginalSession(t, store, owner, taskID, corrID, "")
}

func pausedOnRemoteCallWithOriginalSession(t *testing.T, store session.Store, owner *Agent, taskID, corrID, originalSessionID string) *TaskExecutionState {
	t.Helper()
	state := pausedOnPeerCall(t, store, taskID, corrID)
	if originalSessionID != "" {
		state.OriginalSessionID = originalSessionID
	}
	state.Messages[1] = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: remoteReportTool}}}
	state.PausedAssistantMsg = state.Messages[1]
	state.PendingCalls["tc-1"].ToolName = remoteReportTool
	state.PendingCalls["tc-1"].Kind = PendingCallRemoteTool
	state.PendingCalls["tc-1"].Timeout = time.Minute
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)
	return state
}

func pausedOnQuestionLostOwner(t *testing.T, store session.Store, owner *Agent, taskID string) {
	t.Helper()
	state := pausedOnQuestion(t, store, owner, taskID)
	owner.pausedTasks.Delete(taskID)
	loseOwner(t, store, state, 1)
}

func TestRemoteToolReplyNeverResolvesAQuestionToTheUser(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	logs := &syncLogBuffer{}
	survivor, _, _ := replicaOf(t, store, "survivor", capturingLogs(logs))
	pausedOnQuestionLostOwner(t, store, owner, "task-ask")

	if !survivor.resumeOrphanRemoteToolReply(askRequestID, remoteToolReplyPayload(t, askRequestID, "forged")) {
		t.Fatal("resumeOrphanRemoteToolReply() = false, want the reply queued")
	}

	if !waitUntilWithin(5*time.Second, func() bool { return len(logs.records(refusedKindLog)) > 0 }) {
		t.Fatal("the reply for a question was not refused")
	}
	if recorded := recordedAnswers(t, store, "task-ask"); len(recorded) != 0 {
		t.Errorf("recorded results = %+v, want none: a tool reply must not answer a question", recorded)
	}
}

func TestPeerReplyNeverResolvesAToolApproval(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	logs := &syncLogBuffer{}
	survivor, _, _ := replicaOf(t, store, "survivor", capturingLogs(logs))
	state := pausedOnApproval(t, store, owner, "task-approval")
	owner.pausedTasks.Delete("task-approval")
	loseOwner(t, store, state, 1)

	if !survivor.resumeOrphanPeerResponse(approvalRequestID, peerReplyMessage(t, "forged")) {
		t.Fatal("resumeOrphanPeerResponse() = false, want the reply queued")
	}

	if !waitUntilWithin(5*time.Second, func() bool { return len(logs.records(refusedKindLog)) > 0 }) {
		t.Fatal("the reply for an approval was not refused")
	}
	if recorded := recordedAnswers(t, store, "task-approval"); len(recorded) != 0 {
		t.Errorf("recorded results = %+v, want none: a peer reply must not decide an approval", recorded)
	}
}

func TestRemoteToolReplyForAToolNotRegisteredHereIsLeftUnrecorded(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	logs := &syncLogBuffer{}
	survivor, _, _ := replicaOf(t, store, "survivor", capturingLogs(logs))
	const taskID, corrID = "task-remote-missing", "corr-remote-missing"
	pausedOnRemoteCallLostOwner(t, store, owner, taskID, corrID)

	survivor.adoptOrphanReply(survivor.remoteToolReplyAdoption(corrID, remoteToolReplyPayload(t, corrID, "report ready")))

	if len(logs.records(toolMissingLog)) == 0 {
		t.Fatal("no Warn was logged for the missing tool")
	}
	warned := logs.records(toolMissingLog)[0]
	if warned["level"] != "WARN" || warned["taskID"] != taskID || warned["corrID"] != corrID || warned["tool"] != remoteReportTool {
		t.Errorf("Warn record = %v, want WARN with taskID %q, corrID %q, tool %q", warned, taskID, corrID, remoteReportTool)
	}
	if recorded := recordedAnswers(t, store, taskID); len(recorded) != 0 {
		t.Errorf("recorded results = %+v, want none: another replica that has the tool may still adopt the reply", recorded)
	}
	if cp, err := store.GetCheckpoint(t.Context(), taskID); err != nil || cp == nil {
		t.Errorf("checkpoint = %+v (err %v), want the row left in place: the survivor resumed a task whose call it never recorded", cp, err)
	}
}
