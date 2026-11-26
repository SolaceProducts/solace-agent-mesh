// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func eventPayload(t *testing.T, event map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type":     "event_callback",
		"team_id":  "T1",
		"event_id": "Ev1",
		"event":    event,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func dmEvent() map[string]any {
	return map[string]any{
		"type": "message", "channel_type": "im", "channel": twoInstanceChannel,
		"user": "U1", "text": "test 7", "ts": twoInstanceThread,
	}
}

func deliver(t *testing.T, a *SlackGatewayAdapter, payload json.RawMessage) bool {
	t.Helper()
	evtAPI, err := parseInbound(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	acked := false
	a.receiveEvent(evtAPI, payload, func() { acked = true })
	a.wg.Wait()
	return acked
}

func taskCount(a *SlackGatewayAdapter) int {
	return len(a.core.Tasks().AllByAgent("TestAgent"))
}

func inboundRow(t *testing.T, a *SlackGatewayAdapter, key string) string {
	t.Helper()
	var state string
	err := a.core.Store().QueryRowContext(t.Context(),
		`SELECT state FROM entrypoint_inbound_messages WHERE gateway_id = 'test-gw' AND message_id = ?`, key).Scan(&state)
	if err != nil {
		return ""
	}
	return state
}

func TestInbound_CopyOnOtherInstanceStartsNoTask(t *testing.T) {
	inst, _ := newSlackInstances(t, 2, SlackConfig{InitialStatusMessage: "Working on it"})
	first, second := inst[0], inst[1]
	payload := eventPayload(t, dmEvent())

	if !deliver(t, first, payload) {
		t.Fatal("the first delivery was not acknowledged")
	}
	if !deliver(t, second, payload) {
		t.Fatal("the copy was not acknowledged")
	}
	if taskCount(first) != 1 || taskCount(second) != 0 {
		t.Fatalf("tasks = %d and %d, want one, on the first instance", taskCount(first), taskCount(second))
	}
	if state := inboundRow(t, first, inboundMessageKey(twoInstanceChannel, twoInstanceThread)); state != store.InboundDone {
		t.Fatalf("record state = %q, want done", state)
	}
}

func TestInbound_ReplayedOnOtherInstance(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{InitialStatusMessage: "Working on it"})
	dead, survivor := inst[0], inst[1]
	key := inboundMessageKey(twoInstanceChannel, twoInstanceThread)
	if _, err := dead.core.AcceptInbound(t.Context(), key, string(eventPayload(t, dmEvent()))); err != nil {
		t.Fatal(err)
	}

	lapsed, err := store.NewEntrypointInboundRepo(survivor.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10)
	if err != nil || len(lapsed) != 1 {
		t.Fatalf("TakeLapsed = %+v, %v", lapsed, err)
	}
	survivor.ReplayInbound(t.Context(), entrypoint.InboundMessage{
		ID: lapsed[0].MessageID, Payload: lapsed[0].Payload, Token: lapsed[0].Token, Attempts: lapsed[0].Attempts,
	})

	if taskCount(survivor) != 1 {
		t.Fatalf("tasks on the surviving instance = %d, want 1", taskCount(survivor))
	}
	if got := threadTexts(api); len(got) != 1 || got[0] != "Working on it" {
		t.Fatalf("thread = %q, want the status message of the replayed task", got)
	}
	if state := inboundRow(t, survivor, key); state != store.InboundDone {
		t.Fatalf("record state = %q, want done", state)
	}
}

