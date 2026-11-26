// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestClientGone(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"context canceled", context.Canceled, true},
		{"context deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped canceled", fmt.Errorf("get parent chain: %w", context.Canceled), true},
		{"wrapped deadline", fmt.Errorf("query: %w", context.DeadlineExceeded), true},
		{"wrapped non-context error", fmt.Errorf("db down: %w", errors.New("conn refused")), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientGone(tt.err); got != tt.want {
				t.Errorf("clientGone(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestExtractEntities_Request(t *testing.T) {
	payload := map[string]any{
		"params": map[string]any{
			"message": map[string]any{
				"metadata": map[string]any{
					"agent_name": "my-agent",
				},
			},
		},
	}
	source, target := extractEntities("request", payload)
	if target != "my-agent" {
		t.Errorf("target = %q, want %q", target, "my-agent")
	}
	if source != "unknown" {
		t.Errorf("source = %q, want %q", source, "unknown")
	}
}

func TestExtractEntities_Response(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"metadata": map[string]any{
				"agent_name": "resp-agent",
			},
		},
	}
	source, target := extractEntities("response", payload)
	if source != "resp-agent" {
		t.Errorf("source = %q, want %q", source, "resp-agent")
	}
	if target != "unknown" {
		t.Errorf("target = %q, want %q", target, "unknown")
	}
}

func TestExtractEntities_ResponseFromTaskMetadata(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"task": map[string]any{
				"metadata": map[string]any{
					"agent_name": "task-agent",
				},
			},
		},
	}
	source, _ := extractEntities("response", payload)
	if source != "task-agent" {
		t.Errorf("source = %q, want %q", source, "task-agent")
	}
}

func TestExtractEntities_StatusFromStatusMessage(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"status": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{
						"agent_name": "status-agent",
					},
				},
			},
		},
	}
	source, _ := extractEntities("status", payload)
	if source != "status-agent" {
		t.Errorf("source = %q, want %q", source, "status-agent")
	}
}

func TestExtractEntities_ErrorDirection(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"metadata": map[string]any{
				"agent_name": "error-agent",
			},
		},
	}
	source, _ := extractEntities("error", payload)
	if source != "error-agent" {
		t.Errorf("source = %q, want %q", source, "error-agent")
	}
}

func TestExtractEntities_UnknownDirection(t *testing.T) {
	payload := map[string]any{}
	source, target := extractEntities("other", payload)
	if source != "unknown" {
		t.Errorf("source = %q, want %q", source, "unknown")
	}
	if target != "unknown" {
		t.Errorf("target = %q, want %q", target, "unknown")
	}
}

func TestExtractEntities_EmptyPayload(t *testing.T) {
	source, target := extractEntities("request", map[string]any{})
	if source != "unknown" || target != "unknown" {
		t.Errorf("source=%q target=%q, want both unknown", source, target)
	}
}

