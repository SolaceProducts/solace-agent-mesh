// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	runAsSuperAdmin = "alice@example.com"
	runAsOrdinary   = "bob@example.com"
	runAsSystemUser = "system:nightly"
	runAsLegacyUser = "system:legacy"
	runAsFlakyUser  = "system:flaky"
)

type runAsDirectory map[string]auth.Entitlement

func (d runAsDirectory) Lookup(_ context.Context, userID string, _ map[string]any) (auth.Entitlement, error) {
	ent, known := d[userID]
	if !known {
		ent.SystemUserRow = auth.SystemUserRowMissing
	}
	return ent, nil
}

type runAsResolver struct{ dir runAsDirectory }

func (r runAsResolver) ResolveIdentity(ctx context.Context, userID string) (*auth.Subject, error) {
	id, err := auth.ForScheduledTaskOwner(ctx, userID, r.dir)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", entrypoint.ErrIdentityUnauthorized, err)
	}
	return id, nil
}

func (r runAsResolver) ResolveRunAs(ctx context.Context, principal string) (*auth.Subject, error) {
	id, err := auth.ForScheduledRunAs(ctx, principal, r.dir)
	if auth.IsScheduledRunAsRefusal(err) {
		return nil, fmt.Errorf("%w: %w", entrypoint.ErrIdentityUnauthorized, err)
	}
	return id, err
}

func setupRunAsRoutes(t *testing.T, flagOn bool) (*scheduledTaskHandler, *http.ServeMux) {
	t.Helper()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyScheduledTaskRunAs, Default: flagOn})
	h, mux := setupScheduledTaskTest(t, runtime.WithFeatureRegistry(reg))
	h.core.SetIdentityResolver(runAsResolver{dir: runAsDirectory{
		runAsSuperAdmin: {Scopes: []string{"*:*:*"}},
		runAsOrdinary:   {Scopes: []string{"agent:*:invoke"}},
		runAsSystemUser: {Scopes: []string{"agent:test-agent:invoke"}, SystemUserID: "0193a1c2-7d4e-7f00-8a00-0000000000bb"},
		runAsLegacyUser: {Scopes: []string{"agent:test-agent:invoke"}},
		runAsFlakyUser:  {Scopes: []string{"agent:test-agent:invoke"}, SystemUserRow: auth.SystemUserRowUnavailable},
	}})
	h.artSvc = &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}
	mux.HandleFunc("GET /api/v1/artifacts/scheduled/{id}/{filename}", h.handleGetScheduledArtifact)
	return h, mux
}

func runAsScopes(userID string) []string {
	if userID == runAsSuperAdmin {
		return []string{"*:*:*"}
	}
	return []string{"agent:*:invoke"}
}

func serveRunAsRequest(t *testing.T, mux *http.ServeMux, userID, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = jsonRequest(t, method, path, body)
	}
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: userID, Email: userID, Scopes: runAsScopes(userID), Authenticated: true,
	})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func runAsCreateBody(runAs any) map[string]any {
	body := map[string]any{
		"name":               "nightly report " + idgen.New(),
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "test-agent",
		"taskMessage":        []map[string]any{{"type": "text", "text": "summarize"}},
	}
	if runAs != nil {
		body["runAs"] = runAs
	}
	return body
}

