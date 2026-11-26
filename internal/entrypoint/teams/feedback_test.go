// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func storeBackedTeamsAdapter(t *testing.T) *TeamsAdapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	a := newTestAdapter(t)
	a.core = core
	a.ctx = t.Context()
	a.cfg.FeedbackEnabled = true
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.emailCache = NewEmailCache("", 0, a.botClient, a.logger)
	a.feedbackCorr = store.NewTeamsFeedbackCorrelationRepo(core.Store())
	return a
}

func requireRecorded(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("recordFeedback: %v", err)
	}
}

func recordErr(_ bool, err error) error { return err }

func seedTeamsTask(t *testing.T, a *TeamsAdapter, taskID, agentName string) {
	t.Helper()
	task := &store.Task{
		ID: taskID, UserID: "aad-123",
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	}
	if agentName != "" {
		handlerType := store.TaskHandlerTypeAgent
		if strings.HasPrefix(agentName, "workflow_") {
			handlerType = store.TaskHandlerTypeWorkflow
		}
		task.AgentName = &agentName
		task.HandlerName = &agentName
		task.HandlerType = &handlerType
	}
	if err := store.NewTaskRepo(a.core.Store()).Create(t.Context(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func TestTeamsRecordFeedback_PersistsAndFires(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")

	var fired struct {
		task, rating, comment, user string
		ok                          bool
	}
	a.core.OnFeedback(func(taskID, rating, comment, userID string) {
		fired.task, fired.rating, fired.comment, fired.user, fired.ok = taskID, rating, comment, userID, true
	})

	act := &Activity{}
	act.From.ID = "29:abc"
	act.From.AadObjectID = "aad-123"
	fbCtx := feedbackContext{TaskID: taskID, SessionID: "22222222-2222-2222-2222-222222222222"}
	data := feedbackSubmitData{Rating: "down", Comment: "not helpful"}

	requireRecorded(t, recordErr(a.recordFeedback(t.Context(), act, fbCtx, data)))

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search feedback: %v", err)
	}
	if len(fbs) != 1 {
		t.Fatalf("expected 1 persisted feedback row, got %d", len(fbs))
	}
	row := fbs[0]
	if row.Rating != "down" {
		t.Errorf("rating = %q, want down", row.Rating)
	}
	if row.Comment == nil || *row.Comment != "not helpful" {
		t.Errorf("comment = %v, want 'not helpful'", row.Comment)
	}
	if row.UserID != "aad-123" {
		t.Errorf("userID = %q, want resolved AAD object id 'aad-123'", row.UserID)
	}
	if row.EntrypointType != store.EntrypointTypeTeamsBot {
		t.Errorf("entrypoint_type = %q, want %q", row.EntrypointType, store.EntrypointTypeTeamsBot)
	}
	if row.ShareContent {
		t.Error("share_content must be false: Teams no longer offers a consent control")
	}
	if !fired.ok || fired.task != taskID || fired.rating != "down" || fired.comment != "not helpful" {
		t.Errorf("hook fired wrong: %+v", fired)
	}
	if fired.user != row.UserID {
		t.Errorf("fired userID %q != persisted %q", fired.user, row.UserID)
	}
}

func TestTeamsHandleFeedbackSubmit_RoutingAndValidation(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	fired := 0
	a.core.OnFeedback(func(_, _, _, _ string) { fired++ })

	taskID := "55555555-5555-5555-5555-555555555555"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	fbCtx := feedbackContext{TaskID: taskID, SessionID: "66666666-6666-6666-6666-666666666666"}
	mk := func(rating string) *Activity {
		v, _ := json.Marshal(map[string]any{"rating": rating, "comment": "c"})
		act := &Activity{Type: ActivityTypeInvoke, Name: "adaptiveCard/action", Value: v}
		act.From.AadObjectID = "aad-123"
		return act
	}

	_, _ = a.submitFeedback(t.Context(), mk("sideways"), fbCtx)
	_, _ = a.submitFeedback(t.Context(), mk("down"), fbCtx)

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(fbs) != 1 {
		t.Errorf("invalid rating must be ignored: want 1 row, got %d", len(fbs))
	}
	if fired != 1 {
		t.Errorf("want 1 fire (only the valid submit), got %d", fired)
	}
}

func TestTeamsRecordFeedback_NoStoreIsNoop(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	fired := false
	core.OnFeedback(func(_, _, _, _ string) { fired = true })

	a := newTestAdapter(t)
	a.core = core
	a.ctx = t.Context()
	requireRecorded(t, recordErr(a.recordFeedback(t.Context(), &Activity{},
		feedbackContext{TaskID: "t", SessionID: "s"}, feedbackSubmitData{Rating: "down"})))
	if fired {
		t.Error("no store → feedback hook must not fire")
	}
}

func TestTeamsFeedback_RoutingAndGuards(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	fired := 0
	a.core.OnFeedback(func(_, _, _, _ string) { fired++ })

	full := feedbackContext{TaskID: "t-9", SessionID: "s-9"}
	_, _ = a.submitFeedback(t.Context(), &Activity{}, full)
	_, _ = a.submitFeedback(t.Context(), &Activity{Value: json.RawMessage(`{not json`)}, full)
	_, _ = a.submitFeedback(t.Context(), &Activity{Value: mustJSON(t, map[string]any{"rating": "down"})},
		feedbackContext{TaskID: "t-9"})
	if fired != 0 {
		t.Fatalf("guard paths must not fire; got %d", fired)
	}

}

func TestTeamsOnFinalResponse_FeedbackGating(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	a.queues = map[string]*MessageQueue{}

	var mu sync.Mutex
	var bodies []string
	var postedIDs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "oauth2") || strings.Contains(r.URL.Path, "token") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("a%d", postedIDs.Add(1))})
	}))
	t.Cleanup(srv.Close)
	a.cfg.APIBaseURL = srv.URL
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL

	register := func(taskID string) {
		_ = a.core.Tasks().Register(taskID, "agent", "u", entrypoint.TaskIdentity{})
		a.core.Tasks().SetMetadata(taskID, &TeamsTaskMeta{
			ConversationID: "c", ServiceURL: srv.URL, SessionID: "s-final", Activity: &Activity{ID: "m1"},
		})
	}
	postedFeedback := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, b := range bodies {
			if strings.Contains(b, "feedbackLoop") {
				return true
			}
		}
		return false
	}
	answer := func(taskID string) string {
		q := a.getOrCreateQueue(taskID, a.getTaskMeta(t.Context(), taskID))
		q.Send(TextUpdateOp{Text: "the answer"})
		q.WaitDrain()
		return q.ContentMessageID()
	}
	completed := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}

	a.cfg.FeedbackEnabled = true
	register("t-final-1")
	_ = answer("t-final-1")
	a.OnFinalResponse(t.Context(), "t-final-1", completed, nil)
	if !postedFeedback() {
		t.Error("expected the answer message to carry the feedback marker when FeedbackEnabled")
	}

	mu.Lock()
	bodies = nil
	mu.Unlock()
	a.cfg.FeedbackEnabled = false
	register("t-final-2")
	_ = answer("t-final-2")
	a.OnFinalResponse(t.Context(), "t-final-2", completed, nil)
	if postedFeedback() {
		t.Error("FeedbackEnabled=false must not mark the answer for feedback")
	}

	mu.Lock()
	bodies = nil
	mu.Unlock()
	a.cfg.FeedbackEnabled = true
	register("t-final-3")
	failedAnswerID := answer("t-final-3")
	a.OnFinalResponse(t.Context(), "t-final-3", &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}, nil)
	if !postedFeedback() {
		t.Error("an answer that exists should be rateable even when the task failed")
	}
	got, ok, _ := a.feedbackCorrelation(t.Context(), "c", failedAnswerID)
	if !ok {
		t.Error("a failed task's answer carries icons but no correlation; every click would be refused")
	} else if got.TaskID != "t-final-3" {
		t.Errorf("correlation for the failed answer resolves to %q, want t-final-3", got.TaskID)
	}

	mu.Lock()
	bodies = nil
	mu.Unlock()
	register("t-final-4")
	a.OnFinalResponse(t.Context(), "t-final-4", &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}, nil)
	if postedFeedback() {
		t.Error("a failure with no answer must not mark anything for feedback")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTeamsHandleMessageActivity_UnknownActionValueIsDropped(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	fired := 0
	a.core.OnFeedback(func(_, _, _, _ string) { fired++ })

	act := &Activity{Type: ActivityTypeMessage, Value: mustJSON(t, map[string]any{"action": "totally_unknown_action", "foo": "bar"})}
	act.From.ID = "29:user"
	act.Conversation.ConversationType = ConversationTypePersonal

	a.handleMessageActivity(t.Context(), act)
	if fired != 0 {
		t.Fatalf("unknown card action must not fire feedback; fired=%d", fired)
	}
}

func TestTeamsRecordFeedback_DropPaths(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	fired := false
	a.core.OnFeedback(func(_, _, _, _ string) { fired = true })

	attributed := "t-drop-1"
	seedTeamsTask(t, a, attributed, "OrchestratorAgent")
	bare := "t-drop-2"
	seedTeamsTask(t, a, bare, "")

	resolved := &Activity{}
	resolved.From.ID = "29:abc"
	resolved.From.AadObjectID = "aad-123"

	cases := []struct {
		desc  string
		act   *Activity
		fbCtx feedbackContext
		data  feedbackSubmitData
	}{
		{"unresolved user", &Activity{}, feedbackContext{TaskID: attributed, SessionID: "s"}, feedbackSubmitData{Rating: "down"}},
		{"missing task", resolved, feedbackContext{TaskID: "t-drop-missing", SessionID: "s"}, feedbackSubmitData{Rating: "up"}},
		{"task without entry-point", resolved, feedbackContext{TaskID: bare, SessionID: "s"}, feedbackSubmitData{Rating: "up"}},
	}
	for _, tc := range cases {
		recorded, err := a.recordFeedback(t.Context(), tc.act, tc.fbCtx, tc.data)
		if err != nil {
			t.Fatalf("%s: recordFeedback: %v", tc.desc, err)
		}
		if recorded {
			t.Errorf("%s: reported a capture, want none", tc.desc)
		}
		fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, tc.fbCtx.TaskID, "", "", 10, 0)
		if err != nil {
			t.Fatalf("%s: search: %v", tc.desc, err)
		}
		if len(fbs) != 0 {
			t.Errorf("%s: persisted %d rows, want 0", tc.desc, len(fbs))
		}
	}
	if fired {
		t.Error("dropped feedback must not fire the hook")
	}
}