func TestExtractAgentResponseFromBubbles(t *testing.T) {
	tests := []struct {
		name     string
		bubbles  string
		expected string
	}{
		{"empty string", "", ""},
		{"invalid JSON", "not-json", ""},
		{"no agent role", `[{"role":"user","parts":[{"text":"hello"}]}]`, ""},
		{"agent role with text", `[{"role":"agent","parts":[{"text":"response text"}]}]`, "response text"},
		{"assistant role with text", `[{"role":"assistant","parts":[{"text":"assistant text"}]}]`, "assistant text"},
		{"agent role no text part", `[{"role":"agent","parts":[{"image":"data"}]}]`, ""},
		{"agent role empty text", `[{"role":"agent","parts":[{"text":""}]}]`, ""},
		{"multiple bubbles takes first agent", `[{"role":"user","parts":[{"text":"q"}]},{"role":"agent","parts":[{"text":"answer"}]}]`, "answer"},
		{"parts not array", `[{"role":"agent","parts":"invalid"}]`, ""},
		{"part not object", `[{"role":"agent","parts":["string-part"]}]`, ""},
		{"web-ui type agent top-level text", `[{"type":"user","text":"q"},{"type":"agent","text":"the full answer","parts":[{"kind":"text","text":"the "}]}]`, "the full answer"},
		{"web-ui type agent parts fallback", `[{"type":"agent","parts":[{"kind":"text","text":"from parts"}]}]`, "from parts"},
		{"web-ui type user skipped", `[{"type":"user","text":"just a question"}]`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAgentResponseFromBubbles(tt.bubbles)
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestExtractTextFromSSEEvent_Task(t *testing.T) {
	event := `{"result":{"task":{"artifacts":[{"parts":[{"kind":"text","text":"hello world"}]}]}}}`
	got := extractTextFromSSEEvent(event, "task")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestExtractTextFromSSEEvent_TaskNoArtifacts(t *testing.T) {
	event := `{"result":{"task":{}}}`
	got := extractTextFromSSEEvent(event, "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_TaskNoTask(t *testing.T) {
	event := `{"result":{}}`
	got := extractTextFromSSEEvent(event, "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_TaskNonTextKind(t *testing.T) {
	event := `{"result":{"task":{"artifacts":[{"parts":[{"kind":"image","data":"abc"}]}]}}}`
	got := extractTextFromSSEEvent(event, "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_TaskBadParts(t *testing.T) {
	event := `{"result":{"task":{"artifacts":[{"parts":"not-array"}]}}}`
	got := extractTextFromSSEEvent(event, "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_TaskBadArtifact(t *testing.T) {
	event := `{"result":{"task":{"artifacts":["not-object"]}}}`
	got := extractTextFromSSEEvent(event, "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_TaskBadPartItem(t *testing.T) {
	event := `{"result":{"task":{"artifacts":[{"parts":["not-object"]}]}}}`
	got := extractTextFromSSEEvent(event, "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_StatusUpdate(t *testing.T) {
	event := `{"result":{"status":{"message":{"parts":[{"text":"streaming text"}]}}}}`
	got := extractTextFromSSEEvent(event, "status-update")
	if got != "streaming text" {
		t.Errorf("got %q, want %q", got, "streaming text")
	}
}

func TestExtractTextFromSSEEvent_StatusUpdateNoStatus(t *testing.T) {
	event := `{"result":{}}`
	got := extractTextFromSSEEvent(event, "status-update")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_StatusUpdateNoMessage(t *testing.T) {
	event := `{"result":{"status":{}}}`
	got := extractTextFromSSEEvent(event, "status-update")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_StatusUpdateNoParts(t *testing.T) {
	event := `{"result":{"status":{"message":{}}}}`
	got := extractTextFromSSEEvent(event, "status-update")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_StatusUpdateBadPart(t *testing.T) {
	event := `{"result":{"status":{"message":{"parts":["not-object"]}}}}`
	got := extractTextFromSSEEvent(event, "status-update")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_InvalidJSON(t *testing.T) {
	got := extractTextFromSSEEvent("not-json", "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_NoResult(t *testing.T) {
	got := extractTextFromSSEEvent(`{}`, "task")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextFromSSEEvent_UnknownKind(t *testing.T) {
	event := `{"result":{"task":{"artifacts":[{"parts":[{"kind":"text","text":"hi"}]}]}}}`
	got := extractTextFromSSEEvent(event, "unknown-kind")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFormatTaskEvent(t *testing.T) {
	task1 := idgen.New()
	payload := `{"id":"msg-1","method":"tasks/send","params":{"message":{"metadata":{"agent_name":"test-agent"}}}}`
	event := &store.TaskEvent{
		ID:          idgen.New(),
		TaskID:      task1,
		Direction:   "request",
		Topic:       "test/topic",
		Payload:     payload,
		CreatedTime: 1700000000000,
	}

	result := formatTaskEvent(event, nil)

	if result["event_type"] != "a2a_message" {
		t.Errorf("event_type = %v, want a2a_message", result["event_type"])
	}
	if result["direction"] != "request" {
		t.Errorf("direction = %v, want request", result["direction"])
	}
	if result["target_entity"] != "test-agent" {
		t.Errorf("target_entity = %v, want test-agent", result["target_entity"])
	}
	if result["message_id"] != "msg-1" {
		t.Errorf("message_id = %v, want msg-1", result["message_id"])
	}
	if result["task_id"] != task1 {
		t.Errorf("task_id = %v, want task-1", result["task_id"])
	}

	summary, ok := result["payload_summary"].(map[string]any)
	if !ok {
		t.Fatal("payload_summary is not a map")
	}
	if summary["method"] != "tasks/send" {
		t.Errorf("method = %v, want tasks/send", summary["method"])
	}
}

func TestFormatTaskEvent_DirectionMapping(t *testing.T) {
	tests := []struct {
		direction string
		expected  string
	}{
		{"request", "request"},
		{"response", "task"},
		{"status", "status-update"},
		{"error", "error_response"},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.direction, func(t *testing.T) {
			event := &store.TaskEvent{
				Direction: tt.direction,
				Payload:   `{}`,
			}
			result := formatTaskEvent(event, nil)
			if result["direction"] != tt.expected {
				t.Errorf("direction = %v, want %v", result["direction"], tt.expected)
			}
		})
	}
}

func TestFormatTaskEvent_NilPayload(t *testing.T) {
	event := &store.TaskEvent{
		Direction: "request",
		Payload:   "not-valid-json",
	}
	result := formatTaskEvent(event, nil)
	if result["source_entity"] != "unknown" {
		t.Errorf("source_entity = %v, want unknown", result["source_entity"])
	}
}

func TestTaskToStatusResponse(t *testing.T) {
	parent1 := idgen.New()
	sess1 := idgen.New()
	task1 := idgen.New()
	status := "completed"
	endTime := int64(1700000001000)
	reqText := "hello"
	sessID := sess1
	parentID := parent1
	inTokens := 100
	outTokens := 200

	task := &store.Task{
		ID:                 task1,
		UserID:             "user-1",
		Status:             &status,
		StartTime:          1700000000000,
		EndTime:            &endTime,
		ExecutionMode:      "foreground",
		InitialRequestText: &reqText,
		SessionID:          &sessID,
		ParentTaskID:       &parentID,
		TotalInputTokens:   &inTokens,
		TotalOutputTokens:  &outTokens,
		EventsBuffered:     true,
		EventsConsumed:     true,
	}

	resp := taskToStatusResponse(task)

	if resp["id"] != task1 {
		t.Errorf("id = %v, want task-1", resp["id"])
	}
	if resp["status"] != "completed" {
		t.Errorf("status = %v, want completed", resp["status"])
	}
	if resp["parentTaskId"] != parent1 {
		t.Errorf("parentTask_id = %v, want parent-1", resp["parentTaskId"])
	}
	if resp["sessionId"] != sess1 {
		t.Errorf("session_id = %v, want sess-1", resp["sessionId"])
	}
	if resp["initialRequestText"] != "hello" {
		t.Errorf("initial_request_text = %v, want hello", resp["initialRequestText"])
	}
	if resp["totalInputTokens"] == nil {
		t.Error("total_input_tokens should not be nil")
	}
	if resp["totalOutputTokens"] == nil {
		t.Error("total_output_tokens should not be nil")
	}
	if resp["eventsBuffered"] != true {
		t.Errorf("events_buffered = %v, want true", resp["eventsBuffered"])
	}
	if resp["endTime"] == nil {
		t.Error("end_time should not be nil")
	}
}

func TestTaskToStatusResponse_MinimalFields(t *testing.T) {
	task := &store.Task{
		ID:            idgen.New(),
		UserID:        "user-1",
		StartTime:     1700000000000,
		ExecutionMode: "foreground",
	}

	resp := taskToStatusResponse(task)

	if _, ok := resp["parentTaskId"]; ok {
		t.Error("parentTask_id should be absent")
	}
	if _, ok := resp["endTime"]; ok {
		t.Error("end_time should be absent")
	}
	if _, ok := resp["status"]; ok {
		t.Error("status should be absent")
	}
	if _, ok := resp["sessionId"]; ok {
		t.Error("session_id should be absent")
	}
	if _, ok := resp["totalInputTokens"]; ok {
		t.Error("total_input_tokens should be absent")
	}
	if _, ok := resp["trigger"]; ok {
		t.Error("trigger should be absent when the request stamped none")
	}
}

func TestTaskToStatusResponse_SaysWhatStartedTheTask(t *testing.T) {
	str := func(s string) *string { return &s }
	task := &store.Task{
		ID:             idgen.New(),
		UserID:         "system:default",
		StartTime:      1700000000000,
		ExecutionMode:  "foreground",
		TriggerKind:    str("event"),
		Channel:        str("webhook"),
		EntrypointID:   str("0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"),
		EntrypointName: str("github-hook"),
		TriggerSource: store.EncodeTaskTriggerSource(store.TaskTriggerSource{
			Route:      "pull-requests",
			Event:      "check_suite.completed",
			DeliveryID: "72d3162e-cc78-11e3-81ab-4c9367dc0958",
		}),
	}

	trigger, _ := taskToStatusResponse(task)["trigger"].(map[string]any)

	want := map[string]any{
		"kind":           "event",
		"channel":        "webhook",
		"entrypointId":   "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42",
		"entrypointName": "github-hook",
		"route":          "pull-requests",
		"event":          "check_suite.completed",
		"deliveryId":     "72d3162e-cc78-11e3-81ab-4c9367dc0958",
	}
	if !reflect.DeepEqual(trigger, want) {
		t.Errorf("trigger = %#v, want %#v", trigger, want)
	}
}

func TestTaskToStatusResponse_TriggerLeavesOutWhatTheRequestDidNotSay(t *testing.T) {
	interactive := "interactive"
	task := &store.Task{ID: idgen.New(), UserID: "user-1", StartTime: 1700000000000, ExecutionMode: "foreground", TriggerKind: &interactive}

	trigger, _ := taskToStatusResponse(task)["trigger"].(map[string]any)

	if want := map[string]any{"kind": "interactive"}; !reflect.DeepEqual(trigger, want) {
		t.Errorf("trigger = %#v, want %#v", trigger, want)
	}
}

func TestTaskToStatusResponse_UnreadableTriggerSourceStillReturnsTheRest(t *testing.T) {
	event, garbled := "event", "{not json"
	task := &store.Task{ID: idgen.New(), UserID: "system:default", StartTime: 1700000000000, ExecutionMode: "foreground", TriggerKind: &event, TriggerSource: &garbled}

	trigger, _ := taskToStatusResponse(task)["trigger"].(map[string]any)

	if want := map[string]any{"kind": "event"}; !reflect.DeepEqual(trigger, want) {
		t.Errorf("trigger = %#v, want %#v", trigger, want)
	}
}

func TestHandleTaskList_RowsSayWhatRanAndWhatStartedThem(t *testing.T) {
	th, repo, _, _, _ := setupTaskHandler(t)
	str := func(s string) *string { return &s }
	workflowID := "01a0c9b5-94e6-743d-9896-a779500a1a6c"
	task := &store.Task{
		ID:             idgen.New(),
		UserID:         "list-trigger@example.com",
		StartTime:      1700000000000,
		ExecutionMode:  "foreground",
		Kind:           store.TaskKindNormal,
		HandlerName:    str(a2a.InstanceName(a2a.KindWorkflow, workflowID)),
		HandlerType:    str(store.TaskHandlerTypeWorkflow),
		TriggerKind:    str("event"),
		Channel:        str("webhook"),
		EntrypointName: str("github-hook"),
		TriggerSource:  store.EncodeTaskTriggerSource(store.TaskTriggerSource{Route: "deps", Event: "check_suite.completed", DeliveryID: "d-1"}),
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil), task.UserID))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[map[string]any]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("got %d rows, want 1", len(resp.Data))
	}
	row := resp.Data[0]

	wantTrigger := map[string]any{"kind": "event", "channel": "webhook", "entrypointName": "github-hook", "route": "deps", "event": "check_suite.completed", "deliveryId": "d-1"}
	if !reflect.DeepEqual(row["trigger"], wantTrigger) {
		t.Errorf("trigger = %#v, want %#v", row["trigger"], wantTrigger)
	}
	subject, _ := row["subject"].(map[string]any)
	if subject["id"] != workflowID || subject["type"] != store.SubjectTypeWorkflow {
		t.Errorf("subject = %#v, want the workflow %s", subject, workflowID)
	}
}

func TestParseTaskSort(t *testing.T) {
	cases := []struct {
		raw       string
		wantField store.TaskSortField
		wantDir   store.TaskSortDir
		wantErr   bool
	}{
		{"", store.TaskSortStartTime, store.TaskSortDesc, false},
		{"startTime:desc", store.TaskSortStartTime, store.TaskSortDesc, false},
		{"lastActivityTime:desc", store.TaskSortLastActivity, store.TaskSortDesc, false},
		{"lastActivityTime:asc", store.TaskSortLastActivity, store.TaskSortAsc, false},
		{"startTime", store.TaskSortStartTime, store.TaskSortDesc, false},
		{"createdAt:desc", "", "", true},
		{"startTime:sideways", "", "", true},
	}
	for _, tc := range cases {
		gotField, gotDir, err := parseTaskSort(tc.raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseTaskSort(%q) err = %v, wantErr=%v", tc.raw, err, tc.wantErr)
			continue
		}
		if tc.wantErr {
			continue
		}
		if gotField != tc.wantField || gotDir != tc.wantDir {
			t.Errorf("parseTaskSort(%q) = (%s,%s), want (%s,%s)", tc.raw, gotField, gotDir, tc.wantField, tc.wantDir)
		}
	}
}

func TestHandleTaskList_KindAndSort(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)

	taskRepo := store.NewTaskRepo(core.Store())
	eventRepo := store.NewTaskEventRepo(core.Store())
	sessRepo := store.NewSessionRepo(core.Store())
	chatRepo := store.NewChatTaskRepo(core.Store())

	ctx := context.Background()
	taskA := idgen.New()
	taskB := idgen.New()
	comp := idgen.New()
	la1, la2, la3 := int64(3000), int64(2000), int64(1500)
	for _, ts := range []*store.Task{
		{ID: taskA, UserID: "sam_dev_user", StartTime: 1000, ExecutionMode: "foreground", LastActivityTime: &la1, Kind: store.TaskKindNormal},
		{ID: taskB, UserID: "sam_dev_user", StartTime: 2000, ExecutionMode: "foreground", LastActivityTime: &la2, Kind: store.TaskKindNormal},
		{ID: comp, UserID: "sam_dev_user", StartTime: 3000, ExecutionMode: "foreground", LastActivityTime: &la3, Kind: store.TaskKindCompactionCost},
	} {
		if err := taskRepo.Create(ctx, ts); err != nil {
			t.Fatal(err)
		}
	}

	th := &taskHandler{
		core:      adapter.core,
		taskRepo:  taskRepo,
		eventRepo: eventRepo,
		sseRepo:   sseRepo,
		sessRepo:  sessRepo,
		chatRepo:  chatRepo,
		logger:    adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)

	get := func(path string) (int, PaginatedResponse[map[string]any]) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		mux.ServeHTTP(rec, req)
		var resp PaginatedResponse[map[string]any]
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		return rec.Code, resp
	}

	code, resp := get("/api/v1/tasks")
	if code != http.StatusOK {
		t.Fatalf("default: code=%d", code)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("default: len=%d, want 2 (compaction-cost should be hidden)", len(resp.Data))
	}
	if resp.Data[0]["id"] != taskB || resp.Data[1]["id"] != taskA {
		t.Errorf("default order = [%v,%v], want [%s,%s]", resp.Data[0]["id"], resp.Data[1]["id"], taskB, taskA)
	}
	if resp.Data[0]["lastActivityTime"] == nil {
		t.Error("lastActivityTime missing from response")
	}

	code, resp = get("/api/v1/tasks?sort=lastActivityTime:desc")
	if code != http.StatusOK {
		t.Fatalf("lastActivity sort: code=%d", code)
	}
	if len(resp.Data) != 2 || resp.Data[0]["id"] != taskA || resp.Data[1]["id"] != taskB {
		t.Errorf("lastActivity order wrong: got [%v,%v]", resp.Data[0]["id"], resp.Data[1]["id"])
	}

	code, resp = get("/api/v1/tasks?kind=all")
	if code != http.StatusOK {
		t.Fatalf("kind=all: code=%d", code)
	}
	if len(resp.Data) != 3 {
		t.Errorf("kind=all len = %d, want 3", len(resp.Data))
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks?sort=bogus:desc", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad sort: code=%d, want 400", rec.Code)
	}
	var errResp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.ValidationDetails["sort"] == nil {
		t.Errorf("bad sort missing validationDetails.sort: %+v", errResp)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/tasks?kind=nope", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: code=%d, want 400", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.ValidationDetails["kind"] == nil {
		t.Errorf("bad kind missing validationDetails.kind: %+v", errResp)
	}
}

func TestHandleTaskList_Empty(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)

	taskRepo := store.NewTaskRepo(core.Store())
	eventRepo := store.NewTaskEventRepo(core.Store())
	sessRepo := store.NewSessionRepo(core.Store())
	chatRepo := store.NewChatTaskRepo(core.Store())

	th := &taskHandler{
		core:      adapter.core,
		taskRepo:  taskRepo,
		eventRepo: eventRepo,
		sseRepo:   sseRepo,
		sessRepo:  sessRepo,
		chatRepo:  chatRepo,
		logger:    adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[map[string]any]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty data, got %d items", len(resp.Data))
	}
	if resp.Meta.Pagination.Count != 0 {
		t.Errorf("expected count=0, got %d", resp.Meta.Pagination.Count)
	}
}

func TestHandleTaskStatus(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)

	taskRepo := store.NewTaskRepo(core.Store())
	eventRepo := store.NewTaskEventRepo(core.Store())

	ctx := context.Background()
	task1 := idgen.New()
	status := "running"
	task := &store.Task{
		ID:            task1,
		UserID:        "sam_dev_user",
		Status:        &status,
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground",
	}
	if err := taskRepo.Create(ctx, task); err != nil {
		t.Fatal(err)
	}

	th := &taskHandler{
		core:      adapter.core,
		taskRepo:  taskRepo,
		eventRepo: eventRepo,
		sseRepo:   sseRepo,
		logger:    adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/status", th.handleStatus)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+task1+"/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"task", "isRunning", "isBackground", "canReconnect"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing key: %s", key)
		}
	}

	if raw["isBackground"] != false {
		t.Errorf("expected is_background=false, got %v", raw["isBackground"])
	}
}

func TestHandleTaskStatus_FailedTaskCarriesErrorMessage(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)

	taskRepo := store.NewTaskRepo(core.Store())
	eventRepo := store.NewTaskEventRepo(core.Store())

	ctx := context.Background()
	taskID := idgen.New()
	failed := "failed"
	now := time.Now().UnixMilli()
	task := &store.Task{
		ID:            taskID,
		UserID:        "sam_dev_user",
		Status:        &failed,
		StartTime:     now - 100,
		EndTime:       &now,
		ExecutionMode: "background",
	}
	if err := taskRepo.Create(ctx, task); err != nil {
		t.Fatal(err)
	}

	errEvt := &store.TaskEvent{
		ID:          idgen.New(),
		TaskID:      taskID,
		CreatedTime: now,
		Topic:       "gateway/response/" + taskID,
		Direction:   "response",
		Payload:     `{"jsonrpc":"2.0","id":"` + taskID + `","error":{"code":-32603,"message":"LLM error: model alias \"planning\" unbound"}}`,
	}
	earlierEvt := &store.TaskEvent{
		ID:          idgen.New(),
		TaskID:      taskID,
		CreatedTime: now - 50,
		Topic:       "gateway/status/" + taskID,
		Direction:   "status",
		Payload:     `{"jsonrpc":"2.0","result":{"kind":"status-update"}}`,
	}
	if err := eventRepo.Create(ctx, earlierEvt); err != nil {
		t.Fatal(err)
	}
	if err := eventRepo.Create(ctx, errEvt); err != nil {
		t.Fatal(err)
	}

	th := &taskHandler{
		core:      adapter.core,
		taskRepo:  taskRepo,
		eventRepo: eventRepo,
		sseRepo:   sseRepo,
		logger:    adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/status", th.handleStatus)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+taskID+"/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	msg, _ := resp["errorMessage"].(string)
	if !strings.Contains(msg, "model alias") || !strings.Contains(msg, "unbound") {
		t.Errorf("expected error_message to carry the LLM error text, got %q", msg)
	}
}

func TestHandleTaskStatus_RunningTaskHasNilErrorMessage(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)

	taskRepo := store.NewTaskRepo(core.Store())
	eventRepo := store.NewTaskEventRepo(core.Store())

	ctx := context.Background()
	taskID := idgen.New()
	running := "running"
	task := &store.Task{
		ID:            taskID,
		UserID:        "sam_dev_user",
		Status:        &running,
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground",
	}
	if err := taskRepo.Create(ctx, task); err != nil {
		t.Fatal(err)
	}

	th := &taskHandler{
		core:      adapter.core,
		taskRepo:  taskRepo,
		eventRepo: eventRepo,
		sseRepo:   sseRepo,
		logger:    adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/status", th.handleStatus)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+taskID+"/status", nil)
	mux.ServeHTTP(rec, req)

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["errorMessage"] != nil {
		t.Errorf("expected error_message=nil for running task, got %v", resp["errorMessage"])
	}
}

func TestErrorMessageFromPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "jsonrpc_error",
			payload: `{"jsonrpc":"2.0","error":{"code":-32603,"message":"boom"}}`,
			want:    "boom",
		},
		{
			name:    "failed_task_text_part",
			payload: `{"result":{"status":{"state":"failed","message":{"parts":[{"kind":"text","text":"quota exceeded"}]}}}}`,
			want:    "quota exceeded",
		},
		{
			name:    "failed_task_flat_text",
			payload: `{"result":{"status":{"message":{"text":"legacy shape"}}}}`,
			want:    "legacy shape",
		},
		{
			name:    "no_error_content",
			payload: `{"result":{"kind":"status-update"}}`,
			want:    "",
		},
		{
			name:    "empty_error_message",
			payload: `{"error":{"code":1,"message":""}}`,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &payload); err != nil {
				t.Fatal(err)
			}
			got := errorMessageFromPayload(payload)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleActiveBackground_Empty(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)

	taskRepo := store.NewTaskRepo(core.Store())
	eventRepo := store.NewTaskEventRepo(core.Store())

	th := &taskHandler{
		core:      adapter.core,
		taskRepo:  taskRepo,
		eventRepo: eventRepo,
		sseRepo:   sseRepo,
		logger:    adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/background/active", th.handleActiveBackground)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/background/active", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}

	data, ok := raw["data"].([]any)
	if !ok {
		t.Fatalf("data is not an array: %T", raw["data"])
	}
	if len(data) != 0 {
		t.Errorf("expected empty data, got %d", len(data))
	}

	meta, ok := raw["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta missing: %T", raw["meta"])
	}
	pag, ok := meta["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("pagination missing: %T", meta["pagination"])
	}
	if int(pag["count"].(float64)) != 0 {
		t.Errorf("expected count=0, got %v", pag["count"])
	}
}

func createTaskForEvent(t *testing.T, taskRepo *store.TaskRepo, taskID string) {
	t.Helper()
	status := "completed"
	if err := taskRepo.Create(context.Background(), &store.Task{
		ID: taskID, UserID: "u1", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	}); err != nil {
		t.Fatalf("create task for FK: %v", err)
	}
}

func TestExtractAgentFromEvents_WorkflowName(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	ctx := context.Background()
	taskWf := idgen.New()
	createTaskForEvent(t, taskRepo, taskWf)
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskWf, UserID: strPtr("u1"), Direction: "request",
		CreatedTime: time.Now().UnixMilli(),
		Payload:     `{"params":{"message":{"metadata":{"workflow_name":"my-workflow"}}}}`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	h := &taskHandler{eventRepo: eventRepo}
	name := h.extractAgentFromEvents(ctx, taskWf)
	if name != "my-workflow" {
		t.Errorf("expected 'my-workflow', got %q", name)
	}
}

func TestExtractAgentFromEvents_AgentName(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	ctx := context.Background()
	taskAg := idgen.New()
	createTaskForEvent(t, taskRepo, taskAg)
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskAg, UserID: strPtr("u1"), Direction: "request",
		CreatedTime: time.Now().UnixMilli(),
		Payload:     `{"params":{"message":{"metadata":{"agent_name":"my-agent"}}}}`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	h := &taskHandler{eventRepo: eventRepo}
	name := h.extractAgentFromEvents(ctx, taskAg)
	if name != "my-agent" {
		t.Errorf("expected 'my-agent', got %q", name)
	}
}

func TestAgentNameForTask_PrefersColumn(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	ctx := context.Background()
	taskID := idgen.New()
	status := "running"
	agent := "column-agent"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "u1", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
		AgentName: &agent,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskID, UserID: strPtr("u1"), Direction: "request",
		CreatedTime: time.Now().UnixMilli(),
		Payload:     `{"params":{"message":{"metadata":{"agent_name":"event-agent"}}}}`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	h := &taskHandler{taskRepo: taskRepo, eventRepo: eventRepo}
	if name := h.agentNameForTask(ctx, taskID); name != "column-agent" {
		t.Errorf("agentNameForTask = %q, want column-agent (column should win)", name)
	}
}

func TestAgentNameForTask_FallsBackToEventsWhenColumnNull(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	ctx := context.Background()
	taskID := idgen.New()
	createTaskForEvent(t, taskRepo, taskID)
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskID, UserID: strPtr("u1"), Direction: "request",
		CreatedTime: time.Now().UnixMilli(),
		Payload:     `{"params":{"message":{"metadata":{"agent_name":"event-agent"}}}}`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	h := &taskHandler{taskRepo: taskRepo, eventRepo: eventRepo}
	if name := h.agentNameForTask(ctx, taskID); name != "event-agent" {
		t.Errorf("agentNameForTask = %q, want event-agent (event-scan fallback)", name)
	}
}

func TestErrorMessageForTask_PrefersColumnThenFallsBack(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	ctx := context.Background()

	withColumn := idgen.New()
	status := "failed"
	msg := "column error"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: withColumn, UserID: "u1", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
		ErrorMessage: &msg,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	h := &taskHandler{taskRepo: taskRepo, eventRepo: eventRepo}
	task, _ := taskRepo.GetByID(ctx, withColumn)
	if got := h.errorMessageForTask(ctx, task); got != "column error" {
		t.Errorf("errorMessageForTask (column) = %q, want 'column error'", got)
	}

	nullCol := idgen.New()
	createTaskForEvent(t, taskRepo, nullCol)
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: nullCol, UserID: strPtr("u1"), Direction: "error",
		CreatedTime: time.Now().UnixMilli(),
		Payload:     `{"error":{"message":"event error"}}`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	nullTask, _ := taskRepo.GetByID(ctx, nullCol)
	if got := h.errorMessageForTask(ctx, nullTask); got != "event error" {
		t.Errorf("errorMessageForTask (fallback) = %q, want 'event error'", got)
	}
}

func TestExtractAgentFromEvents_SkipsNonRequest(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	ctx := context.Background()
	taskSkip := idgen.New()
	createTaskForEvent(t, taskRepo, taskSkip)
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskSkip, UserID: strPtr("u1"), Direction: "response",
		CreatedTime: time.Now().UnixMilli(),
		Payload:     `{"params":{"message":{"metadata":{"agent_name":"hidden-agent"}}}}`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	h := &taskHandler{eventRepo: eventRepo}
	name := h.extractAgentFromEvents(ctx, taskSkip)
	if name != "" {
		t.Errorf("expected empty string for non-request events, got %q", name)
	}
}

func TestExtractAgentFromEvents_NoEvents(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	eventRepo := store.NewTaskEventRepo(db)

	h := &taskHandler{eventRepo: eventRepo}
	name := h.extractAgentFromEvents(context.Background(), idgen.New())
	if name != "" {
		t.Errorf("expected empty string, got %q", name)
	}
}

func TestExtractAgentFromEvents_InvalidJSON(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	ctx := context.Background()
	taskBad := idgen.New()
	createTaskForEvent(t, taskRepo, taskBad)
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: taskBad, UserID: strPtr("u1"), Direction: "request",
		CreatedTime: time.Now().UnixMilli(),
		Payload:     `not valid json`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	h := &taskHandler{eventRepo: eventRepo}
	name := h.extractAgentFromEvents(ctx, taskBad)
	if name != "" {
		t.Errorf("expected empty string for invalid JSON, got %q", name)
	}
}

func setupTaskHandler(t *testing.T) (*taskHandler, *store.TaskRepo, *store.ChatTaskRepo, *store.SSEEventRepo, *store.TaskEventRepo) {
	t.Helper()
	adapter, core, _, chatRepo, sseRepo := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	sessRepo := store.NewSessionRepo(db)

	th := &taskHandler{
		core:         adapter.core,
		taskRepo:     taskRepo,
		eventRepo:    eventRepo,
		sseRepo:      sseRepo,
		sessRepo:     sessRepo,
		chatRepo:     chatRepo,
		feedbackRepo: store.NewFeedbackRepo(db),
		logger:       adapter.logger,
	}
	return th, taskRepo, chatRepo, sseRepo, eventRepo
}

func TestHandleTitleData_WithChatTask(t *testing.T) {
	th, taskRepo, chatRepo, _, _ := setupTaskHandler(t)

	ctx := context.Background()
	taskTd := idgen.New()
	sessID := idgen.New()
	sessRepo := store.NewSessionRepo(th.core.Store())
	_ = sessRepo.Create(ctx, &store.Session{ID: sessID, UserID: "sam_dev_user"})

	status := "completed"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskTd, UserID: "sam_dev_user", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
		SessionID: &sessID,
	})

	userMsg := "What is the weather?"
	_, _ = chatRepo.Upsert(ctx, &store.ChatTask{
		ID: taskTd, SessionID: sessID, UserMessage: &userMsg,
		MessageBubbles: `[{"role":"agent","parts":[{"text":"It's sunny"}]}]`,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/titleData", th.handleTitleData)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+taskTd+"/titleData", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["userMessage"] != "What is the weather?" {
		t.Errorf("user_message = %v, want 'What is the weather?'", resp["userMessage"])
	}
	if resp["agentResponse"] != "It's sunny" {
		t.Errorf("agent_response = %v, want 'It's sunny'", resp["agentResponse"])
	}
	if resp["sessionId"] != sessID {
		t.Errorf("session_id = %v, want %s", resp["sessionId"], sessID)
	}
}

func TestHandleTitleData_FallbackToInitialRequestText(t *testing.T) {
	th, taskRepo, _, _, _ := setupTaskHandler(t)

	ctx := context.Background()
	taskFb := idgen.New()
	status := "completed"
	reqText := "Hello agent"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskFb, UserID: "sam_dev_user", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
		InitialRequestText: &reqText,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/titleData", th.handleTitleData)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+taskFb+"/titleData", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["userMessage"] != "Hello agent" {
		t.Errorf("user_message = %v, want 'Hello agent'", resp["userMessage"])
	}
}

func TestHandleTitleData_NotFound(t *testing.T) {
	nonexistent := idgen.New()
	th, _, _, _, _ := setupTaskHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/titleData", th.handleTitleData)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+nonexistent+"/titleData", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDeleteBufferedEvents(t *testing.T) {
	th, taskRepo, _, sseRepo, _ := setupTaskHandler(t)

	ctx := context.Background()
	taskDel := idgen.New()
	status := "completed"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskDel, UserID: "sam_dev_user", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	})
	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskDel, SessionID: idgen.New(), UserID: "sam_dev_user", EventSequence: 1, EventType: "status_update",
		EventData: `{"test":true}`,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/events/buffered", th.handleDeleteBufferedEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/tasks/"+taskDel+"/events/buffered", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	events, _ := sseRepo.GetByTask(ctx, taskDel, 0)
	if len(events) != 0 {
		t.Errorf("expected 0 events after delete, got %d", len(events))
	}
}

func TestHandleDeleteBufferedEvents_RunningTaskPreservesBuffer(t *testing.T) {
	th, taskRepo, _, sseRepo, _ := setupTaskHandler(t)

	ctx := context.Background()
	taskID := idgen.New()
	status := "running"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "sam_dev_user", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	})
	for i := 1; i <= 3; i++ {
		_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
			TaskID: taskID, SessionID: idgen.New(), UserID: "sam_dev_user",
			EventSequence: i, EventType: "status_update", EventData: `{"test":true}`,
		})
	}
	_ = th.core.Tasks().Register(taskID, "Agent", "sam_dev_user", entrypoint.TaskIdentity{})

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/events/buffered", th.handleDeleteBufferedEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/tasks/"+taskID+"/events/buffered", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	events, _ := sseRepo.GetByTask(ctx, taskID, 0)
	if len(events) != 3 {
		t.Errorf("running task buffer must be preserved (delete is a no-op while streaming); got %d events, want 3", len(events))
	}
}

