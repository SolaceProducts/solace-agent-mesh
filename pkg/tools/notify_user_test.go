// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestNotifyUserTool_Execute_PublishesRequest(t *testing.T) {
	var captured a2a.NotifyRequest
	published := false
	toolCtx := &tool.Context{
		UserID:       "trent@example.org",
		AgentName:    "myagent",
		ParentTaskID: "task-1",
		TraceID:      "trace-1",
		SessionID:    "sess-1",
		PublishNotification: func(_ context.Context, req a2a.NotifyRequest) error {
			captured = req
			published = true
			return nil
		},
	}

	tl := &NotifyUserTool{}
	res, err := tl.Execute(t.Context(), map[string]any{
		"message":  "Your report is ready.",
		"title":    "Report ready",
		"channels": []any{"webhook"},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("expected success status, got %q (%s)", res.Status, res.Message)
	}
	if !published {
		t.Fatal("expected PublishNotification to be called")
	}

	if captured.RequestID == "" {
		t.Error("expected a non-empty RequestID")
	}
	if captured.RecipientUserID != "trent@example.org" {
		t.Errorf("recipient: got %q, want the current user", captured.RecipientUserID)
	}
	if captured.Mode != a2a.NotifyModeNotify {
		t.Errorf("mode: got %q, want %q", captured.Mode, a2a.NotifyModeNotify)
	}
	if captured.Message != "Your report is ready." {
		t.Errorf("message: got %q", captured.Message)
	}
	if captured.Title != "Report ready" {
		t.Errorf("title: got %q", captured.Title)
	}
	if len(captured.Channels) != 1 || captured.Channels[0] != "webhook" {
		t.Errorf("channels: got %v, want [webhook]", captured.Channels)
	}
	if captured.SenderUserID != "trent@example.org" || captured.SenderAgent != "myagent" {
		t.Errorf("sender fields not populated: %+v", captured)
	}
	if captured.Metadata["task_id"] != "task-1" || captured.Metadata["trace_id"] != "trace-1" {
		t.Errorf("metadata not populated: %v", captured.Metadata)
	}
}

func TestNotifyUserTool_Execute_RecipientOverride(t *testing.T) {
	var captured a2a.NotifyRequest
	toolCtx := &tool.Context{
		UserID:              "trent@example.org",
		PublishNotification: func(_ context.Context, req a2a.NotifyRequest) error { captured = req; return nil },
	}
	tl := &NotifyUserTool{}
	if _, err := tl.Execute(t.Context(), map[string]any{
		"message":           "heads up",
		"recipient_user_id": "other@example.org",
	}, toolCtx); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if captured.RecipientUserID != "other@example.org" {
		t.Errorf("recipient override: got %q", captured.RecipientUserID)
	}
}

func TestNotifyUserTool_Execute_MissingMessage(t *testing.T) {
	called := false
	toolCtx := &tool.Context{
		UserID:              "trent@example.org",
		PublishNotification: func(_ context.Context, _ a2a.NotifyRequest) error { called = true; return nil },
	}
	tl := &NotifyUserTool{}
	res, err := tl.Execute(t.Context(), map[string]any{"title": "no body"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Status != "error" {
		t.Errorf("expected error status for missing message, got %q", res.Status)
	}
	if called {
		t.Error("PublishNotification should not be called when message is missing")
	}
}

func TestNotifyUserTool_Metadata(t *testing.T) {
	tl := &NotifyUserTool{}
	if tl.Name() != "notify_user" {
		t.Errorf("Name = %q", tl.Name())
	}
	if tl.Description() == "" {
		t.Error("Description is empty")
	}
	if got := tl.RequiredScopes(); len(got) != 1 || got[0] != "notify:_:send" {
		t.Errorf("RequiredScopes = %v, want [notify:_:send]", got)
	}
	p := tl.Parameters()
	if p == nil || p.Properties["message"] == nil {
		t.Fatalf("Parameters missing message: %+v", p)
	}
	if len(p.Required) != 1 || p.Required[0] != "message" {
		t.Errorf("Required = %v, want [message]", p.Required)
	}
}

func TestNotifyUserTool_Execute_PublishError(t *testing.T) {
	toolCtx := &tool.Context{
		UserID: "trent@example.org",
		PublishNotification: func(_ context.Context, _ a2a.NotifyRequest) error {
			return errors.New("broker down")
		},
	}
	tl := &NotifyUserTool{}
	res, err := tl.Execute(t.Context(), map[string]any{"message": "hi"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Status != "error" {
		t.Errorf("expected error status when publish fails, got %q (%s)", res.Status, res.Message)
	}
}

func TestNotifyUserTool_Execute_NoRecipient(t *testing.T) {
	called := false
	toolCtx := &tool.Context{
		PublishNotification: func(_ context.Context, _ a2a.NotifyRequest) error { called = true; return nil },
	}
	res, err := (&NotifyUserTool{}).Execute(t.Context(), map[string]any{"message": "hi"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != "error" {
		t.Errorf("expected error when no recipient can be determined, got %q", res.Status)
	}
	if called {
		t.Error("should not publish without a recipient")
	}
}

func TestNotifyUserTool_Execute_NoPublisher(t *testing.T) {
	toolCtx := &tool.Context{UserID: "trent@example.org"}
	tl := &NotifyUserTool{}
	res, err := tl.Execute(t.Context(), map[string]any{"message": "hi"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Status != "error" {
		t.Errorf("expected error status when no publisher is wired, got %q", res.Status)
	}
}
