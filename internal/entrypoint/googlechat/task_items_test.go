// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var errItemsUnreachable = errors.New("task items unreachable")

type flakyItems struct {
	taskItemStore

	mu           sync.Mutex
	failPut      string
	failDelete   string
	listFailures int
	lists        int
	puts         []string
}

func (f *flakyItems) setFailures(put, del string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPut, f.failDelete = put, del
}

func (f *flakyItems) List(ctx context.Context, taskID string) (map[string]string, error) {
	f.mu.Lock()
	f.lists++
	fail := f.listFailures > 0
	if fail {
		f.listFailures--
	}
	f.mu.Unlock()
	if fail {
		return nil, errItemsUnreachable
	}
	return f.taskItemStore.List(ctx, taskID)
}

func (f *flakyItems) Put(ctx context.Context, taskID, item, value string) error {
	f.mu.Lock()
	f.puts = append(f.puts, item)
	fail := item == f.failPut
	f.mu.Unlock()
	if fail {
		return errItemsUnreachable
	}
	return f.taskItemStore.Put(ctx, taskID, item, value)
}

func (f *flakyItems) Delete(ctx context.Context, taskID, item string) error {
	f.mu.Lock()
	fail := item == f.failDelete
	f.mu.Unlock()
	if fail {
		return errItemsUnreachable
	}
	return f.taskItemStore.Delete(ctx, taskID, item)
}

func (f *flakyItems) listCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

func (f *flakyItems) putItems() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.puts...)
}

func isForgotten(st *taskState) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.forgotten
}

func TestSubmitRefusesATaskWhoseDestinationCannotBeRecorded(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replicaWithTaskLog(t, chat)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: dmMeta().AgentName})
	a.cfg.DefaultAgentName = dmMeta().AgentName
	flaky := &flakyItems{taskItemStore: a.items, failPut: itemDestination}
	a.items = flaky

	res := a.process(t.Context(), &inboundEvent{
		Kind: kindMessage, Addressed: true, IsDM: true, Text: "hello",
		SpaceName: dmMeta().SpaceName, ThreadName: dmMeta().ThreadName, MessageName: dmMeta().SpaceName + "/messages/M1",
		SenderName: requesterName, SenderEmail: requesterEmail, SenderDisplayName: "Ada",
	})

	if res.outcome != outcomeTransient {
		t.Fatalf("outcome = %v, want transient so the message is redelivered", res.outcome)
	}
	if puts := flaky.putItems(); len(puts) != 1 || puts[0] != itemDestination {
		t.Fatalf("item writes = %q, want only the failed destination write", puts)
	}
	rig.mustReceiveNothing(t)
	records, err := store.NewTaskRecordRepo(a.core.Store(), a.gatewayID).TakeAll(t.Context(), "test-probe")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("task records = %+v, want the refused task's record removed", records)
	}
	a.tasksMu.Lock()
	tracked := len(a.tasks)
	a.tasksMu.Unlock()
	if tracked != 0 {
		t.Errorf("tracked tasks = %d, want none for a refused message", tracked)
	}
	if writes, _ := chat.snapshot(); len(writes) != 0 {
		t.Errorf("chat writes = %+v, want no status message for a task that was never published", writes)
	}
}

func TestLoadTask_ReportsAFailedReadAndIgnoresAnUnknownTask(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	a.items = &flakyItems{taskItemStore: a.items, listFailures: 1}

	if err := a.LoadTask(t.Context(), tid("task-1")); !errors.Is(err, errItemsUnreachable) {
		t.Fatalf("LoadTask on unreadable items = %v, want the read error", err)
	}
	if err := a.LoadTask(t.Context(), tid("task-1")); err != nil {
		t.Fatalf("LoadTask on a task with no items = %v, want nil", err)
	}
	if isTracked(a, tid("task-1")) {
		t.Error("a task with no items was tracked")
	}
}

