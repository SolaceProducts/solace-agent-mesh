// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	listOwnerUser      = "sam_dev_user"
	listStrangerUser   = "bob"
	listPrivateComment = "owner-private-feedback-comment"
)

type feedbackListOwnerEnv struct {
	*feedbackTestEnv
	logs   *safeBuffer
	taskID string
	sessID string
}

func newFeedbackListOwnerEnv(t *testing.T) *feedbackListOwnerEnv {
	t.Helper()
	env := setupFeedbackTest(t, false)
	logs := &safeBuffer{}
	env.handler.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	taskID, sessID := idgen.New(), idgen.New()
	comment := listPrivateComment
	if rec := env.submitFeedback(t, taskID, sessID, "up", &comment); rec.Code != http.StatusOK {
		t.Fatalf("owner rates own task: %d %s", rec.Code, rec.Body.String())
	}
	return &feedbackListOwnerEnv{feedbackTestEnv: env, logs: logs, taskID: taskID, sessID: sessID}
}

func (e *feedbackListOwnerEnv) list(t *testing.T, userID, query string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/feedback"+query, nil)
	if userID != "" {
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: userID, Scopes: scopes})))
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func decodeMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Message
}

func TestHandleFeedbackList_TaskFilter_ByOwnership(t *testing.T) {
	t.Run("the owner sees their own rows", func(t *testing.T) {
		env := newFeedbackListOwnerEnv(t)
		rec := env.list(t, "", "?taskId="+env.taskID)
		if rec.Code != http.StatusOK {
			t.Fatalf("owner: status %d, body=%s", rec.Code, rec.Body.String())
		}
		var page PaginatedResponse[FeedbackResponse]
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Data) != 1 || page.Data[0].TaskID != env.taskID || page.Data[0].UserID != listOwnerUser {
			t.Errorf("owner's rows = %+v, want their one rating", page.Data)
		}
	})

	t.Run("a stranger gets the answer a missing task gets", func(t *testing.T) {
		env := newFeedbackListOwnerEnv(t)
		missing := env.list(t, listStrangerUser, "?taskId="+idgen.New())
		refused := env.list(t, listStrangerUser, "?taskId="+env.taskID)

		if missing.Code != http.StatusNotFound {
			t.Fatalf("missing task: status %d, want 404", missing.Code)
		}
		if refused.Code != missing.Code {
			t.Errorf("another user's task: status %d, want %d (the status of a missing task)", refused.Code, missing.Code)
		}
		if got, want := decodeMessage(t, refused), decodeMessage(t, missing); got != want || got != taskNotFoundMsg {
			t.Errorf("another user's task: message %q, missing task: %q, want %q for both", got, want, taskNotFoundMsg)
		}
		for _, leaked := range []string{listOwnerUser, env.sessID, listPrivateComment} {
			if strings.Contains(refused.Body.String(), leaked) {
				t.Errorf("refusal body carries %q: %s", leaked, refused.Body.String())
			}
		}
	})

	t.Run("a cross-user read grant does not widen the route", func(t *testing.T) {
		env := newFeedbackListOwnerEnv(t)
		rec := env.list(t, listStrangerUser, "?taskId="+env.taskID, rbac.ScopeActivityRead, rbac.ScopeAgentReadFeedbackAny)
		if rec.Code != http.StatusNotFound || decodeMessage(t, rec) != taskNotFoundMsg {
			t.Errorf("activity/feedback reader: status %d body=%s, want the 404 %q", rec.Code, rec.Body.String(), taskNotFoundMsg)
		}
	})

	t.Run("a stranger's own filter-free list carries none of the owner's rows", func(t *testing.T) {
		env := newFeedbackListOwnerEnv(t)
		rec := env.list(t, listStrangerUser, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), env.taskID) || strings.Contains(rec.Body.String(), listPrivateComment) {
			t.Errorf("stranger's list carries the owner's rating: %s", rec.Body.String())
		}
	})
}

func TestHandleFeedbackList_TaskFilter_RefusalIsLoggedAtWarn(t *testing.T) {
	env := newFeedbackListOwnerEnv(t)

	env.list(t, listStrangerUser, "?taskId="+env.taskID)

	var lines []string
	for l := range strings.SplitSeq(env.logs.String(), "\n") {
		if strings.Contains(l, "task access denied") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("refusal logged %d times, want once: %s", len(lines), env.logs.String())
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatalf("decode log line %q: %v", lines[0], err)
	}
	if line["level"] != "WARN" || line["callerUser"] != listStrangerUser || line["taskID"] != env.taskID || line["reason"] != denyReadNotOwner {
		t.Errorf("refusal log = %v", line)
	}
	for _, leaked := range []string{listPrivateComment, env.sessID, "@example.com"} {
		if strings.Contains(lines[0], leaked) {
			t.Errorf("refusal log carries %q: %s", leaked, lines[0])
		}
	}
}

func TestHandleFeedbackList_TaskFilter_OwnerAndMissingTaskAreNotLoggedAsRefusals(t *testing.T) {
	env := newFeedbackListOwnerEnv(t)

	env.list(t, "", "?taskId="+env.taskID)
	env.list(t, listStrangerUser, "?taskId="+idgen.New())

	if strings.Contains(env.logs.String(), "task access denied") {
		t.Errorf("a served read or a missing task was logged as a refusal: %s", env.logs.String())
	}
}
