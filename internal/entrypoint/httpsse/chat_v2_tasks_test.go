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
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const completedReply = `{"jsonrpc":"2.0","result":{"kind":"task","status":{"state":"completed","message":{"parts":[{"kind":"text","text":"forty-two"}]}},"artifacts":[{"artifactId":"a1","name":"answer.txt","parts":[{"kind":"text","text":"42"}]}]}}`

func getChatTask(t *testing.T, a *HTTPSSEAdapter, taskID, caller string, scopes ...string) (int, chatv2.Task) {
	t.Helper()
	req := withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/tasks/"+taskID, nil), caller, scopes...)
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	var body struct {
		Data chatv2.Task `json:"data"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body, err)
		}
	}
	return rec.Code, body.Data
}

func finishSweepTask(t *testing.T, a *HTTPSSEAdapter, taskID, status string, errorMessage *string) {
	t.Helper()
	finishSweepTaskAt(t, a, taskID, status, errorMessage, 2000)
}

func finishSweepTaskAt(t *testing.T, a *HTTPSSEAdapter, taskID, status string, errorMessage *string, endTime int64) {
	t.Helper()
	err := store.NewTaskRepo(a.core.Store()).UpdateFinalStatus(context.Background(), taskID, store.FinalStatusUpdate{
		Status: status, EndTime: endTime, ErrorMessage: errorMessage,
	})
	if err != nil {
		t.Fatalf("finish task: %v", err)
	}
}

func TestChatV2GetTaskWorkingHasNoResult(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)

	code, task := getChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if task.Status.State != chatv2.TaskStateWorking || task.Status.Message != nil || len(task.Artifacts) != 0 {
		t.Errorf("a running task shows %+v, want working with no message or artifacts", task)
	}
}

func TestChatV2GetTaskCompletedReturnsTheFinalReply(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	if _, err := a.eventLog.Append(context.Background(), taskID, sweepSession, sweepOwner, "final_response", []byte(completedReply)); err != nil {
		t.Fatal(err)
	}
	waitForSeededBufferRow(t, a, taskID)
	finishSweepTask(t, a, taskID, "completed", nil)

	code, task := getChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateCompleted {
		t.Fatalf("got %d %+v, want 200 completed", code, task)
	}
	if task.Status.Message == nil || len(task.Status.Message.Parts) != 1 || task.Status.Message.Parts[0]["text"] != "forty-two" {
		t.Errorf("message = %+v, want the agent's closing text", task.Status.Message)
	}
	if len(task.Artifacts) != 1 || task.Artifacts[0].ArtifactID != "a1" || task.Artifacts[0].Name != "answer.txt" {
		t.Errorf("artifacts = %+v, want a1 answer.txt", task.Artifacts)
	}
	if task.CompletedTime == 0 {
		t.Error("completedTime is unset for an ended task")
	}
}

func TestChatV2GetTaskFailedCarriesTheErrorMessage(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	reason := "the model was unreachable"
	finishSweepTask(t, a, taskID, "failed", &reason)

	code, task := getChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateFailed {
		t.Fatalf("got %d %+v, want 200 failed", code, task)
	}
	if task.ErrorMessage == nil || *task.ErrorMessage != reason {
		t.Errorf("errorMessage = %v, want %q", task.ErrorMessage, reason)
	}
}

func TestChatV2GetTaskUnknownIsNotFound(t *testing.T) {
	a := newCoverageAdapter(t)

	code, _ := getChatTask(t, a, idgen.New(), sweepOwner)

	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestChatV2GetTaskRejectsAMalformedID(t *testing.T) {
	a := newCoverageAdapter(t)

	code, _ := getChatTask(t, a, "not-a-uuid", sweepOwner)

	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

func TestChatV2GetTaskAnswersBeforeTheTaskLoggerWritesTheRow(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := idgen.New()
	_ = a.core.Tasks().Register(taskID, sweepAgent, sweepOwner, entrypoint.TaskIdentity{SessionID: sweepSession})

	code, task := getChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateWorking {
		t.Errorf("got %d %+v, want 200 working for a task only the registry knows", code, task)
	}
	if task.ContextID == nil || *task.ContextID != sweepSession {
		t.Errorf("contextId = %v, want %s from the registry", task.ContextID, sweepSession)
	}
	if task.CreatedTime == 0 {
		t.Error("createdTime is null for a task the registry knows, want the time it registered")
	}
}

func TestChatV2ExportAnswersForATaskOnlyTheRegistryKnows(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := idgen.New()
	_ = a.core.Tasks().Register(taskID, sweepAgent, sweepOwner, entrypoint.TaskIdentity{SessionID: sweepSession})

	if rec := exportChatTask(t, a, taskID, sweepOwner); rec.Code != http.StatusOK {
		t.Errorf("owner: status = %d, want 200", rec.Code)
	}
	if rec := exportChatTask(t, a, taskID, sweepStranger, rbac.ScopeActivityRead); rec.Code != http.StatusOK {
		t.Errorf("activity reader: status = %d, want 200", rec.Code)
	}
	for name, scopes := range map[string][]string{"no grant": nil, "trace grant only": {rbac.ScopeAgentReadTraceAny}} {
		if rec := exportChatTask(t, a, taskID, sweepStranger, scopes...); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", name, rec.Code)
		}
	}
}

func cancelChatTask(t *testing.T, a *HTTPSSEAdapter, taskID, caller string) (int, chatv2.Task) {
	t.Helper()
	req := withIdentity(httptest.NewRequest(http.MethodPut, chatv2.Prefix+"/tasks/"+taskID+"/cancel", nil), caller)
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	var body struct {
		Data chatv2.Task `json:"data"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body, err)
		}
	}
	return rec.Code, body.Data
}

