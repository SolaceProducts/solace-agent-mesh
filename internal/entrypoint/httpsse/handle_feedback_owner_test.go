// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	ratingSecretText       = "bob-private-feedback-comment"
	taskAccessDeniedLogMsg = "task access denied"
)

type ownerFeedbackFixture struct {
	env          *feedbackTestEnv
	q            broker.Queue
	logs         *safeBuffer
	aliceTask    string
	aliceSession string
	bobTask      string
	bobSession   string
}

func newOwnerFeedbackFixture(t *testing.T) *ownerFeedbackFixture {
	t.Helper()
	env := setupFeedbackTest(t, true)
	logs := &safeBuffer{}
	env.handler.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f := &ownerFeedbackFixture{
		env:          env,
		q:            env.subscribeFeedback(t, "owner-feedback-"+idgen.New()),
		logs:         logs,
		aliceTask:    idgen.New(),
		aliceSession: idgen.New(),
		bobTask:      idgen.New(),
		bobSession:   idgen.New(),
	}
	env.seedTask(t, f.aliceTask, createOwnerAlice)
	env.seedTask(t, f.bobTask, createOwnerBob)
	env.createSession(t, f.aliceSession, createOwnerAlice)
	env.createSession(t, f.bobSession, createOwnerBob)
	return f
}

func (f *ownerFeedbackFixture) rate(t *testing.T, identity *auth.Subject, taskID, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"taskId": taskID, "sessionId": sessionID, "feedbackType": "down", "feedbackText": ratingSecretText,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", bytes.NewReader(raw))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))
	rec := httptest.NewRecorder()
	f.env.mux.ServeHTTP(rec, req)
	return rec
}

func (f *ownerFeedbackFixture) rateAs(t *testing.T, userID, taskID, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	return f.rate(t, auth.NewTestSubject(auth.SubjectFields{UserID: userID, Email: userID + "@example.com"}), taskID, sessionID)
}

func (f *ownerFeedbackFixture) storedFor(t *testing.T, taskID string) []*store.Feedback {
	t.Helper()
	rows, err := store.NewFeedbackRepo(f.env.adapter.core.Store()).Search(context.Background(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search feedback: %v", err)
	}
	return rows
}

func errorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Message
}

func TestHandleFeedback_Submit_ByOwnership(t *testing.T) {
	f := newOwnerFeedbackFixture(t)
	deletedSession := idgen.New()
	f.env.createSession(t, deletedSession, createOwnerBob)
	if _, err := f.env.handler.sessRepo.SoftDelete(t.Context(), deletedSession, createOwnerBob); err != nil {
		t.Fatalf("soft-delete session: %v", err)
	}
	unknownSession := idgen.New()
	unattributedTask, unattributedSession := idgen.New(), idgen.New()
	f.env.seedTask(t, unattributedTask, "unknown")
	f.env.createSession(t, unattributedSession, "")

	cases := []struct {
		name        string
		caller      string
		task        string
		session     string
		wantStatus  int
		wantMessage string
	}{
		{"owner rates own task in own session", createOwnerBob, f.bobTask, f.bobSession, http.StatusOK, ""},
		{"non-owner rates another user's task in that user's session", createOwnerBob, f.aliceTask, f.aliceSession, http.StatusNotFound, taskNotFoundMsg},
		{"non-owner rates another user's task in their own session", createOwnerBob, f.aliceTask, f.bobSession, http.StatusNotFound, taskNotFoundMsg},
		{"owner rates own task in another user's session", createOwnerBob, f.bobTask, f.aliceSession, http.StatusNotFound, sessionNotFoundMsg},
		{"owner rates own task in an unknown session", createOwnerBob, f.bobTask, unknownSession, http.StatusNotFound, sessionNotFoundMsg},
		{"non-owner rates a task stamped with an unknown owner", createOwnerBob, unattributedTask, f.bobSession, http.StatusNotFound, taskNotFoundMsg},
		{"owner rates own task in a session with no owner", createOwnerBob, f.bobTask, unattributedSession, http.StatusNotFound, sessionNotFoundMsg},
		{"owner rates own task in their deleted session", createOwnerBob, f.bobTask, deletedSession, http.StatusNotFound, sessionNotFoundMsg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.storedFor(t, tc.task))

			rec := f.rateAs(t, tc.caller, tc.task, tc.session)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if got := len(f.storedFor(t, tc.task)); got != before+1 {
					t.Errorf("stored rows = %d, want %d", got, before+1)
				}
				if got := receiveFeedbackEvent(t, f.env.brk, f.q); got["session_id"] != tc.session || got["task_id"] != tc.task {
					t.Errorf("published event = %v, want task %s in session %s", got, tc.task, tc.session)
				}
				return
			}
			if got := errorMessage(t, rec); got != tc.wantMessage {
				t.Errorf("message = %q, want %q", got, tc.wantMessage)
			}
			if got := len(f.storedFor(t, tc.task)); got != before {
				t.Errorf("stored rows = %d, want %d: a refused rating was stored", got, before)
			}
			assertNoFeedbackPublished(t, f.env.brk, f.q)
		})
	}
}

