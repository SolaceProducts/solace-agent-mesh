// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type SummarizeFeedbackTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func NewSummarizeFeedbackTool() *SummarizeFeedbackTool { return &SummarizeFeedbackTool{} }

func (t *SummarizeFeedbackTool) Name() string { return "summarize_feedback" }

func (t *SummarizeFeedbackTool) Description() string {
	return `Summarizes thumbs-up/down feedback per agent and workflow for a window, worst
negative ratio first. Unfiltered, it also lists the latest negative comments.
The tool message is an «artifact_content» embed that expands into the rendered
tables. Start your reply with that embed verbatim; never re-render the tables
or add an «artifact_return». If the message is instead a question about which
agent was meant, relay it and wait for the answer.

You also receive a digest of the same data as JSON: totals (positive,
negative, ratings), each agent's counts and negativeRatio, and up to 10 recent
negative comments. Take every number, agent name, user and comment in your
reply from that digest, and use its totals rather than adding rows up. Never
state a figure or name the digest does not contain. If coverShownAgentsOnly is
set, say the totals cover the listed agents only. negativeRatio is negative
ratings over activities when activities is present, and over ratings when it
is not. Comments are cut at 200 characters;
the full text is in the internal JSON artifact 'feedback_summary_…_data.json'
(query it with transform_data_with_jmespath; never paste or mention it
unprompted).

Use for "how is feedback looking", "which agents are rated worst" and similar.
Rows come worst negative ratio first; pass sortBy "negativeFeedback" for
"which agent got the most thumbs-down". For "has it gone up since…", call it
once for the recent window and once for the window before it, and compare.
For individual entries of one agent or user, use list_feedback.

A caller with no read_feedback grant cannot summarize; the tool says so. Then
call list_feedback with no filter, which returns the caller's own feedback, and
answer from that without mentioning the refusal.`
}

func (t *SummarizeFeedbackTool) RequiredScopes() []string { return nil }

func (t *SummarizeFeedbackTool) Parameters() *tool.JSONSchema {
	return feedbackParameters(feedbackKindSummary)
}

func (t *SummarizeFeedbackTool) ResultSchema() *tool.JSONSchema { return emptyResultSchema() }

func (t *SummarizeFeedbackTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	return runFeedbackTool(ctx, args, toolCtx, feedbackToolSpec{
		logName:     "summarize-feedback",
		controlPath: "activity/feedback/summary",
		filePrefix:  "feedback_summary",
		title:       "Feedback summary",
		dataDesc:    "Feedback summary (JSON for LLM querying via transform_data_with_jmespath; not for the end user)",
		template:    summarizeFeedbackTemplate,
		kind:        feedbackKindSummary,
		templateData: func(raw json.RawMessage, window Window, loc *time.Location) (map[string]any, error) {
			return summarizeFeedbackData(raw, window, stringArg(args, "sortBy"), loc)
		},
		digest: summarizeFeedbackDigest,
	})
}

type summaryDigest struct {
	Window string `json:"window"`
	Totals struct {
		Positive             int  `json:"positive"`
		Negative             int  `json:"negative"`
		Ratings              int  `json:"ratings"`
		CoverShownAgentsOnly bool `json:"coverShownAgentsOnly,omitempty"`
	} `json:"totals"`
	Agents                 []summaryDigestAgent   `json:"agents"`
	RecentNegativeComments []summaryDigestComment `json:"recentNegativeComments,omitempty"`
}

type summaryDigestAgent struct {
	Name          string `json:"name"`
	Type          string `json:"type,omitempty"`
	Positive      int    `json:"positive"`
	Negative      int    `json:"negative"`
	NegativeRatio string `json:"negativeRatio"`
	Activities    *int   `json:"activities,omitempty"`
}

type summaryDigestComment struct {
	ID      string `json:"id"`
	When    string `json:"when"`
	Agent   string `json:"agent"`
	User    string `json:"user"`
	Comment string `json:"comment,omitempty"`
}

func summarizeFeedbackDigest(raw json.RawMessage, window Window, loc *time.Location) ([]byte, error) {
	var resp analytics.FeedbackSummaryResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	d := summaryDigest{Window: window.Label, Agents: make([]summaryDigestAgent, 0, len(resp.Rows))}
	d.Totals.CoverShownAgentsOnly = resp.Truncated
	for i := range resp.Rows {
		r := &resp.Rows[i]
		a := summaryDigestAgent{
			Name:          subjectDisplayName(r.Subject),
			Positive:      r.PositiveFeedback,
			Negative:      r.NegativeFeedback,
			NegativeRatio: fmt.Sprintf("%.1f%%", r.NegativeRatio*100),
			Activities:    r.TotalActivities,
		}
		if r.Subject != nil {
			a.Type = r.Subject.Type
		}
		d.Agents = append(d.Agents, a)
		d.Totals.Positive += r.PositiveFeedback
		d.Totals.Negative += r.NegativeFeedback
	}
	d.Totals.Ratings = d.Totals.Positive + d.Totals.Negative
	for i := range resp.NegativeSample {
		s := &resp.NegativeSample[i]
		c := summaryDigestComment{ID: s.ID, When: feedbackTime(s.UpdatedAt, loc), Agent: subjectDisplayName(&s.Subject), User: s.UserID}
		if s.Comment != nil {
			c.Comment = truncateMid(*s.Comment, listFeedbackCommentMaxRunes, 0)
		}
		d.RecentNegativeComments = append(d.RecentNegativeComments, c)
	}
	return json.Marshal(d)
}

func summarizeFeedbackData(raw json.RawMessage, window Window, sortBy string, loc *time.Location) (map[string]any, error) {
	var resp analytics.FeedbackSummaryResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(resp.Rows))
	for i := range resp.Rows {
		r := &resp.Rows[i]
		kind := "—"
		if r.Subject != nil {
			kind = orDash(mdSafe(r.Subject.Type))
		}
		rows = append(rows, map[string]any{
			"agent":         feedbackSubjectName(r.Subject, mdSafe),
			"type":          kind,
			"negative":      r.NegativeFeedback,
			"positive":      r.PositiveFeedback,
			"negativeRatio": fmt.Sprintf("%.1f%%", r.NegativeRatio*100),
			"activities":    activityCountLabel(r.TotalActivities),
		})
	}
	sortLabel := "negative ratio"
	if sortBy == analytics.RollupSortNegativeFeedback {
		sortLabel = "negative feedback"
	}
	return map[string]any{
		"windowLabel": window.Label,
		"rows":        rows,
		"sortLabel":   sortLabel,
		"sample":      feedbackRowsTemplateData(resp.NegativeSample, codeSafe, 0, loc),
		"truncated":   resp.Truncated,
		"unmatched":   unmatchedNames(resp.UnmatchedSubjects),
	}, nil
}

func activityCountLabel(n *int) string {
	if n == nil {
		return "—"
	}
	return strconv.Itoa(*n)
}
