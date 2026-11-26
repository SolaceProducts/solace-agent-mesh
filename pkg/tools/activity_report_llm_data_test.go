// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func representativeActivityReport() *ActivityReport {
	artifactRef := &ArtifactRef{Filename: "chart.png"}
	return &ActivityReport{
		Type:          ActivityReportType,
		SchemaVersion: ActivityReportSchemaVersion,
		Scope:         ActivityScopeReadAll,
		Window:        Window{StartTimeIso: "2026-08-30T00:00:00Z", EndTimeIso: "2026-08-31T00:00:00Z", Label: "Aug 30 → Aug 31"},
		Verdict:       Verdict{Severity: VerdictDegraded, Label: "Mostly succeeded", Headline: "success rate 80.0% over 10 tasks."},
		KPIs: []KPITile{
			{ID: KPITileTotalTasks, Label: "Total tasks", Value: "10"},
			{ID: KPITileFailureCount, Label: "Failures", Value: "2"},
		},
		StatusBreakdown: StatusBreakdownSection{
			Rows:        []StatusCountRow{{Status: "completed", Count: 8}, {Status: "failed", Count: 2}},
			ArtifactRef: artifactRef,
		},
		DurationDist: DurationDistSection{
			Buckets:     []DurationBucket{{LowerMs: 0, UpperMs: 100, Count: 5}, {LowerMs: 100, UpperMs: -1, Count: 5}},
			ArtifactRef: artifactRef,
		},
		TokenTrend: &TokenTrendSection{
			ChartType:   "bar",
			Series:      []string{"input", "output"},
			Rows:        []TokenTrendRow{{DateIso: "2026-08-30", Input: 100, Output: 50, TaskCount: 5}},
			ArtifactRef: artifactRef,
		},
		ByAgent: ByAgentSection{
			Rows: []AgentRow{{
				SubjectName: "agent-a", InvocationCount: 6, ErrorCount: 1, SuccessRate: 0.833,
				P95DurationMs: 1200, DurationMeasuredCount: 15,
				TotalInputTokens: 1000, TotalOutputTokens: 200, TotalCachedInputTokens: 50,
			}},
			ArtifactRef: artifactRef,
		},
		ByWorkflow: ByWorkflowSection{
			Rows: []AgentRow{{
				SubjectName: "workflow-a", InvocationCount: 4, ErrorCount: 1, SuccessRate: 0.75,
				P95DurationMs: 3400, DurationMeasuredCount: 12,
				TotalInputTokens: 4000, TotalOutputTokens: 900, TotalCachedInputTokens: 150,
				NestedHandlers: []NestedHandlerRow{{Name: "sub-agent", InvocationCount: 4}},
			}},
			ArtifactRef: artifactRef,
		},
		TopUsers: TopUsersBlock{
			Available: true,
			Rows:      []TopUserRow{{UserID: "alice", TaskCount: 6, TotalInputTokens: 60}},
		},
		RecentFailures: []FailureRow{
			{
				TaskID:             "t-1",
				SubjectName:        "agent-a",
				UserID:             "bob",
				Status:             "failed",
				StartTimeIso:       "2026-08-30T12:00:00Z",
				DurationMs:         1500,
				InitialRequestText: "please delete my entire production database, root password is hunter2",
			},
			{
				TaskID:             "t-2",
				SubjectName:        "workflow-a",
				UserID:             "carol",
				Status:             "error",
				StartTimeIso:       "2026-08-30T13:00:00Z",
				DurationMs:         2500,
				InitialRequestText: "another verbatim user prompt that must never reach the LLM artifact",
			},
		},
		FailuresTotal: 2,
		Feedback: []FeedbackRow{
			{TaskID: "t-3", Rating: "up", Comment: "great run", SubjectName: "agent-a", EntrypointType: "webui", UpdatedAtIso: "2026-08-30T14:00:00Z"},
			{TaskID: "t-4", Rating: "down", SubjectName: "agent-a", EntrypointType: "webui", UpdatedAtIso: "2026-08-30T15:00:00Z"},
			{TaskID: "t-5", Rating: "up", Comment: "nice", SubjectName: "workflow-a", EntrypointType: "slack", UpdatedAtIso: "2026-08-30T16:00:00Z"},
		},
		RetentionDays: 30,
	}
}