func TestHandleBufferedEvents_WithEvents(t *testing.T) {
	th, taskRepo, _, sseRepo, _ := setupTaskHandler(t)

	ctx := context.Background()
	s1 := idgen.New()
	taskBuf := idgen.New()
	status := "completed"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskBuf, UserID: "sam_dev_user", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	})
	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskBuf, SessionID: s1, UserID: "sam_dev_user", EventSequence: 1, EventType: "status_update",
		EventData: `{"result":{"status":{"message":{"parts":[{"text":"hello"}]}}}}`,
	})
	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskBuf, SessionID: s1, UserID: "sam_dev_user", EventSequence: 2, EventType: "final_response",
		EventData: `{"result":{"task":{"artifacts":[{"parts":[{"kind":"text","text":"done"}]}]}}}`,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/events/buffered", th.handleBufferedEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+taskBuf+"/events/buffered", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	events := resp["data"].([]any)
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
	pag := resp["meta"].(map[string]any)["pagination"].(map[string]any)
	if int(pag["count"].(float64)) != 2 {
		t.Errorf("expected count=2, got %v", pag["count"])
	}

	first := events[0].(map[string]any)
	wrap, ok := first["data"].(map[string]any)
	if !ok {
		t.Fatalf("event data should be wrapper object, got %T", first["data"])
	}
	if wrap["event"] != "status_update" {
		t.Errorf("wrapper event = %v, want \"status_update\" (inner event mirrors the stored event_type — Python wire shape)", wrap["event"])
	}
	inner, ok := wrap["data"].(string)
	if !ok {
		t.Fatalf("wrapper data should be a JSON string, got %T", wrap["data"])
	}
	if inner != `{"result":{"status":{"message":{"parts":[{"text":"hello"}]}}}}` {
		t.Errorf("wrapper data = %q, want the raw envelope string", inner)
	}
}

