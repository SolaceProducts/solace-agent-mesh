// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const statusPlatformUserID = "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f00"

func getBudgetStatus(t *testing.T, gate *budget.Gate, id *auth.Subject) budgetStatusResponse {
	t.Helper()
	core, _, _, _ := setupProjectCopyHarness(t)
	if gate != nil {
		core.SetServices(budgettest.WithGate(core.Services(), gate))
	}
	h := &budgetStatusHandler{core: core}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/budget/status", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), id))
	rec := httptest.NewRecorder()
	h.handleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var resp budgetStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func gateWith(t *testing.T, state budget.EnforcementState) *budget.Gate {
	t.Helper()
	gate := budgettest.NewGate()
	gate.Apply(t.Context(), state)
	return gate
}

func TestBudgetStatus_ReportsTheCallersOwnLevelAndTheMeshNeverAnotherUsers(t *testing.T) {
	periodEnd := budget.PeriodEnd(time.Now())
	state := budgettest.MeshState(true, periodEnd, time.Now())
	state.WarnedUsers = []string{budget.UserKey("", "user-1")}
	state.WarnCapKeys = map[string]string{budget.UserKey("", "user-1"): "cap-1"}
	state.BlockedUsers = []string{budget.UserKey(statusPlatformUserID, "")}

	for _, tc := range []struct {
		name    string
		id      *auth.Subject
		want    string
		wantCap string
	}{
		{"a warned user known by login", auth.NewTestSubject(auth.SubjectFields{UserID: "user-1", Authenticated: true}), "warn", "cap-1"},
		{"a blocked user known by row", auth.NewTestSubject(auth.SubjectFields{UserID: "user-2", PlatformUserID: statusPlatformUserID, Authenticated: true}), "limit", ""},
		{"a user the state does not list", auth.NewTestSubject(auth.SubjectFields{UserID: "user-3", Authenticated: true}), "none", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := getBudgetStatus(t, gateWith(t, state), tc.id)
			if resp.OwnLevel != tc.want || !resp.MeshLimitReached {
				t.Errorf("status = %+v, want own level %s and the mesh reached", resp, tc.want)
			}
			if gotCap := derefCapKey(resp.CapKey); gotCap != tc.wantCap || (tc.wantCap == "" && resp.CapKey != nil) {
				t.Errorf("capKey = %v, want %q (null when empty)", resp.CapKey, tc.wantCap)
			}
		})
	}
}

func derefCapKey(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func TestIntegration_BudgetStatusIsServedToTheCaller(t *testing.T) {
	gw := newTestGateway(t)
	resp := gw.get(t, "/api/v1/budget/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/budget/status = %d, want 200", resp.StatusCode)
	}
	var body budgetStatusResponse
	decodeRespJSON(t, resp, &body)
	if body.OwnLevel != string(budget.LevelNone) || body.MeshLimitReached || body.CapKey != nil || body.PeriodEnd == "" {
		t.Errorf("status = %+v, want nothing reached with the period's end", body)
	}
}

func TestBudgetStatus_AMeshOnlyStateReachesNoUserAndCarriesNoCapKey(t *testing.T) {
	periodEnd := budget.PeriodEnd(time.Now())
	resp := getBudgetStatus(t, gateWith(t, budgettest.MeshState(true, periodEnd, time.Now())), auth.NewTestSubject(auth.SubjectFields{UserID: "user-1", Authenticated: true}))
	if resp.OwnLevel != string(budget.LevelNone) || !resp.MeshLimitReached || resp.CapKey != nil {
		t.Errorf("status = %+v, want only the mesh reached", resp)
	}
}

func TestBudgetStatus_ReportsTheUserSubmitTaskGates(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   *auth.Subject
		key  func(defaultUser string) string
	}{
		{"a user known by login", auth.NewTestSubject(auth.SubjectFields{UserID: "user-1", Authenticated: true}),
			func(string) string { return budget.UserKey("", "user-1") }},
		{"a user known by row", auth.NewTestSubject(auth.SubjectFields{UserID: "user-2", PlatformUserID: statusPlatformUserID, Authenticated: true}),
			func(string) string { return budget.UserKey(statusPlatformUserID, "") }},
		{"a caller with an email and no user id", auth.NewTestSubject(auth.SubjectFields{Email: "someone@example.com", Authenticated: true}),
			func(defaultUser string) string { return budget.UserKey("", defaultUser) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, sessRepo, projectRepo, _ := setupProjectCopyHarness(t)
			key := tc.key(core.Config().DefaultUserIdentity)
			core.SetServices(budgettest.WithGate(core.Services(), gateWith(t, budgettest.UserBlockedState(budget.PeriodEnd(time.Now()), time.Now(), key))))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/budget/status", nil)
			req = req.WithContext(auth.ContextWithSubject(req.Context(), tc.id))
			if status := callerBudgetStatus(req, core); status.OwnLevel != string(budget.LevelLimit) {
				t.Errorf("status = %+v, want the limit of %s", status, key)
			}

			body := `{"jsonrpc":"2.0","id":"req1","params":{"message":{"kind":"message","role":"user",` +
				`"parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"test-agent"}}}}`
			send := httptest.NewRequest(http.MethodPost, "/api/v1/message:send", strings.NewReader(body))
			send = send.WithContext(auth.ContextWithSubject(send.Context(), tc.id))
			rec := httptest.NewRecorder()
			newProjectMessageHandler(core, sessRepo, projectRepo).handleMessageSend(rec, send)
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("message:send = %d %s, want the 429 of the budget the status reports", rec.Code, rec.Body.String())
			}
		})
	}
}
