// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const twoInstanceActivityID = "1792767298349"

func dmKey() string { return inboundMessageKey(twoInstanceConversation, twoInstanceActivityID) }

func TestInbound_CopyOnOtherInstanceStartsNoTask(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	first, second := inst.adapters[0], inst.adapters[1]

	if code := deliver(t, first, dmActivity(twoInstanceActivityID)); code != http.StatusOK {
		t.Fatalf("first delivery answered %d, want 200", code)
	}
	if code := deliver(t, second, dmActivity(twoInstanceActivityID)); code != http.StatusOK {
		t.Fatalf("the copy was answered %d, want 200", code)
	}
	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published = %d, want 1", n)
	}
	if state := inboundRow(t, first, dmKey()); state != store.InboundDone {
		t.Fatalf("record state = %q, want done", state)
	}
}

func TestInbound_NotRecordedIsRefused(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	broken, healthy := inst.adapters[0], inst.adapters[1]
	_ = broken.core.Store().Close()

	if code := deliver(t, broken, dmActivity(twoInstanceActivityID)); code != http.StatusServiceUnavailable {
		t.Fatalf("a message that was not recorded was answered %d, want 503", code)
	}
	if n := inst.published(t); n != 0 {
		t.Fatalf("a message that was not recorded published %d tasks", n)
	}
	if code := deliver(t, healthy, dmActivity(twoInstanceActivityID)); code != http.StatusOK {
		t.Fatalf("the retry was answered %d, want 200", code)
	}
	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published by the retry = %d, want 1", n)
	}
}

func TestInbound_ReplayedOnOtherInstance(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	dead, survivor := inst.adapters[0], inst.adapters[1]
	if _, err := dead.core.AcceptInbound(t.Context(), dmKey(), marshalActivity(t, dmActivity(twoInstanceActivityID))); err != nil {
		t.Fatal(err)
	}

	lapsed, err := store.NewEntrypointInboundRepo(survivor.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10)
	if err != nil || len(lapsed) != 1 {
		t.Fatalf("TakeLapsed = %+v, %v", lapsed, err)
	}
	survivor.ReplayInbound(t.Context(), entrypoint.InboundMessage{
		ID: lapsed[0].MessageID, Payload: lapsed[0].Payload, Token: lapsed[0].Token, Attempts: lapsed[0].Attempts,
	})

	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published by the replay = %d, want 1", n)
	}
	if got := inst.bot.posted(); !slices.Contains(got, twoInstanceStatus) {
		t.Fatalf("posted = %q, want the status message of the replayed task", got)
	}
	if state := inboundRow(t, survivor, dmKey()); state != store.InboundDone {
		t.Fatalf("record state = %q, want done", state)
	}
	if code := deliver(t, survivor, dmActivity(twoInstanceActivityID)); code != http.StatusOK {
		t.Fatalf("a late copy was answered %d, want 200", code)
	}
	if n := inst.published(t); n != 0 {
		t.Fatalf("a late copy published %d tasks", n)
	}
}

