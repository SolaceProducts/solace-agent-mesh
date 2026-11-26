// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestParseScheduledTaskServiceConfig_Defaults(t *testing.T) {
	cfg := ParseScheduledTaskServiceConfig(nil)

	if !cfg.Enabled {
		t.Error("Enabled should default to true")
	}
	if cfg.DefaultTimeoutSeconds != 3600 {
		t.Errorf("DefaultTimeoutSeconds = %d, want 3600", cfg.DefaultTimeoutSeconds)
	}
	if cfg.RetryDelaySeconds != 60 {
		t.Errorf("RetryDelaySeconds = %d, want 60", cfg.RetryDelaySeconds)
	}
	if cfg.ExecutionHistoryKeepCount != 100 {
		t.Errorf("ExecutionHistoryKeepCount = %d, want 100", cfg.ExecutionHistoryKeepCount)
	}
	if cfg.MisfireGraceTimeSeconds != 600 {
		t.Errorf("MisfireGraceTimeSeconds = %d, want 600", cfg.MisfireGraceTimeSeconds)
	}
	if cfg.AutoPauseAfterConsecutiveFailures != 10 {
		t.Errorf("AutoPauseAfterConsecutiveFailures = %d, want 10", cfg.AutoPauseAfterConsecutiveFailures)
	}
}

func TestParseScheduledTaskServiceConfig_Override(t *testing.T) {
	cfg := ParseScheduledTaskServiceConfig(map[string]any{
		"enabled":                               true,
		"default_timeout_seconds":               1800,
		"retry_delay_seconds":                   30,
		"execution_history_keep_count":          50,
		"misfire_grace_time_seconds":            120,
		"auto_pause_after_consecutive_failures": 3,
	})

	if !cfg.Enabled {
		t.Error("Enabled should be true")
	}
	if cfg.DefaultTimeoutSeconds != 1800 {
		t.Errorf("DefaultTimeoutSeconds = %d, want 1800", cfg.DefaultTimeoutSeconds)
	}
	if cfg.RetryDelaySeconds != 30 {
		t.Errorf("RetryDelaySeconds = %d, want 30", cfg.RetryDelaySeconds)
	}
	if cfg.ExecutionHistoryKeepCount != 50 {
		t.Errorf("ExecutionHistoryKeepCount = %d, want 50", cfg.ExecutionHistoryKeepCount)
	}
	if cfg.MisfireGraceTimeSeconds != 120 {
		t.Errorf("MisfireGraceTimeSeconds = %d, want 120", cfg.MisfireGraceTimeSeconds)
	}
	if cfg.AutoPauseAfterConsecutiveFailures != 3 {
		t.Errorf("AutoPauseAfterConsecutiveFailures = %d, want 3", cfg.AutoPauseAfterConsecutiveFailures)
	}
}

func TestParseScheduledTaskServiceConfig_ExplicitDisable(t *testing.T) {
	cfg := ParseScheduledTaskServiceConfig(map[string]any{"enabled": false})
	if cfg.Enabled {
		t.Error("explicit enabled:false should disable the scheduler")
	}
}

func TestParseScheduledTaskServiceConfig_AutoPauseExplicitZeroDisables(t *testing.T) {
	cfg := ParseScheduledTaskServiceConfig(map[string]any{
		"auto_pause_after_consecutive_failures": 0,
	})
	if cfg.AutoPauseAfterConsecutiveFailures != 0 {
		t.Errorf("explicit 0 should disable auto-pause, got %d", cfg.AutoPauseAfterConsecutiveFailures)
	}
}

func TestReplaceTemplateVars(t *testing.T) {
	svc := &ScheduledTaskService{}
	task := &store.ScheduledTask{
		Name:     "Daily Report",
		RunCount: 42,
		Timezone: "UTC",
	}
	text := "Task: {{schedule.name}}, Run: {{schedule.run_count}}, Exec: {{execution.id}}, Date: {{schedule.run_date}}"
	result := svc.replaceTemplateVars(text, task, "exec-123")

	if got := result; got == text {
		t.Error("template variables were not replaced")
	}

	if !contains(result, "Daily Report") {
		t.Error("schedule.name not replaced")
	}
	if !contains(result, "42") {
		t.Error("schedule.run_count not replaced")
	}
	if !contains(result, "exec-123") {
		t.Error("execution.id not replaced")
	}
	if contains(result, "{{schedule.run_date}}") {
		t.Error("schedule.run_date not replaced")
	}
}

func TestRenderTemplate_JSONParts(t *testing.T) {
	svc := &ScheduledTaskService{}
	task := &store.ScheduledTask{
		Name:        "Test Task",
		TaskMessage: `[{"kind":"text","text":"Hello {{schedule.name}}"}]`,
		RunCount:    1,
		Timezone:    "UTC",
	}
	result := svc.renderTemplate(task, "exec-1")
	if !contains(result, "Hello Test Task") {
		t.Errorf("renderTemplate = %q, want to contain 'Hello Test Task'", result)
	}
}

func TestRenderTemplate_PlainText(t *testing.T) {
	svc := &ScheduledTaskService{}
	task := &store.ScheduledTask{
		Name:        "Test Task",
		TaskMessage: "Hello {{schedule.name}}",
		RunCount:    1,
		Timezone:    "UTC",
	}
	result := svc.renderTemplate(task, "exec-1")
	if !contains(result, "Hello Test Task") {
		t.Errorf("renderTemplate = %q, want to contain 'Hello Test Task'", result)
	}
}

func TestBuildMessage_MetadataFiltering(t *testing.T) {
	svc := &ScheduledTaskService{}
	meta := `{"priority":"high","tags":["daily"],"category":"report","source":"ui","secret":"bad","admin":true}`
	task := &store.ScheduledTask{
		Name:            "Test",
		TargetAgentName: "agent1",
		TaskMessage:     `[{"kind":"text","text":"hello"}]`,
		TaskMetadata:    &meta,
		RunCount:        0,
		Timezone:        "UTC",
	}

	msg := svc.buildMessage(task, "exec-1", "session-1")

	if _, ok := msg.Metadata["priority"]; !ok {
		t.Error("priority should be in metadata")
	}
	if _, ok := msg.Metadata["tags"]; !ok {
		t.Error("tags should be in metadata")
	}
	if _, ok := msg.Metadata["category"]; !ok {
		t.Error("category should be in metadata")
	}
	if _, ok := msg.Metadata["source"]; !ok {
		t.Error("source should be in metadata")
	}

	if _, ok := msg.Metadata["secret"]; ok {
		t.Error("secret should NOT be in metadata")
	}
	if _, ok := msg.Metadata["admin"]; ok {
		t.Error("admin should NOT be in metadata")
	}

	if v, _ := msg.Metadata["sessionBehavior"].(string); v != "RUN_BASED" {
		t.Errorf("sessionBehavior = %q, want RUN_BASED", v)
	}
	if v, _ := msg.Metadata["returnArtifacts"].(bool); !v {
		t.Error("returnArtifacts should be true")
	}
}

func TestBuildResultSummary(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "text", Text: "Here is your report."},
				},
			},
		},
	}

	summary := buildResultSummary(task, nil)
	if summary == "" {
		t.Fatal("summary should not be empty")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(summary), &parsed); err != nil {
		t.Fatalf("summary is not valid JSON: %v", err)
	}
	if text, _ := parsed["agentResponse"].(string); text != "Here is your report." {
		t.Errorf("summary agentResponse = %q, want 'Here is your report.'", text)
	}
	if full, _ := parsed["agentResponseFull"].(string); full != "Here is your report." {
		t.Errorf("summary agentResponseFull = %q, want 'Here is your report.'", full)
	}
}

func TestBuildResultSummary_Nil(t *testing.T) {
	if got := buildResultSummary(nil, nil); got != "" {
		t.Errorf("buildResultSummary(nil) = %q, want empty", got)
	}
}

func TestBuildArtifactsSummary(t *testing.T) {
	task := &a2a.Task{
		Artifacts: []a2a.Artifact{
			{Name: "report.pdf", Description: "Monthly report"},
		},
	}
	summary := buildArtifactsSummary(task)
	if summary == "" {
		t.Fatal("artifacts summary should not be empty")
	}

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(summary), &parsed); err != nil {
		t.Fatalf("artifacts summary is not valid JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(parsed))
	}
	if name, _ := parsed[0]["name"].(string); name != "report.pdf" {
		t.Errorf("artifact name = %q, want 'report.pdf'", name)
	}
}

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"short", "fail", "fail"},
		{"multiline", "line1\nline2\nline3", "line1"},
		{"long", string(make([]byte, 300)), string(make([]byte, 256))},
		{"never splits a multi-byte rune at the cut", strings.Repeat("a", 255) + "é", strings.Repeat("a", 255)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeError(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeError = %q (len %d), want %q (len %d)", got, len(got), tt.want, len(tt.want))
			}
		})
	}
}

