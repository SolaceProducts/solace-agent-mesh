// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/liquidsafe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

//go:embed embed/activity_report.html
var activityReportTemplate string

//go:embed embed/assets/chart.umd.min.js
var chartUMDMinJS string

const chartJSInlineMarker = "<script>/*__CHARTJS_INLINE__*/</script>"

const (
	activityLiquidBegin = "<!--ACTIVITY_LIQUID_BEGIN-->"
	activityLiquidEnd   = "<!--ACTIVITY_LIQUID_END-->"
)

var activityReportBody = sync.OnceValues(func() (string, error) {
	return inlineChartJS(activityReportTemplate)
})

func inlineChartJS(body string) (string, error) {
	if !strings.Contains(body, chartJSInlineMarker) {
		return "", fmt.Errorf("activity report template missing Chart.js inline marker %q", chartJSInlineMarker)
	}
	if strings.Contains(chartUMDMinJS, "{{") || strings.Contains(chartUMDMinJS, "{%") {
		return "", fmt.Errorf("vendored Chart.js contains a Liquid opener ({{ or {%%) — it would be corrupted at render; re-vendor or add escaping")
	}
	inlined := "<script>\n" + chartUMDMinJS + "\n</script>"
	return strings.Replace(body, chartJSInlineMarker, inlined, 1), nil
}

type toolScopedStore struct {
	svc                  *artifact.ScopedService
	agent, user, session string
	logger               *slog.Logger
}

func newToolScopedStore(toolCtx *tool.Context) *toolScopedStore {
	return &toolScopedStore{
		svc:     toolCtx.ArtifactService,
		agent:   toolCtx.AgentName,
		user:    toolCtx.UserID,
		session: toolCtx.SessionID,
		logger:  slog.Default(),
	}
}

func (s *toolScopedStore) Save(ctx context.Context, filename string, content []byte, mimeType, description string) (int, error) {
	res, err := artifact.SaveWithMetadata(ctx, s.svc, s.agent, s.user, s.session, filename, content, mimeType,
		artifact.MetadataOpts{Description: description}, s.logger)
	if err != nil {
		return 0, err
	}
	return res.DataVersion, nil
}

func renderActivityReport(
	ctx context.Context,
	toolCtx *tool.Context,
	report *ActivityReport,
	filters activityFilters,
	generatedAt time.Time,
	outName string,
) ([]byte, int, error) {
	if toolCtx == nil || toolCtx.ArtifactService == nil {
		return nil, 0, fmt.Errorf("activity report render: artifact service unavailable")
	}
	body, err := activityReportBody()
	if err != nil {
		return nil, 0, err
	}
	data, err := buildActivityReportData(report, generatedAt)
	if err != nil {
		return nil, 0, fmt.Errorf("build activity data: %w", err)
	}
	html, err := renderActivityHTML(body, buildActivityReportSubstitutions(report, filters, generatedAt), data)
	if err != nil {
		return nil, 0, err
	}
	out := []byte(html)
	version, err := newToolScopedStore(toolCtx).Save(ctx, outName, out, "text/html", activityReportHTMLDesc)
	if err != nil {
		return nil, 0, fmt.Errorf("save activity report artifact: %w", err)
	}
	return out, version, nil
}

func renderActivityHTML(body string, subs map[string]string, data map[string]any) (string, error) {
	pairs := make([]string, 0, len(subs)*2)
	for key, val := range subs {
		pairs = append(pairs, "@@"+key+"@@", val)
	}
	body = strings.NewReplacer(pairs...).Replace(body)
	if idx := strings.Index(body, "@@"); idx >= 0 {
		end := min(idx+40, len(body))
		return "", fmt.Errorf("activity report template has an unsubstituted placeholder near %q", body[idx:end])
	}

	openIdx := strings.Index(body, activityLiquidBegin)
	closeIdx := strings.Index(body, activityLiquidEnd)
	if openIdx < 0 || closeIdx < openIdx {
		return "", fmt.Errorf("activity report template is missing its %s/%s delimiters",
			activityLiquidBegin, activityLiquidEnd)
	}
	head := body[:openIdx]
	liquidRegion := body[openIdx+len(activityLiquidBegin) : closeIdx]
	tail := body[closeIdx+len(activityLiquidEnd):]

	rendered, err := liquidsafe.RenderStringWithDeadline(liquidsafe.NewEngine(),
		liquidRegion, data, liquidsafe.DefaultRenderDeadline)
	if err != nil {
		return "", fmt.Errorf("render activity report template: %w", err)
	}
	return head + rendered + tail, nil
}

