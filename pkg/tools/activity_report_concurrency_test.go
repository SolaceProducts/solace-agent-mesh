// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestActivityReport_RendersDataIntoHTML(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{ArtifactService: svc, AgentName: "a", UserID: "u", SessionID: "s"}

	rep := &ActivityReport{
		Window:          Window{Label: "Jul 23 → Jul 30"},
		Verdict:         Verdict{Severity: VerdictOk, Headline: "success rate 100.0% over 7 tasks."},
		KPIs:            []KPITile{{ID: KPITileTotalTasks, Label: "Total tasks", Value: "8151"}},
		StatusBreakdown: StatusBreakdownSection{Rows: []StatusCountRow{{Status: "completed", Count: 8151}}},
		RecentFailures:  []FailureRow{{TaskID: "10000000-0000-0000-0000-000000000001", Status: "error"}},
	}
	html, _, err := renderActivityReport(context.Background(), toolCtx, rep, activityFilters{}, time.Now(),
		"activity_report_2026-07-23_to_2026-07-30.html")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := string(html)

	if !strings.Contains(got, "8151") {
		t.Error("KPI value from the data map is absent — the Liquid region was not rendered")
	}
	if !strings.Contains(got, "completed") {
		t.Error("status row from the data map is absent — the table loop was not rendered")
	}
	if !strings.Contains(got, "explain a task") {
		t.Error("explain_task hint is absent from a report with failures")
	}
	for _, leftover := range []string{"{%", "{{", "@@", "template_liquid", activityLiquidBegin, activityLiquidEnd} {
		if strings.Contains(got, leftover) {
			t.Errorf("saved report still contains unrendered %q — it is not a finished document", leftover)
		}
	}

	noFailuresHTML, _, err := renderActivityReport(context.Background(), toolCtx,
		&ActivityReport{Window: Window{Label: "L"}}, activityFilters{}, time.Now(), "no_failures.html")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(noFailuresHTML), "explain a task") {
		t.Error("explain_task hint is present in a report with no failures")
	}
}

func TestActivityReport_SavesOnlyTheReportArtifact(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{ArtifactService: svc, AgentName: "a", UserID: "u", SessionID: "s"}
	ctx := context.Background()

	const outName = "activity_report_2026-07-23_to_2026-07-30.html"
	if _, _, err := renderActivityReport(ctx, toolCtx,
		&ActivityReport{Window: Window{Label: "L"}}, activityFilters{}, time.Now(), outName); err != nil {
		t.Fatalf("render: %v", err)
	}

	has := func(name string) bool {
		vs, _ := svc.Inner.ListVersions(ctx, svc.Resolve("a", "u", "s", name))
		return len(vs) > 0
	}
	if !has(outName) {
		t.Errorf("windowed report %q was not saved", outName)
	}
	if has("activity_data.json") {
		t.Error("activity_data.json was created — the report renders in process and needs no companion data artifact")
	}
	if has("activity_report.html") {
		t.Error("static activity_report.html was created — the duplicate HTML must be gone")
	}
}

func TestActivityReport_ConcurrentRendersAreSelfConsistent(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{ArtifactService: svc, AgentName: "a", UserID: "u", SessionID: "s"}
	ctx := context.Background()

	const markerA = "WINDOWa2f9cMARKER"
	const markerB = "WINDOWb7a1dMARKER"

	render := func(marker, outName string) (string, error) {
		h, _, err := renderActivityReport(ctx, toolCtx,
			&ActivityReport{
				Window: Window{Label: "W"},
				KPIs:   []KPITile{{ID: KPITileTotalTasks, Label: "Total tasks", Value: marker}},
			}, activityFilters{}, time.Now(), outName)
		return string(h), err
	}

	for iter := range 40 {
		var wg sync.WaitGroup
		var htmlA, htmlB string
		var errA, errB error
		wg.Add(2)
		go func() {
			defer wg.Done()
			htmlA, errA = render(markerA, "activity_report_2026-07-29_to_2026-07-30.html")
		}()
		go func() {
			defer wg.Done()
			htmlB, errB = render(markerB, "activity_report_2026-07-23_to_2026-07-30.html")
		}()
		wg.Wait()

		if errA != nil || errB != nil {
			t.Fatalf("iter %d: render failed: A=%v B=%v", iter, errA, errB)
		}
		if !strings.Contains(htmlA, markerA) || strings.Contains(htmlA, markerB) {
			t.Fatalf("iter %d: report A does not carry its own data", iter)
		}
		if !strings.Contains(htmlB, markerB) || strings.Contains(htmlB, markerA) {
			t.Fatalf("iter %d: report B does not carry its own data", iter)
		}
	}
}

