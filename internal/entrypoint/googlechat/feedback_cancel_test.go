// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func pressOnCard(t *testing.T, a *Adapter, chat *fakeChat, message, sender, label string, n int) {
	t.Helper()
	function, params := buttonOnCards(t, chat.currentCards(message), label)
	email := requesterEmail
	if sender != requesterName {
		email = bystanderEmail
	}
	payload, err := json.Marshal(map[string]any{
		"type":    eventTypeCardClicked,
		"space":   map[string]any{"name": dmMeta().SpaceName, "type": legacySpaceTypeDM, "spaceType": spaceTypeDM, "singleUserBotDm": true},
		"user":    map[string]any{"name": sender, "email": email, "type": "HUMAN"},
		"message": map[string]any{"name": message, "thread": map[string]any{"name": dmMeta().ThreadName}},
		"common":  map[string]any{"invokedFunction": function, "parameters": params},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := parseEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	job := ingressJob{msg: pulledMessage{AckID: "press#" + strconv.Itoa(n), MessageID: "press-" + strconv.Itoa(n), Data: payload}, ev: ev}
	if got := runIngressJob(t, a, job); !got.acked {
		t.Fatalf("press %d of %q settled %+v, want ack", n, label, got)
	}
	a.bgWG.Wait()
}

func TestFeedbackThumbAfterCancelIsNotDroppedAsARepeat(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	message := postFeedbackCard(t, chat, taskID)

	pressOnCard(t, a, chat, message, requesterName, "\U0001F44D", 1)
	waitCardsContain(t, chat, message, "Rating: \U0001F44D")
	pressOnCard(t, a, chat, message, requesterName, cancelButtonLabel, 2)
	waitCardsContain(t, chat, message, feedbackPrompt)
	pressOnCard(t, a, chat, message, requesterName, "\U0001F44E", 3)
	waitCardsContain(t, chat, message, "Rating: \U0001F44E")
	if _, n := readFeedback(t, a, taskID); n != 0 {
		t.Fatalf("feedback rows = %d before Submit, want none", n)
	}
	pressOnCard(t, a, chat, message, requesterName, feedbackSubmitLabel, 4)
	waitCardsContain(t, chat, message, "Thank you for your feedback")

	var rating, session string
	if err := a.core.Store().QueryRowContext(t.Context(), `SELECT rating, session_id FROM feedback WHERE task_id = ?`, taskID).Scan(&rating, &session); err != nil {
		t.Fatal(err)
	}
	if rating != ratingDown || session != "sess-fb" {
		t.Errorf("stored rating = %q for session %q, want the second thumb for the task's session", rating, session)
	}
}

func TestFeedbackCancelTwiceAndAfterABystanderStillWork(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	message := postFeedbackCard(t, chat, taskID)

	pressOnCard(t, a, chat, message, requesterName, "\U0001F44D", 1)
	waitCardsContain(t, chat, message, "Rating: ")
	pressOnCard(t, a, chat, message, bystanderName, feedbackSubmitLabel, 2)
	pressOnCard(t, a, chat, message, requesterName, cancelButtonLabel, 3)
	waitCardsContain(t, chat, message, feedbackPrompt)
	pressOnCard(t, a, chat, message, requesterName, "\U0001F44D", 4)
	waitCardsContain(t, chat, message, "Rating: ")
	pressOnCard(t, a, chat, message, requesterName, cancelButtonLabel, 5)
	waitCardsContain(t, chat, message, feedbackPrompt)
}

func TestFeedbackThumbWhoseCardCannotBeShownIsRetried(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	message := postFeedbackCard(t, chat, taskID)
	thumb := feedbackClick(requesterName, actionFeedback, taskID, ratingUp, nil)
	thumb.MessageName = message
	chat.mu.Lock()
	chat.failCardAdd = errors.New("chat is down")
	chat.mu.Unlock()
	if res := a.process(t.Context(), thumb); res.outcome != outcomeTransient || res.decline != msgClickFailed {
		t.Fatalf("outcome = %+v, want transient with the try-again notice for when retries run out", res)
	}
	chat.mu.Lock()
	chat.failCardAdd = nil
	chat.mu.Unlock()
	if res := a.process(t.Context(), thumb); res.outcome != outcomeSubmitted {
		t.Fatalf("retried outcome = %v, want submitted", res.outcome)
	}
	waitCardsContain(t, chat, message, "Rating: ")
}

func TestFeedbackThumbOnAGoneMessageIsNotRetried(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	thumb := feedbackClick(requesterName, actionFeedback, taskID, ratingUp, nil)
	thumb.MessageName = postFeedbackCard(t, chat, taskID)
	chat.deleteMessage(thumb.MessageName)
	if res := a.process(t.Context(), thumb); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted for a message that is gone", res.outcome)
	}
}

