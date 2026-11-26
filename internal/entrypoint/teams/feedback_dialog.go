// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	InvokeMessageFetchTask    = "message/fetchTask"
	InvokeMessageSubmitAction = "message/submitAction"
)

const feedbackCorrelationTTL = 24 * time.Hour

const feedbackCorrelationTimeout = 5 * time.Second

type feedbackContext struct {
	TaskID      string
	SessionID   string
	ContentText string
}

func feedbackCorrelationKey(conversationID, messageID string) string {
	return conversationID + "|" + messageID
}

func (a *TeamsAdapter) registerFeedback(taskID string, meta *TeamsTaskMeta, contentMessageID, contentText string) {
	if contentMessageID == "" {
		a.logger.Debug("feedback not registered: no content message", "taskID", taskID)
		return
	}
	a.feedbackCtx.Set(feedbackCorrelationKey(meta.ConversationID, contentMessageID), feedbackContext{
		TaskID: taskID, SessionID: meta.SessionID, ContentText: contentText,
	}, runtime.WithTTL(feedbackCorrelationTTL))

	if a.feedbackCorr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), feedbackCorrelationTimeout)
		defer cancel()
		if err := a.feedbackCorr.Put(ctx, meta.ConversationID, contentMessageID, taskID, meta.SessionID); err != nil {
			a.logger.Warn("feedback correlation not persisted; this answer is rateable on this instance only",
				"taskID", taskID, "error", err)
		}
	}
	a.logger.Debug("feedback registered", "taskID", taskID, "messageID", contentMessageID)
}

func (a *TeamsAdapter) feedbackCorrelation(ctx context.Context, conversationID, messageID string) (feedbackContext, bool, error) {
	if messageID == "" {
		return feedbackContext{}, false, nil
	}
	if v, ok := a.feedbackCtx.Get(feedbackCorrelationKey(conversationID, messageID)); ok {
		if fbCtx, ok := v.(feedbackContext); ok {
			return fbCtx, true, nil
		}
	}
	if a.feedbackCorr == nil {
		return feedbackContext{}, false, nil
	}
	row, found, err := a.feedbackCorr.Get(ctx, conversationID, messageID, feedbackCorrelationTTL)
	if err != nil {
		return feedbackContext{}, false, fmt.Errorf("feedback correlation lookup: %w", err)
	}
	if !found {
		return feedbackContext{}, false, nil
	}
	return feedbackContext{TaskID: row.TaskID, SessionID: row.SessionID}, true, nil
}

func taskModule(typ string, value any) *InvokeResponse {
	return &InvokeResponse{Status: 200, Body: map[string]any{
		"task": map[string]any{"type": typ, "value": value},
	}}
}

func taskModuleMessage(text string) *InvokeResponse {
	return taskModule("message", text)
}

const feedbackUnavailableNotice = "Feedback isn't available for this response."

func (a *TeamsAdapter) handleMessageFetchTask(ctx context.Context, activity *Activity) *InvokeResponse {
	var envelope struct {
		Data struct {
			ActionName  string `json:"actionName"`
			ActionValue struct {
				Reaction string `json:"reaction"`
			} `json:"actionValue"`
		} `json:"data"`
	}
	if err := json.Unmarshal(activity.Value, &envelope); err != nil {
		a.logger.Warn("invalid message/fetchTask payload", "error", err)
		return taskModuleMessage(feedbackUnavailableNotice)
	}
	payload := envelope.Data
	if payload.ActionName != "feedback" {
		a.logger.Debug("message/fetchTask for a non-feedback action", "actionName", payload.ActionName)
		return taskModuleMessage(feedbackUnavailableNotice)
	}

	var rating string
	switch payload.ActionValue.Reaction {
	case "like":
		rating = "up"
	case "dislike":
		rating = "down"
	default:
		a.logger.Warn("feedback click with unrecognised reaction; ignored",
			"reaction", payload.ActionValue.Reaction)
		return taskModuleMessage(feedbackUnavailableNotice)
	}

	ctx, cancel := context.WithTimeout(ctx, feedbackCorrelationTimeout)
	defer cancel()
	fbCtx, ok, err := a.feedbackCorrelation(ctx, activity.Conversation.ID, activity.ReplyToID)
	if err != nil {
		a.logger.Warn("feedback correlation unreadable; dialog refused", "replyToID", activity.ReplyToID, "error", err)
		return taskModuleMessage(feedbackUnavailableNotice)
	}
	if !ok || fbCtx.TaskID == "" {
		a.logger.Warn("feedback click with no correlation; dialog refused",
			"replyToID", activity.ReplyToID)
		return taskModuleMessage(feedbackUnavailableNotice)
	}

	return taskModule("continue", feedbackDialog(rating))
}

func feedbackDialog(rating string) map[string]any {
	prompt := "What did you like?"
	if rating == "down" {
		prompt = "What went wrong?"
	}

	card := adaptiveCard([]any{
		map[string]any{
			"type": "TextBlock", "text": prompt, "wrap": true, "weight": "Bolder",
		},
		map[string]any{
			"type": "Input.Text", "id": FeedbackCommentInputID,
			"placeholder": "Give as much detail as you can, but don't include any private or sensitive information.",
			"isMultiline": true, "isRequired": false,
			"maxLength": maxFeedbackCommentLen,
			"spacing":   "Small",
		},
		map[string]any{
			"type": "TextBlock", "text": entrypoint.FeedbackDataUseNotice,
			"wrap": true, "size": "Small", "isSubtle": true,
			"spacing": "Medium",
		},
	})
	card["actions"] = []any{
		map[string]any{
			"type":  "Action.Submit",
			"title": "Submit",
			"data":  map[string]any{"rating": rating},
		},
	}

	return map[string]any{
		"title": "Submit feedback",
		"width": 900,
		"card": map[string]any{
			"contentType": adaptiveCardContentType,
			"content":     card,
		},
	}
}

