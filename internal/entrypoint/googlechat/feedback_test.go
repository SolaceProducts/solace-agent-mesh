// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"bytes"
	"database/sql"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func feedbackReplica(t *testing.T) (*Adapter, *fakeChat, *syncBuffer, string) {
	t.Helper()
	chat := newFakeChat()
	a := newClickRig(t).replicaWithTaskLog(t, chat)
	logs := &syncBuffer{}
	a.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a.writer.logger = a.logger
	taskID := tid("task-fb")
	ended := time.Now().UnixMilli()
	agent, handlerType, session := dmMeta().AgentName, store.TaskHandlerTypeAgent, "sess-fb"
	if err := store.NewTaskRepo(a.core.Store()).Create(t.Context(), &store.Task{
		ID: taskID, UserID: requesterEmail, StartTime: ended - 1000, EndTime: &ended, ExecutionMode: "foreground",
		SessionID: &session, HandlerName: &agent, HandlerType: &handlerType,
	}); err != nil {
		t.Fatalf("record the finished task: %v", err)
	}
	return a, chat, logs, taskID
}

func feedbackClick(sender, function, taskID, rating string, inputs map[string][]string) *inboundEvent {
	action := map[string]string{actionMethodParam: function, paramTaskID: taskID, paramSessionID: "sess-fb", paramRating: rating}
	if function == actionFeedbackDetails || function == actionFeedbackCancel {
		action[paramRater] = requesterName
	}
	ev := clickBy(dmMeta(), sender, dmMeta().SpaceName+"/messages/fb", action)
	ev.FormInputs = inputs
	return ev
}

type feedbackRow struct {
	comment sql.NullString
	share   int
	details sql.NullInt64
}

