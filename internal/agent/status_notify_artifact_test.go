// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestPublishArtifactSavedFromHook_EndToEnd(t *testing.T) {
	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mb.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-abc"
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: "task-abc", ContextID: "ctx-abc", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	q, err := mb.CreateQueue("hook-signals", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := mb.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}

	publishArtifactSavedFromHook(t.Context(), sp, tool.ArtifactSavedInfo{
		Filename:    "out.csv",
		Version:     3,
		MIMEType:    "text/csv",
		SizeBytes:   1024,
		Description: "extracted rows",
		Tags:        []string{"__generated"},
		ToolCallID:  "call-abc-1",
	})

	progressData := receiveSignalDataPart(t, mb, q)
	var progress a2a.ArtifactCreationProgressData
	if err := json.Unmarshal(progressData, &progress); err != nil {
		t.Fatalf("unmarshal ArtifactCreationProgressData: %v", err)
	}
	if progress.Type != string(a2a.SignalArtifactCreationProgress) {
		t.Errorf("progress Type = %q", progress.Type)
	}
	if progress.Status != "completed" {
		t.Errorf("progress Status = %q, want \"completed\"", progress.Status)
	}
	if progress.Filename != "out.csv" {
		t.Errorf("progress Filename = %q", progress.Filename)
	}
	if progress.BytesTransferred != 1024 {
		t.Errorf("progress BytesTransferred = %d", progress.BytesTransferred)
	}
	if progress.MIMEType == nil || *progress.MIMEType != "text/csv" {
		t.Errorf("progress MIMEType = %v", progress.MIMEType)
	}
	if progress.Version == nil || *progress.Version != 3 {
		t.Errorf("progress Version = %v", progress.Version)
	}
	if progress.FunctionCallID != nil {
		t.Errorf("progress FunctionCallID = %v, want nil", *progress.FunctionCallID)
	}
	if progress.Description == nil || *progress.Description != "extracted rows" {
		t.Errorf("progress Description = %v", progress.Description)
	}
	if len(progress.Tags) != 1 || progress.Tags[0] != "__generated" {
		t.Errorf("progress Tags = %v", progress.Tags)
	}

	savedData := receiveSignalDataPart(t, mb, q)
	var saved a2a.ArtifactSavedData
	if err := json.Unmarshal(savedData, &saved); err != nil {
		t.Fatalf("unmarshal ArtifactSavedData: %v", err)
	}
	if saved.Type != string(a2a.SignalArtifactSaved) {
		t.Errorf("Type = %q", saved.Type)
	}
	if saved.Filename != "out.csv" || saved.Version != 3 {
		t.Errorf("filename/version = %q/%d", saved.Filename, saved.Version)
	}
	if saved.MIMEType != "text/csv" {
		t.Errorf("MIMEType = %q", saved.MIMEType)
	}
	if saved.SizeBytes != 1024 {
		t.Errorf("SizeBytes = %d", saved.SizeBytes)
	}
	if saved.Description == nil || *saved.Description != "extracted rows" {
		t.Errorf("Description = %v", saved.Description)
	}
	if saved.FunctionCallID == nil || *saved.FunctionCallID != "call-abc-1" {
		t.Errorf("FunctionCallID = %v", saved.FunctionCallID)
	}
	if len(saved.Tags) != 1 || saved.Tags[0] != "__generated" {
		t.Errorf("Tags = %v", saved.Tags)
	}
}

func receiveSignalDataPart(t *testing.T, mb broker.Broker, q broker.Queue) json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := mb.Receive(ctx, q)
	if err != nil {
		t.Fatalf("expected signal on status topic; got error: %v", err)
	}
	var resp a2a.Response
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var event a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(resultBytes, &event); err != nil {
		t.Fatalf("unmarshal TaskStatusUpdateEvent: %v", err)
	}
	if len(event.Status.Message.Parts) == 0 {
		t.Fatal("event has no parts")
	}
	partBytes, _ := json.Marshal(event.Status.Message.Parts[0])
	var dataPart struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(partBytes, &dataPart); err != nil {
		t.Fatalf("unmarshal data part: %v", err)
	}
	if dataPart.Kind != "data" {
		t.Fatalf("unexpected part kind: %q", dataPart.Kind)
	}
	return dataPart.Data
}