func seedRunAsTask(t *testing.T, h *scheduledTaskHandler, owner, runAs string) *store.ScheduledTask {
	t.Helper()
	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "seeded " + idgen.New(), Namespace: "test-ns",
		UserID: &owner, CreatedBy: owner,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *", Timezone: "UTC",
		TargetAgentName: "test-agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"summarize"}]`, Source: "ui",
	}
	if runAs != "" {
		task.RunAs = &runAs
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func decodeTask(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

func TestScheduledTaskRunAs_Create(t *testing.T) {
	cases := []struct {
		name     string
		flagOn   bool
		author   string
		runAs    any
		wantCode int
		wantText string
	}{
		{"a super administrator names a service account", true, runAsSuperAdmin, runAsSystemUser, http.StatusCreated, ""},
		{"an ordinary user is refused", true, runAsOrdinary, runAsSystemUser, http.StatusForbidden, "super administrator"},
		{"an unknown service account is refused", true, runAsSuperAdmin, "system:typo", http.StatusBadRequest, "exists and holds a role"},
		{"a person is refused", true, runAsSuperAdmin, runAsOrdinary, http.StatusBadRequest, "must name a service account as system:"},
		{"a bare name is refused", true, runAsSuperAdmin, "nightly", http.StatusBadRequest, "must name a service account as system:"},
		{"the evaluation service account is refused", true, runAsSuperAdmin, "system:eval", http.StatusBadRequest, "built-in"},
		{"a platform that reports no service account rows is refused", true, runAsSuperAdmin, runAsLegacyUser, http.StatusBadRequest, "platform must be upgraded"},
		{"a row the platform could not confirm is retryable", true, runAsSuperAdmin, runAsFlakyUser, http.StatusServiceUnavailable, "try again"},
		{"the flag off refuses the field", false, runAsSuperAdmin, runAsSystemUser, http.StatusBadRequest, "not available"},
		{"a non-string is refused", true, runAsSuperAdmin, 7, http.StatusBadRequest, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, mux := setupRunAsRoutes(t, c.flagOn)

			rec := serveRunAsRequest(t, mux, c.author, http.MethodPost, "/api/v1/scheduledTasks", runAsCreateBody(c.runAs))

			if rec.Code != c.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), c.wantText) {
				t.Errorf("body = %s, want it to say %q", rec.Body.String(), c.wantText)
			}
			if rec.Code != http.StatusCreated {
				return
			}
			created := decodeTask(t, rec)
			if created["runAs"] != runAsSystemUser || created["createdBy"] != c.author {
				t.Errorf("created = runAs %v createdBy %v, want %s by %s", created["runAs"], created["createdBy"], runAsSystemUser, c.author)
			}
			stored, err := h.repo.GetByID(t.Context(), created["id"].(string))
			if err != nil || stored == nil || stored.RunAs == nil || *stored.RunAs != runAsSystemUser {
				t.Fatalf("stored task = %+v, %v, want runAs %s", stored, err, runAsSystemUser)
			}
		})
	}
}

func TestScheduledTaskRunAs_CreateWithoutRunAsIsUnchanged(t *testing.T) {
	for _, flagOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("flag %v", flagOn), func(t *testing.T) {
			_, mux := setupRunAsRoutes(t, flagOn)

			rec := serveRunAsRequest(t, mux, runAsOrdinary, http.MethodPost, "/api/v1/scheduledTasks", runAsCreateBody(nil))

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
			}
			if _, present := decodeTask(t, rec)["runAs"]; present {
				t.Errorf("response carries runAs for a task without one: %s", rec.Body.String())
			}
		})
	}
}

func TestScheduledTaskRunAs_Update(t *testing.T) {
	cases := []struct {
		name      string
		owner     string
		storedAs  string
		body      map[string]any
		wantCode  int
		wantRunAs any
	}{
		{"an ordinary owner cannot change what a service account runs", runAsOrdinary, runAsSystemUser,
			map[string]any{"taskMessage": []map[string]any{{"type": "text", "text": "export everything"}}}, http.StatusForbidden, runAsSystemUser},
		{"an ordinary owner cannot retarget it", runAsOrdinary, runAsSystemUser,
			map[string]any{"name": "renamed"}, http.StatusForbidden, runAsSystemUser},
		{"an ordinary owner cannot edit it by sending enabled alongside", runAsOrdinary, runAsSystemUser,
			map[string]any{"enabled": true, "taskMessage": []map[string]any{{"type": "text", "text": "export everything"}}}, http.StatusForbidden, runAsSystemUser},
		{"an ordinary owner may pause it", runAsOrdinary, runAsSystemUser,
			map[string]any{"enabled": false}, http.StatusOK, runAsSystemUser},
		{"an ordinary owner may clear runAs and edit in the same write", runAsOrdinary, runAsSystemUser,
			map[string]any{"runAs": nil, "name": "mine again"}, http.StatusOK, nil},
		{"an ordinary owner cannot set runAs", runAsOrdinary, "",
			map[string]any{"runAs": runAsSystemUser}, http.StatusForbidden, nil},
		{"a super administrator may edit it", runAsSuperAdmin, runAsSystemUser,
			map[string]any{"name": "renamed"}, http.StatusOK, runAsSystemUser},
		{"a super administrator may set runAs", runAsSuperAdmin, "",
			map[string]any{"runAs": runAsSystemUser}, http.StatusOK, runAsSystemUser},
		{"an unknown service account is refused on update", runAsSuperAdmin, "",
			map[string]any{"runAs": "system:typo"}, http.StatusBadRequest, nil},
		{"an ordinary owner edits a task without runAs as before", runAsOrdinary, "",
			map[string]any{"name": "renamed"}, http.StatusOK, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, mux := setupRunAsRoutes(t, true)
			task := seedRunAsTask(t, h, c.owner, c.storedAs)

			rec := serveRunAsRequest(t, mux, c.owner, http.MethodPatch, "/api/v1/scheduledTasks/"+task.ID, c.body)

			if rec.Code != c.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.wantCode, rec.Body.String())
			}
			stored, err := h.repo.GetByID(t.Context(), task.ID)
			if err != nil || stored == nil {
				t.Fatal(err)
			}
			var got any
			if stored.RunAs != nil {
				got = *stored.RunAs
			}
			if got != c.wantRunAs {
				t.Errorf("stored runAs = %v, want %v", got, c.wantRunAs)
			}
			if rec.Code == http.StatusForbidden && (stored.Name != task.Name || stored.TaskMessage != task.TaskMessage) {
				t.Errorf("a refused write still changed the task to {name %q, message %s}", stored.Name, stored.TaskMessage)
			}
		})
	}
}

