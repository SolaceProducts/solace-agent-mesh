// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const maxFailureRowsInHTML = 10

type kpiGoodDir int

const (
	kpiGoodNeutral kpiGoodDir = iota
	kpiGoodUp
	kpiGoodDown
)

var kpiGoodDirectionByID = map[string]kpiGoodDir{
	KPITileTotalTasks:            kpiGoodNeutral,
	KPITileSuccessRate:           kpiGoodUp,
	KPITileFailureCount:          kpiGoodDown,
	KPITileP95DurationMs:         kpiGoodDown,
	KPITileActiveBackgroundCount: kpiGoodNeutral,
}

type htmlKPITileView struct {
	Label, Value, Sub string
	SeverityClass     string
	DeltaPctStr       string
	DeltaDir          string
}

type htmlStatusRowView struct {
	Status     string
	Count      string
	Pct        string
	BadgeClass string
}

type htmlDurationRowView struct {
	Bucket string
	Count  string
}

type htmlTopUserRowView struct {
	UserID       string
	TaskCount    string
	TotalTokens  string
	InputTokens  string
	OutputTokens string
}

type htmlTokenTrendRowView struct {
	Date   string
	Input  string
	Output string
	Cached string
}

type htmlFailureRowView struct {
	TaskID    string
	Agent     string
	StartTime string
	Status    string
	Duration  string
	Request   string
}

type htmlFeedbackRowView struct {
	TaskID         string
	UserID         string
	Rating         string
	Comment        string
	CommentFull    string
	SubjectName    string
	EntrypointType string
	UpdatedAt      string
}

type htmlSubjectRowView struct {
	Name         string
	Invocations  string
	Errors       string
	SuccessRate  string
	BadgeClass   string
	P95          string
	InputTokens  string
	OutputTokens string
	CachedTokens string
	NestedRows   []htmlNestedHandlerRowView
}

type htmlNestedHandlerRowView struct {
	Name         string
	Invocations  string
	Errors       string
	SuccessRate  string
	BadgeClass   string
	InputTokens  string
	OutputTokens string
	CachedTokens string
}

func buildHTMLKPITileViews(tiles []KPITile) []htmlKPITileView {
	out := make([]htmlKPITileView, 0, len(tiles))
	for _, t := range tiles {
		sub := t.Sub
		if sub == "" {
			sub = kpiSubLabel(t.ID)
		}
		v := htmlKPITileView{
			Label:         t.Label,
			Value:         t.Value,
			SeverityClass: kpiSeverityClass(t),
			Sub:           sub,
		}
		if t.DeltaPct != nil {
			d := *t.DeltaPct
			v.DeltaPctStr = formatDeltaPct(d)
			v.DeltaDir = pickKPIDeltaDir(t.ID, d)
		}
		out = append(out, v)
	}
	return out
}

func buildHTMLSubjectRowViews(rows []AgentRow) []htmlSubjectRowView {
	out := make([]htmlSubjectRowView, 0, len(rows))
	for _, r := range rows {
		var p95 string
		if r.DurationMeasuredCount < smallSamplePercentileThreshold {
			p95 = "—"
		} else {
			p95 = fmt.Sprintf("%sms", commaInt64(r.P95DurationMs))
		}
		view := htmlSubjectRowView{
			Name:         html.EscapeString(r.SubjectName),
			Invocations:  commaInt(r.InvocationCount),
			Errors:       commaInt(r.ErrorCount),
			SuccessRate:  pct(r.SuccessRate),
			BadgeClass:   successBadgeClass(r.SuccessRate),
			P95:          p95,
			InputTokens:  commaInt64(r.TotalInputTokens),
			OutputTokens: commaInt64(r.TotalOutputTokens),
			CachedTokens: commaInt64(r.TotalCachedInputTokens),
		}
		for _, n := range r.NestedHandlers {
			view.NestedRows = append(view.NestedRows, htmlNestedHandlerRowView{
				Name:         "└ " + html.EscapeString(n.Name),
				Invocations:  commaInt(n.InvocationCount),
				Errors:       commaInt(n.ErrorCount),
				SuccessRate:  pct(n.SuccessRate),
				BadgeClass:   successBadgeClass(n.SuccessRate),
				InputTokens:  commaInt64(n.TotalInputTokens),
				OutputTokens: commaInt64(n.TotalOutputTokens),
				CachedTokens: commaInt64(n.TotalCachedInputTokens),
			})
		}
		out = append(out, view)
	}
	return out
}