func TestHandleBufferedEvents_Empty(t *testing.T) {
	nonexistent := idgen.New()
	th, _, _, _, _ := setupTaskHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/events/buffered", th.handleBufferedEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+nonexistent+"/events/buffered", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	pag := resp["meta"].(map[string]any)["pagination"].(map[string]any)
	if int(pag["count"].(float64)) != 0 {
		t.Errorf("expected count=0, got %v", pag["count"])
	}
}

func TestHandleEvents_WorkflowHierarchy(t *testing.T) {
	th, taskRepo, _, _, eventRepo := setupTaskHandler(t)

	ctx := context.Background()
	now := time.Now().UnixMilli()

	orchID := idgen.New()
	wfID := idgen.New()
	subAgentID := idgen.New()

	mustCreateTask(ctx, t, taskRepo, &store.Task{
		ID: orchID, UserID: "sam_dev_user",
		StartTime: now, ExecutionMode: "foreground",
	})
	mustCreateTask(ctx, t, taskRepo, &store.Task{
		ID: wfID, UserID: "sam_dev_user", ParentTaskID: &orchID,
		StartTime: now + 1, ExecutionMode: "foreground",
	})
	mustCreateTask(ctx, t, taskRepo, &store.Task{
		ID: subAgentID, UserID: "sam_dev_user", ParentTaskID: &wfID,
		StartTime: now + 2, ExecutionMode: "foreground",
	})

	for _, taskID := range []string{orchID, wfID, subAgentID} {
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: idgen.New(), TaskID: taskID, CreatedTime: now,
			Topic: "test", Direction: "outbound", Payload: `{}`,
		}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/events", th.handleEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+orchID+"/events", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data []struct {
			TaskID string           `json:"taskId"`
			Events []map[string]any `json:"events"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byID := make(map[string]int, len(resp.Data))
	for _, e := range resp.Data {
		byID[e.TaskID] = len(e.Events)
	}
	for _, taskID := range []string{orchID, wfID, subAgentID} {
		eventCount, ok := byID[taskID]
		if !ok {
			t.Errorf("task %s missing from response.data", taskID)
			continue
		}
		if eventCount != 1 {
			t.Errorf("task %s: expected 1 event, got %d", taskID, eventCount)
		}
	}
}

func TestHandleEvents_RootTraversal(t *testing.T) {
	th, taskRepo, _, _, eventRepo := setupTaskHandler(t)

	ctx := context.Background()
	now := time.Now().UnixMilli()

	orchID := idgen.New()
	wfID := idgen.New()
	siblingAgentID := idgen.New()
	subAgentID := idgen.New()

	mustCreateTask(ctx, t, taskRepo, &store.Task{
		ID: orchID, UserID: "sam_dev_user",
		StartTime: now, ExecutionMode: "foreground",
	})
	mustCreateTask(ctx, t, taskRepo, &store.Task{
		ID: wfID, UserID: "sam_dev_user", ParentTaskID: &orchID,
		StartTime: now + 1, ExecutionMode: "foreground",
	})
	mustCreateTask(ctx, t, taskRepo, &store.Task{
		ID: siblingAgentID, UserID: "sam_dev_user", ParentTaskID: &wfID,
		StartTime: now + 2, ExecutionMode: "foreground",
	})
	mustCreateTask(ctx, t, taskRepo, &store.Task{
		ID: subAgentID, UserID: "sam_dev_user", ParentTaskID: &wfID,
		StartTime: now + 3, ExecutionMode: "foreground",
	})

	for _, taskID := range []string{orchID, wfID, siblingAgentID, subAgentID} {
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: idgen.New(), TaskID: taskID, CreatedTime: now,
			Topic: "test", Direction: "outbound", Payload: `{}`,
		}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{id}/events", th.handleEvents)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks/"+subAgentID+"/events", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data []struct {
			TaskID string `json:"taskId"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byID := make(map[string]bool, len(resp.Data))
	for _, e := range resp.Data {
		byID[e.TaskID] = true
	}
	for _, taskID := range []string{orchID, wfID, siblingAgentID, subAgentID} {
		if !byID[taskID] {
			t.Errorf("task %s missing from response.data", taskID)
		}
	}
}

