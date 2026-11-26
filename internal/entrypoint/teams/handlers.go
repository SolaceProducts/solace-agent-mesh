// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"strings"
)

func (a *TeamsAdapter) handleActivity(ctx context.Context, activity *Activity) *InvokeResponse {
	switch activity.Type {
	case ActivityTypeMessage:
		a.handleMessageActivity(ctx, activity)
	case ActivityTypeInvoke:
		return a.handleInvokeActivity(ctx, activity)
	case ActivityTypeConversationUpdate:
		a.logger.Debug("conversation update received", "conversation", activity.Conversation.ID)
	default:
		a.logger.Debug("unhandled activity type", "type", activity.Type)
	}
	return nil
}

func (a *TeamsAdapter) handleMessageActivity(ctx context.Context, activity *Activity) {
	if activity.From.ID == a.cfg.AppID {
		return
	}
	if isCardClick(activity) {
		a.handleAdaptiveCardAction(activity)
		return
	}
	text, files, ok := a.taskMessage(activity)
	if !ok {
		return
	}
	a.submitTeamsTask(ctx, activity, text, files, nil)
}

func isCardClick(activity *Activity) bool {
	return len(activity.Value) > 0
}

func (a *TeamsAdapter) taskMessage(activity *Activity) (text string, files []Attachment, ok bool) {
	if activity.Type != ActivityTypeMessage || activity.From.ID == a.cfg.AppID || isCardClick(activity) {
		return "", nil, false
	}

	text = activity.Text

	if activity.Conversation.ConversationType != ConversationTypePersonal &&
		activity.Conversation.ConversationType != "" {
		botID := activity.Recipient.ID
		if botID == "" {
			a.logger.Warn("teams activity has no recipient ID; cannot evaluate bot mention",
				"conversationID", activity.Conversation.ID)
			return "", nil, false
		}
		if !isBotMentioned(activity, botID) {
			return "", nil, false
		}
		text = stripBotMention(text, activity, botID)
	}

	text = strings.TrimSpace(text)

	for _, att := range activity.Attachments {
		if isFileAttachment(att) {
			files = append(files, att)
		}
	}

	if text == "" && len(files) == 0 {
		return "", nil, false
	}
	return text, files, true
}

func isFileAttachment(att Attachment) bool {
	if att.ContentType == "application/vnd.microsoft.teams.file.download.info" {
		return true
	}
	if att.ContentURL != "" {
		return true
	}
	return false
}

func (a *TeamsAdapter) handleInvokeActivity(ctx context.Context, activity *Activity) *InvokeResponse {
	switch activity.Name {
	case InvokeFileConsent, InvokeTeamsFileConsent:
		return a.handleFileConsentResponse(ctx, activity)
	case InvokeMessageFetchTask:
		return a.handleMessageFetchTask(ctx, activity)
	case InvokeMessageSubmitAction:
		return a.handleMessageSubmitAction(ctx, activity)
	default:
		a.handleAdaptiveCardAction(activity)
	}
	return nil
}

func (a *TeamsAdapter) handleAdaptiveCardAction(activity *Activity) {
	if activity.Value == nil {
		return
	}

	var data AdaptiveCardActionData
	if err := json.Unmarshal(activity.Value, &data); err != nil {
		a.logger.Warn("invalid adaptive card action data", "error", err)
		return
	}

	switch data.Action {
	case "a2a_cancel_request_action":
		if data.TaskID != "" {
			a.logger.Info("cancel request from Teams", "taskID", data.TaskID)
			agentName := a.core.Tasks().AgentName(data.TaskID)
			if err := a.core.CancelTask(a.ctx, data.TaskID, agentName); err != nil {
				a.logger.Error("failed to cancel task", "taskID", data.TaskID, "error", err)
			}
		}
	case HILSubmitAction:
		a.handleHILSubmit(activity)
	case HILCancelAction:
		a.handleHILCancel(activity)
	case FeedbackActionID:
		a.logger.Warn("submit from a retired feedback card; not recorded", "taskID", data.TaskID)
		ctx, cancel := context.WithTimeout(context.Background(), a.feedbackWorkBound())
		defer cancel()
		a.sendFeedbackNotice(ctx, activity, data.TaskID, retiredFeedbackCardNotice)
	default:
		a.logger.Debug("unhandled adaptive card action", "action", data.Action)
	}
}

func (a *TeamsAdapter) handleFileConsentResponse(ctx context.Context, activity *Activity) *InvokeResponse {
	consent, ok := a.fileConsentOf(activity)
	if !ok {
		return nil
	}
	switch consent.Action {
	case "accept":
		return a.receiveFileConsentAccept(ctx, activity, consent)
	case "decline":
		a.handleFileConsentDecline(activity, consent)
	}
	return nil
}

func (a *TeamsAdapter) fileConsentOf(activity *Activity) (*FileConsentCardResponse, bool) {
	if activity.Value == nil {
		return nil, false
	}
	var consent FileConsentCardResponse
	if err := json.Unmarshal(activity.Value, &consent); err != nil {
		a.logger.Warn("invalid file consent response", "error", err)
		return nil, false
	}
	return &consent, true
}

func isBotMentioned(activity *Activity, botID string) bool {
	for _, entity := range activity.Entities {
		if entity.Type == "mention" && entity.Mentioned != nil {
			if entity.Mentioned.ID == botID {
				return true
			}
		}
	}
	return false
}

func stripBotMention(text string, activity *Activity, botID string) string {
	for _, entity := range activity.Entities {
		if entity.Type != "mention" || entity.Mentioned == nil {
			continue
		}
		if entity.Mentioned.ID == botID && entity.Text != "" {
			text = strings.ReplaceAll(text, entity.Text, "")
		}
	}
	return strings.TrimSpace(text)
}
