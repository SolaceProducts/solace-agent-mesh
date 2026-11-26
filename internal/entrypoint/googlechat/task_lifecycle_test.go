// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

const secretQuestion = "what is the launch code for project zebra"

func readyToSubmit(a *Adapter) {
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: dmMeta().AgentName})
	a.cfg.DefaultAgentName = dmMeta().AgentName
}

func dmMessage(id string) *inboundEvent {
	return &inboundEvent{
		Kind: kindMessage, Addressed: true, IsDM: true, Text: secretQuestion,
		SpaceName: dmMeta().SpaceName, ThreadName: dmMeta().ThreadName, MessageName: dmMeta().SpaceName + "/messages/" + id,
		SenderName: requesterName, SenderEmail: requesterEmail, SenderDisplayName: "Ada",
	}
}

func onlyTrackedTask(t *testing.T, a *Adapter) string {
	t.Helper()
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	if len(a.tasks) != 1 {
		t.Fatalf("tracked tasks = %d, want 1", len(a.tasks))
	}
	for id := range a.tasks {
		return id
	}
	return ""
}

func trackedState(a *Adapter, taskID string) *taskState {
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	return a.tasks[taskID]
}

func countRows(t *testing.T, db *store.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubmitterReleasesATaskAnotherReplicaEnded(t *testing.T) {
	chat := newFakeChat()
	submitter := NewAdapter(nil)
	submitter.chatOverride = chat
	core, _ := eventCore(t, submitter, store.TestDBURL(t))
	readyToSubmit(submitter)
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core start: %v", err)
	}

	if res := submitter.process(t.Context(), dmMessage("M1")); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}
	taskID := onlyTrackedTask(t, submitter)
	st := trackedState(submitter, taskID)
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	waitForPersistedStatus(t, submitter, taskID, writes[0].name)

	other := newTestAdapter(t, core.Store(), chat)
	if err := other.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	other.OnFinalResponse(t.Context(), taskID, completedTask("Answered elsewhere."), nil)
	waitTerminalWritten(t, other)
	answered, _ := chat.snapshot()

	core.RunTaskSweepNow()
	waitUntil(t, "the submitter releases the task", func() bool { return !isTracked(submitter, taskID) && isForgotten(st) })
	st.mu.Lock()
	q := st.queue
	st.mu.Unlock()
	select {
	case <-q.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the task's reply queue is still running")
	}
	if after, _ := chat.snapshot(); len(after) != len(answered) {
		t.Errorf("releasing the task wrote to Chat: %+v", after[len(answered):])
	}
}

type afterPutItems struct {
	taskItemStore
	after func()
	once  sync.Once
}

func (a *afterPutItems) Put(ctx context.Context, taskID, item, value string) error {
	if err := a.taskItemStore.Put(ctx, taskID, item, value); err != nil {
		return err
	}
	a.once.Do(a.after)
	return nil
}

func TestRefusedSubmitLeavesNoTaskBehind(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replicaWithTaskLog(t, chat)
	a.core.SetIdentityAuthorizer(&stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupMatched}})
	a.items = &afterPutItems{taskItemStore: a.items, after: func() { a.core.SetAuthzEnabled(true) }}
	readyToSubmit(a)

	if res := a.process(t.Context(), dmMessage("M1")); res.outcome != outcomeUnauthorized {
		t.Fatalf("outcome = %v, want unauthorized", res.outcome)
	}
	rig.mustReceiveNothing(t)
	if n := countRows(t, a.core.Store(), "task_records"); n != 0 {
		t.Errorf("task records = %d, want the refused task's record removed", n)
	}
	if n := countRows(t, a.core.Store(), "entrypoint_task_items"); n != 0 {
		t.Errorf("task items = %d, want the refused task's destination removed", n)
	}
	a.tasksMu.Lock()
	tracked := len(a.tasks)
	a.tasksMu.Unlock()
	if tracked != 0 {
		t.Errorf("tracked tasks = %d, want none for a refused message", tracked)
	}
}