func mustCreateTask(ctx context.Context, t *testing.T, repo *store.TaskRepo, task *store.Task) {
	t.Helper()
	if err := repo.Create(ctx, task); err != nil {
		t.Fatalf("create task %s: %v", task.ID, err)
	}
}

func TestHandleTaskPost_UnknownAction(t *testing.T) {
	someTaskID := idgen.New()
	th, _, _, _, _ := setupTaskHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tasks/", th.handleTaskPost)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/tasks/"+someTaskID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleTaskPost_CancelEmptyID(t *testing.T) {
	th, _, _, _, _ := setupTaskHandler(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tasks/", th.handleTaskPost)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/tasks/:cancel", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestExtractAgentResponseFromSSE_FinalResponse(t *testing.T) {
	th, taskRepo, _, sseRepo, _ := setupTaskHandler(t)
	ctx := context.Background()
	taskSse1 := idgen.New()

	status := "completed"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskSse1, UserID: "u1", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	})
	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskSse1, SessionID: idgen.New(), UserID: "u1", EventSequence: 1, EventType: "final_response",
		EventData: `{"result":{"task":{"artifacts":[{"parts":[{"kind":"text","text":"final answer"}]}]}}}`,
	})

	got := th.extractAgentResponseFromSSE(ctx, taskSse1)
	if got != "final answer" {
		t.Errorf("expected 'final answer', got %q", got)
	}
}

