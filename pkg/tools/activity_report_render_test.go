// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
)

func TestComputeVerdictSeverity(t *testing.T) {
	tests := []struct {
		name     string
		byStatus map[string]int
		want     VerdictSeverity
	}{
		{"empty window", map[string]int{}, VerdictNoData},
		{"all stale — nothing finished", map[string]int{"stale": 100}, VerdictNoData},
		{"all in_progress — nothing finished", map[string]int{"in_progress": 100}, VerdictNoData},
		{"all completed", map[string]int{"completed": 100}, VerdictOk},
		{"completed plus open work", map[string]int{"completed": 90, "stale": 10}, VerdictOk},
		{"all canceled — no failures (severity OK; label says \"No failures\")", map[string]int{"canceled": 15}, VerdictOk},
		{"completed plus canceled", map[string]int{"completed": 80, "canceled": 20}, VerdictOk},
		{"all failing", map[string]int{"failed": 10}, VerdictCritical},
		{"boundary 15%", map[string]int{"completed": 85, "failed": 15}, VerdictDegraded},
		{"just over 15%", map[string]int{"completed": 84, "failed": 16}, VerdictFailed},
		{"boundary 50%", map[string]int{"completed": 50, "failed": 50}, VerdictFailed},
		{"just over 50%", map[string]int{"completed": 49, "failed": 51}, VerdictCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total := 0
			for _, c := range tt.byStatus {
				total += c
			}
			p := populationsOf(&taskHealthResp{TotalTasks: total, ByStatus: tt.byStatus})
			if got := computeVerdictSeverity(p); got != tt.want {
				t.Errorf("severity(%v) = %q, want %q (terminal=%d completed=%d failures=%d other=%d)",
					tt.byStatus, got, tt.want, p.terminal, p.completed, p.failures, p.other)
			}
		})
	}
}

func TestTotalFailureCount_ExcludesStale(t *testing.T) {
	h := &taskHealthResp{
		TotalTasks: 15,
		ByStatus: map[string]int{
			"completed": 10,
			"stale":     5,
		},
	}
	if got := totalFailureCount(h); got != 0 {
		t.Errorf("totalFailureCount with 10 completed + 5 stale = %d, want 0 (stale is not a failure)", got)
	}

	withReal := &taskHealthResp{
		TotalTasks: 15,
		ByStatus: map[string]int{
			"completed": 8,
			"failed":    1,
			"error":     1,
			"timeout":   1,
			"stale":     4,
		},
	}
	if got := totalFailureCount(withReal); got != 3 {
		t.Errorf("totalFailureCount = %d, want 3 (failed+error+timeout, stale excluded)", got)
	}
}

func TestStaleWindowVerdictAndHeadlineAgree(t *testing.T) {
	h := &taskHealthResp{
		TotalTasks:  15,
		SuccessRate: 100,
		ByStatus: map[string]int{
			"completed": 10,
			"stale":     5,
		},
	}
	p := populationsOf(h)
	if sev := computeVerdictSeverity(p); sev != VerdictOk {
		t.Errorf("verdict severity = %q, want %q (no terminal failures)", sev, VerdictOk)
	}
	if head := composeHeadline(h, p); strings.Contains(head, "fail") {
		t.Errorf("headline mentions failure for a stale-only window: %q", head)
	}
}

func TestAllStaleWindow_NotRenderedAsSucceeded(t *testing.T) {
	health := &taskHealthResp{
		TotalTasks: 8,
		ByStatus:   map[string]int{"stale": 8},
	}
	rpt := composeActivityReport("", Window{}, health, nil, nil, nil, nil, nil, 0, nil, nil, false, false, nil, false)
	if rpt.Verdict.Severity == VerdictOk {
		t.Fatal(`all-stale window rendered as VerdictOk ("all succeeded") — stale counted as success`)
	}
	if rpt.Verdict.Severity != VerdictNoData {
		t.Errorf("verdict severity = %q, want %q (zero terminal tasks)", rpt.Verdict.Severity, VerdictNoData)
	}
}