func TestWithoutADatabaseATaskIsAnsweredFromMemory(t *testing.T) {
	chat := newFakeChat()
	a := NewAdapter(nil)
	a.chatOverride = chat
	newEventCore(t, a, nil)
	if a.items != nil {
		t.Fatal("a gateway without a database has shared task items")
	}
	readyToSubmit(a)

	if res := a.process(t.Context(), dmMessage("M1")); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}
	taskID := onlyTrackedTask(t, a)
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	a.OnFinalResponse(t.Context(), taskID, completedTask("Answered from memory."), nil)
	waitTerminalWritten(t, a)
	if _, texts := chat.snapshot(); len(texts) != 1 || texts[0] != "Answered from memory." {
		t.Errorf("messages = %q, want the answer in the status message", texts)
	}

	if err := a.LoadTask(t.Context(), "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a73"); err != nil {
		t.Errorf("LoadTask of an unknown task = %v, want nil", err)
	}
}

func TestDestinationItemHoldsNoEmailOrContent(t *testing.T) {
	rig := newClickRig(t)
	a := rig.replicaWithTaskLog(t, newFakeChat())
	readyToSubmit(a)
	if res := a.process(t.Context(), dmMessage("M1")); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted", res.outcome)
	}

	raw, found, err := store.NewEntrypointTaskItemRepo(a.core.Store()).Get(t.Context(), onlyTrackedTask(t, a), itemDestination)
	if err != nil || !found {
		t.Fatalf("destination: found=%v err=%v", found, err)
	}
	if strings.Contains(raw, requesterEmail) || strings.Contains(raw, secretQuestion) {
		t.Errorf("destination %s carries the sender's email or message", raw)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		switch key {
		case "spaceName", "threadName", "senderName", "sessionId", "agentName", "isDm", "runAsSwapped", "addOn":
		default:
			t.Errorf("destination carries %q, which is not a routing field", key)
		}
	}
}

func TestDestinationStoredFormIsStable(t *testing.T) {
	const stored = `{"spaceName":"spaces/S1","threadName":"spaces/S1/threads/T1","senderName":"users/1",` +
		`"sessionId":"sess-1","agentName":"agent","isDm":true,"runAsSwapped":true,"addOn":true}`
	want := taskDestination{
		SpaceName: "spaces/S1", ThreadName: "spaces/S1/threads/T1", SenderName: "users/1",
		SessionID: "sess-1", AgentName: "agent", IsDM: true, RunAsSwapped: true, AddOn: true,
	}
	got, err := decodeDestination(stored)
	if err != nil || got != want {
		t.Fatalf("decoded %+v, %v; want %+v", got, err, want)
	}
	encoded, err := json.Marshal(want)
	if err != nil || string(encoded) != stored {
		t.Errorf("encoded %s, %v; want %s", encoded, err, stored)
	}
}

func TestDestinationWithoutASpaceIsNotRecorded(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	meta := dmMeta()
	meta.SpaceName = ""
	if a.recordTask(t.Context(), tid("task-n"), requesterEmail, &taskState{meta: meta}) {
		t.Fatal("a task with no space was recorded")
	}
	if isTracked(a, tid("task-n")) {
		t.Error("a task with no space is tracked")
	}
}

func TestUndecodableDestinationIsNotHeldAndItsReplyLossIsAnError(t *testing.T) {
	for name, raw := range map[string]string{"invalid": "not json", "no space": `{"spaceName":""}`} {
		t.Run(name, func(t *testing.T) {
			a := newTestAdapter(t, store.TestDB(t), newFakeChat())
			logs := recordLogs(a)
			taskID := tid(tid("task-u"))
			if err := a.taskRegistry.Register(taskID, "agent", requesterEmail, entrypoint.TaskIdentity{}); err != nil {
				t.Fatal(err)
			}
			if err := a.items.Put(t.Context(), taskID, itemDestination, raw); err != nil {
				t.Fatal(err)
			}

			if err := a.LoadTask(t.Context(), taskID); err != nil {
				t.Fatalf("LoadTask = %v, want nil so the event is not held", err)
			}
			if isTracked(a, taskID) {
				t.Fatal("a task whose destination cannot be decoded is tracked")
			}
			a.OnFinalResponse(t.Context(), taskID, completedTask("Lost."), nil)
			if n := logs.count(slog.LevelError, "google chat reply dropped: the task destination cannot be decoded"); n != 1 {
				t.Errorf("undecodable reply-loss errors = %d, want 1", n)
			}
		})
	}
}