func TestExtractAgentResponseFromSSE_StatusUpdate(t *testing.T) {
	th, taskRepo, _, sseRepo, _ := setupTaskHandler(t)
	ctx := context.Background()
	taskSse2 := idgen.New()

	status := "completed"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskSse2, UserID: "u1", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	})
	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskSse2, SessionID: idgen.New(), UserID: "u1", EventSequence: 1, EventType: "status_update",
		EventData: `{"result":{"status":{"message":{"parts":[{"text":"streaming text"}]}}}}`,
	})

	got := th.extractAgentResponseFromSSE(ctx, taskSse2)
	if got != "streaming text" {
		t.Errorf("expected 'streaming text', got %q", got)
	}
}

func TestExtractAgentResponseFromSSE_JoinsStreamedChunks(t *testing.T) {
	th, taskRepo, _, sseRepo, _ := setupTaskHandler(t)
	ctx := t.Context()
	taskID := idgen.New()
	session := idgen.New()

	status := "completed"
	_ = taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "u1", Status: &status,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	})
	for i, chunk := range []string{"The answer ", "is ", "forty-two."} {
		_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
			TaskID: taskID, SessionID: session, UserID: "u1", EventSequence: i + 1, EventType: "status_update",
			EventData: `{"result":{"status":{"message":{"parts":[{"text":"` + chunk + `"}]}}}}`,
		})
	}

	_ = sseRepo.Append(ctx, &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: session, UserID: "u1", EventSequence: 4, EventType: "status_update",
		EventData: `{"result":{"final":true,"status":{"message":{"parts":[{"text":"The answer is forty-two."}]}}}}`,
	})

	if got := th.extractAgentResponseFromSSE(ctx, taskID); got != "The answer is forty-two." {
		t.Errorf("got %q, want the streamed chunks joined without the final full-answer update", got)
	}
}