func TestComposeHeadline_CanceledNotFoldedIntoCompleted(t *testing.T) {
	h := &taskHealthResp{
		TotalTasks: 20,
		ByStatus: map[string]int{
			"completed": 2,
			"canceled":  16,
			"failed":    2,
		},
	}
	head := composeHeadline(h, populationsOf(h))
	if !strings.Contains(head, "2 completed") {
		t.Errorf("headline should report 2 completed (read directly), got %q", head)
	}
	if strings.Contains(head, "18 completed") {
		t.Errorf("headline folded canceled into completed: %q", head)
	}
	if !strings.Contains(head, "16 other outcomes") {
		t.Errorf("headline should surface the 16 non-completed/non-failed tasks as 'other outcomes', got %q", head)
	}
	if !strings.Contains(head, "2 failed") {
		t.Errorf("headline should still report 2 failed, got %q", head)
	}
}

func TestComposeHeadline_StillRunningAndOther(t *testing.T) {
	h := &taskHealthResp{
		TotalTasks: 20,
		ByStatus: map[string]int{
			"completed":   5,
			"canceled":    3,
			"failed":      2,
			"in_progress": 10,
		},
	}
	head := composeHeadline(h, populationsOf(h))
	for _, want := range []string{"5 completed", "10 finished tasks", "2 failed", "3 other outcomes", "10 still running"} {
		if !strings.Contains(head, want) {
			t.Errorf("headline missing %q; got %q", want, head)
		}
	}
}

func TestBucketsFromCounts(t *testing.T) {
	edges := analytics.DurationBucketEdgesMs

	t.Run("counts equal edge count → last bucket count=0", func(t *testing.T) {
		counts := make([]int, len(edges))
		for i := range counts {
			counts[i] = i + 1
		}
		got := bucketsFromCounts(counts)
		if len(got) != len(edges)+1 {
			t.Fatalf("len(buckets) = %d, want %d", len(got), len(edges)+1)
		}
		top := got[len(got)-1]
		if top.UpperMs != -1 {
			t.Errorf("top bucket UpperMs = %d, want -1 (open-ended sentinel)", top.UpperMs)
		}
		if top.Count != 0 {
			t.Errorf("top bucket Count = %d, want 0 (no overflow provided)", top.Count)
		}
	})

	t.Run("counts include overflow → top bucket carries it", func(t *testing.T) {
		counts := make([]int, len(edges)+1)
		counts[len(edges)] = 42
		got := bucketsFromCounts(counts)
		top := got[len(got)-1]
		if top.Count != 42 {
			t.Errorf("top bucket Count = %d, want 42", top.Count)
		}
		if top.UpperMs != -1 {
			t.Errorf("top bucket UpperMs = %d, want -1", top.UpperMs)
		}
	})
}

func TestBuildTopUsersBlock(t *testing.T) {
	rows := []tokenUsageRow{
		{GroupKey: "alice@example.com", TaskCount: 5, TotalInputTokens: 100, TotalOutputTokens: 200},
	}

	t.Run("read_all → available with rows", func(t *testing.T) {
		got := buildTopUsersBlock(ActivityScopeReadAll, rows, false)
		if !got.Available {
			t.Fatalf("expected Available, got %+v", got)
		}
		if len(got.Rows) != 1 || got.Rows[0].UserID != "alice@example.com" {
			t.Errorf("rows = %+v, want alice row", got.Rows)
		}
		if got.Truncated {
			t.Errorf("expected Truncated=false, got true")
		}
	})

	t.Run("read_all → truncated passed through", func(t *testing.T) {
		got := buildTopUsersBlock(ActivityScopeReadAll, rows, true)
		if !got.Truncated {
			t.Errorf("expected Truncated=true, got false")
		}
	})

	t.Run("read_own scope → unavailable(scope_read_own)", func(t *testing.T) {
		got := buildTopUsersBlock(ActivityScopeReadOwn, rows, false)
		if got.Available {
			t.Errorf("expected unavailable under ReadOwn scope; got Available=true")
		}
		if got.Reason != TopUsersUnavailableScopeReadOwn {
			t.Errorf("reason = %q, want %q", got.Reason, TopUsersUnavailableScopeReadOwn)
		}
	})
}

