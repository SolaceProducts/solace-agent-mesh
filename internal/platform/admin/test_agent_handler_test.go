// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newTestAgentHandlerForTest(t *testing.T) (*testAgentHandler, *store.AgentRepo, *store.WorkflowRepo, *store.SkillRepo) {
	t.Helper()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	skillRepo := store.NewSkillRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	svc := NewTestAgentService(agentRepo, workflowRepo, skillRepo, nil, agentSvc, time.Hour, slog.Default())
	return &testAgentHandler{svc: svc, logger: slog.Default()}, agentRepo, workflowRepo, skillRepo
}

func withFakeUser(req *http.Request, email string) *http.Request {
	ctx := gwauth.ContextWithSubject(req.Context(), gwauth.NewTestSubject(gwauth.SubjectFields{Email: email}))
	return req.WithContext(ctx)
}

func muxFor(h *testAgentHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/platform/builder/sessions/{sessionId}/test-agent", h.handleCreate)
	mux.HandleFunc("GET /api/v1/platform/builder/sessions/{sessionId}/test-agent", h.handleGet)
	mux.HandleFunc("DELETE /api/v1/platform/builder/sessions/{sessionId}/test-agent", h.handleDelete)
	return mux
}

func TestTestAgentHandler_RejectsNonUUIDSessionId(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/platform/builder/sessions/not-a-uuid/test-agent",
				strings.NewReader(`{"componentName":"X"}`))
			req.Header.Set("Content-Type", "application/json")
			req = withFakeUser(req, "u@example.com")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d (non-UUID sessionId must surface as 422 ValidationError)",
					rr.Code, http.StatusUnprocessableEntity)
			}
		})
	}
}

func TestTestAgentHandler_Create_RejectsMissingComponentName(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = withFakeUser(req, "u@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code < 400 || rr.Code >= 500 {
		t.Errorf("status = %d, want 4xx (missing componentName must fail validation)", rr.Code)
	}
}

func TestTestAgentHandler_Create_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	body := `{"componentName":"MyAgent","extraField":"sneaky"}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withFakeUser(req, "u@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (unknown fields must be rejected by strict decode)", rr.Code)
	}
}

func TestTestAgentHandler_Create_RejectsInvalidComponentKind(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)
	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"

	cases := []string{
		"connector",
		"Agent",
		"workflow ",
		"gateway",
		"tool",
		"random-garbage",
	}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			body := `{"componentName":"X","componentKind":"` + kind + `"}`
			req := httptest.NewRequest(http.MethodPost,
				"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent",
				strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req = withFakeUser(req, "u@example.com")
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("kind=%q: status = %d, want %d (oneof must reject)",
					kind, rr.Code, http.StatusUnprocessableEntity)
			}
		})
	}
}

func TestTestAgentHandler_Create_UnknownComponentReturns404(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	body := `{"componentName":"NeverDeployed","componentKind":"agent"}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withFakeUser(req, "u@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (unknown component should surface as NotFoundError)", rr.Code)
	}
}

func decodeErrorEnvelope(t *testing.T, rr *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	if envelope.Message == "" || envelope.ErrorID == "" {
		t.Errorf("error body = %+v, want non-empty message and errorId", envelope)
	}
	return envelope
}

func captureDefaultLog(t *testing.T) *syncedBuffer {
	t.Helper()
	buf := &syncedBuffer{}
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	return buf
}