func buildActivityReportSubstitutions(report *ActivityReport, filters activityFilters, generatedAt time.Time) map[string]string {
	subs := map[string]string{
		"window_label":          report.Window.Label,
		"generated_at":          generatedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		"scope_key":             effectiveScopeChipKey(report.Scope, filters),
		"scope_label":           effectiveScopeChipLabel(report.Scope, filters),
		"user_filter_chip":      filterChipForUser(filters.TargetUserID),
		"verdict_emoji":         verdictEmoji(report.Verdict.Severity),
		"verdict_label":         report.Verdict.Label,
		"verdict_class":         verdictAlertClass(report.Verdict.Severity),
		"verdict_headline":      report.Verdict.Headline,
		"retention_days_footer": "",
	}
	if report.RetentionDays > 0 {
		startsIso := generatedAt.UTC().AddDate(0, 0, -report.RetentionDays).Format("2006-01-02")
		subs["retention_days_footer"] = fmt.Sprintf(
			"Data retained for the last %d days · Oldest data from %s",
			report.RetentionDays, startsIso)
	}
	subs["active_bg_tooltip"] = "Background tasks currently in flight — rows with execution_mode=background and end_time not yet set."
	subs["histogram_sample_note"] = buildHistogramSampleNote(report)
	return subs
}

func buildHistogramSampleNote(report *ActivityReport) string {
	measured := 0
	for _, b := range report.DurationDist.Buckets {
		measured += b.Count
	}
	total := 0
	for _, r := range report.StatusBreakdown.Rows {
		total += r.Count
	}
	if total <= 0 || measured >= total {
		return ""
	}
	return fmt.Sprintf("%d of %d tasks measured — the rest are still in progress, stale, or have no duration recorded.", measured, total)
}

const activityReportPeekRows = 5

func activityReportOverflowCount(rowCount int) int {
	if rowCount <= activityReportPeekRows {
		return 0
	}
	return rowCount - activityReportPeekRows
}

func buildActivityReportData(report *ActivityReport, generatedAt time.Time) (map[string]any, error) {
	kpiViews := buildHTMLKPITileViews(report.KPIs)
	statusViews := buildHTMLStatusRowViews(report.StatusBreakdown.Rows)
	durationViews := buildHTMLDurationRowViews(report.DurationDist.Buckets)
	agentViews := buildHTMLSubjectRowViews(report.ByAgent.Rows)
	workflowViews := buildHTMLSubjectRowViews(report.ByWorkflow.Rows)
	topUserViews := buildHTMLTopUserRowViews(report.TopUsers)
	tokenTrendViews := buildHTMLTokenTrendRowViews(report.TokenTrend)
	failureViews, extra := buildHTMLFailureRowViews(report.RecentFailures, report.FailuresTotal, maxFailureRowsInHTML)
	feedbackViews := buildHTMLFeedbackRowViews(report.Feedback)

	chartConfigs := map[string]any{
		"statusPie":         statusPieChartJSConfig(report.StatusBreakdown.Rows),
		"durationHistogram": durationHistogramChartJSConfig(report.DurationDist.Buckets),
	}
	if report.TokenTrend != nil && len(report.TokenTrend.Rows) > 0 {
		chartConfigs["tokenTrend"] = tokenTrendChartJSConfig(report.TokenTrend.Rows)
		chartConfigs["taskCountTrend"] = taskCountTrendChartJSConfig(report.TokenTrend.Rows)
	} else {
		chartConfigs["tokenTrend"] = nil
		chartConfigs["taskCountTrend"] = nil
	}

	return map[string]any{
		"window": map[string]any{
			"label":       report.Window.Label,
			"generatedAt": generatedAt.UTC().Format(time.RFC3339),
		},
		"verdict": map[string]any{
			"severity": string(report.Verdict.Severity),
			"headline": report.Verdict.Headline,
		},
		"kpis":              kpiTilesToMaps(kpiViews),
		"statusBreakdown":   map[string]any{"rows": statusRowsToMaps(statusViews)},
		"durationDist":      map[string]any{"buckets": durationRowsToMaps(durationViews)},
		"tokenTrend":        tokenTrendSection(tokenTrendViews),
		"byAgent":           map[string]any{"rows": subjectRowsToMaps(agentViews)},
		"byWorkflow":        map[string]any{"rows": subjectRowsToMaps(workflowViews)},
		"topUsers":          topUsersSection(report.TopUsers, topUserViews),
		"topUsersOverflow":  activityReportOverflowCount(len(topUserViews)),
		"topUsersSummary":   summarizeTopUsers(report.TopUsers),
		"recentFailures":    failureRowsToMaps(failureViews),
		"extraFailures":     extra,
		"feedback":          feedbackRowsToMaps(feedbackViews),
		"feedbackOverflow":  activityReportOverflowCount(len(feedbackViews)),
		"feedbackTruncated": report.FeedbackTruncated,
		"feedbackSummary":   summarizeFeedback(report.Feedback, report.FeedbackTruncated),
		"chartConfigs":      chartConfigs,
	}, nil
}

