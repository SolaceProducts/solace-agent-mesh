// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestLookupParent_ValidationErrors(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := newTestAgentSvcForUnit(t, store.NewAgentRepo(db), store.NewWorkflowRepo(db))

	cases := []struct {
		name, agent, user string
	}{
		{"empty name", "", "user-1"},
		{"empty user", "test-agent", ""},
		{"both empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.LookupParentByTestAgentName(t.Context(), c.agent, c.user)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("err = %v, want ValidationError", err)
			}
		})
	}
}

func TestLookupParent_NonEphemeralPrefixReturnsNil(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := newTestAgentSvcForUnit(t, store.NewAgentRepo(db), store.NewWorkflowRepo(db))

	got, err := svc.LookupParentByTestAgentName(t.Context(), "regular-agent", "user-1")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for non-ephemeral name", got)
	}
}

func TestLookupParent_MissingAgentReturnsNil(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := newTestAgentSvcForUnit(t, store.NewAgentRepo(db), store.NewWorkflowRepo(db))

	missingName := generateTestAgentName("019e045e-1491-7ba0-9611-717bd7cc595e", "user-1")
	got, err := svc.LookupParentByTestAgentName(t.Context(), missingName, "user-1")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for missing agent", got)
	}
}

func TestLookupParent_DifferentOwnerReturnsNil(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, store.NewWorkflowRepo(db))

	eph := seedEphemeral(t, agentRepo, "session-X", "owner@example.com", time.Hour)

	got, err := svc.LookupParentByTestAgentName(t.Context(), eph.Name, "intruder@example.com")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil (different owner must not see parent)", got)
	}
}

func TestLookupParent_HappyPath(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, store.NewWorkflowRepo(db))

	eph := seedEphemeral(t, agentRepo, "session-XYZ", "owner@example.com", time.Hour)

	got, err := svc.LookupParentByTestAgentName(t.Context(), eph.Name, "owner@example.com")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got == nil {
		t.Fatal("got nil, want ParentLookup")
	}
	if got.BuilderSessionID != "session-XYZ" {
		t.Errorf("BuilderSessionID = %q, want session-XYZ", got.BuilderSessionID)
	}
	if got.ComponentName != "Source" {
		t.Errorf("ComponentName = %q, want Source", got.ComponentName)
	}
	if got.TestAgentName != eph.Name {
		t.Errorf("TestAgentName = %q, want %q", got.TestAgentName, eph.Name)
	}
	if got.TestAgentInstanceID != eph.ID {
		t.Errorf("TestAgentInstanceID = %q, want %q", got.TestAgentInstanceID, eph.ID)
	}
}

func TestLookupParent_AcceptsInstanceName(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, store.NewWorkflowRepo(db))

	eph := seedEphemeral(t, agentRepo, "session-INST", "owner@example.com", time.Hour)

	got, err := svc.LookupParentByTestAgentName(t.Context(), AgentInstanceName(eph), "owner@example.com")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got == nil {
		t.Fatal("got nil, want ParentLookup for instance-name lookup")
	}
	if got.BuilderSessionID != "session-INST" {
		t.Errorf("BuilderSessionID = %q, want session-INST", got.BuilderSessionID)
	}
	if got.TestAgentName != eph.Name {
		t.Errorf("TestAgentName = %q, want %q (resolved row's display name)", got.TestAgentName, eph.Name)
	}
}

func TestLookupParent_InstanceNameUnknownAgentReturnsNil(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := newTestAgentSvcForUnit(t, store.NewAgentRepo(db), store.NewWorkflowRepo(db))

	instanceName := "agent_" + "019e045e_1491_7ba0_9611_717bd7cc595e"
	got, err := svc.LookupParentByTestAgentName(t.Context(), instanceName, "user-1")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for missing instance name", got)
	}
}