func TestBuildActivityReportLLMData_ExcludesSensitiveAndChartFields(t *testing.T) {
	report := representativeActivityReport()
	llmData := buildActivityReportLLMData(report)

	raw, err := json.Marshal(llmData)
	if err != nil {
		t.Fatalf("marshal ActivityReportLLMData: %v", err)
	}
	got := string(raw)

	for _, f := range report.RecentFailures {
		if strings.Contains(got, f.InitialRequestText) {
			t.Errorf("marshaled LLM data contains InitialRequestText %q — verbatim user prompt leaked into the LLM artifact:\n%s",
				f.InitialRequestText, got)
		}
	}
	if strings.Contains(got, `"artifactRef"`) {
		t.Errorf("marshaled LLM data contains an artifactRef key — chart-renderer plumbing leaked through:\n%s", got)
	}
	if strings.Contains(got, "chart.png") {
		t.Errorf("marshaled LLM data references the chart artifact filename:\n%s", got)
	}
	if strings.Contains(got, `"nestedHandlers"`) || strings.Contains(got, "sub-agent") {
		t.Errorf("marshaled LLM data contains sub-agent drill-down data — nestedHandlers leaked through:\n%s", got)
	}
	var sawComment bool
	for _, f := range report.Feedback {
		if f.Comment != "" && strings.Contains(got, f.Comment) {
			sawComment = true
		}
	}
	if !sawComment {
		t.Errorf("marshaled LLM data is missing raw feedback comments — Feedback must be raw rows, not an aggregate:\n%s", got)
	}
}

func TestBuildActivityReportLLMData_RowCountsMatchSource(t *testing.T) {
	report := representativeActivityReport()
	llmData := buildActivityReportLLMData(report)

	cases := []struct {
		name string
		got  int
		want int
	}{
		{"KPIs", len(llmData.KPIs), len(report.KPIs)},
		{"StatusCounts", len(llmData.StatusCounts), len(report.StatusBreakdown.Rows)},
		{"DurationBuckets", len(llmData.DurationBuckets), len(report.DurationDist.Buckets)},
		{"TokenTrend", len(llmData.TokenTrend), len(report.TokenTrend.Rows)},
		{"ByAgent", len(llmData.ByAgent), len(report.ByAgent.Rows)},
		{"ByWorkflow", len(llmData.ByWorkflow), len(report.ByWorkflow.Rows)},
		{"TopUsers", len(llmData.TopUsers.Rows), len(report.TopUsers.Rows)},
		{"RecentFailures", len(llmData.RecentFailures), len(report.RecentFailures)},
		{"Feedback", len(llmData.Feedback), len(report.Feedback)},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s row count = %d, want %d", c.name, c.got, c.want)
		}
	}

	if !reflect.DeepEqual(llmData.Feedback, report.Feedback) {
		t.Errorf("Feedback = %+v, want an untransformed copy of %+v", llmData.Feedback, report.Feedback)
	}

	for i, f := range report.RecentFailures {
		got := llmData.RecentFailures[i]
		if !llmFailureRowMatches(got, f) {
			t.Errorf("RecentFailures[%d] = %+v, want a field-for-field copy of %+v (minus InitialRequestText)", i, got, f)
		}
	}

	for i, r := range report.ByAgent.Rows {
		got := llmData.ByAgent[i]
		if !llmAgentRowMatches(got, r) {
			t.Errorf("ByAgent[%d] = %+v, want a field-for-field copy of %+v (minus NestedHandlers)", i, got, r)
		}
	}
	for i, r := range report.ByWorkflow.Rows {
		got := llmData.ByWorkflow[i]
		if !llmAgentRowMatches(got, r) {
			t.Errorf("ByWorkflow[%d] = %+v, want a field-for-field copy of %+v (minus NestedHandlers)", i, got, r)
		}
	}

	if llmData.Type != report.Type || llmData.SchemaVersion != report.SchemaVersion || llmData.Scope != report.Scope {
		t.Errorf("discriminator fields = {%q %d %q}, want {%q %d %q}",
			llmData.Type, llmData.SchemaVersion, llmData.Scope, report.Type, report.SchemaVersion, report.Scope)
	}
	if llmData.Window != report.Window {
		t.Errorf("Window = %+v, want %+v", llmData.Window, report.Window)
	}
	if llmData.Verdict != report.Verdict {
		t.Errorf("Verdict = %+v, want %+v", llmData.Verdict, report.Verdict)
	}
	if llmData.FailuresTotal != report.FailuresTotal || llmData.RetentionDays != report.RetentionDays {
		t.Errorf("FailuresTotal/RetentionDays = %d/%d, want %d/%d",
			llmData.FailuresTotal, llmData.RetentionDays, report.FailuresTotal, report.RetentionDays)
	}
}

