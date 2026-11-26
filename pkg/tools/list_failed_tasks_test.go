// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestListFailedTasksBody_UserIDAndWireKeys(t *testing.T) {
	raw, _, err := listFailedTasksBody(map[string]any{
		"window":     "24h",
		"userId":     "bob",
		"pageNumber": float64(3),
		"pageSize":   float64(50),
	})
	if err != nil {
		t.Fatalf("listFailedTasksBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["userId"] != "bob" {
		t.Errorf(`body["userId"] = %v, want "bob"`, body["userId"])
	}
	if body["window"] != "24h" {
		t.Errorf(`body["window"] = %v, want "24h"`, body["window"])
	}
	if _, ok := body["startDate"]; ok {
		t.Error("startDate present when window is set — window must take precedence")
	}
	if body["pageNumber"] != float64(3) || body["pageSize"] != float64(50) {
		t.Errorf("pagination = (%v,%v), want (3,50)", body["pageNumber"], body["pageSize"])
	}
}

func TestListFailedTasksBody_OmitsUserIDAndConvertsAbsoluteDates(t *testing.T) {
	start := "2026-06-01T00:00:00Z"
	end := "2026-06-15T00:00:00Z"
	raw, _, err := listFailedTasksBody(map[string]any{"startDate": start, "endDate": end})
	if err != nil {
		t.Fatalf("listFailedTasksBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := body["userId"]; ok {
		t.Errorf("userId present when not supplied: %v", body["userId"])
	}
	if _, ok := body["window"]; ok {
		t.Error("window present when only absolute dates supplied")
	}
	if body["pageNumber"] != float64(1) || body["pageSize"] != float64(20) {
		t.Errorf("default pagination = (%v,%v), want (1,20)", body["pageNumber"], body["pageSize"])
	}
	st, _ := time.Parse(time.RFC3339, start)
	en, _ := time.Parse(time.RFC3339, end)
	if body["startDate"] != float64(st.UnixMilli()) || body["endDate"] != float64(en.UnixMilli()) {
		t.Errorf("dates = (%v,%v), want (%d,%d) as UnixMilli",
			body["startDate"], body["endDate"], st.UnixMilli(), en.UnixMilli())
	}
}

func TestListFailedTasksBody_InvalidPageSizeKeepsDefault(t *testing.T) {
	for _, size := range []float64{0, -5} {
		raw, _, err := listFailedTasksBody(map[string]any{"pageSize": size})
		if err != nil {
			t.Fatalf("listFailedTasksBody(pageSize=%v): %v", size, err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body["pageSize"] != float64(20) {
			t.Errorf("pageSize=%v: body[pageSize] = %v, want default 20", size, body["pageSize"])
		}
		if body["pageNumber"] != float64(1) {
			t.Errorf("pageSize=%v: body[pageNumber] = %v, want default 1", size, body["pageNumber"])
		}
	}
}

func TestListFailedTasksBody_PageSizeCapped(t *testing.T) {
	for _, tc := range []struct{ asked, want float64 }{{100, 100}, {101, 100}, {500, 100}} {
		raw, _, err := listFailedTasksBody(map[string]any{"pageSize": tc.asked})
		if err != nil {
			t.Fatalf("listFailedTasksBody: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body["pageSize"] != tc.want {
			t.Errorf("pageSize=%v: body[pageSize] = %v, want %v", tc.asked, body["pageSize"], tc.want)
		}
	}
}

func TestListFailedTasksBody_InvalidPageNumberKeepsFirstPage(t *testing.T) {
	for _, page := range []float64{0, -1} {
		raw, _, err := listFailedTasksBody(map[string]any{"pageNumber": page})
		if err != nil {
			t.Fatalf("listFailedTasksBody: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body["pageNumber"] != float64(1) {
			t.Errorf("pageNumber=%v: body[pageNumber] = %v, want 1", page, body["pageNumber"])
		}
	}
}

func TestListFailedTasksBody_RejectsMalformedDates(t *testing.T) {
	for _, key := range []string{"startDate", "endDate"} {
		_, _, err := listFailedTasksBody(map[string]any{key: "last-tuesday"})
		if err == nil {
			t.Errorf("%s=%q: err = nil, want a parse error", key, "last-tuesday")
			continue
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("%s: err = %q, want it to name the offending field", key, err)
		}
	}
}

func TestListFailedTasksBody_SharesWindowValidationWithReport(t *testing.T) {
	cases := []struct {
		name, start, end, wantSub string
	}{
		{"inverted", "2026-06-15T00:00:00Z", "2026-06-01T00:00:00Z", "strictly before"},
		{"beyond 90 days", "2020-01-01T00:00:00Z", "2026-06-01T00:00:00Z", "90-day"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := listFailedTasksBody(map[string]any{"startDate": c.start, "endDate": c.end})
			if err == nil {
				t.Fatalf("%s range must error, got nil", c.name)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("err = %q, want it to mention %q", err, c.wantSub)
			}
		})
	}
}

func TestListFailedTasks_ControlCallFailureIsToolError(t *testing.T) {
	toolCtx := &tool.Context{ControlQuerier: &failingQuerier{err: errors.New("gateway unavailable")}}

	res, err := NewListFailedTasksTool().Execute(context.Background(), map[string]any{"window": "24h"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error (%v); want a tool-level error result", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("Status = %q, want %q", res.Status, tool.StatusError)
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("failed read must not emit a data object, got %d", len(res.DataObjects))
	}
	if !strings.Contains(res.Message, "gateway unavailable") {
		t.Errorf("Message = %q, want it to surface the underlying cause", res.Message)
	}
}

func TestStripRequestText(t *testing.T) {
	in := `{"data":[` +
		`{"id":"a","name":"A","status":"failed","initialRequestText":"secret one","userId":"u1"},` +
		`{"id":"b","name":"B","status":"timeout","initialRequestText":"secret two"}],` +
		`"meta":{"pagination":{"pageNumber":1,"pageSize":50,"count":2,"totalPages":1},"windowActivityCount":9}}`

	var got map[string]any
	if err := json.Unmarshal(stripRequestText(json.RawMessage(in)), &got); err != nil {
		t.Fatalf("unmarshal stripped: %v", err)
	}
	rows, ok := got["data"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("data = %v, want 2 rows", got["data"])
	}
	for i, r := range rows {
		row := r.(map[string]any)
		if _, present := row["initialRequestText"]; present {
			t.Errorf("row %d still carries initialRequestText", i)
		}
		if row["id"] == nil || row["status"] == nil {
			t.Errorf("row %d lost a rendered field: %v", i, row)
		}
	}
	if rows[0].(map[string]any)["userId"] != "u1" {
		t.Error("userId must survive — admins drill down by user")
	}
	meta, _ := got["meta"].(map[string]any)
	if meta == nil || meta["windowActivityCount"] != float64(9) {
		t.Errorf("meta not preserved: %v", got["meta"])
	}
	if p, _ := meta["pagination"].(map[string]any); p == nil || p["count"] != float64(2) {
		t.Errorf("pagination not preserved: %v", meta["pagination"])
	}
}

func TestStripRequestText_MalformedFallsThrough(t *testing.T) {
	for _, in := range []string{`not json`, `{"data":"not-an-array"}`} {
		if got := string(stripRequestText(json.RawMessage(in))); got != in {
			t.Errorf("input %q returned %q, want it unchanged", in, got)
		}
	}
}

func failedPage(count, pageNumber, pageSize, totalPages, windowActivity int, rows string) string {
	return `{"data":[` + rows + `],"meta":{"pagination":{"pageNumber":` + itoa(pageNumber) +
		`,"pageSize":` + itoa(pageSize) + `,"totalPages":` + itoa(totalPages) + `,"count":` + itoa(count) +
		`},"windowActivityCount":` + itoa(windowActivity) + `}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestListFailedTasks_RendersMarkdownReport(t *testing.T) {
	rows := `{"id":"t1","name":"billing|agent","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:23:45Z","durationMs":1500,"userId":"u1","initialRequestText":"secret"},` +
		`{"id":"t2","name":"","status":"timeout","statusEmoji":"⚠️","startDate":"2026-08-01T15:00:00Z","durationMs":900000,"userId":"u1"}`
	raw := failedPage(45, 2, 20, 3, 120, rows)
	run := newMarkdownToolRun(raw)

	res, err := NewListFailedTasksTool().Execute(context.Background(), map[string]any{"window": "24h", "pageNumber": float64(2)}, run.toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(run.notified) != 1 {
		t.Fatalf("artifact_saved notifications = %+v, want one", run.notified)
	}
	stem := strings.TrimSuffix(run.notified[0].Filename, ".md")
	if !strings.HasPrefix(stem, "failed_tasks_") || !strings.HasSuffix(stem, "_p2") {
		t.Fatalf("report name = %q, want failed_tasks_<window>_p2.md", run.notified[0].Filename)
	}
	md := assertMarkdownDelivery(t, run, res, stem+".md", stem+"_data.json", tool.DispositionArtifact, stripRequestText(json.RawMessage(raw)))
	for _, want := range []string{
		"## ⚠️ Failed tasks — ",
		"showing 21–40 of 45 (page 2 of 3)",
		"| Aug 1 14:23:45 UTC | `billing\\|agent` | ❌ Failed | 1s | `t1` |",
		"| Aug 1 15:00:00 UTC | `—` | ⚠️ Timeout | — | `t2` |",
		"Ask for the next page for more.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "secret") {
		t.Error("initialRequestText leaked into the report")
	}
	wantData := map[string]any{"count": 45, "pageNumber": 2, "totalPages": 3}
	if !reflect.DeepEqual(res.Data, wantData) {
		t.Errorf("Data = %v, want %v (pagination for the LLM, no rows)", res.Data, wantData)
	}
	declared := NewListFailedTasksTool().ResultSchema().Properties
	if got, want := mapKeys(res.Data), mapKeys(declared); !reflect.DeepEqual(got, want) {
		t.Errorf("Data keys = %v, ResultSchema declares %v", got, want)
	}
}

func TestListFailedTasks_LastPage(t *testing.T) {
	row := `{"id":"t1","name":"billing","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:23:45Z","durationMs":1500,"userId":"u1"}`
	for _, tc := range []struct {
		count int
		want  string
	}{{45, "showing 41–45 of 45 (page 3 of 3)"}, {41, "showing 41–41 of 41 (page 3 of 3)"}} {
		run := newMarkdownToolRun(failedPage(tc.count, 3, 20, 3, 120, row))
		res, err := NewListFailedTasksTool().Execute(context.Background(), map[string]any{"window": "24h", "pageNumber": float64(3)}, run.toolCtx)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != tool.StatusSuccess || len(run.notified) != 1 {
			t.Fatalf("status = %q, notified = %d", res.Status, len(run.notified))
		}
		md := run.load(t, run.notified[0].Filename)
		if !strings.Contains(md, tc.want) || !strings.Contains(md, "Ask me to explain a task") {
			t.Errorf("count %d: unexpected last page:\n%s", tc.count, md)
		}
		if strings.Contains(md, "Ask for the next page") || strings.Contains(md, "past the end") {
			t.Errorf("count %d: last page offers a next page or claims past the end:\n%s", tc.count, md)
		}
	}
}

func TestListFailedTasks_EmptyBranches(t *testing.T) {
	cases := []struct {
		name, raw, wantMD string
	}{
		{"no activity", failedPage(0, 1, 20, 0, 0, ""), "## 🟦 No activity — "},
		{"no failures", failedPage(0, 1, 20, 0, 7, ""), "7 tasks ran, none failed."},
		{"past the last page", failedPage(9, 2, 50, 1, 21, ""), "Page 2 is past the end: 9 failures fit on 1 page(s)."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := newMarkdownToolRun(c.raw)
			res, err := NewListFailedTasksTool().Execute(context.Background(), map[string]any{}, run.toolCtx)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			stem := strings.TrimSuffix(run.notified[0].Filename, ".md")
			md := assertMarkdownDelivery(t, run, res, stem+".md", stem+"_data.json", tool.DispositionArtifact, stripRequestText(json.RawMessage(c.raw)))
			if !strings.Contains(md, c.wantMD) || strings.Contains(md, "| When |") {
				t.Errorf("report = %q, want %q and no table", md, c.wantMD)
			}
		})
	}
}
