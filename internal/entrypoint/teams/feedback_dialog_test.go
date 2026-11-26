// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestFeedbackDialog_UsesSharedCopy(t *testing.T) {
	const wantNotice = "This conversation may be viewed to help improve responses"
	const wantAck = "Thank you for your feedback"

	if entrypoint.FeedbackDataUseNotice != wantNotice {
		t.Errorf("data-use notice = %q, want %q", entrypoint.FeedbackDataUseNotice, wantNotice)
	}
	if entrypoint.FeedbackAckNotice != wantAck {
		t.Errorf("ack notice = %q, want %q", entrypoint.FeedbackAckNotice, wantAck)
	}

	if raw := openFeedbackDialog(t); !strings.Contains(raw, wantNotice) {
		t.Errorf("dialog missing the data-use notice: %s", raw)
	}
}

func TestFeedbackDialog_HasNoConsentControl(t *testing.T) {
	const consentLabel = "Attach this conversation to your feedback to help improve responses"

	raw := openFeedbackDialog(t)
	for _, banned := range []string{"Input.Toggle", consentLabel, "shareTaskContent"} {
		if strings.Contains(raw, banned) {
			t.Errorf("dialog still carries %q: %s", banned, raw)
		}
	}
}

func openFeedbackDialog(t *testing.T) string {
	t.Helper()
	a := dialogTestAdapter()
	a.registerFeedback("task-1", &TeamsTaskMeta{SessionID: "sess-1", ConversationID: "conv-1"}, "msg-1", "the answer")

	resp := a.handleMessageFetchTask(t.Context(), &Activity{
		Name:         InvokeMessageFetchTask,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        json.RawMessage(`{"data":{"actionName":"feedback","actionValue":{"reaction":"like"}}}`),
	})
	if resp == nil {
		t.Fatal("fetchTask returned no response")
	}
	raw, err := json.Marshal(resp.Body)
	if err != nil {
		t.Fatalf("marshal dialog: %v", err)
	}
	return string(raw)
}

func TestFeedbackDialog_CarriesRatingOnlyNotAttribution(t *testing.T) {
	a := dialogTestAdapter()
	a.registerFeedback("task-42", &TeamsTaskMeta{SessionID: "sess-42", ConversationID: "conv-42"}, "msg-42", "the answer")

	resp := a.handleMessageFetchTask(t.Context(), &Activity{
		Name:         InvokeMessageFetchTask,
		ReplyToID:    "msg-42",
		Conversation: Conversation{ID: "conv-42"},
		Value:        json.RawMessage(`{"data":{"actionName":"feedback","actionValue":{"reaction":"dislike"}}}`),
	})
	submit := dialogSubmitData(t, resp)

	if submit["rating"] != "down" {
		t.Errorf("rating = %v, want down (a dislike must not record as positive)", submit["rating"])
	}
	for _, leaked := range []string{"taskId", "sessionId", "action"} {
		if v, ok := submit[leaked]; ok {
			t.Errorf("dialog submit data carries %q = %v; attribution is server-side", leaked, v)
		}
	}
}

func TestFeedbackDialog_SameMessageIDInTwoConversations(t *testing.T) {
	a := dialogTestAdapter()
	a.registerFeedback("task-a", &TeamsTaskMeta{SessionID: "sess-a", ConversationID: "conv-a"}, "shared-id", "answer A")
	a.registerFeedback("task-b", &TeamsTaskMeta{SessionID: "sess-b", ConversationID: "conv-b"}, "shared-id", "answer B")

	for _, tc := range []struct{ conv, wantTask, wantText string }{
		{"conv-a", "task-a", "answer A"},
		{"conv-b", "task-b", "answer B"},
	} {
		got, ok, _ := a.feedbackCorrelation(t.Context(), tc.conv, "shared-id")
		if !ok {
			t.Fatalf("%s: no correlation", tc.conv)
		}
		if got.TaskID != tc.wantTask {
			t.Errorf("%s: taskID = %q, want %q", tc.conv, got.TaskID, tc.wantTask)
		}
		if got.ContentText != tc.wantText {
			t.Errorf("%s: text = %q, want %q", tc.conv, got.ContentText, tc.wantText)
		}
	}
}

