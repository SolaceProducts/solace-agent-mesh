// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	tiersOwner    = "tiers-owner@example.com"
	tiersReviewer = "tiers-reviewer@example.com"
	tiersPrompt   = "TIERS-CANARY-user-prompt"
	tiersToolArg  = "TIERS-CANARY-tool-arg"
	tiersToolName = "lookup_salary"
	tiersRootSubj = "01a0c9b5-94e6-743d-9896-a779500a1a6c"
	tiersHRSubj   = "hr-bot"
)

func seedTiersTree(t *testing.T, a *HTTPSSEAdapter, rated bool) (string, string) {
	t.Helper()
	ctx := t.Context()
	db := a.core.Store()
	tasks := store.NewTaskRepo(db)
	events := store.NewTaskEventRepo(db)
	str := func(s string) *string { return &s }

	root, child := idgen.New(), idgen.New()
	initial := tiersPrompt
	if err := tasks.Create(ctx, &store.Task{
		ID: root, UserID: tiersOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		InitialRequestText: &initial,
		HandlerName:        str("workflow_01a0c9b5_94e6_743d_9896_a779500a1a6c"), HandlerType: str(store.TaskHandlerTypeWorkflow),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := tasks.Create(ctx, &store.Task{
		ID: child, UserID: tiersOwner, StartTime: 2, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		ParentTaskID: &root, HandlerName: str(tiersHRSubj), HandlerType: str(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := events.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: root, Direction: "request", Topic: "ns/a2a/v1/agent/request/workflow_01a0c9b5_94e6_743d_9896_a779500a1a6c",
		CreatedTime: 1700000000000,
		Payload:     `{"id":"m1","method":"message/stream","params":{"message":{"messageId":"m1","metadata":{"agent_name":"wf"},"parts":[{"kind":"text","text":"` + tiersPrompt + `"}]}}}`,
	}); err != nil {
		t.Fatalf("create root event: %v", err)
	}
	if err := events.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: child, Direction: "status", Topic: "ns/a2a/v1/agent/status/hr-bot",
		CreatedTime: 1700000001000,
		Payload:     `{"id":"m2","result":{"taskId":"` + child + `","status":{"state":"working","message":{"messageId":"m2","metadata":{"agent_name":"hr-bot"},"parts":[{"kind":"data","data":{"type":"tool_invocation_start","function_call_id":"c1","tool_name":"` + tiersToolName + `","tool_args":{"employee":"` + tiersToolArg + `"}}}]}}}}`,
	}); err != nil {
		t.Fatalf("create child event: %v", err)
	}
	if rated {
		if _, err := store.NewFeedbackRepo(db).Submit(ctx, store.FeedbackSubmission{
			TaskID: root, UserID: tiersOwner, SessionID: idgen.New(), Rating: "down",
			SubjectID: tiersRootSubj, SubjectType: store.SubjectTypeWorkflow, EntrypointType: store.EntrypointTypeWebUI,
		}); err != nil {
			t.Fatalf("save feedback: %v", err)
		}
	}
	return root, child
}

func getTierAs(t *testing.T, a *HTTPSSEAdapter, path, caller string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := withIdentity(httptest.NewRequest(http.MethodGet, path, nil), caller, scopes...)
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

func decodeEntries(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return resp.Data
}

func TestTaskTraceTiers_Events(t *testing.T) {
	type want struct {
		status    int
		hasPrompt bool
		hasArg    bool
	}
	tests := []struct {
		desc   string
		caller string
		scopes []string
		want   want
	}{
		{"owner full", tiersOwner, nil, want{200, true, true}},
		{"activity wildcard full", tiersReviewer, []string{rbac.ScopeActivityRead}, want{200, true, true}},
		{"stranger 404", tiersReviewer, nil, want{404, false, false}},
		{"agent feedback wildcard does not open a workflow root: 404", tiersReviewer, []string{rbac.ScopeAgentReadFeedbackAny}, want{404, false, false}},
		{"agent trace wildcard alone: 404", tiersReviewer, []string{rbac.ScopeAgentReadTraceAny}, want{404, false, false}},
		{"workflow read on root alone: 404", tiersReviewer, []string{rbac.WorkflowReadFeedbackScope(tiersRootSubj)}, want{404, false, false}},
		{"workflow read plus every agent trace: 404, a workflow root never opens", tiersReviewer, []string{rbac.WorkflowReadFeedbackScope(tiersRootSubj), rbac.AgentReadTraceScope(tiersHRSubj)}, want{404, false, false}},
		{"workflow read plus agent trace wildcard: 404, a workflow root never opens", tiersReviewer, []string{rbac.WorkflowReadFeedbackScope(tiersRootSubj), rbac.ScopeAgentReadTraceAny}, want{404, false, false}},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			a, _, _, _, _ := setupTestAdapterWithDB(t)
			root, _ := seedTiersTree(t, a, true)
			rec := getTierAs(t, a, "/api/v1/tasks/"+root+"/events", tt.caller, tt.scopes...)
			if rec.Code != tt.want.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.want.status, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, tiersPrompt) != tt.want.hasPrompt {
				t.Errorf("prompt present = %v, want %v; body=%s", !tt.want.hasPrompt, tt.want.hasPrompt, body)
			}
			if strings.Contains(body, tiersToolArg) != tt.want.hasArg {
				t.Errorf("tool arg present = %v, want %v; body=%s", !tt.want.hasArg, tt.want.hasArg, body)
			}
			if tt.want.status != 200 {
				return
			}
			entries := decodeEntries(t, rec)
			if len(entries) != 2 {
				t.Fatalf("expected both tree tasks in the response, got %d", len(entries))
			}
			for _, e := range entries {
				evs, _ := e["events"].([]any)
				if len(evs) != 1 {
					t.Errorf("every event must be served, task %v has %d", e["taskId"], len(evs))
				}
			}
			if !strings.Contains(body, tiersToolName) {
				t.Errorf("tool name must be served; body=%s", body)
			}
		})
	}
}

