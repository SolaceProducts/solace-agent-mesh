// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type fakeControlQuerier struct {
	app, method, path string
	body              json.RawMessage
	resp              json.RawMessage
	err               error
}

func (f *fakeControlQuerier) Custom(_ context.Context, app, method, path string, body json.RawMessage) (json.RawMessage, error) {
	f.app, f.method, f.path, f.body = app, method, path, body
	return f.resp, f.err
}

func TestSentinelAnalyticsTool_RoutesToGatewayControl(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{"totalTasks":7,"successRate":0.5}`)}
	toolCtx := &tool.Context{ControlQuerier: f}

	tl := NewQueryTaskHealthTool()

	res, err := tl.Execute(context.Background(), map[string]any{"window": "24h"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %v, want success (message: %s)", res.Status, res.Message)
	}
	if f.app != "gateway" || f.method != "get" || f.path != "analytics/tasks/summary" {
		t.Errorf("routed to app=%q method=%q path=%q, want gateway/get/analytics/tasks/summary", f.app, f.method, f.path)
	}

	var body map[string]any
	if err := json.Unmarshal(f.body, &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if _, ok := body["startDate"]; !ok {
		t.Errorf("body missing startDate: %s", f.body)
	}
	if _, ok := body["endDate"]; !ok {
		t.Errorf("body missing endDate: %s", f.body)
	}
	if res.Data != nil {
		t.Errorf("expected no inline Data (report is a data object), got %v", res.Data)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(res.DataObjects))
	}
	obj := res.DataObjects[0]
	if obj.Disposition != tool.DispositionAuto {
		t.Errorf("disposition = %q, want %q", obj.Disposition, tool.DispositionAuto)
	}
	if obj.Name != "" {
		t.Errorf("name = %q, want none: a read_only tool lets the runtime pick the artifact's name", obj.Name)
	}
	if obj.MIMEType != "application/json" {
		t.Errorf("mimeType = %q, want application/json", obj.MIMEType)
	}
	if !bytes.Equal(obj.Content, []byte(f.resp)) {
		t.Errorf("data object content = %q, want verbatim server response %q", obj.Content, f.resp)
	}
	var parsed map[string]any
	if err := json.Unmarshal(obj.Content, &parsed); err != nil {
		t.Fatalf("data object content not JSON: %v", err)
	}
	if parsed["totalTasks"] != float64(7) {
		t.Errorf("data object totalTasks = %v, want 7", parsed["totalTasks"])
	}
}

func TestSentinelAnalyticsTool_PaginationAndGroupBy(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{"data":[],"meta":{"pagination":{"pageNumber":2}}}`)}
	toolCtx := &tool.Context{ControlQuerier: f}

	tl := NewQueryTokenUsageTool()
	_, err := tl.Execute(context.Background(), map[string]any{
		"window": "7d", "page_number": float64(2), "page_size": float64(50), "group_by": "user",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal(f.body, &body)
	if body["pageNumber"] != float64(2) || body["pageSize"] != float64(50) {
		t.Errorf("pagination not forwarded: %s", f.body)
	}
	if body["groupBy"] != "user" {
		t.Errorf("groupBy = %v, want user", body["groupBy"])
	}
}

func TestSentinelAnalyticsTool_NoQuerier(t *testing.T) {
	tl := NewQueryFailedTasksTool()
	res, err := tl.Execute(context.Background(), map[string]any{}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("status = %v, want error when no control querier", res.Status)
	}
}

func TestSentinelAnalyticsTool_BadGroupBy(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{}`)}
	tl := NewQueryTokenUsageTool()
	res, err := tl.Execute(context.Background(), map[string]any{"group_by": "bogus"}, &tool.Context{ControlQuerier: f})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("status = %v, want error for invalid group_by", res.Status)
	}
}

func TestParseWindow(t *testing.T) {
	cases := map[string]int64{
		"1h":  1,
		"24h": 24,
		"7d":  168,
		"30d": 720,
	}
	for in, wantHours := range cases {
		d, err := parseWindow(in)
		if err != nil {
			t.Errorf("parseWindow(%q): %v", in, err)
			continue
		}
		if int64(d.Hours()) != wantHours {
			t.Errorf("parseWindow(%q) = %v hours, want %d", in, d.Hours(), wantHours)
		}
	}
	for _, bad := range []string{"", "h", "0d", "10x", "abc"} {
		if _, err := parseWindow(bad); err == nil {
			t.Errorf("parseWindow(%q) should error", bad)
		}
	}
}

func TestQueryTasksTool_BuildsBodyAndRoutes(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{"data":[],"meta":{"pagination":{"pageNumber":1}}}`)}
	toolCtx := &tool.Context{ControlQuerier: f}

	tl := NewQueryTasksTool()
	if tl.Name() != "query_tasks" {
		t.Fatalf("name = %q", tl.Name())
	}

	args := map[string]any{
		"window":      "90d",
		"agent_name":  "Agent AAA",
		"user_id":     "u1",
		"status":      "error",
		"group_by":    []any{"agent", "month"},
		"page_size":   50,
		"page_number": 2,
	}
	res, err := tl.Execute(context.Background(), args, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %v (%s)", res.Status, res.Message)
	}
	if f.app != "gateway" || f.method != "get" || f.path != "analytics/tasks/query" {
		t.Errorf("routed to %q/%q/%q, want gateway/get/analytics/tasks/query", f.app, f.method, f.path)
	}

	var body map[string]any
	if err := json.Unmarshal(f.body, &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["agentName"] != "Agent AAA" || body["userId"] != "u1" || body["status"] != "error" {
		t.Errorf("filters not mapped: %s", f.body)
	}
	dims, ok := body["groupDims"].([]any)
	if !ok || len(dims) != 2 || dims[0] != "agent" || dims[1] != "month" {
		t.Errorf("groupDims = %v, want [agent month]", body["groupDims"])
	}
	if body["pageSize"] != float64(50) || body["pageNumber"] != float64(2) {
		t.Errorf("pagination not mapped: %s", f.body)
	}
	if _, ok := body["startDate"]; !ok {
		t.Errorf("missing startDate: %s", f.body)
	}
	if len(res.DataObjects) != 1 || res.DataObjects[0].Name != "" || res.DataObjects[0].Disposition != tool.DispositionAuto {
		t.Errorf("expected one nameless auto-disposition data object, got %+v", res.DataObjects)
	}
}