func readFeedback(t *testing.T, a *Adapter, taskID string) (feedbackRow, int) {
	t.Helper()
	var n int
	if err := a.core.Store().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM feedback WHERE task_id = ?`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	var row feedbackRow
	if n > 0 {
		if err := a.core.Store().QueryRowContext(t.Context(),
			`SELECT comment, share_content, details_submitted_time FROM feedback WHERE task_id = ?`, taskID).
			Scan(&row.comment, &row.share, &row.details); err != nil {
			t.Fatal(err)
		}
	}
	return row, n
}

func TestFeedbackCommentIsStoredButNeverLogged(t *testing.T) {
	const marker = "zebra-comment-canary-7731"
	a, _, logs, taskID := feedbackReplica(t)
	details := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, map[string][]string{feedbackCommentInput: {marker}})
	if res := a.process(t.Context(), details); res.outcome != outcomeSubmitted {
		t.Fatalf("details outcome = %v, want submitted", res.outcome)
	}
	a.bgWG.Wait()

	if row, _ := readFeedback(t, a, taskID); row.comment.String != marker || row.share != 0 || !row.details.Valid {
		t.Errorf("feedback row = %+v, want the comment stored with no consent", row)
	}
	out := logs.String()
	if !strings.Contains(out, "google chat feedback details recorded") {
		t.Fatal("the details were not logged, so the canary proves nothing")
	}
	if strings.Contains(out, marker) {
		t.Errorf("the feedback comment reached the logs:\n%s", out)
	}
}

func TestFeedbackSubmitFromAnotherClickerIsDeclined(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	details := feedbackClick(bystanderName, actionFeedbackDetails, taskID, ratingUp, map[string][]string{feedbackCommentInput: {"not mine"}})
	if res := a.process(t.Context(), details); res.outcome != outcomeDeclined {
		t.Fatalf("bystander submit outcome = %v, want declined", res.outcome)
	}
	a.bgWG.Wait()
	if _, n := readFeedback(t, a, taskID); n != 0 {
		t.Errorf("feedback rows = %d, want none from a bystander", n)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgNotYourFeedback) })
}

func TestFeedbackThumbRecordsNothingUntilSubmitted(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	thumb := feedbackClick(requesterName, actionFeedback, taskID, ratingDown, nil)
	thumb.MessageName = postFeedbackCard(t, chat, taskID)
	if res := a.process(t.Context(), thumb); res.outcome != outcomeSubmitted {
		t.Fatalf("thumb outcome = %v, want submitted", res.outcome)
	}
	a.bgWG.Wait()
	if _, n := readFeedback(t, a, taskID); n != 0 {
		t.Fatalf("feedback rows = %d after a thumb, want none until it is submitted", n)
	}
	last := lastReplacement(t, chat)
	if !last.replacedCards || last.cards != 1 || last.cardButtons != 2 || !strings.Contains(last.cardText, entrypoint.FeedbackDataUseNotice) || !strings.Contains(last.cardText, thumbFor(ratingDown)) {
		t.Errorf("the thumb was answered with %+v, want the details card with Submit and Cancel and the data-use notice", last)
	}

	submit := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingDown, nil)
	if res := a.process(t.Context(), submit); res.outcome != outcomeSubmitted {
		t.Fatalf("submit outcome = %v, want submitted", res.outcome)
	}
	if _, n := readFeedback(t, a, taskID); n != 1 {
		t.Errorf("feedback rows = %d after Submit, want the rating stored", n)
	}
}

func TestFeedbackCancelRecordsNothingAndRestoresTheThumbs(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	cancel := feedbackClick(requesterName, actionFeedbackCancel, taskID, ratingUp, nil)
	cancel.MessageName = postFeedbackCard(t, chat, taskID)
	if res := a.process(t.Context(), cancel); res.outcome != outcomeSubmitted {
		t.Fatalf("cancel outcome = %v, want submitted", res.outcome)
	}
	a.bgWG.Wait()
	if _, n := readFeedback(t, a, taskID); n != 0 {
		t.Errorf("feedback rows = %d after Cancel, want none", n)
	}
	last := lastReplacement(t, chat)
	if !last.replacedCards || last.cards != 1 || last.cardButtons != 2 || !strings.Contains(last.cardText, feedbackPrompt) {
		t.Errorf("Cancel was answered with %+v, want the thumbs card back", last)
	}
}

func TestFeedbackCancelFromAnotherClickerIsDeclined(t *testing.T) {
	a, chat, _, taskID := feedbackReplica(t)
	cancel := feedbackClick(bystanderName, actionFeedbackCancel, taskID, ratingUp, nil)
	cancel.MessageName = postFeedbackCard(t, chat, taskID)
	if res := a.process(t.Context(), cancel); res.outcome != outcomeDeclined {
		t.Fatalf("bystander cancel outcome = %v, want declined", res.outcome)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgNotYourFeedback) })
	a.bgWG.Wait()
	writes, _ := chat.snapshot()
	for _, w := range writes {
		if w.op == "update" && w.replacedCards {
			t.Errorf("the card was replaced by a bystander's Cancel: %+v", w)
		}
	}
}

func TestFeedbackSubmitThatFailsToSaveIsRetried(t *testing.T) {
	const marker = "zebra-retry-canary-4410"
	for name, comment := range map[string]string{"with a comment": marker, "without a comment": ""} {
		t.Run(name, func(t *testing.T) {
			a, chat, logs, taskID := feedbackReplica(t)
			writesBefore, _ := chat.snapshot()
			db := a.core.Store()
			if _, err := db.ExecContext(t.Context(), `ALTER TABLE feedback RENAME TO feedback_hidden`); err != nil {
				t.Fatal(err)
			}
			var inputs map[string][]string
			if comment != "" {
				inputs = map[string][]string{feedbackCommentInput: {comment}}
			}
			submit := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingDown, inputs)
			res := a.process(t.Context(), submit)
			if res.outcome != outcomeTransient || res.decline != msgFeedbackNotRecorded {
				t.Fatalf("submit outcome = %+v, want transient with the not-recorded notice for when retries run out", res)
			}
			a.bgWG.Wait()
			if writes, _ := chat.snapshot(); len(writes) != len(writesBefore) {
				t.Errorf("writes = %+v, want the form left in place for the retry", writes[len(writesBefore):])
			}

			if _, err := db.ExecContext(t.Context(), `ALTER TABLE feedback_hidden RENAME TO feedback`); err != nil {
				t.Fatal(err)
			}
			if res := a.process(t.Context(), submit); res.outcome != outcomeSubmitted {
				t.Fatalf("redelivered submit outcome = %v, want submitted", res.outcome)
			}
			row, n := readFeedback(t, a, taskID)
			if n != 1 || row.comment.String != comment || row.share != 0 {
				t.Errorf("feedback rows = %d, row = %+v, want the rating stored with %q and no consent", n, row, comment)
			}
			out := logs.String()
			if !strings.Contains(out, "google chat feedback could not be saved; the click is retried") {
				t.Fatal("the failed write was not logged, so the canary proves nothing")
			}
			if strings.Contains(out, marker) {
				t.Errorf("the feedback comment reached the logs:\n%s", out)
			}
		})
	}
}

func TestNoDatabaseStillAcknowledgesTheRating(t *testing.T) {
	a := NewAdapter(nil)
	a.chatOverride = newFakeChat()
	newEventCore(t, a, nil)
	logs := &logCapture{}
	a.logger = slog.New(logs)
	kept, err := a.recordFeedback(t.Context(), requesterEmail, store.FeedbackSubmission{TaskID: tid("task-n"), SessionID: "s", Rating: ratingUp})
	if err != nil || kept.rating != ratingUp {
		t.Errorf("recordFeedback without a database = %+v, %v; want the rating acknowledged", kept, err)
	}
	if logs.count(slog.LevelWarn, "google chat feedback dropped: the gateway has no database") != 1 {
		t.Error("the rating dropped for lack of a database was not logged")
	}
}

func TestFeedbackWhoseTaskLookupFailsIsRetried(t *testing.T) {
	a, _, _, taskID := feedbackReplica(t)
	db := a.core.Store()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE tasks RENAME TO tasks_hidden`); err != nil {
		t.Fatal(err)
	}
	details := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, map[string][]string{feedbackCommentInput: {"Kept."}})
	if res := a.process(t.Context(), details); res.outcome != outcomeTransient {
		t.Fatalf("details outcome = %v, want transient while the task log cannot be read", res.outcome)
	}
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE tasks_hidden RENAME TO tasks`); err != nil {
		t.Fatal(err)
	}
	if res := a.process(t.Context(), details); res.outcome != outcomeSubmitted {
		t.Fatalf("redelivered details outcome = %v, want submitted", res.outcome)
	}
	if row, _ := readFeedback(t, a, taskID); row.comment.String != "Kept." {
		t.Errorf("feedback row = %+v, want the comment stored", row)
	}
}

func TestFeedbackOnAnUnattributableTaskIsAcknowledgedNotRetried(t *testing.T) {
	for name, logged := range map[string]bool{"not in the task log": false, "unclassified": true} {
		t.Run(name, func(t *testing.T) {
			a, _, logs, _ := feedbackReplica(t)
			taskID := tid("task-unattributable")
			if logged {
				ended := time.Now().UnixMilli()
				if err := store.NewTaskRepo(a.core.Store()).Create(t.Context(), &store.Task{
					ID: taskID, UserID: requesterEmail, StartTime: ended - 1000, EndTime: &ended, ExecutionMode: "foreground",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if res := a.process(t.Context(), feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, nil)); res.outcome != outcomeSubmitted {
				t.Fatalf("outcome = %v, want submitted", res.outcome)
			}
			if _, n := readFeedback(t, a, taskID); n != 0 {
				t.Errorf("feedback rows = %d, want none", n)
			}
			if !strings.Contains(logs.String(), `level=ERROR msg="google chat feedback dropped: the task could not be attributed"`) {
				t.Errorf("the dropped rating was not logged at ERROR:\n%s", logs.String())
			}
		})
	}
}

func TestFeedbackWithAMalformedTaskIDNeverReadsTheTaskLog(t *testing.T) {
	a, _, logs, _ := feedbackReplica(t)
	if _, err := a.core.Store().ExecContext(t.Context(), `ALTER TABLE tasks RENAME TO tasks_hidden`); err != nil {
		t.Fatal(err)
	}
	if res := a.process(t.Context(), feedbackClick(requesterName, actionFeedbackDetails, "not-a-uuid", ratingUp, nil)); res.outcome != outcomeSubmitted {
		t.Fatalf("outcome = %v, want submitted without reading the task log", res.outcome)
	}
	out := logs.String()
	if !strings.Contains(out, `level=WARN msg="google chat feedback dropped: the card names a task id this gateway did not mint" taskIDLen=10`) {
		t.Errorf("the malformed id was not logged at WARN by its length:\n%s", out)
	}
	if strings.Contains(out, "not-a-uuid") {
		t.Errorf("the card's task id reached the logs:\n%s", out)
	}
}

func TestClickRetryBudgetIsShort(t *testing.T) {
	if maxClickAttempts < 2 {
		t.Fatalf("maxClickAttempts = %d, want at least one retry", maxClickAttempts)
	}
	var wait time.Duration
	for attempt := 1; attempt < maxClickAttempts; attempt++ {
		wait += retryPace(attempt)
	}
	if wait > 3*time.Minute {
		t.Errorf("a failed click is retried for %v before the user hears back, want at most 3m", wait)
	}
}

func TestFeedbackGiveUpLetsTheUserClickAgain(t *testing.T) {
	a, chat, logs, taskID := feedbackReplica(t)
	db := a.core.Store()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE feedback RENAME TO feedback_hidden`); err != nil {
		t.Fatal(err)
	}
	ev := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingDown, nil)
	ev.Action[paramRequestID] = "req-submit"
	ev.EventKey = "click|" + ev.MessageName + "|" + actionFeedbackDetails + "|req-submit|" + requesterName
	job := func(n int) ingressJob {
		return ingressJob{msg: pulledMessage{AckID: "fb#" + strconv.Itoa(n), MessageID: "fb"}, ev: *ev}
	}
	for attempt := 1; attempt < maxClickAttempts; attempt++ {
		if got := runIngressJob(t, a, job(attempt)); !got.nacked {
			t.Fatalf("attempt %d settled %+v, want held for retry", attempt, got)
		}
	}
	if got := runIngressJob(t, a, job(maxClickAttempts)); !got.acked {
		t.Fatalf("last attempt settled %+v, want given up", got)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, msgFeedbackNotRecorded) })
	if !strings.Contains(logs.String(), `level=ERROR msg="google chat card click given up after retries; the user is asked to try again"`) {
		t.Error("the give-up was not logged at ERROR")
	}
	if got := runIngressJob(t, a, job(maxClickAttempts+1)); !got.acked {
		t.Fatalf("a click while the store still fails settled %+v, want answered at once rather than held again", got)
	}
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
		return strings.Count(strings.Join(texts, "\n"), msgFeedbackNotRecorded) == 2
	})

	if _, err := db.ExecContext(t.Context(), `ALTER TABLE feedback_hidden RENAME TO feedback`); err != nil {
		t.Fatal(err)
	}
	if got := runIngressJob(t, a, job(maxClickAttempts+2)); !got.acked {
		t.Fatalf("the click after the notice settled %+v, want processed", got)
	}
	if _, n := readFeedback(t, a, taskID); n != 1 {
		t.Errorf("feedback rows = %d, want the rating stored by the click after the notice", n)
	}
}