func TestTaskTraceTiers_UnratedTreeAndUnknownTask(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	root, _ := seedTiersTree(t, a, false)
	if rec := getTierAs(t, a, "/api/v1/tasks/"+root+"/events", tiersReviewer, rbac.ScopeWorkflowReadFeedbackAny, rbac.ScopeAgentReadTraceAny); rec.Code != http.StatusNotFound {
		t.Errorf("workflow root with both wildcards: %d, want 404", rec.Code)
	}
	if rec := getTierAs(t, a, "/api/v1/tasks/"+idgen.New()+"/events", tiersReviewer, rbac.ScopeWorkflowReadFeedbackAny, rbac.ScopeAgentReadTraceAny); rec.Code != http.StatusOK {
		t.Errorf("unknown task: %d, want 200 (empty list)", rec.Code)
	}
}

func TestTaskTraceTiers_StimExport(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	root, _ := seedTiersTree(t, a, true)
	path := "/api/v1/tasks/" + root
	if rec := getTierAs(t, a, path, tiersReviewer, rbac.WorkflowReadFeedbackScope(tiersRootSubj)); rec.Code != http.StatusNotFound {
		t.Errorf("feedback read without trace coverage on STIM: %d, want 404", rec.Code)
	}
	if rec := getTierAs(t, a, path, tiersReviewer, rbac.ScopeWorkflowReadFeedbackAny, rbac.ScopeAgentReadTraceAny); rec.Code != http.StatusNotFound {
		t.Errorf("both wildcards on a workflow-rooted STIM: %d, want 404 (feedback grants open nothing)", rec.Code)
	}
	if rec := getTierAs(t, a, path, tiersReviewer, rbac.ScopeActivityRead); rec.Code != http.StatusOK {
		t.Errorf("activity holder on STIM: %d, want 200 (#4095 behavior)", rec.Code)
	}
}

func TestTaskTraceTiers_BufferedAndStatusUnchanged(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	root, _ := seedTiersTree(t, a, true)
	for _, path := range []string{"/api/v1/tasks/" + root + "/events/buffered", "/api/v1/tasks/" + root + "/status", "/api/v1/tasks/" + root + "/titleData"} {
		if rec := getTierAs(t, a, path, tiersReviewer, rbac.ScopeWorkflowReadFeedbackAny, rbac.ScopeAgentReadTraceAny); rec.Code != http.StatusNotFound {
			t.Errorf("%s with both feedback wildcards: %d, want 404 (feedback tiers do not apply)", path, rec.Code)
		}
	}
}