func TestFeedbackClicksWithoutAPersonAreNotActedOn(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	message := postFeedbackCard(t, chat, taskID)

	thumb := feedbackClick("", actionFeedback, taskID, ratingUp, nil)
	thumb.MessageName = message
	if res := a.process(t.Context(), thumb); res.outcome != outcomeIgnored {
		t.Errorf("thumb without a sender outcome = %v, want ignored", res.outcome)
	}
	submit := feedbackClick("", actionFeedbackDetails, taskID, ratingUp, nil)
	submit.Action[paramRater] = ""
	submit.MessageName = message
	if res := a.process(t.Context(), submit); res.outcome != outcomeDeclined {
		t.Errorf("submit without a sender outcome = %v, want declined even though the card names no rater either", res.outcome)
	}
	a.bgWG.Wait()
	if _, n := readFeedback(t, a, taskID); n != 0 {
		t.Errorf("feedback rows = %d, want none", n)
	}
	writes, _ := chat.snapshot()
	for _, w := range writes {
		if w.op == "update" {
			t.Errorf("the card was changed by a click with no person: %+v", w)
		}
	}
}

func TestFeedbackMetricCountsASubmitWithAndWithoutAComment(t *testing.T) {
	for name, tc := range map[string]struct {
		inputs      map[string][]string
		wantDetails int64
	}{
		"with a comment":    {inputs: map[string][]string{feedbackCommentInput: {"Good."}}, wantDetails: 1},
		"without a comment": {inputs: nil, wantDetails: 0},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, _, taskID := feedbackReplica(t)
			metrics := &countingMetrics{}
			a.metrics = metrics
			submit := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, tc.inputs)
			if res := a.process(t.Context(), submit); res.outcome != outcomeSubmitted {
				t.Fatalf("outcome = %v, want submitted", res.outcome)
			}
			if got := metrics.count(metricFeedback, "gatewayId", a.gatewayID, "rating", ratingUp, "part", "rating"); got != 1 {
				t.Errorf("rating count = %d, want 1", got)
			}
			if got := metrics.count(metricFeedback, "gatewayId", a.gatewayID, "rating", ratingUp, "part", "details"); got != tc.wantDetails {
				t.Errorf("details count = %d, want %d", got, tc.wantDetails)
			}
		})
	}
}

func TestFeedbackCardWriteIsOneShortAttempt(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	capture := recordLogs(a)
	message := postFeedbackCard(t, chat, taskID)
	thumb := feedbackClick(requesterName, actionFeedback, taskID, ratingUp, nil)
	thumb.MessageName = message
	chat.mu.Lock()
	chat.failCardAdd = errors.New("chat is down")
	chat.mu.Unlock()

	if res := a.process(t.Context(), thumb); res.outcome != outcomeTransient || res.decline != msgClickFailed {
		t.Fatalf("outcome = %+v, want transient with the try-again notice for when retries run out", res)
	}
	if n := chat.updatesAttempted(message); n != 1 {
		t.Errorf("update attempts = %d, want one for one press", n)
	}
	if n := capture.count(slog.LevelError, "google chat write failed after retries"); n != 0 {
		t.Errorf("terminal write errors = %d, want none while the click is still retried", n)
	}
}