func TestTestAgentHandler_Create_NameTakenReturns409(t *testing.T) {
	logged := captureDefaultLog(t)
	h, agentRepo, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	user := "u@example.com"
	seed := func(name string) {
		t.Helper()
		desc, prompt, skills := "seeded agent", "seeded prompt", "[]"
		a := &store.Agent{
			Name:         name,
			Type:         AgentTypeStandard,
			Description:  &desc,
			SystemPrompt: &prompt,
			Skills:       &skills,
			CreatedBy:    user,
			UpdatedBy:    user,
		}
		if err := agentRepo.Create(t.Context(), a, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}
	seed("Wrapped")
	seed(generateTestAgentName(sessionID, user))

	body := `{"componentName":"Wrapped","componentKind":"agent"}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withFakeUser(req, user)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a test-agent name already in use; body: %s", rr.Code, rr.Body.String())
	}
	if envelope := decodeErrorEnvelope(t, rr); envelope.Message != testAgentNameInUseMessage {
		t.Errorf("message = %q, want %q", envelope.Message, testAgentNameInUseMessage)
	}
	if log := logged.String(); !strings.Contains(log, "create ephemeral agent:") || strings.Contains(log, user) {
		t.Errorf("log = %q, want the unique-violation cause and no user email", log)
	}
}

func TestTestAgentHandler_StoreFailureReturns500WithLoggedCause(t *testing.T) {
	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	user := "u@example.com"
	for _, tc := range []struct {
		name, method, path string
	}{
		{"get", http.MethodGet, "/api/v1/platform/builder/sessions/" + sessionID + "/test-agent"},
		{"delete", http.MethodDelete, "/api/v1/platform/builder/sessions/" + sessionID + "/test-agent"},
		{"lookup", http.MethodGet, "/api/v1/platform/builder/test-agents/lookup?name=__test__abc__def"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureDefaultLog(t)
			db := testOpenDB(t)
			agentRepo := store.NewAgentRepo(db)
			agentSvc := NewAgentService(agentRepo, nil, nil, nil, platpkg.NewAgentRegistry(), nil, slog.Default())
			svc := NewTestAgentService(agentRepo, store.NewWorkflowRepo(db), store.NewSkillRepo(db), nil, agentSvc, time.Hour, slog.Default())
			h := &testAgentHandler{svc: svc, logger: slog.Default()}
			mux := muxFor(h)
			mux.HandleFunc("GET /api/v1/platform/builder/test-agents/lookup", h.handleLookupParent)
			if err := db.Close(); err != nil {
				t.Fatalf("close db: %v", err)
			}

			req := withFakeUser(httptest.NewRequest(tc.method, tc.path, nil), user)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 when the store fails; body: %s", rr.Code, rr.Body.String())
			}
			if envelope := decodeErrorEnvelope(t, rr); envelope.Message != "internal error" {
				t.Errorf("message = %q, want %q", envelope.Message, "internal error")
			}
			if strings.Contains(rr.Body.String(), "database is closed") {
				t.Errorf("body %q leaks the store error", rr.Body.String())
			}
			if log := logged.String(); !strings.Contains(log, "database is closed") || strings.Contains(log, user) {
				t.Errorf("log = %q, want the store cause and no user email", log)
			}
		})
	}
}

func TestTestAgentHandler_Get_NoExistingAgentReturns404(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent", nil)
	req = withFakeUser(req, "u@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no ephemeral for this session yet)", rr.Code)
	}
}

func TestTestAgentHandler_Delete_NoExistingAgentReturnsNoContent(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent", nil)
	req = withFakeUser(req, "u@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (delete on missing must no-op)", rr.Code)
	}
}

func TestTestAgentHandler_Get_DifferentUserSeesNoEphemeral(t *testing.T) {
	t.Parallel()
	h, agentRepo, _, _ := newTestAgentHandlerForTest(t)
	mux := muxFor(h)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	expires := time.Now().Add(time.Hour).UnixMilli()
	desc := "owned by user A"
	prompt := "test"
	skills := "[]"
	src := "Source"
	a := &store.Agent{
		Name:                generateTestAgentName(sessionID, "userA@example.com"),
		Type:                AgentTypeStandard,
		Description:         &desc,
		SystemPrompt:        &prompt,
		Skills:              &skills,
		CreatedBy:           "userA@example.com",
		UpdatedBy:           "userA@example.com",
		IsEphemeral:         true,
		ExpiresAt:           &expires,
		SourceSessionID:     stringPtr(sessionID),
		SourceComponentName: &src,
	}
	if err := agentRepo.Create(context.Background(), a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent", nil)
	req = withFakeUser(req, "userB@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (user B must not see user A's ephemeral)", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet,
		"/api/v1/platform/builder/sessions/"+sessionID+"/test-agent", nil)
	req2 = withFakeUser(req2, "userA@example.com")
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (user A should see their own ephemeral)", rr2.Code)
	}
	var resp AgentResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.IsEphemeral {
		t.Error("response missing isEphemeral=true marker")
	}
	if resp.SourceSessionID != sessionID {
		t.Errorf("sourceSessionId = %q, want %q", resp.SourceSessionID, sessionID)
	}
	if resp.SourceComponentName != "Source" {
		t.Errorf("sourceComponentName = %q, want %q", resp.SourceComponentName, "Source")
	}
}

func stringPtr(s string) *string { return &s }