func TestInbound_ReplayKeepsFiles(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	const fileURL = "https://files.slack.com/files-pri/T1-F1/report.csv"
	api.SeedFile(fileURL, []byte("a,b,c"))
	event := dmEvent()
	event["files"] = []map[string]any{{"id": "F1", "name": "report.csv", "mimetype": "text/csv", "url_private_download": fileURL}}
	key := inboundMessageKey(twoInstanceChannel, twoInstanceThread)

	a.ReplayInbound(t.Context(), entrypoint.InboundMessage{ID: key, Payload: string(eventPayload(t, event))})

	if taskCount(a) != 1 {
		t.Fatalf("tasks = %d, want the replayed one", taskCount(a))
	}
	fetched := slices.ContainsFunc(api.Calls(), func(c slackapi.RecordedCall) bool {
		return c.Method == "GetFile" && c.Params["url"] == fileURL
	})
	if !fetched {
		t.Fatal("the replayed message's file was not fetched for the task")
	}
}

func TestInbound_AbandonedMessageTellsTheUser(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	inst[0].AbandonInbound(t.Context(), entrypoint.InboundMessage{
		ID: inboundMessageKey(twoInstanceChannel, twoInstanceThread), Payload: string(eventPayload(t, dmEvent())),
	})
	if got := threadTexts(api); !slices.Contains(got, msgInboundAbandoned) {
		t.Fatalf("thread = %q, want the apology", got)
	}
	inst[0].AbandonInbound(t.Context(), entrypoint.InboundMessage{ID: "x", Payload: "not json"})
	inst[0].ReplayInbound(t.Context(), entrypoint.InboundMessage{ID: "x", Payload: "not json"})
}

func TestInbound_RecordedPayloadWithoutInnerEvent(t *testing.T) {
	payloads := map[string]string{
		"null event":    `{"type":"event_callback","team_id":"T1","event_id":"Ev1","event":null}`,
		"missing event": `{"type":"event_callback","team_id":"T1","event_id":"Ev1"}`,
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			if _, err := parseInbound(payload); err == nil {
				t.Fatal("parseInbound accepted an event_callback without an inner event")
			}

			inst, api := newSlackInstances(t, 1, SlackConfig{})
			a := inst[0]
			key := inboundMessageKey(twoInstanceChannel, twoInstanceThread)
			claim, err := a.core.AcceptInbound(t.Context(), key, payload)
			if err != nil {
				t.Fatal(err)
			}

			msg := entrypoint.InboundMessage{ID: key, Payload: payload, Token: claim.Token}
			if !a.AbandonInbound(t.Context(), msg) {
				t.Fatal("AbandonInbound of an unreadable payload reported the notice as undelivered")
			}
			if got := threadTexts(api); len(got) != 0 {
				t.Fatalf("thread = %q, want no notice for an unreadable payload", got)
			}

			a.ReplayInbound(t.Context(), msg)
			if taskCount(a) != 0 {
				t.Fatalf("tasks = %d, want none for an unreadable payload", taskCount(a))
			}
			if state := inboundRow(t, a, key); state != store.InboundDone {
				t.Fatalf("record state = %q, want done so the unreadable payload is not retried", state)
			}
		})
	}
}

func TestInbound_NotRecordedNotAcknowledged(t *testing.T) {
	inst, _ := newSlackInstances(t, 1, SlackConfig{})
	_ = inst[0].core.Store().Close()
	if deliver(t, inst[0], eventPayload(t, dmEvent())) {
		t.Fatal("a message that was not recorded was acknowledged")
	}
	if taskCount(inst[0]) != 0 {
		t.Fatal("a message that was not recorded started a task")
	}
}