func TestTerminalEventWithoutALoadIsWrittenAfterAFailedRead(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	submitter := newTestAdapter(t, db, chat)
	submitForTest(t, submitter, tid("task-t"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	waitForPersistedStatus(t, submitter, tid("task-t"), status)

	other := newTestAdapter(t, db, chat)
	other.items = &flakyItems{taskItemStore: other.items, listFailures: 1}
	other.OnError(t.Context(), tid("task-t"), &a2a.Error{Code: -32603, Message: "agent crashed"}, nil)

	chat.waitFor(t, func(w []fakeWrite, _ []string) bool {
		for _, write := range w {
			if write.name == status && write.text == msgAgentError {
				return true
			}
		}
		return false
	})
}

func TestAnchorWriteThatFailedIsWrittenWithTheNextChange(t *testing.T) {
	const status = "spaces/DM1/messages/S"
	for _, tc := range []struct {
		name                string
		failPut, failDelete string
		recorded, want      queueAnchors
	}{
		{"status put", itemStatusMsg, "", queueAnchors{}, queueAnchors{Status: status}},
		{"status delete", "", itemStatusMsg, queueAnchors{Status: status}, queueAnchors{Prompted: true}},
		{"prompted put", itemPrompted, "", queueAnchors{}, queueAnchors{Prompted: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := store.TestDB(t)
			a := newTestAdapter(t, db, newFakeChat())
			taskID := tid("task-w")
			if err := a.taskRegistry.Register(taskID, "agent", requesterEmail, entrypoint.TaskIdentity{}); err != nil {
				t.Fatal(err)
			}
			if tc.recorded.Status != "" {
				if err := a.items.Put(t.Context(), taskID, itemStatusMsg, tc.recorded.Status); err != nil {
					t.Fatal(err)
				}
			}
			flaky := &flakyItems{taskItemStore: a.items, failPut: tc.failPut, failDelete: tc.failDelete}
			a.items = flaky

			recorded := a.writeAnchors(taskID, tc.recorded, tc.want)
			if reflect.DeepEqual(recorded, tc.want) {
				t.Fatalf("recorded %+v after a failed write, want the old value kept", recorded)
			}
			flaky.setFailures("", "")
			if recorded = a.writeAnchors(taskID, recorded, tc.want); !reflect.DeepEqual(recorded, tc.want) {
				t.Fatalf("recorded %+v with the next change, want %+v", recorded, tc.want)
			}

			items, err := store.NewEntrypointTaskItemRepo(db).List(t.Context(), taskID)
			if err != nil {
				t.Fatal(err)
			}
			_, prompted := items[itemPrompted]
			if items[itemStatusMsg] != tc.want.Status || prompted != tc.want.Prompted {
				t.Errorf("items = %v, want status %q and prompted %v", items, tc.want.Status, tc.want.Prompted)
			}
		})
	}
}

func TestControlsAreRecordedBeforeTheStatusMessage(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	a := newTestAdapter(t, db, chat)
	recording := &flakyItems{taskItemStore: a.items}
	a.items = recording
	taskID := tid("task-o")
	submitForTest(t, a, taskID, dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	waitForItem(t, db, taskID, itemStatusMsg, writes[0].name)

	puts := recording.putItems()
	want := []string{itemDestination, itemControlsMsg, itemStatusMsg}
	if len(puts) != len(want) || puts[0] != want[0] || puts[1] != want[1] || puts[2] != want[2] {
		t.Errorf("item writes = %q, want %q", puts, want)
	}
}

func TestPromptUnrecordsTheStatusMessageBeforeRecordingPrompted(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	a := newTestAdapter(t, db, chat)
	flaky := &flakyItems{taskItemStore: a.items}
	a.items = flaky
	taskID := tid("task-p")
	st := submitForTest(t, a, taskID, dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	waitForItem(t, db, taskID, itemStatusMsg, writes[0].name)
	flaky.setFailures("", itemStatusMsg)

	st.queue.Prompt("❓ Which day?", "Which day?", nil)
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) >= 2 })
	waitUntil(t, "the prompt's anchors are reported", func() bool { return st.snapshot().Prompted })
	items := store.NewEntrypointTaskItemRepo(db)
	if _, found, err := items.Get(t.Context(), taskID, itemPrompted); err != nil || found {
		t.Fatalf("prompted found=%v err=%v while the status message is still recorded, want it not written", found, err)
	}

	flaky.setFailures("", "")
	st.queue.Prompt("❓ Which time?", "Which time?", nil)
	waitForItem(t, db, taskID, itemPrompted, "")
	if _, found, err := items.Get(t.Context(), taskID, itemStatusMsg); err != nil || found {
		t.Errorf("status message still recorded (found=%v err=%v) after the prompt", found, err)
	}
}

func TestTimeoutFiringAsTheAnswerFinishesWritesNoSecondNotice(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	a := newTestAdapter(t, db, chat)
	taskID := tid("task-f")
	submitForTest(t, a, taskID, dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	waitForPersistedStatus(t, a, taskID, writes[0].name)
	a.taskRegistry = &removeHook{TaskRegistry: a.taskRegistry, before: func(id string) {
		a.OnError(t.Context(), id, &a2a.Error{Code: -32603, Message: "agent crashed"}, nil)
	}}

	a.OnFinalResponse(t.Context(), taskID, completedTask("The answer."), nil)
	waitTerminalWritten(t, a)

	if _, texts := chat.snapshot(); len(texts) != 1 || texts[0] != "The answer." {
		t.Errorf("messages = %q, want only the answer", texts)
	}
}

type removeHook struct {
	entrypoint.TaskRegistry
	before func(taskID string)
	fired  atomic.Bool
}

func (r *removeHook) Remove(taskID string) {
	if r.fired.CompareAndSwap(false, true) {
		r.before(taskID)
	}
	r.TaskRegistry.Remove(taskID)
}

type hangingItems struct {
	taskItemStore
	mu    sync.Mutex
	reads int
}

func (h *hangingItems) List(ctx context.Context, _ string) (map[string]string, error) {
	h.mu.Lock()
	h.reads++
	h.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (h *hangingItems) readCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads
}

func TestTerminalEventWhoseReadsHangIsReportedLost(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	logs := recordLogs(a)
	hanging := &hangingItems{taskItemStore: a.items}
	a.items = hanging
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()

	a.OnError(ctx, tid("task-h"), &a2a.Error{Code: -32603, Message: "task timed out"}, nil)

	if n := logs.count(slog.LevelError, "google chat reply dropped: the task items could not be read"); n != 1 {
		t.Errorf("lost-reply errors = %d, want 1", n)
	}
	if n := hanging.readCount(); n != terminalLoadAttempts {
		t.Errorf("reads = %d, want %d, each with its share of the time", n, terminalLoadAttempts)
	}
}

func TestClickWithAMalformedTaskIDIsNotFound(t *testing.T) {
	rig := newClickRig(t)
	db := store.TestDB(t)
	chat := newFakeChat()
	a := rig.replica(t, db, chat, matchedAuthorizer())
	a.items = &flakyItems{taskItemStore: a.items, listFailures: 1}
	card := postCard(t, chat, dmMeta())

	for _, id := range []string{"not-a-uuid", "urn:uuid:" + tid("task-m")} {
		res := a.process(t.Context(), clickBy(dmMeta(), requesterName, card, cancelClick(id)))
		if res.outcome != outcomeDeclined {
			t.Errorf("click on %q: outcome = %v, want declined as not found", id, res.outcome)
		}
	}
	a.bgWG.Wait()
	rig.mustReceiveNothing(t)
}

func TestMessageWithoutASpaceIsIgnored(t *testing.T) {
	ev, err := parseEvent([]byte(`{"type":"MESSAGE","user":{"name":"users/1","email":"ada@example.com"},
		"message":{"name":"spaces/DM1/messages/M1","text":"hello"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != kindIgnored || ev.IgnoreReason != "message_without_space" {
		t.Errorf("parsed kind=%v reason=%q, want ignored as message_without_space", ev.Kind, ev.IgnoreReason)
	}
}