func TestFeedbackDialog_RefusesUnknownReaction(t *testing.T) {
	a := dialogTestAdapter()
	a.registerFeedback("task-1", &TeamsTaskMeta{SessionID: "sess-1", ConversationID: "conv-1"}, "msg-1", "the answer")

	resp := a.handleMessageFetchTask(t.Context(), &Activity{
		Name:         InvokeMessageFetchTask,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        json.RawMessage(`{"data":{"actionName":"feedback","actionValue":{"reaction":"sparkles"}}}`),
	})
	assertRefusalTaskModule(t, resp)
}

func TestFeedbackDialog_RefusesUncorrelatedMessage(t *testing.T) {
	a := dialogTestAdapter()

	resp := a.handleMessageFetchTask(t.Context(), &Activity{
		Name:         InvokeMessageFetchTask,
		ReplyToID:    "never-registered",
		Conversation: Conversation{ID: "conv-1"},
		Value:        json.RawMessage(`{"data":{"actionName":"feedback","actionValue":{"reaction":"like"}}}`),
	})
	raw := assertRefusalTaskModule(t, resp)
	if strings.Contains(raw, FeedbackCommentInputID) {
		t.Errorf("an uncorrelated click must not get a comment box: %s", raw)
	}
	if strings.Contains(raw, "no longer") {
		t.Errorf("refusal must not claim the window has passed: %s", raw)
	}
}

func dialogTestAdapter() *TeamsAdapter {
	return &TeamsAdapter{logger: teamsNoopLogger(), feedbackCtx: runtime.NewMapCache()}
}

func assertRefusalTaskModule(t *testing.T, resp *InvokeResponse) string {
	t.Helper()
	if resp == nil || resp.Body == nil {
		t.Fatalf("a refusal still needs a task-module body, got %+v", resp)
	}
	raw, err := json.Marshal(resp.Body)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"message"`) {
		t.Errorf("want a message task telling the reader, got: %s", raw)
	}
	return string(raw)
}

func dialogSubmitData(t *testing.T, resp *InvokeResponse) map[string]any {
	t.Helper()
	if resp == nil || resp.Body == nil {
		t.Fatal("fetchTask returned no dialog")
	}
	raw, err := json.Marshal(resp.Body)
	if err != nil {
		t.Fatalf("marshal dialog: %v", err)
	}
	var envelope struct {
		Task struct {
			Value struct {
				Card struct {
					Content struct {
						Actions []struct {
							Data map[string]any `json:"data"`
						} `json:"actions"`
					} `json:"content"`
				} `json:"card"`
			} `json:"value"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal dialog: %v", err)
	}
	actions := envelope.Task.Value.Card.Content.Actions
	if len(actions) != 1 {
		t.Fatalf("want exactly one Submit, got %d: %s", len(actions), raw)
	}
	return actions[0].Data
}

func TestQueue_ContentMessageCarriesFeedbackMarker(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		wantMark bool
	}{
		{"feedback enabled", true, true},
		{"feedback disabled", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls, mu := newMockBotServer(t)
			defer srv.Close()

			q := newTestQueue(t, srv)
			q.feedbackEnabled = tc.enabled
			q.Start()
			q.Send(TextUpdateOp{Text: "the answer"})
			q.Stop(2 * time.Second)

			mu.Lock()
			defer mu.Unlock()
			var sent map[string]any
			for _, c := range *calls {
				if c.Method == http.MethodPost && c.Body["text"] == "the answer" {
					sent = c.Body
				}
			}
			if sent == nil {
				t.Fatalf("the answer was never sent; calls: %+v", *calls)
			}

			raw, _ := json.Marshal(sent["channelData"])
			has := strings.Contains(string(raw), "feedbackLoop")
			if has != tc.wantMark {
				t.Errorf("feedbackLoop present = %v, want %v (channelData: %s)", has, tc.wantMark, raw)
			}
		})
	}

	t.Run("content id is exposed for correlation", func(t *testing.T) {
		srv, _, _ := newMockBotServer(t)
		defer srv.Close()

		q := newTestQueue(t, srv)
		q.Start()
		q.Send(TextUpdateOp{Text: "the answer"})
		q.Stop(2 * time.Second)

		if q.ContentMessageID() == "" {
			t.Error("ContentMessageID is empty; feedback could not be attributed to a task")
		}
	})
}