func TestInbound_OnlyTaskEventsRecorded(t *testing.T) {
	inst, _ := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	const channel, ts = "C9", "1700000020.000001"
	key := inboundMessageKey(channel, ts)

	channelMessage := map[string]any{"type": "message", "channel_type": "channel", "channel": channel, "user": "U1", "text": "<@UBOT> hi", "ts": ts}
	if !deliver(t, a, eventPayload(t, channelMessage)) {
		t.Fatal("ignored channel message not acknowledged")
	}
	if state := inboundRow(t, a, key); state != "" {
		t.Fatalf("ignored channel message recorded as %q", state)
	}

	mention := map[string]any{"type": "app_mention", "channel": channel, "user": "U1", "text": "<@UBOT> hi", "ts": ts}
	if !deliver(t, a, eventPayload(t, mention)) {
		t.Fatal("mention not acknowledged")
	}
	if state := inboundRow(t, a, key); state != store.InboundDone {
		t.Fatalf("mention record state = %q, want done", state)
	}
	if taskCount(a) != 1 {
		t.Fatalf("tasks = %d, want 1", taskCount(a))
	}

	bot := map[string]any{"type": "message", "channel_type": "im", "channel": "D2", "bot_id": "B1", "text": "from a bot", "ts": "1.1"}
	if !deliver(t, a, eventPayload(t, bot)) {
		t.Fatal("bot message not acknowledged")
	}
	if state := inboundRow(t, a, inboundMessageKey("D2", "1.1")); state != "" {
		t.Fatalf("bot message recorded as %q", state)
	}
}

func unreadableItems(t *testing.T) taskItems {
	t.Helper()
	closed, err := store.Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = closed.Close()
	return taskItems{repo: store.NewEntrypointTaskItemRepo(closed), warn: testLogger().Warn, debug: testLogger().Debug}
}

func TestTaskItemsUnreadable_LoadTask(t *testing.T) {
	inst, _ := newSlackInstances(t, 2, SlackConfig{InitialStatusMessage: "Working on it"})
	submitting, receiving := inst[0], inst[1]
	taskID := submitDM(t, submitting)

	working := receiving.items
	receiving.items = unreadableItems(t)
	if err := receiving.LoadTask(t.Context(), taskID); err == nil {
		t.Fatal("LoadTask on a GWE that cannot read the task's items reported no error")
	}
	receiving.items = working

	working = submitting.items
	submitting.items = unreadableItems(t)
	if err := submitting.LoadTask(t.Context(), taskID); err != nil {
		t.Fatalf("LoadTask on the submitting GWE = %v, want its own copy used", err)
	}
	meta, _ := submitting.core.Tasks().Metadata(taskID).(*SlackTaskMeta)
	if meta == nil || meta.syncedFromItems.Load() {
		t.Fatal("the submitting GWE's copy must stay unsynced after a failed read")
	}
	submitting.items = working
	if err := submitting.LoadTask(t.Context(), taskID); err != nil || meta.syncedFromItems.Load() {
		t.Fatalf("LoadTask within refreshRetryDelay of the failed read = %v, synced %v; want no read yet", err, meta.syncedFromItems.Load())
	}
	meta.refreshRetryAt.Store(0)
	if err := submitting.LoadTask(t.Context(), taskID); err != nil || !meta.syncedFromItems.Load() {
		t.Fatalf("LoadTask once the items can be read = %v, synced %v; want the copy refreshed", err, meta.syncedFromItems.Load())
	}
}

func TestTaskItemsUnreadable_OwnTextNotResumed(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{InitialStatusMessage: "Working on it"})
	a := inst[0]
	taskID := submitDM(t, a)

	working := a.items
	a.items = unreadableItems(t)
	if err := a.LoadTask(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	a.OnStatusUpdate(t.Context(), taskID, textStatus("Hello "), nil)
	a.queues[taskID].Flush()
	a.items = working
	a.items.put(t.Context(), taskID, itemTextStarted, "")
	meta, _ := a.core.Tasks().Metadata(taskID).(*SlackTaskMeta)
	meta.refreshRetryAt.Store(0)

	for _, text := range []string{"big ", "world"} {
		if err := a.LoadTask(t.Context(), taskID); err != nil {
			t.Fatal(err)
		}
		a.OnStatusUpdate(t.Context(), taskID, textStatus(text), nil)
	}
	a.OnFinalResponse(t.Context(), taskID, completedTask("Hello big world"), nil)

	if got := strings.Join(threadTexts(api), "|"); !strings.Contains(got, "Hello big world") {
		t.Fatalf("thread = %q, want the whole answer", got)
	}
}

