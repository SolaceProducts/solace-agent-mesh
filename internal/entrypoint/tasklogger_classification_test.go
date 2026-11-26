// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func registerForClassification(reg *AgentRegistry, name, typ string) {
	card := a2a.AgentCard{Name: name}
	if typ == store.TaskHandlerTypeWorkflow {
		card.Capabilities.Extensions = []a2a.AgentExtension{{
			URI:    agent.ExtensionURIAgentType,
			Params: map[string]any{"type": "workflow"},
		}}
	}
	reg.AddOrUpdate(card)
}

func TestTaskLogger_RequestPopulatesHandlerFromTopic(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	reg := NewAgentRegistry()
	registerForClassification(reg, "news_scraper", store.TaskHandlerTypeWorkflow)

	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default()).
		WithAgentRegistry(reg)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})

	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("agent-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/NewsScraper",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("workflow-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/news_scraper",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	})

	cancel()
	svc.Stop()

	agentTask, _ := taskRepo.GetByID(context.Background(), tid("agent-task"))
	if agentTask == nil {
		t.Fatal("expected NewsScraper task row")
	}
	if agentTask.HandlerName == nil || *agentTask.HandlerName != "NewsScraper" {
		t.Errorf("agent handler_name = %v, want NewsScraper", agentTask.HandlerName)
	}
	if agentTask.HandlerType != nil && *agentTask.HandlerType != "" {
		t.Errorf("unregistered agent handler_type = %v, want NULL/empty", *agentTask.HandlerType)
	}

	wfTask, _ := taskRepo.GetByID(context.Background(), tid("workflow-task"))
	if wfTask == nil {
		t.Fatal("expected news_scraper workflow task row")
	}
	if wfTask.HandlerName == nil || *wfTask.HandlerName != "news_scraper" {
		t.Errorf("workflow handler_name = %v, want news_scraper", wfTask.HandlerName)
	}
	if wfTask.HandlerType == nil || *wfTask.HandlerType != store.TaskHandlerTypeWorkflow {
		t.Errorf("workflow handler_type = %v, want %q", wfTask.HandlerType, store.TaskHandlerTypeWorkflow)
	}
}

func TestTaskLogger_NilRegistry_HandlerNameSetTypeStaysNull(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("nil-registry-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/SomeAgent",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	})

	cancel()
	svc.Stop()

	row, err := taskRepo.GetByID(context.Background(), tid("nil-registry-task"))
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("expected task row")
	}
	if row.HandlerName == nil || *row.HandlerName != "SomeAgent" {
		t.Errorf("handler_name = %v, want SomeAgent (topic-derived, no registry needed)", row.HandlerName)
	}
	if row.HandlerType != nil {
		t.Errorf("handler_type = %v, want NULL (no registry to classify against)", *row.HandlerType)
	}
}

func TestTaskLogger_RegisteredAgentCard_HandlerTypeIsAgent(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	reg := NewAgentRegistry()
	registerForClassification(reg, "PlainAgent", store.TaskHandlerTypeAgent)

	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default()).
		WithAgentRegistry(reg)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("plain-agent-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/PlainAgent",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	})

	cancel()
	svc.Stop()

	row, err := taskRepo.GetByID(context.Background(), tid("plain-agent-task"))
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("expected task row")
	}
	if row.HandlerType == nil || *row.HandlerType != store.TaskHandlerTypeAgent {
		t.Errorf("handler_type = %v, want %q (registered card, no workflow extension)", row.HandlerType, store.TaskHandlerTypeAgent)
	}
}

func TestTaskLogger_UUIDPrefixedName_ClassifiesWithoutRegistry(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("uuid-agent-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/agent_1e2d3c4b_5a69_4f0e_8b1a_0f9e8d7c6b5a",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("uuid-workflow-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/workflow_1e2d3c4b_5a69_4f0e_8b1a_0f9e8d7c6b5a",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	})

	cancel()
	svc.Stop()

	agentTask, _ := taskRepo.GetByID(context.Background(), tid("uuid-agent-task"))
	if agentTask == nil {
		t.Fatal("expected agent_ task row")
	}
	if agentTask.HandlerType == nil || *agentTask.HandlerType != store.TaskHandlerTypeAgent {
		t.Errorf("agent_<uuid> handler_type = %v, want %q (no registry needed)", agentTask.HandlerType, store.TaskHandlerTypeAgent)
	}

	wfTask, _ := taskRepo.GetByID(context.Background(), tid("uuid-workflow-task"))
	if wfTask == nil {
		t.Fatal("expected workflow_ task row")
	}
	if wfTask.HandlerType == nil || *wfTask.HandlerType != store.TaskHandlerTypeWorkflow {
		t.Errorf("workflow_<uuid> handler_type = %v, want %q (no registry needed)", wfTask.HandlerType, store.TaskHandlerTypeWorkflow)
	}
}

