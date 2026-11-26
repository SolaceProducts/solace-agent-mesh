// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func statusUpdateFixture(taskID, agentName string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"result": map[string]any{
			"kind":   "status-update",
			"taskId": taskID,
			"status": map[string]any{
				"message": map[string]any{
					"metadata": map[string]any{"agent_name": agentName},
				},
			},
		},
	}
}

func expectNoFrame(t *testing.T, ch <-chan *SSEEvent) {
	t.Helper()
	select {
	case evt := <-ch:
		t.Fatalf("expected no activity_event frame, got %q", evt.EventType)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestOnActivityEvent_EmitsEnrichedFrame(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, idgen.New(), "user")
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	topic := "test/a2a/v1/agent/status/AgentA/" + taskID
	adapter.OnActivityEvent(t.Context(), taskID, topic, statusUpdateFixture(taskID, "AgentA"))

	select {
	case evt := <-ch:
		if evt.EventType != "activity_event" {
			t.Fatalf("EventType = %q, want activity_event", evt.EventType)
		}
		if evt.Final {
			t.Error("activity_event must not be Final")
		}
		if evt.Sequence != 0 {
			t.Errorf("Sequence = %d, want 0 (live-only, not durably logged)", evt.Sequence)
		}
		var enriched map[string]any
		if err := json.Unmarshal(evt.Data, &enriched); err != nil {
			t.Fatalf("unmarshal enriched: %v", err)
		}
		if enriched["event_type"] != "a2a_message" {
			t.Errorf("event_type = %v, want a2a_message", enriched["event_type"])
		}
		if enriched["task_id"] != taskID {
			t.Errorf("task_id = %v, want %s", enriched["task_id"], taskID)
		}
		if enriched["source_entity"] != "AgentA" {
			t.Errorf("source_entity = %v, want AgentA", enriched["source_entity"])
		}
		if _, ok := enriched["full_payload"]; !ok {
			t.Error("missing full_payload")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for activity_event frame")
	}
}

func TestOnActivityEvent_EmitsLLMInvocation(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, idgen.New(), "user")
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	llmInvocation := map[string]any{
		"result": map[string]any{
			"kind":   "status-update",
			"taskId": taskID,
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"metadata": map[string]any{"agent_name": "AgentA"},
					"parts": []any{map[string]any{
						"kind": "data",
						"data": map[string]any{"type": "llm_invocation", "request": map[string]any{"model": "openai/e2e-mock"}},
					}},
				},
			},
		},
	}
	adapter.OnActivityEvent(t.Context(), taskID, "test/a2a/v1/agent/status/A/"+taskID, llmInvocation)

	select {
	case evt := <-ch:
		if evt.EventType != "activity_event" {
			t.Fatalf("EventType = %q, want activity_event", evt.EventType)
		}
	case <-time.After(time.Second):
		t.Fatal("llm_invocation was dropped from the activity_event stream — the chat activity panel cannot render the LLM Call/model step")
	}
}

func TestOnActivityEvent_FiltersWorkingState(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, idgen.New(), "user")
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	working := map[string]any{
		"result": map[string]any{
			"status": map[string]any{
				"state": "working",
				"message": map[string]any{
					"parts": []any{map[string]any{"kind": "text", "text": "thinking..."}},
				},
			},
		},
	}
	adapter.OnActivityEvent(t.Context(), taskID, "test/a2a/v1/agent/status/A/"+taskID, working)
	expectNoFrame(t, ch)
}

func TestOnActivityEvent_FiltersVisualizationExcluded(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, idgen.New(), "user")
	ch, _, _ := adapter.sseManager.Subscribe(taskID)

	excluded := map[string]any{
		"method": "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"metadata": map[string]any{"visualization": false},
			},
		},
	}
	adapter.OnActivityEvent(t.Context(), taskID, "test/a2a/v1/agent/request/A/"+taskID, excluded)
	expectNoFrame(t, ch)
}

func TestOnActivityEvent_SkipsUnregisteredTask(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()

	adapter.OnActivityEvent(t.Context(), taskID,
		"test/a2a/v1/agent/status/AgentA/"+taskID, statusUpdateFixture(taskID, "AgentA"))

	if adapter.sseManager.HasTask(taskID) {
		t.Fatal("OnActivityEvent must skip (not auto-register) a task with no SSE entry")
	}
}