func TestInbound_OnlyTaskMessagesRecorded(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]

	unmentioned := dmActivity("1792767298350")
	unmentioned["conversation"] = map[string]any{"id": "19:group@thread.v2", "conversationType": ConversationTypeGroupChat}
	click := dmActivity("1792767298351")
	delete(click, "text")
	click["value"] = map[string]any{"action": "unknown"}
	fromBot := dmActivity("1792767298352")
	fromBot["from"] = map[string]any{"id": twoInstanceAppID}
	update := dmActivity("1792767298353")
	update["type"] = ActivityTypeConversationUpdate
	noID := dmActivity("")
	delete(noID, "id")

	for name, activity := range map[string]map[string]any{
		"group message without a mention": unmentioned,
		"card click":                      click,
		"bot's own message":               fromBot,
		"conversation update":             update,
		"message without an id":           noID,
	} {
		if code := deliver(t, a, activity); code != http.StatusOK {
			t.Errorf("%s answered %d, want 200", name, code)
		}
		conversation, _ := activity["conversation"].(map[string]any)
		id, _ := activity["id"].(string)
		if state := inboundRow(t, a, inboundMessageKey(conversation["id"].(string), id)); state != "" {
			t.Errorf("%s recorded as %q", name, state)
		}
	}
	if n := inst.published(t); n != 0 {
		t.Fatalf("tasks published = %d, want none", n)
	}
	var rows int
	if err := a.core.Store().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM entrypoint_inbound_messages`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("inbound records = %d, %v; want none", rows, err)
	}
}

func TestInbound_AbandonedMessageTellsTheUser(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	if !a.AbandonInbound(t.Context(), entrypoint.InboundMessage{ID: dmKey(), Payload: marshalActivity(t, dmActivity(twoInstanceActivityID))}) {
		t.Fatal("the apology was not reported as posted")
	}
	if got := inst.bot.posted(); !slices.Contains(got, msgInboundAbandoned) {
		t.Fatalf("posted = %q, want the apology", got)
	}
	if !a.AbandonInbound(t.Context(), entrypoint.InboundMessage{ID: "x", Payload: "not json"}) {
		t.Fatal("an unreadable record was not settled")
	}
	a.ReplayInbound(t.Context(), entrypoint.InboundMessage{ID: "x", Payload: "not json"})
}

func TestInbound_UnsettledMessageKeepsItsRecord(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	activity := dmActivity(twoInstanceActivityID)
	claim, err := a.core.AcceptInbound(t.Context(), dmKey(), marshalActivity(t, activity))
	if err != nil {
		t.Fatal(err)
	}
	a.cancel()

	replayed, err := parseInbound(marshalActivity(t, activity))
	if err != nil {
		t.Fatal(err)
	}
	text, files, _ := a.taskMessage(replayed)
	hold := &inboundHold{key: dmKey(), token: claim.Token}
	a.settleInbound(hold, a.submitTeamsTask(t.Context(), replayed, text, files, hold))

	if state := inboundRow(t, a, dmKey()); state != store.InboundProcessing {
		t.Fatalf("record state = %q, want processing for another instance to finish", state)
	}
	if n := inst.published(t); n != 0 {
		t.Fatalf("a stopping instance published %d tasks", n)
	}
}

func TestInbound_TakenOverBeforeSubmitIsNotSubmitted(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	activity := dmActivity(twoInstanceActivityID)
	claim, err := a.core.AcceptInbound(t.Context(), dmKey(), marshalActivity(t, activity))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewEntrypointInboundRepo(a.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10); err != nil {
		t.Fatal(err)
	}

	replayed, _ := parseInbound(marshalActivity(t, activity))
	text, files, _ := a.taskMessage(replayed)
	hold := &inboundHold{key: dmKey(), token: claim.Token}
	if a.submitTeamsTask(t.Context(), replayed, text, files, hold) {
		t.Fatal("a message this instance no longer holds was settled here")
	}
	if n := inst.published(t); n != 0 {
		t.Fatalf("a message taken over by another instance was published here %d times", n)
	}
}

func TestInboundMessageKey_DigestsTheConversation(t *testing.T) {
	key := inboundMessageKey(twoInstanceConversation, twoInstanceActivityID)
	if strings.Contains(key, twoInstanceConversation) {
		t.Fatalf("key %q carries the conversation id", key)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}:` + twoInstanceActivityID + `$`).MatchString(key) {
		t.Fatalf("key %q, want 16 hex digits, a colon and the activity id", key)
	}
	if other := inboundMessageKey("a:1another-chat", twoInstanceActivityID); other == key {
		t.Fatal("the same activity id in two conversations gave one key")
	}
	if again := inboundMessageKey(twoInstanceConversation, twoInstanceActivityID); again != key {
		t.Fatal("the key is not stable")
	}
}

func TestInbound_StopBeforeTheSubmitLeavesTheMessageRecorded(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	stopping, survivor := inst.adapters[0], inst.adapters[1]
	stopping.emailCache = NewEmailCache("tenant", 0, stopping.botClient, stopping.logger)
	arrived, release := inst.bot.hold(t, http.MethodGet)

	if code := click(t, stopping, dmActivity(twoInstanceActivityID)); code != http.StatusOK {
		t.Fatalf("delivery answered %d, want 200", code)
	}
	<-arrived
	if state := inboundRow(t, survivor, dmKey()); state != store.InboundProcessing {
		t.Fatalf("record state = %q after the acknowledge, want processing", state)
	}
	if err := stopping.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	release()

	if state := inboundRow(t, survivor, dmKey()); state != store.InboundProcessing {
		t.Fatalf("record state = %q after the stop, want still processing", state)
	}
	if n := inst.published(t); n != 0 {
		t.Fatalf("a stopping instance published %d tasks", n)
	}

	lapsed, err := store.NewEntrypointInboundRepo(survivor.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10)
	if err != nil || len(lapsed) != 1 {
		t.Fatalf("TakeLapsed = %+v, %v; want the message", lapsed, err)
	}
	survivor.ReplayInbound(t.Context(), entrypoint.InboundMessage{
		ID: lapsed[0].MessageID, Payload: lapsed[0].Payload, Token: lapsed[0].Token, Attempts: lapsed[0].Attempts,
	})
	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published by the replay = %d, want 1", n)
	}
	if state := inboundRow(t, survivor, dmKey()); state != store.InboundDone {
		t.Fatalf("record state = %q after the replay, want done", state)
	}
}

func TestInbound_StopDuringTheStatusPostPublishesOnceOnTheDevBroker(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	stopping, other := inst.adapters[0], inst.adapters[1]
	arrived, release := inst.bot.hold(t, http.MethodPost)

	if code := click(t, stopping, dmActivity(twoInstanceActivityID)); code != http.StatusOK {
		t.Fatalf("delivery answered %d, want 200", code)
	}
	<-arrived
	if err := stopping.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	release()

	if n := inst.published(t); n != 1 {
		t.Fatalf("tasks published = %d, want 1", n)
	}
	if state := inboundRow(t, other, dmKey()); state != store.InboundDone {
		t.Fatalf("record state = %q, want done: the task was published", state)
	}
	if lapsed, err := store.NewEntrypointInboundRepo(other.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10); err != nil || len(lapsed) != 0 {
		t.Fatalf("TakeLapsed = %+v, %v; want nothing left for another instance", lapsed, err)
	}
}
