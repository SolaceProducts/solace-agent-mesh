// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	ownershipTestOwner = "owner-user@example.com"
	ownershipTestOther = "other-user@example.com"
)

type ownershipEndpoint struct {
	name           string
	method         string
	pattern        string
	path           func(taskID string) string
	handler        func(*taskHandler) http.HandlerFunc
	wantAllowed    int
	writesTaskData bool
}

func ownershipEndpoints() []ownershipEndpoint {
	return []ownershipEndpoint{
		{
			name:        "status",
			method:      http.MethodGet,
			pattern:     "GET /api/v1/tasks/{id}/status",
			path:        func(id string) string { return "/api/v1/tasks/" + id + "/status" },
			handler:     func(h *taskHandler) http.HandlerFunc { return h.handleStatus },
			wantAllowed: http.StatusOK,
		},
		{
			name:        "events",
			method:      http.MethodGet,
			pattern:     "GET /api/v1/tasks/{id}/events",
			path:        func(id string) string { return "/api/v1/tasks/" + id + "/events" },
			handler:     func(h *taskHandler) http.HandlerFunc { return h.handleEvents },
			wantAllowed: http.StatusOK,
		},
		{
			name:        "bufferedEvents",
			method:      http.MethodGet,
			pattern:     "GET /api/v1/tasks/{id}/events/buffered",
			path:        func(id string) string { return "/api/v1/tasks/" + id + "/events/buffered" },
			handler:     func(h *taskHandler) http.HandlerFunc { return h.handleBufferedEvents },
			wantAllowed: http.StatusOK,
		},
		{
			name:           "deleteBufferedEvents",
			method:         http.MethodDelete,
			pattern:        "DELETE /api/v1/tasks/{id}/events/buffered",
			path:           func(id string) string { return "/api/v1/tasks/" + id + "/events/buffered" },
			handler:        func(h *taskHandler) http.HandlerFunc { return h.handleDeleteBufferedEvents },
			wantAllowed:    http.StatusNoContent,
			writesTaskData: true,
		},
		{
			name:        "titleData",
			method:      http.MethodGet,
			pattern:     "GET /api/v1/tasks/{id}/titleData",
			path:        func(id string) string { return "/api/v1/tasks/" + id + "/titleData" },
			handler:     func(h *taskHandler) http.HandlerFunc { return h.handleTitleData },
			wantAllowed: http.StatusOK,
		},
		{
			name:        "stimExport",
			method:      http.MethodGet,
			pattern:     "GET /api/v1/tasks/{id}",
			path:        func(id string) string { return "/api/v1/tasks/" + id },
			handler:     func(h *taskHandler) http.HandlerFunc { return h.handleGetTask },
			wantAllowed: http.StatusOK,
		},
	}
}

func seedOwnedTask(t *testing.T, repo *store.TaskRepo, status string) string {
	t.Helper()
	return seedTask(t, repo, ownershipTestOwner, status)
}

func serveOwnedTask(t *testing.T, ep ownershipEndpoint, caller string, scopes ...string) int {
	t.Helper()
	th, taskRepo, _, _, _ := setupTaskHandler(t)

	taskID := seedOwnedTask(t, taskRepo, "completed")

	mux := http.NewServeMux()
	mux.HandleFunc(ep.pattern, ep.handler(th))

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(ep.method, ep.path(taskID), nil), caller, scopes...)
	mux.ServeHTTP(rec, req)
	return rec.Code
}

func TestTaskEndpoints_Ownership(t *testing.T) {
	allowed := func(ep ownershipEndpoint) int { return ep.wantAllowed }
	refused := func(ownershipEndpoint) int { return http.StatusNotFound }
	reads := func(ep ownershipEndpoint) bool { return !ep.writesTaskData }
	writes := func(ep ownershipEndpoint) bool { return ep.writesTaskData }
	every := func(ownershipEndpoint) bool { return true }

	cases := []struct {
		name      string
		why       string
		caller    string
		scopes    []string
		applies   func(ownershipEndpoint) bool
		want      func(ownershipEndpoint) int
		mustMatch bool
	}{
		{
			name:    "owner is served",
			why:     "the guard must not lock out the person whose task it is",
			caller:  ownershipTestOwner,
			applies: every,
			want:    allowed,
		},
		{
			name:    "cross-user is refused",
			why:     "404 rather than 403: a 403 would confirm the task exists to a caller who only guessed or scraped the id",
			caller:  ownershipTestOther,
			applies: every,
			want:    refused,
		},
		{
			name:    "activity scope reads any task",
			why:     "activity:*:read is the Activity Monitor's cross-user grant, and its UI reads these endpoints for other users' tasks",
			caller:  ownershipTestOther,
			scopes:  []string{rbac.ScopeActivityRead},
			applies: reads,
			want:    allowed,
		},
		{
			name:      "activity scope cannot write",
			why:       "a read scope must not authorize a write: the escalation is \"may observe every task\" becoming \"may destroy every task's data\" — for the DELETE, permanent cross-user loss of the replay buffer",
			caller:    ownershipTestOther,
			scopes:    []string{rbac.ScopeActivityRead},
			applies:   writes,
			want:      refused,
			mustMatch: true,
		},
		{
			name:    "an unrelated scope is not elevated",
			why:     "scope possession is not the grant: a caller holding agent:*:invoke and nothing else must be refused exactly as a caller holding nothing is",
			caller:  ownershipTestOther,
			scopes:  []string{rbac.ScopeAgentInvokeAny},
			applies: every,
			want:    refused,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched := 0
			for _, ep := range ownershipEndpoints() {
				if !tc.applies(ep) {
					continue
				}
				matched++
				t.Run(ep.name, func(t *testing.T) {
					want := tc.want(ep)
					if got := serveOwnedTask(t, ep, tc.caller, tc.scopes...); got != want {
						t.Errorf("got %d, want %d — %s", got, want, tc.why)
					}
				})
			}
			if tc.mustMatch && matched == 0 {
				t.Fatalf("no endpoint matched this row, so it asserts nothing — %s", tc.why)
			}
		})
	}
}

