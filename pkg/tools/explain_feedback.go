// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const sessionNoAccess = "they can see this feedback but do not have access to its full chat session"

const sessionTooDeep = "this chat session is nested too deeply to replay"

func sessionRefusedGuidance(refusal string) string {
	reason := sessionNoAccess
	if refusal == analytics.SessionRefusalTooDeep {
		reason = sessionTooDeep
	}
	return "The embed above is this feedback entry. Start your reply with it verbatim, then tell the user " +
		reason + ". Anything you add must come from the entry JSON you were given, which is all you know " +
		"about this feedback: never describe the session or any other entry."
}

type ExplainFeedbackTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func NewExplainFeedbackTool() *ExplainFeedbackTool { return &ExplainFeedbackTool{} }

func (t *ExplainFeedbackTool) Name() string { return "explain_feedback" }

func (t *ExplainFeedbackTool) Description() string {
	return `Explains one piece of feedback by replaying the chat session up to the
rated turn. The tool message is an «artifact_content» embed that expands into a
rendered report (rating, comment, each turn's request, tree and timeline). Start
your reply with that embed verbatim; never re-render the trees or timelines,
paste the session, or add an «artifact_return».

You also receive the session as JSON. Follow the embed with
"### What likely went wrong": 2-3 bullets grounded in the session (failed turns,
errorMessage, last events, the user's comment). If earlierTurnsOmitted is set,
say earlier turns were left out rather than guessing at them. Nodes with eventsOmitted had their
events left out to fit the size limit, so never say they did nothing. Ignore durationMs where
hideDuration is set.

Use when asked why a user rated an answer the way they did. Pass exactly one
of feedbackId (from list_feedback or given by the user) or taskId (the rated
turn's task id, when that is what the user has). With only a task id,
call this directly; never list feedback to find the id first. A session id
alone is not enough: ask for the task id or feedback id. Call it once per
request, never in a loop over several entries: if asked to open many, say it
opens one at a time and offer the list instead.

If the user can see the feedback but not its chat session, or the session is
too deeply nested to replay, the tool returns the feedback entry instead, with
that entry as JSON, followed by instructions: show the entry, say why the
session is not shown, and never call that "not found" or guess at the
session's content.`
}

func (t *ExplainFeedbackTool) RequiredScopes() []string { return nil }

func (t *ExplainFeedbackTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"feedbackId": map[string]any{
				"type":        "string",
				"description": "A single feedback id, from list_feedback or given by the user. Omit when passing taskId.",
			},
			"taskId": map[string]any{
				"type":        "string",
				"description": "The rated turn's task id, when the user has that instead of a feedback id. Omit when passing feedbackId.",
			},
		},
	}
}

func (t *ExplainFeedbackTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *ExplainFeedbackTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx == nil || toolCtx.ControlQuerier == nil {
		return tool.Error("Activity service is not available. Ensure the gateway is running."), nil
	}

	feedbackID, _ := args["feedbackId"].(string)
	taskID, _ := args["taskId"].(string)
	if (feedbackID == "") == (taskID == "") {
		return tool.Error("Pass exactly one of feedbackId or taskId (a single string)."), nil
	}

	logger := slog.Default().With("logger_name", "explain-feedback", "traceID", toolTraceID(toolCtx), "feedbackID", feedbackID, "taskID", taskID)
	path := fmt.Sprintf("activity/feedback/%s/session", feedbackID)
	if taskID != "" {
		path = fmt.Sprintf("activity/feedback/task/%s/session", taskID)
	}
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", path, nil)
	if err != nil {
		logger.Warn("explain_feedback control call failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to explain feedback: %s", err)), nil
	}

	var session analytics.SessionTraceResponse
	if err = json.Unmarshal(raw, &session); err != nil {
		logger.Warn("explain_feedback response parse failed", "error", err, "responseBytes", len(raw))
		return tool.Error(fmt.Sprintf("Failed to parse feedback session response: %s", err)), nil
	}

	rendered, err := renderActivityMarkdown(explainFeedbackTemplate, explainFeedbackData(&session, userLocation(ctx, toolCtx, logger)))
	if err != nil {
		logger.Error("explain_feedback render failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to render feedback explanation: %s", err)), nil
	}

	stem := "feedback_session_" + sanitizeFilenameSegment(cmp.Or(session.FeedbackID, feedbackID, taskID))
	report, err := saveActivityMarkdown(ctx, toolCtx, stem+".md", rendered, "Feedback session")
	if err != nil {
		logger.Warn("explain_feedback save failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to save feedback explanation: %s", err)), nil
	}
	if session.Refusal != "" {
		logger.Debug("explain_feedback session refused", "refusal", session.Refusal)
		return tool.OK(report+"\n\n"+sessionRefusedGuidance(session.Refusal),
			tool.WithDataObjects(traceJSON(stem+".json", "Feedback entry (LLM reasoning only)", raw)),
		), nil
	}

	return tool.OK(report,
		tool.WithDataObjects(traceJSON(stem+".json", "Feedback session (LLM reasoning only)", raw)),
	), nil
}

func explainFeedbackData(s *analytics.SessionTraceResponse, loc *time.Location) map[string]any {
	turns := make([]map[string]any, 0, len(s.Turns))
	when := ""
	if s.RatedAt != "" {
		when = feedbackTime(s.RatedAt, loc)
	}
	for i := range s.Turns {
		turn := explainFeedbackTurn(&s.Turns[i], i+1, s.RatedTaskID, loc)
		if when == "" && turn["rated"] == true {
			when = turn["when"].(string)
		}
		turns = append(turns, turn)
	}
	return map[string]any{
		"rating":         feedbackRatingLabel(s.Rating),
		"agent":          feedbackSubjectName(&s.Subject, codeSafe),
		"when":           when,
		"comment":        codeSafe(s.Comment),
		"turns":          turns,
		"earlierOmitted": s.EarlierTurnsOmitted,
		"retentionGap":   s.RetentionGap,
		"refused":        s.Refusal == analytics.SessionRefusalNoAccess,
		"tooDeep":        s.Refusal == analytics.SessionRefusalTooDeep,
	}
}

func explainFeedbackTurn(turn *analytics.TaskTreeResponse, number int, ratedTaskID string, loc *time.Location) map[string]any {
	inZone := func(iso string) string { return feedbackTime(iso, loc) }
	nodes := traceNodes(turn)
	queried := queriedNode(turn, nodes)
	return map[string]any{
		"number":        number,
		"rated":         ratedTaskID != "" && slices.ContainsFunc(nodes, func(n *analytics.TreeNodeDTO) bool { return n.ID == ratedTaskID }),
		"failed":        endedUnsuccessfully(queried.Status),
		"request":       turnRequest(queried),
		"statusEmoji":   queried.StatusEmoji,
		"status":        titleStatus(queried.Status),
		"when":          inZone(cmp.Or(queried.StartDate, queried.EndDate)),
		"treeLines":     treeLines(nodes, queried),
		"timeline":      timeline(queried.Events, inZone),
		"eventsOmitted": queried.EventsOmitted && len(queried.Events) == 0,
		"rootCause":     rootCause(queried),
	}
}

func turnRequest(n *analytics.TreeNodeDTO) string {
	for _, e := range n.Events {
		if e.Kind == "request" {
			return codeSafe(e.Summary)
		}
	}
	return ""
}
