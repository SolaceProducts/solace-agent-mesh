// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
)

const DefaultActivityReportWindow = 24 * time.Hour

const smallSamplePercentileThreshold = 10

const windowSlugFormat = "%s_to_%s"

func parseWindowArgsWithKey(windowKeyArg, startDateArg, endDateArg string, now time.Time) (Window, error) {
	now = now.UTC()

	if windowKeyArg != "" {
		dur, err := analytics.ParseWindowDuration(windowKeyArg)
		if err != nil {
			return Window{}, err
		}
		start := now.Add(-dur)
		return Window{
			StartTimeIso: start.Format(time.RFC3339),
			EndTimeIso:   now.Format(time.RFC3339),
			Label:        formatWindowLabel(start, now),
			WindowKey:    windowKeyArg,
		}, nil
	}

	var (
		start, end time.Time
		err        error
	)

	if endDateArg == "" {
		end = now
	} else if end, err = time.Parse(time.RFC3339, endDateArg); err != nil {
		return Window{}, fmt.Errorf("parse endDate %q: %w (expected ISO 8601, e.g. 2026-06-26T12:00:00Z)", endDateArg, err)
	}
	end = end.UTC()
	if end.After(now) {
		end = now
	}

	if startDateArg == "" {
		start = end.Add(-DefaultActivityReportWindow)
	} else if start, err = time.Parse(time.RFC3339, startDateArg); err != nil {
		return Window{}, fmt.Errorf("parse startDate %q: %w (expected ISO 8601, e.g. 2026-06-19T12:00:00Z)", startDateArg, err)
	}
	start = start.UTC()

	if !start.Before(end) {
		return Window{}, fmt.Errorf("startDate (%s) must be strictly before endDate (%s)", start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	if end.Sub(start) > analytics.MaxWindow {
		return Window{}, fmt.Errorf("window of %s exceeds the 90-day maximum — narrow the range and retry", end.Sub(start).Round(time.Hour))
	}

	return Window{
		StartTimeIso: start.Format(time.RFC3339),
		EndTimeIso:   end.Format(time.RFC3339),
		Label:        formatWindowLabel(start, end),
	}, nil
}

func formatWindowLabel(start, end time.Time) string {
	dur := end.Sub(start)
	days := int(dur.Round(24*time.Hour) / (24 * time.Hour))
	switch {
	case days == 1:
		return fmt.Sprintf("%s → %s (last 24h)", start.Format("Jan 2"), end.Format("Jan 2"))
	case days >= 1:
		return fmt.Sprintf("%s → %s (%d days)", start.Format("Jan 2"), end.Format("Jan 2"), days)
	}
	return fmt.Sprintf("%s → %s", start.Format("Jan 2 15:04"), end.Format("Jan 2 15:04"))
}

func windowSpansMultipleDays(w Window) bool {
	start, err := time.Parse(time.RFC3339, w.StartTimeIso)
	if err != nil {
		return false
	}
	end, err := time.Parse(time.RFC3339, w.EndTimeIso)
	if err != nil {
		return false
	}
	return end.Sub(start) > 24*time.Hour
}

func priorWindow(current Window) (Window, error) {
	start, err := time.Parse(time.RFC3339, current.StartTimeIso)
	if err != nil {
		return Window{}, fmt.Errorf("priorWindow: parse start %q: %w", current.StartTimeIso, err)
	}
	end, err := time.Parse(time.RFC3339, current.EndTimeIso)
	if err != nil {
		return Window{}, fmt.Errorf("priorWindow: parse end %q: %w", current.EndTimeIso, err)
	}
	length := end.Sub(start)
	return Window{
		StartTimeIso: start.Add(-length).Format(time.RFC3339),
		EndTimeIso:   start.Format(time.RFC3339),
	}, nil
}

func deltaPct(curr, prev float64) *float64 {
	if prev == 0 {
		return nil
	}
	d := (curr - prev) / prev * 100
	return &d
}

func bucketLabel(b DurationBucket) string {
	if b.LowerMs == 0 {
		return fmt.Sprintf("≤%s", formatDurationShort(b.UpperMs))
	}
	if b.UpperMs == -1 {
		return fmt.Sprintf("≥%s", formatDurationShort(b.LowerMs))
	}
	return fmt.Sprintf("%s–%s", formatDurationShort(b.LowerMs), formatDurationShort(b.UpperMs))
}

func formatDurationShort(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm", s/60)
}

type populations struct {
	total      int
	terminal   int
	completed  int
	failures   int
	inProgress int
	stale      int
	other      int
}

func populationsOf(h *taskHealthResp) populations {
	p := populations{
		total:      h.TotalTasks,
		completed:  h.ByStatus["completed"],
		failures:   totalFailureCount(h),
		inProgress: inProgressTaskCount(h),
		stale:      staleTaskCount(h),
	}
	p.terminal = p.total - p.inProgress - p.stale
	p.other = p.terminal - p.completed - p.failures
	return p
}

func computeVerdictSeverity(p populations) VerdictSeverity {
	if p.terminal == 0 {
		return VerdictNoData
	}
	if p.failures == 0 {
		return VerdictOk
	}
	if p.terminal == p.failures {
		return VerdictCritical
	}
	rate := float64(p.failures) / float64(p.terminal)
	switch {
	case rate <= 0.15:
		return VerdictDegraded
	case rate <= 0.50:
		return VerdictFailed
	default:
		return VerdictCritical
	}
}

func reportFilenameStem(prefix string, window Window, scope string, filters activityFilters) string {
	stem := prefix + "_" + reportWindowStem(window)
	if filters.TargetUserID != "" {
		stem += "_user-" + sanitizeFilenameSegment(filters.TargetUserID)
	} else if effectiveScopeChipKey(scope, filters) == "all" {
		stem += "_all"
	}
	return stem
}

func reportWindowStem(window Window) string {
	start, errS := time.Parse(time.RFC3339, window.StartTimeIso)
	end, errE := time.Parse(time.RFC3339, window.EndTimeIso)
	if errS != nil || errE != nil {
		safe := func(s string) string {
			s = strings.ReplaceAll(s, ":", "-")
			s = strings.ReplaceAll(s, ".", "-")
			return s
		}
		return fmt.Sprintf(windowSlugFormat, safe(window.StartTimeIso), safe(window.EndTimeIso))
	}
	if end.Sub(start) >= 24*time.Hour {
		return fmt.Sprintf(windowSlugFormat, start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
	return fmt.Sprintf(windowSlugFormat, start.Format("2006-01-02T15-04"), end.Format("2006-01-02T15-04"))
}

func sanitizeFilenameSegment(s string) string {
	const maxLen = 48
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= maxLen {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func composeActivityReport(
	scope string,
	window Window,
	health *taskHealthResp,
	priorHealth *taskHealthResp,
	agentPerf []agentPerfResp,
	workflowPerf []agentPerfResp,
	workflowBreakdown []workflowBreakdownResp,
	failures []failedTaskResp,
	failuresTotal int,
	tokenTrend []tokenUsageRow,
	topUsers []tokenUsageRow,
	topUsersTruncated bool,
	multiDay bool,
	feedback []feedbackResp,
	feedbackTruncated bool,
) *ActivityReport {
	pop := populationsOf(health)
	kpis := buildKPIs(health, priorHealth, pop)
	severity := computeVerdictSeverity(pop)
	verdict := Verdict{
		Severity: severity,
		Label:    verdictLabelFor(severity, pop),
		Headline: composeHeadline(health, pop),
	}
	statusRows := buildStatusRows(health)
	agentRows := buildAgentRows(agentPerf)
	workflowRows := buildAgentRows(workflowPerf)
	attachNestedHandlers(workflowRows, workflowBreakdown)

	return &ActivityReport{
		Type:              ActivityReportType,
		SchemaVersion:     ActivityReportSchemaVersion,
		Scope:             scope,
		Window:            window,
		Verdict:           verdict,
		KPIs:              kpis,
		StatusBreakdown:   StatusBreakdownSection{Rows: statusRows},
		DurationDist:      DurationDistSection{Buckets: bucketsFromCounts(health.DurationBucketCounts)},
		TokenTrend:        buildTokenTrendSection(multiDay, tokenTrend),
		ByAgent:           ByAgentSection{Rows: agentRows},
		ByWorkflow:        ByWorkflowSection{Rows: workflowRows},
		TopUsers:          buildTopUsersBlock(scope, topUsers, topUsersTruncated),
		RecentFailures:    buildFailureRows(failures),
		FailuresTotal:     failuresTotal,
		Feedback:          buildFeedbackRows(feedback),
		FeedbackTruncated: feedbackTruncated,
		RetentionDays:     health.TaskRetentionDays,
	}
}

func bucketsFromCounts(counts []int) []DurationBucket {
	edges := analytics.DurationBucketEdgesMs
	buckets := make([]DurationBucket, 0, len(edges)+1)
	var lower int64
	for i, upper := range edges {
		c := 0
		if i < len(counts) {
			c = counts[i]
		}
		buckets = append(buckets, DurationBucket{LowerMs: lower, UpperMs: upper, Count: c})
		lower = upper
	}
	topCount := 0
	if len(counts) == len(edges)+1 {
		topCount = counts[len(edges)]
	}
	buckets = append(buckets, DurationBucket{LowerMs: lower, UpperMs: -1, Count: topCount})
	return buckets
}

func buildTokenTrendSection(multiDay bool, rows []tokenUsageRow) *TokenTrendSection {
	if !multiDay {
		return nil
	}
	out := make([]TokenTrendRow, 0, len(rows))
	for _, r := range rows {
		date := r.GroupKey
		if r.PeriodStart != nil && *r.PeriodStart != "" {
			date = stripTimeOfDay(*r.PeriodStart)
		}
		out = append(out, TokenTrendRow{
			DateIso:   date,
			Input:     r.TotalInputTokens,
			Output:    r.TotalOutputTokens,
			Cached:    r.TotalCachedInputTokens,
			TaskCount: r.TaskCount,
		})
	}
	return &TokenTrendSection{
		ChartType: "stacked_bar",
		Series:    []string{"input", "output", "cached"},
		Rows:      out,
	}
}

func buildTopUsersBlock(scope string, rows []tokenUsageRow, truncated bool) TopUsersBlock {
	if scope != ActivityScopeReadAll {
		return TopUsersBlock{Available: false, Reason: TopUsersUnavailableScopeReadOwn}
	}
	out := make([]TopUserRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, TopUserRow{
			UserID:            r.GroupKey,
			TaskCount:         r.TaskCount,
			TotalInputTokens:  r.TotalInputTokens,
			TotalOutputTokens: r.TotalOutputTokens,
		})
	}
	return TopUsersBlock{Available: true, Rows: out, Truncated: truncated}
}

func buildKPIs(h, prior *taskHealthResp, p populations) []KPITile {
	failures := p.failures
	p95Value, p95Sub, p95HasStat := p95TileValue(h)
	successValue := pct(h.SuccessRate)
	successSub := ""
	if p.terminal == 0 {
		successValue = "—"
	} else if p.terminal != p.total {
		successSub = fmt.Sprintf("of %s finished", commaInt(p.terminal))
	}
	tiles := []KPITile{
		{ID: KPITileTotalTasks, Label: "Total tasks", Value: commaInt(h.TotalTasks)},
		{ID: KPITileSuccessRate, Label: "Success rate", Value: successValue, Sub: successSub},
		{ID: KPITileFailureCount, Label: "Failures", Value: commaInt(failures)},
		{ID: KPITileP95DurationMs, Label: "p95 duration", Value: p95Value, Sub: p95Sub},
		{ID: KPITileActiveBackgroundCount, Label: "Active background", Value: commaInt(h.ActiveBackgroundCount)},
	}
	if prior != nil {
		priorFailures := totalFailureCount(prior)
		tiles[0].DeltaPct = deltaPct(float64(h.TotalTasks), float64(prior.TotalTasks))
		tiles[1].DeltaPct = deltaPct(h.SuccessRate, prior.SuccessRate)
		tiles[2].DeltaPct = deltaPct(float64(failures), float64(priorFailures))
		if p95HasStat && prior.DurationMeasuredCount >= smallSamplePercentileThreshold {
			tiles[3].DeltaPct = deltaPct(float64(h.P95DurationMs), float64(prior.P95DurationMs))
		}
	}
	return tiles
}

func p95TileValue(h *taskHealthResp) (value, sub string, hasStat bool) {
	measured := h.DurationMeasuredCount
	if measured < smallSamplePercentileThreshold {
		return "—", fmt.Sprintf("insufficient data (%d of %d)", measured, h.TotalTasks), false
	}
	return fmt.Sprintf("%sms", commaInt64(h.P95DurationMs)), "", true
}

func composeHeadline(h *taskHealthResp, p populations) string {
	if p.total == 0 {
		return "No tasks in this window."
	}
	if p.terminal == 0 {
		return composeNoTerminalHeadline(p.inProgress, p.stale)
	}
	if p.failures == 0 && p.other == 0 {
		if p.inProgress+p.stale > 0 {
			return fmt.Sprintf("success rate %s over %s finished tasks (%s).",
				pct(h.SuccessRate), commaInt(p.terminal), openWorkPhrase(p.inProgress, p.stale))
		}
		return fmt.Sprintf("success rate %s over %s tasks.", pct(h.SuccessRate), commaInt(p.terminal))
	}
	parts := make([]string, 0, 3)
	if p.failures > 0 {
		parts = append(parts, fmt.Sprintf("%s failed", commaInt(p.failures)))
	}
	if p.other > 0 {
		parts = append(parts, fmt.Sprintf("%s other outcomes", commaInt(p.other)))
	}
	if phrase := openWorkPhrase(p.inProgress, p.stale); phrase != "" {
		parts = append(parts, phrase)
	}
	return fmt.Sprintf("%s completed across %s finished tasks (%s).",
		commaInt(p.completed), commaInt(p.terminal), strings.Join(parts, ", "))
}

func openWorkPhrase(inProgress, stale int) string {
	parts := make([]string, 0, 2)
	if inProgress > 0 {
		parts = append(parts, fmt.Sprintf("%s still running", commaInt(inProgress)))
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("%s stale", commaInt(stale)))
	}
	return strings.Join(parts, ", ")
}

func composeNoTerminalHeadline(inProgress, stale int) string {
	phrase := openWorkPhrase(inProgress, stale)
	if phrase == "" {
		return "no terminal outcomes in this window."
	}
	return fmt.Sprintf("no terminal outcomes yet — %s.", phrase)
}

func buildStatusRows(h *taskHealthResp) []StatusCountRow {
	order := []string{"completed", "in_progress", "failed", "error", "timeout", "stale", "canceled", "running", "unknown"}
	out := make([]StatusCountRow, 0, len(h.ByStatus))
	seen := map[string]bool{}
	for _, s := range order {
		if c, ok := h.ByStatus[s]; ok && c > 0 {
			out = append(out, StatusCountRow{Status: s, Count: c})
			seen[s] = true
		}
	}
	for s, c := range h.ByStatus {
		if !seen[s] && c > 0 {
			out = append(out, StatusCountRow{Status: s, Count: c})
		}
	}
	return out
}

func buildAgentRows(perf []agentPerfResp) []AgentRow {
	const cap = 10
	if len(perf) > cap {
		perf = perf[:cap]
	}
	out := make([]AgentRow, 0, len(perf))
	for _, p := range perf {
		out = append(out, AgentRow{
			SubjectName:            p.Agent.Name,
			InvocationCount:        p.InvocationCount,
			ErrorCount:             p.ErrorCount,
			SuccessRate:            p.SuccessRate,
			P95DurationMs:          p.P95DurationMs,
			DurationMeasuredCount:  p.DurationMeasuredCount,
			TotalInputTokens:       p.TotalInputTokens,
			TotalOutputTokens:      p.TotalOutputTokens,
			TotalCachedInputTokens: p.TotalCachedInputTokens,
		})
	}
	return out
}

func attachNestedHandlers(workflowRows []AgentRow, breakdown []workflowBreakdownResp) {
	if len(workflowRows) == 0 || len(breakdown) == 0 {
		return
	}
	byWorkflow := make(map[string][]NestedHandlerRow, len(workflowRows))
	for _, b := range breakdown {
		byWorkflow[b.Workflow.Name] = append(byWorkflow[b.Workflow.Name], NestedHandlerRow{
			Name:                   b.Agent.Name,
			Type:                   b.Agent.Type,
			InvocationCount:        b.InvocationCount,
			ErrorCount:             b.ErrorCount,
			SuccessRate:            b.SuccessRate,
			TotalInputTokens:       b.TotalInputTokens,
			TotalOutputTokens:      b.TotalOutputTokens,
			TotalCachedInputTokens: b.TotalCachedInputTokens,
		})
	}
	for i := range workflowRows {
		if handlers, ok := byWorkflow[workflowRows[i].SubjectName]; ok {
			workflowRows[i].NestedHandlers = handlers
		}
	}
}

func totalFailureCount(h *taskHealthResp) int {
	return h.ByStatus["failed"] + h.ByStatus["error"] + h.ByStatus["timeout"]
}

func inProgressTaskCount(h *taskHealthResp) int {
	return h.ByStatus["in_progress"]
}

func staleTaskCount(h *taskHealthResp) int {
	return h.ByStatus["stale"]
}

func buildFailureRows(failures []failedTaskResp) []FailureRow {
	out := make([]FailureRow, 0, len(failures))
	for _, f := range failures {
		dur := int64(0)
		if f.DurationMs != nil {
			dur = *f.DurationMs
		}
		out = append(out, FailureRow{
			TaskID:             f.ID,
			SubjectName:        f.Name,
			UserID:             f.UserID,
			Status:             f.Status,
			StartTimeIso:       f.StartDate,
			DurationMs:         dur,
			InitialRequestText: f.InitialRequestText,
		})
	}
	return out
}

func buildFeedbackRows(rows []feedbackResp) []FeedbackRow {
	out := make([]FeedbackRow, 0, len(rows))
	for _, r := range rows {
		comment := ""
		if r.Comment != nil {
			comment = *r.Comment
		}
		out = append(out, FeedbackRow{
			TaskID:         r.TaskID,
			UserID:         r.UserID,
			Rating:         r.Rating,
			Comment:        comment,
			SubjectName:    r.Subject.Name,
			EntrypointType: r.EntrypointType,
			UpdatedAtIso:   r.UpdatedAt,
		})
	}
	return out
}

func composeChatDigest(report *ActivityReport, filters activityFilters) string {
	var sb strings.Builder

	if isEmptyWindow(report) {
		fmt.Fprintf(&sb, "## 🟦 No activity — %s (%s)\n\n",
			report.Window.Label, effectiveScopeChipLabel(report.Scope, filters))
		sb.WriteString("Full interactive report attached.\n\n")
		if pop := report.openWorkSummary(); pop != "" {
			fmt.Fprintf(&sb, "No tasks finished in this window (%s). Rates need finished tasks, so there is nothing to rate yet.", pop)
		} else {
			sb.WriteString("No tasks started in this window. Try a longer window if you expected activity.")
		}
		return sb.String()
	}

	fmt.Fprintf(&sb, "## Activity Report — %s (%s)\n\n",
		report.Window.Label, effectiveScopeChipLabel(report.Scope, filters))

	sb.WriteString("Full interactive report attached.\n\n")

	if filters.TargetUserID != "" {
		fmt.Fprintf(&sb, "**Filtered to:** user `%s`\n\n", filters.TargetUserID)
	}

	fmt.Fprintf(&sb, "%s **%s** — %s\n\n",
		verdictEmoji(report.Verdict.Severity),
		report.Verdict.Label,
		report.Verdict.Headline)

	sb.WriteString("| Section | Details |\n|---|---|\n")

	totalTasks := lookupKPI(report.KPIs, KPITileTotalTasks)
	successRate := lookupKPI(report.KPIs, KPITileSuccessRate)
	failures := lookupKPI(report.KPIs, KPITileFailureCount)
	p95 := lookupKPI(report.KPIs, KPITileP95DurationMs)
	snapshot := fmt.Sprintf("%s tasks · %s success", totalTasks, successRate)
	if sub := lookupKPISub(report.KPIs, KPITileSuccessRate); sub != "" {
		snapshot += " (" + sub + ")"
	}
	fmt.Fprintf(&sb, "| Snapshot | %s · %s failures · p95 %s |\n", snapshot, failures, p95)

	statusSummary := summarizeStatusRows(report.StatusBreakdown.Rows)
	fmt.Fprintf(&sb, "| Status | %s |\n", statusSummary)

	fmt.Fprintf(&sb, "| By agent | %s |\n", summarizeSubjectRows(report.ByAgent.Rows, "agent"))
	fmt.Fprintf(&sb, "| By workflow | %s |\n", summarizeSubjectRows(report.ByWorkflow.Rows, "workflow"))
	fmt.Fprintf(&sb, "| Feedback | %s |\n", summarizeFeedback(report.Feedback, report.FeedbackTruncated))
	if report.TopUsers.Available && len(report.TopUsers.Rows) > 0 {
		fmt.Fprintf(&sb, "| Users | %s |\n", summarizeTopUsers(report.TopUsers))
	}
	fmt.Fprintf(&sb, "| Recent failures | %s |\n", summarizeFailures(report.RecentFailures, report.FailuresTotal))

	return sb.String()
}

func isEmptyWindow(report *ActivityReport) bool {
	return report.Verdict.Severity == VerdictNoData
}

func verdictLabelFor(s VerdictSeverity, p populations) string {
	if s != VerdictOk {
		return verdictLabel(s)
	}
	switch {
	case p.other > 0:
		return "No failures"
	case p.inProgress+p.stale > 0:
		return "Finished tasks succeeded"
	default:
		return "All succeeded"
	}
}

func summarizeStatusRows(rows []StatusCountRow) string {
	if len(rows) == 0 {
		return "No tasks"
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%d %s", r.Count, displayStatus(r.Status)))
	}
	return strings.Join(parts, " · ")
}

func displayStatus(s string) string {
	if s == "" {
		return s
	}
	if s == "in_progress" {
		return "In progress"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func summarizeSubjectRows(rows []AgentRow, kind string) string {
	if len(rows) == 0 {
		return fmt.Sprintf("No %ss invoked", kind)
	}
	top := rows
	if len(top) > 3 {
		top = top[:3]
	}
	names := make([]string, 0, len(top))
	for _, r := range top {
		names = append(names, fmt.Sprintf("%s (%d)", r.SubjectName, r.InvocationCount))
	}
	prefix := fmt.Sprintf("%d %s", len(rows), kind)
	if len(rows) > 1 {
		prefix += "s"
	}
	return prefix + " · " + strings.Join(names, ", ")
}

func summarizeFeedback(rows []FeedbackRow, truncated bool) string {
	if len(rows) == 0 {
		return "None"
	}
	var up, down, withComment int
	for _, r := range rows {
		switch r.Rating {
		case "up":
			up++
		case "down":
			down++
		}
		if r.Comment != "" {
			withComment++
		}
	}
	summary := fmt.Sprintf("%s total · %s up, %s down (%s with comments)",
		commaInt(len(rows)), commaInt(up), commaInt(down), commaInt(withComment))
	if truncated {
		summary += fmt.Sprintf(" (capped at %s — more may exist)", commaInt(len(rows)))
	}
	return summary
}

func summarizeTopUsers(block TopUsersBlock) string {
	if len(block.Rows) == 0 {
		return "No users"
	}
	var tasks int
	var inputTokens, outputTokens int64
	for _, r := range block.Rows {
		tasks += r.TaskCount
		inputTokens += r.TotalInputTokens
		outputTokens += r.TotalOutputTokens
	}
	summary := fmt.Sprintf("%s users · %s total tasks · %s total input tokens · %s total output tokens",
		commaInt(len(block.Rows)), commaInt(tasks), commaInt64(inputTokens), commaInt64(outputTokens))
	if block.Truncated {
		summary += fmt.Sprintf(" (capped at %s users — more may exist)", commaInt(len(block.Rows)))
	}
	return summary
}

func summarizeFailures(rows []FailureRow, total int) string {
	if total == 0 {
		return "None"
	}
	if total == 1 {
		return "1 failure"
	}
	if len(rows) < total {
		return fmt.Sprintf("%s failures (showing first %d)", commaInt(total), len(rows))
	}
	return fmt.Sprintf("%s failures", commaInt(total))
}

func lookupKPISub(tiles []KPITile, id string) string {
	for _, t := range tiles {
		if t.ID == id {
			return t.Sub
		}
	}
	return ""
}

func lookupKPI(tiles []KPITile, id string) string {
	for _, t := range tiles {
		if t.ID == id {
			return t.Value
		}
	}
	return "—"
}

func effectiveScopeChipKey(scope string, f activityFilters) string {
	if shouldRenderAsTenantScope(scope, f) {
		return "all"
	}
	return "own"
}

func effectiveScopeChipLabel(scope string, f activityFilters) string {
	if shouldRenderAsTenantScope(scope, f) {
		return "Tenant-wide activity"
	}
	return "Your activity"
}

func shouldRenderAsTenantScope(scope string, f activityFilters) bool {
	if scope == ActivityScopeReadAll {
		return true
	}
	if f.TargetUserID != "" && f.TargetUserID != f.CallerUserID {
		return true
	}
	return false
}

func filterChipForUser(userID string) string {
	if userID == "" {
		return ""
	}
	return "User: " + sanitizeChipValue(userID)
}

var activityChipStripper = strings.NewReplacer("{", "", "}", "", "%", "", "«", "", "»", "")

func sanitizeChipValue(s string) string {
	return html.EscapeString(activityChipStripper.Replace(s))
}

func stripTimeOfDay(iso string) string {
	if idx := strings.Index(iso, "T"); idx > 0 {
		return iso[:idx]
	}
	return iso
}

func truncateMid(s string, maxLen, tail int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if tail == 0 || tail >= maxLen-3 {
		return string(runes[:maxLen-1]) + "…"
	}
	head := maxLen - tail - 1
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

var verdictView = map[VerdictSeverity]struct {
	Emoji      string
	Label      string
	AlertClass string
}{
	VerdictNoData:   {"🟦", "No activity", "info"},
	VerdictOk:       {"✅", "All succeeded", "good"},
	VerdictDegraded: {"🟢", "Mostly succeeded", "warn"},
	VerdictFailed:   {"🟡", "Mixed outcomes", "warn"},
	VerdictCritical: {"🔴", "Mostly failed", "danger"},
}

func verdictEmoji(s VerdictSeverity) string { return verdictView[s].Emoji }
func verdictLabel(s VerdictSeverity) string {
	if v, ok := verdictView[s]; ok {
		return v.Label
	}
	return string(s)
}

func commaInt(n int) string {
	return commaInt64(int64(n))
}

func commaInt64(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, r := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, r)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func pct(f float64) string {
	return fmt.Sprintf("%.1f%%", f*100)
}

func (r *ActivityReport) openWorkSummary() string {
	var inProgress, stale int
	for _, row := range r.StatusBreakdown.Rows {
		switch row.Status {
		case "in_progress":
			inProgress = row.Count
		case "stale":
			stale = row.Count
		}
	}
	return openWorkPhrase(inProgress, stale)
}
