// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newConnectedDevBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() {
		_ = brk.Disconnect(context.Background())
	})
	return brk
}

func TestToolSyncPublisher_ToolUploadedPublishesToToolsTopic(t *testing.T) {
	t.Parallel()
	brk := newConnectedDevBroker(t)
	pub := NewToolSyncPublisher(brk, "ns", slog.Default())

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if subErr := brk.Subscribe(q, a2a.SyncNotifySubscription("ns")); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	event := store.OutboxEvent{
		ID:          "evt-1",
		EventType:   "tool.uploaded",
		AggregateID: "my_pkg",
		Payload:     `{"agentIds":["a"],"userId":"u"}`,
	}
	if pubErr := pub.PublishOutboxEvent(context.Background(), event); pubErr != nil {
		t.Fatalf("Publish: %v", pubErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	wantTopic := a2a.SyncNotifyTopic("ns", "tools")
	if msg.Topic != wantTopic {
		t.Errorf("topic = %q, want %q", msg.Topic, wantTopic)
	}

	var got syncNotification
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.Category != "tools" {
		t.Errorf("Category = %q, want tools", got.Category)
	}
	if got.Reason != "tool.uploaded" {
		t.Errorf("Reason = %q, want tool.uploaded", got.Reason)
	}
	if got.Name != "my_pkg" {
		t.Errorf("Name = %q, want my_pkg", got.Name)
	}
}

func TestToolSyncPublisher_ToolConfigUpdatedPublishesToToolsTopic(t *testing.T) {
	t.Parallel()
	brk := newConnectedDevBroker(t)
	pub := NewToolSyncPublisher(brk, "ns", slog.Default())

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if subErr := brk.Subscribe(q, a2a.SyncNotifySubscription("ns")); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	event := store.OutboxEvent{
		ID:          "evt-config-1",
		EventType:   "tool.config.updated",
		AggregateID: "my_pkg",
		Payload:     `{"agentIds":["a1","a2"],"name":"my_pkg","userId":"u"}`,
	}
	if pubErr := pub.PublishOutboxEvent(context.Background(), event); pubErr != nil {
		t.Fatalf("Publish: %v", pubErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	wantTopic := a2a.SyncNotifyTopic("ns", "tools")
	if msg.Topic != wantTopic {
		t.Errorf("topic = %q, want %q", msg.Topic, wantTopic)
	}

	var got syncNotification
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.Category != "tools" {
		t.Errorf("Category = %q, want tools", got.Category)
	}
	if got.Reason != "tool.config.updated" {
		t.Errorf("Reason = %q, want tool.config.updated", got.Reason)
	}
	if got.Name != "my_pkg" {
		t.Errorf("Name = %q, want my_pkg", got.Name)
	}
}

func TestToolSyncPublisher_SkillConfigUpdatedPublishesToSkillsTopic(t *testing.T) {
	t.Parallel()
	brk := newConnectedDevBroker(t)
	pub := NewToolSyncPublisher(brk, "ns", slog.Default())

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if subErr := brk.Subscribe(q, a2a.SyncNotifySubscription("ns")); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	event := store.OutboxEvent{
		ID:          "evt-skill-config-1",
		EventType:   "skill.config.updated",
		AggregateID: "00000000-0000-0000-0000-000000000abc",
		Payload:     `{"name":"manim_skill","userId":"u"}`,
	}
	if pubErr := pub.PublishOutboxEvent(context.Background(), event); pubErr != nil {
		t.Fatalf("Publish: %v", pubErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	wantTopic := a2a.SyncNotifyTopic("ns", "skills")
	if msg.Topic != wantTopic {
		t.Errorf("topic = %q, want %q", msg.Topic, wantTopic)
	}

	var got syncNotification
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.Category != "skills" {
		t.Errorf("Category = %q, want skills", got.Category)
	}
	if got.Reason != "skill.config.updated" {
		t.Errorf("Reason = %q, want skill.config.updated", got.Reason)
	}
	if got.Name != "manim_skill" {
		t.Errorf("Name = %q, want manim_skill (taken from payload)", got.Name)
	}
}

func TestToolSyncPublisher_UnknownPrefixIsNoop(t *testing.T) {
	t.Parallel()
	brk := newConnectedDevBroker(t)
	pub := NewToolSyncPublisher(brk, "ns", slog.Default())

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.SyncNotifySubscription("ns")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	event := store.OutboxEvent{
		ID:          "evt-3",
		EventType:   "connector.updated",
		AggregateID: "c",
		Payload:     "{}",
	}
	if err := pub.PublishOutboxEvent(context.Background(), event); err != nil {
		t.Fatalf("Publish: unexpected error %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if msg, err := brk.Receive(ctx, q); err == nil {
		t.Errorf("expected no message for unknown prefix, got topic=%q", msg.Topic)
	}
}

func TestToolSyncPublisher_NilBrokerReturnsError(t *testing.T) {
	t.Parallel()
	pub := NewToolSyncPublisher(nil, "ns", slog.Default())
	err := pub.PublishOutboxEvent(context.Background(), store.OutboxEvent{
		EventType:   "tool.uploaded",
		AggregateID: "pkg",
	})
	if err == nil {
		t.Fatal("expected error for nil broker")
	}
}