func TestFeedbackCardWriteEndsWithTheClicksContext(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	capture := recordLogs(a)
	thumb := feedbackClick(requesterName, actionFeedback, taskID, ratingUp, nil)
	thumb.MessageName = postFeedbackCard(t, chat, taskID)
	release := holdWrites(chat)
	t.Cleanup(sync.OnceFunc(func() { close(release) }))

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := a.process(ctx, thumb)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the press held its worker for %v after its context ended", took)
	}
	if res.outcome != outcomeTransient {
		t.Errorf("outcome = %v, want transient", res.outcome)
	}
	if n := capture.count(slog.LevelWarn, "google chat feedback card could not be shown"); n != 0 {
		t.Errorf("warnings = %d, want a context that ended logged below WARN", n)
	}
}

func TestFeedbackCardChatRefusesForGoodIsNotRetried(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	thumb := feedbackClick(requesterName, actionFeedback, taskID, ratingUp, nil)
	thumb.MessageName = postFeedbackCard(t, chat, taskID)
	chat.mu.Lock()
	chat.failCardAdd = &permanentError{err: errors.New("bad card")}
	chat.mu.Unlock()

	if res := a.process(t.Context(), thumb); res.outcome != outcomeDeclined {
		t.Fatalf("outcome = %v, want declined", res.outcome)
	}
	a.bgWG.Wait()
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgFeedbackUnavailable) })
}

func TestFeedbackSubmitWhoseThankYouCannotBeShownIsRetried(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	submit := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, map[string][]string{feedbackCommentInput: {"Good."}})
	submit.MessageName = postFeedbackCard(t, chat, taskID)
	chat.mu.Lock()
	chat.failCardAdd = errors.New("chat is down")
	chat.mu.Unlock()
	metrics := &countingMetrics{}
	a.metrics = metrics

	if res := a.process(t.Context(), submit); res.outcome != outcomeTransient || res.decline != msgFeedbackStoredCardFailed {
		t.Fatalf("outcome = %+v, want transient with the notice that the feedback was recorded", res)
	}
	if _, n := readFeedback(t, a, taskID); n != 1 {
		t.Fatalf("feedback rows = %d, want the rating stored before the thank-you", n)
	}
	chat.mu.Lock()
	chat.failCardAdd = nil
	chat.mu.Unlock()
	if res := a.process(t.Context(), submit); res.outcome != outcomeSubmitted {
		t.Fatalf("retried outcome = %v, want submitted", res.outcome)
	}
	waitCardsContain(t, chat, submit.MessageName, "Thank you for your feedback")
	if got := metrics.count(metricFeedback, "gatewayId", a.gatewayID, "rating", ratingUp, "part", "rating"); got != 1 {
		t.Errorf("rating count = %d after a retried Submit, want 1", got)
	}
}

func TestFeedbackClicksThatNameNoMessageAreIgnored(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	for _, function := range []string{actionFeedback, actionFeedbackCancel} {
		click := feedbackClick(requesterName, function, taskID, ratingUp, nil)
		click.MessageName = ""
		if res := a.process(t.Context(), click); res.outcome != outcomeIgnored {
			t.Errorf("%s outcome = %v, want ignored", function, res.outcome)
		}
	}
	a.bgWG.Wait()
	if writes, _ := chat.snapshot(); len(writes) != 0 {
		t.Errorf("writes = %+v, want none", writes)
	}
	if _, n := readFeedback(t, a, taskID); n != 0 {
		t.Errorf("feedback rows = %d, want none", n)
	}
}

func TestFeedbackThumbsHaveDifferentRequestIDs(t *testing.T) {
	cards := feedbackCard(tid("task-ids"), "sess")
	_, up := buttonOnCards(t, cards, "\U0001F44D")
	_, down := buttonOnCards(t, cards, "\U0001F44E")
	if up[paramRequestID] == "" || up[paramRequestID] == down[paramRequestID] {
		t.Errorf("request ids = %q and %q, want a different one for each thumb", up[paramRequestID], down[paramRequestID])
	}
	if strings.Contains(up[paramRequestID], "|") {
		t.Errorf("request id %q contains the dedup key's separator", up[paramRequestID])
	}
}