func TestLoadTask_NoDestinationIsNotAnError(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	before := len(api.Calls())
	if err := inst[0].LoadTask(t.Context(), "01a00000-0000-7000-8000-000000000001"); err != nil {
		t.Fatalf("LoadTask for an unknown task = %v, want nil", err)
	}
	inst[0].OnStatusUpdate(t.Context(), "01a00000-0000-7000-8000-000000000001", textStatus("late"), nil)
	if n := len(api.Calls()) - before; n != 0 {
		t.Fatalf("Slack calls for an unknown task = %d, want 0", n)
	}
}

func TestTwoInstances_AbandonedAnswerClaimTakenOver(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{})
	first, second := inst[0], inst[1]
	taskID := submitDM(t, first)
	first.OnStatusUpdate(t.Context(), taskID, inputRequired("req-3"), nil)
	blocks, ok := first.buildHILBlocks(parseSurfaceT(t), "req-3", taskID, "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	click := func(a *SlackGatewayAdapter) {
		a.hilDedup.Delete("req-3")
		a.handleHILInput(goslack.InteractionCallback{
			User:      goslack.User{ID: "U1", Name: "alice"},
			Container: goslack.Container{ChannelID: twoInstanceChannel, MessageTs: "1700000000.6"},
		}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitButtonValue(t, blocks, "submit-btn")})
	}
	api.SeedMessage(twoInstanceChannel, "1700000000.6", "prompt")
	item := itemHILAnsweredPrefix + "req-3"
	repo := first.items.repo

	if _, err := repo.Claim(t.Context(), taskID, item, strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
		t.Fatal(err)
	}
	click(second)
	if n := countCalls(api.Calls(), "UpdateMessage"); n != 0 {
		t.Fatalf("card updates = %d, want 0 while another instance holds a fresh claim", n)
	}

	stale := strconv.FormatInt(time.Now().Add(-hilAnswerLease-time.Second).UnixMilli(), 10)
	if err := repo.Put(t.Context(), taskID, item, stale); err != nil {
		t.Fatal(err)
	}
	click(second)
	if n := countCalls(api.Calls(), "UpdateMessage"); n != 1 {
		t.Fatalf("card updates = %d, want 1: the abandoned claim is taken over", n)
	}
	if v, _, _ := repo.Get(t.Context(), taskID, item); v != hilAnswerForwarded {
		t.Fatalf("claim = %q, want %q after the forward", v, hilAnswerForwarded)
	}

	click(first)
	if n := countCalls(api.Calls(), "UpdateMessage"); n != 1 {
		t.Fatalf("card updates = %d, want no second answer once one was forwarded", n)
	}
}

func hilForwards(t *testing.T, a *SlackGatewayAdapter, name string) (*broker.DevBroker, func() bool) {
	t.Helper()
	brk := a.core.Services().Broker().(*broker.DevBroker)
	q, err := brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}
	return brk, func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()
		_, err := brk.Receive(ctx, q)
		return err == nil
	}
}