func subscribeFeedback(t *testing.T, core *entrypoint.GatewayCore, name string) (broker.Broker, broker.Queue) {
	t.Helper()
	brk := core.Services().Broker()
	q, err := brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.FeedbackTopic(core.Config().Namespace)); err != nil {
		t.Fatal(err)
	}
	return brk, q
}

func receiveFeedbackEvent(t *testing.T, brk broker.Broker, q broker.Queue) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected broker message, got error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal broker payload: %v", err)
	}
	fb, ok := payload["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'feedback' key in payload, got: %v", payload)
	}
	return fb
}

func assertNoFeedbackEvent(t *testing.T, brk broker.Broker, q broker.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if msg, err := brk.Receive(ctx, q); err == nil && msg != nil {
		t.Fatalf("expected no broker message, got one: %s", msg.Payload)
	}
}

func TestTeamsRecordFeedback_PublishesToBrokerWhenEnabled(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	a.core.Config().FeedbackPublishingRaw = map[string]any{"enabled": true}
	brk, q := subscribeFeedback(t, a.core, "test-teams-feedback-pub")

	taskID := "10101010-1010-1010-1010-101010101010"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	act := &Activity{}
	act.From.ID = "29:abc"
	act.From.AadObjectID = "aad-123"
	fbCtx := feedbackContext{TaskID: taskID, SessionID: "20202020-2020-2020-2020-202020202020"}
	data := feedbackSubmitData{Rating: "down", Comment: "not helpful"}

	requireRecorded(t, recordErr(a.recordFeedback(t.Context(), act, fbCtx, data)))

	fb := receiveFeedbackEvent(t, brk, q)
	checks := map[string]any{
		"event":           "created",
		"task_id":         taskID,
		"session_id":      fbCtx.SessionID,
		"feedback_type":   "down",
		"feedback_text":   "not helpful",
		"entrypoint_type": store.EntrypointTypeTeamsBot,
	}
	for key, want := range checks {
		if got := fb[key]; got != want {
			t.Errorf("broker payload %s: expected %v, got %v", key, want, got)
		}
	}
}