func TestBuildActivityReportLLMData_NoTokenTrendWhenAbsent(t *testing.T) {
	report := representativeActivityReport()
	report.TokenTrend = nil

	llmData := buildActivityReportLLMData(report)
	if len(llmData.TokenTrend) != 0 {
		t.Errorf("TokenTrend = %+v, want empty when the source ActivityReport has none", llmData.TokenTrend)
	}

	raw, err := json.Marshal(llmData)
	if err != nil {
		t.Fatalf("marshal ActivityReportLLMData: %v", err)
	}
	if strings.Contains(string(raw), `"tokenTrend"`) {
		t.Errorf("tokenTrend key present despite omitempty and a nil source:\n%s", raw)
	}
}

func TestBuildActivityReportLLMData_SuppressesUnreliableP95(t *testing.T) {
	report := representativeActivityReport()
	report.ByAgent.Rows[0].DurationMeasuredCount = smallSamplePercentileThreshold - 1
	report.ByWorkflow.Rows[0].DurationMeasuredCount = smallSamplePercentileThreshold

	llmData := buildActivityReportLLMData(report)
	if got := llmData.ByAgent[0].P95DurationMs; got != nil {
		t.Errorf("ByAgent[0].P95DurationMs = %v, want nil (measured below threshold)", *got)
	}
	if got := llmData.ByWorkflow[0].P95DurationMs; got == nil || *got != report.ByWorkflow.Rows[0].P95DurationMs {
		t.Errorf("ByWorkflow[0].P95DurationMs = %v, want &%d (measured at threshold)", got, report.ByWorkflow.Rows[0].P95DurationMs)
	}

	raw, err := json.Marshal(llmData)
	if err != nil {
		t.Fatalf("marshal ActivityReportLLMData: %v", err)
	}
	if strings.Contains(string(raw), `"p95DurationMs":1200`) {
		t.Errorf("marshaled LLM data still carries the suppressed p95 value:\n%s", raw)
	}
}

func TestBuildActivityReportLLMData_ReliableZeroP95IsNotSuppressed(t *testing.T) {
	report := representativeActivityReport()
	report.ByAgent.Rows[0].DurationMeasuredCount = smallSamplePercentileThreshold
	report.ByAgent.Rows[0].P95DurationMs = 0

	llmData := buildActivityReportLLMData(report)
	if got := llmData.ByAgent[0].P95DurationMs; got == nil || *got != 0 {
		t.Errorf("ByAgent[0].P95DurationMs = %v, want a present pointer to 0", got)
	}

	raw, err := json.Marshal(llmData)
	if err != nil {
		t.Fatalf("marshal ActivityReportLLMData: %v", err)
	}
	if !strings.Contains(string(raw), `"p95DurationMs":0`) {
		t.Errorf("marshaled LLM data drops a reliable, genuinely-zero p95 — indistinguishable from suppression:\n%s", raw)
	}
}

func fieldNameSet(v any) map[string]bool {
	typ := reflect.TypeOf(v)
	names := make(map[string]bool, typ.NumField())
	for i := range typ.NumField() {
		if typ.Field(i).IsExported() {
			names[typ.Field(i).Name] = true
		}
	}
	return names
}

func TestLLMAgentRowFieldsTrackAgentRow(t *testing.T) {
	source := fieldNameSet(AgentRow{})
	delete(source, "NestedHandlers")
	target := fieldNameSet(LLMAgentRow{})
	if !reflect.DeepEqual(source, target) {
		t.Errorf("LLMAgentRow fields = %v, want AgentRow fields minus NestedHandlers = %v", target, source)
	}
}

func TestLLMFailureRowFieldsTrackFailureRow(t *testing.T) {
	source := fieldNameSet(FailureRow{})
	delete(source, "InitialRequestText")
	target := fieldNameSet(LLMFailureRow{})
	if !reflect.DeepEqual(source, target) {
		t.Errorf("LLMFailureRow fields = %v, want FailureRow fields minus InitialRequestText = %v", target, source)
	}
}