func TestHILInput_FailedForwardLeavesTheCardForARetry(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	taskID := submitDM(t, a)
	a.OnStatusUpdate(t.Context(), taskID, inputRequired("req-5"), nil)
	blocks, ok := a.buildHILBlocks(parseSurfaceT(t), "req-5", taskID, "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	api.SeedMessage(twoInstanceChannel, "1700000000.8", "prompt")
	brk, forwarded := hilForwards(t, a, "hil-retry")
	item := itemHILAnsweredPrefix + "req-5"
	click := func() {
		a.handleHILInput(goslack.InteractionCallback{
			User:      goslack.User{ID: "U1", Name: "alice"},
			Container: goslack.Container{ChannelID: twoInstanceChannel, MessageTs: "1700000000.8"},
		}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitButtonValue(t, blocks, "submit-btn")})
	}

	if err := brk.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	click()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(api.Calls(), "UpdateMessage"); n != 0 {
		t.Fatalf("card updates = %d, want 0: the answer never left this instance", n)
	}
	if _, found, _ := a.items.repo.Get(t.Context(), taskID, item); found {
		t.Fatal("the claim is still held after the failed forward")
	}
	if !slices.ContainsFunc(api.Messages(), func(m slackapi.StoredMessage) bool {
		return strings.Contains(m.Text, "please try again")
	}) {
		t.Fatal("the user was not told to try again")
	}

	click()
	if !forwarded() {
		t.Fatal("the retry was not forwarded")
	}
	if n := countCalls(api.Calls(), "UpdateMessage"); n != 1 {
		t.Fatalf("card updates = %d, want 1 after the forward", n)
	}
	if v, _, _ := a.items.repo.Get(t.Context(), taskID, item); v != hilAnswerForwarded {
		t.Fatalf("claim = %q, want %q", v, hilAnswerForwarded)
	}
}

func TestHILInput_ForwardedAnswerIsNotForwardedAgain(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{})
	first, second := inst[0], inst[1]
	taskID := submitDM(t, first)
	first.OnStatusUpdate(t.Context(), taskID, inputRequired("req-6"), nil)
	blocks, ok := first.buildHILBlocks(parseSurfaceT(t), "req-6", taskID, "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	api.SeedMessage(twoInstanceChannel, "1700000000.9", "prompt")
	_, forwarded := hilForwards(t, first, "hil-marked")
	click := func(a *SlackGatewayAdapter) {
		a.handleHILInput(goslack.InteractionCallback{
			User:      goslack.User{ID: "U1", Name: "alice"},
			Container: goslack.Container{ChannelID: twoInstanceChannel, MessageTs: "1700000000.9"},
		}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitButtonValue(t, blocks, "submit-btn")})
	}

	api.SetNextError("UpdateMessage", errors.New("ratelimited"))
	click(first)
	if !forwarded() {
		t.Fatal("the answer was not forwarded")
	}
	if n := countCalls(api.Calls(), "UpdateMessage"); n != 0 {
		t.Fatalf("card updates = %d, want the failed edit only", n)
	}

	click(second)
	if forwarded() {
		t.Fatal("the answer was forwarded twice")
	}
	if n := countCalls(api.Calls(), "UpdateMessage"); n != 0 {
		t.Fatalf("card updates = %d, want none", n)
	}
}

func TestStatusUpdate_FileUploadedBeforeReturn(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	taskID := submitDM(t, a)
	event := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateWorking,
		Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{{
			Kind: "file",
			File: &a2a.FileContent{Name: "report.txt", MIMEType: "text/plain", Bytes: base64.StdEncoding.EncodeToString([]byte("hello"))},
		}}},
	}}
	before := len(api.Calls())
	a.OnStatusUpdate(t.Context(), taskID, event, nil)
	if n := countCalls(api.Calls()[before:], "CompleteUploadExternal"); n != 1 {
		t.Fatalf("uploads finished before return = %d, want 1", n)
	}
}

