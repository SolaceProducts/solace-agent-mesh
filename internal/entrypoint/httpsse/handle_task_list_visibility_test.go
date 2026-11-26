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
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	listViewer      = "list-viewer@example.com"
	listOther       = "list-other@example.com"
	listAgentA      = "agent-a"
	listAgentB      = "agent-b"
	listDeployedID  = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	listWorkflowID  = "01a0c9b5-94e6-743d-9896-a779500a1a6c"
	listSystemOwner = rbac.DefaultSystemUserSub
)

type listFixture struct {
	own          string
	ownOnB       string
	otherRootA   string
	systemRootA  string
	childBUnderA string
	orphanB      string
	otherRootB   string
	deployedRoot string
	workflowRoot string
	unclassified string
}

func seedListFixture(t *testing.T, repo *store.TaskRepo) listFixture {
	t.Helper()
	ctx := context.Background()
	start := int64(1000)
	create := func(owner string, parent *string, handler, kind string) string {
		t.Helper()
		id := idgen.New()
		start++
		task := &store.Task{ID: id, UserID: owner, StartTime: start, ExecutionMode: "foreground", Kind: store.TaskKindNormal, ParentTaskID: parent}
		if handler != "" {
			task.HandlerName, task.HandlerType = ptr(handler), ptr(kind)
		}
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		return id
	}
	f := listFixture{
		own:          create(listViewer, nil, listAgentA, store.TaskHandlerTypeAgent),
		ownOnB:       create(listViewer, nil, listAgentB, store.TaskHandlerTypeAgent),
		otherRootA:   create(listOther, nil, listAgentA, store.TaskHandlerTypeAgent),
		systemRootA:  create(listSystemOwner, nil, listAgentA, store.TaskHandlerTypeAgent),
		otherRootB:   create(listOther, nil, listAgentB, store.TaskHandlerTypeAgent),
		deployedRoot: create(listOther, nil, a2a.InstanceName(a2a.KindAgent, listDeployedID), store.TaskHandlerTypeAgent),
		workflowRoot: create(listOther, nil, a2a.InstanceName(a2a.KindWorkflow, listWorkflowID), store.TaskHandlerTypeWorkflow),
		unclassified: create(listOther, nil, "", ""),
	}
	f.childBUnderA = create(listOther, &f.otherRootA, listAgentB, store.TaskHandlerTypeAgent)
	missingParent := idgen.New()
	f.orphanB = create(listOther, &missingParent, listAgentB, store.TaskHandlerTypeAgent)
	return f
}

func listTasksAs(t *testing.T, h *taskHandler, caller string, scopes ...string) []string {
	t.Helper()
	return listTasksWithQueryAs(t, h, caller, "", scopes...)
}

func listTasksWithQueryAs(t *testing.T, h *taskHandler, caller, query string, scopes ...string) []string {
	t.Helper()
	rows := listTaskRowsAs(t, h, caller, query, scopes...)
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func listTaskRowsAs(t *testing.T, h *taskHandler, caller, query string, scopes ...string) map[string]map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", h.handleList)
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks?pageSize=100"+query, nil), caller, scopes...)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list as %s %v: %d %s", caller, scopes, rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[map[string]any]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rows := make(map[string]map[string]any, len(resp.Data))
	for _, row := range resp.Data {
		rows[row["id"].(string)] = row
	}
	return rows
}

func assertSameIDs(t *testing.T, got, want []string) {
	t.Helper()
	want = append([]string{}, want...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %d rows %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %s, want %s (got %v)", i, got[i], want[i], got)
		}
	}
}

func TestHandleTaskList_GrantHolderFiltersStayWithinVisibility(t *testing.T) {
	th, repo, _, _, _ := setupTaskHandler(t)
	f := seedListFixture(t, repo)
	traceA := []string{rbac.AgentReadTraceScope(listAgentA)}
	activityRead := []string{rbac.ScopeActivityRead}

	tests := []struct {
		desc   string
		query  string
		scopes []string
		want   []string
	}{
		{"owner filter under a trace grant keeps that owner's roots in reach", "&userId=" + listOther, traceA, []string{f.otherRootA}},
		{"owner filter on the caller under a trace grant keeps their own rows", "&userId=" + listViewer, traceA, []string{f.own, f.ownOnB}},
		{"owner filter under activity read keeps every row of that owner",
			"&userId=" + listOther, activityRead, []string{f.otherRootA, f.otherRootB, f.deployedRoot, f.workflowRoot, f.unclassified, f.childBUnderA, f.orphanB}},
		{"search by id finds another owner's root in reach", "&search=" + f.otherRootA, traceA, []string{f.otherRootA}},
		{"search by id never reaches a root outside the trace reach", "&search=" + f.otherRootB, traceA, nil},
		{"search by owner under a trace grant lists that owner's roots in reach", "&search=" + listOther, traceA, []string{f.otherRootA}},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			assertSameIDs(t, listTasksWithQueryAs(t, th, listViewer, tt.query, tt.scopes...), tt.want)
		})
	}
}