func TestQueue_FeedbackMarkerSurvivesStreamingUpdates(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.feedbackEnabled = true
	q.Start()
	q.Send(TextUpdateOp{Text: "first chunk "})
	q.Send(TextUpdateOp{Text: "second chunk "})
	q.Send(TextUpdateOp{Text: "third chunk"})
	q.Stop(2 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	var updates, markedUpdates int
	for _, c := range *calls {
		if c.Method != http.MethodPut {
			continue
		}
		updates++
		raw, _ := json.Marshal(c.Body["channelData"])
		if strings.Contains(string(raw), "feedbackLoop") {
			markedUpdates++
		}
	}
	if updates == 0 {
		t.Skip("this run produced no update; the buffer coalesced every chunk")
	}
	if markedUpdates != updates {
		t.Errorf("%d of %d updates carried the feedback marker; the icons would vanish mid-stream",
			markedUpdates, updates)
	}
}

func TestFeedback_SubmitAnswersEmpty_FetchAnswersWithDialog(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	a.registerFeedback("t-1", &TeamsTaskMeta{SessionID: "s-1", ConversationID: "conv-1"}, "msg-1", "the answer")

	fetch := a.handleMessageFetchTask(t.Context(), &Activity{
		Name:         InvokeMessageFetchTask,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        json.RawMessage(`{"data":{"actionName":"feedback","actionValue":{"reaction":"like"}}}`),
	})
	if fetch == nil || fetch.Body == nil {
		t.Fatal("the dialog fetch must return a task envelope")
	}
	if fetch.EmptyBody {
		t.Error("the dialog fetch must not answer empty; the modal would never open")
	}
	if !strings.Contains(mustMarshal(t, fetch.Body), FeedbackCommentInputID) {
		t.Error("a correlated click must open the comment dialog, not a refusal")
	}

	submit := a.handleMessageSubmitAction(t.Context(), &Activity{
		Name:         InvokeMessageSubmitAction,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        json.RawMessage(`{"actionValue":{"feedback":"{\"rating\":\"up\"}"}}`),
	})
	if submit == nil {
		t.Fatal("the submit returned no response")
	}
	if !submit.EmptyBody {
		t.Error("the submit must answer with an empty body, or Teams rejects it")
	}
	if submit.Body != nil {
		t.Errorf("the submit must carry no body, got %v", submit.Body)
	}
}

func TestFeedbackSubmit_CommentNeverLogged(t *testing.T) {
	logs := &lockedBuffer{}
	a := storeBackedTeamsAdapter(t)
	a.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	taskID := "77777777-7777-7777-7777-777777777777"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	a.registerFeedback(taskID, &TeamsTaskMeta{
		SessionID: "88888888-8888-8888-8888-888888888888", ConversationID: "conv-1",
	}, "msg-1", "the answer")

	const comment = "SECRET-FEEDBACK-COMMENT the agent leaked my account number"
	inner, err := json.Marshal(map[string]any{"rating": "up", "comment": comment})
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	envelope, err := json.Marshal(map[string]any{
		"actionValue": map[string]any{"feedback": string(inner)},
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	act := &Activity{
		Name:         InvokeMessageSubmitAction,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        envelope,
	}
	act.From.ID = "29:user"
	act.From.AadObjectID = "aad-1"
	a.handleMessageSubmitAction(t.Context(), act)
	a.server.Wait()

	if out := logs.String(); strings.Contains(out, "SECRET-FEEDBACK-COMMENT") {
		t.Errorf("comment text leaked into the logs:\n%s", out)
	}
	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(fbs) != 1 {
		t.Fatalf("submit did not reach the store (rows=%d, err=%v); the log assertion proves nothing", len(fbs), err)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestFeedback_RemovesIconsWithoutTouchingTheAnswer(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	a := storeBackedTeamsAdapter(t)
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.markAnswerFeedbackRecorded(t.Context(), &Activity{
		ServiceURL:   srv.URL,
		ReplyToID:    "answer-msg",
		Conversation: Conversation{ID: "conv-1"},
	}, feedbackContext{TaskID: "t-1", SessionID: "s-1", ContentText: "the original answer"})

	mu.Lock()
	defer mu.Unlock()
	var updated map[string]any
	for _, c := range *calls {
		if strings.Contains(c.Path, "answer-msg") {
			updated = c.Body
		}
	}
	if updated == nil {
		t.Fatalf("the answer was never marked; calls: %+v", *calls)
	}
	text, _ := updated["text"].(string)
	if !strings.Contains(text, "the original answer") {
		t.Errorf("the answer text was lost: %q", text)
	}
	if text != "the original answer" {
		t.Errorf("the answer was altered: %q", text)
	}
	if att, ok := updated["attachments"]; ok && att != nil {
		t.Errorf("the update carries an attachment alongside text; Teams will reject it: %v", att)
	}
	chanData, _ := json.Marshal(updated["channelData"])
	if strings.Contains(string(chanData), "feedbackLoop") {
		t.Errorf("the feedback icons survived a final rating: %s", chanData)
	}
}

func TestFeedbackSubmit_MalformedPayloadAnswersWithEmptyBody(t *testing.T) {
	a := dialogTestAdapter()

	for _, tc := range []struct {
		name  string
		value string
	}{
		{"unparsable", `{"data":`},
		{"empty feedback", `{"actionValue":{"feedback":""}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := a.handleMessageSubmitAction(t.Context(), &Activity{
				Name:  InvokeMessageSubmitAction,
				Value: json.RawMessage(tc.value),
			})
			if resp == nil {
				t.Fatal("no response")
			}
			if resp.Status != 200 {
				t.Errorf("status = %d, want 200", resp.Status)
			}
			if !resp.EmptyBody {
				t.Error("EmptyBody = false; Teams rejects a submit response with a body and strands the dialog")
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func TestFeedback_CorrelationSurvivesLosingTheSendingInstance(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	taskID := "99999999-9999-9999-9999-999999999999"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	a.registerFeedback(taskID, &TeamsTaskMeta{
		SessionID: "aaaaaaaa-9999-9999-9999-999999999999", ConversationID: "conv-1",
	}, "msg-1", "the answer")

	a.feedbackCtx = runtime.NewMapCache()

	got, ok, _ := a.feedbackCorrelation(t.Context(), "conv-1", "msg-1")
	if !ok {
		t.Fatal("the click is unattributable on an instance that did not send the answer")
	}
	if got.TaskID != taskID {
		t.Errorf("taskID = %q, want %q", got.TaskID, taskID)
	}
	if got.ContentText != "" {
		t.Errorf("answer text came back from the store: %q", got.ContentText)
	}
}

func TestFeedback_RecordsOnAnInstanceThatDidNotSendTheAnswer(t *testing.T) {
	srv, _, _ := newMockBotServer(t)
	defer srv.Close()

	a := storeBackedTeamsAdapter(t)
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL

	taskID := "bbbbbbbb-9999-9999-9999-999999999999"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	a.registerFeedback(taskID, &TeamsTaskMeta{
		SessionID: "cccccccc-9999-9999-9999-999999999999", ConversationID: "conv-1",
	}, "msg-1", "the answer")
	a.feedbackCtx = runtime.NewMapCache()

	act := &Activity{
		ServiceURL:   srv.URL,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        mustJSON(t, map[string]any{"rating": "down", "comment": "wrong answer"}),
	}
	act.From.ID = "29:user"
	act.From.AadObjectID = "aad-1"
	a.handleFeedbackSubmit(act, false)

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(fbs) != 1 {
		t.Fatalf("feedback rows = %d (err %v), want the rating recorded anyway", len(fbs), err)
	}
	if fbs[0].Rating != "down" {
		t.Errorf("rating = %q, want down", fbs[0].Rating)
	}
}

func TestFeedbackSubmit_IgnoresTaskIDInThePayload(t *testing.T) {
	srv, _, _ := newMockBotServer(t)
	defer srv.Close()

	a := storeBackedTeamsAdapter(t)
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL

	rated := "dddddddd-9999-9999-9999-999999999999"
	other := "eeeeeeee-9999-9999-9999-999999999999"
	seedTeamsTask(t, a, rated, "OrchestratorAgent")
	seedTeamsTask(t, a, other, "OrchestratorAgent")
	a.registerFeedback(rated, &TeamsTaskMeta{
		SessionID: "ffffffff-9999-9999-9999-999999999999", ConversationID: "conv-1",
	}, "msg-1", "the answer")

	act := &Activity{
		ServiceURL:   srv.URL,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value: mustJSON(t, map[string]any{
			"rating": "up", "taskId": other, "sessionId": "00000000-9999-9999-9999-999999999999",
		}),
	}
	act.From.ID = "29:user"
	act.From.AadObjectID = "aad-1"
	a.handleFeedbackSubmit(act, false)

	repo := store.NewFeedbackRepo(a.core.Store())
	if fbs, _ := repo.Search(t.Context(), "*", 0, 0, other, "", "", 10, 0); len(fbs) != 0 {
		t.Errorf("the payload's task got %d rows; attribution followed the client", len(fbs))
	}
	if fbs, _ := repo.Search(t.Context(), "*", 0, 0, rated, "", "", 10, 0); len(fbs) != 1 {
		t.Errorf("the correlated task got %d rows, want 1", len(fbs))
	}
}

func TestFeedbackDialog_CommentIsBoundedOnBothSides(t *testing.T) {
	if !strings.Contains(openFeedbackDialog(t), `"maxLength":2000`) {
		t.Error("the comment box declares no maxLength")
	}

	srv, _, _ := newMockBotServer(t)
	defer srv.Close()
	a := storeBackedTeamsAdapter(t)
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL

	taskID := "12121212-1212-1212-1212-121212121212"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	a.registerFeedback(taskID, &TeamsTaskMeta{
		SessionID: "13131313-1313-1313-1313-131313131313", ConversationID: "conv-1",
	}, "msg-1", "the answer")

	act := &Activity{
		ServiceURL:   srv.URL,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value: mustJSON(t, map[string]any{
			"rating": "down", "comment": strings.Repeat("x", maxFeedbackCommentLen*3),
		}),
	}
	act.From.ID = "29:user"
	act.From.AadObjectID = "aad-1"
	a.handleFeedbackSubmit(act, false)

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(fbs) != 1 || fbs[0].Comment == nil {
		t.Fatalf("feedback rows = %d (err %v), want one with a comment", len(fbs), err)
	}
	if got := len(*fbs[0].Comment); got != maxFeedbackCommentLen {
		t.Errorf("stored comment is %d chars, want it capped at %d", got, maxFeedbackCommentLen)
	}
}

func TestFeedbackSubmit_UnrecordedSubmitTellsTheUserWhenTheStoreFails(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()
	a := storeBackedTeamsAdapter(t)
	a.botClient.apiBaseURL = srv.URL
	taskID := "14141414-1414-1414-1414-141414141414"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	a.registerFeedback(taskID, &TeamsTaskMeta{
		SessionID: "15151515-1515-1515-1515-151515151515", ConversationID: "conv-1",
	}, "msg-1", "the answer")
	_ = a.core.Store().Close()

	act := &Activity{
		ServiceURL:   srv.URL,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        mustJSON(t, map[string]any{"rating": "up"}),
	}
	act.From.ID = "29:user"
	act.From.AadObjectID = "aad-1"
	if !a.handleFeedbackSubmit(act, false) {
		t.Error("an unrecorded submit was left unsettled, with nothing to try it again")
	}

	mu.Lock()
	defer mu.Unlock()
	var texts []string
	for _, c := range *calls {
		if text, _ := c.Body["text"].(string); c.Method == http.MethodPost && text != "" {
			texts = append(texts, text)
		}
	}
	if !slices.Contains(texts, entrypoint.FeedbackNotRecordedNotice) {
		t.Errorf("posted %q, want the not-recorded notice", texts)
	}
}

func TestFeedbackSubmit_TruncatesTheCommentBetweenCharacters(t *testing.T) {
	srv, _, _ := newMockBotServer(t)
	defer srv.Close()
	a := storeBackedTeamsAdapter(t)
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL

	taskID := "16161616-1616-1616-1616-161616161616"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	a.registerFeedback(taskID, &TeamsTaskMeta{
		SessionID: "17171717-1717-1717-1717-171717171717", ConversationID: "conv-1",
	}, "msg-1", "the answer")

	act := &Activity{
		ServiceURL:   srv.URL,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value: mustJSON(t, map[string]any{
			"rating": "down", "comment": strings.Repeat("é", maxFeedbackCommentLen),
		}),
	}
	act.From.ID = "29:user"
	act.From.AadObjectID = "aad-1"
	a.handleFeedbackSubmit(act, false)

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(fbs) != 1 || fbs[0].Comment == nil {
		t.Fatalf("feedback rows = %d (err %v), want one with a comment", len(fbs), err)
	}
	stored := *fbs[0].Comment
	if !utf8.ValidString(stored) {
		t.Errorf("the stored comment is not valid text: it was cut inside a character")
	}
	if len(stored) > maxFeedbackCommentLen || len(stored) < maxFeedbackCommentLen-utf8.UTFMax {
		t.Errorf("stored comment is %d bytes, want the limit less at most one character", len(stored))
	}
}
