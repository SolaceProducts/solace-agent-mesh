// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

type streamingDownstream struct {
	events []proxy.ResponseEvent
}

func (d *streamingDownstream) FetchAgentCard(context.Context) (*a2a.AgentCard, error) {
	return &a2a.AgentCard{Name: "ProxiedAgent"}, nil
}

func (d *streamingDownstream) ForwardRequest(context.Context, *proxy.TaskContext, *a2a.Request) (<-chan proxy.ResponseEvent, error) {
	ch := make(chan proxy.ResponseEvent, len(d.events))
	for _, ev := range d.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (d *streamingDownstream) CancelTask(context.Context, *proxy.TaskContext) error { return nil }
func (d *streamingDownstream) Close() error                                         { return nil }

func TestProxiedAgentStreamingReachesGatewayAdapter(t *testing.T) {
	core, _, adapter := setupTestGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(context.Background()) }()

	downstream := &streamingDownstream{events: []proxy.ResponseEvent{
		{StatusUpdate: &a2a.TaskStatusUpdateEvent{
			TaskID: "downstream-1",
			Kind:   "status-update",
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateWorking,
				Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{a2a.NewTextPart("step 1 of 2")}},
			},
		}},
		{ArtifactUpdate: &a2a.TaskArtifactUpdateEvent{
			TaskID: "downstream-1",
			Kind:   "artifact-update",
			Artifact: a2a.Artifact{
				ArtifactID: "report",
				Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
					Name:     "report.pdf",
					MIMEType: "application/pdf",
					URI:      "https://downstream.example/report.pdf",
				}}},
			},
		}},
		{Task: &a2a.Task{
			ID:     "downstream-1",
			Kind:   "task",
			Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}},
	}}

	inst := proxy.NewInstanceFromConfig(&proxy.ProxyConfig{Name: "ProxiedAgent", Namespace: "test-ns"})
	inst.SetAdapter(downstream)
	if err := inst.Init(ctx, nil, core.Services()); err != nil {
		t.Fatalf("proxy Init: %v", err)
	}
	if err := inst.Start(ctx); err != nil {
		t.Fatalf("proxy Start: %v", err)
	}
	defer func() { _ = inst.Stop(context.Background()) }()

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("go")},
		ContextID: "session-1",
	}
	if err := core.SubmitTask(ctx, "ProxiedAgent", "task-proxy-1", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	for {
		adapter.mu.Lock()
		done := len(adapter.finalResponses) > 0
		adapter.mu.Unlock()
		if done {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for the proxied task's final response")
		case <-time.After(10 * time.Millisecond):
		}
	}

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.statusUpdates) != 1 {
		t.Fatalf("OnStatusUpdate calls = %d, want 1 (streamed status update dropped)", len(adapter.statusUpdates))
	}
	su := adapter.statusUpdates[0]
	if su.TaskID != "task-proxy-1" || su.Status.Message == nil || su.Status.Message.Parts[0].Text != "step 1 of 2" {
		t.Errorf("status update = %+v, want the streamed text for task-proxy-1", su)
	}
	if len(adapter.artifactUpdates) != 1 {
		t.Fatalf("OnArtifactUpdate calls = %d, want 1 (streamed artifact update dropped)", len(adapter.artifactUpdates))
	}
	if got := adapter.artifactUpdates[0].Artifact.ArtifactID; got != "report" {
		t.Errorf("artifact update ID = %q, want %q", got, "report")
	}
}
