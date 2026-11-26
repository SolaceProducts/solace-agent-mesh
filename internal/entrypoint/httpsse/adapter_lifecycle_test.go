// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestAdapterOnStatusUpdate(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	adapter.sseManager.RegisterTask("task-1", "", "")
	ch, _, _ := adapter.sseManager.Subscribe("task-1")

	raw := []byte(`{"status":"working"}`)
	adapter.OnStatusUpdate(context.Background(), "task-1", &a2a.TaskStatusUpdateEvent{}, raw)

	select {
	case evt := <-ch:
		if evt.EventType != "status_update" {
			t.Errorf("expected event type 'status_update', got %q", evt.EventType)
		}
		if string(evt.Data) != string(raw) {
			t.Errorf("expected data %q, got %q", raw, evt.Data)
		}
	default:
		t.Error("expected SSE event but channel was empty")
	}
}

func TestAdapterOnArtifactUpdate(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	adapter.sseManager.RegisterTask("task-2", "", "")
	ch, _, _ := adapter.sseManager.Subscribe("task-2")

	raw := []byte(`{"artifact":"data"}`)
	adapter.OnArtifactUpdate(context.Background(), "task-2", &a2a.TaskArtifactUpdateEvent{}, raw)

	select {
	case evt := <-ch:
		if evt.EventType != "artifact_update" {
			t.Errorf("expected event type 'artifact_update', got %q", evt.EventType)
		}
	default:
		t.Error("expected SSE event but channel was empty")
	}
}

func TestAdapterOnFinalResponse(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	adapter.sseManager.RegisterTask("task-3", "", "")
	ch, _, _ := adapter.sseManager.Subscribe("task-3")

	raw := []byte(`{"result":"done"}`)
	adapter.OnFinalResponse(context.Background(), "task-3", &a2a.Task{}, raw)

	select {
	case evt := <-ch:
		if evt.EventType != "final_response" {
			t.Errorf("expected event type 'final_response', got %q", evt.EventType)
		}
		if !evt.Final {
			t.Error("expected Final=true")
		}
	default:
		t.Error("expected SSE event but channel was empty")
	}
}

func TestAdapterOnError(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	adapter.sseManager.RegisterTask("task-4", "", "")
	ch, _, _ := adapter.sseManager.Subscribe("task-4")

	raw := []byte(`{"jsonrpc":"2.0","id":"task-4","error":{"code":-32603,"message":"model alias \"planning\" unbound"}}`)
	adapter.OnError(context.Background(), "task-4", &a2a.Error{}, raw)

	select {
	case evt := <-ch:
		if evt.EventType != "final_response" {
			t.Errorf("expected event type 'final_response' (matching Python SAM), got %q", evt.EventType)
		}
		if !evt.Final {
			t.Error("expected Final=true")
		}
		if string(evt.Data) != string(raw) {
			t.Errorf("expected data to pass through unchanged, got %q", evt.Data)
		}
	default:
		t.Error("expected SSE event but channel was empty")
	}
}

func TestAdapterOnAgentDiscovered(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	adapter.OnAgentDiscovered(&a2a.AgentCard{
		Name:   "test-agent",
		Skills: []a2a.AgentSkill{{ID: idgen.New()}},
	})
}

func TestAdapterOnAgentRemoved(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	adapter.OnAgentRemoved("test-agent")
}