func TestLoadTask_RebuildsTheTaskFromItsItems(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	submitter := newTestAdapter(t, db, chat)
	shared := sharedMeta()
	submitForTest(t, submitter, tid("task-l"), shared)
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	waitForItem(t, db, tid("task-l"), itemControlsMsg, status)
	waitForItem(t, db, tid("task-l"), itemStatusMsg, status)

	other := newTestAdapter(t, db, chat)
	if err := other.LoadTask(t.Context(), tid("task-l")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}

	other.tasksMu.Lock()
	st := other.tasks[tid("task-l")]
	other.tasksMu.Unlock()
	if st == nil {
		t.Fatal("LoadTask did not track the task")
	}
	got := st.snapshot()
	want := shared
	want.StatusMessage, want.ControlsMessage = status, status
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded meta = %+v, want %+v", got, want)
	}
	if !st.recovered {
		t.Error("a task loaded from its items is not marked recovered, so its final answer would not be written in full")
	}
	if other.taskRegistry.Metadata(tid("task-l")) == nil {
		t.Error("the loaded task has no registry metadata, so this replica is never told when another one ends it")
	}
}

func TestOnTaskEndedElsewhere_ReleasesTheTaskWithoutWriting(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	st := submitForTest(t, a, tid("task-e"), dmMeta())
	before, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	q := st.queue

	a.OnTaskEndedElsewhere(tid("task-e"))
	a.bgWG.Wait()

	if isTracked(a, tid("task-e")) {
		t.Error("the task is still tracked after another replica ended it")
	}
	if !isForgotten(st) {
		t.Error("the task's state was not released")
	}
	select {
	case <-q.doneCh:
	default:
		t.Error("the task's reply queue is still running")
	}
	if after, _ := chat.snapshot(); len(after) != len(before) {
		t.Errorf("releasing the task wrote to Chat: %+v", after[len(before):])
	}
}

func eventCore(t *testing.T, a *Adapter, dbURL string) (*entrypoint.GatewayCore, *broker.DevBroker) {
	t.Helper()
	return newEventCore(t, a, map[string]any{"type": "sql", "database_url": dbURL})
}

func newEventCore(t *testing.T, a *Adapter, sessionService map[string]any, opts ...runtime.Option) (*entrypoint.GatewayCore, *broker.DevBroker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	flags := features.NewRegistry()
	flags.Register(features.Definition{Key: features.KeyGoogleChatGateway, Default: true})
	a.rawCfg = map[string]any{"delivery": DeliveryPubSub, "pubsub_subscription": "projects/p/subscriptions/s"}
	a.subOverride = &fakeSub{}
	appConfig := map[string]any{"namespace": clickNamespace, "gateway_id": "gw-test"}
	if sessionService != nil {
		appConfig["session_service"] = sessionService
	}
	core := entrypoint.NewGatewayCore(a, appConfig)
	core.SetServices(runtime.NewRuntime(brk, nil, append([]runtime.Option{runtime.WithFeatureRegistry(flags)}, opts...)...).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	core.SetIdentityAuthorizer(matchedAuthorizer())
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return core, brk
}

func TestFinalEventIsHeldWhileTheTaskItemsCannotBeRead(t *testing.T) {
	const taskID = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a70"
	chat := newFakeChat()
	receiver := NewAdapter(nil)
	receiver.chatOverride = chat
	core, brk := eventCore(t, receiver, store.TestDBURL(t))
	flaky := &flakyItems{taskItemStore: receiver.items, listFailures: 2}
	receiver.items = flaky

	submitter := newTestAdapter(t, core.Store(), chat)
	submitForTest(t, submitter, taskID, dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	waitForPersistedStatus(t, submitter, taskID, status)

	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core start: %v", err)
	}
	payload, err := json.Marshal(a2a.NewResponse(taskID, completedTask("Held, then answered.")))
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Publish(t.Context(), a2a.GatewayResponseTopic(clickNamespace, "gw-test", taskID), &broker.Message{Payload: payload}); err != nil {
		t.Fatal(err)
	}

	writes, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "Held, then answered.") })
	if n := flaky.listCalls(); n < 3 {
		t.Errorf("item reads = %d, want the two failures retried before the answer", n)
	}
	if len(texts) != 1 {
		t.Errorf("messages = %q, want the answer in the submitter's status message", texts)
	}
	answered := false
	for _, w := range writes {
		answered = answered || (w.name == status && w.text == "Held, then answered.")
	}
	if !answered {
		t.Errorf("writes = %+v, want the answer written into %s", writes, status)
	}
}