func TestChatV2CancelEndedTaskReturnsItUnchanged(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	finishSweepTask(t, a, taskID, "completed", nil)

	code, task := cancelChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateCompleted {
		t.Errorf("got %d %+v, want 200 with the task still completed", code, task)
	}
}

func TestChatV2CancelUnknownTaskIsNotFound(t *testing.T) {
	a := newCoverageAdapter(t)

	code, _ := cancelChatTask(t, a, idgen.New(), sweepOwner)

	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

func TestChatV2CancelSomeoneElsesTaskLeavesItRunning(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)

	code, _ := cancelChatTask(t, a, taskID, sweepStranger)

	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if !a.core.IsTaskRunning(taskID) {
		t.Error("a refused cancel stopped the task")
	}
}

func exportChatTask(t *testing.T, a *HTTPSSEAdapter, taskID, caller string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	return getTierAs(t, a, chatv2.Prefix+"/tasks/"+taskID+"/export", caller, scopes...)
}

func TestChatV2ExportIsTheSameFileAsV1(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	root, _ := seedTiersTree(t, a, true)

	v2 := exportChatTask(t, a, root, tiersOwner)
	v1 := getTierAs(t, a, "/api/v1/tasks/"+root, tiersOwner)

	if v2.Code != http.StatusOK || v1.Code != http.StatusOK {
		t.Fatalf("status v2=%d v1=%d, want 200 both", v2.Code, v1.Code)
	}
	if v2.Body.String() != v1.Body.String() {
		t.Error("the v2 export differs from the v1 export of the same task")
	}
	if got := v2.Header().Get("Content-Type"); got != chatv2.ExportTaskFormat {
		t.Errorf("Content-Type = %q, want %q", got, chatv2.ExportTaskFormat)
	}
	if got := v2.Header().Get("Content-Disposition"); !strings.Contains(got, root+".stim") {
		t.Errorf("Content-Disposition = %q, want the root task's .stim file", got)
	}
}