func TestInbound_UnsettledMessageKeepsItsRecord(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	payload := eventPayload(t, dmEvent())
	key := inboundMessageKey(twoInstanceChannel, twoInstanceThread)
	claim, err := a.core.AcceptInbound(t.Context(), key, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	a.cancel()
	api.SetNextError("PostMessage", errors.New("context canceled"))
	evtAPI, err := parseInbound(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	hold := &inboundHold{key: key, token: claim.Token}
	a.settleInbound(hold, a.dispatchInbound(evtAPI, nil, hold))

	if state := inboundRow(t, a, key); state != store.InboundProcessing {
		t.Fatalf("record state = %q, want processing for another instance to finish", state)
	}
	if taskCount(a) != 0 {
		t.Fatal("a task was started by a stopping instance")
	}
}

func TestInbound_TakenOverBeforeSubmitIsNotSubmitted(t *testing.T) {
	inst, _ := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	payload := eventPayload(t, dmEvent())
	key := inboundMessageKey(twoInstanceChannel, twoInstanceThread)
	claim, err := a.core.AcceptInbound(t.Context(), key, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewEntrypointInboundRepo(a.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10); err != nil {
		t.Fatal(err)
	}
	evtAPI, _ := parseInbound(string(payload))
	hold := &inboundHold{key: key, token: claim.Token}
	if settled := a.dispatchInbound(evtAPI, nil, hold); settled {
		t.Fatal("a message this instance no longer holds was settled here")
	}
	if taskCount(a) != 0 {
		t.Fatal("a message taken over by another instance was submitted here")
	}
}

func TestInbound_SubmitPastItsLeaseRenewsAndSettles(t *testing.T) {
	inst, _ := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	payload := eventPayload(t, dmEvent())
	key := inboundMessageKey(twoInstanceChannel, twoInstanceThread)
	if _, err := a.core.AcceptInbound(t.Context(), key, string(payload)); err != nil {
		t.Fatal(err)
	}
	lapsed := time.Now().Add(-3 * time.Minute).UnixMilli()
	if _, err := a.core.Store().ExecContext(t.Context(),
		`UPDATE entrypoint_inbound_messages SET claimed_at = ?`, lapsed); err != nil {
		t.Fatal(err)
	}
	evtAPI, _ := parseInbound(string(payload))
	hold := &inboundHold{key: key, token: lapsed}
	settled := a.dispatchInbound(evtAPI, nil, hold)
	if !settled || taskCount(a) != 1 {
		t.Fatalf("settled %v, tasks %d; want the message submitted once", settled, taskCount(a))
	}
	taken, err := store.NewEntrypointInboundRepo(a.core.Store()).TakeLapsed(t.Context(), "test-gw", 2*time.Minute, 10)
	if err != nil || len(taken) != 0 {
		t.Fatalf("TakeLapsed after the submit = %+v, %v; want nothing, the lease was renewed", taken, err)
	}
	a.settleInbound(hold, settled)
	if state := inboundRow(t, a, key); state != store.InboundDone {
		t.Fatalf("record state = %q, want done with the renewed token", state)
	}
}

func TestInbound_MentionReplayedAndAbandonedInItsThread(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	const channel, ts = "C7", "1700000030.000001"
	mention := map[string]any{"type": "app_mention", "channel": channel, "user": "U1", "text": "<@UBOT> hi", "ts": ts}
	payload := string(eventPayload(t, mention))
	key := inboundMessageKey(channel, ts)
	if _, err := a.core.AcceptInbound(t.Context(), key, payload); err != nil {
		t.Fatal(err)
	}
	lapsed, err := store.NewEntrypointInboundRepo(a.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10)
	if err != nil || len(lapsed) != 1 {
		t.Fatalf("TakeLapsed = %+v, %v", lapsed, err)
	}
	a.ReplayInbound(t.Context(), entrypoint.InboundMessage{ID: key, Payload: payload, Token: lapsed[0].Token})
	if taskCount(a) != 1 || inboundRow(t, a, key) != store.InboundDone {
		t.Fatalf("tasks = %d, record = %q; want the mention submitted and settled", taskCount(a), inboundRow(t, a, key))
	}

	if !a.AbandonInbound(t.Context(), entrypoint.InboundMessage{ID: key, Payload: payload}) {
		t.Fatal("the apology was not reported as posted")
	}
	posted := false
	for _, m := range api.Messages() {
		if m.Channel == channel && m.ThreadTS == ts && m.Text == msgInboundAbandoned {
			posted = true
		}
	}
	if !posted {
		t.Fatal("the apology was not posted in the mention's thread")
	}
}

func TestInbound_IgnoredEventsNotRecorded(t *testing.T) {
	inst, _ := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	a.botUserID, a.botID, a.identityResolved = "UBOT", "BBOT", true
	edit := map[string]any{"type": "message", "subtype": "message_changed", "channel_type": "im", "channel": "D3", "ts": "2.2",
		"message": map[string]any{"text": "edited", "ts": "1.1", "user": "U1"}}
	own := map[string]any{"type": "message", "channel_type": "im", "channel": "D3", "user": "UBOT", "bot_id": "BBOT", "text": "my reply", "ts": "3.3"}
	for _, ev := range []map[string]any{edit, own} {
		if !deliver(t, a, eventPayload(t, ev)) {
			t.Fatal("an ignored event was not acknowledged")
		}
	}
	for _, key := range []string{inboundMessageKey("D3", "2.2"), inboundMessageKey("D3", "3.3")} {
		if state := inboundRow(t, a, key); state != "" {
			t.Fatalf("ignored event %s recorded as %q", key, state)
		}
	}
}

func TestInbound_DisplayAsBotMessageStartsTask(t *testing.T) {
	for _, value := range []any{true, false, nil} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			inst, _ := newSlackInstances(t, 1, SlackConfig{})
			event := dmEvent()
			event["display_as_bot"] = value

			if !deliver(t, inst[0], eventPayload(t, event)) {
				t.Fatal("message not acknowledged")
			}
			if taskCount(inst[0]) != 1 {
				t.Fatalf("tasks = %d, want 1", taskCount(inst[0]))
			}
		})
	}
}

