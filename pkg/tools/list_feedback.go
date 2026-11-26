// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ListFeedbackTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func NewListFeedbackTool() *ListFeedbackTool { return &ListFeedbackTool{} }

func (t *ListFeedbackTool) Name() string { return "list_feedback" }

func (t *ListFeedbackTool) Description() string {
	return `Lists the newest individual feedback entries (rating and comment) for an agent,
workflow or user, or one entry by feedbackId. At least one of subjects, userId
or feedbackId is required, except for a caller with no feedback grant: called
with no filter, it lists the feedback they gave themselves. The tool
message is an «artifact_content» embed that expands into the rendered table.
Start your reply with that embed verbatim; never re-render the table or add an
«artifact_return». If the message is instead a question about which agent was
meant, relay it and wait for the answer.

For follow-ups the table doesn't answer, query the internal JSON artifact
'feedback_rows_…_data.json' with transform_data_with_jmespath (rows under
data); never paste it or mention it unprompted. Each row carries display-ready
'when' and 'ratingEmoji' fields: quote those, never updatedAt or rating. The
table cuts comments longer than 200 characters with "…"; when asked for a full
comment, read it from that artifact by its feedback id.

The ids in the table are feedback ids, even when the user calls them task
ids. When the user asks why a piece of feedback was given, call
explain_feedback with that row's feedback id; never look it up here first.`
}

func (t *ListFeedbackTool) RequiredScopes() []string { return nil }

func (t *ListFeedbackTool) Parameters() *tool.JSONSchema { return feedbackParameters(feedbackKindRows) }

func (t *ListFeedbackTool) ResultSchema() *tool.JSONSchema { return emptyResultSchema() }

func (t *ListFeedbackTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	return runFeedbackTool(ctx, args, toolCtx, feedbackToolSpec{
		logName:      "list-feedback",
		controlPath:  "activity/feedback/rows",
		filePrefix:   "feedback_rows",
		title:        "Feedback",
		dataDesc:     "Feedback rows (JSON for LLM querying via transform_data_with_jmespath; not for the end user)",
		template:     listFeedbackTemplate,
		kind:         feedbackKindRows,
		templateData: listFeedbackData,
	})
}

const listFeedbackCommentMaxRunes = 200

func listFeedbackData(raw json.RawMessage, window Window, loc *time.Location) (map[string]any, error) {
	var resp analytics.FeedbackRowsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"windowLabel": window.Label,
		"rows":        feedbackRowsTemplateData(resp.Data, mdSafe, listFeedbackCommentMaxRunes, loc),
		"truncated":   resp.Meta.Truncated,
		"unmatched":   unmatchedNames(resp.UnmatchedSubjects),
	}, nil
}
