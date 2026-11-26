// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

type Pagination struct {
	PageNumber int  `json:"pageNumber"`
	PageSize   int  `json:"pageSize"`
	TotalPages int  `json:"totalPages"`
	Count      int  `json:"count"`
	NextPage   *int `json:"nextPage"`
}

type Meta struct {
	Pagination          Pagination `json:"pagination"`
	Truncated           bool       `json:"truncated,omitempty"`
	WindowActivityCount *int       `json:"windowActivityCount,omitempty"`
}

type Paginated[T any] struct {
	Data []T  `json:"data"`
	Meta Meta `json:"meta"`
}

type Window struct {
	StartDate *string `json:"startDate"`
	EndDate   *string `json:"endDate"`
}

type TaskHealthSummary struct {
	Window                Window         `json:"window"`
	TotalTasks            int            `json:"totalTasks"`
	ByStatus              map[string]int `json:"byStatus"`
	SuccessRate           float64        `json:"successRate"`
	ErrorRate             float64        `json:"errorRate"`
	AvgDurationMs         int64          `json:"avgDurationMs"`
	P50DurationMs         int64          `json:"p50DurationMs"`
	P95DurationMs         int64          `json:"p95DurationMs"`
	DurationMeasuredCount int            `json:"durationMeasuredCount"`
	ActiveBackgroundCount int            `json:"activeBackgroundCount"`
	TaskRetentionDays     int            `json:"taskRetentionDays,omitempty"`
	DurationBucketCounts  []int          `json:"durationBucketCounts,omitempty"`
}

var DurationBucketEdgesMs = []int64{
	100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000, 120000, 300000,
}

type TaskHealthRow struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name,omitempty"`
	Type               string  `json:"type,omitempty"`
	UserID             string  `json:"userId"`
	Status             string  `json:"status"`
	StatusEmoji        string  `json:"statusEmoji,omitempty"`
	StartDate          string  `json:"startDate"`
	EndDate            *string `json:"endDate"`
	DurationMs         *int64  `json:"durationMs"`
	ExecutionMode      string  `json:"executionMode"`
	Kind               string  `json:"kind"`
	InitialRequestText string  `json:"initialRequestText"`
}

type AgentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type AgentPerformanceRow struct {
	Agent                  AgentRef `json:"agent"`
	InvocationCount        int      `json:"invocationCount"`
	ErrorCount             int      `json:"errorCount"`
	SuccessRate            float64  `json:"successRate"`
	AvgDurationMs          int64    `json:"avgDurationMs"`
	P95DurationMs          int64    `json:"p95DurationMs"`
	DurationMeasuredCount  int      `json:"durationMeasuredCount"`
	TotalInputTokens       int64    `json:"totalInputTokens,omitempty"`
	TotalOutputTokens      int64    `json:"totalOutputTokens,omitempty"`
	TotalCachedInputTokens int64    `json:"totalCachedInputTokens,omitempty"`
}

type WorkflowBreakdownRow struct {
	Workflow               AgentRef `json:"workflow"`
	Agent                  AgentRef `json:"agent"`
	InvocationCount        int      `json:"invocationCount"`
	ErrorCount             int      `json:"errorCount"`
	SuccessRate            float64  `json:"successRate"`
	AvgDurationMs          int64    `json:"avgDurationMs"`
	P95DurationMs          int64    `json:"p95DurationMs"`
	TotalInputTokens       int64    `json:"totalInputTokens,omitempty"`
	TotalOutputTokens      int64    `json:"totalOutputTokens,omitempty"`
	TotalCachedInputTokens int64    `json:"totalCachedInputTokens,omitempty"`
}

type TokenUsageRow struct {
	GroupKey               string  `json:"groupKey"`
	PeriodStart            *string `json:"periodStart"`
	TotalInputTokens       int64   `json:"totalInputTokens"`
	TotalOutputTokens      int64   `json:"totalOutputTokens"`
	TotalCachedInputTokens int64   `json:"totalCachedInputTokens"`
	TaskCount              int     `json:"taskCount"`
}

type TaskQueryRow struct {
	ID                string    `json:"id"`
	UserID            string    `json:"userId"`
	Agent             *AgentRef `json:"agent,omitempty"`
	Status            string    `json:"status"`
	StartDate         string    `json:"startDate"`
	EndDate           *string   `json:"endDate"`
	DurationMs        *int64    `json:"durationMs"`
	TotalInputTokens  int64     `json:"totalInputTokens"`
	TotalOutputTokens int64     `json:"totalOutputTokens"`
	TotalCachedTokens int64     `json:"totalCachedInputTokens"`
	ExecutionMode     string    `json:"executionMode"`
	Kind              string    `json:"kind"`
	ParentTaskID      string    `json:"parentTaskId,omitempty"`
}

type TaskMetricGroup struct {
	Agent       *AgentRef `json:"agent,omitempty"`
	UserID      *string   `json:"userId,omitempty"`
	Status      *string   `json:"status,omitempty"`
	Period      string    `json:"period,omitempty"`
	PeriodStart *string   `json:"periodStart,omitempty"`
}

type TaskMetricRow struct {
	Group                  TaskMetricGroup `json:"group"`
	TaskCount              int             `json:"taskCount"`
	ErrorCount             int             `json:"errorCount"`
	SuccessRate            float64         `json:"successRate"`
	AvgDurationMs          int64           `json:"avgDurationMs"`
	P95DurationMs          int64           `json:"p95DurationMs"`
	TotalInputTokens       int64           `json:"totalInputTokens"`
	TotalOutputTokens      int64           `json:"totalOutputTokens"`
	TotalCachedInputTokens int64           `json:"totalCachedInputTokens"`
}

