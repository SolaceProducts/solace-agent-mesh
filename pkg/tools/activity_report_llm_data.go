// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

type ActivityReportLLMData struct {
	Type              string           `json:"type"`
	SchemaVersion     int              `json:"schemaVersion"`
	Scope             string           `json:"scope"`
	Window            Window           `json:"window"`
	Verdict           Verdict          `json:"verdict"`
	KPIs              []KPITile        `json:"kpis"`
	StatusCounts      []StatusCountRow `json:"statusCounts"`
	DurationBuckets   []DurationBucket `json:"durationBuckets"`
	TokenTrend        []TokenTrendRow  `json:"tokenTrend,omitempty"`
	ByAgent           []LLMAgentRow    `json:"byAgent"`
	ByWorkflow        []LLMAgentRow    `json:"byWorkflow"`
	TopUsers          TopUsersBlock    `json:"topUsers"`
	RecentFailures    []LLMFailureRow  `json:"recentFailures"`
	FailuresTotal     int              `json:"failuresTotal,omitempty"`
	Feedback          []FeedbackRow    `json:"feedback"`
	FeedbackTruncated bool             `json:"feedbackTruncated,omitempty"`
	RetentionDays     int              `json:"retentionDays,omitempty"`
}

type LLMAgentRow struct {
	SubjectName            string  `json:"subjectName"`
	InvocationCount        int     `json:"invocationCount"`
	ErrorCount             int     `json:"errorCount"`
	SuccessRate            float64 `json:"successRate"`
	P95DurationMs          *int64  `json:"p95DurationMs,omitempty"`
	DurationMeasuredCount  int     `json:"durationMeasuredCount"`
	TotalInputTokens       int64   `json:"totalInputTokens"`
	TotalOutputTokens      int64   `json:"totalOutputTokens"`
	TotalCachedInputTokens int64   `json:"totalCachedInputTokens"`
}

type LLMFailureRow struct {
	TaskID       string `json:"taskId"`
	SubjectName  string `json:"subjectName"`
	UserID       string `json:"userId"`
	Status       string `json:"status"`
	StartTimeIso string `json:"startTimeIso"`
	DurationMs   int64  `json:"durationMs"`
}

func toLLMAgentRows(rows []AgentRow) []LLMAgentRow {
	out := make([]LLMAgentRow, 0, len(rows))
	for _, r := range rows {
		row := LLMAgentRow{
			SubjectName:            r.SubjectName,
			InvocationCount:        r.InvocationCount,
			ErrorCount:             r.ErrorCount,
			SuccessRate:            r.SuccessRate,
			DurationMeasuredCount:  r.DurationMeasuredCount,
			TotalInputTokens:       r.TotalInputTokens,
			TotalOutputTokens:      r.TotalOutputTokens,
			TotalCachedInputTokens: r.TotalCachedInputTokens,
		}
		if r.DurationMeasuredCount >= smallSamplePercentileThreshold {
			p95 := r.P95DurationMs
			row.P95DurationMs = &p95
		}
		out = append(out, row)
	}
	return out
}

func buildActivityReportLLMData(report *ActivityReport) *ActivityReportLLMData {
	failures := make([]LLMFailureRow, 0, len(report.RecentFailures))
	for _, f := range report.RecentFailures {
		failures = append(failures, LLMFailureRow{
			TaskID:       f.TaskID,
			SubjectName:  f.SubjectName,
			UserID:       f.UserID,
			Status:       f.Status,
			StartTimeIso: f.StartTimeIso,
			DurationMs:   f.DurationMs,
		})
	}
	var tokenTrend []TokenTrendRow
	if report.TokenTrend != nil {
		tokenTrend = report.TokenTrend.Rows
	}
	return &ActivityReportLLMData{
		Type:              report.Type,
		SchemaVersion:     report.SchemaVersion,
		Scope:             report.Scope,
		Window:            report.Window,
		Verdict:           report.Verdict,
		KPIs:              report.KPIs,
		StatusCounts:      report.StatusBreakdown.Rows,
		DurationBuckets:   report.DurationDist.Buckets,
		TokenTrend:        tokenTrend,
		ByAgent:           toLLMAgentRows(report.ByAgent.Rows),
		ByWorkflow:        toLLMAgentRows(report.ByWorkflow.Rows),
		TopUsers:          report.TopUsers,
		RecentFailures:    failures,
		FailuresTotal:     report.FailuresTotal,
		Feedback:          report.Feedback,
		FeedbackTruncated: report.FeedbackTruncated,
		RetentionDays:     report.RetentionDays,
	}
}