func TestHandleFeedback_Submit_RefusalMatchesAnUnknownID(t *testing.T) {
	f := newOwnerFeedbackFixture(t)

	foreignTask := f.rateAs(t, createOwnerBob, f.aliceTask, f.bobSession)
	unknownTask := f.rateAs(t, createOwnerBob, idgen.New(), f.bobSession)
	foreignSession := f.rateAs(t, createOwnerBob, f.bobTask, f.aliceSession)
	unknownSession := f.rateAs(t, createOwnerBob, f.bobTask, idgen.New())

	for _, pair := range []struct {
		name         string
		foreign, gap *httptest.ResponseRecorder
	}{{"task", foreignTask, unknownTask}, {"session", foreignSession, unknownSession}} {
		if pair.foreign.Code != pair.gap.Code || errorMessage(t, pair.foreign) != errorMessage(t, pair.gap) {
			t.Errorf("%s: foreign answer (%d %q) differs from unknown answer (%d %q)", pair.name,
				pair.foreign.Code, errorMessage(t, pair.foreign), pair.gap.Code, errorMessage(t, pair.gap))
		}
		for _, leaked := range []string{createOwnerAlice, "userId"} {
			if strings.Contains(pair.foreign.Body.String(), leaked) {
				t.Errorf("%s: refusal body leaks %q: %s", pair.name, leaked, pair.foreign.Body.String())
			}
		}
	}
}

func TestHandleFeedback_Submit_ReadScopeDoesNotGrantRating(t *testing.T) {
	f := newOwnerFeedbackFixture(t)
	auditor := auth.NewTestSubject(auth.SubjectFields{UserID: createOwnerBob, Email: "bob@example.com", Scopes: []string{rbac.ScopeActivityRead}})

	rec := f.rate(t, auditor, f.aliceTask, f.bobSession)

	if rec.Code != http.StatusNotFound || errorMessage(t, rec) != taskNotFoundMsg {
		t.Fatalf("auditor rating another user's task: %d %s, want 404 %q", rec.Code, rec.Body.String(), taskNotFoundMsg)
	}
	if got := len(f.storedFor(t, f.aliceTask)); got != 0 {
		t.Errorf("stored rows = %d, want 0", got)
	}
}

func TestHandleFeedback_Submit_UnclassifiedForeignTaskIsRefusedAsForeign(t *testing.T) {
	f := newOwnerFeedbackFixture(t)
	unclassified := idgen.New()
	if err := f.env.handler.taskRepo.Create(t.Context(), &store.Task{
		ID: unclassified, UserID: createOwnerAlice, StartTime: 1_700_000_000_000, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	rec := f.rateAs(t, createOwnerBob, unclassified, f.bobSession)

	if rec.Code != http.StatusNotFound || errorMessage(t, rec) != taskNotFoundMsg {
		t.Fatalf("status/body = %d %s, want 404 %q", rec.Code, rec.Body.String(), taskNotFoundMsg)
	}
	if !strings.Contains(f.logs.String(), taskAccessDeniedLogMsg) {
		t.Errorf("refusal not logged under %q: %s", taskAccessDeniedLogMsg, f.logs.String())
	}
}

func TestHandleFeedback_Submit_RefusalIsLoggedAtWarn(t *testing.T) {
	cases := []struct {
		name    string
		msg     string
		userKey string
		task    func(*ownerFeedbackFixture) string
		session func(*ownerFeedbackFixture) string
		wantKey string
		wantVal func(*ownerFeedbackFixture) string
	}{
		{
			name: "task", msg: taskAccessDeniedLogMsg, userKey: "callerUser",
			task:    func(f *ownerFeedbackFixture) string { return f.aliceTask },
			session: func(f *ownerFeedbackFixture) string { return f.bobSession },
			wantKey: "taskID", wantVal: func(f *ownerFeedbackFixture) string { return f.aliceTask },
		},
		{
			name: "session", msg: msgFeedbackSessionNotOwned, userKey: "userID",
			task:    func(f *ownerFeedbackFixture) string { return f.bobTask },
			session: func(f *ownerFeedbackFixture) string { return f.aliceSession },
			wantKey: "sessionID", wantVal: func(f *ownerFeedbackFixture) string { return f.aliceSession },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOwnerFeedbackFixture(t)

			f.rateAs(t, createOwnerBob, tc.task(f), tc.session(f))

			var lines []string
			for l := range strings.SplitSeq(f.logs.String(), "\n") {
				if strings.Contains(l, tc.msg) {
					lines = append(lines, l)
				}
			}
			if len(lines) != 1 {
				t.Fatalf("refusal logged %d times under %q, want once: %s", len(lines), tc.msg, f.logs.String())
			}
			var line map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
				t.Fatalf("decode log line %q: %v", lines[0], err)
			}
			if line["level"] != "WARN" {
				t.Errorf("refusal logged at %v, want WARN", line["level"])
			}
			if line[tc.wantKey] != tc.wantVal(f) || line[tc.userKey] != createOwnerBob {
				t.Errorf("refusal log lacks ids: %v", line)
			}
			for _, leaked := range []string{ratingSecretText, "@example.com", createOwnerAlice} {
				if strings.Contains(lines[0], leaked) {
					t.Errorf("refusal log carries %q: %s", leaked, lines[0])
				}
			}
		})
	}
}