func TestQueryTasksTool_CommaSeparatedGroupBy(t *testing.T) {
	got := sentinelStrSliceArg(map[string]any{"group_by": "agent, week , "}, "group_by")
	if len(got) != 2 || got[0] != "agent" || got[1] != "week" {
		t.Errorf("sentinelStrSliceArg(csv) = %v, want [agent week]", got)
	}
}

func TestQueryTasksTool_Metadata(t *testing.T) {
	tl := NewQueryTasksTool()
	if tl.Description() == "" {
		t.Error("Description must be non-empty")
	}
	p := tl.Parameters()
	if p == nil || p.Type != "object" {
		t.Fatalf("Parameters = %+v, want object schema", p)
	}
	for _, k := range []string{"window", "start_time", "end_time", "execution_mode", "agent_name", "user_id", "status", "group_by", "page_number", "page_size"} {
		if _, ok := p.Properties[k]; !ok {
			t.Errorf("Parameters missing property %q", k)
		}
	}
	gb, _ := p.Properties["group_by"].(map[string]any)
	if gb["type"] != "array" {
		t.Errorf("group_by type = %v, want array", gb["type"])
	}
}

func TestQueryTasksTool_ErrorPaths(t *testing.T) {
	tl := NewQueryTasksTool()

	t.Run("no control querier", func(t *testing.T) {
		res, err := tl.Execute(context.Background(), map[string]any{}, &tool.Context{})
		if err != nil {
			t.Fatalf("Execute err: %v", err)
		}
		if res.Status != tool.StatusError {
			t.Errorf("status = %v, want error when ControlQuerier is nil", res.Status)
		}
	})

	t.Run("bad window", func(t *testing.T) {
		f := &fakeControlQuerier{resp: json.RawMessage(`{}`)}
		res, _ := tl.Execute(context.Background(), map[string]any{"window": "5x"}, &tool.Context{ControlQuerier: f})
		if res.Status != tool.StatusError {
			t.Errorf("status = %v, want error for invalid window", res.Status)
		}
		if f.path != "" {
			t.Error("control should not be called when window parsing fails")
		}
	})

	t.Run("control error", func(t *testing.T) {
		f := &fakeControlQuerier{err: errTestControl}
		res, _ := tl.Execute(context.Background(), map[string]any{"window": "24h"}, &tool.Context{ControlQuerier: f})
		if res.Status != tool.StatusError {
			t.Errorf("status = %v, want error when control call fails", res.Status)
		}
	})

	t.Run("non-json response", func(t *testing.T) {
		f := &fakeControlQuerier{resp: json.RawMessage(`not json`)}
		res, _ := tl.Execute(context.Background(), map[string]any{"window": "24h"}, &tool.Context{ControlQuerier: f})
		if res.Status != tool.StatusError {
			t.Errorf("status = %v, want error for non-JSON response", res.Status)
		}
	})
}

