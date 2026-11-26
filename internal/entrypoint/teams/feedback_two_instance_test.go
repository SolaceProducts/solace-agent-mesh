// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	feedbackSubmitID = "1792767300200"
	ratedTaskID      = "a1a1a1a1-0000-4000-8000-000000000001"
	ratedSessionID   = "b2b2b2b2-0000-4000-8000-000000000002"
	ratedAnswerID    = "answer-1"
	ratedComment     = "clear and correct"
)

func (inst *teamsInstances) ratedAnswer(t *testing.T) {
	t.Helper()
	sender := inst.adapters[0]
	seedTeamsTask(t, sender, ratedTaskID, twoInstanceAgent)
	sender.registerFeedback(ratedTaskID, &TeamsTaskMeta{
		SessionID: ratedSessionID, ConversationID: twoInstanceConversation,
	}, ratedAnswerID, "the answer")
}

func feedbackSubmit(t *testing.T, rating string) map[string]any {
	t.Helper()
	activity := dmActivity(feedbackSubmitID)
	delete(activity, "text")
	activity["type"] = ActivityTypeInvoke
	activity["name"] = InvokeMessageSubmitAction
	activity["replyToId"] = ratedAnswerID
	inner, err := json.Marshal(map[string]any{"rating": rating, "comment": ratedComment})
	if err != nil {
		t.Fatal(err)
	}
	activity["value"] = map[string]any{
		"actionName":  "feedback",
		"actionValue": map[string]any{"reaction": "like", "feedback": string(inner)},
	}
	return activity
}

func submitKey() string { return inboundMessageKey(twoInstanceConversation, feedbackSubmitID) }

