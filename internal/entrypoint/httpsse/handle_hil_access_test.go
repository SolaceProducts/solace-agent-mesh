// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestHandleRespond_DelegationDenied(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "restricted-agent"})
	taskID := idgen.New()
	_ = core.Tasks().Register(taskID, "restricted-agent", "alice@example.com", entrypoint.TaskIdentity{})

	h := &hilHandler{core: core, eventLog: eventlog.NewMemoryEventLog(), logger: core.Logger()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/userInput/respond", h.handleRespond)

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice@example.com",
		Authenticated: true,
		Scopes:        []string{"agent:other-agent:invoke"},
	})

	body := respondBody(taskID, "restricted-agent", "r1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for delegation-denied identity", rec.Code)
	}
}

func TestHandleRespond_DelegationAllowed(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "allowed-agent"})
	taskID := idgen.New()
	_ = core.Tasks().Register(taskID, "allowed-agent", "bob@example.com", entrypoint.TaskIdentity{})

	h := &hilHandler{core: core, eventLog: eventlog.NewMemoryEventLog(), logger: core.Logger()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/userInput/respond", h.handleRespond)

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "bob@example.com",
		Authenticated: true,
		Scopes:        []string{"agent:allowed-agent:invoke"},
	})

	body := respondBody(taskID, "allowed-agent", "r2")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for identity holding agent:allowed-agent:invoke, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRespond_NoIdentityAllowed(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "open-agent"})
	taskID := idgen.New()
	_ = core.Tasks().Register(taskID, "open-agent", "sam_dev_user", entrypoint.TaskIdentity{})

	h := &hilHandler{core: core, eventLog: eventlog.NewMemoryEventLog(), logger: core.Logger()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/userInput/respond", h.handleRespond)

	body := respondBody(taskID, "open-agent", "r3")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when no identity is present, body=%s", rec.Code, rec.Body.String())
	}
}