func TestTerminalReplySurvivesAReplicaThatDiesWhileWritingIt(t *testing.T) {
	failed := completedTask("")
	failed.Status.State = a2a.TaskStateFailed
	for _, tc := range []struct {
		name     string
		terminal func(a *Adapter, taskID string)
		want     string
	}{
		{"completed", func(a *Adapter, taskID string) {
			a.OnFinalResponse(t.Context(), taskID, completedTask("The answer."), nil)
		}, "The answer."},
		{"failed", func(a *Adapter, taskID string) { a.OnFinalResponse(t.Context(), taskID, failed, nil) }, msgAgentError},
		{"error", func(a *Adapter, taskID string) {
			a.OnError(t.Context(), taskID, &a2a.Error{Code: -32603, Message: "agent crashed"}, nil)
		}, msgAgentError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := store.TestDB(t)
			dyingChat := newFakeChat()
			dying := newTestAdapter(t, db, dyingChat)
			submitForTest(t, dying, tid("task-k"), dmMeta())
			writes, texts := dyingChat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
			status := writes[0].name
			waitForPersistedStatus(t, dying, tid("task-k"), status)

			writing, killed := make(chan struct{}), make(chan struct{})
			var once sync.Once
			dyingChat.mu.Lock()
			dyingChat.onWrite = func(context.Context) error {
				once.Do(func() { close(writing) })
				<-killed
				return &permanentError{err: errors.New("replica killed")}
			}
			dyingChat.mu.Unlock()
			dyingDone := make(chan struct{})
			go func() {
				defer close(dyingDone)
				tc.terminal(dying, tid("task-k"))
			}()
			<-writing

			successorChat := newFakeChat()
			successorChat.messages[status] = texts[0]
			successorChat.order = []string{status}
			successor := newTestAdapter(t, db, successorChat)
			if err := successor.LoadTask(t.Context(), tid("task-k")); err != nil {
				t.Fatalf("LoadTask: %v", err)
			}
			tc.terminal(successor, tid("task-k"))
			close(killed)
			<-dyingDone
			waitTerminalWritten(t, successor)

			writes, texts = successorChat.snapshot()
			if len(texts) != 1 || texts[0] != tc.want {
				t.Errorf("messages = %q, want the successor's reply %q in the status message", texts, tc.want)
			}
			answered := false
			for _, w := range writes {
				answered = answered || (w.name == status && w.text == tc.want)
			}
			if !answered {
				t.Errorf("successor writes = %+v, want %q written into %s", writes, tc.want, status)
			}
		})
	}
}

func TestClickIsRetriedWhileTheTaskItemsCannotBeRead(t *testing.T) {
	const taskID = "0192a3b4-c5d6-7e8f-9a0b-1c2d3e4f5a72"
	for _, tc := range []struct {
		name   string
		action map[string]string
		method string
	}{
		{"answer", hilClick(actionHILSubmit, taskID), "message/send"},
		{"cancel", cancelClick(taskID), "tasks/cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newClickRig(t)
			db := store.TestDB(t)
			chat := newFakeChat()
			submitter := rig.replica(t, db, chat, matchedAuthorizer())
			other := rig.replica(t, db, chat, matchedAuthorizer())
			submitForTest(t, submitter, taskID, dmMeta())
			chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
			card := postCard(t, chat, dmMeta())
			before, _ := chat.snapshot()
			other.items = &flakyItems{taskItemStore: other.items, listFailures: 1}

			click := clickBy(dmMeta(), requesterName, card, tc.action)
			if res := other.process(t.Context(), click); res.outcome != outcomeTransient {
				t.Fatalf("outcome = %v, want transient so the click is redelivered", res.outcome)
			}
			other.bgWG.Wait()
			rig.mustReceiveNothing(t)
			if after, _ := chat.snapshot(); len(after) != len(before) {
				t.Errorf("writes = %+v, want nothing written for a click that is retried", after[len(before):])
			}

			if res := other.process(t.Context(), click); res.outcome != outcomeSubmitted {
				t.Fatalf("redelivered click outcome = %v, want submitted", res.outcome)
			}
			rig.mustReceive(t, tc.method)
		})
	}
}