func (inst *teamsInstances) ratings(t *testing.T) []string {
	t.Helper()
	rows, err := store.NewFeedbackRepo(inst.adapters[len(inst.adapters)-1].core.Store()).Search(t.Context(), "*", 0, 0, ratedTaskID, "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ratings []string
	for _, row := range rows {
		ratings = append(ratings, row.Rating)
	}
	return ratings
}

func (inst *teamsInstances) lapsedSubmit(t *testing.T, a *TeamsAdapter) entrypoint.InboundMessage {
	t.Helper()
	lapsed, err := store.NewEntrypointInboundRepo(a.core.Store()).TakeLapsed(t.Context(), "test-gw", 0, 10)
	if err != nil || len(lapsed) != 1 {
		t.Fatalf("TakeLapsed = %+v, %v; want the submit", lapsed, err)
	}
	return entrypoint.InboundMessage{
		ID: lapsed[0].MessageID, Payload: lapsed[0].Payload, Token: lapsed[0].Token, Attempts: lapsed[0].Attempts,
	}
}

func inboundPayload(t *testing.T, a *TeamsAdapter, key string) string {
	t.Helper()
	var payload string
	err := a.core.Store().QueryRowContext(t.Context(),
		`SELECT payload FROM entrypoint_inbound_messages WHERE gateway_id = 'test-gw' AND message_id = ?`, key).Scan(&payload)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestTwoInstances_FeedbackSubmitFinishedByAnotherInstanceAfterTheLease(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	first, survivor := inst.adapters[0], inst.adapters[1]
	inst.ratedAnswer(t)
	first.emailCache = NewEmailCache("tenant", 0, first.botClient, first.logger)
	arrived, release := inst.bot.hold(t, http.MethodGet)

	if code := click(t, first, feedbackSubmit(t, "up")); code != http.StatusOK {
		t.Fatalf("the submit was answered %d, want 200", code)
	}
	<-arrived
	if state := inboundRow(t, first, submitKey()); state != store.InboundProcessing {
		t.Fatalf("record state = %q right after the answer, want processing", state)
	}
	if got := inst.ratings(t); len(got) != 0 {
		t.Fatalf("ratings = %q while the write is held, want none", got)
	}

	survivor.ReplayInbound(t.Context(), inst.lapsedSubmit(t, survivor))

	if got := inst.ratings(t); !slices.Equal(got, []string{"up"}) {
		t.Fatalf("ratings = %q after the replay, want one up", got)
	}
	if state := inboundRow(t, survivor, submitKey()); state != store.InboundDone {
		t.Errorf("record state = %q, want done", state)
	}
	if payload := inboundPayload(t, survivor, submitKey()); payload != "" {
		t.Errorf("the record still holds the submit: %q", payload)
	}
	if n := inst.bot.edits(ratedAnswerID); n != 0 {
		t.Errorf("the answer was edited %d times by the survivor, want 0", n)
	}

	release()
	first.server.Wait()
	if got := inst.ratings(t); !slices.Equal(got, []string{"up"}) {
		t.Errorf("ratings = %q after the first instance's late write, want still one up", got)
	}
	if posted := inst.bot.posted(); len(posted) != 0 {
		t.Errorf("posted %q, want no notice", posted)
	}
}

func TestTwoInstances_FeedbackReplayedAfterItsWriteLandedChangesNothing(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	first, survivor := inst.adapters[0], inst.adapters[1]
	inst.ratedAnswer(t)
	arrived, release := inst.bot.hold(t, http.MethodPut)

	if code := click(t, first, feedbackSubmit(t, "down")); code != http.StatusOK {
		t.Fatalf("the submit was answered %d, want 200", code)
	}
	<-arrived
	if got := inst.ratings(t); !slices.Equal(got, []string{"down"}) {
		t.Fatalf("ratings = %q before the replay, want one down", got)
	}

	survivor.ReplayInbound(t.Context(), inst.lapsedSubmit(t, survivor))

	if got := inst.ratings(t); !slices.Equal(got, []string{"down"}) {
		t.Errorf("ratings = %q after the replay, want still one down", got)
	}
	if state := inboundRow(t, survivor, submitKey()); state != store.InboundDone {
		t.Errorf("record state = %q, want done", state)
	}
	if posted := inst.bot.posted(); len(posted) != 0 {
		t.Errorf("posted %q, want no notice", posted)
	}

	release()
	first.server.Wait()
	if n := inst.bot.edits(ratedAnswerID); n != 1 {
		t.Errorf("the answer was edited %d times, want once", n)
	}
	if posted := inst.bot.posted(); len(posted) != 0 {
		t.Errorf("posted %q after the late settle, want no notice", posted)
	}
}

func TestTwoInstances_CopyOfAFeedbackSubmitIsDropped(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	first, other := inst.adapters[0], inst.adapters[1]
	inst.ratedAnswer(t)
	first.emailCache = NewEmailCache("tenant", 0, first.botClient, first.logger)
	arrived, release := inst.bot.hold(t, http.MethodGet)

	if code := click(t, first, feedbackSubmit(t, "up")); code != http.StatusOK {
		t.Fatalf("the submit was answered %d, want 200", code)
	}
	<-arrived
	if code := deliver(t, other, feedbackSubmit(t, "up")); code != http.StatusOK {
		t.Fatalf("the copy was answered %d, want 200", code)
	}
	if got := inst.ratings(t); len(got) != 0 {
		t.Fatalf("ratings = %q after the copy, want none until the holder writes", got)
	}

	release()
	first.server.Wait()
	if got := inst.ratings(t); !slices.Equal(got, []string{"up"}) {
		t.Errorf("ratings = %q, want one up", got)
	}
	if state := inboundRow(t, first, submitKey()); state != store.InboundDone {
		t.Errorf("record state = %q, want done", state)
	}
}

func TestTwoInstances_FeedbackWriteCutShortByItsTimeoutIsLeftForAnotherInstance(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	first, survivor := inst.adapters[0], inst.adapters[1]
	first.feedbackWorkTimeout = 500 * time.Millisecond
	inst.ratedAnswer(t)
	first.emailCache = NewEmailCache("tenant", 0, first.botClient, first.logger)
	arrived, _ := inst.bot.hold(t, http.MethodGet)

	if code := click(t, first, feedbackSubmit(t, "up")); code != http.StatusOK {
		t.Fatalf("the submit was answered %d, want 200", code)
	}
	<-arrived
	first.server.Wait()

	if state := inboundRow(t, first, submitKey()); state != store.InboundProcessing {
		t.Fatalf("record state = %q after the timeout, want still processing", state)
	}
	if got := inst.ratings(t); len(got) != 0 {
		t.Fatalf("ratings = %q after the timeout, want none", got)
	}
	if posted := inst.bot.posted(); len(posted) != 0 {
		t.Fatalf("posted %q, want nothing: the record is not given up", posted)
	}

	survivor.ReplayInbound(t.Context(), inst.lapsedSubmit(t, survivor))

	if got := inst.ratings(t); !slices.Equal(got, []string{"up"}) {
		t.Errorf("ratings = %q after the replay, want one up", got)
	}
	if state := inboundRow(t, survivor, submitKey()); state != store.InboundDone {
		t.Errorf("record state = %q, want done", state)
	}
}

func TestAbandonInbound_FeedbackSubmitTellsTheUserItWasNotRecorded(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]

	if !a.AbandonInbound(t.Context(), entrypoint.InboundMessage{ID: submitKey(), Payload: marshalActivity(t, feedbackSubmit(t, "up"))}) {
		t.Fatal("the notice did not reach Teams")
	}

	waitUntil(t, "the notice is posted", func() bool {
		return slices.Contains(inst.bot.posted(), entrypoint.FeedbackNotRecordedNotice)
	})
}

func TestTwoInstances_FeedbackSubmitNotRecordedIsRefused(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	broken, healthy := inst.adapters[0], inst.adapters[1]
	inst.ratedAnswer(t)
	_ = broken.core.Store().Close()

	if code := deliver(t, broken, feedbackSubmit(t, "up")); code != http.StatusServiceUnavailable {
		t.Fatalf("a submit that was not recorded was answered %d, want 503", code)
	}
	if got := inst.ratings(t); len(got) != 0 {
		t.Fatalf("ratings = %q after the refused submit, want none", got)
	}

	if code := deliver(t, healthy, feedbackSubmit(t, "up")); code != http.StatusOK {
		t.Fatalf("the retry was answered %d, want 200", code)
	}
	if got := inst.ratings(t); !slices.Equal(got, []string{"up"}) {
		t.Errorf("ratings = %q after the retry, want one up", got)
	}
}

func TestTwoInstances_FeedbackReplayWithTheStoreUnreadableIsLeftForAnotherAttempt(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	sender, other := inst.adapters[0], inst.adapters[1]
	inst.ratedAnswer(t)
	if _, err := other.core.AcceptInbound(t.Context(), submitKey(), marshalActivity(t, feedbackSubmit(t, "up"))); err != nil {
		t.Fatal(err)
	}
	msg := inst.lapsedSubmit(t, sender)
	_ = other.core.Store().Close()

	other.ReplayInbound(t.Context(), msg)

	if state := inboundRow(t, sender, submitKey()); state != store.InboundProcessing {
		t.Errorf("record state = %q, want still processing", state)
	}
	rows, err := store.NewFeedbackRepo(sender.core.Store()).Search(t.Context(), "*", 0, 0, ratedTaskID, "", "", 10, 0)
	if err != nil || len(rows) != 0 {
		t.Errorf("feedback rows = %d, %v; want none", len(rows), err)
	}
	if posted := inst.bot.posted(); len(posted) != 0 {
		t.Errorf("posted %q, want nothing: the submit is not given up", posted)
	}
}

func TestTwoInstances_FeedbackReplayWithTheStoreUnwritableIsLeftForAnotherAttempt(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	sender, other := inst.adapters[0], inst.adapters[1]
	inst.ratedAnswer(t)
	if _, err := sender.core.AcceptInbound(t.Context(), submitKey(), marshalActivity(t, feedbackSubmit(t, "up"))); err != nil {
		t.Fatal(err)
	}
	msg := inst.lapsedSubmit(t, other)
	_ = sender.core.Store().Close()

	sender.ReplayInbound(t.Context(), msg)

	if state := inboundRow(t, other, submitKey()); state != store.InboundProcessing {
		t.Errorf("record state = %q, want still processing", state)
	}
	if got := inst.ratings(t); len(got) != 0 {
		t.Errorf("ratings = %q, want none", got)
	}
	if posted := inst.bot.posted(); len(posted) != 0 {
		t.Errorf("posted %q, want nothing: the submit is not given up", posted)
	}
}