func TestScheduledTaskRunAs_UpdateWithTheFlagOff(t *testing.T) {
	cases := []struct {
		name      string
		owner     string
		storedAs  string
		body      map[string]any
		wantCode  int
		wantText  string
		wantRunAs any
	}{
		{"a null runAs clears a stored one", runAsOrdinary, runAsSystemUser,
			map[string]any{"runAs": nil}, http.StatusOK, "", nil},
		{"an empty runAs clears a stored one", runAsOrdinary, runAsSystemUser,
			map[string]any{"runAs": ""}, http.StatusOK, "", nil},
		{"a null runAs on a task without one changes nothing", runAsOrdinary, "",
			map[string]any{"runAs": nil}, http.StatusOK, "", nil},
		{"a service account is refused", runAsSuperAdmin, "",
			map[string]any{"runAs": runAsSystemUser}, http.StatusBadRequest, "not available", nil},
		{"a service account cannot replace the stored one", runAsSuperAdmin, runAsSystemUser,
			map[string]any{"runAs": "system:other"}, http.StatusBadRequest, "not available", runAsSystemUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, mux := setupRunAsRoutes(t, false)
			task := seedRunAsTask(t, h, c.owner, c.storedAs)

			rec := serveRunAsRequest(t, mux, c.owner, http.MethodPatch, "/api/v1/scheduledTasks/"+task.ID, c.body)

			if rec.Code != c.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), c.wantText) {
				t.Errorf("body = %s, want it to say %q", rec.Body.String(), c.wantText)
			}
			stored, err := h.repo.GetByID(t.Context(), task.ID)
			if err != nil || stored == nil {
				t.Fatalf("GetByID(%q) = %v, %v", task.ID, stored, err)
			}
			var got any
			if stored.RunAs != nil {
				got = *stored.RunAs
			}
			if got != c.wantRunAs {
				t.Errorf("stored runAs = %v, want %v", got, c.wantRunAs)
			}
		})
	}
}

func TestScheduledTaskRunAs_ArtifactDownloadReadsTheRunsOwnFilesOnly(t *testing.T) {
	h, mux := setupRunAsRoutes(t, true)
	task := seedRunAsTask(t, h, runAsSuperAdmin, runAsSystemUser)
	ranAs := runAsSystemUser
	exec := &store.ScheduledTaskExecution{ID: idgen.New(), ScheduledTaskID: task.ID, Status: "completed", ScheduledFor: 1, RunAs: &ranAs}
	if err := h.repo.CreateExecution(t.Context(), exec); err != nil {
		t.Fatal(err)
	}
	session, err := h.sessKeyRepo.GetOrCreateByExternalKey(t.Context(), store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeScheduled, ExternalKey: exec.ID, UserID: runAsSuperAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.txt", "user:shared-secret.txt"} {
		if _, err := h.artSvc.Inner.Save(t.Context(), h.artSvc.Resolve("", runAsSystemUser, session, name), []byte(name), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}

	if rec := serveRunAsRequest(t, mux, runAsSuperAdmin, http.MethodGet, "/api/v1/artifacts/scheduled/"+session+"/report.txt", nil); rec.Code != http.StatusOK || rec.Body.String() != "report.txt" {
		t.Errorf("the run's own file: status %d body %q, want 200 with its bytes", rec.Code, rec.Body.String())
	}
	if rec := serveRunAsRequest(t, mux, runAsSuperAdmin, http.MethodGet, "/api/v1/artifacts/scheduled/"+session+"/user:shared-secret.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("the service account's user-scoped file: status %d, want 404", rec.Code)
	}
}
