// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const firstProducedSheet = "quarterly-forecast-sheet-0000.csv"

func oversizedPeerReplyProducing(fileCount int) *tool.Result {
	produced := make([]any, fileCount)
	for i := range produced {
		produced[i] = map[string]any{"filename": fmt.Sprintf("quarterly-forecast-sheet-%04d.csv", i), "version": 1}
	}
	text := strings.Repeat("reply text ", 30_000)
	task := map[string]any{
		"id": "task-1", "kind": "task", "contextId": "ctx-1",
		"status": map[string]any{"state": "completed", "message": map[string]any{
			"role": "agent", "parts": []any{map[string]any{"kind": "text", "text": text}}}},
		"metadata": map[string]any{"produced_artifacts": produced},
	}
	return tool.OK(text, tool.WithDelegatedTask(task))
}

func TestOversizedDelegatedResultWarnsWhenItsFileListDoesNotFit(t *testing.T) {
	tests := map[string]struct {
		fileCount int
		wantKept  bool
		wantWarn  bool
	}{
		"a file list small enough to stay in the result": {3, true, false},
		"a file list too large to stay in the result":    {400, false, true},
	}
	for label, tc := range tests {
		t.Run(label, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			var logs bytes.Buffer
			agent.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			toolCtx, _ := ceilingTestContext()

			out, _ := agent.formatToolResult(context.Background(), oversizedPeerReplyProducing(tc.fileCount), toolCtx, nil)

			if kept := strings.Contains(out, firstProducedSheet); kept != tc.wantKept {
				t.Fatalf("file list kept in the result = %v, want %v: fixture drift", kept, tc.wantKept)
			}
			warned := strings.Contains(logs.String(), "level=WARN") && strings.Contains(logs.String(), "produced_artifacts")
			if warned != tc.wantWarn {
				t.Fatalf("warned = %v, want %v; logs: %s", warned, tc.wantWarn, logs.String())
			}
		})
	}
}