func TestExtractAgentResponseFromSSE_NoEvents(t *testing.T) {
	th, _, _, _, _ := setupTaskHandler(t)
	got := th.extractAgentResponseFromSSE(context.Background(), idgen.New())
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestHandleTaskList_WithTasks(t *testing.T) {
	th, taskRepo, _, _, _ := setupTaskHandler(t)

	ctx := context.Background()
	status := "completed"
	reqText := "test request"
	sessID := idgen.New()
	for range 3 {
		_ = taskRepo.Create(ctx, &store.Task{
			ID: idgen.New(), UserID: "sam_dev_user", Status: &status,
			StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
			InitialRequestText: &reqText, SessionID: &sessID,
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks?pageNumber=1&pageSize=2", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[map[string]any]
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 items (page_size=2), got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.Count != 3 {
		t.Errorf("expected count=3, got %d", resp.Meta.Pagination.Count)
	}
}

func TestHandleTaskList_ExpandSessionName(t *testing.T) {
	th, taskRepo, _, _, _ := setupTaskHandler(t)

	sessRepo := store.NewSessionRepo(th.core.Store())
	namedSess, unnamedSess := idgen.New(), idgen.New()
	title := "Weather in Ottawa"
	mustCreateSession(t, sessRepo, &store.Session{ID: namedSess, UserID: "sam_dev_user", Name: &title})
	mustCreateSession(t, sessRepo, &store.Session{ID: unnamedSess, UserID: "sam_dev_user"})

	status := "completed"
	reqText := "test request"
	now := time.Now().UnixMilli()
	inNamed, inUnnamed, noSession := idgen.New(), idgen.New(), idgen.New()
	mustCreateTask(context.Background(), t, taskRepo, &store.Task{ID: inNamed, UserID: "sam_dev_user", Status: &status, StartTime: now, ExecutionMode: "foreground", InitialRequestText: &reqText, SessionID: &namedSess})
	mustCreateTask(context.Background(), t, taskRepo, &store.Task{ID: inUnnamed, UserID: "sam_dev_user", Status: &status, StartTime: now, ExecutionMode: "foreground", InitialRequestText: &reqText, SessionID: &unnamedSess})
	mustCreateTask(context.Background(), t, taskRepo, &store.Task{ID: noSession, UserID: "sam_dev_user", Status: &status, StartTime: now, ExecutionMode: "foreground", InitialRequestText: &reqText})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)
	list := func(url string) map[string]map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d; body: %s", url, rec.Code, rec.Body.String())
		}
		var resp PaginatedResponse[map[string]any]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		byID := make(map[string]map[string]any, len(resp.Data))
		for _, item := range resp.Data {
			byID[item["id"].(string)] = item
		}
		return byID
	}

	plain := list("/api/v1/tasks?pageSize=10")
	for id, item := range plain {
		if _, present := item["sessionName"]; present {
			t.Errorf("task %s: sessionName present without expand", id)
		}
	}

	expanded := list("/api/v1/tasks?pageSize=10&expand=sessionName")
	if got := expanded[inNamed]["sessionName"]; got != title {
		t.Errorf("named session task: sessionName = %v, want %q", got, title)
	}
	for _, id := range []string{inUnnamed, noSession} {
		if got, present := expanded[id]["sessionName"]; present {
			t.Errorf("task %s: sessionName = %v, want absent", id, got)
		}
	}
}

func mustCreateSession(t *testing.T, repo *store.SessionRepo, sess *store.Session) {
	t.Helper()
	if err := repo.Create(context.Background(), sess); err != nil {
		t.Fatalf("create session %s: %v", sess.ID, err)
	}
}

func TestHandleTaskList_UserIDAndSearch(t *testing.T) {
	th, taskRepo, _, _, _ := setupTaskHandler(t)

	status := "completed"
	reqText := "test request"
	now := time.Now().UnixMilli()
	sess := idgen.New()
	mine, theirs := idgen.New(), idgen.New()
	mustCreateTask(context.Background(), t, taskRepo, &store.Task{ID: mine, UserID: "sam_dev_user", Status: &status, StartTime: now, ExecutionMode: "foreground", InitialRequestText: &reqText, SessionID: &sess})
	mustCreateTask(context.Background(), t, taskRepo, &store.Task{ID: theirs, UserID: "someone_else", Status: &status, StartTime: now, ExecutionMode: "foreground", InitialRequestText: &reqText, SessionID: &sess})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)
	list := func(url string) (int, []string) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != http.StatusOK {
			return rec.Code, nil
		}
		var resp PaginatedResponse[map[string]any]
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		ids := make([]string, 0, len(resp.Data))
		for _, item := range resp.Data {
			ids = append(ids, item["id"].(string))
		}
		return rec.Code, ids
	}

	if _, ids := list("/api/v1/tasks?pageSize=10&userId=sam_dev_user"); len(ids) != 1 || ids[0] != mine {
		t.Errorf("userId filter returned %v, want [%s]", ids, mine)
	}
	if _, ids := list("/api/v1/tasks?pageSize=10&search=" + theirs); len(ids) != 0 {
		t.Errorf("search by another owner's task id returned %v, want none", ids)
	}
	if code, ids := list("/api/v1/tasks?pageSize=10&userId=sam_dev_user&search=sam_dev&expand=sessionName&sort=lastActivityTime:desc"); code != http.StatusOK || len(ids) != 0 {
		t.Errorf("search by a partial term: status %d rows %v, want 200 and none", code, ids)
	}
	if _, ids := list("/api/v1/tasks?pageSize=10&sessionIds=" + sess + ",no-such-session"); len(ids) != 1 || ids[0] != mine {
		t.Errorf("sessionIds filter returned %v, want [%s]", ids, mine)
	}
	if code, _ := list("/api/v1/tasks?pageSize=10&sessionIds=" + strings.Repeat("x,", maxTaskSessionIDs+1)); code != http.StatusBadRequest {
		t.Errorf("over-long sessionIds: status %d, want 400", code)
	}
	if _, ids := list("/api/v1/tasks?pageSize=10&search=" + mine); len(ids) != 1 || ids[0] != mine {
		t.Errorf("search by task id returned %v, want [%s]", ids, mine)
	}
	if _, ids := list("/api/v1/tasks?pageSize=10&search=%20" + sess + "%20"); len(ids) != 1 || ids[0] != mine {
		t.Errorf("search by session id (trimmed) returned %v, want [%s]", ids, mine)
	}
	if _, ids := list("/api/v1/tasks?pageSize=10&search=no-such-id"); len(ids) != 0 {
		t.Errorf("search miss returned %v, want none", ids)
	}
	if code, _ := list("/api/v1/tasks?pageSize=10&search=" + strings.Repeat("x", maxTaskSearchLen+1)); code != http.StatusBadRequest {
		t.Errorf("over-long search: status %d, want 400", code)
	}
}

