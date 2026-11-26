// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const itemsPhone = "15551230200"

func countSent(bodies [][]byte, text string) int {
	n := 0
	for _, b := range bodies {
		if strings.Contains(string(b), text) {
			n++
		}
	}
	return n
}

func TestAdapter_LoadTaskReadsTheDestinationRow(t *testing.T) {
	env := newPromptEnv(t)
	first := env.start(t)
	if err := first.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+itemsPhone, entrypoint.TaskIdentity{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := first.taskItems().writeDestination(t.Context(), taskT1, itemsPhone, "sess-1"); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	env.stop(t, first)

	second := env.start(t)
	if second.getTaskMeta(taskT1) != nil {
		t.Fatal("a fresh instance already holds the task")
	}
	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	meta := second.getTaskMeta(taskT1)
	if meta == nil || meta.FromPhone != itemsPhone || meta.SessionID != "sess-1" {
		t.Fatalf("loaded meta = %+v, want the destination row's user and session", meta)
	}
	second.OnFinalResponse(t.Context(), taskT1, completedTask("answer from the other instance"), nil)
	waitQueueDrained(t, second)
	if countSent(env.mock.recorded(), "answer from the other instance") != 1 {
		t.Error("the answer did not reach the user")
	}
}

func TestAdapter_LoadTaskBridgesATaskWithNoRow(t *testing.T) {
	env := newPromptEnv(t)
	first := env.start(t)
	sessionID, err := first.resolveSessionID(t.Context(), buildExternalKey(itemsPhone, 1700000000), promptAgent, "whatsapp:"+itemsPhone)
	if err != nil {
		t.Fatalf("resolveSessionID: %v", err)
	}
	if err := first.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+itemsPhone, entrypoint.TaskIdentity{SessionID: sessionID}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	env.stop(t, first)

	second := env.start(t)
	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	meta := second.getTaskMeta(taskT1)
	if meta == nil || meta.FromPhone != itemsPhone || meta.SessionID != sessionID {
		t.Fatalf("bridged meta = %+v, want the session's sender %s and session %s", meta, itemsPhone, sessionID)
	}
}

func TestAdapter_LoadTaskOfAnUnknownTaskIsNotAnError(t *testing.T) {
	env := newPromptEnv(t)
	a := env.start(t)
	if err := a.LoadTask(t.Context(), "018f0000-0000-7000-8000-000000000000"); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if a.getTaskMeta("018f0000-0000-7000-8000-000000000000") != nil {
		t.Fatal("an unknown task got metadata")
	}
}

func TestAuthPrompt_RowsOutliveTheInstance(t *testing.T) {
	env := newPromptEnv(t)
	first := env.start(t)
	if err := first.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+itemsPhone, entrypoint.TaskIdentity{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := first.taskItems().writeDestination(t.Context(), taskT1, itemsPhone, "sess-1"); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	if err := first.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	first.OnStatusUpdate(t.Context(), taskT1, authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	waitQueueDrained(t, first)
	items, err := first.taskItems().list(t.Context(), taskT1)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	cards := 0
	for item := range items {
		if strings.HasPrefix(item, itemAuthPromptPrefix) {
			cards++
		}
	}
	if cards != 1 {
		t.Fatalf("auth-prompt rows = %d, want 1", cards)
	}
	env.stop(t, first)

	second := env.start(t)
	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	second.OnStatusUpdate(t.Context(), taskT1, authResultEvent(true), nil)
	waitQueueDrained(t, second)
	second.OnFinalResponse(t.Context(), taskT1, completedTask("done after authorizing"), nil)
	waitQueueDrained(t, second)
	sent := env.mock.recorded()
	if countSent(sent, "Authorized") != 1 {
		t.Errorf("authorized notices = %d, want exactly 1", countSent(sent, "Authorized"))
	}
	if countSent(sent, "done after authorizing") != 1 {
		t.Error("the answer did not reach the user")
	}
}

func TestAdapter_SavedFileRowsOutliveTheInstance(t *testing.T) {
	env := newPromptEnv(t)
	first := env.start(t)
	if err := first.core.Tasks().Register(taskT1, promptAgent, "whatsapp:"+itemsPhone, entrypoint.TaskIdentity{SessionID: "sess-1"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := first.taskItems().writeDestination(t.Context(), taskT1, itemsPhone, "sess-1"); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	if err := first.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	first.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: "chart.png", Version: 2, MIMEType: "image/png"}, nil), nil)
	first.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: "scratch.txt", MIMEType: "text/plain"}, nil), nil)
	first.OnStatusUpdate(t.Context(), taskT1, artifactSavedEvent(t, a2a.ArtifactSavedData{Filename: "scratch.txt", MIMEType: "text/plain", Tags: []string{a2a.ArtifactTagWorking}}, nil), nil)
	env.stop(t, first)

	second := env.start(t)
	if err := second.LoadTask(t.Context(), taskT1); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	saves := second.recordedSaves(t.Context(), taskT1, second.getTaskMeta(taskT1))
	if len(saves.files) != 1 || saves.files[0].Filename != "chart.png" || saves.files[0].Version == nil || *saves.files[0].Version != 2 || saves.files[0].MIMEType != "image/png" {
		t.Fatalf("saved files = %+v, want chart.png at version 2", saves.files)
	}
	if saves.overflowed {
		t.Error("two saves read as overflowed")
	}
}