func TestChatV2ExportFollowsTheV1TraceTiers(t *testing.T) {
	a, _, _, _, _ := setupTestAdapterWithDB(t)
	root, _ := seedTiersTree(t, a, true)

	for name, scopes := range map[string][]string{
		"no scopes":                  nil,
		"feedback read, no coverage": {rbac.WorkflowReadFeedbackScope(tiersRootSubj)},
		"both wildcards, feedback":   {rbac.ScopeWorkflowReadFeedbackAny, rbac.ScopeAgentReadTraceAny},
	} {
		if rec := exportChatTask(t, a, root, tiersReviewer, scopes...); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
	if rec := exportChatTask(t, a, root, tiersReviewer, rbac.ScopeActivityRead); rec.Code != http.StatusOK {
		t.Errorf("activity holder: %d, want 200", rec.Code)
	}
}

func TestChatV2ExportUnknownTaskIsNotFound(t *testing.T) {
	a := newCoverageAdapter(t)

	if rec := exportChatTask(t, a, idgen.New(), sweepOwner); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestChatV2GetTaskRegistryOnlyIsHiddenFromStrangersWithoutAGrant(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := idgen.New()
	_ = a.core.Tasks().Register(taskID, sweepAgent, sweepOwner, entrypoint.TaskIdentity{SessionID: sweepSession})

	if code, _ := getChatTask(t, a, taskID, sweepStranger); code != http.StatusNotFound {
		t.Errorf("a stranger without a grant got %d, want 404", code)
	}
	code, task := getChatTask(t, a, taskID, sweepStranger, rbac.ScopeActivityRead)
	if code != http.StatusOK || task.Status.State != chatv2.TaskStateWorking || task.Status.Message != nil {
		t.Errorf("an activity reader got %d %+v, want 200 working with no message", code, task)
	}
}

func TestChatV2GetTaskJustEndedWithoutItsReplyIsNotFinalYet(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	finishSweepTaskAt(t, a, taskID, "completed", nil, time.Now().UnixMilli())

	code, task := getChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateWorking || task.CompletedTime != 0 {
		t.Errorf("got %d %+v, want 200 working with no completedTime until the reply is stored", code, task)
	}
}

func TestChatV2GetTaskFallsBackToTheChatRecordWhenTheBufferIsSwept(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	finishSweepTask(t, a, taskID, "completed", nil)
	if _, err := a.core.Store().ExecContext(context.Background(), `UPDATE tasks SET session_id = ? WHERE id = ?`, sweepSession, taskID); err != nil {
		t.Fatal(err)
	}
	bubbles := `[{"type":"agent","text":"forty-two"}]`
	if err := store.NewChatTaskRepo(a.core.Store()).Create(context.Background(), &store.ChatTask{
		ID: taskID, SessionID: sweepSession, UserID: sweepOwner, MessageBubbles: bubbles, CreatedTime: 1000,
	}); err != nil {
		t.Fatal(err)
	}

	code, task := getChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateCompleted {
		t.Fatalf("got %d %+v, want 200 completed", code, task)
	}
	if task.Status.Message == nil || len(task.Status.Message.Parts) != 1 || task.Status.Message.Parts[0]["text"] != "forty-two" {
		t.Errorf("message = %+v, want the reply kept in the chat record", task.Status.Message)
	}
}

func TestChatV2GetTaskWithAnUnreadableStoredReplyIsAnError(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	if _, err := a.eventLog.Append(context.Background(), taskID, sweepSession, sweepOwner, "final_response", []byte(`{not json`)); err != nil {
		t.Fatal(err)
	}
	waitForSeededBufferRow(t, a, taskID)
	finishSweepTask(t, a, taskID, "completed", nil)

	if code, _ := getChatTask(t, a, taskID, sweepOwner); code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 rather than an empty answer", code)
	}
}

func TestChatV2GetTaskWaitingForTheUserHasNoCompletedTime(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	finishSweepTask(t, a, taskID, "input-required", nil)

	code, task := getChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateInputNeeded || task.CompletedTime != 0 {
		t.Errorf("got %d %+v, want 200 input-required with no completedTime", code, task)
	}
}

func subscribeToAgentRequests(t *testing.T, a *HTTPSSEAdapter, agent string) func(wantTaskID string) bool {
	t.Helper()
	brk := a.core.Services().Broker()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agent)); err != nil {
		t.Fatal(err)
	}
	return func(wantTaskID string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for {
			msg, err := brk.Receive(ctx, q)
			if err != nil {
				return false
			}
			var req struct {
				Method string `json:"method"`
				Params struct {
					ID string `json:"id"`
				} `json:"params"`
			}
			if json.Unmarshal(msg.Payload, &req) == nil && req.Method == "tasks/cancel" && req.Params.ID == wantTaskID {
				return true
			}
		}
	}
}

func TestChatV2CancelRunningTaskAsksTheAgentToStop(t *testing.T) {
	a := newCoverageAdapter(t)
	taskID := seedSweepTask(t, a)
	cancelled := subscribeToAgentRequests(t, a, sweepAgent)

	code, task := cancelChatTask(t, a, taskID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateWorking {
		t.Fatalf("got %d %+v, want 200 working until the agent confirms", code, task)
	}
	if !cancelled(taskID) {
		t.Error("no tasks/cancel request for the task reached the agent")
	}
}

func TestChatV2CancelEndedParentStillStopsItsRunningChild(t *testing.T) {
	a := newCoverageAdapter(t)
	parentID := seedSweepTask(t, a)
	finishSweepTask(t, a, parentID, "timeout", nil)
	childID := idgen.New()
	status := "running"
	if err := store.NewTaskRepo(a.core.Store()).Create(context.Background(), &store.Task{
		ID: childID, UserID: sweepOwner, ParentTaskID: &parentID, StartTime: 1500, Status: &status,
		ExecutionMode: "foreground", Kind: store.TaskKindNormal, HandlerName: ptr(sweepAgent), HandlerType: ptr(store.TaskHandlerTypeAgent),
		AgentName: ptr(sweepAgent),
	}); err != nil {
		t.Fatal(err)
	}
	cancelled := subscribeToAgentRequests(t, a, sweepAgent)

	code, task := cancelChatTask(t, a, parentID, sweepOwner)

	if code != http.StatusOK || task.Status.State != chatv2.TaskStateFailed {
		t.Fatalf("got %d %+v, want 200 with the parent still failed", code, task)
	}
	if !cancelled(childID) {
		t.Error("the running child was not asked to stop")
	}
}