func TestHandleTaskList_EventCount(t *testing.T) {
	th, taskRepo, _, _, eventRepo := setupTaskHandler(t)

	ctx := context.Background()
	status := "completed"
	reqText := "test request"
	now := time.Now().UnixMilli()
	withEvents := idgen.New()
	noEvents := idgen.New()
	_ = taskRepo.Create(ctx, &store.Task{ID: withEvents, UserID: "sam_dev_user", Status: &status, StartTime: now, ExecutionMode: "foreground", InitialRequestText: &reqText})
	_ = taskRepo.Create(ctx, &store.Task{ID: noEvents, UserID: "sam_dev_user", Status: &status, StartTime: now, ExecutionMode: "foreground", InitialRequestText: &reqText})
	_ = eventRepo.Create(ctx, &store.TaskEvent{ID: idgen.New(), TaskID: withEvents, CreatedTime: 1000, Topic: "a", Direction: "request", Payload: "{}"})
	_ = eventRepo.Create(ctx, &store.TaskEvent{ID: idgen.New(), TaskID: withEvents, CreatedTime: 2000, Topic: "b", Direction: "response", Payload: "{}"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/tasks?pageNumber=1&pageSize=10", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[map[string]any]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	counts := make(map[string]float64, len(resp.Data))
	for _, item := range resp.Data {
		id, _ := item["id"].(string)
		ec, ok := item["eventCount"].(float64)
		if !ok {
			t.Errorf("task %s: eventCount missing or not a number: %v", id, item["eventCount"])
			continue
		}
		counts[id] = ec
	}
	if counts[withEvents] != 2 {
		t.Errorf("withEvents eventCount = %v, want 2", counts[withEvents])
	}
	if counts[noEvents] != 0 {
		t.Errorf("noEvents eventCount = %v, want 0", counts[noEvents])
	}
}

func TestHandleTaskList_EventCountMatchesEventsEndpoint(t *testing.T) {
	th, taskRepo, _, _, eventRepo := setupTaskHandler(t)

	ctx := context.Background()
	status := "completed"
	now := time.Now().UnixMilli()
	taskID := idgen.New()
	_ = taskRepo.Create(ctx, &store.Task{ID: taskID, UserID: "sam_dev_user", Status: &status, StartTime: now, ExecutionMode: "foreground"})
	for i := range 4 {
		_ = eventRepo.Create(ctx, &store.TaskEvent{ID: idgen.New(), TaskID: taskID, CreatedTime: now + int64(i), Topic: "t", Direction: "request", Payload: "{}"})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks", th.handleList)
	mux.HandleFunc("GET /api/v1/tasks/{id}/events", th.handleEvents)

	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, httptest.NewRequest("GET", "/api/v1/tasks?pageNumber=1&pageSize=10", nil))
	var listResp PaginatedResponse[map[string]any]
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var listCount float64
	for _, item := range listResp.Data {
		if item["id"] == taskID {
			listCount, _ = item["eventCount"].(float64)
		}
	}

	eventsRec := httptest.NewRecorder()
	mux.ServeHTTP(eventsRec, httptest.NewRequest("GET", "/api/v1/tasks/"+taskID+"/events", nil))
	var eventsResp struct {
		Data []struct {
			TaskID string           `json:"taskId"`
			Events []map[string]any `json:"events"`
		} `json:"data"`
	}
	if err := json.NewDecoder(eventsRec.Body).Decode(&eventsResp); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	var detailCount int
	for _, e := range eventsResp.Data {
		if e.TaskID == taskID {
			detailCount = len(e.Events)
		}
	}

	if int(listCount) != detailCount {
		t.Errorf("list eventCount (%d) != detail events (%d) for the same task — count would change on selection", int(listCount), detailCount)
	}
	if detailCount != 4 {
		t.Errorf("expected 4 detail events, got %d", detailCount)
	}
}