func TestAdapter_OnTaskEndedElsewhereReleasesTheUserSide(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	const task = "task-ended-elsewhere"
	a.senders.taskStarted(itemsPhone, task)
	a.typingTrack(itemsPhone, task, "wamid.in")

	a.OnTaskEndedElsewhere(task)

	if a.senders.runningTasks(itemsPhone) != 0 {
		t.Error("the task still counts against the sender's limit")
	}
	if a.typingTracked(itemsPhone, task) {
		t.Error("the task still holds the typing indicator")
	}
}

func TestTaskItems_NoDatabaseIsANoOp(t *testing.T) {
	t.Parallel()
	items := taskItems{logger: discardLogger()}
	if items.enabled() {
		t.Fatal("items with no repo read as enabled")
	}
	if err := items.writeDestination(t.Context(), taskT1, itemsPhone, "sess"); err != nil {
		t.Fatalf("writeDestination: %v", err)
	}
	if items.put(t.Context(), taskT1, itemDestination, "{}") {
		t.Fatal("a put with no repo reports written")
	}
	items.delete(t.Context(), taskT1, itemDestination)
	if claimed, err := items.claim(t.Context(), taskT1, itemAuthPromptPrefix+"x"); claimed || err != nil {
		t.Fatalf("claim with no repo = %v, %v; want neither", claimed, err)
	}
	if rows, err := items.list(t.Context(), taskT1); err != nil || rows != nil {
		t.Fatalf("list = %v, %v; want nothing", rows, err)
	}
}

func TestMetaFromItems(t *testing.T) {
	t.Parallel()
	logger := discardLogger()
	if meta := metaFromItems(map[string]string{}, logger, taskT1); meta != nil {
		t.Fatalf("no destination: meta = %+v, want nil", meta)
	}
	if meta := metaFromItems(map[string]string{itemDestination: "{not json"}, logger, taskT1); meta != nil {
		t.Fatalf("unreadable destination: meta = %+v, want nil", meta)
	}
	meta := metaFromItems(map[string]string{
		itemDestination:                 `{"user":"15551230200","session":"sess-1"}`,
		itemSavedFilePrefix + "b.png":   `{"mime":"image/png","version":1,"seq":2}`,
		itemSavedFilePrefix + "a.txt":   `{"mime":"text/plain","seq":1}`,
		itemSavedFilePrefix + "bad.bin": `{not json`,
		itemSavedFilesOverflowed:        "",
		itemAuthPromptPrefix + "row-2":  "",
		itemAuthPromptPrefix + "row-1":  "",
	}, logger, taskT1)
	if meta == nil || meta.FromPhone != "15551230200" || meta.SessionID != "sess-1" {
		t.Fatalf("meta = %+v, want the destination's user and session", meta)
	}
	if len(meta.savedArtifacts) != 2 || meta.savedArtifacts[0].Filename != "a.txt" || meta.savedArtifacts[1].Filename != "b.png" {
		t.Errorf("saved files = %+v, want a.txt then b.png", meta.savedArtifacts)
	}
	if !meta.savesOverflowed {
		t.Error("the overflow marker was not read")
	}
	if len(meta.authPromptIDs) != 2 || meta.authPromptIDs[0] != "row-1" {
		t.Errorf("auth prompts = %v, want [row-1 row-2]", meta.authPromptIDs)
	}
	if sender, ok := senderOfExternalKey("whatsapp:15551230200:1700000000"); !ok || sender != "15551230200" {
		t.Errorf("senderOfExternalKey = %q, %v", sender, ok)
	}
	for _, key := range []string{"slack:C1:T1", "whatsapp:", "whatsapp:nocolon"} {
		if _, ok := senderOfExternalKey(key); ok {
			t.Errorf("senderOfExternalKey(%q) accepted a key that names no sender", key)
		}
	}
}

type failingDestinationRepo struct{ taskItemRepo }

func (f failingDestinationRepo) Put(ctx context.Context, taskID, item, value string) error {
	if item == itemDestination {
		return errors.New("database unavailable")
	}
	return f.taskItemRepo.Put(ctx, taskID, item, value)
}

func TestSubmit_RefusesATaskWhoseDestinationCannotBeRecorded(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.items = taskItems{repo: failingDestinationRepo{store.NewEntrypointTaskItemRepo(a.core.Store())}, logger: a.logger}
	a.itemsOnce.Do(func() {})

	brk := a.core.Services().Broker()
	agentQ, err := brk.CreateQueue("no-destination-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if subErr := brk.Subscribe(agentQ, a2a.AgentRequestTopic("test-ns", "Builder")); subErr != nil {
		t.Fatalf("subscribe agent topic: %v", subErr)
	}

	value, msg := textMessage("wamid.NODEST", itemsPhone, "no destination")
	value.Metadata.PhoneNumberID = "555111"
	if got := a.deliver(value, msg); got != settleNack {
		t.Fatalf("settlement = %v, want nack so the message is delivered again", got)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if published, _ := brk.Receive(ctx, agentQ); published != nil {
		t.Fatal("a task with no destination row was published")
	}
	if n := a.core.InFlightTaskCount(); n != 0 {
		t.Fatalf("in-flight tasks = %d, want 0: the task record was not removed", n)
	}
}