func TestTaskTraceTiers_RowlessParentWithLandedChildServesNothing(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()
	db := a.core.Store()
	str := func(s string) *string { return &s }

	parent, child := idgen.New(), idgen.New()
	if err := store.NewTaskRepo(db).Create(ctx, &store.Task{
		ID: child, UserID: tiersOwner, StartTime: 2, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		ParentTaskID: &parent, HandlerName: str(tiersHRSubj), HandlerType: str(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := store.NewTaskEventRepo(db).Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: child, Direction: "request", Topic: "ns/a2a/v1/agent/request/hr-bot",
		CreatedTime: 1700000000000,
		Payload:     `{"id":"m1","method":"message/stream","params":{"message":{"messageId":"m1","parts":[{"kind":"text","text":"` + tiersPrompt + `"}]}}}`,
	}); err != nil {
		t.Fatalf("create child event: %v", err)
	}

	rec := getTierAs(t, a, "/api/v1/tasks/"+parent+"/events", tiersReviewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the async-row contract must hold", rec.Code)
	}
	if entries := decodeEntries(t, rec); len(entries) != 0 {
		t.Errorf("entries = %d, want 0; body=%s", len(entries), rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), tiersPrompt) {
		t.Errorf("event content served without a tier decision: %s", rec.Body.String())
	}
}

func TestCollectTaskEventsForIDs_ServesOnlyTheIDsGiven(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()
	root, child := seedTiersTree(t, a, true)
	tasks, events := store.NewTaskRepo(a.core.Store()), store.NewTaskEventRepo(a.core.Store())

	full, _, err := collectTaskEvents(ctx, tasks, events, []string{root}, true, a.logger)
	if err != nil {
		t.Fatalf("collectTaskEvents: %v", err)
	}
	if _, ok := full[child]; !ok {
		t.Fatalf("the unconfined walk must reach the child, got %v", keysOf(full))
	}

	confined, _, err := collectTaskEventsForIDs(ctx, tasks, events, []string{root}, true, a.logger)
	if err != nil {
		t.Fatalf("collectTaskEventsForIDs: %v", err)
	}
	if _, ok := confined[child]; ok {
		t.Errorf("a task outside the authorized set must not be served: %v", keysOf(confined))
	}
	if _, ok := confined[root]; !ok {
		t.Errorf("the authorized task must still be served: %v", keysOf(confined))
	}
	if body := fmt.Sprint(confined); strings.Contains(body, tiersToolArg) {
		t.Errorf("the excluded task's event content leaked into the confined result")
	}
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestTaskTraceTiers_TraceArmOnThePerTaskReads(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()
	tasks := store.NewTaskRepo(a.core.Store())
	const rootBot, childBot, orphanBot = "root-bot", "hr-bot", "orphan-bot"

	create := func(parent *string, handler string) string {
		t.Helper()
		id := idgen.New()
		initial := tiersPrompt
		if err := tasks.Create(ctx, &store.Task{
			ID: id, UserID: tiersOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			ParentTaskID: parent, InitialRequestText: &initial,
			HandlerName: ptr(handler), HandlerType: ptr(store.TaskHandlerTypeAgent),
		}); err != nil {
			t.Fatalf("create task: %v", err)
		}
		a.sseManager.RegisterTask(id, idgen.New(), tiersOwner)
		return id
	}
	root := create(nil, rootBot)
	child := create(&root, childBot)
	missingParent := idgen.New()
	orphan := create(&missingParent, orphanBot)
	agTrace := rbac.AgentReadTraceScope

	type want struct {
		task   string
		scopes []string
		code   int
	}
	cases := []struct {
		desc string
		want want
	}{
		{"root, trace on the root agent only: refused", want{root, []string{agTrace(rootBot)}, http.StatusNotFound}},
		{"root, no grant at all: refused", want{root, nil, http.StatusNotFound}},
		{"root, trace on every agent: served", want{root, []string{agTrace(rootBot), agTrace(childBot)}, http.StatusOK}},
		{"root, trace wildcard: served", want{root, []string{rbac.ScopeAgentReadTraceAny}, http.StatusOK}},
		{"root, agent verb wildcard reaches read_trace: served", want{root, []string{"agent:*:*"}, http.StatusOK}},
		{"child, trace on the child's agent only: refused", want{child, []string{agTrace(childBot)}, http.StatusNotFound}},
		{"child, trace on every agent: served", want{child, []string{agTrace(rootBot), agTrace(childBot)}, http.StatusOK}},
		{"orphan, trace on its own agent: refused", want{orphan, []string{agTrace(orphanBot)}, http.StatusNotFound}},
		{"orphan, trace wildcard: refused", want{orphan, []string{rbac.ScopeAgentReadTraceAny}, http.StatusNotFound}},
	}
	serve := func(t *testing.T, path, task, caller string, scopes ...string) *httptest.ResponseRecorder {
		t.Helper()
		if strings.HasPrefix(path, "/api/v1/sse/") {
			req := withIdentity(httptest.NewRequest(http.MethodGet, path+task, nil), caller, scopes...)
			return serveUntilFirstWrite(t, a, req)
		}
		return getTierAs(t, a, "/api/v1/tasks/"+task+path, caller, scopes...)
	}
	for _, path := range []string{"/status", "/events", ""} {
		if rec := serve(t, path, root, tiersOwner); rec.Code != http.StatusOK {
			t.Fatalf("%s: owner got %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
		for _, tc := range cases {
			t.Run(path+" "+tc.desc, func(t *testing.T) {
				rec := serve(t, path, tc.want.task, tiersReviewer, tc.want.scopes...)
				if rec.Code != tc.want.code {
					t.Errorf("got %d, want %d; body=%s", rec.Code, tc.want.code, rec.Body.String())
				}
				if tc.want.code != http.StatusOK && strings.Contains(rec.Body.String(), tiersPrompt) {
					t.Errorf("refused caller received task content: %s", rec.Body.String())
				}
			})
		}
	}

	for _, path := range []string{"/events/buffered", "/titleData", "/api/v1/sse/subscribe/"} {
		if rec := serve(t, path, root, tiersOwner); rec.Code != http.StatusOK {
			t.Fatalf("%s: owner got %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
		if rec := serve(t, path, root, tiersReviewer, rbac.ScopeActivityRead); rec.Code != http.StatusOK {
			t.Fatalf("%s: activity:*:read got %d, want 200; body=%s", path, rec.Code, rec.Body.String())
		}
		for _, tc := range cases {
			t.Run(path+" "+tc.desc+" (raw frames: refused)", func(t *testing.T) {
				rec := serve(t, path, tc.want.task, tiersReviewer, tc.want.scopes...)
				if rec.Code != http.StatusNotFound {
					t.Errorf("got %d, want 404; body=%s", rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "SSE connection established") {
					t.Error("trace holder was attached to the owner's event stream")
				}
			})
		}
	}
}

func TestTaskTraceTiers_TitleDataNeverServesBufferedFramesToATraceHolder(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()
	const rootBot, canary = "root-bot", "CANARY-peer-answer-in-the-buffer"

	root := idgen.New()
	initial := tiersPrompt
	if err := store.NewTaskRepo(a.core.Store()).Create(ctx, &store.Task{
		ID: root, UserID: tiersOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		InitialRequestText: &initial, HandlerName: ptr(rootBot), HandlerType: ptr(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := store.NewSSEEventRepo(a.core.Store()).Append(ctx, &store.SSEEventBufferRow{
		TaskID: root, SessionID: idgen.New(), UserID: tiersOwner, EventSequence: 1, EventType: "status_update",
		EventData: `{"result":{"final":false,"status":{"message":{"parts":[{"kind":"text","text":"` + canary + `"}],"metadata":{"forwarded_from_peer":"peer-bot"}}}}}`,
	}); err != nil {
		t.Fatalf("append buffered frame: %v", err)
	}
	path := "/api/v1/tasks/" + root + "/titleData"

	for _, served := range []struct {
		desc, caller string
		scopes       []string
	}{
		{"owner", tiersOwner, nil},
		{"activity:*:read", tiersReviewer, []string{rbac.ScopeActivityRead}},
	} {
		rec := getTierAs(t, a, path, served.caller, served.scopes...)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), canary) {
			t.Fatalf("%s: got %d, want 200 with the buffered answer; body=%s", served.desc, rec.Code, rec.Body.String())
		}
	}
	for _, refused := range []struct {
		desc   string
		scopes []string
	}{
		{"trace on the root agent", []string{rbac.AgentReadTraceScope(rootBot)}},
		{"trace on every agent named anywhere", []string{rbac.AgentReadTraceScope(rootBot), rbac.AgentReadTraceScope("peer-bot")}},
		{"trace wildcard", []string{rbac.ScopeAgentReadTraceAny}},
	} {
		rec := getTierAs(t, a, path, tiersReviewer, refused.scopes...)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404; body=%s", refused.desc, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), canary) {
			t.Errorf("%s: buffered peer text served to a trace holder: %s", refused.desc, rec.Body.String())
		}
	}
}

func TestHandleBufferedEvents_OwnerDoesNotReadFramesStampedWithAnotherUser(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()
	task := idgen.New()
	if err := store.NewTaskRepo(a.core.Store()).Create(ctx, &store.Task{
		ID: task, UserID: tiersOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	buffer, session := store.NewSSEEventRepo(a.core.Store()), idgen.New()
	for seq, user := range map[int]string{1: tiersOwner, 2: "somebody-else", 3: ""} {
		if err := buffer.Append(ctx, &store.SSEEventBufferRow{
			TaskID: task, SessionID: session, UserID: user, EventSequence: seq, EventType: "status_update", EventData: `{}`,
		}); err != nil {
			t.Fatalf("append frame %d: %v", seq, err)
		}
	}
	sequences := func(rec *httptest.ResponseRecorder) []int {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		out := make([]int, 0, 3)
		for _, e := range decodeEntries(t, rec) {
			out = append(out, int(e["sequence"].(float64)))
		}
		slices.Sort(out)
		return out
	}
	path := "/api/v1/tasks/" + task + "/events/buffered"
	if got := sequences(getTierAs(t, a, path, tiersOwner)); !slices.Equal(got, []int{1, 3}) {
		t.Errorf("owner replay = %v, want [1 3]", got)
	}
	if got := sequences(getTierAs(t, a, path, tiersReviewer, rbac.ScopeActivityRead)); !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("activity:*:read replay = %v, want [1 2 3]", got)
	}
}
