// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRepairOrphanUserMessagesMarksCancelledRequestAsAbandoned(t *testing.T) {
	logger := silentLogger()
	cancelMarker := "[The previous request was cancelled or interrupted before completion. Do NOT re-execute it — proceed with the current user request.]"

	tests := []struct {
		name        string
		messages    []llm.ChatMessage
		historyEnd  int
		wantInsert  int
		wantPattern []string
	}{
		{
			name: "balanced history needs no repair",
			messages: []llm.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "q1"},
				{Role: "assistant", Content: "a1"},
				{Role: "user", Content: "q2-current"},
			},
			historyEnd:  3,
			wantInsert:  0,
			wantPattern: []string{"system", "user", "assistant", "user"},
		},
		{
			name: "single orphan at end of history (cancel-then-followup)",
			messages: []llm.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "write a poem"},
				{Role: "user", Content: "what did I ask"},
			},
			historyEnd:  2,
			wantInsert:  1,
			wantPattern: []string{"system", "user", "assistant", "user"},
		},
		{
			name: "two consecutive orphans (user cancelled twice)",
			messages: []llm.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "write a poem"},
				{Role: "user", Content: "save it as MD"},
				{Role: "user", Content: "what did I ask"},
			},
			historyEnd:  3,
			wantInsert:  2,
			wantPattern: []string{"system", "user", "assistant", "user", "assistant", "user"},
		},
		{
			name: "orphan followed by completed turn",
			messages: []llm.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "cancelled"},
				{Role: "user", Content: "succeeded"},
				{Role: "assistant", Content: "answer"},
				{Role: "user", Content: "current"},
			},
			historyEnd:  4,
			wantInsert:  1,
			wantPattern: []string{"system", "user", "assistant", "user", "assistant", "user"},
		},
		{
			name:        "empty history (no system message even)",
			messages:    []llm.ChatMessage{},
			historyEnd:  0,
			wantInsert:  0,
			wantPattern: []string{},
		},
		{
			name: "system-only history (first turn)",
			messages: []llm.ChatMessage{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "first ever question"},
			},
			historyEnd:  1,
			wantInsert:  0,
			wantPattern: []string{"system", "user"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repaired, newEnd := repairOrphanUserMessages(tc.messages, tc.historyEnd, logger)

			expectedLen := len(tc.messages) + tc.wantInsert
			if len(repaired) != expectedLen {
				t.Fatalf("repaired len = %d, want %d (original=%d + inserted=%d)",
					len(repaired), expectedLen, len(tc.messages), tc.wantInsert)
			}
			if newEnd != tc.historyEnd+tc.wantInsert {
				t.Errorf("new historyEnd = %d, want %d", newEnd, tc.historyEnd+tc.wantInsert)
			}
			roles := make([]string, len(repaired))
			for i, m := range repaired {
				roles[i] = m.Role
			}
			if len(roles) != len(tc.wantPattern) {
				t.Fatalf("role pattern len = %d, want %d (got=%v)", len(roles), len(tc.wantPattern), roles)
			}
			for i := range roles {
				if roles[i] != tc.wantPattern[i] {
					t.Errorf("role[%d] = %q, want %q (full=%v)", i, roles[i], tc.wantPattern[i], roles)
				}
			}

			for i, m := range repaired {
				if i >= newEnd {
					break
				}
				if m.Role == "assistant" && m.Content == cancelMarker {
					continue
				}
			}
		})
	}
}

func TestRepairOrphanUserMessages_DoesNotTouchCurrentTurn(t *testing.T) {
	logger := silentLogger()
	messages := []llm.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "old orphan"},
		{Role: "user", Content: "current — must not get a synthetic marker after it"},
	}
	historyEnd := 2

	repaired, newEnd := repairOrphanUserMessages(messages, historyEnd, logger)

	if newEnd >= len(repaired) {
		t.Fatalf("newEnd %d >= len(repaired) %d — current turn lost", newEnd, len(repaired))
	}
	current := repaired[newEnd]
	if current.Role != "user" || current.Content != "current — must not get a synthetic marker after it" {
		t.Errorf("current-turn msg corrupted: %+v", current)
	}
}
