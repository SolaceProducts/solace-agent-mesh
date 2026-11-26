// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func dmMeta() taskMeta {
	return taskMeta{taskDestination: taskDestination{SpaceName: "spaces/DM1", ThreadName: "spaces/DM1/threads/T1", SenderName: "users/1", IsDM: true, AgentName: "agent"}}
}

func completedTask(text string) *a2a.Task {
	return &a2a.Task{Status: a2a.TaskStatus{
		State:   a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: text}}},
	}}
}

func progressEvent(text string) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{
		Kind: "data", Data: map[string]any{"type": "agent_progress_update", "status_text": text},
	}}}}}
}

func textEvent(text string) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: text}}}}}
}

func TestSecondReplicaRecoversFinalResponseIntoPersistedStatusMessage(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)

	submitForTest(t, replicaA, tid("task-1"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	statusMessage := writes[0].name
	waitForPersistedStatus(t, replicaB, tid("task-1"), statusMessage)

	if err := replicaB.LoadTask(t.Context(), tid("task-1")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaB.OnFinalResponse(t.Context(), tid("task-1"), completedTask("Answer from replica **B**"), nil)

	writes, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "replica *B*") })
	last := writes[len(writes)-1]
	if last.op != "update" || last.name != statusMessage {
		t.Fatalf("final write = %+v, want an update of the status message %q", last, statusMessage)
	}
	if len(texts) != 1 {
		t.Errorf("messages = %q, want the one status message rewritten, not a second post", texts)
	}

	replicaB.OnError(t.Context(), tid("task-1"), &a2a.Error{Message: "late duplicate terminal"}, nil)
	if after, _ := chat.snapshot(); len(after) != len(writes) {
		t.Errorf("a second terminal event wrote again: %+v", after[len(writes):])
	}
}

func waitForPersistedStatus(t *testing.T, a *Adapter, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if meta, state, err := a.readTaskItems(t.Context(), taskID); err == nil && state == destinationFound && meta.StatusMessage == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status message %q was never persisted for %s", want, taskID)
}

func TestRecoveredTaskStreamsIntoStatusMessageAndFinishesInFull(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	submitForTest(t, replicaA, tid("task-2"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	waitForPersistedStatus(t, replicaA, tid("task-2"), status)

	replicaB := newTestAdapter(t, db, chat)
	if err := replicaB.LoadTask(t.Context(), tid("task-2")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaB.OnStatusUpdate(t.Context(), tid("task-2"), textEvent("the answer so"), nil)
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "the answer so") })

	if _, state, err := replicaB.readTaskItems(t.Context(), tid("task-2")); err != nil || state != destinationFound {
		t.Fatalf("task items after recovered status updates: state=%v err=%v, want still present", state, err)
	}

	replicaB.OnFinalResponse(t.Context(), tid("task-2"), completedTask("The complete answer."), nil)
	waitTerminalWritten(t, replicaB)
	writes, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "The complete answer.") })
	if len(texts) != 1 {
		t.Errorf("messages = %q, want the answer in the submitter's status message only", texts)
	}
	if last := writes[len(writes)-1]; last.name != status || !last.replacedCards || last.cards != 0 {
		t.Errorf("last write = %+v, want the submitter's Cancel control cleared from %s", last, status)
	}
	if _, state, _ := replicaB.readTaskItems(t.Context(), tid("task-2")); state != destinationMissing {
		t.Error("the task's items outlived the task")
	}
}

func TestFinalResponseForUnknownTaskIsDroppedWithoutWriting(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)

	a.OnFinalResponse(t.Context(), "never-submitted", completedTask("orphan"), nil)

	time.Sleep(50 * time.Millisecond)
	if writes, _ := chat.snapshot(); len(writes) != 0 {
		t.Errorf("writes for an unknown task = %+v, want none", writes)
	}
}

func TestOwnerDoesNotRewriteStreamedTextOnFinal(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-3"), dmMeta())

	a.OnStatusUpdate(t.Context(), tid("task-3"), textEvent("Hello "), nil)
	a.OnStatusUpdate(t.Context(), tid("task-3"), textEvent("world."), nil)
	a.OnFinalResponse(t.Context(), tid("task-3"), completedTask("Hello world."), nil)

	writes, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "Hello world.") })
	if len(texts) != 1 || texts[0] != "Hello world." {
		t.Errorf("messages = %q, want the placeholder rewritten to exactly the streamed answer", texts)
	}
	finalWrites := 0
	for _, w := range writes {
		if w.text == "Hello world." && !w.replacedCards {
			finalWrites++
		}
	}
	if finalWrites != 1 {
		t.Errorf("the final answer was written %d times, want once", finalWrites)
	}
}

func TestCancelledTaskShowsCancelNoticeInStatusMessage(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-4"), dmMeta())

	a.OnFinalResponse(t.Context(), tid("task-4"), &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil)

	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgTaskCanceled) })
	if len(texts) != 1 {
		t.Errorf("messages = %q, want the notice in the status message", texts)
	}
}

func TestAuthorizationLinkOnlyInDirectMessages(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	shared := dmMeta()
	shared.IsDM = false
	submitForTest(t, a, tid("task-5"), shared)

	authEvent := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{
		Kind: "data", Data: map[string]any{"type": "authentication_required", "auth_uri": "https://accounts.example.com/authorize?state=secret"},
	}}}}}
	a.OnStatusUpdate(t.Context(), tid("task-5"), authEvent, nil)

	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "Message me directly") })
	if anyTextContains(texts, "accounts.example.com") {
		t.Errorf("the authorization link reached a shared space: %q", texts)
	}
}

func TestAuthorizePrompt(t *testing.T) {
	cases := []struct {
		uri, displayName, want string
		ok                     bool
	}{
		{uri: "https://accounts.example.com/o/authorize", displayName: "Drive", want: "access Drive on", ok: true},
		{uri: "https://accounts.example.com/o/authorize", want: "access accounts.example.com on", ok: true},
		{uri: "http://accounts.example.com/o/authorize"},
		{uri: "javascript:alert(1)"},
		{uri: "https:///no-host"},
		{uri: "%zz"},
	}
	for _, tc := range cases {
		text, ok := authorizePrompt(&a2a.AuthenticationRequiredData{AuthURI: tc.uri, ToolDisplayName: tc.displayName})
		if ok != tc.ok || !strings.Contains(text, tc.want) {
			t.Errorf("authorizePrompt(%q, %q) = (%q, %v), want ok=%v containing %q", tc.uri, tc.displayName, text, ok, tc.ok, tc.want)
		}
	}
}