func TestFeedbackMetricCountsStoredChangesOnce(t *testing.T) {
	a, _, _, taskID := feedbackReplica(t)
	metrics := &countingMetrics{}
	a.metrics = metrics
	recordOlderCardRating(t, a, taskID)
	for _, ev := range []*inboundEvent{
		feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, map[string][]string{feedbackCommentInput: {"Good."}}),
		feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, map[string][]string{feedbackCommentInput: {"Good."}}),
	} {
		if res := a.process(t.Context(), ev); res.outcome != outcomeSubmitted {
			t.Fatalf("%s outcome = %v, want submitted", ev.Action[actionMethodParam], res.outcome)
		}
	}

	for part, want := range map[string]int64{"rating": 1, "details": 1} {
		if got := metrics.count(metricFeedback, "gatewayId", a.gatewayID, "rating", ratingUp, "part", part); got != want {
			t.Errorf("%s count = %d, want %d", part, got, want)
		}
	}
}

func TestFeedbackDetailsFromAnOlderCardStoreNoConsent(t *testing.T) {
	a, _, _, taskID := feedbackReplica(t)
	recordOlderCardRating(t, a, taskID)
	details := feedbackClick(requesterName, actionFeedbackDetails, taskID, ratingUp, map[string][]string{
		feedbackCommentInput: {"From an older card."}, "feedback_share": {"share_task_content"},
	})
	if res := a.process(t.Context(), details); res.outcome != outcomeSubmitted {
		t.Fatalf("details outcome = %v, want submitted", res.outcome)
	}
	if row, _ := readFeedback(t, a, taskID); row.comment.String != "From an older card." || row.share != 0 {
		t.Errorf("feedback row = %+v, want the comment kept and no consent", row)
	}
}

func recordOlderCardRating(t *testing.T, a *Adapter, taskID string) {
	t.Helper()
	if _, err := a.recordFeedback(t.Context(), requesterEmail, store.FeedbackSubmission{TaskID: taskID, SessionID: "sess-fb", Rating: ratingUp}); err != nil {
		t.Fatalf("record the older card's rating: %v", err)
	}
}

func postFeedbackCard(t *testing.T, chat *fakeChat, taskID string) string {
	t.Helper()
	meta := dmMeta()
	name, err := chat.CreateMessage(t.Context(), meta.SpaceName, meta.ThreadName, messageContent{FallbackText: feedbackPrompt, Cards: feedbackCard(taskID, "sess-fb")})
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func lastReplacement(t *testing.T, chat *fakeChat) fakeWrite {
	t.Helper()
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool {
		return slices.ContainsFunc(w, func(x fakeWrite) bool { return x.op == "update" && x.replacedCards })
	})
	for i := len(writes) - 1; i >= 0; i-- {
		if writes[i].op == "update" && writes[i].replacedCards {
			return writes[i]
		}
	}
	t.Fatal("no card was replaced")
	return fakeWrite{}
}