func TestFeedbackSubmitOnARecordedRatingSaysSoOnce(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	recordOlderCardRating(t, a, taskID)
	submit := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingDown, nil)
	submit.MessageName = postFeedbackCard(t, chat, taskID)
	chat.mu.Lock()
	chat.failCardAdd = errors.New("chat is down")
	chat.mu.Unlock()

	for attempt := 1; attempt <= 3; attempt++ {
		res := a.process(t.Context(), submit)
		if res.outcome != outcomeTransient || res.decline != entrypoint.FeedbackAlreadyRecordedNotice {
			t.Fatalf("attempt %d: outcome = %+v, want transient with the already-recorded notice for when retries run out", attempt, res)
		}
	}
	a.bgWG.Wait()
	if _, texts := chat.snapshot(); anyTextContains(texts, entrypoint.FeedbackAlreadyRecordedNotice) {
		t.Fatalf("messages = %q, want no notice while the card write is still being retried", texts)
	}

	chat.mu.Lock()
	chat.failCardAdd = nil
	chat.mu.Unlock()
	if res := a.process(t.Context(), submit); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted once the card is written", res.outcome)
	}
	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
		return anyTextContains(texts, entrypoint.FeedbackAlreadyRecordedNotice)
	})
	if n := strings.Count(strings.Join(texts, "\n"), entrypoint.FeedbackAlreadyRecordedNotice); n != 1 {
		t.Errorf("already-recorded notices = %d, want 1", n)
	}
}

func TestFeedbackSubmitStoredButThankYouRefusedForGood(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	submit := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, nil)
	submit.MessageName = postFeedbackCard(t, chat, taskID)
	chat.mu.Lock()
	chat.failCardAdd = &permanentError{err: errors.New("bad card")}
	chat.mu.Unlock()

	if res := a.process(t.Context(), submit); res.outcome != outcomeDeclined {
		t.Fatalf("outcome = %v, want declined", res.outcome)
	}
	a.bgWG.Wait()
	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgFeedbackStoredCardFailed) })
	if anyTextContains(texts, msgFeedbackUnavailable) {
		t.Errorf("messages = %q, want no claim that feedback cannot be given: it was stored", texts)
	}
	if _, n := readFeedback(t, a, taskID); n != 1 {
		t.Errorf("feedback rows = %d, want the rating stored", n)
	}
}

func TestFeedbackCardWriteHasADeadlineInsideTheLease(t *testing.T) {
	if feedbackCardWriteTimeout >= ackDeadline {
		t.Fatalf("feedbackCardWriteTimeout %v is not inside ackDeadline %v", feedbackCardWriteTimeout, ackDeadline)
	}
	a, chat, _, taskID := feedbackReplica(t)
	thumb := feedbackClick(requesterName, actionFeedback, taskID, ratingUp, nil)
	thumb.MessageName = postFeedbackCard(t, chat, taskID)
	var deadline time.Time
	var has bool
	chat.mu.Lock()
	chat.onWrite = func(ctx context.Context) error {
		deadline, has = ctx.Deadline()
		return nil
	}
	chat.mu.Unlock()

	start := time.Now()
	a.process(t.Context(), thumb)
	if !has || deadline.After(start.Add(ackDeadline)) {
		t.Errorf("write deadline = %v (set: %v), want one within %v of the press", deadline, has, ackDeadline)
	}
}

func TestFeedbackCardFailureNeverLogsAnUntrustedTaskID(t *testing.T) {
	a, chat, _, _ := feedbackReplica(t)
	logs := jsonLogsOf(a)
	thumb := feedbackClick(requesterName, actionFeedback, "not-a-uuid-card-param", ratingUp, nil)
	thumb.MessageName = postFeedbackCard(t, chat, "not-a-uuid-card-param")
	chat.mu.Lock()
	chat.failCardAdd = errors.New("chat is down")
	chat.mu.Unlock()
	a.process(t.Context(), thumb)

	out := logs.String()
	if !strings.Contains(out, "google chat feedback card could not be shown") || !strings.Contains(out, `"taskIDLen":`) {
		t.Fatalf("log = %s, want the failure logged by the id's length", out)
	}
	if strings.Contains(out, "not-a-uuid-card-param") {
		t.Errorf("the card's task id reached the logs:\n%s", out)
	}
}