func buildHTMLFailureRowViews(rows []FailureRow, total, limit int) ([]htmlFailureRowView, int) {
	if len(rows) == 0 {
		return nil, 0
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	extra := total - len(rows)
	if extra < 0 {
		extra = 0
	}
	out := make([]htmlFailureRowView, 0, len(rows))
	for _, r := range rows {
		agent := r.SubjectName
		if agent == "" {
			agent = "—"
		}
		duration := "—"
		if r.Status != store.TaskStatusJanitorTimeout {
			duration = formatDurationShort(r.DurationMs)
		}
		out = append(out, htmlFailureRowView{
			TaskID:    r.TaskID,
			Agent:     html.EscapeString(agent),
			StartTime: humanizeIsoTime(r.StartTimeIso),
			Status:    html.EscapeString(r.Status),
			Duration:  duration,
			Request:   html.EscapeString(truncateMid(r.InitialRequestText, 120, 0)),
		})
	}
	return out, extra
}

func buildHTMLFeedbackRowViews(rows []FeedbackRow) []htmlFeedbackRowView {
	if len(rows) == 0 {
		return nil
	}
	out := make([]htmlFeedbackRowView, 0, len(rows))
	for _, r := range rows {
		subject := r.SubjectName
		if subject == "" {
			subject = "—"
		}
		userID := r.UserID
		if userID == "" {
			userID = "—"
		}
		out = append(out, htmlFeedbackRowView{
			TaskID:         r.TaskID,
			UserID:         html.EscapeString(userID),
			Rating:         html.EscapeString(r.Rating),
			Comment:        html.EscapeString(truncateMid(r.Comment, 100, 0)),
			CommentFull:    html.EscapeString(r.Comment),
			SubjectName:    html.EscapeString(subject),
			EntrypointType: html.EscapeString(r.EntrypointType),
			UpdatedAt:      humanizeIsoTime(r.UpdatedAtIso),
		})
	}
	return out
}

func buildHTMLStatusRowViews(rows []StatusCountRow) []htmlStatusRowView {
	total := 0
	for _, r := range rows {
		total += r.Count
	}
	out := make([]htmlStatusRowView, 0, len(rows))
	for _, r := range rows {
		var share float64
		if total > 0 {
			share = float64(r.Count) / float64(total)
		}
		out = append(out, htmlStatusRowView{
			Status:     html.EscapeString(r.Status),
			Count:      commaInt(r.Count),
			Pct:        pct(share),
			BadgeClass: statusBadgeClass(r.Status),
		})
	}
	return out
}

func buildHTMLDurationRowViews(buckets []DurationBucket) []htmlDurationRowView {
	out := make([]htmlDurationRowView, 0, len(buckets))
	for _, b := range buckets {
		if b.Count == 0 {
			continue
		}
		out = append(out, htmlDurationRowView{
			Bucket: bucketLabel(b),
			Count:  commaInt(b.Count),
		})
	}
	return out
}

func buildHTMLTopUserRowViews(block TopUsersBlock) []htmlTopUserRowView {
	if !block.Available {
		return nil
	}
	out := make([]htmlTopUserRowView, 0, len(block.Rows))
	for _, r := range block.Rows {
		userID := r.UserID
		if userID == "" {
			userID = "—"
		}
		out = append(out, htmlTopUserRowView{
			UserID:       html.EscapeString(userID),
			TaskCount:    commaInt(r.TaskCount),
			TotalTokens:  commaInt64(r.TotalInputTokens + r.TotalOutputTokens),
			InputTokens:  commaInt64(r.TotalInputTokens),
			OutputTokens: commaInt64(r.TotalOutputTokens),
		})
	}
	return out
}

func buildHTMLTokenTrendRowViews(section *TokenTrendSection) []htmlTokenTrendRowView {
	if section == nil {
		return nil
	}
	out := make([]htmlTokenTrendRowView, 0, len(section.Rows))
	for _, r := range section.Rows {
		out = append(out, htmlTokenTrendRowView{
			Date:   r.DateIso,
			Input:  commaInt64(r.Input),
			Output: commaInt64(r.Output),
			Cached: commaInt64(r.Cached),
		})
	}
	return out
}

func kpiSubLabel(id string) string {
	if id == KPITileActiveBackgroundCount {
		return "running now"
	}
	return ""
}

func kpiSeverityClass(t KPITile) string {
	switch t.ID {
	case KPITileSuccessRate:
		if r, ok := parsePercentValue(t.Value); ok {
			switch {
			case r >= 0.95:
				return "good"
			case r >= 0.80:
				return "warn"
			default:
				return "danger"
			}
		}
	case KPITileFailureCount:
		if n, err := strconv.Atoi(strings.ReplaceAll(t.Value, ",", "")); err == nil {
			if n == 0 {
				return "good"
			}
			return "danger"
		}
	}
	return ""
}

func successBadgeClass(rate float64) string {
	switch {
	case rate >= 0.95:
		return "badge-green"
	case rate >= 0.80:
		return "badge-yellow"
	default:
		return "badge-red"
	}
}

func statusBadgeClass(status string) string {
	switch status {
	case "completed":
		return "badge-green"
	case "failed", "error", "timeout", "stale":
		return "badge-red"
	case "running", "in_progress":
		return "badge-yellow"
	}
	return "badge-gray"
}

func pickKPIDeltaDir(tileID string, d float64) string {
	if math.Abs(d) < 0.05 {
		return "flat"
	}
	switch kpiGoodDirectionByID[tileID] {
	case kpiGoodUp:
		if d > 0 {
			return "up"
		}
		return "down"
	case kpiGoodDown:
		if d < 0 {
			return "up"
		}
		return "down"
	default:
		return "flat"
	}
}

func formatDeltaPct(d float64) string {
	if math.Abs(d) < 0.05 {
		return "0.0%"
	}
	if d > 0 {
		return fmt.Sprintf("↑ %.1f%%", d)
	}
	return fmt.Sprintf("↓ %.1f%%", -d)
}

func parsePercentValue(s string) (float64, bool) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(s), "%")
	f, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, false
	}
	return f / 100, true
}

func humanizeIsoTime(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		t2, err2 := time.Parse(time.RFC3339, iso)
		if err2 != nil {
			return iso
		}
		t = t2
	}
	return t.UTC().Format("Jan 2 15:04:05 UTC")
}

func verdictAlertClass(s VerdictSeverity) string {
	if v, ok := verdictView[s]; ok {
		return v.AlertClass
	}
	return "info"
}
