// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func assertCleanLLMHistory(req mockllm.RecordedRequest) error {
	seenIDs := make(map[string]struct{})
	pendingToolCallIDs := make(map[string]string)

	for i, m := range req.Messages {
		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) == 0 {
				if len(pendingToolCallIDs) > 0 {
					return fmt.Errorf(
						"orphan tool_call(s) at index %d: assistant carried unresolved tool_call_ids %s when a non-tool follow-up appeared",
						i, sortedIDs(pendingToolCallIDs),
					)
				}
				continue
			}
			if len(pendingToolCallIDs) > 0 {
				return fmt.Errorf(
					"orphan tool_call(s) at index %d: new assistant tool_calls began while %s from a prior assistant were still pending",
					i, sortedIDs(pendingToolCallIDs),
				)
			}
			for _, tc := range m.ToolCalls {
				if _, dup := seenIDs[tc.ID]; dup {
					return fmt.Errorf(
						"duplicate tool_use_id %q at message index %d (tool=%s) — repeat of a previously declared tool_call",
						tc.ID, i, tc.Name,
					)
				}
				seenIDs[tc.ID] = struct{}{}
				pendingToolCallIDs[tc.ID] = tc.Name
			}
		case "tool":
			if m.ToolCallID == "" {
				return fmt.Errorf("tool message at index %d has empty ToolCallID — cannot match to any tool_call", i)
			}
			if _, ok := pendingToolCallIDs[m.ToolCallID]; !ok {
				if _, declared := seenIDs[m.ToolCallID]; declared {
					return fmt.Errorf(
						"duplicate tool response at index %d for tool_call_id %q — its tool_call was already paired earlier",
						i, m.ToolCallID,
					)
				}
				return fmt.Errorf(
					"orphan tool response at index %d for tool_call_id %q — no preceding assistant tool_call announced this id",
					i, m.ToolCallID,
				)
			}
			delete(pendingToolCallIDs, m.ToolCallID)
		}
	}

	if len(pendingToolCallIDs) > 0 {
		return fmt.Errorf(
			"history ends with %d unresolved tool_call_id(s): %s",
			len(pendingToolCallIDs), sortedIDs(pendingToolCallIDs),
		)
	}
	return nil
}

func sortedIDs(m map[string]string) string {
	ids := make([]string, 0, len(m))
	for id, name := range m {
		ids = append(ids, fmt.Sprintf("%s(%s)", id, name))
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func finalTaskStateFromEvents(events []CollectedEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		if evt.Type != "response" && evt.Type != "status" {
			continue
		}
		var env struct {
			Result struct {
				Kind   string `json:"kind"`
				Status struct {
					State string `json:"state"`
				} `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(evt.Payload, &env); err != nil {
			continue
		}
		if env.Result.Kind == "task" && env.Result.Status.State != "" {
			return env.Result.Status.State
		}
	}
	return ""
}
