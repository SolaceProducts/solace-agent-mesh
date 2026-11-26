// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestPublishBuilderComponentProgress_PublishesSignal(t *testing.T) {
	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mb.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-bcp"
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: "task-bcp", ContextID: "ctx-bcp", AgentName: "builder", ClientID: "client-1", UserID: "user-1"}, slog.Default())

	q, err := mb.CreateQueue("bcp-signals", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := mb.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}

	percent := 42
	sp.PublishBuilderComponentProgress(t.Context(),
		"comp-1", "agent", "Web Search", "in_progress", "Generating config", &percent,
	)

	raw := receiveSignalDataPart(t, mb, q)
	var got a2a.BuilderComponentProgressData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal BuilderComponentProgressData: %v", err)
	}
	if got.Type != string(a2a.SignalBuilderComponentProgress) {
		t.Errorf("Type = %q", got.Type)
	}
	if got.ComponentID != "comp-1" || got.ComponentType != "agent" || got.ComponentName != "Web Search" {
		t.Errorf("identity fields = %+v", got)
	}
	if got.State != "in_progress" || got.StatusText != "Generating config" {
		t.Errorf("state/status_text = %q/%q", got.State, got.StatusText)
	}
	if got.ProgressPercent == nil || *got.ProgressPercent != 42 {
		t.Errorf("ProgressPercent = %v", got.ProgressPercent)
	}
}

func TestPublishBuilderComponentProgress_DisabledIsNoOp(t *testing.T) {
	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "builder", ClientID: "client-1", UserID: "user-1"}, slog.Default())
	sp.PublishBuilderComponentProgress(t.Context(), "comp-1", "agent", "n", "pending", "", nil)
}