func TestPublishArtifactSavedFromHook_NilPublisherIsNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("adapter panicked with nil publisher: %v", r)
		}
	}()
	publishArtifactSavedFromHook(t.Context(), nil, tool.ArtifactSavedInfo{Filename: "x"})
}

func TestPublishArtifactSavedFromHook_DisabledPublisherIsNoOp(t *testing.T) {
	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("adapter panicked with disabled publisher: %v", r)
		}
	}()
	publishArtifactSavedFromHook(t.Context(), sp, tool.ArtifactSavedInfo{Filename: "x"})
}

func TestFacadeSaveArtifact_EmitsBothEvents(t *testing.T) {
	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mb.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/agent/status/test-agent/task-xyz"
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: "task-xyz", ContextID: "ctx-xyz", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	q, err := mb.CreateQueue("facade-save-signals", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := mb.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}

	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}

	toolCtx := &tool.Context{
		ArtifactService: scopedSvc,
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
		ToolCallID:      "call-tool-99",
		NotifyArtifactSaved: func(innerCtx context.Context, info tool.ArtifactSavedInfo) {
			publishArtifactSavedFromHook(innerCtx, sp, info)
		},
	}
	facade := tool.NewFacade(toolCtx)

	content := []byte(`{"value":42}`)
	version, err := facade.SaveArtifact(t.Context(), "result.json", content, "application/json")
	if err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}
	if version != 0 {
		t.Errorf("first save version = %d, want 0", version)
	}

	progressData := receiveSignalDataPart(t, mb, q)
	var progress a2a.ArtifactCreationProgressData
	if err := json.Unmarshal(progressData, &progress); err != nil {
		t.Fatalf("unmarshal ArtifactCreationProgressData: %v", err)
	}
	if progress.Type != string(a2a.SignalArtifactCreationProgress) {
		t.Errorf("progress Type = %q", progress.Type)
	}
	if progress.Status != "completed" {
		t.Errorf("progress Status = %q", progress.Status)
	}
	if progress.Filename != "result.json" {
		t.Errorf("progress Filename = %q", progress.Filename)
	}
	if progress.BytesTransferred != len(content) {
		t.Errorf("progress BytesTransferred = %d, want %d", progress.BytesTransferred, len(content))
	}
	if progress.MIMEType == nil || *progress.MIMEType != "application/json" {
		t.Errorf("progress MIMEType = %v", progress.MIMEType)
	}
	if progress.Version == nil || *progress.Version != 0 {
		t.Errorf("progress Version = %v", progress.Version)
	}
	if progress.FunctionCallID != nil {
		t.Errorf("progress FunctionCallID = %v, want nil", *progress.FunctionCallID)
	}

	savedData := receiveSignalDataPart(t, mb, q)
	var saved a2a.ArtifactSavedData
	if err := json.Unmarshal(savedData, &saved); err != nil {
		t.Fatalf("unmarshal ArtifactSavedData: %v", err)
	}
	if saved.Type != string(a2a.SignalArtifactSaved) {
		t.Errorf("Type = %q", saved.Type)
	}
	if saved.Filename != "result.json" || saved.Version != 0 {
		t.Errorf("filename/version = %q/%d", saved.Filename, saved.Version)
	}
	if saved.MIMEType != "application/json" {
		t.Errorf("MIMEType = %q", saved.MIMEType)
	}
	if saved.SizeBytes != int64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", saved.SizeBytes, len(content))
	}
	if saved.FunctionCallID == nil || *saved.FunctionCallID != "call-tool-99" {
		t.Errorf("FunctionCallID = %v", saved.FunctionCallID)
	}
}
