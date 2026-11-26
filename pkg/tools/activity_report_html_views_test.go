// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestBuildHTMLFailureRowViews_TimeoutRowHidesDuration(t *testing.T) {
	rows := []FailureRow{
		{TaskID: "t-timeout", SubjectName: "DeepNewsScraper", Status: store.TaskStatusJanitorTimeout, DurationMs: 4_304_620, StartTimeIso: "2026-07-22T04:57:44Z"},
		{TaskID: "t-failed", SubjectName: "MarketAnalyst", Status: "failed", DurationMs: 1_500, StartTimeIso: "2026-07-22T05:00:00Z"},
		{TaskID: "t-error", SubjectName: "Orchestrator", Status: "error", DurationMs: 4_200, StartTimeIso: "2026-07-22T05:00:00Z"},
	}
	got, _ := buildHTMLFailureRowViews(rows, len(rows), 10)
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	if got[0].Duration != "—" {
		t.Errorf("timeout row Duration = %q, want \"—\" (janitor-stamped end_time is observation-window noise)", got[0].Duration)
	}
	if got[1].Duration == "—" {
		t.Errorf("failed row Duration must not be dashed, got %q", got[1].Duration)
	}
	if got[2].Duration == "—" {
		t.Errorf("error row Duration must not be dashed, got %q", got[2].Duration)
	}
	if !strings.Contains(got[1].Duration, "1") && !strings.Contains(got[1].Duration, "ms") {
		t.Errorf("failed row Duration should format the real ms value, got %q", got[1].Duration)
	}
}

func TestBuildHTMLFeedbackRowViews_Escapes(t *testing.T) {
	rows := []FeedbackRow{
		{TaskID: "t", UserID: "<img src=x onerror=alert(1)>", Rating: "up", SubjectName: "<b>Bold</b>", EntrypointType: "webui",
			UpdatedAtIso: "2026-07-22T05:00:00Z", Comment: "<script>alert(1)</script>"},
		{TaskID: "t2", Rating: "down", SubjectName: "Agent", EntrypointType: "webui",
			UpdatedAtIso: "2026-07-22T05:00:00Z"},
	}

	got := buildHTMLFeedbackRowViews(rows)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if strings.Contains(got[0].Comment, "<script>") {
		t.Errorf("Comment not escaped: %q", got[0].Comment)
	}
	if strings.Contains(got[0].SubjectName, "<b>") {
		t.Errorf("SubjectName not escaped: %q", got[0].SubjectName)
	}
	if strings.Contains(got[0].UserID, "<img") {
		t.Errorf("UserID not escaped: %q", got[0].UserID)
	}
	if got[1].UserID != "—" {
		t.Errorf("empty UserID = %q, want the em-dash fallback", got[1].UserID)
	}
}

func TestBuildHTMLFeedbackRowViews_TruncatesCommentWithFullTooltip(t *testing.T) {
	long := strings.Repeat("a", 250)
	rows := []FeedbackRow{{TaskID: "t", Rating: "up", Comment: long}}

	got := buildHTMLFeedbackRowViews(rows)
	if n := utf8.RuneCountInString(got[0].Comment); n > 100 {
		t.Errorf("Comment rune count = %d, want <= 100 (truncated)", n)
	}
	if !strings.HasSuffix(got[0].Comment, "…") {
		t.Errorf("truncated Comment missing ellipsis: %q", got[0].Comment)
	}
	if got[0].CommentFull != long {
		t.Errorf("CommentFull = %q, want the untruncated comment", got[0].CommentFull)
	}
}

func TestBuildHTMLFeedbackRowViews_Empty(t *testing.T) {
	got := buildHTMLFeedbackRowViews(nil)
	if got != nil {
		t.Errorf("empty input = %v, want nil", got)
	}
}

func TestBuildHTMLTopUserRowViews_TotalTokensSumsInputAndOutput(t *testing.T) {
	block := TopUsersBlock{Available: true, Rows: []TopUserRow{
		{UserID: "alice", TaskCount: 5, TotalInputTokens: 1234, TotalOutputTokens: 5678},
	}}
	got := buildHTMLTopUserRowViews(block)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if want := "6,912"; got[0].TotalTokens != want {
		t.Errorf("TotalTokens = %q, want %q (1234+5678, comma-formatted)", got[0].TotalTokens, want)
	}
}

func TestBuildHTMLTopUserRowViews_Unavailable(t *testing.T) {
	got := buildHTMLTopUserRowViews(TopUsersBlock{Available: false, Rows: []TopUserRow{{UserID: "alice"}}})
	if got != nil {
		t.Errorf("unavailable block = %v, want nil", got)
	}
}

func TestBuildHTMLSubjectRowViews_P95GatesOnMeasuredCount(t *testing.T) {
	rows := []AgentRow{
		{SubjectName: "HighVolLowMeasured", InvocationCount: 100, DurationMeasuredCount: 3, P95DurationMs: 1_234},
		{SubjectName: "EnoughSamples", InvocationCount: 100, DurationMeasuredCount: 10, P95DurationMs: 5_678},
	}
	got := buildHTMLSubjectRowViews(rows)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].P95 != "—" {
		t.Errorf("row 0 P95 = %q, want \"—\" (measured=3 below threshold=%d)", got[0].P95, smallSamplePercentileThreshold)
	}
	if got[1].P95 == "—" {
		t.Errorf("row 1 P95 should render a value (measured=10 ≥ threshold=%d), got \"—\"", smallSamplePercentileThreshold)
	}
}