func TestHandleFeedback_Submit_SessionLookupErrorIsServerError(t *testing.T) {
	f := newOwnerFeedbackFixture(t)
	f.env.handler.sessRepo = closedSessionRepo(t)

	rec := f.rateAs(t, createOwnerBob, f.bobTask, f.bobSession)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), sessionNotFoundMsg) {
		t.Errorf("a failed lookup reads as not-found: %s", rec.Body.String())
	}
	if got := len(f.storedFor(t, f.bobTask)); got != 0 {
		t.Errorf("stored rows = %d, want 0", got)
	}
	assertNoFeedbackPublished(t, f.env.brk, f.q)
	if lines := logLinesFor(f.logs, "get session error", f.bobSession); len(lines) != 1 || !strings.Contains(lines[0], `"level":"ERROR"`) {
		t.Errorf("lookup failure logged as %v, want one ERROR line", lines)
	}
}

func TestFeedbackOwnership_ThroughTheComposedHandler(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: ownerTestAgent, URL: "solace:test"})
	taskRepo := store.NewTaskRepo(core.Store())
	feedbackRepo := store.NewFeedbackRepo(core.Store())

	do := func(userID, method, path string, body any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SAM-User-ID", userID)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}
	rate := func(userID, taskID, sessionID string) *httptest.ResponseRecorder {
		return do(userID, http.MethodPost, "/api/v1/feedback", map[string]any{"taskId": taskID, "sessionId": sessionID, "feedbackType": "down"})
	}

	aliceSession, bobSession, aliceTask := idgen.New(), idgen.New(), idgen.New()
	for user, session := range map[string]string{createOwnerAlice: aliceSession, createOwnerBob: bobSession} {
		if rec := do(user, http.MethodPost, "/api/v1/sessions", map[string]any{"id": session, "agentId": aliceAgent}); rec.Code != http.StatusCreated {
			t.Fatalf("%s creates a session: %d %s", user, rec.Code, rec.Body.String())
		}
	}
	handler, handlerType := ownerTestAgent, store.TaskHandlerTypeAgent
	if err := taskRepo.Create(t.Context(), &store.Task{
		ID: aliceTask, UserID: createOwnerAlice, StartTime: 1_700_000_000_000, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		AgentName: &handler, HandlerName: &handler, HandlerType: &handlerType,
	}); err != nil {
		t.Fatalf("seed alice's task: %v", err)
	}

	for _, session := range []string{aliceSession, bobSession} {
		rec := rate(createOwnerBob, aliceTask, session)
		if rec.Code != http.StatusNotFound || errorMessage(t, rec) != taskNotFoundMsg {
			t.Fatalf("bob rating alice's task (session %s): %d %s, want 404 %q", session, rec.Code, rec.Body.String(), taskNotFoundMsg)
		}
	}
	if rec := rate(createOwnerAlice, aliceTask, bobSession); rec.Code != http.StatusNotFound || errorMessage(t, rec) != sessionNotFoundMsg {
		t.Fatalf("alice rating into bob's session: %d %s, want 404 %q", rec.Code, rec.Body.String(), sessionNotFoundMsg)
	}
	if rows, err := feedbackRepo.Search(t.Context(), "*", 0, 0, aliceTask, "", "", 10, 0); err != nil || len(rows) != 0 {
		t.Fatalf("refused ratings were stored: %v rows=%d", err, len(rows))
	}

	if rec := rate(createOwnerAlice, aliceTask, aliceSession); rec.Code != http.StatusOK {
		t.Fatalf("alice rating her own task: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := feedbackRepo.Search(t.Context(), "*", 0, 0, aliceTask, "", "", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].UserID != createOwnerAlice || rows[0].SessionID != aliceSession {
		t.Fatalf("stored rows = %+v (%v), want alice's one rating in her session", rows, err)
	}
}