var errTestControl = fmt.Errorf("control unavailable")

func TestQueryTools_LeaveTheArtifactNameToTheRuntime(t *testing.T) {
	datadog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(datadog.Close)
	datadogBase := func() datadogToolBase {
		return datadogToolBase{cfg: datadogConfig{apiKey: "k", appKey: "a", apiHost: datadog.URL, httpClient: datadog.Client()}}
	}
	for _, tc := range []struct {
		name string
		tool tool.BuiltinTool
	}{
		{"query_task_health", NewQueryTaskHealthTool()},
		{"query_failed_tasks", NewQueryFailedTasksTool()},
		{"query_slow_tasks", NewQuerySlowTasksTool()},
		{"query_agent_performance", NewQueryAgentPerformanceTool()},
		{"query_token_usage", NewQueryTokenUsageTool()},
		{"query_scheduled_task_health", NewQueryScheduledTaskHealthTool()},
		{"query_feedback_summary", NewQueryFeedbackSummaryTool()},
		{"query_tasks", NewQueryTasksTool()},
		{"datadog_query_logs", &DatadogQueryLogsTool{datadogToolBase: datadogBase()}},
		{"datadog_get_errors", &DatadogGetErrorsTool{datadogToolBase: datadogBase()}},
		{"correlate_task_with_logs", &CorrelateTaskWithLogsTool{datadogToolBase: datadogBase()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.tool.Name() != tc.name {
				t.Fatalf("tool name = %q, want %q", tc.tool.Name(), tc.name)
			}
			toolCtx := &tool.Context{ControlQuerier: &fakeControlQuerier{resp: json.RawMessage(`{"rows":[]}`)}, TraceID: "trace-123"}
			res, err := tc.tool.Execute(t.Context(), map[string]any{}, toolCtx)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if res.Status != tool.StatusSuccess || len(res.DataObjects) != 1 {
				t.Fatalf("status = %v with %d data objects, want success and exactly one (message: %s)", res.Status, len(res.DataObjects), res.Message)
			}
			if obj := res.DataObjects[0]; obj.Name != "" || obj.Disposition != tool.DispositionAuto {
				t.Errorf("data object name %q disposition %q, want no name and %q so the runtime picks a fresh one", obj.Name, obj.Disposition, tool.DispositionAuto)
			}
		})
	}
}
