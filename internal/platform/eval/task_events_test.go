// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import "testing"

func TestBuildVisualizationEventsMapsEveryMessage(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		{
			"topic":     "ns/a2a/v1/gateway/response/evals/task-x",
			"wall_time": "2026-05-27T00:00:00.000Z",
			"payload": makeStatusMessage(map[string]any{
				"type":    "llm_invocation",
				"request": map[string]any{"model": "gpt-4"},
			}),
		},
		{
			"topic":     "ns/a2a/v1/gateway/response/evals/task-x",
			"wall_time": "2026-05-27T00:00:01.000Z",
			"payload":   makeStatusMessage(map[string]any{"type": "llm_response"}),
		},
	}
	events := buildVisualizationEvents(messages)
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2 (no type filtering); got=%+v", len(events), events)
	}
	for i, e := range events {
		if e["event_type"] != "a2a_message" {
			t.Errorf("event[%d].event_type = %v, want a2a_message", i, e["event_type"])
		}
		if _, ok := e["full_payload"].(map[string]any); !ok {
			t.Errorf("event[%d] missing full_payload (FE visualizer reads it)", i)
		}
		if e["solace_topic"] != "ns/a2a/v1/gateway/response/evals/task-x" {
			t.Errorf("event[%d].solace_topic = %v", i, e["solace_topic"])
		}
	}
	if events[0]["timestamp"] != "2026-05-27T00:00:00.000Z" {
		t.Errorf("event[0].timestamp = %v, want captured wall_time", events[0]["timestamp"])
	}
	if events[0]["direction"] != "response" {
		t.Errorf("event[0].direction = %v, want response", events[0]["direction"])
	}
}

func TestBuildVisualizationEventsReturnsNilForEmpty(t *testing.T) {
	t.Parallel()
	if got := buildVisualizationEvents(nil); got != nil {
		t.Fatalf("nil messages: got %v, want nil", got)
	}
	nonDict := []map[string]any{{"payload": "not-a-dict"}}
	if got := buildVisualizationEvents(nonDict); got != nil {
		t.Fatalf("non-dict payload: got %v, want nil", got)
	}
}
