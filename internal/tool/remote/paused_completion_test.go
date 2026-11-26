// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const pausedCompletionPanicLog = "a paused remote tool call's completion panicked"

type lockedLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLogs) records(msg string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var matching []map[string]any
	for line := range strings.SplitSeq(l.buf.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == msg {
			matching = append(matching, record)
		}
	}
	return matching
}

func TestPausedCallWhosePostProcessingPanicsDeliversAnErrorResultAndLogsIt(t *testing.T) {
	t.Parallel()
	logs := &lockedLogs{}
	exe, brk := newTestExecutor(t, WithLogger(slog.New(slog.NewJSONHandler(logs, nil))))
	startExecutor(t, exe)
	simulateSTR(t, brk, "artifact_tool", func(corrID, replyTo, _ string) {
		payload, _ := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{
			ToolResult:       map[string]any{"status": "success"},
			CreatedArtifacts: []remotetool.CreatedArtifact{{Filename: "report.pdf", MIMEType: "application/pdf", SizeBytes: 2048}},
		}))
		if err := brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload}); err != nil {
			t.Errorf("publish reply: %v", err)
		}
	})
	rt := NewTool(&ToolConfig{ToolName: "artifact_tool", TimeoutSeconds: 5}, exe)
	toolCtx := pausingToolCtx()
	toolCtx.TraceID, toolCtx.ParentTaskID = "trace-panic", "task-panic"
	toolCtx.NotifyArtifactSaved = func(context.Context, tool.ArtifactSavedInfo) { panic("artifact sink exploded") }

	pending, err := rt.Execute(t.Context(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := deliveredResult(t, pending, 5*time.Second)

	if got.Status != tool.StatusError {
		t.Errorf("delivered status = %s %q, want an error result so the task does not wait forever", got.Status, got.Message)
	}
	corrID, _ := pending.Data["sub_task_id"].(string)
	records := logs.records(pausedCompletionPanicLog)
	if len(records) != 1 {
		t.Fatalf("panic records = %v, want exactly one", records)
	}
	record := records[0]
	if record["level"] != "ERROR" || record["traceID"] != "trace-panic" || record["taskID"] != "task-panic" || record["corrID"] != corrID || record["tool"] != "artifact_tool" {
		t.Errorf("record = %v, want ERROR carrying traceID, taskID, corrID %q and tool", record, corrID)
	}
	if panicked, _ := record["panic"].(string); !strings.Contains(panicked, "artifact sink exploded") {
		t.Errorf("record panic = %v, want the panic value", record["panic"])
	}
}

func TestCompletePausedCallAlwaysEndsWithAnErrorResultWhenAPanicStrandsTheDelivery(t *testing.T) {
	tests := []struct {
		name                string
		finishPanics        bool
		deliveriesThatPanic int
		wantDelivered       []string
	}{
		{
			name:          "post-processing panics before any delivery",
			finishPanics:  true,
			wantDelivered: []string{tool.StatusError},
		},
		{
			name:                "the delivery callback panics once",
			deliveriesThatPanic: 1,
			wantDelivered:       []string{tool.StatusSuccess, tool.StatusError},
		},
		{
			name:                "the delivery callback panics every time",
			deliveriesThatPanic: 2,
			wantDelivered:       []string{tool.StatusSuccess, tool.StatusError},
		},
		{
			name:                "post-processing panics and so does the recovery delivery",
			finishPanics:        true,
			deliveriesThatPanic: 1,
			wantDelivered:       []string{tool.StatusError},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := &lockedLogs{}
			exe, _ := newTestExecutor(t, WithLogger(slog.New(slog.NewJSONHandler(logs, nil))))
			rt := NewTool(&ToolConfig{ToolName: "report_tool"}, exe)
			var delivered []string
			deliver := func(r *tool.Result) {
				delivered = append(delivered, r.Status)
				if len(delivered) <= tt.deliveriesThatPanic {
					panic("delivery sink exploded")
				}
			}
			finish := func() *tool.Result {
				if tt.finishPanics {
					panic("post-processing exploded")
				}
				return &tool.Result{Status: tool.StatusSuccess}
			}

			escaped := func() (escaped any) {
				defer func() { escaped = recover() }()
				rt.completePausedCall(&tool.Context{}, &Call{corrID: "corr-panic"}, deliver, finish)
				return nil
			}()

			if escaped != nil {
				t.Errorf("a panic escaped completePausedCall and would crash the process: %v", escaped)
			}
			if diff := cmp.Diff(tt.wantDelivered, delivered); diff != "" {
				t.Errorf("delivered statuses mismatch (-want +got):\n%s", diff)
			}
			panics := logs.records(pausedCompletionPanicLog)
			if len(panics) != 1 {
				t.Fatalf("completion panic records = %v, want exactly one", panics)
			}
			if stack, _ := panics[0]["stack"].(string); stack == "" {
				t.Errorf("panic record %v carries no stack", panics[0])
			}
		})
	}
}
