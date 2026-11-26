// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestFeedbackRepo_GetByID(t *testing.T) {
	env := setupFeedbackTest(t, false)
	repo := env.handler.feedbackRepo
	ctx := context.Background()

	res, err := repo.Submit(ctx, store.FeedbackSubmission{
		TaskID: idgen.New(), UserID: "user-a", SessionID: idgen.New(), Rating: "down",
		SubjectID: "agent-1", SubjectType: store.SubjectTypeAgent, EntrypointType: store.EntrypointTypeWebUI,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	row := res.Feedback

	got, err := repo.GetByID(ctx, row.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.ID != row.ID || got.SubjectID != "agent-1" || got.Rating != "down" {
		t.Errorf("GetByID returned %+v, want the seeded row", got)
	}

	missing, err := repo.GetByID(ctx, idgen.New())
	if err != nil {
		t.Fatalf("GetByID unknown: %v", err)
	}
	if missing != nil {
		t.Errorf("unknown id must return nil, got %+v", missing)
	}
}

func newFeedbackItemEnv(t *testing.T) *feedbackTestEnv {
	t.Helper()
	env := setupFeedbackTest(t, false)
	env.mux.HandleFunc("GET /api/v1/feedback/all", env.handler.handleListCrossUser)
	env.mux.HandleFunc("GET /api/v1/feedback/{id}", env.handler.handleGetItem)
	return env
}

func seedItemRow(t *testing.T, env *feedbackTestEnv, owner, subjectType, subjectID string) *store.Feedback {
	t.Helper()
	comment := "the limit quoted was wrong"
	res, err := env.handler.feedbackRepo.Submit(context.Background(), store.FeedbackSubmission{
		TaskID: idgen.New(), UserID: owner, SessionID: idgen.New(), Rating: "down", Comment: &comment,
		SubjectID: subjectID, SubjectType: subjectType, EntrypointType: store.EntrypointTypeWebUI,
	})
	if err != nil {
		t.Fatalf("seed item: %v", err)
	}
	return res.Feedback
}

func getItemAs(t *testing.T, env *feedbackTestEnv, id, caller string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/feedback/"+id, nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: caller, Scopes: scopes})))
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleGetItem_Authorization(t *testing.T) {
	tests := []struct {
		name        string
		subjectType string
		subjectID   string
		caller      string
		scopes      []string
		wantStatus  int
	}{
		{"owner without scopes", "", "", "user-a", nil, http.StatusOK},
		{"reviewer with agent wildcard", "", "", "auditor", []string{rbac.ScopeAgentReadFeedbackAny}, http.StatusOK},
		{"reviewer with the exact agent grant", "", "", "auditor", []string{rbac.AgentReadFeedbackScope("agent-1")}, http.StatusOK},
		{"reviewer with a different agent grant", "", "", "auditor", []string{rbac.AgentReadFeedbackScope("agent-2")}, http.StatusNotFound},
		{"reviewer with workflow wildcard only", "", "", "auditor", []string{rbac.ScopeWorkflowReadFeedbackAny}, http.StatusNotFound},
		{"trace grant alone", "", "", "auditor", []string{rbac.ScopeAgentReadTraceAny}, http.StatusNotFound},
		{"stranger with no scopes", "", "", "auditor", nil, http.StatusNotFound},
		{"workflow row, agent wildcard only", store.SubjectTypeWorkflow, "workflow-1", "auditor", []string{rbac.ScopeAgentReadFeedbackAny}, http.StatusNotFound},
		{"workflow row, workflow wildcard", store.SubjectTypeWorkflow, "workflow-1", "auditor", []string{rbac.ScopeWorkflowReadFeedbackAny}, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFeedbackItemEnv(t)
			subjectType, subjectID := tc.subjectType, tc.subjectID
			if subjectType == "" {
				subjectType, subjectID = store.SubjectTypeAgent, "agent-1"
			}
			row := seedItemRow(t, env, "user-a", subjectType, subjectID)
			rec := getItemAs(t, env, row.ID, tc.caller, tc.scopes...)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusNotFound && strings.Contains(rec.Body.String(), subjectID) {
				t.Error("a refused read must not describe the row")
			}
		})
	}
}

func TestHandleGetItem_BodyIsBareRowWithSubjectName(t *testing.T) {
	env := newFeedbackItemEnv(t)
	env.registerCard(t, "agent-1", "HR Assistant")
	row := seedItemRow(t, env, "user-a", store.SubjectTypeAgent, "agent-1")

	rec := getItemAs(t, env, row.ID, "auditor", rbac.ScopeAgentReadFeedbackAny)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, wrapped := body["data"]; wrapped {
		t.Error("single resource must be bare, not wrapped in data")
	}
	if body["id"] != row.ID || body["subjectName"] != "HR Assistant" || body["rating"] != "down" || body["comment"] != "the limit quoted was wrong" {
		t.Errorf("unexpected body: %v", body)
	}
	if body["taskId"] != row.TaskID || body["entrypointType"] != store.EntrypointTypeWebUI {
		t.Errorf("row fields missing: %v", body)
	}
}

func TestHandleGetItem_UnknownAndMalformedIDs(t *testing.T) {
	env := newFeedbackItemEnv(t)
	if rec := getItemAs(t, env, idgen.New(), "auditor", rbac.ScopeAgentReadFeedbackAny); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: status = %d, want 404", rec.Code)
	}
	if rec := getItemAs(t, env, "not-a-uuid", "auditor", rbac.ScopeAgentReadFeedbackAny); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed id: status = %d, want 400", rec.Code)
	}
}

func TestHandleGetItem_ListRouteStillWins(t *testing.T) {
	env := newFeedbackItemEnv(t)
	rec := getItemAs(t, env, "all", "auditor", rbac.ScopeAgentReadFeedbackAny)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "\"data\"") {
		t.Errorf("/feedback/all must still be served by the list handler: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetItem_DeniedRowIsIndistinguishableFromUnknownID(t *testing.T) {
	env := newFeedbackItemEnv(t)
	row := seedItemRow(t, env, "user-a", store.SubjectTypeAgent, "agent-1")

	deniedRec := getItemAs(t, env, row.ID, "auditor", rbac.AgentReadFeedbackScope("agent-2"))
	unknownRec := getItemAs(t, env, idgen.New(), "auditor", rbac.AgentReadFeedbackScope("agent-2"))

	if deniedRec.Code != unknownRec.Code {
		t.Fatalf("status codes differ: denied=%d unknown=%d", deniedRec.Code, unknownRec.Code)
	}

	var deniedBody, unknownBody map[string]any
	if err := json.Unmarshal(deniedRec.Body.Bytes(), &deniedBody); err != nil {
		t.Fatalf("decode denied body: %v", err)
	}
	if err := json.Unmarshal(unknownRec.Body.Bytes(), &unknownBody); err != nil {
		t.Fatalf("decode unknown body: %v", err)
	}
	delete(deniedBody, "errorId")
	delete(unknownBody, "errorId")
	if !reflect.DeepEqual(deniedBody, unknownBody) {
		t.Errorf("denied and unknown bodies must match (up to errorId): denied=%v unknown=%v", deniedBody, unknownBody)
	}

	for _, body := range []string{deniedRec.Body.String(), unknownRec.Body.String()} {
		if strings.Contains(body, "the limit quoted was wrong") {
			t.Error("a refused read must not leak the row's comment text")
		}
	}
}