func TestResolveUserID(t *testing.T) {
	svc := &ScheduledTaskService{}
	uid := "user-1"

	task := &store.ScheduledTask{UserID: &uid, CreatedBy: "creator"}
	if got := svc.resolveUserID(task); got != "user-1" {
		t.Errorf("resolveUserID = %q, want 'user-1'", got)
	}

	task = &store.ScheduledTask{CreatedBy: "creator"}
	if got := svc.resolveUserID(task); got != "creator" {
		t.Errorf("resolveUserID = %q, want 'creator'", got)
	}

	task = &store.ScheduledTask{}
	if got := svc.resolveUserID(task); got != "" {
		t.Errorf("resolveUserID = %q, want no owner", got)
	}
}

func TestTaskCompleteHook_Fires(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := t.Context()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	var hookCalled sync.WaitGroup
	hookCalled.Add(1)
	var hookTaskID string
	var hookTask *a2a.Task

	core.OnTaskComplete(func(taskID string, task *a2a.Task, a2aErr *a2a.Error) {
		hookTaskID = taskID
		hookTask = task
		hookCalled.Done()
	})

	_ = core.tasks.Register("task-hook-1", "agent1", "test-user", TaskIdentity{})

	task := &a2a.Task{
		ID:        "task-hook-1",
		ContextID: "ctx-1",
		Kind:      "task",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{{Kind: "text", Text: "done"}},
			},
		},
	}
	resp := a2a.NewResponse("task-hook-1", task)
	payload, _ := json.Marshal(resp)
	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-hook-1")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish response: %v", err)
	}

	done := make(chan struct{})
	go func() {
		hookCalled.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for task complete hook")
	}

	if hookTaskID != "task-hook-1" {
		t.Errorf("hookTaskID = %q, want 'task-hook-1'", hookTaskID)
	}
	if hookTask == nil {
		t.Error("hookTask should not be nil")
	}
}

func TestTaskCompleteHook_Error(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := t.Context()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	var hookCalled sync.WaitGroup
	hookCalled.Add(1)
	var hookErr *a2a.Error

	core.OnTaskComplete(func(taskID string, task *a2a.Task, a2aErr *a2a.Error) {
		hookErr = a2aErr
		hookCalled.Done()
	})

	errResp := a2a.NewErrorResponse("task-err-1", -32000, "something went wrong")
	payload, _ := json.Marshal(errResp)
	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-err-1")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		hookCalled.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for task complete hook (error)")
	}

	if hookErr == nil {
		t.Fatal("hookErr should not be nil")
	}
	if hookErr.Message != "something went wrong" {
		t.Errorf("hookErr.Message = %q, want 'something went wrong'", hookErr.Message)
	}
}

func TestScheduledTaskService_InitAndStop(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())

	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if core.ScheduledTasks() == nil {
		t.Fatal("ScheduledTasks() should not be nil when enabled with DB")
	}

	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := core.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func newSchedulerSvcForTest(t *testing.T, autoPauseThreshold int) *ScheduledTaskService {
	t.Helper()
	return newSchedulerSvcWithArtifactStore(t, autoPauseThreshold, artmem.New())
}

func newSchedulerSvcWithArtifactStore(t *testing.T, autoPauseThreshold int, backend artifact.Service) *ScheduledTaskService {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled":                               true,
			"auto_pause_after_consecutive_failures": autoPauseThreshold,
		},
	}
	core := NewGatewayCore(&recordingAdapter{}, appConfig)
	scoped := &artifact.ScopedService{Inner: backend, Scope: "namespace", Namespace: "test-ns"}
	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped))
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	svc := core.ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() should not be nil")
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return svc
}