type ScheduledTaskHealth struct {
	TotalEnabled   int                   `json:"totalEnabled"`
	TotalDisabled  int                   `json:"totalDisabled"`
	Executions     map[string]int        `json:"executions"`
	RecentFailures []ScheduledFailureDTO `json:"recentFailures"`
}

type ScheduledFailureDTO struct {
	ScheduledTaskID         string `json:"scheduledTaskId"`
	Name                    string `json:"name"`
	LastError               string `json:"lastError"`
	ConsecutiveFailureCount int    `json:"consecutiveFailureCount"`
}

type FeedbackSummary struct {
	Total          int                  `json:"total"`
	ByRating       map[string]int       `json:"byRating"`
	RecentComments []FeedbackCommentDTO `json:"recentComments"`
}

type FeedbackCommentDTO struct {
	TaskID    string `json:"taskId"`
	Rating    string `json:"rating"`
	Comment   string `json:"comment"`
	UpdatedAt string `json:"updatedAt"`
}

type ActivityFeedbackRow struct {
	ID             string   `json:"id"`
	TaskID         string   `json:"taskId"`
	UserID         string   `json:"userId"`
	Rating         string   `json:"rating"`
	Comment        *string  `json:"comment"`
	Subject        AgentRef `json:"subject"`
	EntrypointType string   `json:"entrypointType"`
	UpdatedAt      string   `json:"updatedAt"`
}

type FeedbackSummaryResponse struct {
	Rows              []FeedbackRollupRow   `json:"rows"`
	NegativeSample    []ActivityFeedbackRow `json:"negativeSample"`
	Candidates        []AgentRef            `json:"candidates,omitempty"`
	UnmatchedSubjects []string              `json:"unmatchedSubjects,omitempty"`
	Truncated         bool                  `json:"truncated,omitempty"`
}

type FeedbackRowsResponse struct {
	Paginated[ActivityFeedbackRow]
	Candidates        []AgentRef `json:"candidates,omitempty"`
	UnmatchedSubjects []string   `json:"unmatchedSubjects,omitempty"`
}

type TraceEventDTO struct {
	CreatedAt string `json:"createdAt"`
	Direction string `json:"direction"`
	Kind      string `json:"kind,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

const maxEventSummaryChars = 200

const (
	maxErrorMessageChars = 1000
	minErrorMessageChars = 200
)

const MaxTreeWalkUpHops = 10

const MaxTreeWalkDownDepth = 3

const MaxTreeTasks = 100

const DefaultTreeEventsPerTask = 30

const MaxTreeTotalEvents = 1000

const maxTreeBytes = 95000

type TaskTreeResponse struct {
	FeedbackID       string        `json:"feedbackId,omitempty"`
	QueriedTaskID    string        `json:"queriedTaskId"`
	Root             TreeNodeDTO   `json:"root"`
	Tree             []TreeNodeDTO `json:"tree"`
	TreeEventsTotal  int           `json:"treeEventsTotal"`
	TreeEventsCapped bool          `json:"treeEventsCapped,omitempty"`
	TreeCapped       bool          `json:"treeCapped,omitempty"`
	Owner            string        `json:"-"`
}

type TreeNodeDTO struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name,omitempty"`
	Type                   string          `json:"type,omitempty"`
	Status                 string          `json:"status,omitempty"`
	StatusEmoji            string          `json:"statusEmoji,omitempty"`
	ErrorMessage           string          `json:"errorMessage,omitempty"`
	ParentTaskID           string          `json:"parentTaskId,omitempty"`
	StartDate              string          `json:"startDate"`
	EndDate                string          `json:"endDate,omitempty"`
	DurationMs             *int64          `json:"durationMs,omitempty"`
	HideDuration           bool            `json:"hideDuration,omitempty"`
	Depth                  int             `json:"depth"`
	IsQueried              bool            `json:"isQueried,omitempty"`
	Events                 []TraceEventDTO `json:"events,omitempty"`
	EventsTotal            int             `json:"eventsTotal,omitempty"`
	EventsCapped           bool            `json:"eventsCapped,omitempty"`
	EventsOmitted          bool            `json:"eventsOmitted,omitempty"`
	TotalInputTokens       int64           `json:"totalInputTokens,omitempty"`
	TotalOutputTokens      int64           `json:"totalOutputTokens,omitempty"`
	TotalCachedInputTokens int64           `json:"totalCachedInputTokens,omitempty"`
}

func statusEmoji(status string) string {
	switch status {
	case "completed":
		return "✅"
	case "timeout", "stale":
		return "⚠️"
	case "failed", "error", "cancelled", "canceled":
		return "❌"
	default:
		return ""
	}
}

const MaxSessionTurns = 20

const SessionRefusalNoAccess = "no_session_access"

const SessionRefusalTooDeep = "session_too_deep"

const maxSessionBytes = maxTreeBytes

const maxSessionCommentRunes = 2000

type SessionTraceResponse struct {
	FeedbackID          string             `json:"feedbackId"`
	Rating              string             `json:"rating"`
	Comment             string             `json:"comment,omitempty"`
	Subject             AgentRef           `json:"subject"`
	RatedTaskID         string             `json:"ratedTaskId"`
	Turns               []TaskTreeResponse `json:"turns"`
	EarlierTurnsOmitted bool               `json:"earlierTurnsOmitted,omitempty"`
	RetentionGap        bool               `json:"retentionGap,omitempty"`
	RatedAt             string             `json:"ratedAt,omitempty"`
	Refusal             string             `json:"refusal,omitempty"`
	Owner               string             `json:"-"`
}
