// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type createSessionTestRequest struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId,omitempty"`
	Source  string `json:"source,omitempty"`
}

func newSessionMux(t *testing.T) (*http.ServeMux, *sessionHandler, *store.SessionRepo) {
	t.Helper()
	adapter, _, sessRepo, _, sseRepo := setupTestAdapterWithDB(t)
	sessH := &sessionHandler{core: adapter.core, sessRepo: sessRepo, sseRepo: sseRepo, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions", sessH.handleCreate)
	return mux, sessH, sessRepo
}

func TestHandleCreateSession_RequiresID(t *testing.T) {
	mux, _, _ := newSessionMux(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing id must surface as 400)", rec.Code)
	}
}

func TestHandleCreateSession_RejectsInvalidJSON(t *testing.T) {
	mux, _, _ := newSessionMux(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewBufferString(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed body", rec.Code)
	}
}

func TestHandleCreateSession_CreatesSession(t *testing.T) {
	mux, _, sessRepo := newSessionMux(t)

	id := idgen.New()
	body, _ := json.Marshal(createSessionTestRequest{ID: id, AgentID: "agent-1"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	got, err := sessRepo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("session not persisted")
	}
	if got.AgentID == nil || *got.AgentID != "agent-1" {
		t.Errorf("AgentID = %v, want agent-1", got.AgentID)
	}
}

func TestHandleCreateSession_BuilderTestSourceMarksEphemeralDurably(t *testing.T) {
	mux, _, sessRepo := newSessionMux(t)

	id := idgen.New()
	body, _ := json.Marshal(createSessionTestRequest{ID: id, AgentID: "agent_ephemeral", Source: "builder_test"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	var resp SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.IsEphemeralTest {
		t.Error("IsEphemeralTest = false, want true (durable source marker, no live agent)")
	}

	got, err := sessRepo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Source == nil || *got.Source != "builder_test" {
		t.Errorf("persisted Source = %v, want builder_test", got.Source)
	}
}

func TestHandleCreateSession_IgnoresSpoofedSource(t *testing.T) {
	mux, _, sessRepo := newSessionMux(t)

	id := idgen.New()
	body, _ := json.Marshal(createSessionTestRequest{ID: id, AgentID: "agent-1", Source: "scheduler"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	var resp SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IsEphemeralTest {
		t.Error("IsEphemeralTest = true, want false (spoofed source must not mark the session)")
	}

	got, err := sessRepo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Source != nil {
		t.Errorf("persisted Source = %v, want nil (spoofed source must be ignored)", *got.Source)
	}
}

func TestHandleCreateSession_DuplicateReturnsExisting(t *testing.T) {
	mux, _, sessRepo := newSessionMux(t)

	id := idgen.New()
	existingAgent := "preexisting-agent"
	if err := sessRepo.Create(context.Background(), &store.Session{
		ID: id, UserID: "sam_dev_user", AgentID: &existingAgent,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body, _ := json.Marshal(createSessionTestRequest{ID: id, AgentID: "different-agent"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (duplicate id should return existing), body=%s", rec.Code, rec.Body.String())
	}

	var resp SessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AgentID == nil || *resp.AgentID != "preexisting-agent" {
		t.Errorf("AgentID = %v, want preexisting-agent (POST must not overwrite existing)", resp.AgentID)
	}
}
