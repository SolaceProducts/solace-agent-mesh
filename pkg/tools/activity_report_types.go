// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

const ActivityReportType = "activity_report"

const ActivityReportSchemaVersion = 1

type ActivityReport struct {
	Type              string                 `json:"type"`
	SchemaVersion     int                    `json:"schemaVersion"`
	Scope             string                 `json:"scope"`
	Window            Window                 `json:"window"`
	Verdict           Verdict                `json:"verdict"`
	KPIs              []KPITile              `json:"kpis"`
	StatusBreakdown   StatusBreakdownSection `json:"statusBreakdown"`
	DurationDist      DurationDistSection    `json:"durationDist"`
	TokenTrend        *TokenTrendSection     `json:"tokenTrend,omitempty"`
	ByAgent           ByAgentSection         `json:"byAgent"`
	ByWorkflow        ByWorkflowSection      `json:"byWorkflow"`
	TopUsers          TopUsersBlock          `json:"topUsers"`
	RecentFailures    []FailureRow           `json:"recentFailures"`
	FailuresTotal     int                    `json:"failuresTotal,omitempty"`
	Feedback          []FeedbackRow          `json:"feedback,omitempty"`
	FeedbackTruncated bool                   `json:"feedbackTruncated,omitempty"`
	RetentionDays     int                    `json:"retentionDays,omitempty"`
}

type Window struct {
	StartTimeIso string `json:"startTimeIso"`
	EndTimeIso   string `json:"endTimeIso"`
	Label        string `json:"label"`
	WindowKey    string `json:"windowKey,omitempty"`
}

type VerdictSeverity string

const (
	VerdictNoData   VerdictSeverity = "no_data"
	VerdictOk       VerdictSeverity = "ok"
	VerdictDegraded VerdictSeverity = "degraded"
	VerdictFailed   VerdictSeverity = "failed"
	VerdictCritical VerdictSeverity = "critical"
)

type Verdict struct {
	Severity VerdictSeverity `json:"severity"`
	Label    string          `json:"label"`
	Headline string          `json:"headline"`
}

type KPITile struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Value    string   `json:"value"`
	Sub      string   `json:"sub,omitempty"`
	DeltaPct *float64 `json:"deltaPct,omitempty"`
}

const (
	KPITileTotalTasks            = "totalTasks"
	KPITileSuccessRate           = "successRate"
	KPITileFailureCount          = "failureCount"
	KPITileP95DurationMs         = "p95DurationMs"
	KPITileActiveBackgroundCount = "activeBackgroundCount"
)

type StatusCountRow struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type StatusBreakdownSection struct {
	Rows        []StatusCountRow `json:"rows"`
	ArtifactRef *ArtifactRef     `json:"artifactRef,omitempty"`
}

type DurationBucket struct {
	LowerMs int64 `json:"lowerMs"`
	UpperMs int64 `json:"upperMs"`
	Count   int   `json:"count"`
}

type DurationDistSection struct {
	Buckets     []DurationBucket `json:"buckets"`
	ArtifactRef *ArtifactRef     `json:"artifactRef,omitempty"`
}

type TokenTrendSection struct {
	ChartType   string          `json:"chartType"`
	Series      []string        `json:"series"`
	Rows        []TokenTrendRow `json:"rows"`
	ArtifactRef *ArtifactRef    `json:"artifactRef,omitempty"`
}

type TokenTrendRow struct {
	DateIso   string `json:"dateIso"`
	Input     int64  `json:"input"`
	Output    int64  `json:"output"`
	Cached    int64  `json:"cached"`
	TaskCount int    `json:"taskCount"`
}

type ArtifactRef struct {
	Filename string `json:"filename"`
}

type AgentRow struct {
	SubjectName            string             `json:"subjectName"`
	InvocationCount        int                `json:"invocationCount"`
	ErrorCount             int                `json:"errorCount"`
	SuccessRate            float64            `json:"successRate"`
	P95DurationMs          int64              `json:"p95DurationMs"`
	DurationMeasuredCount  int                `json:"durationMeasuredCount"`
	TotalInputTokens       int64              `json:"totalInputTokens"`
	TotalOutputTokens      int64              `json:"totalOutputTokens"`
	TotalCachedInputTokens int64              `json:"totalCachedInputTokens"`
	NestedHandlers         []NestedHandlerRow `json:"nestedHandlers,omitempty"`
}

type NestedHandlerRow struct {
	Name                   string  `json:"name"`
	Type                   string  `json:"type"`
	InvocationCount        int     `json:"invocationCount"`
	ErrorCount             int     `json:"errorCount"`
	SuccessRate            float64 `json:"successRate"`
	TotalInputTokens       int64   `json:"totalInputTokens"`
	TotalOutputTokens      int64   `json:"totalOutputTokens"`
	TotalCachedInputTokens int64   `json:"totalCachedInputTokens"`
}

type ByAgentSection struct {
	Rows        []AgentRow   `json:"rows"`
	ArtifactRef *ArtifactRef `json:"artifactRef,omitempty"`
}

type ByWorkflowSection struct {
	Rows        []AgentRow   `json:"rows"`
	ArtifactRef *ArtifactRef `json:"artifactRef,omitempty"`
}

const (
	ActivityScopeReadOwn = "read_own"
	ActivityScopeReadAll = "read_all"
)

type TopUsersUnavailableReason string

const (
	TopUsersUnavailableScopeReadOwn TopUsersUnavailableReason = "scope_read_own"
)

type TopUsersBlock struct {
	Available bool                      `json:"available"`
	Reason    TopUsersUnavailableReason `json:"reason,omitempty"`
	Rows      []TopUserRow              `json:"rows,omitempty"`
	Truncated bool                      `json:"truncated,omitempty"`
}

type TopUserRow struct {
	UserID            string `json:"userId"`
	TaskCount         int    `json:"taskCount"`
	TotalInputTokens  int64  `json:"totalInputTokens"`
	TotalOutputTokens int64  `json:"totalOutputTokens"`
}

type FailureRow struct {
	TaskID             string `json:"taskId"`
	SubjectName        string `json:"subjectName"`
	UserID             string `json:"userId"`
	Status             string `json:"status"`
	StartTimeIso       string `json:"startTimeIso"`
	DurationMs         int64  `json:"durationMs"`
	InitialRequestText string `json:"initialRequestText"`
}

type FeedbackRow struct {
	TaskID         string `json:"taskId"`
	UserID         string `json:"userId"`
	Rating         string `json:"rating"`
	Comment        string `json:"comment"`
	SubjectName    string `json:"subjectName"`
	EntrypointType string `json:"entrypointType"`
	UpdatedAtIso   string `json:"updatedAtIso"`
}
