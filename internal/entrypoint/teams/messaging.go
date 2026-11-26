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
)

var (
	errNilBotClient = errors.New("nil BotClient")
	errNilMeta      = errors.New("nil meta")
)

var nonRetryableBotStatusCodes = map[int]struct{}{
	400: {}, 401: {}, 403: {}, 404: {}, 405: {}, 409: {}, 410: {}, 422: {},
}

func isNonRetryableBotStatus(code int) bool {
	_, ok := nonRetryableBotStatusCodes[code]
	return ok
}

func withFeedbackMarker(activity *Activity, feedback bool) *Activity {
	if feedback {
		activity.ChannelData = json.RawMessage(`{"feedbackLoop":{"type":"custom"}}`)
	}
	return activity
}

func sendTeamsMessage(
	ctx context.Context,
	bc *BotClient,
	meta *TeamsTaskMeta,
	text string,
	card *Attachment,
	replyToID string,
) (string, error) {
	return sendTeamsMessageWithFeedback(ctx, bc, meta, text, card, replyToID, false)
}

func sendTeamsMessageWithFeedback(
	ctx context.Context,
	bc *BotClient,
	meta *TeamsTaskMeta,
	text string,
	card *Attachment,
	replyToID string,
	feedback bool,
) (string, error) {
	if bc == nil {
		return "", errNilBotClient
	}
	if meta == nil {
		return "", errNilMeta
	}
	activity := withFeedbackMarker(buildActivityWithCard(text, card, replyToID), feedback)
	return bc.SendActivity(ctx, meta.ServiceURL, meta.ConversationID, activity)
}

func updateTeamsMessage(
	ctx context.Context,
	bc *BotClient,
	meta *TeamsTaskMeta,
	activityID, text string,
	card *Attachment,
) error {
	return updateTeamsMessageWithFeedback(ctx, bc, meta, activityID, text, card, false)
}

func updateTeamsMessageWithFeedback(
	ctx context.Context,
	bc *BotClient,
	meta *TeamsTaskMeta,
	activityID, text string,
	card *Attachment,
	feedback bool,
) error {
	if bc == nil {
		return errNilBotClient
	}
	if meta == nil {
		return errNilMeta
	}
	if activityID == "" {
		return fmt.Errorf("empty activityID")
	}
	activity := withFeedbackMarker(buildActivityWithCard(text, card, ""), feedback)
	return bc.UpdateActivity(ctx, meta.ServiceURL, meta.ConversationID, activityID, activity)
}

func (a *TeamsAdapter) retireClickedCard(ctx context.Context, activity *Activity, text string) {
	if activity.ReplyToID == "" {
		return
	}
	meta := &TeamsTaskMeta{ConversationID: activity.Conversation.ID, ServiceURL: activity.ServiceURL}
	if err := updateTeamsMessage(ctx, a.botClient, meta, activity.ReplyToID, text, nil); err != nil {
		a.logger.Warn("failed to retire the answered card", "error", err)
	}
}

func deleteTeamsMessage(
	ctx context.Context,
	bc *BotClient,
	meta *TeamsTaskMeta,
	activityID string,
) error {
	if bc == nil {
		return errNilBotClient
	}
	if meta == nil {
		return errNilMeta
	}
	if activityID == "" {
		return fmt.Errorf("empty activityID")
	}
	return bc.DeleteActivity(ctx, meta.ServiceURL, meta.ConversationID, activityID)
}

func sendTypingIndicator(ctx context.Context, bc *BotClient, meta *TeamsTaskMeta, botAppID string) {
	if bc == nil || meta == nil {
		return
	}
	if err := bc.SendTyping(ctx, meta.ServiceURL, meta.ConversationID, Account{ID: botAppID}); err != nil {
		bc.logger.Debug("typing indicator failed (non-critical)", "error", err)
	}
}

func buildActivityWithCard(text string, card *Attachment, replyToID string) *Activity {
	activity := &Activity{
		Type: ActivityTypeMessage,
		Text: text,
	}
	if replyToID != "" {
		activity.ReplyToID = replyToID
	}
	if card != nil {
		activity.Attachments = []Attachment{*card}
	}
	return activity
}