func TestInbound_MalformedDisplayAsBotRejectedAndSettled(t *testing.T) {
	inst, _ := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	event := dmEvent()
	event["display_as_bot"] = "false"
	payload := string(eventPayload(t, event))

	if _, err := parseInbound(payload); err == nil {
		t.Fatal("parseInbound accepted a non-boolean display_as_bot")
	}

	key := inboundMessageKey(twoInstanceChannel, twoInstanceThread)
	claim, err := a.core.AcceptInbound(t.Context(), key, payload)
	if err != nil {
		t.Fatal(err)
	}
	a.ReplayInbound(t.Context(), entrypoint.InboundMessage{ID: key, Payload: payload, Token: claim.Token})

	if taskCount(a) != 0 {
		t.Fatalf("tasks = %d, want none for an unparseable message", taskCount(a))
	}
	if state := inboundRow(t, a, key); state != store.InboundDone {
		t.Fatalf("record state = %q, want done", state)
	}
}

func TestStatusUpdate_ArtifactMessageFlushedBeforeReturn(t *testing.T) {
	inst, api := newSlackInstances(t, 1, SlackConfig{})
	a := inst[0]
	taskID := submitDM(t, a)
	event := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateWorking,
		Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
			"type": "artifact_creation_progress", "status": "completed", "filename": "report.md", "description": "the report",
		})}},
	}}
	a.OnStatusUpdate(t.Context(), taskID, event, nil)
	if !slices.ContainsFunc(threadTexts(api), func(s string) bool { return strings.Contains(s, "report.md") }) {
		t.Fatalf("thread = %q, want the artifact message in Slack before return", threadTexts(api))
	}
}

func TestHILInput_LostClaimReleasesTheLocalGuard(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{})
	first, second := inst[0], inst[1]
	taskID := submitDM(t, first)
	first.OnStatusUpdate(t.Context(), taskID, inputRequired("req-4"), nil)
	blocks, ok := first.buildHILBlocks(parseSurfaceT(t), "req-4", taskID, "TestAgent", false)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	api.SeedMessage(twoInstanceChannel, "1700000000.7", "prompt")
	if _, err := first.items.repo.Claim(t.Context(), taskID, itemHILAnsweredPrefix+"req-4", strconv.FormatInt(time.Now().UnixMilli(), 10)); err != nil {
		t.Fatal(err)
	}
	second.handleHILInput(goslack.InteractionCallback{
		User:      goslack.User{ID: "U1", Name: "alice"},
		Container: goslack.Container{ChannelID: twoInstanceChannel, MessageTs: "1700000000.7"},
	}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitButtonValue(t, blocks, "submit-btn")})
	if _, held := second.hilDedup.Get("req-4"); held {
		t.Fatal("the local guard is still held after the claim was lost")
	}
}