func TestComposeActivityReport_TopUsersWindowIndependent(t *testing.T) {
	h := simulateTaskHealth(map[string]int{"completed": 3})
	topUsers := []tokenUsageRow{
		{GroupKey: "alice@example.com", TaskCount: 5, TotalInputTokens: 100, TotalOutputTokens: 200},
	}
	report := composeActivityReport(ActivityScopeReadAll, Window{Label: "W"}, h, nil,
		nil, nil, nil, nil, 0, nil, topUsers, false, false, nil, false)
	if !report.TopUsers.Available {
		t.Fatalf("TopUsers.Available = false for a single-day read_all report; want true (window-independent), reason=%q", report.TopUsers.Reason)
	}
	if len(report.TopUsers.Rows) != 1 || report.TopUsers.Rows[0].UserID != "alice@example.com" {
		t.Errorf("TopUsers.Rows = %+v, want one alice row", report.TopUsers.Rows)
	}
}

func TestFilterChipEscapesInjection(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) string
		in   string
	}{
		{"user chip <script>", filterChipForUser, `<script>alert(1)</script>`},
		{"user chip ampersand", filterChipForUser, `a&b`},
		{"user chip liquid output", filterChipForUser, `{{ 7*7 }}`},
		{"user chip liquid tag", filterChipForUser, `{% assign x = 1 %}`},
		{"user chip reassembly output", filterChipForUser, `{}}{ 7*7 }{%}`},
		{"user chip reassembly opener", filterChipForUser, `{}}{`},
		{"user chip embed sentinel", filterChipForUser, `«artifact_content:secret.json»`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn(tt.in)
			if got == "" {
				t.Fatal("empty chip for non-empty input")
			}
			if strings.ContainsRune(got, '<') || strings.ContainsRune(got, '>') {
				t.Errorf("chip still carries raw '<' or '>': %q", got)
			}
			for _, r := range []rune{'{', '}', '%', '«', '»'} {
				if strings.ContainsRune(got, r) {
					t.Errorf("chip still carries delimiter/sentinel char %q (could form a Liquid/embed opener at the @@-splice): %q", r, got)
				}
			}
		})
	}
}

func TestReportFilenameStem_DistinguishesVisibility(t *testing.T) {
	win := Window{
		StartTimeIso: "2026-07-30T00:00:00Z",
		EndTimeIso:   "2026-07-31T00:00:00Z",
	}
	own := reportFilenameStem("activity_report", win, ActivityScopeReadOwn, activityFilters{CallerUserID: "admin"})
	self := reportFilenameStem("activity_report", win, ActivityScopeReadAll, activityFilters{CallerUserID: "admin", TargetUserID: "admin"})
	bob := reportFilenameStem("activity_report", win, ActivityScopeReadAll, activityFilters{CallerUserID: "admin", TargetUserID: "bob"})
	tenant := reportFilenameStem("activity_report", win, ActivityScopeReadAll, activityFilters{CallerUserID: "admin"})

	seen := map[string]string{}
	for name, stem := range map[string]string{"own": own, "self-filtered": self, "bob": bob, "tenant-wide": tenant} {
		if prev, dup := seen[stem]; dup {
			t.Errorf("%s and %s alias to the same artifact name %q — opening it without a version serves whichever was written last",
				prev, name, stem)
		}
		seen[stem] = name
	}
	if !strings.Contains(bob, "user-bob") {
		t.Errorf("named-user report should carry the user discriminator, got %q", bob)
	}
	if !strings.Contains(tenant, "_all") {
		t.Errorf("tenant-wide report should be distinguishable from an own-data one, got %q", tenant)
	}
}