func (a *TeamsAdapter) feedbackSubmitOf(activity *Activity) (*Activity, bool) {
	var envelope struct {
		ActionValue struct {
			Feedback string `json:"feedback"`
		} `json:"actionValue"`
	}
	if err := json.Unmarshal(activity.Value, &envelope); err != nil || envelope.ActionValue.Feedback == "" {
		a.logger.Warn("invalid message/submitAction payload", "error", err)
		return nil, false
	}
	inner := *activity
	inner.Value = json.RawMessage(envelope.ActionValue.Feedback)
	return &inner, true
}

func (a *TeamsAdapter) handleMessageSubmitAction(ctx context.Context, activity *Activity) *InvokeResponse {
	inner, ok := a.feedbackSubmitOf(activity)
	if !ok {
		return &InvokeResponse{Status: 200, EmptyBody: true}
	}
	hold := &inboundHold{}
	if activity.ID != "" {
		payload, err := json.Marshal(activity)
		if err != nil {
			a.logger.Error("teams feedback submit cannot be encoded to record", "error", err)
			return &InvokeResponse{Status: 503, EmptyBody: true}
		}
		hold.key = inboundMessageKey(activity.Conversation.ID, activity.ID)
		claim, err := a.core.AcceptInbound(ctx, hold.key, string(payload))
		if err != nil {
			a.logger.Warn("teams feedback submit not recorded; refusing it so Bot Framework sends it again", "messageKey", hold.key, "error", err)
			return &InvokeResponse{Status: 503, EmptyBody: true}
		}
		if claim.Outcome == entrypoint.InboundSkip {
			a.logger.Debug("dropping a copy of a teams feedback submit another instance took", "messageKey", hold.key)
			return &InvokeResponse{Status: 200, EmptyBody: true}
		}
		hold.token = claim.Token
	}

	if !a.server.TrackBackground(func() { a.settleInbound(hold, a.handleFeedbackSubmit(inner, hold.key != "")) }) {
		a.logger.Warn("feedback submit refused during shutdown; it is recorded for another instance")
		return &InvokeResponse{Status: 503, EmptyBody: true}
	}

	return &InvokeResponse{Status: 200, EmptyBody: true}
}

func (a *TeamsAdapter) handleFeedbackSubmit(activity *Activity, recorded bool) (settled bool) {
	defer func() {
		if r := recover(); r != nil {
			a.logger.Error("panic while storing feedback", "panic", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), a.feedbackWorkBound())
	defer cancel()

	fbCtx, ok, err := a.feedbackCorrelation(ctx, activity.Conversation.ID, activity.ReplyToID)
	if ctx.Err() != nil {
		return false
	}
	if err != nil && recorded {
		a.logger.Warn("feedback correlation unreadable; the submit is left for another attempt", "replyToID", activity.ReplyToID, "error", err)
		return false
	}
	if err != nil {
		a.logger.Warn("feedback correlation unreadable; the submit is dropped", "replyToID", activity.ReplyToID, "error", err)
		return a.sendFeedbackNotice(ctx, activity, "", entrypoint.FeedbackNotRecordedNotice)
	}
	if !ok || fbCtx.TaskID == "" {
		a.logger.Warn("feedback submit with no correlation; dropped", "replyToID", activity.ReplyToID)
		return a.sendFeedbackNotice(ctx, activity, "", entrypoint.FeedbackNotRecordedNotice)
	}

	stored, err := a.submitFeedback(ctx, activity, fbCtx)
	if ctx.Err() != nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		a.sendFeedbackNotice(ctx, activity, fbCtx.TaskID, entrypoint.FeedbackAlreadyRecordedNotice)
	case err != nil && recorded:
		a.logger.Warn("feedback not written; the submit is left for another attempt", "taskID", fbCtx.TaskID, "error", err)
		return false
	case err != nil:
		a.logger.Error("teams feedback dropped: store failed on an unrecorded submit", "taskID", fbCtx.TaskID, "error", err)
		return a.sendFeedbackNotice(ctx, activity, fbCtx.TaskID, entrypoint.FeedbackNotRecordedNotice)
	case !stored:
		return a.sendFeedbackNotice(ctx, activity, fbCtx.TaskID, entrypoint.FeedbackNotRecordedNotice)
	}
	a.markAnswerFeedbackRecorded(ctx, activity, fbCtx)
	return true
}

func (a *TeamsAdapter) markAnswerFeedbackRecorded(ctx context.Context, activity *Activity, fbCtx feedbackContext) {
	if activity.ReplyToID == "" {
		a.logger.Warn("cannot mark the answer: the submit carries no replyToId")
		return
	}
	if fbCtx.ContentText == "" {
		a.logger.Debug("cannot mark the answer: this instance did not send it",
			"replyToID", activity.ReplyToID)
		return
	}
	meta := &TeamsTaskMeta{ServiceURL: activity.ServiceURL, ConversationID: activity.Conversation.ID}
	if meta.ServiceURL == "" || meta.ConversationID == "" {
		a.logger.Warn("cannot mark the answer: no routable conversation", "taskID", fbCtx.TaskID)
		return
	}
	a.logger.Debug("marking the answer as rated", "taskID", fbCtx.TaskID, "messageID", activity.ReplyToID)
	if err := updateTeamsMessageWithFeedback(ctx, a.botClient, meta,
		activity.ReplyToID, fbCtx.ContentText, nil, false); err != nil {
		a.logger.Warn("failed to mark the answer as rated", "taskID", fbCtx.TaskID, "error", err)
	}
}