func TestTaskLogger_UUIDPrefixedName_OverridesContradictingCard(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	const name = "agent_1e2d3c4b_5a69_4f0e_8b1a_0f9e8d7c6b5a"
	reg := NewAgentRegistry()
	registerForClassification(reg, name, store.TaskHandlerTypeWorkflow)

	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default()).
		WithAgentRegistry(reg)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("prefix-wins-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/" + name,
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "hi",
		ExecutionMode:      "foreground",
	})

	cancel()
	svc.Stop()

	row, _ := taskRepo.GetByID(context.Background(), tid("prefix-wins-task"))
	if row == nil {
		t.Fatal("expected task row")
	}
	if row.HandlerType == nil || *row.HandlerType != store.TaskHandlerTypeAgent {
		t.Errorf("handler_type = %v, want %q (prefix wins over contradicting card)", row.HandlerType, store.TaskHandlerTypeAgent)
	}
}

func TestTaskLogger_PlaceholderRaceBackfillsHandler(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	reg := NewAgentRegistry()
	registerForClassification(reg, "news_scraper", store.TaskHandlerTypeWorkflow)

	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default()).
		WithAgentRegistry(reg)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	statusPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "status-update"}})
	svc.LogEvent(TaskLogEvent{
		TaskID:    tid("race-task"),
		UserID:    "u1",
		Topic:     "test-ns/a2a/v1/gateway/status/gw-other/race-task",
		Direction: "status",
		EventKind: "status-update",
		Payload:   statusPayload,
	})

	reqPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("race-task"),
		UserID:             "u1",
		Topic:              "test-ns/a2a/v1/agent/request/news_scraper",
		Direction:          "request",
		Payload:            reqPayload,
		IsRequest:          true,
		InitialRequestText: "the real request",
		ExecutionMode:      "foreground",
	})

	cancel()
	svc.Stop()

	row, err := taskRepo.GetByID(context.Background(), tid("race-task"))
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatal("expected placeholder row to still exist")
	}
	if row.HandlerName == nil || *row.HandlerName != "news_scraper" {
		t.Errorf("placeholder handler_name = %v, want news_scraper (COALESCE-backfill)", row.HandlerName)
	}
	if row.HandlerType == nil || *row.HandlerType != store.TaskHandlerTypeWorkflow {
		t.Errorf("placeholder handler_type = %v, want workflow (COALESCE-backfill)", row.HandlerType)
	}
	if row.AgentName == nil || *row.AgentName != "news_scraper" {
		t.Errorf("placeholder agent_name = %v, want news_scraper (COALESCE-backfill from the request topic)", row.AgentName)
	}
	const placeholderSentinel = "[Task started before logger was active]"
	if row.InitialRequestText == nil || *row.InitialRequestText != placeholderSentinel {
		t.Errorf("placeholder InitialRequestText = %v, want the sentinel (COALESCE keeps existing)",
			row.InitialRequestText)
	}
}

func TestTaskLogger_AgentNameFromMetadataThenTopic(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	reqPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID: tid("bare-request"), UserID: "u1", Direction: "request", IsRequest: true,
		Topic:   "test-ns/a2a/v1/agent/request/agent_019e1877_ea74_7356_9cd3_f4c99c058418",
		Payload: reqPayload, ExecutionMode: "foreground",
	})
	svc.LogEvent(TaskLogEvent{
		TaskID: tid("named-request"), UserID: "u1", Direction: "request", IsRequest: true,
		Topic:   "test-ns/a2a/v1/agent/request/agent_019e1877_ea74_7356_9cd3_f4c99c058418",
		Payload: reqPayload, ExecutionMode: "foreground", AgentName: "from_metadata",
	})

	cancel()
	svc.Stop()

	bare, err := taskRepo.GetByID(context.Background(), tid("bare-request"))
	if err != nil || bare == nil {
		t.Fatalf("bare task: %v, %v", bare, err)
	}
	if bare.AgentName == nil || *bare.AgentName != "agent_019e1877_ea74_7356_9cd3_f4c99c058418" {
		t.Errorf("bare request agent_name = %v, want the topic's agent", bare.AgentName)
	}
	named, err := taskRepo.GetByID(context.Background(), tid("named-request"))
	if err != nil || named == nil {
		t.Fatalf("named task: %v, %v", named, err)
	}
	if named.AgentName == nil || *named.AgentName != "from_metadata" {
		t.Errorf("named request agent_name = %v, want the metadata value to win over the topic", named.AgentName)
	}
}