func activityQuerierWithAgentMarker(marker string) *stubActivityQuerier {
	return &stubActivityQuerier{respond: func(endpoint string) json.RawMessage {
		switch {
		case strings.HasSuffix(endpoint, "tasks/summary"):
			return json.RawMessage(`{"totalTasks":1,"byStatus":{"completed":1}}`)
		case endpoint == "activity/agents/performance":
			return json.RawMessage(fmt.Sprintf(`{"data":[{"agent":{"id":"a1","name":%q},"invocationCount":1}]}`, marker))
		default:
			return json.RawMessage(`{"data":[]}`)
		}
	}}
}

func TestReportActivityTool_ConcurrentExecuteCallsAreSelfConsistent(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}

	const markerA = "AGENTMARKERaaa111"
	const markerB = "AGENTMARKERbbb222"

	run := func(marker string) (*tool.Result, error) {
		toolCtx := &tool.Context{
			ControlQuerier:  activityQuerierWithAgentMarker(marker),
			ArtifactService: svc,
			AgentName:       "a",
			UserID:          "u",
			SessionID:       "s",
		}
		return NewReportActivityTool().Execute(context.Background(), map[string]any{"window": "24h"}, toolCtx)
	}

	for iter := range 20 {
		var wg sync.WaitGroup
		var resA, resB *tool.Result
		var errA, errB error
		wg.Add(2)
		go func() {
			defer wg.Done()
			resA, errA = run(markerA)
		}()
		go func() {
			defer wg.Done()
			resB, errB = run(markerB)
		}()
		wg.Wait()

		if errA != nil || errB != nil {
			t.Fatalf("iter %d: Execute failed: A=%v B=%v", iter, errA, errB)
		}
		if resA == nil || resA.Status == tool.StatusError || resB == nil || resB.Status == tool.StatusError {
			t.Fatalf("iter %d: Execute errored instead of delivering a report: A=%+v B=%+v", iter, resA, resB)
		}
		if len(resA.DataObjects) != 2 || len(resB.DataObjects) != 2 {
			t.Fatalf("iter %d: DataObjects = A:%+v B:%+v, want [html, llmData] on each", iter, resA.DataObjects, resB.DataObjects)
		}

		llmContentA, llmContentB := string(resA.DataObjects[1].Content), string(resB.DataObjects[1].Content)
		if !strings.Contains(llmContentA, markerA) || strings.Contains(llmContentA, markerB) {
			t.Fatalf("iter %d: call A's LLM data does not carry only its own marker:\n%s", iter, llmContentA)
		}
		if !strings.Contains(llmContentB, markerB) || strings.Contains(llmContentB, markerA) {
			t.Fatalf("iter %d: call B's LLM data does not carry only its own marker:\n%s", iter, llmContentB)
		}

		htmlRefA, htmlRefB := resA.DataObjects[0].ArtifactRef, resB.DataObjects[0].ArtifactRef
		if htmlRefA == nil || htmlRefB == nil {
			t.Fatalf("iter %d: HTML DataObject missing its ArtifactRef: A=%+v B=%+v", iter, htmlRefA, htmlRefB)
		}
		if htmlRefA.Filename != htmlRefB.Filename {
			t.Fatalf("iter %d: expected both calls to target the same filename (same window/scope/filters), got %q vs %q",
				iter, htmlRefA.Filename, htmlRefB.Filename)
		}
		if htmlRefA.Version == htmlRefB.Version {
			t.Fatalf("iter %d: concurrent saves to %q landed on the same version %d — one call clobbered the other",
				iter, htmlRefA.Filename, htmlRefA.Version)
		}
	}
}