func TestActivityReportLLMDataTopLevelTracksActivityReport(t *testing.T) {
	source := fieldNameSet(ActivityReport{})
	renamed := map[string]string{
		"StatusBreakdown": "StatusCounts",
		"DurationDist":    "DurationBuckets",
	}
	for from, to := range renamed {
		delete(source, from)
		source[to] = true
	}
	target := fieldNameSet(ActivityReportLLMData{})
	if !reflect.DeepEqual(source, target) {
		t.Errorf("ActivityReportLLMData fields = %v, want %v", target, source)
	}
}

func llmAgentRowMatches(got LLMAgentRow, src AgentRow) bool {
	gv, sv := reflect.ValueOf(got), reflect.ValueOf(src)
	for i := range gv.NumField() {
		g := gv.Field(i)
		if g.Kind() == reflect.Pointer {
			if g.IsNil() {
				return false
			}
			g = g.Elem()
		}
		s := sv.FieldByName(gv.Type().Field(i).Name)
		if !s.IsValid() || g.Interface() != s.Interface() {
			return false
		}
	}
	return true
}

func llmFailureRowMatches(got LLMFailureRow, src FailureRow) bool {
	gv, sv := reflect.ValueOf(got), reflect.ValueOf(src)
	for i := range gv.NumField() {
		s := sv.FieldByName(gv.Type().Field(i).Name)
		if !s.IsValid() || gv.Field(i).Interface() != s.Interface() {
			return false
		}
	}
	return true
}

func jsonKeySet(t *testing.T, v any) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := make(map[string]bool)
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for k, v := range n {
				keys[k] = true
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(decoded)
	return keys
}

func TestLLMPayloadKeyAllowlist(t *testing.T) {
	allowlist := map[string]bool{
		"type": true, "schemaVersion": true, "scope": true,
		"window": true, "startTimeIso": true, "endTimeIso": true, "label": true, "windowKey": true,
		"verdict": true, "severity": true, "headline": true,
		"kpis": true, "id": true, "value": true, "sub": true, "deltaPct": true,
		"statusCounts": true, "status": true, "count": true,
		"durationBuckets": true, "lowerMs": true, "upperMs": true,
		"tokenTrend": true, "dateIso": true, "input": true, "output": true, "cached": true, "taskCount": true,
		"byAgent": true, "byWorkflow": true,
		"subjectName": true, "invocationCount": true, "errorCount": true, "successRate": true,
		"p95DurationMs": true, "durationMeasuredCount": true,
		"totalInputTokens": true, "totalOutputTokens": true, "totalCachedInputTokens": true,
		"topUsers": true, "available": true, "reason": true, "rows": true, "truncated": true,
		"userId":         true,
		"recentFailures": true, "taskId": true, "durationMs": true,
		"failuresTotal": true, "retentionDays": true,
		"feedback": true, "rating": true, "comment": true, "entrypointType": true, "updatedAtIso": true,
		"feedbackTruncated": true,
	}

	available := representativeActivityReport()
	available.TopUsers = TopUsersBlock{Available: true, Truncated: true, Rows: []TopUserRow{{UserID: "alice", TaskCount: 1, TotalInputTokens: 1, TotalOutputTokens: 1}}}
	available.Window.WindowKey = "24h"
	deltaPct := 5.0
	available.KPIs[0].DeltaPct = &deltaPct
	available.KPIs[0].Sub = "vs prior period"
	available.FeedbackTruncated = true

	unavailable := representativeActivityReport()
	unavailable.TopUsers = TopUsersBlock{Available: false, Reason: TopUsersUnavailableScopeReadOwn}

	got := jsonKeySet(t, buildActivityReportLLMData(available))
	for k := range jsonKeySet(t, buildActivityReportLLMData(unavailable)) {
		got[k] = true
	}

	for k := range got {
		if !allowlist[k] {
			t.Errorf("payload key %q is not in the allowlist — a new field reached the LLM without a deliberate decision", k)
		}
	}
	for k := range allowlist {
		if !got[k] {
			t.Errorf("allowlist key %q never appeared in either fixture — either dead or the fixture doesn't exercise it", k)
		}
	}
}