func TestHandleTaskList_ExpandSessionNameStaysWithinSessionReach(t *testing.T) {
	th, repo, _, _, _ := setupTaskHandler(t)
	sessRepo := store.NewSessionRepo(th.core.Store())
	ownSess, otherSess := idgen.New(), idgen.New()
	ownTitle, otherTitle := "Mine", "Theirs"
	mustCreateSession(t, sessRepo, &store.Session{ID: ownSess, UserID: listViewer, Name: &ownTitle})
	mustCreateSession(t, sessRepo, &store.Session{ID: otherSess, UserID: listOther, Name: &otherTitle})
	own, other := idgen.New(), idgen.New()
	rootOnA := func(id, owner string, sess *string) *store.Task {
		return &store.Task{ID: id, UserID: owner, StartTime: 1000, ExecutionMode: "foreground", Kind: store.TaskKindNormal, HandlerName: ptr(listAgentA), HandlerType: ptr(store.TaskHandlerTypeAgent), SessionID: sess}
	}
	mustCreateTask(context.Background(), t, repo, rootOnA(own, listViewer, &ownSess))
	mustCreateTask(context.Background(), t, repo, rootOnA(other, listOther, &otherSess))

	titles := func(scopes ...string) map[string]any {
		rows := listTaskRowsAs(t, th, listViewer, "&expand=sessionName", scopes...)
		return map[string]any{own: rows[own]["sessionName"], other: rows[other]["sessionName"]}
	}
	if got := titles(rbac.AgentReadTraceScope(listAgentA)); got[own] != ownTitle || got[other] != nil {
		t.Errorf("trace grant: titles = %v, want own %q and the other row untitled", got, ownTitle)
	}
	if got := titles(rbac.ScopeActivityRead); got[own] != ownTitle || got[other] != otherTitle {
		t.Errorf("activity read: titles = %v, want %q and %q", got, ownTitle, otherTitle)
	}
}

func TestHandleTaskList_VisibilityByGrant(t *testing.T) {
	th, repo, _, _, _ := setupTaskHandler(t)
	f := seedListFixture(t, repo)
	agTrace := rbac.AgentReadTraceScope

	ownOnly := []string{f.own, f.ownOnB}
	tests := []struct {
		desc   string
		scopes []string
		want   []string
	}{
		{"no grant lists own tasks only", nil, ownOnly},
		{"feedback read on an agent lists nothing more", []string{rbac.AgentReadFeedbackScope(listAgentA)}, ownOnly},
		{"an unrelated scope lists nothing more", []string{rbac.ScopeAgentInvokeAny}, ownOnly},
		{"trace on agent-b lists its roots, not a child or an orphan it handled",
			[]string{agTrace(listAgentB)}, []string{f.own, f.ownOnB, f.otherRootB}},
		{"trace on agent-a lists every root agent-a ran, system users included, not its children",
			[]string{agTrace(listAgentA)}, []string{f.own, f.ownOnB, f.otherRootA, f.systemRootA}},
		{"trace on a deployed agent matches its card name through the dashed uuid",
			[]string{agTrace(listDeployedID)}, []string{f.own, f.ownOnB, f.deployedRoot}},
		{"a grant written with the card name itself lists nothing: the detail reads could not open it",
			[]string{agTrace(a2a.InstanceName(a2a.KindAgent, listDeployedID))}, ownOnly},
		{"trace wildcard lists every agent-rooted task but no workflow or unclassified root",
			[]string{rbac.ScopeAgentReadTraceAny}, []string{f.own, f.ownOnB, f.otherRootA, f.systemRootA, f.otherRootB, f.deployedRoot}},
		{"activity read lists everything",
			[]string{rbac.ScopeActivityRead}, []string{f.own, f.ownOnB, f.otherRootA, f.systemRootA, f.otherRootB, f.deployedRoot, f.workflowRoot, f.unclassified, f.childBUnderA, f.orphanB}},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			assertSameIDs(t, listTasksAs(t, th, listViewer, tt.scopes...), tt.want)
		})
	}
}

func TestHandleTaskList_NoIdentityListsOwnRowsOnly(t *testing.T) {
	th, repo, _, _, _ := setupTaskHandler(t)
	f := seedListFixture(t, repo)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tasks?pageSize=100", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, id := range []string{f.otherRootA, f.systemRootA, f.workflowRoot} {
		if strings.Contains(body, id) {
			t.Errorf("anonymous list returned another identity's task %s", id)
		}
	}
}

func TestHandleTaskList_AuditsCrossUserRows(t *testing.T) {
	th, repo, _, _, _ := setupTaskHandler(t)
	f := seedListFixture(t, repo)

	var logs bytes.Buffer
	th.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	for _, tc := range []struct {
		desc   string
		scopes []string
		want   []string
	}{
		{"trace grant", []string{rbac.AgentReadTraceScope(listAgentA)}, []string{"task list", "callerUser=" + listViewer, "crossUser=true", "crossUserCount=2", "grant=" + grantAgentTrace}},
		{"activity read", []string{rbac.ScopeActivityRead}, []string{"task list", "crossUser=true", "crossUserCount=" + strconv.Itoa(len([]string{f.otherRootA, f.systemRootA, f.otherRootB, f.deployedRoot, f.workflowRoot, f.unclassified, f.childBUnderA, f.orphanB})), "grant=" + grantActivityRead}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			logs.Reset()
			listTasksAs(t, th, listViewer, tc.scopes...)
			for _, want := range tc.want {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("audit line missing %q: %s", want, logs.String())
				}
			}
		})
	}

	logs.Reset()
	listTasksAs(t, th, listViewer)
	if strings.Contains(logs.String(), "task list") {
		t.Errorf("an own-rows-only list must not be audited as cross-user: %s", logs.String())
	}
}
