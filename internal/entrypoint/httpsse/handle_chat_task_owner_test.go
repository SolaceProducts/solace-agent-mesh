// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	aliceBubbles = `[{"text":"alice-original-answer"}]`
	aliceMessage = "alice-original-question"
	bobBubbles   = `[{"text":"bob-injected-answer"}]`
	bobMessage   = "bob-injected-question"
)

type chatTaskOwnerFixture struct {
	core     *entrypoint.GatewayCore
	mux      *http.ServeMux
	sessRepo *store.SessionRepo
	chatRepo *store.ChatTaskRepo
	sseRepo  *store.SSEEventRepo
	taskRepo *store.TaskRepo
	logs     *safeBuffer

	aliceSession  string
	aliceSession2 string
	bobSession    string
	aliceTask     string
}

func newChatTaskOwnerFixture(t *testing.T) *chatTaskOwnerFixture {
	t.Helper()
	adapter, _, sessRepo, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	logs := &safeBuffer{}
	f := &chatTaskOwnerFixture{
		core:     adapter.core,
		sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logs: logs,
		taskRepo:     store.NewTaskRepo(adapter.core.Store()),
		aliceSession: idgen.New(), aliceSession2: idgen.New(), bobSession: idgen.New(), aliceTask: idgen.New(),
	}
	ctx := t.Context()
	for id, owner := range map[string]string{f.aliceSession: createOwnerAlice, f.aliceSession2: createOwnerAlice, f.bobSession: createOwnerBob} {
		if err := sessRepo.Create(ctx, &store.Session{ID: id, UserID: owner}); err != nil {
			t.Fatal(err)
		}
	}
	aliceMsg := aliceMessage
	if err := chatRepo.Create(ctx, &store.ChatTask{
		ID: f.aliceTask, SessionID: f.aliceSession, UserID: createOwnerAlice,
		UserMessage: &aliceMsg, MessageBubbles: aliceBubbles,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: f.aliceTask, SessionID: f.aliceSession, UserID: createOwnerAlice,
		EventSequence: 1, EventType: "final_response", EventData: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	chatH := &chatTaskHandler{
		core: adapter.core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo,
		logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)
	f.mux.HandleFunc("GET /api/v1/sessions/{id}/chatTasks", chatH.handleList)
	return f
}

func (f *chatTaskOwnerFixture) save(userID, sessionID, taskID, bubbles, message string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"id":%q,"messageBubbles":%q,"userMessage":%q}`, taskID, bubbles, message)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sessionID+"/chatTasks", strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, asUser(req, userID))
	return rec
}

func (f *chatTaskOwnerFixture) requireAliceTaskUntouched(t *testing.T) {
	t.Helper()
	got, err := f.chatRepo.GetByID(t.Context(), f.aliceTask)
	if err != nil || got == nil {
		t.Fatalf("alice's task: %+v, %v", got, err)
	}
	if got.UserID != createOwnerAlice || got.SessionID != f.aliceSession ||
		got.MessageBubbles != aliceBubbles || got.UserMessage == nil || *got.UserMessage != aliceMessage || got.UpdatedTime != nil {
		t.Errorf("alice's task changed: %+v", got)
	}
	unconsumed, err := f.sseRepo.GetUnconsumedBySession(t.Context(), f.aliceSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(unconsumed[f.aliceTask]) != 1 {
		t.Errorf("alice's buffered events were consumed: %v", unconsumed)
	}
}

func TestSaveChatTask_ByOwnership(t *testing.T) {
	cases := []struct {
		name      string
		user      string
		session   func(*chatTaskOwnerFixture) string
		wantCode  int
		untouched bool
	}{
		{"another user's task id in the caller's own session", createOwnerBob, func(f *chatTaskOwnerFixture) string { return f.bobSession }, http.StatusNotFound, true},
		{"the owner's own task id in another of the owner's sessions", createOwnerAlice, func(f *chatTaskOwnerFixture) string { return f.aliceSession2 }, http.StatusNotFound, true},
		{"the owner re-saves their own task", createOwnerAlice, func(f *chatTaskOwnerFixture) string { return f.aliceSession }, http.StatusOK, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newChatTaskOwnerFixture(t)
			rec := f.save(tc.user, tc.session(f), f.aliceTask, bobBubbles, bobMessage)

			if rec.Code != tc.wantCode {
				t.Fatalf("status %d, want %d; body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.untouched {
				f.requireAliceTaskUntouched(t)
				if !strings.Contains(rec.Body.String(), taskNotFoundMsg) {
					t.Errorf("body %q lacks %q", rec.Body.String(), taskNotFoundMsg)
				}
				for _, leaked := range []string{createOwnerAlice, f.aliceSession, aliceMessage, aliceBubbles} {
					if strings.Contains(rec.Body.String(), leaked) {
						t.Errorf("refusal body carries %q: %s", leaked, rec.Body.String())
					}
				}
				return
			}
			got, err := f.chatRepo.GetByID(t.Context(), f.aliceTask)
			if err != nil || got == nil || got.MessageBubbles != bobBubbles || got.UpdatedTime == nil {
				t.Errorf("owner's re-save did not update the row: %+v, %v", got, err)
			}
			unconsumed, err := f.sseRepo.GetUnconsumedBySession(t.Context(), f.aliceSession)
			if err != nil || len(unconsumed[f.aliceTask]) != 0 {
				t.Errorf("owner's re-save left the events unconsumed: %v, %v", unconsumed, err)
			}
		})
	}
}

func TestSaveChatTask_NewIDInTheCallersSessionIsServed(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	taskID := idgen.New()

	rec := f.save(createOwnerBob, f.bobSession, taskID, bobBubbles, bobMessage)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["id"] != taskID || resp["sessionId"] != f.bobSession {
		t.Errorf("response = %v", resp)
	}
	f.requireAliceTaskUntouched(t)
}

func TestSaveChatTask_NeverConsumesAnotherSessionsEvents(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	streaming := idgen.New()
	if err := f.sseRepo.Append(t.Context(), &store.SSEEventBufferRow{
		TaskID: streaming, SessionID: f.aliceSession, UserID: createOwnerAlice,
		EventSequence: 1, EventType: "status_update", EventData: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	f.save(createOwnerBob, f.bobSession, streaming, bobBubbles, bobMessage)

	unconsumed, err := f.sseRepo.GetUnconsumedBySession(t.Context(), f.aliceSession)
	if err != nil || len(unconsumed[streaming]) != 1 {
		t.Errorf("alice's streaming task events after bob's save: %v, %v; want still unconsumed", unconsumed, err)
	}
}

func TestSaveChatTask_PendingSaveConsumesNoEvents(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	save := func(metadata string) {
		t.Helper()
		body := fmt.Sprintf(`{"id":%q,"messageBubbles":%q,"taskMetadata":%q}`, f.aliceTask, aliceBubbles, metadata)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+f.aliceSession+"/chatTasks", strings.NewReader(body))
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, asUser(req, createOwnerAlice))
		if rec.Code != http.StatusOK {
			t.Fatalf("save with metadata %s: status %d; body=%s", metadata, rec.Code, rec.Body.String())
		}
	}
	unconsumed := func() int {
		t.Helper()
		events, err := f.sseRepo.GetUnconsumedBySession(t.Context(), f.aliceSession)
		if err != nil {
			t.Fatal(err)
		}
		return len(events[f.aliceTask])
	}

	save(`{"schemaVersion":1,"status":"pending"}`)
	if n := unconsumed(); n != 1 {
		t.Fatalf("after the pending save %d events unconsumed, want 1", n)
	}
	save(`{"schemaVersion":1,"status":"completed"}`)
	if n := unconsumed(); n != 0 {
		t.Errorf("after the completed save %d events unconsumed, want 0", n)
	}
}

func TestSaveChatTask_UnparsableMetadataConsumesEvents(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	body := fmt.Sprintf(`{"id":%q,"messageBubbles":%q,"taskMetadata":"not json"}`, f.aliceTask, aliceBubbles)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+f.aliceSession+"/chatTasks", strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, asUser(req, createOwnerAlice))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d; body=%s", rec.Code, rec.Body.String())
	}
	events, err := f.sseRepo.GetUnconsumedBySession(t.Context(), f.aliceSession)
	if err != nil || len(events[f.aliceTask]) != 0 {
		t.Errorf("after a save with unparsable metadata: %v, %v; want every event consumed", events, err)
	}
}

func TestSaveChatTask_NonCanonicalTaskIDIsRefused(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	live := idgen.New()
	if err := f.core.Tasks().Register(live, "Agent", createOwnerAlice, entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}

	rec := f.save(createOwnerBob, f.bobSession, strings.ToUpper(live), bobBubbles, bobMessage)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("upper-case spelling of a live task id: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestSaveChatTask_TaskRecordOfAnotherUserRefusesTheClaim(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	streaming := idgen.New()
	if err := f.taskRepo.Create(t.Context(), &store.Task{ID: streaming, UserID: createOwnerAlice, StartTime: 1, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	rec := f.save(createOwnerBob, f.bobSession, streaming, bobBubbles, bobMessage)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), taskNotFoundMsg) {
		t.Fatalf("bob claims alice's task id: %d %s, want 404 %q", rec.Code, rec.Body.String(), taskNotFoundMsg)
	}
	if tasks, err := f.chatRepo.ListBySession(t.Context(), f.bobSession); err != nil || len(tasks) != 0 {
		t.Errorf("bob's session holds %v, %v after the refusal", tasks, err)
	}
	if !strings.Contains(f.logs.String(), denyTaskOwnedByAnotherUser) {
		t.Errorf("refusal not logged with reason %q: %s", denyTaskOwnedByAnotherUser, f.logs.String())
	}

	if rec := f.save(createOwnerAlice, f.aliceSession, streaming, aliceBubbles, aliceMessage); rec.Code != http.StatusOK {
		t.Errorf("alice saves her own task: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSaveChatTask_LiveTaskOfAnotherUserRefusesTheClaim(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	streaming := idgen.New()
	if err := f.core.Tasks().Register(streaming, "Agent", createOwnerAlice, entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}

	rec := f.save(createOwnerBob, f.bobSession, streaming, bobBubbles, bobMessage)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), taskNotFoundMsg) {
		t.Fatalf("bob claims alice's live task id: %d %s, want 404 %q", rec.Code, rec.Body.String(), taskNotFoundMsg)
	}
	if tasks, err := f.chatRepo.ListBySession(t.Context(), f.bobSession); err != nil || len(tasks) != 0 {
		t.Errorf("bob's session holds %v, %v after the refusal", tasks, err)
	}
	if !strings.Contains(f.logs.String(), denyTaskOwnedByAnotherUser) {
		t.Errorf("refusal not logged with reason %q: %s", denyTaskOwnedByAnotherUser, f.logs.String())
	}
	if rec := f.save(createOwnerAlice, f.aliceSession, streaming, aliceBubbles, aliceMessage); rec.Code != http.StatusOK {
		t.Errorf("alice saves her own live task: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSaveChatTask_UnattributedTaskRecordDoesNotRefuseTheOwner(t *testing.T) {
	f := newChatTaskOwnerFixture(t)
	unattributed := idgen.New()
	if err := f.taskRepo.Create(t.Context(), &store.Task{ID: unattributed, UserID: store.TaskUserUnknown, StartTime: 1, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	if rec := f.save(createOwnerAlice, f.aliceSession, unattributed, aliceBubbles, aliceMessage); rec.Code != http.StatusOK {
		t.Errorf("alice saves a task recorded without a user: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSaveChatTask_RefusalWritesNothingForTheCaller(t *testing.T) {
	f := newChatTaskOwnerFixture(t)

	f.save(createOwnerBob, f.bobSession, f.aliceTask, bobBubbles, bobMessage)

	tasks, err := f.chatRepo.ListBySession(t.Context(), f.bobSession)
	if err != nil || len(tasks) != 0 {
		t.Errorf("bob's session holds %v, %v after the refusal", tasks, err)
	}
	session, err := f.sessRepo.GetByID(t.Context(), f.bobSession)
	if err != nil || session == nil || session.HasActivity {
		t.Errorf("bob's session after the refusal: %+v, %v", session, err)
	}
}

func TestSaveChatTask_RefusalIsLoggedAtWarn(t *testing.T) {
	f := newChatTaskOwnerFixture(t)

	f.save(createOwnerBob, f.bobSession, f.aliceTask, bobBubbles, bobMessage)

	var lines []string
	for l := range strings.SplitSeq(f.logs.String(), "\n") {
		if strings.Contains(l, "task access denied") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("refusal logged %d times, want once: %s", len(lines), f.logs.String())
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatalf("decode log line %q: %v", lines[0], err)
	}
	if line["level"] != "WARN" || line["callerUser"] != createOwnerBob || line["taskID"] != f.aliceTask || line["reason"] != denyChatTaskHeldElsewhere {
		t.Errorf("refusal log = %v", line)
	}
	for _, leaked := range []string{bobMessage, bobBubbles, aliceMessage, "bob-injected", "@example.com"} {
		if strings.Contains(lines[0], leaked) {
			t.Errorf("refusal log carries %q: %s", leaked, lines[0])
		}
	}
}

func TestSaveChatTask_IDCollisionThroughTheComposedHandler(t *testing.T) {
	adapter, _, _, chatRepo, _ := setupTestAdapterWithDB(t)

	do := func(userID, method, path string, body any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SAM-User-ID", userID)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}
	aliceSession, bobSession, taskID := idgen.New(), idgen.New(), idgen.New()
	for user, id := range map[string]string{createOwnerAlice: aliceSession, createOwnerBob: bobSession} {
		if rec := do(user, http.MethodPost, "/api/v1/sessions", map[string]any{"id": id, "agentId": aliceAgent}); rec.Code != http.StatusCreated {
			t.Fatalf("%s creates a session: %d %s", user, rec.Code, rec.Body.String())
		}
	}
	save := func(user, session, bubbles string) *httptest.ResponseRecorder {
		return do(user, http.MethodPost, "/api/v1/sessions/"+session+"/chatTasks",
			map[string]any{"id": taskID, "messageBubbles": bubbles, "userMessage": "q"})
	}

	inFlight := idgen.New()
	if err := store.NewTaskRepo(adapter.core.Store()).Create(t.Context(), &store.Task{ID: inFlight, UserID: createOwnerAlice, StartTime: 1, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	claim := do(createOwnerBob, http.MethodPost, "/api/v1/sessions/"+bobSession+"/chatTasks",
		map[string]any{"id": inFlight, "messageBubbles": bobBubbles, "userMessage": "q"})
	if claim.Code != http.StatusNotFound {
		t.Fatalf("bob claims alice's in-flight task id: %d %s, want 404", claim.Code, claim.Body.String())
	}

	if rec := save(createOwnerAlice, aliceSession, aliceBubbles); rec.Code != http.StatusOK {
		t.Fatalf("alice saves her task: %d %s", rec.Code, rec.Body.String())
	}
	if rec := save(createOwnerBob, bobSession, bobBubbles); rec.Code != http.StatusNotFound {
		t.Fatalf("bob saves alice's task id into his session: %d %s, want 404", rec.Code, rec.Body.String())
	}
	if rec := save(createOwnerAlice, aliceSession, aliceBubbles); rec.Code != http.StatusOK {
		t.Fatalf("alice re-saves her task: %d %s", rec.Code, rec.Body.String())
	}

	got, err := chatRepo.GetByID(t.Context(), taskID)
	if err != nil || got == nil || got.UserID != createOwnerAlice || got.SessionID != aliceSession || got.MessageBubbles != aliceBubbles {
		t.Errorf("alice's task after bob's attempt: %+v, %v", got, err)
	}
	if rec := do(createOwnerBob, http.MethodGet, "/api/v1/sessions/"+bobSession+"/chatTasks", nil); !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("bob's session lists a task after the refusal: %s", rec.Body.String())
	}
}

func TestSaveChatTask_OwnerOfARealSubmitThroughTheComposedHandler(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: ownerTestAgent, URL: "solace:test"})

	do := func(userID, method, path string, body any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SAM-User-ID", userID)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}
	aliceSession, bobSession := idgen.New(), idgen.New()
	for user, id := range map[string]string{createOwnerAlice: aliceSession, createOwnerBob: bobSession} {
		if rec := do(user, http.MethodPost, "/api/v1/sessions", map[string]any{"id": id, "agentId": aliceAgent}); rec.Code != http.StatusCreated {
			t.Fatalf("%s creates a session: %d %s", user, rec.Code, rec.Body.String())
		}
	}
	submit := do(createOwnerAlice, http.MethodPost, "/api/v1/message:send", map[string]any{
		"jsonrpc": "2.0", "id": "req1",
		"params": map[string]any{"message": map[string]any{
			"kind": "message", "role": "user", "contextId": aliceSession,
			"parts":    []map[string]any{{"kind": "text", "text": ownerTestPrompt}},
			"metadata": map[string]any{"agent_name": ownerTestAgent},
		}},
	})
	if submit.Code != http.StatusOK {
		t.Fatalf("alice submits: %d %s", submit.Code, submit.Body.String())
	}
	taskID := decodeTaskResult(t, submit).ID

	save := func(user, session string) *httptest.ResponseRecorder {
		return do(user, http.MethodPost, "/api/v1/sessions/"+session+"/chatTasks",
			map[string]any{"id": taskID, "messageBubbles": aliceBubbles, "userMessage": "q"})
	}
	if rec := save(createOwnerBob, bobSession); rec.Code != http.StatusNotFound {
		t.Errorf("bob claims alice's submitted task id: %d %s, want 404", rec.Code, rec.Body.String())
	}
	if rec := save(createOwnerAlice, aliceSession); rec.Code != http.StatusOK {
		t.Errorf("alice saves her submitted task: %d %s", rec.Code, rec.Body.String())
	}
}
