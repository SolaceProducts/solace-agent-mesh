// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func simulateTaskHealth(byStatus map[string]int) *taskHealthResp {
	total := 0
	for _, c := range byStatus {
		total += c
	}
	den := total - byStatus["in_progress"] - byStatus["stale"]
	var rate float64
	if den > 0 {
		rate = float64(byStatus["completed"]) / float64(den)
	}
	return &taskHealthResp{TotalTasks: total, SuccessRate: rate, ByStatus: byStatus}
}

var rateInProseRe = regexp.MustCompile(`\d+\.\d%`)

func TestActivityReportSurfacesAgree(t *testing.T) {
	scenarios := []map[string]int{
		{},
		{"stale": 100},
		{"in_progress": 100},
		{"completed": 90, "stale": 10},
		{"canceled": 15},
		{"completed": 80, "canceled": 15, "unknown": 5},
		{"failed": 1, "stale": 9},
		{"timeout": 12},
		{"completed": 90, "failed": 10},
		{"completed": 70, "failed": 5, "error": 3, "timeout": 2, "in_progress": 10, "stale": 10},
	}

	for _, byStatus := range scenarios {
		t.Run(fmt.Sprint(byStatus), func(t *testing.T) {
			h := simulateTaskHealth(byStatus)
			p := populationsOf(h)

			report := composeActivityReport(ActivityScopeReadOwn, Window{Label: "W"}, h, nil,
				nil, nil, nil, nil, p.failures, nil, nil, false, false, nil, false)
			digest := composeChatDigest(report, activityFilters{})
			headline := report.Verdict.Headline
			rateTile := lookupKPI(report.KPIs, KPITileSuccessRate)

			llmData := buildActivityReportLLMData(report)
			llmJSON, jsonErr := json.Marshal(llmData)
			if jsonErr != nil {
				t.Fatalf("marshal ActivityReportLLMData: %v", jsonErr)
			}
			if llmData.Verdict.Severity != report.Verdict.Severity {
				t.Errorf("LLM data Verdict.Severity = %q, want %q (report)", llmData.Verdict.Severity, report.Verdict.Severity)
			}
			if llmFailureTile := lookupKPI(llmData.KPIs, KPITileFailureCount); llmFailureTile != commaInt(p.failures) {
				t.Errorf("LLM data failureCount KPI = %q, want %q", llmFailureTile, commaInt(p.failures))
			}
			if len(llmData.StatusCounts) != len(report.StatusBreakdown.Rows) {
				t.Errorf("LLM data StatusCounts has %d rows, want %d (report.StatusBreakdown.Rows)",
					len(llmData.StatusCounts), len(report.StatusBreakdown.Rows))
			}
			var roundTripped ActivityReportLLMData
			if err := json.Unmarshal(llmJSON, &roundTripped); err != nil {
				t.Fatalf("unmarshal LLM data JSON: %v", err)
			}
			if got := lookupKPI(roundTripped.KPIs, KPITileFailureCount); got != commaInt(p.failures) {
				t.Errorf("LLM data JSON round-trip failureCount KPI = %q, want %q", got, commaInt(p.failures))
			}
			totalTile := lookupKPI(report.KPIs, KPITileTotalTasks)

			if got := p.completed + p.failures + p.other + p.inProgress + p.stale; got != p.total {
				t.Errorf("populations do not sum to the window: %d != %d (%+v)", got, p.total, p)
			}

			gotOk := report.Verdict.Severity == VerdictOk
			wantOk := p.terminal > 0 && p.failures == 0
			if gotOk != wantOk {
				t.Errorf("VerdictOk = %v, want %v (terminal=%d failures=%d)", gotOk, wantOk, p.terminal, p.failures)
			}

			wantNoData := p.terminal == 0
			if (report.Verdict.Severity == VerdictNoData) != wantNoData {
				t.Errorf("VerdictNoData = %v, want %v (terminal=%d)", report.Verdict.Severity == VerdictNoData, wantNoData, p.terminal)
			}
			if isEmptyWindow(report) != wantNoData {
				t.Errorf("isEmptyWindow = %v, want %v — the digest gate must agree with the verdict", isEmptyWindow(report), wantNoData)
			}

			if p.terminal == 0 {
				if rateTile != "—" {
					t.Errorf("success-rate tile = %q, want %q with nothing finished", rateTile, "—")
				}
				if rateInProseRe.MatchString(headline) {
					t.Errorf("headline quotes a rate with a zero denominator: %q", headline)
				}
				if rateInProseRe.MatchString(digest) {
					t.Errorf("digest quotes a rate with a zero denominator:\n%s", digest)
				}
			}

			if p.terminal > 0 && p.terminal != p.total {
				if !strings.Contains(headline, "finished") {
					t.Errorf("headline quotes a rate over %d of %d tasks without saying \"finished\": %q",
						p.terminal, p.total, headline)
				}
				if !strings.Contains(digest, "finished") {
					t.Errorf("digest pairs total %q with a rate over %d and never names the denominator:\n%s",
						totalTile, p.terminal, digest)
				}
			}

			switch report.Verdict.Label {
			case "All succeeded":
				if p.completed != p.total {
					t.Errorf("%q but only %d of %d tasks completed", report.Verdict.Label, p.completed, p.total)
				}
			case "Finished tasks succeeded":
				if p.completed != p.terminal {
					t.Errorf("%q but only %d of %d finished tasks completed", report.Verdict.Label, p.completed, p.terminal)
				}
			}

			if tile := lookupKPI(report.KPIs, KPITileFailureCount); tile != commaInt(p.failures) {
				t.Errorf("failures tile = %q, want %q", tile, commaInt(p.failures))
			}
			if p.failures > 0 && !strings.Contains(digest, commaInt(p.failures)) {
				t.Errorf("digest omits the failure count %d:\n%s", p.failures, digest)
			}

			if p.inProgress+p.stale > 0 {
				if strings.Contains(digest, "All succeeded") {
					t.Errorf("digest claims \"All succeeded\" with %d running + %d stale:\n%s",
						p.inProgress, p.stale, digest)
				}
				if p.terminal == 0 && !strings.Contains(digest, "stale") && p.stale > 0 {
					t.Errorf("a window that is entirely stale never mentions stale:\n%s", digest)
				}
			}
		})
	}
}

func TestActivityReportDigestQuotesTotalFailures(t *testing.T) {
	h := simulateTaskHealth(map[string]int{"completed": 53, "failed": 47})
	page := make([]failedTaskResp, 10)
	for i := range page {
		page[i] = failedTaskResp{ID: fmt.Sprintf("t%d", i), Status: "failed"}
	}
	report := composeActivityReport(ActivityScopeReadOwn, Window{Label: "W"}, h, nil,
		nil, nil, nil, page, 47, nil, nil, false, false, nil, false)

	digest := composeChatDigest(report, activityFilters{})
	if !strings.Contains(digest, "47 failures (showing first 10)") {
		t.Errorf("digest should quote the window total and label the page; got:\n%s", digest)
	}
	if strings.Contains(digest, "| 10 failures") {
		t.Errorf("digest reports the page length as the failure count:\n%s", digest)
	}
}