func kpiTilesToMaps(views []htmlKPITileView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, t := range views {
		out = append(out, map[string]any{
			"label":     t.Label,
			"value":     t.Value,
			"subLabel":  t.Sub,
			"severity":  t.SeverityClass,
			"deltaText": t.DeltaPctStr,
			"deltaDir":  t.DeltaDir,
		})
	}
	return out
}

func statusRowsToMaps(views []htmlStatusRowView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, r := range views {
		out = append(out, map[string]any{
			"status":     r.Status,
			"count":      r.Count,
			"share":      r.Pct,
			"badgeClass": r.BadgeClass,
		})
	}
	return out
}

func durationRowsToMaps(views []htmlDurationRowView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, r := range views {
		out = append(out, map[string]any{
			"label": r.Bucket,
			"count": r.Count,
		})
	}
	return out
}

func subjectRowsToMaps(views []htmlSubjectRowView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, r := range views {
		nested := make([]map[string]any, 0, len(r.NestedRows))
		for _, n := range r.NestedRows {
			nested = append(nested, map[string]any{
				"name":         n.Name,
				"invocations":  n.Invocations,
				"errors":       n.Errors,
				"successRate":  n.SuccessRate,
				"badgeClass":   n.BadgeClass,
				"inputTokens":  n.InputTokens,
				"outputTokens": n.OutputTokens,
				"cachedTokens": n.CachedTokens,
			})
		}
		out = append(out, map[string]any{
			"name":           r.Name,
			"invocations":    r.Invocations,
			"errors":         r.Errors,
			"successRate":    r.SuccessRate,
			"badgeClass":     r.BadgeClass,
			"p95":            r.P95,
			"inputTokens":    r.InputTokens,
			"outputTokens":   r.OutputTokens,
			"cachedTokens":   r.CachedTokens,
			"nestedHandlers": nested,
		})
	}
	return out
}

func tokenTrendSection(views []htmlTokenTrendRowView) map[string]any {
	if len(views) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(views))
	for _, r := range views {
		rows = append(rows, map[string]any{
			"date":   r.Date,
			"input":  r.Input,
			"output": r.Output,
			"cached": r.Cached,
		})
	}
	return map[string]any{"rows": rows}
}

func topUsersSection(block TopUsersBlock, views []htmlTopUserRowView) map[string]any {
	return map[string]any{
		"available": block.Available,
		"rows":      topUserRowsToMaps(views),
		"truncated": block.Truncated,
	}
}

func topUserRowsToMaps(views []htmlTopUserRowView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, r := range views {
		out = append(out, map[string]any{
			"userId":       r.UserID,
			"totalTasks":   r.TaskCount,
			"totalTokens":  r.TotalTokens,
			"inputTokens":  r.InputTokens,
			"outputTokens": r.OutputTokens,
		})
	}
	return out
}

func failureRowsToMaps(views []htmlFailureRowView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, r := range views {
		out = append(out, map[string]any{
			"taskId":    r.TaskID,
			"agent":     r.Agent,
			"startTime": r.StartTime,
			"status":    r.Status,
			"duration":  r.Duration,
			"request":   r.Request,
		})
	}
	return out
}

func feedbackRowsToMaps(views []htmlFeedbackRowView) []map[string]any {
	out := make([]map[string]any, 0, len(views))
	for _, r := range views {
		out = append(out, map[string]any{
			"taskId":         r.TaskID,
			"userId":         r.UserID,
			"rating":         r.Rating,
			"comment":        r.Comment,
			"commentFull":    r.CommentFull,
			"agent":          r.SubjectName,
			"entrypointType": r.EntrypointType,
			"updatedAt":      r.UpdatedAt,
		})
	}
	return out
}