func TestTwoInstances_AuthorizeMetAgainIsRetired(t *testing.T) {
	inst, api := newSlackInstances(t, 2, SlackConfig{})
	submitting, receiving := inst[0], inst[1]
	taskID := submitDM(t, submitting)
	submitting.OnStatusUpdate(t.Context(), taskID, textStatus("working"), nil)
	submitting.queues[taskID].WaitDrain()
	authEvent := authRequiredEvent("https://idp.example/authorize?state=s2")
	authEvent.Status.Message.Parts[0].Data.(map[string]any)["auth_request_function_call_id"] = "call-2"
	receiving.OnStatusUpdate(t.Context(), taskID, authEvent, nil)
	submitting.OnStatusUpdate(t.Context(), taskID, authEvent, nil)
	submitting.OnFinalResponse(t.Context(), taskID, completedTask("done"), nil)
	if !slices.Contains(threadTexts(api), "Authorized") {
		t.Fatalf("thread = %q, want the other instance's button retired", threadTexts(api))
	}
}

func TestFlush_StopAndTimeout(t *testing.T) {
	q := NewMessageQueue("t1", "C1", "1.1", slackapi.NewInMemorySlackAPI(), false, testLogger())
	flushTimeout = 50 * time.Millisecond
	t.Cleanup(func() { flushTimeout = time.Minute })
	start := time.Now()
	q.Flush()
	if time.Since(start) < flushTimeout {
		t.Fatal("Flush returned before its timeout with no processor")
	}

	stopped := NewMessageQueue("t2", "C1", "1.1", slackapi.NewInMemorySlackAPI(), false, testLogger())
	stopped.Start()
	stopped.Stop(time.Second)
	done := make(chan struct{})
	go func() { stopped.Flush(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Flush on a stopped queue did not return")
	}
}

func TestAppMention_ReplayWaitsForAnOriginalBeforeItsSubmit(t *testing.T) {
	a, _, _ := newHandlerTestAdapter(t)
	ev := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", Text: "<@UBOT> hi", TimeStamp: "1700000001.1"}
	if claimed, _ := a.claimMention(mentionKey(ev.Channel, ev.TimeStamp)); !claimed {
		t.Fatal("the original did not reserve the slot")
	}

	if settled := a.handleAppMention(ev, "T1", nil, &inboundHold{key: "C1:1700000001.1", token: 7}); settled {
		t.Fatal("a replay settled a mention whose original is still before its submit")
	}
	if settled := a.handleAppMention(ev, "T1", nil, nil); !settled {
		t.Fatal("a duplicate delivery was not settled")
	}
}

func TestClaimAnswer_PressAfterTheTaskEndedIsNotAWarning(t *testing.T) {
	inst, _ := newSlackInstances(t, 1, SlackConfig{})
	items := inst[0].items
	var warned, debugged []string
	items.warn = func(msg string, _ ...any) { warned = append(warned, msg) }
	items.debug = func(msg string, _ ...any) { debugged = append(debugged, msg) }

	won, claim := items.claimAnswer(t.Context(), "11111111-1111-4111-8111-111111111111", itemHILAnsweredPrefix+"req-9")

	if !won || claim != "" {
		t.Errorf("claimAnswer = %v, %q; want won with no value, leaving the decision to the local guard", won, claim)
	}
	if len(warned) != 0 {
		t.Errorf("warned %q for a press after the task ended", warned)
	}
	if len(debugged) != 1 {
		t.Errorf("debug lines = %q, want the ended-task one", debugged)
	}
}