func makeEnabledTask(t *testing.T, svc *ScheduledTaskService, agentName string) *store.ScheduledTask {
	t.Helper()
	task := &store.ScheduledTask{
		ID:                 idgen.New(),
		Name:               "test-schedule",
		Namespace:          "test-ns",
		CreatedBy:          "alice@example.com",
		ScheduleType:       "interval",
		ScheduleExpression: "300s",
		Timezone:           "UTC",
		TargetAgentName:    agentName,
		TargetType:         "agent",
		TaskMessage:        `[{"kind":"text","text":"hello"}]`,
		Enabled:            true,
		Source:             "ui",
	}
	if err := svc.repo.Create(t.Context(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

func TestExtractUserMessage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"json parts", `[{"type":"text","text":"Generate a report"}]`, "Generate a report"},
		{"plain text fallback", "Hello world", "Hello world"},
		{"empty parts", `[{"type":"image","url":"http://example.com"}]`, ""},
		{"multiple parts", `[{"type":"text","text":"first"},{"type":"text","text":"second"}]`, "first"},
		{"kind spelling", `[{"kind":"text","text":"Generate a report"}]`, "Generate a report"},
		{"kind after non-text", `[{"kind":"data","data":{"a":1}},{"kind":"text","text":"second"}]`, "second"},
		{"bare string unquoted", `"https://x"`, "https://x"},
		{"malformed preserved", `[{`, `[{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUserMessage(tt.input)
			if got != tt.want {
				t.Errorf("extractUserMessage = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractChatContent_TextAndArtifacts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "text", Text: "Here is your report."},
					{Kind: "file", File: &a2a.FileContent{
						Name:     "report.pdf",
						URI:      "artifact://session-1/report.pdf",
						MIMEType: "application/pdf",
					}},
				},
			},
		},
	}

	text, artifacts := extractChatContent(task, "session-1")
	if text != "Here is your report." {
		t.Errorf("text = %q, want 'Here is your report.'", text)
	}
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(artifacts))
	}
	if artifacts[0]["name"] != "report.pdf" {
		t.Errorf("artifact name = %v, want 'report.pdf'", artifacts[0]["name"])
	}
	fileInfo := artifacts[0]["file"].(map[string]any)
	if fileInfo["mime_type"] != "application/pdf" {
		t.Errorf("artifact mime_type = %v, want 'application/pdf'", fileInfo["mime_type"])
	}
}

func TestExtractChatContent_FiltersWebContentArtifacts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "text", Text: "Done"},
					{Kind: "file", File: &a2a.FileContent{
						Name: "web_content_123",
						URI:  "artifact://session-1/web_content_123",
					}},
					{Kind: "file", File: &a2a.FileContent{
						Name: "real_output.csv",
						URI:  "artifact://session-1/real_output.csv",
					}},
				},
			},
		},
	}

	_, artifacts := extractChatContent(task, "session-1")
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1 (web_content should be filtered)", len(artifacts))
	}
	if artifacts[0]["name"] != "real_output.csv" {
		t.Errorf("artifact name = %v, want 'real_output.csv'", artifacts[0]["name"])
	}
}

func TestExtractChatContent_DeduplicatesArtifacts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "text", Text: "Done"},
					{Kind: "file", File: &a2a.FileContent{
						Name: "report.pdf",
						URI:  "artifact://session-1/report.pdf",
					}},
				},
			},
		},
		History: []a2a.Message{
			{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "file", File: &a2a.FileContent{
						Name: "report.pdf",
						URI:  "artifact://session-1/report.pdf",
					}},
				},
			},
		},
		Artifacts: []a2a.Artifact{
			{ArtifactID: "report.pdf", Name: "report.pdf"},
		},
	}

	_, artifacts := extractChatContent(task, "session-1")
	if len(artifacts) != 1 {
		t.Errorf("artifacts = %d, want 1 (duplicates should be removed)", len(artifacts))
	}
}

func TestExtractChatContent_MetadataArtifacts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{{Kind: "text", Text: "Done"}},
			},
		},
		Metadata: map[string]any{
			"produced_artifacts": []any{
				map[string]any{"name": "chart.png", "mime_type": "image/png"},
				map[string]any{"name": "web_content_456"},
			},
		},
	}

	_, artifacts := extractChatContent(task, "session-1")
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(artifacts))
	}
	if artifacts[0]["name"] != "chart.png" {
		t.Errorf("artifact name = %v, want 'chart.png'", artifacts[0]["name"])
	}
	fileInfo := artifacts[0]["file"].(map[string]any)
	if fileInfo["uri"] != "artifact://session-1/chart.png" {
		t.Errorf("artifact URI = %v, want 'artifact://session-1/chart.png'", fileInfo["uri"])
	}
}

func TestSaveChatTask_Success(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())

	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	svc := core.ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() should not be nil")
	}

	sessRepo := store.NewSessionRepo(core.Store())
	sessionID := idgen.New()
	sessName := "Test Task"
	if err := sessRepo.Create(ctx, &store.Session{
		ID:     sessionID,
		Name:   &sessName,
		UserID: "user-1",
		Source: new(string),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	a2aTask := &a2a.Task{
		ID: "a2a-task-1",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "text", Text: "Report generated successfully."},
					{Kind: "file", File: &a2a.FileContent{
						Name: "report.pdf",
						URI:  "artifact://" + sessionID + "/report.pdf",
					}},
				},
			},
		},
	}

	task := &store.ScheduledTask{
		ID:              "task-1",
		Name:            "Test Task",
		TargetAgentName: "agent1",
		TaskMessage:     `[{"type":"text","text":"Generate a monthly report"}]`,
		CreatedBy:       "user-1",
	}

	svc.saveChatTask(ctx, task, sessionID, "a2a-task-1", a2aTask, "", nil)

	chatRepo := store.NewChatTaskRepo(core.Store())
	ct, err := chatRepo.GetByID(ctx, "a2a-task-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ct == nil {
		t.Fatal("ChatTask should have been created")
	}
	if ct.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", ct.SessionID, sessionID)
	}
	if ct.UserID != "user-1" {
		t.Errorf("UserID = %q, want 'user-1'", ct.UserID)
	}
	if ct.UserMessage == nil || *ct.UserMessage != "Generate a monthly report" {
		t.Errorf("UserMessage = %v, want 'Generate a monthly report'", ct.UserMessage)
	}

	var bubbles []map[string]any
	if err := json.Unmarshal([]byte(ct.MessageBubbles), &bubbles); err != nil {
		t.Fatalf("failed to parse message_bubbles: %v", err)
	}
	if len(bubbles) != 2 {
		t.Fatalf("bubbles = %d, want 2 (user + agent)", len(bubbles))
	}
	if bubbles[0]["type"] != "user" {
		t.Errorf("bubble[0].type = %v, want 'user'", bubbles[0]["type"])
	}
	if bubbles[1]["type"] != "agent" {
		t.Errorf("bubble[1].type = %v, want 'agent'", bubbles[1]["type"])
	}
	if bubbles[1]["isError"] != false {
		t.Errorf("bubble[1].isError = %v, want false", bubbles[1]["isError"])
	}

	agentParts, ok := bubbles[1]["parts"].([]any)
	if !ok {
		t.Fatal("agent bubble parts should be an array")
	}
	if len(agentParts) != 2 {
		t.Fatalf("agent parts = %d, want 2 (text + artifact)", len(agentParts))
	}

	if ct.TaskMetadata == nil {
		t.Fatal("TaskMetadata should not be nil")
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(*ct.TaskMetadata), &meta); err != nil {
		t.Fatalf("failed to parse task_metadata: %v", err)
	}
	if meta["status"] != "completed" {
		t.Errorf("metadata.status = %v, want 'completed'", meta["status"])
	}
	if meta["source"] != "scheduler" {
		t.Errorf("metadata.source = %v, want 'scheduler'", meta["source"])
	}
	if meta["agentName"] != "agent1" {
		t.Errorf("metadata.agentName = %v, want 'agent1'", meta["agentName"])
	}
}

func TestSaveChatTask_Error(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())

	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	svc := core.ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() should not be nil")
	}

	sessRepo := store.NewSessionRepo(core.Store())
	sessionID := idgen.New()
	sessName := "Error Task"
	if err := sessRepo.Create(ctx, &store.Session{
		ID:     sessionID,
		Name:   &sessName,
		UserID: "user-1",
		Source: new(string),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	task := &store.ScheduledTask{
		ID:              "task-2",
		Name:            "Error Task",
		TargetAgentName: "agent1",
		TaskMessage:     `[{"type":"text","text":"Do something"}]`,
		CreatedBy:       "user-1",
	}

	svc.saveChatTask(ctx, task, sessionID, "a2a-err-1", nil, "Task execution failed", nil)

	chatRepo := store.NewChatTaskRepo(core.Store())
	ct, err := chatRepo.GetByID(ctx, "a2a-err-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ct == nil {
		t.Fatal("ChatTask should have been created for error")
	}

	var bubbles []map[string]any
	if err := json.Unmarshal([]byte(ct.MessageBubbles), &bubbles); err != nil {
		t.Fatalf("failed to parse message_bubbles: %v", err)
	}
	if len(bubbles) != 2 {
		t.Fatalf("bubbles = %d, want 2 (user + error agent)", len(bubbles))
	}
	if bubbles[1]["isError"] != true {
		t.Errorf("bubble[1].isError = %v, want true", bubbles[1]["isError"])
	}
	if bubbles[1]["text"] != "Task execution failed" {
		t.Errorf("bubble[1].text = %v, want 'Task execution failed'", bubbles[1]["text"])
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(*ct.TaskMetadata), &meta); err != nil {
		t.Fatalf("failed to parse task_metadata: %v", err)
	}
	if meta["status"] != "error" {
		t.Errorf("metadata.status = %v, want 'error'", meta["status"])
	}
}

func TestExtractChatContent_ArtifactsOnlyNoText(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "file", File: &a2a.FileContent{
						Name: "output.xlsx",
						URI:  "artifact://session-1/output.xlsx",
					}},
				},
			},
		},
	}

	text, artifacts := extractChatContent(task, "session-1")
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(artifacts))
	}
	if artifacts[0]["name"] != "output.xlsx" {
		t.Errorf("artifact name = %v, want 'output.xlsx'", artifacts[0]["name"])
	}
}

func TestSaveChatTask_ArtifactsOnlyCreatesBubble(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())

	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	svc := core.ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() should not be nil")
	}

	sessRepo := store.NewSessionRepo(core.Store())
	sessionID := idgen.New()
	sessName := "Artifacts Only Task"
	if err := sessRepo.Create(ctx, &store.Session{
		ID:     sessionID,
		Name:   &sessName,
		UserID: "user-1",
		Source: new(string),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	a2aTask := &a2a.Task{
		ID: "a2a-art-1",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{Kind: "file", File: &a2a.FileContent{
						Name: "output.xlsx",
						URI:  "artifact://" + sessionID + "/output.xlsx",
					}},
				},
			},
		},
	}

	task := &store.ScheduledTask{
		ID:              "task-art",
		Name:            "Artifacts Only Task",
		TargetAgentName: "agent1",
		TaskMessage:     `[{"type":"text","text":"Generate spreadsheet"}]`,
		CreatedBy:       "user-1",
	}

	svc.saveChatTask(ctx, task, sessionID, "a2a-art-1", a2aTask, "", nil)

	chatRepo := store.NewChatTaskRepo(core.Store())
	ct, err := chatRepo.GetByID(ctx, "a2a-art-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ct == nil {
		t.Fatal("ChatTask should have been created for artifact-only response")
	}

	var bubbles []map[string]any
	if err := json.Unmarshal([]byte(ct.MessageBubbles), &bubbles); err != nil {
		t.Fatalf("failed to parse message_bubbles: %v", err)
	}
	if len(bubbles) != 2 {
		t.Fatalf("bubbles = %d, want 2 (user + agent with artifact)", len(bubbles))
	}
	if bubbles[1]["type"] != "agent" {
		t.Errorf("bubble[1].type = %v, want 'agent'", bubbles[1]["type"])
	}

	agentParts, ok := bubbles[1]["parts"].([]any)
	if !ok {
		t.Fatal("agent bubble parts should be an array")
	}
	if len(agentParts) != 1 {
		t.Fatalf("agent parts = %d, want 1 (artifact only, no text part)", len(agentParts))
	}
}

func TestSaveChatTask_NilChatRepo(t *testing.T) {
	svc := &ScheduledTaskService{}
	svc.saveChatTask(t.Context(), &store.ScheduledTask{}, "session-1", "task-1", nil, "error", nil)
}

func TestRagEntryKey_SourceURL(t *testing.T) {
	entry := map[string]any{"source_url": "https://example.com/doc1", "title": "Doc 1"}
	key := ragEntryKey(entry)
	if key != "https://example.com/doc1" {
		t.Errorf("ragEntryKey = %q, want source_url value", key)
	}
}

func TestRagEntryKey_URLFallback(t *testing.T) {
	entry := map[string]any{"url": "https://example.com/doc2", "title": "Doc 2"}
	key := ragEntryKey(entry)
	if key != "https://example.com/doc2" {
		t.Errorf("ragEntryKey = %q, want url value", key)
	}
}

func TestRagEntryKey_SourceFallback(t *testing.T) {
	entry := map[string]any{"source": "https://example.com/doc3"}
	key := ragEntryKey(entry)
	if key != "https://example.com/doc3" {
		t.Errorf("ragEntryKey = %q, want source value", key)
	}
}

func TestRagEntryKey_ContentHash(t *testing.T) {
	entry := map[string]any{"title": "No URL", "content": "some text"}
	key := ragEntryKey(entry)
	if len(key) != 64 {
		t.Errorf("ragEntryKey length = %d, want 64 (sha256 hex)", len(key))
	}
	key2 := ragEntryKey(map[string]any{"title": "No URL", "content": "some text"})
	if key != key2 {
		t.Errorf("same content produced different keys: %q vs %q", key, key2)
	}
}

func TestRagEntryKey_DifferentContentDifferentHash(t *testing.T) {
	key1 := ragEntryKey(map[string]any{"content": "text A"})
	key2 := ragEntryKey(map[string]any{"content": "text B"})
	if key1 == key2 {
		t.Error("different content should produce different keys")
	}
}

func TestMergeRAGData_BothEmpty(t *testing.T) {
	result := mergeRAGData(nil, nil)
	if result != nil {
		t.Errorf("mergeRAGData(nil, nil) = %v, want nil", result)
	}
}

func TestMergeRAGData_ResponseOnly(t *testing.T) {
	resp := []map[string]any{
		{"source_url": "https://a.com", "title": "A"},
	}
	result := mergeRAGData(resp, nil)
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
}

func TestMergeRAGData_AccumulatedOnly(t *testing.T) {
	accum := []map[string]any{
		{"source_url": "https://b.com", "title": "B"},
	}
	result := mergeRAGData(nil, accum)
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
}

func TestMergeRAGData_DeduplicatesBySourceURL(t *testing.T) {
	resp := []map[string]any{
		{"source_url": "https://a.com", "title": "A"},
		{"source_url": "https://b.com", "title": "B"},
	}
	accum := []map[string]any{
		{"source_url": "https://a.com", "title": "A duplicate"},
		{"source_url": "https://c.com", "title": "C"},
	}
	result := mergeRAGData(resp, accum)
	if len(result) != 3 {
		t.Fatalf("len = %d, want 3 (a, b, c)", len(result))
	}
	if result[0]["title"] != "A" {
		t.Errorf("result[0] title = %q, want 'A' (from response, not accumulated)", result[0]["title"])
	}
}

func TestMergeRAGData_DeduplicatesByContentHash(t *testing.T) {
	entry := map[string]any{"title": "same", "content": "same content"}
	resp := []map[string]any{entry}
	accum := []map[string]any{{"title": "same", "content": "same content"}}
	result := mergeRAGData(resp, accum)
	if len(result) != 1 {
		t.Errorf("len = %d, want 1 (deduplicated by content hash)", len(result))
	}
}

func TestExtractRAGFromParts_ToolResultWithRAG(t *testing.T) {
	msg := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{
			{
				Kind: "data",
				Data: map[string]any{
					"type": "tool_result",
					"result_data": map[string]any{
						"rag_metadata": map[string]any{
							"query":      "q",
							"searchType": "document_search",
							"sources": []any{map[string]any{
								"citationId": "idx0r0",
								"sourceUrl":  "https://example.com/result",
								"title":      "Research Result",
							}},
						},
					},
				},
			},
		},
	}

	entries := extractRAGFromParts(msg)
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}
	if entries[0]["searchType"] != "document_search" {
		t.Errorf("searchType = %v, want document_search", entries[0]["searchType"])
	}
}

func TestExtractRAGFromParts_NonToolResult(t *testing.T) {
	msg := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{
			{Kind: "text", Text: "thinking..."},
			{
				Kind: "data",
				Data: map[string]any{
					"type": "progress",
				},
			},
		},
	}
	entries := extractRAGFromParts(msg)
	if len(entries) != 0 {
		t.Errorf("len = %d, want 0 (no tool_result parts)", len(entries))
	}
}

func TestExtractRAGFromParts_NilMessage(t *testing.T) {
	entries := extractRAGFromParts(nil)
	if entries != nil {
		t.Errorf("expected nil for nil message")
	}
}

func ragInfoUpdateDataPart(t *testing.T, signal a2a.RAGInfoUpdateData) a2a.Part {
	t.Helper()
	raw, err := json.Marshal(signal)
	if err != nil {
		t.Fatalf("marshal signal: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal signal: %v", err)
	}
	return a2a.Part{Kind: "data", Data: data}
}

func TestExtractRAGFromParts_RAGInfoUpdateSignal(t *testing.T) {
	msg := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{
			ragInfoUpdateDataPart(t, a2a.RAGInfoUpdateData{
				Type:       string(a2a.SignalRAGInfoUpdate),
				Title:      "Web Results",
				Query:      "solace agent mesh",
				SearchType: "web_search",
				Sources: []a2a.RAGSource{
					{CitationID: "s1r0", Title: "Solace", SourceURL: "https://solace.com", URL: "https://solace.com", ContentPreview: "preview", RelevanceScore: 1},
				},
				IsComplete: true,
				Timestamp:  "2024-01-01T00:00:00Z",
			}),
		},
	}

	entries := extractRAGFromParts(msg)
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry["searchType"] != "web_search" {
		t.Errorf("searchType = %v, want web_search", entry["searchType"])
	}
	if entry["query"] != "solace agent mesh" {
		t.Errorf("query = %v, want 'solace agent mesh'", entry["query"])
	}
	if entry["title"] != "Web Results" {
		t.Errorf("title = %v, want 'Web Results'", entry["title"])
	}
	if entry["timestamp"] != "2024-01-01T00:00:00Z" {
		t.Errorf("timestamp = %v, want 2024-01-01T00:00:00Z", entry["timestamp"])
	}
	sources, ok := entry["sources"].([]any)
	if !ok {
		t.Fatalf("sources type = %T, want []any", entry["sources"])
	}
	if len(sources) != 1 {
		t.Fatalf("sources len = %d, want 1", len(sources))
	}
	source, ok := sources[0].(map[string]any)
	if !ok {
		t.Fatalf("source type = %T, want map[string]any", sources[0])
	}
	if source["citationId"] != "s1r0" {
		t.Errorf("citationId = %v, want s1r0", source["citationId"])
	}
	if source["sourceUrl"] != "https://solace.com" {
		t.Errorf("sourceUrl = %v, want https://solace.com", source["sourceUrl"])
	}
}

func TestExtractRAGFromParts_ToolResultAndSignalTogether(t *testing.T) {
	msg := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{
			{
				Kind: "data",
				Data: map[string]any{
					"type": "tool_result",
					"result_data": map[string]any{
						"rag_metadata": map[string]any{
							"query":      "docs query",
							"searchType": "document_search",
							"sources":    []any{map[string]any{"citationId": "turn1file0"}},
						},
					},
				},
			},
			ragInfoUpdateDataPart(t, a2a.RAGInfoUpdateData{
				Type:       string(a2a.SignalRAGInfoUpdate),
				Query:      "web query",
				SearchType: "web_search",
				Sources:    []a2a.RAGSource{{CitationID: "s1r0", URL: "https://solace.com"}},
				IsComplete: true,
			}),
		},
	}

	entries := extractRAGFromParts(msg)
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2 (tool_result + rag_info_update)", len(entries))
	}
	if entries[0]["searchType"] != "document_search" {
		t.Errorf("entries[0].searchType = %v, want document_search", entries[0]["searchType"])
	}
	if entries[1]["searchType"] != "web_search" {
		t.Errorf("entries[1].searchType = %v, want web_search", entries[1]["searchType"])
	}
}

func TestExtractRAGFromParts_RAGInfoUpdateWithoutSearchType(t *testing.T) {
	msg := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{
			{
				Kind: "data",
				Data: map[string]any{
					"type":    string(a2a.SignalRAGInfoUpdate),
					"query":   "q",
					"sources": []any{map[string]any{"citationId": "research0"}},
				},
			},
		},
	}

	entries := extractRAGFromParts(msg)
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}
	if entries[0]["searchType"] != "deep_research" {
		t.Errorf("searchType = %v, want deep_research fallback", entries[0]["searchType"])
	}
}

func TestExtractRAGFromParts_RAGInfoUpdateNoSources(t *testing.T) {
	msg := &a2a.Message{
		Role: a2a.RoleAgent,
		Parts: []a2a.Part{
			ragInfoUpdateDataPart(t, a2a.RAGInfoUpdateData{
				Type:       string(a2a.SignalRAGInfoUpdate),
				Query:      "q",
				SearchType: "deep_research",
			}),
		},
	}

	if entries := extractRAGFromParts(msg); len(entries) != 0 {
		t.Errorf("len = %d, want 0 (sourceless signal ignored)", len(entries))
	}
}

func TestExtractRAGFromTask_StatusAndHistory(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{
						Kind: "data",
						Data: map[string]any{
							"type": "tool_result",
							"result_data": map[string]any{
								"rag_metadata": map[string]any{
									"query": "q", "searchType": "document_search",
									"sources": []any{map[string]any{"sourceUrl": "https://status.com"}},
								},
							},
						},
					},
				},
			},
		},
		History: []a2a.Message{
			{
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{
						Kind: "data",
						Data: map[string]any{
							"type": "tool_result",
							"result_data": map[string]any{
								"rag_metadata": map[string]any{
									"query": "q", "searchType": "document_search",
									"sources": []any{map[string]any{"sourceUrl": "https://history.com"}},
								},
							},
						},
					},
					{
						Kind: "data",
						Data: map[string]any{
							"type": "tool_result",
							"result_data": map[string]any{
								"rag_metadata": map[string]any{
									"query": "q", "searchType": "document_search",
									"sources": []any{map[string]any{"sourceUrl": "https://status.com"}},
								},
							},
						},
					},
				},
			},
		},
	}
	entries := extractRAGFromTask(task)
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2 (status + history, deduplicated)", len(entries))
	}
}

func TestRAGFromStatusUpdates_AccumulatesRAG(t *testing.T) {
	svc := newRAGAccumulator()

	event1 := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{
						Kind: "data",
						Data: map[string]any{
							"type": "tool_result",
							"result_data": map[string]any{
								"rag_metadata": map[string]any{
									"query": "q", "searchType": "document_search",
									"sources": []any{map[string]any{"sourceUrl": "https://a.com"}},
								},
							},
						},
					},
				},
			},
		},
	}
	svc.handleStatusUpdate("task-1", event1)

	if len(svc.accumulatedRAG["task-1"]) != 1 {
		t.Fatalf("after first update: len = %d, want 1", len(svc.accumulatedRAG["task-1"]))
	}

	event2 := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{
						Kind: "data",
						Data: map[string]any{
							"type": "tool_result",
							"result_data": map[string]any{
								"rag_metadata": map[string]any{
									"query": "q", "searchType": "document_search",
									"sources": []any{map[string]any{"sourceUrl": "https://a.com"}},
								},
							},
						},
					},
					{
						Kind: "data",
						Data: map[string]any{
							"type": "tool_result",
							"result_data": map[string]any{
								"rag_metadata": map[string]any{
									"query": "q", "searchType": "document_search",
									"sources": []any{map[string]any{"sourceUrl": "https://b.com"}},
								},
							},
						},
					},
				},
			},
		},
	}
	svc.handleStatusUpdate("task-1", event2)

	if len(svc.accumulatedRAG["task-1"]) != 2 {
		t.Fatalf("after second update: len = %d, want 2 (deduplicated)", len(svc.accumulatedRAG["task-1"]))
	}
}

func TestMaxRAGEntriesPerTaskIsABoundedNumber(t *testing.T) {
	if maxRAGEntriesPerTask > 600 {
		t.Errorf("maxRAGEntriesPerTask = %d; this caps per-task memory and must stay small", maxRAGEntriesPerTask)
	}
}

func TestRAGFromStatusUpdates_CapsEntries(t *testing.T) {
	svc := newRAGAccumulator()

	existing := make([]map[string]any, maxRAGEntriesPerTask)
	for i := range existing {
		existing[i] = map[string]any{"source_url": fmt.Sprintf("https://existing-%d.com", i)}
	}
	capped := make([]ragEntry, len(existing))
	for i, e := range existing {
		capped[i] = ragEntry{envelope: e}
	}
	svc.accumulatedRAG["task-cap"] = capped

	event := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					{
						Kind: "data",
						Data: map[string]any{
							"type": "tool_result",
							"result_data": map[string]any{
								"rag_metadata": map[string]any{
									"query": "overflow", "searchType": "document_search",
									"sources": []any{map[string]any{"sourceUrl": "https://overflow.com"}},
								},
							},
						},
					},
				},
			},
		},
	}
	svc.handleStatusUpdate("task-cap", event)

	if len(svc.accumulatedRAG["task-cap"]) != maxRAGEntriesPerTask {
		t.Errorf("len = %d, want %d (should not exceed cap)", len(svc.accumulatedRAG["task-cap"]), maxRAGEntriesPerTask)
	}
}

func TestSaveChatTask_WithRAGData(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())

	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	svc := core.ScheduledTasks()
	if svc == nil {
		t.Fatal("ScheduledTasks() should not be nil")
	}

	sessRepo := store.NewSessionRepo(core.Store())
	sessionID := idgen.New()
	sessName := "Research Task"
	if err := sessRepo.Create(ctx, &store.Session{
		ID:     sessionID,
		Name:   &sessName,
		UserID: "user-1",
		Source: new(string),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	task := &store.ScheduledTask{
		ID:              "sched-1",
		Name:            "Research Task",
		TaskMessage:     `[{"type":"text","text":"Search for info"}]`,
		TargetAgentName: "research-agent",
		CreatedBy:       "user-1",
	}

	a2aTask := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{{Kind: "text", Text: "Here are the results."}},
			},
		},
	}

	ragData := []map[string]any{
		{"source_url": "https://example.com/doc1", "title": "Document 1"},
		{"source_url": "https://example.com/doc2", "title": "Document 2"},
	}

	svc.saveChatTask(ctx, task, sessionID, "a2a-rag-1", a2aTask, "", ragData)

	ct, err := svc.chatRepo.GetByID(ctx, "a2a-rag-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ct == nil {
		t.Fatal("ChatTask not found")
	}

	var metadata map[string]any
	if err := json.Unmarshal([]byte(*ct.TaskMetadata), &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	ragDataResult, ok := metadata["ragData"]
	if !ok {
		t.Fatal("metadata missing ragData")
	}

	ragList, ok := ragDataResult.([]any)
	if !ok {
		t.Fatalf("ragData type = %T, want []any", ragDataResult)
	}
	if len(ragList) != 2 {
		t.Errorf("ragData len = %d, want 2", len(ragList))
	}

	for i, entry := range ragList {
		m, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("ragData[%d] type = %T, want map", i, entry)
		}
		if m["taskId"] != "a2a-rag-1" {
			t.Errorf("ragData[%d].taskId = %v, want 'a2a-rag-1'", i, m["taskId"])
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

type recordingSigner struct {
	mu    sync.Mutex
	calls []*auth.Subject
}

func (r *recordingSigner) SignTask(identity *auth.Subject, _ string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, identity)
	return "stub-token", nil
}

func (r *recordingSigner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingSigner) firstIdentity() *auth.Subject {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[0]
}

type stubIdentityResolver struct {
	mu    sync.Mutex
	calls int
	asked string
	ident *auth.Subject
	err   error
}

func (s *stubIdentityResolver) ResolveIdentity(_ context.Context, userID string) (*auth.Subject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.asked = userID
	return s.ident, s.err
}

func (s *stubIdentityResolver) askedFor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked
}

func (s *stubIdentityResolver) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func setupIdentityTestCore(t *testing.T, opts ...runtime.Option) *GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}
	core := NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil, opts...)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "test-agent"})
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return core
}

func makeIdentityTestTask(id, createdBy string) *store.ScheduledTask {
	return &store.ScheduledTask{
		ID:                 id,
		Name:               "identity-test-task",
		Namespace:          "test-ns",
		CreatedBy:          createdBy,
		ScheduleType:       "cron",
		ScheduleExpression: "0 * * * *",
		TargetAgentName:    "test-agent",
		TaskMessage:        `[{"type":"text","text":"run scheduled task"}]`,
		Enabled:            true,
		MaxRetries:         0,
	}
}

func insertTask(t *testing.T, svc *ScheduledTaskService, task *store.ScheduledTask) {
	t.Helper()
	if err := svc.repo.Create(t.Context(), task); err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

func installFailingResolver(t *testing.T, core *GatewayCore, resolverErr error) (*recordingSigner, *stubIdentityResolver) {
	t.Helper()
	signer := &recordingSigner{}
	core.SetTaskSigner(signer)
	resolver := &stubIdentityResolver{err: resolverErr}
	core.SetIdentityResolver(resolver)
	return signer, resolver
}

func TestTriggerTaskNow_NotFound(t *testing.T) {
	core := setupIdentityTestCore(t)
	svc := core.ScheduledTasks()

	_, err := svc.TriggerTaskNow(t.Context(), "does-not-exist", "alice")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("err = %v, want ErrTaskNotFound", err)
	}
}

func ragSignalEvent(t *testing.T, data a2a.RAGInfoUpdateData) *a2a.TaskStatusUpdateEvent {
	t.Helper()
	return &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateWorking,
			Message: &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{ragInfoUpdateDataPart(t, data)}},
		},
	}
}

func ragSource(id, url, preview string, fetched bool) a2a.RAGSource {
	s := a2a.RAGSource{CitationID: id, Title: id, SourceURL: url, URL: url, ContentPreview: preview, RelevanceScore: 1}
	if fetched {
		s.Metadata = map[string]any{"fetched": true}
	}
	return s
}

func TestRAGFromStatusUpdates_DeepResearchSnapshotsCollapseToLatest(t *testing.T) {
	svc := newRAGAccumulator()
	const question = "what is solace agent mesh"
	svc.handleStatusUpdate("task-1", ragSignalEvent(t, a2a.RAGInfoUpdateData{
		Type: string(a2a.SignalRAGInfoUpdate), Query: question, SearchType: "deep_research",
		Sources:   []a2a.RAGSource{ragSource("s0r0", "https://a.com", "unfetched snippet", false)},
		Timestamp: "2024-01-01T00:00:00Z",
	}))
	svc.handleStatusUpdate("task-1", ragSignalEvent(t, a2a.RAGInfoUpdateData{
		Type: string(a2a.SignalRAGInfoUpdate), Query: question, SearchType: "deep_research",
		Sources: []a2a.RAGSource{
			ragSource("s0r0", "https://a.com", "the full fetched page text", true),
			ragSource("s0r1", "https://b.com", "second source", true),
		},
		IsComplete: true,
		Timestamp:  "2024-01-01T00:05:00Z",
	}))

	got := ragEnvelopes(svc.accumulatedRAG["task-1"])
	if len(got) != 1 {
		t.Fatalf("accumulated %d entries, want 1 — every cumulative snapshot was kept", len(got))
	}
	if got[0]["timestamp"] != "2024-01-01T00:05:00Z" {
		t.Errorf("kept the earlier snapshot (timestamp %v); the latest must win", got[0]["timestamp"])
	}
	if got[0]["isComplete"] != true {
		t.Errorf("isComplete = %v, want true — the panel has no other way to know the run finished", got[0]["isComplete"])
	}
	sources, _ := got[0]["sources"].([]any)
	if len(sources) != 2 {
		t.Fatalf("sources = %d, want 2 from the final snapshot", len(sources))
	}
	first, _ := sources[0].(map[string]any)
	if first["contentPreview"] != "the full fetched page text" {
		t.Errorf("citations resolve to the unfetched snapshot: contentPreview = %v", first["contentPreview"])
	}
}

func TestRAGFromStatusUpdates_RepeatedWebSearchQueryKeepsBothTurns(t *testing.T) {
	svc := newRAGAccumulator()
	const query = "solace agent mesh"
	for _, tc := range []struct {
		turn int
		id   string
	}{{0, "s0r0"}, {3, "s3r0"}} {
		svc.handleStatusUpdate("task-1", ragSignalEvent(t, a2a.RAGInfoUpdateData{
			Type: string(a2a.SignalRAGInfoUpdate), Query: query, SearchType: "web_search",
			SearchTurn: tc.turn, IsComplete: true,
			Sources:   []a2a.RAGSource{ragSource(tc.id, "https://solace.com", "preview", false)},
			Timestamp: "2024-01-01T00:00:00Z",
		}))
	}

	got := ragEnvelopes(svc.accumulatedRAG["task-1"])
	if len(got) != 2 {
		t.Fatalf("accumulated %d entries, want 2 — the later turn replaced the earlier one", len(got))
	}
	seen := map[string]bool{}
	for _, e := range got {
		sources, _ := e["sources"].([]any)
		for _, s := range sources {
			if m, ok := s.(map[string]any); ok {
				seen[fmt.Sprint(m["citationId"])] = true
			}
		}
	}
	for _, id := range []string{"s0r0", "s3r0"} {
		if !seen[id] {
			t.Errorf("citation %s is unresolvable after the repeated query", id)
		}
	}
}

func TestRAGFromStatusUpdates_IndexSearchTurnsAreNotCollapsed(t *testing.T) {
	svc := newRAGAccumulator()
	event := func(turn int, cid string, sources []any) *a2a.TaskStatusUpdateEvent {
		return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{{
				Kind: "data",
				Data: map[string]any{
					"type": "tool_result",
					"result_data": map[string]any{"rag_metadata": map[string]any{
						"query": "quarterly revenue", "searchType": "document_search",
						"turnNumber": float64(turn), "timestamp": "2024-01-01T00:00:0" + cid + "Z",
						"sources": sources,
					}},
				},
			}}},
		}}
	}
	src := func(id string) []any {
		return []any{map[string]any{"citationId": id, "sourceUrl": "doc://" + id}}
	}
	svc.handleStatusUpdate("task-1", event(0, "0", src("idx0r0")))
	svc.handleStatusUpdate("task-1", event(1, "1", src("idx1r0")))
	svc.handleStatusUpdate("task-1", event(2, "2", []any{}))

	got := ragEnvelopes(svc.accumulatedRAG["task-1"])
	seen := map[string]bool{}
	for _, e := range got {
		if ss, ok := e["sources"].([]any); ok {
			for _, x := range ss {
				if m, ok := x.(map[string]any); ok {
					seen[fmt.Sprint(m["citationId"])] = true
				}
			}
		}
	}
	for _, id := range []string{"idx0r0", "idx1r0"} {
		if !seen[id] {
			t.Errorf("citation %s was collapsed away; entries=%d", id, len(got))
		}
	}
	if len(got) != 2 {
		t.Errorf("entries = %d, want 2; the sourceless envelope was stored as junk", len(got))
	}
}

func TestRAGFromStatusUpdates_ReplacesASignalEntryWhileAtCap(t *testing.T) {
	svc := newRAGAccumulator()
	const question = "what is solace agent mesh"
	svc.handleStatusUpdate("task-1", ragSignalEvent(t, a2a.RAGInfoUpdateData{
		Type: string(a2a.SignalRAGInfoUpdate), Query: question, SearchType: "deep_research",
		Sources:   []a2a.RAGSource{ragSource("s0r0", "https://a.com", "unfetched snippet", false)},
		Timestamp: "2024-01-01T00:00:00Z",
	}))
	filled := svc.accumulatedRAG["task-1"]
	for len(filled) < maxRAGEntriesPerTask {
		filled = append(filled, ragEntry{envelope: map[string]any{
			"source_url": fmt.Sprintf("https://filler-%d.example", len(filled)),
		}})
	}
	svc.accumulatedRAG["task-1"] = filled

	svc.handleStatusUpdate("task-1", &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateWorking,
		Message: &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{
			ragInfoUpdateDataPart(t, a2a.RAGInfoUpdateData{
				Type: string(a2a.SignalRAGInfoUpdate), Query: "an unrelated new query",
				SearchType: "web_search", SearchTurn: 9, IsComplete: true,
				Sources:   []a2a.RAGSource{ragSource("s9r0", "https://new.example", "p", false)},
				Timestamp: "2024-01-01T00:04:00Z",
			}),
			ragInfoUpdateDataPart(t, a2a.RAGInfoUpdateData{
				Type: string(a2a.SignalRAGInfoUpdate), Query: question, SearchType: "deep_research",
				Sources:    []a2a.RAGSource{ragSource("s0r0", "https://a.com", "the full fetched page text", true)},
				IsComplete: true,
				Timestamp:  "2024-01-01T00:05:00Z",
			}),
		}},
	}})

	got := ragEnvelopes(svc.accumulatedRAG["task-1"])
	if len(got) != maxRAGEntriesPerTask {
		t.Fatalf("entries = %d, want %d (cap must still bound growth)", len(got), maxRAGEntriesPerTask)
	}
	sources, _ := got[0]["sources"].([]any)
	if len(sources) == 0 {
		t.Fatal("the signal entry lost its sources")
	}
	first, _ := sources[0].(map[string]any)
	if first["contentPreview"] != "the full fetched page text" {
		t.Errorf("at cap the entry froze on the unfetched snapshot: %v", first["contentPreview"])
	}
}

func TestRAGFromStatusUpdates_SignalDoesNotReplaceAToolResultEntry(t *testing.T) {
	svc := newRAGAccumulator()
	const query = "solace agent mesh"
	svc.handleStatusUpdate("task-1", &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State: a2a.TaskStateWorking,
		Message: &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{{
			Kind: "data",
			Data: map[string]any{
				"type": "tool_result",
				"result_data": map[string]any{"rag_metadata": map[string]any{
					"query": query, "searchType": "web_search", "turnNumber": 1,
					"timestamp": "2024-01-01T00:00:00Z",
					"sources":   []any{map[string]any{"citationId": "s1r0", "sourceUrl": "https://peer.example"}},
				}},
			},
		}}},
	}})
	svc.handleStatusUpdate("task-1", ragSignalEvent(t, a2a.RAGInfoUpdateData{
		Type: string(a2a.SignalRAGInfoUpdate), Query: query, SearchType: "web_search",
		SearchTurn: 0, IsComplete: true,
		Sources:   []a2a.RAGSource{ragSource("s0r0", "https://solace.com", "preview", false)},
		Timestamp: "2024-01-01T00:05:00Z",
	}))

	got := ragEnvelopes(svc.accumulatedRAG["task-1"])
	seen := map[string]bool{}
	for _, e := range got {
		if ss, ok := e["sources"].([]any); ok {
			for _, x := range ss {
				if m, ok := x.(map[string]any); ok {
					seen[fmt.Sprint(m["citationId"])] = true
				}
			}
		}
	}
	for _, id := range []string{"s1r0", "s0r0"} {
		if !seen[id] {
			t.Errorf("citation %s stranded: the signal replaced a tool-result entry (entries=%d)", id, len(got))
		}
	}
}

func TestRAGEnvelopes_ClonesSoCallersCannotMutateStoredEntries(t *testing.T) {
	stored := []ragEntry{
		{envelope: map[string]any{"query": "q", "searchType": "web_search", "sources": []any{}}, fromSignal: true},
	}
	out := ragEnvelopes(stored)
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	out[0]["taskId"] = "task-1"
	if _, leaked := stored[0].envelope["taskId"]; leaked {
		t.Error("a caller's write reached the stored envelope; concurrent reads would abort the process")
	}
}

func TestRAGEnvelopeFromToolResult_RequiresSources(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
		want bool
	}{
		{"populated sources", map[string]any{"query": "q", "searchType": "document_search",
			"sources": []any{map[string]any{"sourceUrl": "https://a.com"}}}, true},
		{"empty sources", map[string]any{"query": "q", "searchType": "document_search",
			"sources": []any{}}, false},
		{"sources key absent", map[string]any{"query": "q", "searchType": "document_search",
			"turnNumber": 3, "timestamp": "2024-01-01T00:00:00Z"}, false},
		{"sources is not a list", map[string]any{"query": "q", "sources": "nope"}, false},
		{"typed Go slice is rejected, since only broker-decoded sources reach this path", map[string]any{"query": "q",
			"sources": []a2a.RAGSource{{CitationID: "idx0r0"}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := ragEnvelopeFromToolResult(map[string]any{"result_data": map[string]any{"rag_metadata": c.meta}})
			if ok != c.want {
				t.Errorf("accepted = %v, want %v", ok, c.want)
			}
		})
	}
}

func TestDecodeTaskMessage(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantParts int
		wantText  string
	}{
		{"parts array", `[{"kind":"text","text":"hi"}]`, 1, ""},
		{"bare string is unquoted", `"https://x"`, -1, "https://x"},
		{"non-JSON raw preserved", `hello`, -1, "hello"},
		{"empty array", `[]`, 0, ""},
		{"null", `null`, -1, ""},
		{"malformed raw preserved", `[{`, -1, `[{`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts, text := decodeTaskMessage(tt.raw)
			if tt.wantParts < 0 {
				if parts != nil {
					t.Errorf("parts = %v, want nil", parts)
				}
			} else {
				if parts == nil {
					t.Fatalf("parts = nil, want non-nil with len %d", tt.wantParts)
				}
				if len(parts) != tt.wantParts {
					t.Errorf("len(parts) = %d, want %d", len(parts), tt.wantParts)
				}
			}
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
		})
	}
}

func TestRenderTemplate_BareStringIsUnquoted(t *testing.T) {
	svc := &ScheduledTaskService{}
	task := &store.ScheduledTask{
		Name:        "Test Task",
		TaskMessage: `"https://cameras.example.com/camera?id=133"`,
		RunCount:    1,
		Timezone:    "UTC",
	}
	got := svc.renderTemplate(task, "exec-1")
	want := "https://cameras.example.com/camera?id=133"
	if got != want {
		t.Errorf("renderTemplate = %q, want %q (no surrounding quotes)", got, want)
	}
}

func discardSvc() *ScheduledTaskService {
	return &ScheduledTaskService{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestDataParts(t *testing.T) {
	tests := []struct {
		name          string
		targetType    string
		taskMsg       string
		wantLen       int
		wantFirstKey  string
		wantFirstVal  string
		wantSecondKey string
		wantSecondVal string
	}{
		{
			name: "kind discriminator", targetType: "workflow",
			taskMsg:      `[{"kind":"data","data":{"image_url":"https://a"}}]`,
			wantLen:      1,
			wantFirstKey: "image_url", wantFirstVal: "https://a",
		},
		{
			name: "type discriminator", targetType: "workflow",
			taskMsg:      `[{"type":"data","data":{"image_url":"https://b"}}]`,
			wantLen:      1,
			wantFirstKey: "image_url", wantFirstVal: "https://b",
		},
		{
			name: "text only yields nothing", targetType: "workflow",
			taskMsg: `[{"kind":"text","text":"hello"}]`, wantLen: 0,
		},
		{
			name: "two data parts both forwarded", targetType: "workflow",
			taskMsg:      `[{"kind":"data","data":{"a":"1"}},{"kind":"data","data":{"b":"2"}}]`,
			wantLen:      2,
			wantFirstKey: "a", wantFirstVal: "1",
			wantSecondKey: "b", wantSecondVal: "2",
		},
		{
			name: "data part with null data is skipped", targetType: "workflow",
			taskMsg: `[{"kind":"data","data":null}]`, wantLen: 0,
		},
		{
			name: "data part with no data key is skipped", targetType: "workflow",
			taskMsg: `[{"kind":"data"}]`, wantLen: 0,
		},
		{
			name: "agent target drops data parts", targetType: "agent",
			taskMsg: `[{"kind":"data","data":{"a":"1"}}]`, wantLen: 0,
		},
		{
			name: "unset target type behaves like an agent, not a workflow", targetType: "",
			taskMsg: `[{"kind":"data","data":{"a":"1"}}]`, wantLen: 0,
		},
		{
			name: "bare object yields nothing", targetType: "workflow",
			taskMsg: `{"kind":"data","data":{"a":"1"}}`, wantLen: 0,
		},
		{
			name: "bare string yields nothing", targetType: "workflow",
			taskMsg: `"https://x"`, wantLen: 0,
		},
		{
			name: "empty array yields nothing", targetType: "workflow",
			taskMsg: `[]`, wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := discardSvc()
			task := &store.ScheduledTask{
				ID: "task-1", TargetType: tt.targetType, TaskMessage: tt.taskMsg,
			}
			out := svc.dataParts(task, "exec-1")
			if len(out) != tt.wantLen {
				t.Fatalf("len(dataParts) = %d, want %d (got %+v)", len(out), tt.wantLen, out)
			}
			if tt.wantLen == 0 {
				return
			}
			if out[0].Kind != "data" {
				t.Errorf("out[0].Kind = %q, want \"data\"", out[0].Kind)
			}
			m, ok := out[0].Data.(map[string]any)
			if !ok {
				t.Fatalf("out[0].Data is %T, want map[string]any", out[0].Data)
			}
			if got, _ := m[tt.wantFirstKey].(string); got != tt.wantFirstVal {
				t.Errorf("out[0].Data[%q] = %q, want %q", tt.wantFirstKey, got, tt.wantFirstVal)
			}
			if tt.wantSecondKey == "" {
				return
			}
			if out[1].Kind != "data" {
				t.Errorf("out[1].Kind = %q, want \"data\"", out[1].Kind)
			}
			m2, ok := out[1].Data.(map[string]any)
			if !ok {
				t.Fatalf("out[1].Data is %T, want map[string]any", out[1].Data)
			}
			if got, _ := m2[tt.wantSecondKey].(string); got != tt.wantSecondVal {
				t.Errorf("out[1].Data[%q] = %q, want %q", tt.wantSecondKey, got, tt.wantSecondVal)
			}
		})
	}
}

func capturingSvc(buf *bytes.Buffer) *ScheduledTaskService {
	return &ScheduledTaskService{logger: slog.New(slog.NewTextHandler(buf, nil))}
}

func TestDataParts_BareObjectWarns(t *testing.T) {
	var buf bytes.Buffer
	svc := capturingSvc(&buf)
	task := &store.ScheduledTask{
		ID: "task-1", TargetType: "workflow",
		TaskMessage: `{"kind":"data","data":{"a":"1"}}`,
	}

	if out := svc.dataParts(task, "exec-1"); len(out) != 0 {
		t.Fatalf("len(dataParts) = %d, want 0", len(out))
	}
	if got := buf.String(); !strings.Contains(got, "wrap it in [ ]") {
		t.Errorf("log does not mention wrapping in [ ]; got %q", got)
	}
}

func TestDataParts_MalformedArrayWarns(t *testing.T) {
	var buf bytes.Buffer
	svc := capturingSvc(&buf)
	task := &store.ScheduledTask{
		ID: "task-1", TargetType: "workflow",
		TaskMessage: `["urgent",{"kind":"data","data":{"a":"1"}}]`,
	}

	if out := svc.dataParts(task, "exec-1"); len(out) != 0 {
		t.Fatalf("len(dataParts) = %d, want 0", len(out))
	}
	if got := buf.String(); !strings.Contains(got, "not a uniform array of part objects") {
		t.Errorf("log does not warn about the malformed parts array; got %q", got)
	}
}

func TestDataParts_NonObjectDataWarns(t *testing.T) {
	var buf bytes.Buffer
	svc := capturingSvc(&buf)
	task := &store.ScheduledTask{
		ID: "task-1", TargetType: "workflow",
		TaskMessage: `[{"kind":"data","data":["a","b"]}]`,
	}

	if out := svc.dataParts(task, "exec-1"); len(out) != 1 {
		t.Fatalf("len(dataParts) = %d, want 1 (forwarded, not dropped)", len(out))
	}
	if got := buf.String(); !strings.Contains(got, "not a JSON object") {
		t.Errorf("log does not warn about the non-object data part; got %q", got)
	}
}

func TestDataParts_MultiplePartsWarns(t *testing.T) {
	var buf bytes.Buffer
	svc := capturingSvc(&buf)
	task := &store.ScheduledTask{
		ID: "task-1", TargetType: "workflow",
		TaskMessage: `[{"kind":"data","data":{"a":"1"}},{"kind":"data","data":{"b":"2"}}]`,
	}

	if out := svc.dataParts(task, "exec-1"); len(out) != 2 {
		t.Fatalf("len(dataParts) = %d, want 2 (still forwarded, not dropped)", len(out))
	}
	if got := buf.String(); !strings.Contains(got, "only consumes the first") {
		t.Errorf("log does not warn that only the first data part is consumed; got %q", got)
	}
}

func TestDataParts_NoTypeKeyOnEmittedPart(t *testing.T) {
	svc := discardSvc()
	task := &store.ScheduledTask{
		ID: "task-1", TargetType: "workflow",
		TaskMessage: `[{"kind":"data","data":{"image_1":"https://a"}}]`,
	}
	out := svc.dataParts(task, "exec-1")
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	m, ok := out[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("out[0].Data is %T, want map[string]any", out[0].Data)
	}
	if _, present := m["type"]; present {
		t.Error(`emitted data map must not carry a "type" key; ` +
			`extractWorkflowInput skips type=="structured_invocation_request"`)
	}
	if len(m) != 1 {
		t.Errorf("data map = %+v, want exactly the caller's keys", m)
	}
}

func TestDataParts_SubstitutesTemplateVars(t *testing.T) {
	svc := discardSvc()
	task := &store.ScheduledTask{
		ID:         "task-1",
		Name:       "Nightly Rollup",
		TargetType: "workflow",
		TaskMessage: `[{"kind":"data","data":{` +
			`"schedule":"{{schedule.name}}",` +
			`"exec":"{{execution.id}}",` +
			`"nested":{"count":"{{schedule.run_count}}"}` +
			`}}]`,
		RunCount: 3,
		Timezone: "UTC",
	}

	out := svc.dataParts(task, "exec-42")
	if len(out) != 1 {
		t.Fatalf("len(dataParts) = %d, want 1", len(out))
	}
	m, ok := out[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("out[0].Data is %T, want map[string]any", out[0].Data)
	}
	if got, _ := m["schedule"].(string); got != "Nightly Rollup" {
		t.Errorf(`data["schedule"] = %q, want %q`, got, "Nightly Rollup")
	}
	if got, _ := m["exec"].(string); got != "exec-42" {
		t.Errorf(`data["exec"] = %q, want %q`, got, "exec-42")
	}
	nested, ok := m["nested"].(map[string]any)
	if !ok {
		t.Fatalf(`data["nested"] is %T, want map[string]any`, m["nested"])
	}
	if got, _ := nested["count"].(string); got != "3" {
		t.Errorf(`data["nested"]["count"] = %q, want %q`, got, "3")
	}
}

func TestBuildMessage_WorkflowTargetGetsDataPart(t *testing.T) {
	svc := discardSvc()
	task := &store.ScheduledTask{
		ID:              "task-1",
		Name:            "Four Cams",
		TargetAgentName: "Detect And Annotate Four Images",
		TargetType:      "workflow",
		TaskMessage: `[{"kind":"text","text":"annotate four images"},` +
			`{"kind":"data","data":{"image_1":"https://a","image_2":"https://b"}}]`,
		RunCount: 0,
		Timezone: "UTC",
	}

	msg := svc.buildMessage(task, "exec-1", "session-1")

	if len(msg.Parts) != 2 {
		t.Fatalf("len(msg.Parts) = %d, want 2 (text then data); parts=%+v", len(msg.Parts), msg.Parts)
	}
	if msg.Parts[0].Kind != "text" {
		t.Errorf("Parts[0].Kind = %q, want \"text\" (text stays at index 0)", msg.Parts[0].Kind)
	}
	if msg.Parts[0].Text != "annotate four images" {
		t.Errorf("Parts[0].Text = %q, want %q", msg.Parts[0].Text, "annotate four images")
	}
	if msg.Parts[1].Kind != "data" {
		t.Fatalf("Parts[1].Kind = %q, want \"data\"", msg.Parts[1].Kind)
	}
	m, ok := msg.Parts[1].Data.(map[string]any)
	if !ok {
		t.Fatalf("Parts[1].Data is %T, want map[string]any", msg.Parts[1].Data)
	}
	if got, _ := m["image_1"].(string); got != "https://a" {
		t.Errorf("data[image_1] = %q, want %q", got, "https://a")
	}
	if got, _ := m["image_2"].(string); got != "https://b" {
		t.Errorf("data[image_2] = %q, want %q", got, "https://b")
	}
}

func TestBuildMessage_AgentTargetUnchanged(t *testing.T) {
	svc := discardSvc()
	task := &store.ScheduledTask{
		ID:              "task-1",
		Name:            "Daily",
		TargetAgentName: "agent1",
		TargetType:      "agent",
		TaskMessage: `[{"kind":"text","text":"do the thing"},` +
			`{"kind":"data","data":{"a":"1"}}]`,
		RunCount: 0,
		Timezone: "UTC",
	}

	msg := svc.buildMessage(task, "exec-1", "session-1")

	if len(msg.Parts) != 1 {
		t.Fatalf("len(msg.Parts) = %d, want exactly 1 for an agent target", len(msg.Parts))
	}
	if msg.Parts[0].Kind != "text" {
		t.Errorf("Parts[0].Kind = %q, want \"text\"", msg.Parts[0].Kind)
	}
}

func TestBuildMessage_TextOnlyWorkflowUnchanged(t *testing.T) {
	svc := discardSvc()
	task := &store.ScheduledTask{
		ID:              "task-1",
		Name:            "Cam Check",
		TargetAgentName: "Scheduled Cam Check",
		TargetType:      "workflow",
		TaskMessage:     `[{"kind":"text","text":"check the camera"}]`,
		RunCount:        0,
		Timezone:        "UTC",
	}

	msg := svc.buildMessage(task, "exec-1", "session-1")

	if len(msg.Parts) != 1 {
		t.Fatalf("len(msg.Parts) = %d, want exactly 1", len(msg.Parts))
	}
	if msg.Parts[0].Text != "check the camera" {
		t.Errorf("Parts[0].Text = %q, want %q", msg.Parts[0].Text, "check the camera")
	}
}