func TestTeamsRecordFeedback_PublishingDisabledByDefault(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	brk, q := subscribeFeedback(t, a.core, "test-teams-feedback-off")

	taskID := "30303030-3030-3030-3030-303030303030"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	act := &Activity{}
	act.From.ID = "29:abc"
	act.From.AadObjectID = "aad-123"

	requireRecorded(t, recordErr(a.recordFeedback(t.Context(), act,
		feedbackContext{TaskID: taskID, SessionID: "40404040-4040-4040-4040-404040404040"},
		feedbackSubmitData{Rating: "up"})))

	assertNoFeedbackEvent(t, brk, q)
}

func TestTeamsQueue_NoIconsWithoutAStore(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	a := newTestAdapter(t)
	a.core = core
	a.ctx = t.Context()
	a.cfg.FeedbackEnabled = true

	meta := &TeamsTaskMeta{ConversationID: "c", ServiceURL: "http://example.invalid", SessionID: "s", Activity: &Activity{ID: "m1"}}
	q := a.getOrCreateQueue("t-no-store", meta)
	if q.feedbackEnabled {
		t.Error("the answer would carry feedback icons with nowhere to record a rating")
	}
}

func TestTeamsAdapter_RetiredFeedbackCardIsAnswered(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	a := storeBackedTeamsAdapter(t)
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL

	act := &Activity{
		Type:         ActivityTypeMessage,
		ServiceURL:   srv.URL,
		Conversation: Conversation{ID: "conv-1"},
		Value: mustJSON(t, map[string]any{
			"action": FeedbackActionID, "taskId": "t-old", "sessionId": "s-old", "rating": "up",
		}),
	}
	act.From.ID = "29:user"
	a.handleAdaptiveCardAction(act)

	mu.Lock()
	defer mu.Unlock()
	var posted []string
	for _, c := range *calls {
		if c.Method == http.MethodPost {
			if text, ok := c.Body["text"].(string); ok {
				posted = append(posted, text)
			}
		}
	}
	if len(posted) != 1 || posted[0] != retiredFeedbackCardNotice {
		t.Errorf("a retired card click posted %v, want exactly the retired-form notice", posted)
	}
}
