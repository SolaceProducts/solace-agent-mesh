// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
	"github.com/google/uuid"
)

type feedbackTestEnv struct {
	adapter *HTTPSSEAdapter
	handler *feedbackHandler
	mux     *http.ServeMux
	brk     broker.Broker
}

func setupFeedbackTest(t *testing.T, enablePublishing bool) *feedbackTestEnv {
	t.Helper()

	adapter, _, _, _, _ := setupTestAdapterWithDB(t)

	if enablePublishing {
		adapter.core.Config().FeedbackPublishingRaw = map[string]any{
			"enabled": true,
		}
	}

	db := adapter.core.Store()
	fbRepo := store.NewFeedbackRepo(db)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	fh := &feedbackHandler{
		core:           adapter.core,
		feedbackRepo:   fbRepo,
		taskRepo:       taskRepo,
		sessRepo:       store.NewSessionRepo(db),
		eventRepo:      eventRepo,
		logger:         adapter.logger,
		publishTimeout: feedbackPublishTimeout,
		nameResolver:   adapter.core.AgentRegistry().DisplayNameResolver(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/feedback", fh.handleSubmit)
	mux.HandleFunc("GET /api/v1/feedback", fh.handleList)

	return &feedbackTestEnv{
		adapter: adapter,
		handler: fh,
		mux:     mux,
		brk:     adapter.core.Services().Broker(),
	}
}

func (env *feedbackTestEnv) createTask(t *testing.T, taskID, agentName string) {
	t.Helper()
	if taskID == "" {
		return
	}
	ctx := context.Background()
	if existing, err := env.handler.taskRepo.GetByID(ctx, taskID); err != nil {
		t.Fatalf("get task: %v", err)
	} else if existing != nil {
		return
	}
	task := &store.Task{
		ID: taskID, UserID: feedbackTestUser, StartTime: time.Now().UnixMilli(),
		ExecutionMode: "foreground",
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
	if err := env.handler.taskRepo.Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
}

func (env *feedbackTestEnv) submitFeedback(t *testing.T, taskID, sessID, feedbackType string, feedbackText *string) *httptest.ResponseRecorder {
	t.Helper()
	env.createTask(t, taskID, "OrchestratorAgent")
	return env.submitFeedbackRaw(t, taskID, sessID, feedbackType, feedbackText)
}

const feedbackTestUser = "sam_dev_user"

func (env *feedbackTestEnv) createSession(t *testing.T, sessionID, userID string) {
	t.Helper()
	ctx := context.Background()
	existing, err := env.handler.sessRepo.GetByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if existing != nil {
		return
	}
	if err := env.handler.sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: userID}); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

func (env *feedbackTestEnv) createSessionIfValid(t *testing.T, sessionID string) {
	t.Helper()
	if _, err := uuid.Parse(sessionID); err == nil {
		env.createSession(t, sessionID, feedbackTestUser)
	}
}

func (env *feedbackTestEnv) submitFeedbackRaw(t *testing.T, taskID, sessID, feedbackType string, feedbackText *string) *httptest.ResponseRecorder {
	t.Helper()
	env.createSessionIfValid(t, sessID)
	body := map[string]any{
		"taskId":       taskID,
		"sessionId":    sessID,
		"feedbackType": feedbackType,
	}
	if feedbackText != nil {
		body["feedbackText"] = *feedbackText
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/feedback", bytes.NewReader(data))
	env.mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleFeedback_Submit(t *testing.T) {
	env := setupFeedbackTest(t, false)

	taskID := idgen.New()
	sessID := idgen.New()
	rec := env.submitFeedback(t, taskID, sessID, "up", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "received" {
		t.Errorf("expected status='received', got %v", resp["status"])
	}
}

func TestHandleFeedback_SubmitUpWithComment(t *testing.T) {
	env := setupFeedbackTest(t, false)

	taskID := idgen.New()
	sessID := idgen.New()
	comment := "Great response!"
	rec := env.submitFeedback(t, taskID, sessID, "up", &comment)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	feedbacks, err := env.handler.feedbackRepo.Search(context.Background(),
		"sam_dev_user", 0, 0, taskID, sessID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedbacks) != 1 {
		t.Fatalf("expected 1 feedback in DB, got %d", len(feedbacks))
	}
	if feedbacks[0].Rating != "up" {
		t.Errorf("expected rating=up, got %s", feedbacks[0].Rating)
	}
	if feedbacks[0].Comment == nil || *feedbacks[0].Comment != comment {
		t.Errorf("expected comment=%q, got %v", comment, feedbacks[0].Comment)
	}
	if feedbacks[0].EntrypointType != store.EntrypointTypeWebUI {
		t.Errorf("entrypoint_type = %q, want %q", feedbacks[0].EntrypointType, store.EntrypointTypeWebUI)
	}
}

func TestHandleFeedback_SubmitDownWithComment(t *testing.T) {
	env := setupFeedbackTest(t, false)

	taskID := idgen.New()
	sessID := idgen.New()
	comment := "Not helpful"
	rec := env.submitFeedback(t, taskID, sessID, "down", &comment)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	feedbacks, err := env.handler.feedbackRepo.Search(context.Background(),
		"sam_dev_user", 0, 0, taskID, sessID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedbacks) != 1 {
		t.Fatalf("expected 1 feedback in DB, got %d", len(feedbacks))
	}
	if feedbacks[0].Rating != "down" {
		t.Errorf("expected rating=down, got %s", feedbacks[0].Rating)
	}
	if feedbacks[0].Comment == nil || *feedbacks[0].Comment != comment {
		t.Errorf("expected comment=%q, got %v", comment, feedbacks[0].Comment)
	}
}

func TestHandleFeedback_SubmitInvalidType(t *testing.T) {
	env := setupFeedbackTest(t, false)

	taskID := idgen.New()
	sessID := idgen.New()
	rec := env.submitFeedback(t, taskID, sessID, "neutral", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedback_Submit_MissingFields(t *testing.T) {
	env := setupFeedbackTest(t, false)

	sessID := idgen.New()
	body := fmt.Sprintf(`{"sessionId":%q,"feedbackType":"up"}`, sessID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/feedback", strings.NewReader(body))
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedback_List(t *testing.T) {
	env := setupFeedbackTest(t, false)

	taskID := idgen.New()
	sessID := idgen.New()
	rec := env.submitFeedback(t, taskID, sessID, "up", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feedback", nil)
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fbResp PaginatedResponse[FeedbackResponse]
	if err := json.NewDecoder(rec.Body).Decode(&fbResp); err != nil {
		t.Fatal(err)
	}
	if len(fbResp.Data) != 1 {
		t.Fatalf("expected 1 feedback, got %d", len(fbResp.Data))
	}
	if fbResp.Data[0].TaskID != taskID {
		t.Errorf("expected taskId=%s, got %s", taskID, fbResp.Data[0].TaskID)
	}
	if fbResp.Data[0].SessionID != sessID {
		t.Errorf("expected sessionId=%s, got %s", sessID, fbResp.Data[0].SessionID)
	}
	if fbResp.Data[0].Rating != "up" {
		t.Errorf("expected rating=up, got %s", fbResp.Data[0].Rating)
	}
}

func TestHandleFeedback_ListBySessionID(t *testing.T) {
	env := setupFeedbackTest(t, false)

	sess1 := idgen.New()
	sess2 := idgen.New()
	task1 := idgen.New()
	task2 := idgen.New()

	rec := env.submitFeedback(t, task1, sess1, "up", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit 1: expected 200, got %d", rec.Code)
	}
	rec = env.submitFeedback(t, task2, sess2, "down", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit 2: expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feedback?sessionId="+sess1, nil)
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fbResp PaginatedResponse[FeedbackResponse]
	if err := json.NewDecoder(rec.Body).Decode(&fbResp); err != nil {
		t.Fatal(err)
	}
	if len(fbResp.Data) != 1 {
		t.Fatalf("expected 1 feedback for session %s, got %d", sess1, len(fbResp.Data))
	}
	if fbResp.Data[0].SessionID != sess1 {
		t.Errorf("expected sessionId=%s, got %s", sess1, fbResp.Data[0].SessionID)
	}
}

func TestHandleFeedback_ListByRating(t *testing.T) {
	env := setupFeedbackTest(t, false)

	sess := idgen.New()

	rec := env.submitFeedback(t, idgen.New(), sess, "up", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit up: expected 200, got %d", rec.Code)
	}
	rec = env.submitFeedback(t, idgen.New(), sess, "down", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit down: expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feedback?rating=up", nil)
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fbResp PaginatedResponse[FeedbackResponse]
	if err := json.NewDecoder(rec.Body).Decode(&fbResp); err != nil {
		t.Fatal(err)
	}
	if len(fbResp.Data) != 1 {
		t.Fatalf("expected 1 'up' feedback, got %d", len(fbResp.Data))
	}
	if fbResp.Data[0].Rating != "up" {
		t.Errorf("expected rating=up, got %s", fbResp.Data[0].Rating)
	}
}

func TestHandleFeedback_ListPagination(t *testing.T) {
	env := setupFeedbackTest(t, false)

	sess := idgen.New()

	for range 5 {
		rec := env.submitFeedback(t, idgen.New(), sess, "up", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("submit: expected 200, got %d", rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feedback?pageNumber=1&pageSize=2", nil)
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fbPage1 PaginatedResponse[FeedbackResponse]
	if err := json.NewDecoder(rec.Body).Decode(&fbPage1); err != nil {
		t.Fatal(err)
	}
	if len(fbPage1.Data) != 2 {
		t.Fatalf("page 1: expected 2 entries, got %d", len(fbPage1.Data))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/feedback?pageNumber=2&pageSize=2", nil)
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("page 2: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fbPage2 PaginatedResponse[FeedbackResponse]
	if err := json.NewDecoder(rec.Body).Decode(&fbPage2); err != nil {
		t.Fatal(err)
	}
	if len(fbPage2.Data) != 2 {
		t.Fatalf("page 2: expected 2 entries, got %d", len(fbPage2.Data))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/feedback?pageNumber=3&pageSize=2", nil)
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("page 3: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fbPage3 PaginatedResponse[FeedbackResponse]
	if err := json.NewDecoder(rec.Body).Decode(&fbPage3); err != nil {
		t.Fatal(err)
	}
	if len(fbPage3.Data) != 1 {
		t.Fatalf("page 3: expected 1 entry, got %d", len(fbPage3.Data))
	}
}

func TestHandleFeedback_CamelCaseJSONResponse(t *testing.T) {
	env := setupFeedbackTest(t, false)

	taskID := idgen.New()
	sessID := idgen.New()
	comment := "test comment"
	rec := env.submitFeedback(t, taskID, sessID, "up", &comment)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/feedback", nil)
	env.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var fbWrapped PaginatedResponse[json.RawMessage]
	if err := json.Unmarshal(rec.Body.Bytes(), &fbWrapped); err != nil {
		t.Fatal(err)
	}
	if len(fbWrapped.Data) != 1 {
		t.Fatalf("expected 1 feedback entry, got %d", len(fbWrapped.Data))
	}

	var fields map[string]any
	if err := json.Unmarshal(fbWrapped.Data[0], &fields); err != nil {
		t.Fatal(err)
	}

	requiredKeys := []string{"id", "sessionId", "taskId", "userId", "rating", "comment", "createdAt"}
	for _, key := range requiredKeys {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing expected camelCase key %q in response JSON", key)
		}
	}

	forbiddenKeys := []string{"session_id", "task_id", "user_id", "created_at", "created_time", "feedback_type"}
	for _, key := range forbiddenKeys {
		if _, ok := fields[key]; ok {
			t.Errorf("unexpected snake_case key %q found in response JSON", key)
		}
	}

	if fields["taskId"] != taskID {
		t.Errorf("expected task_id=%s, got %v", taskID, fields["taskId"])
	}
	if fields["sessionId"] != sessID {
		t.Errorf("expected session_id=%s, got %v", sessID, fields["sessionId"])
	}
	if fields["rating"] != "up" {
		t.Errorf("expected rating=up, got %v", fields["rating"])
	}
	if fields["comment"] != comment {
		t.Errorf("expected comment=%q, got %v", comment, fields["comment"])
	}
}

func TestHandleFeedback_BrokerPublishing(t *testing.T) {
	env := setupFeedbackTest(t, true)

	q := env.subscribeFeedback(t, "test-feedback-sub")

	taskID := idgen.New()
	sessID := idgen.New()
	comment := "broker test"
	rec := env.submitFeedback(t, taskID, sessID, "down", &comment)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	fb := receiveFeedbackEvent(t, env.brk, q)

	checks := map[string]any{
		"event":           "created",
		"task_id":         taskID,
		"session_id":      sessID,
		"feedback_type":   "down",
		"feedback_text":   comment,
		"user_id":         "sam_dev_user",
		"entrypoint_type": store.EntrypointTypeWebUI,
	}
	for key, want := range checks {
		got, exists := fb[key]
		if !exists {
			t.Errorf("missing key %q in broker payload", key)
			continue
		}
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			t.Errorf("broker payload %s: expected %v, got %v", key, want, got)
		}
	}
	for _, key := range []string{"subject_id", "subject_type", "created_at", "updated_at"} {
		if v, ok := fb[key]; !ok || v == "" {
			t.Errorf("broker payload %s: expected a value, got %v", key, v)
		}
	}
}

func (env *feedbackTestEnv) subscribeFeedback(t *testing.T, name string) broker.Queue {
	t.Helper()
	q, err := env.brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.brk.Subscribe(q, a2a.FeedbackTopic(env.adapter.core.Config().Namespace)); err != nil {
		t.Fatal(err)
	}
	return q
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

func TestHandleFeedback_PublishingDisabled(t *testing.T) {
	env := setupFeedbackTest(t, false)
	q := env.subscribeFeedback(t, "test-feedback-off")

	rec := env.submitFeedback(t, idgen.New(), idgen.New(), "down", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if msg, err := env.brk.Receive(ctx, q); err == nil && msg != nil {
		t.Fatalf("published with publishing disabled: %s", msg.Payload)
	}
}

func TestHandleFeedback_DBOverrideBeatsYAML(t *testing.T) {
	env := setupFeedbackTest(t, false)
	q := env.subscribeFeedback(t, "test-feedback-override")

	on := true
	reg := webuisettings.NewRegistry(func(context.Context) (*webuisettings.Settings, error) {
		return &webuisettings.Settings{PublishFeedback: &on}, nil
	}, nil)
	if _, err := reg.Get(t.Context()); err != nil {
		t.Fatalf("prime the registry: %v", err)
	}
	env.adapter.core.SetWebuiSettingsRegistry(reg)

	if rec := env.submitFeedback(t, idgen.New(), idgen.New(), "up", nil); rec.Code != http.StatusOK {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}
	if got := receiveFeedbackEvent(t, env.brk, q)["event"]; got != "created" {
		t.Errorf("event = %v, want created", got)
	}
}

func TestHandleFeedback_CacheMissFallsBackToYAML(t *testing.T) {
	env := setupFeedbackTest(t, true)
	q := env.subscribeFeedback(t, "test-feedback-miss")

	env.adapter.core.SetWebuiSettingsRegistry(webuisettings.NewRegistry(nil, nil))

	if rec := env.submitFeedback(t, idgen.New(), idgen.New(), "down", nil); rec.Code != http.StatusOK {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}
	if got := receiveFeedbackEvent(t, env.brk, q)["event"]; got != "created" {
		t.Errorf("event = %v, want created", got)
	}
}

func (env *feedbackTestEnv) submitShareFeedback(t *testing.T, taskID, sessID string, share bool) *httptest.ResponseRecorder {
	t.Helper()
	env.createSessionIfValid(t, sessID)
	data, _ := json.Marshal(map[string]any{
		"taskId": taskID, "sessionId": sessID, "feedbackType": "up", "shareTaskContent": share,
	})
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/feedback", bytes.NewReader(data)))
	return rec
}

func (env *feedbackTestEnv) seedTask(t *testing.T, id, owner string) {
	t.Helper()
	agent := "OrchestratorAgent"
	handlerType := store.TaskHandlerTypeAgent
	if err := store.NewTaskRepo(env.adapter.core.Store()).Create(context.Background(), &store.Task{
		ID: id, UserID: owner, StartTime: 1_700_000_000_000, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		AgentName: &agent, HandlerName: &agent, HandlerType: &handlerType,
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func TestHandleFeedback_IgnoresShareConsent(t *testing.T) {
	env := setupFeedbackTest(t, false)
	sessID := idgen.New()
	repo := store.NewFeedbackRepo(env.adapter.core.Store())

	own := idgen.New()
	env.seedTask(t, own, "sam_dev_user")
	if rec := env.submitShareFeedback(t, own, sessID, true); rec.Code != http.StatusOK {
		t.Fatalf("owner submit: status %d, body %s", rec.Code, rec.Body.String())
	}
	rows, err := repo.Search(context.Background(), "*", 0, 0, own, "", "", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("search: %v rows=%d", err, len(rows))
	}
	if rows[0].ShareContent {
		t.Error("share_content persisted true; the request field must not reach the row")
	}

	other := idgen.New()
	env.seedTask(t, other, "someone-else")
	if rec := env.submitShareFeedback(t, other, sessID, true); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner submit: status %d, want 404", rec.Code)
	}
	if otherRows, searchErr := repo.Search(context.Background(), "*", 0, 0, other, "", "", 10, 0); searchErr != nil || len(otherRows) != 0 {
		t.Fatalf("search other: %v rows=%d, want none", searchErr, len(otherRows))
	}

	if rec := env.submitShareFeedback(t, idgen.New(), sessID, true); rec.Code != http.StatusNotFound {
		t.Errorf("missing task: status %d, want 404", rec.Code)
	}
}

func TestHandleFeedback_SubjectAttribution(t *testing.T) {
	env := setupFeedbackTest(t, false)
	sessID := idgen.New()
	repo := store.NewFeedbackRepo(env.adapter.core.Store())

	agentTask := idgen.New()
	env.createTask(t, agentTask, "OrchestratorAgent")
	if rec := env.submitFeedbackRaw(t, agentTask, sessID, "up", nil); rec.Code != http.StatusOK {
		t.Fatalf("agent submit: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := repo.Search(context.Background(), "*", 0, 0, agentTask, "", "", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("agent rows: %v n=%d", err, len(rows))
	}
	if rows[0].SubjectID != "OrchestratorAgent" || rows[0].SubjectType != store.SubjectTypeAgent {
		t.Errorf("agent subject = (%q, %q), want (OrchestratorAgent, agent)", rows[0].SubjectID, rows[0].SubjectType)
	}

	wfTask := idgen.New()
	env.createTask(t, wfTask, "workflow_019e47f4_1234_7abc_8def_0123456789ab")
	if rec := env.submitFeedbackRaw(t, wfTask, sessID, "down", nil); rec.Code != http.StatusOK {
		t.Fatalf("workflow submit: %d %s", rec.Code, rec.Body.String())
	}
	rows, err = repo.Search(context.Background(), "*", 0, 0, wfTask, "", "", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("workflow rows: %v n=%d", err, len(rows))
	}
	if rows[0].SubjectID != "019e47f4-1234-7abc-8def-0123456789ab" || rows[0].SubjectType != store.SubjectTypeWorkflow {
		t.Errorf("workflow subject = (%q, %q), want dashed uuid + workflow", rows[0].SubjectID, rows[0].SubjectType)
	}

	missing := idgen.New()
	if rec := env.submitFeedbackRaw(t, missing, sessID, "up", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing task: %d, want 404", rec.Code)
	}

	bare := idgen.New()
	env.createTask(t, bare, "")
	if rec := env.submitFeedbackRaw(t, bare, sessID, "up", nil); rec.Code != http.StatusNotFound {
		t.Errorf("null agent_name: %d, want 404", rec.Code)
	}
	for _, id := range []string{missing, bare} {
		if rows, _ := repo.Search(context.Background(), "*", 0, 0, id, "", "", 10, 0); len(rows) != 0 {
			t.Errorf("task %s: %d rows persisted, want 0", id, len(rows))
		}
	}
}

type feedbackProbeBroker struct {
	broker.Broker
	entered chan struct{}
	err     error
	block   bool
}

func (b *feedbackProbeBroker) PublishGuaranteed(ctx context.Context, _ string, _ *broker.Message) error {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	if !b.block {
		return b.err
	}
	<-ctx.Done()
	return ctx.Err()
}

func (env *feedbackTestEnv) swapBroker(t *testing.T, b broker.Broker) {
	t.Helper()
	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load community flags: %v", err)
	}
	rt := runtime.NewRuntime(b, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(flagRegistry),
	)
	env.adapter.core.SetServices(rt.Services())
}

func (env *feedbackTestEnv) shortenPublishTimeout(d time.Duration) time.Duration {
	env.handler.publishTimeout = d
	return d
}

func TestHandleFeedback_WedgedBrokerDoesNotParkTheRater(t *testing.T) {
	env := setupFeedbackTest(t, true)
	bound := env.shortenPublishTimeout(200 * time.Millisecond)
	env.swapBroker(t, &feedbackProbeBroker{Broker: env.brk, block: true, entered: make(chan struct{}, 1)})

	start := time.Now()
	rec := env.submitFeedback(t, idgen.New(), idgen.New(), "up", nil)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}
	if elapsed < bound {
		t.Errorf("returned in %v, before the %v bound: the publish is not being waited on", elapsed, bound)
	}
	if elapsed > bound+2*time.Second {
		t.Errorf("returned in %v, past the %v bound: a wedged broker parks the rater", elapsed, bound)
	}
}

func TestHandleFeedback_PublishOutlivesRequestCancel(t *testing.T) {
	env := setupFeedbackTest(t, true)
	bound := env.shortenPublishTimeout(time.Second)

	probe := &feedbackProbeBroker{Broker: env.brk, block: true, entered: make(chan struct{}, 1)}
	env.swapBroker(t, probe)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()

	go func() {
		<-probe.entered
		cancelReq()
	}()

	taskID := idgen.New()
	env.createTask(t, taskID, "OrchestratorAgent")
	sessionID := idgen.New()
	env.createSession(t, sessionID, feedbackTestUser)
	data, err := json.Marshal(map[string]any{
		"taskId":       taskID,
		"sessionId":    sessionID,
		"feedbackType": "up",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/feedback", bytes.NewReader(data)).WithContext(reqCtx)

	start := time.Now()
	env.mux.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}
	if elapsed < bound {
		t.Errorf("publish gave up after %v, before its own %v deadline: it is following the request context", elapsed, bound)
	}
}

func TestHandleFeedback_PublishErrorStillReturns200(t *testing.T) {
	env := setupFeedbackTest(t, true)

	var logs bytes.Buffer
	env.handler.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	env.swapBroker(t, &feedbackProbeBroker{
		Broker:  env.brk,
		entered: make(chan struct{}, 1),
		err:     errors.New("broker unavailable"),
	})

	if rec := env.submitFeedback(t, idgen.New(), idgen.New(), "down", nil); rec.Code != http.StatusOK {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "publish feedback event error") {
		t.Errorf("expected a WARN naming the failed publish, got: %s", logs.String())
	}
}

func (env *feedbackTestEnv) setFeedbackPublishing(level string, capBytes int) {
	raw := map[string]any{"enabled": true, "include_task_info": level}
	if capBytes > 0 {
		raw["max_payload_size_bytes"] = capBytes
	}
	env.adapter.core.Config().FeedbackPublishingRaw = raw
}

func receiveFullFeedbackPayload(t *testing.T, brk broker.Broker, q broker.Queue) map[string]any {
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
	return payload
}

func (env *feedbackTestEnv) seedTaskEvent(t *testing.T, taskID, topic, direction string, createdTime int64, payloadJSON string) {
	t.Helper()
	if err := store.NewTaskEventRepo(env.adapter.core.Store()).Create(context.Background(), &store.TaskEvent{
		ID:          idgen.New(),
		TaskID:      taskID,
		CreatedTime: createdTime,
		Topic:       topic,
		Direction:   direction,
		Payload:     payloadJSON,
	}); err != nil {
		t.Fatalf("seed task event: %v", err)
	}
}

func TestHandleFeedback_IncludeTaskInfoNone_MatchesExisting(t *testing.T) {
	env := setupFeedbackTest(t, true)
	env.setFeedbackPublishing(webuisettings.FeedbackTaskInfoLevelNone, 0)
	q := env.subscribeFeedback(t, "test-none")

	rec := env.submitShareFeedback(t, mustSeedOwnTask(t, env), idgen.New(), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}

	payload := receiveFullFeedbackPayload(t, env.brk, q)
	for _, forbidden := range []string{"task_summary", "task_stim_data", "truncation_details"} {
		if _, ok := payload[forbidden]; ok {
			t.Errorf("payload carries %q at level=none: %v", forbidden, payload[forbidden])
		}
	}
	if _, ok := payload["feedback"].(map[string]any); !ok {
		t.Errorf("payload missing feedback object: %v", payload)
	}
}

func TestHandleFeedback_NeverAttachesTaskSummary(t *testing.T) {
	env := setupFeedbackTest(t, true)
	env.setFeedbackPublishing(webuisettings.FeedbackTaskInfoLevelSummary, 0)
	q := env.subscribeFeedback(t, "test-summary-no-enrichment")

	rec := env.submitShareFeedback(t, mustSeedOwnTask(t, env), idgen.New(), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}

	payload := receiveFullFeedbackPayload(t, env.brk, q)
	if _, ok := payload["task_summary"]; ok {
		t.Errorf("task_summary present on web UI feedback: %v", payload["task_summary"])
	}
}

func TestHandleFeedback_NeverAttachesTaskStimData(t *testing.T) {
	env := setupFeedbackTest(t, true)
	env.setFeedbackPublishing(webuisettings.FeedbackTaskInfoLevelStim, 0)
	q := env.subscribeFeedback(t, "test-stim-no-enrichment")

	taskID := mustSeedOwnTask(t, env)
	env.seedTaskEvent(t, taskID, "sam/agent/request/x", "request", 100, `{"params":{"message":{"parts":[{"kind":"text","text":"hi"}]}}}`)

	rec := env.submitShareFeedback(t, taskID, idgen.New(), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
	}

	payload := receiveFullFeedbackPayload(t, env.brk, q)
	if _, ok := payload["task_stim_data"]; ok {
		t.Errorf("task_stim_data present on web UI feedback: %v", payload["task_stim_data"])
	}
	if _, ok := payload["task_summary"]; ok {
		t.Errorf("task_summary present on web UI feedback: %v", payload["task_summary"])
	}
}

func mustSeedOwnTask(t *testing.T, env *feedbackTestEnv) string {
	t.Helper()
	id := idgen.New()
	env.seedTask(t, id, "sam_dev_user")
	return id
}

func newFeedbackTestEnv(t *testing.T) *feedbackTestEnv {
	t.Helper()
	env := setupFeedbackTest(t, false)
	env.mux.HandleFunc("GET /api/v1/feedback/all", env.handler.handleListCrossUser)
	return env
}

func seedCrossUserRows(t *testing.T, env *feedbackTestEnv) {
	t.Helper()
	repo := env.handler.feedbackRepo
	rows := []store.FeedbackSubmission{
		{
			TaskID: idgen.New(), UserID: "user-a", SessionID: idgen.New(), Rating: "up",
			SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI,
		},
		{
			TaskID: idgen.New(), UserID: "user-b", SessionID: idgen.New(), Rating: "down",
			SubjectID: "workflow-1", SubjectType: store.SubjectTypeWorkflow, EntrypointType: store.EntrypointTypeWebUI,
		},
	}
	for _, row := range rows {
		if _, err := repo.Submit(context.Background(), row); err != nil {
			t.Fatalf("seed cross-user feedback: %v", err)
		}
	}
}

func (env *feedbackTestEnv) registerCard(t *testing.T, name, displayName string) {
	t.Helper()
	env.adapter.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: name,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": displayName},
			}},
		},
	})
}

func seedFeedbackRow(t *testing.T, env *feedbackTestEnv, subjectType, subjectID string) {
	t.Helper()
	sub := store.FeedbackSubmission{
		TaskID: idgen.New(), UserID: "user-a", SessionID: idgen.New(), Rating: "up",
		SubjectID: subjectID, SubjectType: subjectType, EntrypointType: store.EntrypointTypeWebUI,
	}
	if _, err := env.handler.feedbackRepo.Submit(context.Background(), sub); err != nil {
		t.Fatalf("seed feedback row: %v", err)
	}
}

func scopedRequest(t *testing.T, query string, scopes ...string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/feedback/all"+query, nil)
	ident := auth.NewTestSubject(auth.SubjectFields{UserID: "auditor", Scopes: scopes})
	return req.WithContext(auth.ContextWithSubject(req.Context(), ident))
}

func TestHandleListCrossUser_Authorization(t *testing.T) {
	tests := []struct {
		name       string
		scopes     []string
		wantStatus int
	}{
		{"agent wildcard admitted", []string{rbac.ScopeAgentReadFeedbackAny}, http.StatusOK},
		{"workflow wildcard admitted", []string{rbac.ScopeWorkflowReadFeedbackAny}, http.StatusOK},
		{"per-subject agent grant admitted", []string{rbac.AgentReadFeedbackScope("agent-1")}, http.StatusOK},
		{"trace grant alone rejected", []string{rbac.ScopeAgentReadTraceAny}, http.StatusForbidden},
		{"activity read alone rejected", []string{rbac.ScopeActivityRead}, http.StatusForbidden},
		{"analytics read alone rejected", []string{rbac.ScopeAnalyticsRead}, http.StatusForbidden},
		{"no scopes rejected", nil, http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFeedbackTestEnv(t)
			rec := httptest.NewRecorder()
			env.mux.ServeHTTP(rec, scopedRequest(t, "", tc.scopes...))

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestHandleListCrossUser_NoSelfScopeFallback(t *testing.T) {
	env := newFeedbackTestEnv(t)
	seedCrossUserRows(t, env)

	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, scopedRequest(t, ""))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "\"data\"") {
		t.Error("denied response leaked a data payload")
	}
}

func TestHandleListCrossUser_RestrictsRowsToReach(t *testing.T) {
	env := newFeedbackTestEnv(t)
	seedCrossUserRows(t, env)

	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, scopedRequest(t, "", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[CrossUserFeedbackResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("want exactly the seeded agent row, got %d rows", len(resp.Data))
	}
	for _, row := range resp.Data {
		if row.SubjectType != store.SubjectTypeAgent {
			t.Errorf("agent-wildcard caller saw a %q row (subject %s)", row.SubjectType, row.SubjectID)
		}
	}
}

func TestHandleListCrossUser_EmptyDataIsArrayNotNull(t *testing.T) {
	env := newFeedbackTestEnv(t)

	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, scopedRequest(t, "", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("empty result did not serialize data as []: %s", rec.Body.String())
	}
}

func TestHandleListCrossUser_RejectsStatusParam(t *testing.T) {
	env := newFeedbackTestEnv(t)

	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, scopedRequest(t, "?status=open", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unsupported status filter", rec.Code)
	}
}

func TestHandleListCrossUser_OverLongIDListIsBadRequest(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"agentIds", "agentIds"},
		{"workflowIds", "workflowIds"},
		{"userIds", "userIds"},
	}

	ids := make([]string, dbutil.InClauseCap+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("agent-%d", i)
	}
	overLong := strings.Join(ids, ",")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFeedbackTestEnv(t)

			rec := httptest.NewRecorder()
			env.mux.ServeHTTP(rec, scopedRequest(t, "?"+tc.query+"="+overLong, rbac.ScopeAgentReadFeedbackAny))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.query) {
				t.Errorf("400 body does not name the offending parameter %q: %s", tc.query, rec.Body.String())
			}
		})
	}
}

func TestHandleListCrossUser_ResolvesSubjectName(t *testing.T) {
	tests := []struct {
		name        string
		subjectType string
		cardName    string
		scope       string
	}{
		{"agent", store.SubjectTypeAgent, "agent_11111111_2222_3333_4444_555555555555", rbac.ScopeAgentReadFeedbackAny},
		{"workflow", store.SubjectTypeWorkflow, "workflow_aaaaaaaa_bbbb_cccc_dddd_eeeeeeeeeeee", rbac.ScopeWorkflowReadFeedbackAny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFeedbackTestEnv(t)
			env.registerCard(t, tc.cardName, "Invoice Approval")
			subjectID, _, ok := a2a.IDFromInstanceName(tc.cardName)
			if !ok {
				t.Fatalf("test fixture card name %q does not decode to a UUID", tc.cardName)
			}
			seedFeedbackRow(t, env, tc.subjectType, subjectID)

			rec := httptest.NewRecorder()
			env.mux.ServeHTTP(rec, scopedRequest(t, "", tc.scope))

			var resp PaginatedResponse[CrossUserFeedbackResponse]
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Data) != 1 {
				t.Fatalf("got %d rows, want 1", len(resp.Data))
			}
			if got := resp.Data[0].SubjectName; got != "Invoice Approval" {
				t.Errorf("subjectName = %q, want the registry display name", got)
			}
		})
	}
}

func TestHandleListCrossUser_UnresolvableSubjectNameIsEmptyNotAnError(t *testing.T) {
	env := newFeedbackTestEnv(t)
	seedFeedbackRow(t, env, store.SubjectTypeAgent, "99999999-0000-0000-0000-000000000000")

	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, scopedRequest(t, "", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp PaginatedResponse[CrossUserFeedbackResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("got %d rows, want 1 — an unresolvable name must not drop the row", len(resp.Data))
	}
	if resp.Data[0].SubjectName != "" {
		t.Errorf("subjectName = %q, want empty", resp.Data[0].SubjectName)
	}
}

func TestHandleListCrossUser_AuditsSuccessfulRead(t *testing.T) {
	env := newFeedbackTestEnv(t)
	seedCrossUserRows(t, env)

	var logs bytes.Buffer
	env.handler.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, scopedRequest(t, "", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	out := logs.String()
	if !strings.Contains(out, msgCrossUserFeedbackRead) {
		t.Fatalf("expected an INFO line %q, got: %s", msgCrossUserFeedbackRead, out)
	}
	for _, want := range []string{
		"callerUser=auditor",
		"crossUser=true",
		"agentReachAll=true",
		"rowCount=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("audit line missing %q: %s", want, out)
		}
	}
}

func TestHandleListCrossUser_AuditsRefusal(t *testing.T) {
	tests := []struct {
		name           string
		withIdentity   bool
		scopes         []string
		wantIdentityKV string
	}{
		{"no identity at all", false, nil, "hasIdentity=false"},
		{"identity present but no feedback reach", true, []string{rbac.ScopeActivityRead}, "hasIdentity=true"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFeedbackTestEnv(t)

			var logs bytes.Buffer
			env.handler.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

			var req *http.Request
			if tc.withIdentity {
				req = scopedRequest(t, "", tc.scopes...)
			} else {
				req = httptest.NewRequest("GET", "/api/v1/feedback/all", nil)
			}

			rec := httptest.NewRecorder()
			env.mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}

			out := logs.String()
			if !strings.Contains(out, msgFeedbackReadDenied) {
				t.Fatalf("expected a WARN line %q, got: %s", msgFeedbackReadDenied, out)
			}
			if !strings.Contains(out, tc.wantIdentityKV) {
				t.Errorf("refusal line missing %q: %s", tc.wantIdentityKV, out)
			}
		})
	}
}

func TestHandleListCrossUser_NeverLogsCommentText(t *testing.T) {
	env := newFeedbackTestEnv(t)

	const distinctiveComment = "zz-distinctive-comment-marker-152373-do-not-log-this"
	comment := distinctiveComment
	sub := store.FeedbackSubmission{
		TaskID: idgen.New(), UserID: "user-a", SessionID: idgen.New(), Rating: "down",
		Comment:        &comment,
		SubjectID:      "agent-1",
		SubjectType:    store.SubjectTypeAgent,
		EntrypointType: store.EntrypointTypeWebUI,
	}
	if _, err := env.handler.feedbackRepo.Submit(context.Background(), sub); err != nil {
		t.Fatalf("seed feedback row: %v", err)
	}

	var logs bytes.Buffer
	env.handler.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, scopedRequest(t, "", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[CrossUserFeedbackResponse]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Comment == nil || *resp.Data[0].Comment != distinctiveComment {
		t.Fatalf("test fixture broken: response does not carry the seeded comment: %+v", resp.Data)
	}

	if strings.Contains(logs.String(), distinctiveComment) {
		t.Fatalf("comment text leaked into the logs: %s", logs.String())
	}
}

func TestFeedbackAuditHelpers_NilLoggerSafe(t *testing.T) {
	auditCrossUserFeedbackRead(nil, "caller", rbac.FeedbackReach{}, 0)
	warnFeedbackReadDenied(nil, "caller", true)
	warnFeedbackReadDenied(nil, "", false)
}

func TestHandleListCrossUser_UserIdsFilter(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantRows int
		wantUser string
	}{
		{"narrows to the named submitter", "?userIds=user-a", 1, "user-a"},
		{"a submitter outside the reach yields nothing", "?userIds=user-b", 0, ""},
		{"an unknown submitter yields nothing", "?userIds=nobody", 0, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFeedbackTestEnv(t)
			seedCrossUserRows(t, env)

			rec := httptest.NewRecorder()
			env.mux.ServeHTTP(rec, scopedRequest(t, tc.query, rbac.ScopeAgentReadFeedbackAny))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			var resp PaginatedResponse[CrossUserFeedbackResponse]
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Data) != tc.wantRows {
				t.Fatalf("rows = %d, want %d (body %s)", len(resp.Data), tc.wantRows, rec.Body.String())
			}
			if tc.wantUser != "" && resp.Data[0].UserID != tc.wantUser {
				t.Errorf("userId = %q, want %q", resp.Data[0].UserID, tc.wantUser)
			}
		})
	}
}
