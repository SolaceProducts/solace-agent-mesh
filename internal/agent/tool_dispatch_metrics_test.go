// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type recordingToolMetrics struct {
	histograms []toolMetricCall
}

type toolMetricCall struct {
	name     string
	duration time.Duration
	labels   []string
}

func (m *recordingToolMetrics) Counter(string, int64, ...string) {}
func (m *recordingToolMetrics) Gauge(string, float64, ...string) {}
func (m *recordingToolMetrics) Histogram(name string, _ time.Duration, labels ...string) {
	m.histograms = append(m.histograms, toolMetricCall{name: name, labels: labels})
}

var _ runtime.Metrics = (*recordingToolMetrics)(nil)

func (m *recordingToolMetrics) find(name string) *toolMetricCall {
	for i := range m.histograms {
		if m.histograms[i].name == name {
			return &m.histograms[i]
		}
	}
	return nil
}

func assertToolLabel(t *testing.T, labels []string, key, want string) {
	t.Helper()
	for i := 0; i+1 < len(labels); i += 2 {
		if labels[i] == key {
			if labels[i+1] != want {
				t.Errorf("label %q = %q, want %q", key, labels[i+1], want)
			}
			return
		}
	}
	t.Errorf("label %q not found in %v", key, labels)
}

func TestDispatchToolCallsWithStatus_PausedToolRecordsPausedLabel(t *testing.T) {
	h := newToolStatusHarness(t, "task-paused-metric-1")
	rec := &recordingToolMetrics{}
	h.agent.svc.(*mockServices).metrics = rec

	h.registerTool(t, "longrunner", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.Pending("waiting for sub-task"), nil
	})

	h.dispatch(t.Context(), []llm.ToolCall{
		{ID: "call-P", Name: "longrunner", Arguments: `{}`},
	}, "")

	got := rec.find("operation.duration")
	if got == nil {
		t.Fatalf("operation.duration not recorded for a paused tool; histograms=%+v", rec.histograms)
	}
	assertToolLabel(t, got.labels, "type", "tool")
	assertToolLabel(t, got.labels, "component.name", "longrunner")
	assertToolLabel(t, got.labels, "operation.name", "execute")
	assertToolLabel(t, got.labels, "error.type", "paused")
}

func TestDispatchToolCallsWithStatus_CompletedToolNotPaused(t *testing.T) {
	h := newToolStatusHarness(t, "task-done-metric-1")
	rec := &recordingToolMetrics{}
	h.agent.svc.(*mockServices).metrics = rec

	h.registerTool(t, "echo", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("done"), nil
	})

	h.dispatch(t.Context(), []llm.ToolCall{
		{ID: "call-D", Name: "echo", Arguments: `{}`},
	}, "")

	got := rec.find("operation.duration")
	if got == nil {
		t.Fatalf("operation.duration not recorded; histograms=%+v", rec.histograms)
	}
	assertToolLabel(t, got.labels, "error.type", "")
}