func TestLookupParent_MalformedInstanceNameReturnsNilNoError(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := newTestAgentSvcForUnit(t, store.NewAgentRepo(db), store.NewWorkflowRepo(db))

	got, err := svc.LookupParentByTestAgentName(t.Context(), "agent_not-a-uuid", "user-1")
	if err != nil {
		t.Fatalf("err = %v, want nil (malformed UUID must surface as 'no parent', not 500)", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for malformed instance name", got)
	}
}

func TestAgentIDFromInstanceName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		input  string
		wantID string
		wantOK bool
	}{
		{"valid instance name", "agent_019e045e_1491_7ba0_9611_717bd7cc595e", "019e045e-1491-7ba0-9611-717bd7cc595e", true},
		{"non-agent prefix", "__test__abc__user", "", false},
		{"empty", "", "", false},
		{"agent prefix without uuid body", "agent_not-a-uuid", "", false},
		{"agent prefix with truncated uuid", "agent_019e045e_1491_7ba0", "", false},
		{"workflow prefix with valid uuid body", "workflow_019e045e_1491_7ba0_9611_717bd7cc595e", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			gotID, gotOK := agentIDFromInstanceName(c.input)
			if gotOK != c.wantOK {
				t.Errorf("ok = %v, want %v", gotOK, c.wantOK)
			}
			if gotID != c.wantID {
				t.Errorf("id = %q, want %q", gotID, c.wantID)
			}
		})
	}
}

func TestHandleLookupParent_MissingNameReturnsValidationError(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/builder/test-agents/lookup", h.handleLookupParent)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/builder/test-agents/lookup", nil)
	req = withFakeUser(req, "u@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (missing 'name' must surface as ValidationError)", rr.Code)
	}
}

func TestHandleLookupParent_UnknownAgentReturns404(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newTestAgentHandlerForTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/builder/test-agents/lookup", h.handleLookupParent)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/builder/test-agents/lookup?name=not-an-ephemeral", nil)
	req = withFakeUser(req, "u@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestHandleLookupParent_HappyPath(t *testing.T) {
	t.Parallel()
	h, agentRepo, _, _ := newTestAgentHandlerForTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/builder/test-agents/lookup", h.handleLookupParent)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	expires := time.Now().Add(time.Hour).UnixMilli()
	desc := "owned by user A"
	prompt := "test"
	skills := "[]"
	src := "MyWorkflow"
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/builder/test-agents/lookup?name="+a.Name, nil)
	req = withFakeUser(req, "userA@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp LookupTestAgentParentResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.BuilderSessionID != sessionID {
		t.Errorf("BuilderSessionID = %q, want %q", resp.BuilderSessionID, sessionID)
	}
	if resp.ComponentName != "MyWorkflow" {
		t.Errorf("ComponentName = %q, want MyWorkflow", resp.ComponentName)
	}
	if resp.TestAgentName != a.Name {
		t.Errorf("TestAgentName = %q, want %q", resp.TestAgentName, a.Name)
	}
	if resp.TestAgentID != a.ID {
		t.Errorf("TestAgentID = %q, want %q", resp.TestAgentID, a.ID)
	}
}

func TestCreateForSession_RejectsBlankInputs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := newTestAgentSvcForUnit(t, store.NewAgentRepo(db), store.NewWorkflowRepo(db))

	cases := []struct {
		name, sessionID, componentName, componentKind, userID string
	}{
		{"blank session", "", "comp", "agent", "user-1"},
		{"blank component", "session-1", "", "agent", "user-1"},
		{"blank kind", "session-1", "comp", "", "user-1"},
		{"blank user", "session-1", "comp", "agent", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.CreateForSession(t.Context(), c.sessionID, c.componentName, c.componentKind, c.userID)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("err = %v, want ValidationError", err)
			}
		})
	}
}

func TestCreateForSession_UnknownComponentReturnsNotFound(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := newTestAgentSvcForUnit(t, store.NewAgentRepo(db), store.NewWorkflowRepo(db))

	_, err := svc.CreateForSession(t.Context(), "019e045e-1491-7ba0-9611-717bd7cc595e", "NonexistentComp", "agent", "user-1")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("err = %v, want NotFoundError", err)
	}
}

func TestHandleLookupParent_DifferentUserSees404(t *testing.T) {
	t.Parallel()
	h, agentRepo, _, _ := newTestAgentHandlerForTest(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/builder/test-agents/lookup", h.handleLookupParent)

	sessionID := "019e045e-1491-7ba0-9611-717bd7cc595e"
	expires := time.Now().Add(time.Hour).UnixMilli()
	desc := "owned by A"
	prompt := "p"
	skills := "[]"
	src := "Comp"
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/builder/test-agents/lookup?name="+a.Name, nil)
	req = withFakeUser(req, "userB@example.com")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (cross-user lookup must not leak parent info)", rr.Code)
	}
}