func TestSanitizeFilenameSegment(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"alice", "alice"},
		{"user@example.com", "user-example-com"},
		{"../../etc/passwd", "etc-passwd"},
		{"a/b\\c", "a-b-c"},
		{"", "unknown"},
		{"///", "unknown"},
	} {
		if got := sanitizeFilenameSegment(tc.in); got != tc.want {
			t.Errorf("sanitizeFilenameSegment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	long := sanitizeFilenameSegment(strings.Repeat("x", 200))
	if len(long) > 48 {
		t.Errorf("length cap not applied: got %d chars", len(long))
	}
}

func TestTruncateMid_RuneSafeOnMultiByteInput(t *testing.T) {
	s := strings.Repeat("你好世界", 10)
	got := truncateMid(s, 20, 0)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateMid produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated output missing ellipsis: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 20 {
		t.Errorf("rune count = %d, want <= 20", n)
	}
}

func TestSummarizeFeedback(t *testing.T) {
	if got := summarizeFeedback(nil, false); got != "None" {
		t.Errorf("summarizeFeedback(nil, false) = %q, want %q", got, "None")
	}
	rows := []FeedbackRow{
		{Rating: "up", Comment: "nice"},
		{Rating: "up"},
		{Rating: "down", Comment: "bad"},
	}
	got := summarizeFeedback(rows, false)
	for _, want := range []string{"3 total", "2 up", "1 down", "2 with comments"} {
		if !strings.Contains(got, want) {
			t.Errorf("summarizeFeedback(...) = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "capped") {
		t.Errorf("summarizeFeedback(rows, false) mentions capping: %q", got)
	}
}

func TestSummarizeFeedback_Truncated(t *testing.T) {
	rows := []FeedbackRow{{Rating: "up"}, {Rating: "down"}}
	got := summarizeFeedback(rows, true)
	if !strings.Contains(got, "capped") {
		t.Errorf("summarizeFeedback(rows, true) = %q, want a capped/truncation note", got)
	}
}

func TestSummarizeTopUsers(t *testing.T) {
	if got := summarizeTopUsers(TopUsersBlock{}); got != "No users" {
		t.Errorf("summarizeTopUsers(empty) = %q, want %q", got, "No users")
	}
	block := TopUsersBlock{Rows: []TopUserRow{
		{UserID: "alice", TaskCount: 5, TotalInputTokens: 100, TotalOutputTokens: 20},
		{UserID: "bob", TaskCount: 3, TotalInputTokens: 50, TotalOutputTokens: 10},
	}}
	got := summarizeTopUsers(block)
	for _, want := range []string{"2 users", "8 total tasks", "150 total input tokens", "30 total output tokens"} {
		if !strings.Contains(got, want) {
			t.Errorf("summarizeTopUsers(...) = %q, missing %q", got, want)
		}
	}
}

func TestSummarizeTopUsers_Truncated(t *testing.T) {
	block := TopUsersBlock{Rows: []TopUserRow{{UserID: "alice", TaskCount: 1}}, Truncated: true}
	got := summarizeTopUsers(block)
	if !strings.Contains(got, "capped") {
		t.Errorf("summarizeTopUsers(truncated) = %q, want a capped/truncation note", got)
	}
}

func TestComposeChatDigest_IncludesFeedbackRow(t *testing.T) {
	h := simulateTaskHealth(map[string]int{"completed": 1})
	report := composeActivityReport(ActivityScopeReadOwn, Window{Label: "W"}, h, nil,
		nil, nil, nil, nil, 0, nil, nil, false, false,
		[]feedbackResp{{TaskID: "t1", Rating: "up", Subject: agentRefResp{Name: "Agent"}}}, false)

	digest := composeChatDigest(report, activityFilters{})
	if !strings.Contains(digest, "| Feedback | 1 total · 1 up, 0 down (0 with comments) |") {
		t.Errorf("digest missing Feedback row: %s", digest)
	}
}

func TestComposeChatDigest_IncludesUsersRow(t *testing.T) {
	h := simulateTaskHealth(map[string]int{"completed": 1})
	topUsers := []tokenUsageRow{
		{GroupKey: "alice", TaskCount: 5, TotalInputTokens: 100, TotalOutputTokens: 20},
	}
	report := composeActivityReport(ActivityScopeReadAll, Window{Label: "W"}, h, nil,
		nil, nil, nil, nil, 0, nil, topUsers, false, false, nil, false)

	digest := composeChatDigest(report, activityFilters{})
	want := "| Users | 1 users · 5 total tasks · 100 total input tokens · 20 total output tokens |"
	if !strings.Contains(digest, want) {
		t.Errorf("digest missing Users row %q: %s", want, digest)
	}
}

func TestComposeChatDigest_OmitsUsersRowUnderReadOwn(t *testing.T) {
	h := simulateTaskHealth(map[string]int{"completed": 1})
	report := composeActivityReport(ActivityScopeReadOwn, Window{Label: "W"}, h, nil,
		nil, nil, nil, nil, 0, nil, nil, false, false, nil, false)

	digest := composeChatDigest(report, activityFilters{})
	if strings.Contains(digest, "| Users |") {
		t.Errorf("digest should omit the Users row under read-own scope: %s", digest)
	}
}