func TestTaskEndpoints_UnknownTaskKeepsPermissiveContract(t *testing.T) {
	permissive := map[string]int{
		"events":               http.StatusOK,
		"bufferedEvents":       http.StatusOK,
		"deleteBufferedEvents": http.StatusNoContent,
	}
	for _, ep := range ownershipEndpoints() {
		want, ok := permissive[ep.name]
		if !ok {
			continue
		}
		t.Run(ep.name, func(t *testing.T) {
			th, _, _, _, _ := setupTaskHandler(t)
			mux := http.NewServeMux()
			mux.HandleFunc(ep.pattern, ep.handler(th))

			rec := httptest.NewRecorder()
			req := withIdentity(httptest.NewRequest(ep.method, ep.path(idgen.New()), nil), ownershipTestOther)
			mux.ServeHTTP(rec, req)

			if rec.Code != want {
				t.Errorf("unknown task got %d, want %d", rec.Code, want)
			}
		})
	}
}

func serveCancel(t *testing.T, caller string, storeTask bool, scopes ...string) int {
	t.Helper()
	th, taskRepo, _, _, _ := setupTaskHandler(t)

	taskID := idgen.New()
	if storeTask {
		taskID = seedOwnedTask(t, taskRepo, "working")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tasks/", th.handleTaskPost)

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+":cancel", nil), caller, scopes...)
	mux.ServeHTTP(rec, req)
	return rec.Code
}

func TestTaskCancel_OwnerIsAccepted(t *testing.T) {
	if got := serveCancel(t, ownershipTestOwner, true); got != http.StatusAccepted {
		t.Errorf("owner got %d, want 202", got)
	}
}

func TestTaskCancel_CrossUserIsNotFound(t *testing.T) {
	if got := serveCancel(t, ownershipTestOther, true); got != http.StatusNotFound {
		t.Errorf("cross-user got %d, want 404", got)
	}
}

func TestTaskCancel_ActivityScopeCannotCancel(t *testing.T) {
	if got := serveCancel(t, ownershipTestOther, true, rbac.ScopeActivityRead); got != http.StatusNotFound {
		t.Errorf("activity-scope holder got %d, want 404 — cancel is a write", got)
	}
}

func TestTaskCancel_UnknownTaskStillAccepted(t *testing.T) {
	if got := serveCancel(t, ownershipTestOther, false); got != http.StatusAccepted {
		t.Errorf("unknown task got %d, want 202", got)
	}
}

func TestBufferedEvents_UnknownTaskWithholdsAnotherUsersRows(t *testing.T) {
	th, _, _, sseRepo, _ := setupTaskHandler(t)

	taskID := idgen.New()
	const ownerSecret = "cross-user-leak-marker"
	if err := sseRepo.Append(context.Background(), &store.SSEEventBufferRow{
		TaskID:        taskID,
		SessionID:     idgen.New(),
		UserID:        ownershipTestOwner,
		EventSequence: 1,
		EventType:     "final_response",
		EventData:     `{"secret":"` + ownerSecret + `"}`,
	}); err != nil {
		t.Fatalf("seed buffered event: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/events/buffered", th.handleBufferedEvents)

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet,
		"/api/v1/tasks/"+taskID+"/events/buffered", nil), ownershipTestOther)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 — the permissive contract still holds", rec.Code)
	}
	if strings.Contains(rec.Body.String(), ownerSecret) {
		t.Errorf("stranger received the owner's buffered event content: %s", rec.Body.String())
	}
}

func TestBufferedEvents_UnattributedRowsStillReachTheOwner(t *testing.T) {
	th, _, _, sseRepo, _ := setupTaskHandler(t)

	taskID := idgen.New()
	const answer = "the-owners-final-answer"
	if err := sseRepo.Append(context.Background(), &store.SSEEventBufferRow{
		TaskID:        taskID,
		SessionID:     idgen.New(),
		UserID:        "",
		EventSequence: 1,
		EventType:     "final_response",
		EventData:     `{"text":"` + answer + `"}`,
	}); err != nil {
		t.Fatalf("seed buffered event: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/events/buffered", th.handleBufferedEvents)

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet,
		"/api/v1/tasks/"+taskID+"/events/buffered", nil), ownershipTestOwner)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), answer) {
		t.Errorf("unattributed row withheld from the owner: %s", rec.Body.String())
	}
}

func TestBufferedEvents_ActivityScopeStillReadsAnotherUsersRows(t *testing.T) {
	th, _, _, sseRepo, _ := setupTaskHandler(t)

	taskID := idgen.New()
	const content = "owner-event-the-monitor-must-see"
	if err := sseRepo.Append(context.Background(), &store.SSEEventBufferRow{
		TaskID:        taskID,
		SessionID:     idgen.New(),
		UserID:        ownershipTestOwner,
		EventSequence: 1,
		EventType:     "final_response",
		EventData:     `{"text":"` + content + `"}`,
	}); err != nil {
		t.Fatalf("seed buffered event: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/events/buffered", th.handleBufferedEvents)

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet,
		"/api/v1/tasks/"+taskID+"/events/buffered", nil), ownershipTestOther, rbac.ScopeActivityRead)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), content) {
		t.Errorf("activity-scope holder was denied the owner's rows: %s", rec.Body.String())
	}
}
