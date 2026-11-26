// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	goslack "github.com/slack-go/slack"
)

func (a *SlackGatewayAdapter) handleNativeFeedbackClick(callback goslack.InteractionCallback, action *goslack.BlockAction, ack func()) {
	var payload FeedbackPayload
	if err := json.Unmarshal([]byte(action.Value), &payload); err != nil {
		ack()
		a.logger.Error("invalid feedback payload", "error", err)
		channelID, userID := callback.Container.ChannelID, callback.User.ID
		a.wg.Go(func() { a.postFeedbackNotice(channelID, userID, msgFeedbackNotRecorded) })
		return
	}

	a.logger.Info("native feedback button clicked", "taskID", payload.TaskID, "rating", payload.Rating)

	metadata := nativeFeedbackModalMetadata{
		Payload:   payload,
		ChannelID: callback.Container.ChannelID,
		MessageTS: callback.Container.MessageTs,
	}
	if _, err := a.api.OpenView(a.ctx, callback.TriggerID,
		NativeFeedbackModal(payload, metadata.ChannelID, metadata.MessageTS)); err != nil {
		if a.ctx.Err() != nil {
			return
		}
		a.logger.Warn("failed to open feedback modal", "taskID", payload.TaskID, "error", err)
		ack()
		a.wg.Go(func() { a.postFeedbackNotice(metadata.ChannelID, callback.User.ID, msgFeedbackNotRecorded) })
		return
	}
	ack()
}

func (a *SlackGatewayAdapter) handleNativeFeedbackSubmit(callback goslack.InteractionCallback, ack func()) {
	var metadata nativeFeedbackModalMetadata
	if err := json.Unmarshal([]byte(callback.View.PrivateMetadata), &metadata); err != nil {
		ack()
		a.logger.Error("invalid feedback modal metadata", "error", err)
		userID := callback.User.ID
		a.wg.Go(func() { a.postFeedbackNotice("", userID, msgFeedbackNotRecorded) })
		return
	}
	payload := metadata.Payload

	comment := ""
	if callback.View.State != nil {
		if sv, ok := callback.View.State.Values[FeedbackCommentBlockID]; ok {
			if input, ok := sv[FeedbackCommentInputID]; ok {
				comment = input.Value
			}
		}
	}

	a.logger.Info("native feedback submitted", "taskID", payload.TaskID, "rating", payload.Rating,
		"commentLen", len(comment))

	a.wg.Go(func() {
		stored := a.recordFeedbackAndNotify(callback, payload, comment, metadata.ChannelID)
		if a.stoppedBeforeWrite(stored) {
			return
		}
		ack()
		if stored != "" {
			a.acknowledgeFeedbackOnMessage(metadata, stored)
		}
	})
}

func (a *SlackGatewayAdapter) acknowledgeFeedbackOnMessage(metadata nativeFeedbackModalMetadata, storedRating string) {
	if metadata.ChannelID == "" || metadata.MessageTS == "" {
		return
	}
	if _, _, _, err := a.api.UpdateMessage(a.ctx, metadata.ChannelID, metadata.MessageTS,
		goslack.MsgOptionBlocks(FeedbackAcknowledgedBlocks(storedRating)...),
		goslack.MsgOptionText(feedbackAckText(storedRating), false),
	); err != nil {
		a.logger.Warn("failed to acknowledge feedback on message",
			"taskID", metadata.Payload.TaskID, "error", err)
	}
}

func (a *SlackGatewayAdapter) resolveFeedbackUser(callback goslack.InteractionCallback, payloadUserID string) string {
	if a.emailCache == nil {
		return payloadUserID
	}
	teamID := callback.Team.ID
	if teamID == "" {
		teamID = callback.User.TeamID
	}
	if id := a.emailCache.ResolveIdentity(callback.User.ID, teamID); id != nil && id.UserID() != "" {
		return id.UserID()
	}
	return payloadUserID
}

var errFeedbackNotRecorded = errors.New("feedback not recorded")

func (a *SlackGatewayAdapter) stoppedBeforeWrite(stored string) bool {
	return stored == "" && a.ctx.Err() != nil
}

const msgFeedbackNotRecorded = "Sorry, that feedback could not be recorded. Please try again."

func (a *SlackGatewayAdapter) recordFeedbackAndNotify(callback goslack.InteractionCallback, payload FeedbackPayload, comment, channelID string) string {
	stored, err := a.recordFeedback(callback, payload, comment)
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		a.postFeedbackNotice(channelID, callback.User.ID, entrypoint.FeedbackAlreadyRecordedNotice)
		return stored
	case err != nil:
		if !a.stoppedBeforeWrite("") {
			a.postFeedbackNotice(channelID, callback.User.ID, msgFeedbackNotRecorded)
		}
		return ""
	}
	return stored
}

func (a *SlackGatewayAdapter) postFeedbackNotice(channelID, userID, text string) {
	if userID == "" {
		a.logger.Warn("cannot post feedback notice: no user id")
		return
	}
	if channelID == "" {
		dm, err := a.api.OpenDMChannel(a.ctx, userID)
		if err != nil {
			a.logger.Error("cannot post feedback notice: opening DM failed", "error", err)
			return
		}
		channelID = dm
	}
	if _, err := a.api.PostEphemeral(a.ctx, channelID, userID, goslack.MsgOptionText(text, false)); err != nil {
		a.logger.Error("failed to post feedback notice", "error", err)
	}
}

func (a *SlackGatewayAdapter) recordFeedback(callback goslack.InteractionCallback, payload FeedbackPayload, comment string) (string, error) {
	db := a.core.Store()
	if db == nil {
		return payload.Rating, nil
	}
	if payload.TaskID == "" || payload.SessionID == "" {
		a.logger.Warn("slack feedback missing task/session id; not recorded",
			"taskID", payload.TaskID, "sessionID", payload.SessionID)
		return "", errFeedbackNotRecorded
	}
	if payload.Rating != feedbackRatingUp && payload.Rating != feedbackRatingDown {
		a.logger.Warn("slack feedback invalid rating; not recorded", "taskID", payload.TaskID)
		return "", errFeedbackNotRecorded
	}

	userID := a.resolveFeedbackUser(callback, payload.UserID)
	if userID == "" {
		a.logger.Warn("slack feedback dropped: unresolved user", "taskID", payload.TaskID)
		return "", errFeedbackNotRecorded
	}

	subject, _, err := entrypoint.FeedbackSubjectForTask(a.ctx, store.NewTaskRepo(db), payload.TaskID)
	switch {
	case errors.Is(err, entrypoint.ErrFeedbackTaskNotFound):
		a.logger.Error("slack feedback dropped: task not found", "taskID", payload.TaskID, "userID", userID)
		return "", errFeedbackNotRecorded
	case errors.Is(err, entrypoint.ErrFeedbackTaskUnclassified):
		a.logger.Error("slack feedback dropped: task has no classified handler to attribute",
			"taskID", payload.TaskID, "userID", userID)
		return "", errFeedbackNotRecorded
	case err != nil:
		a.logger.Error("slack feedback dropped: task lookup failed", "taskID", payload.TaskID, "error", err)
		return "", errFeedbackNotRecorded
	}

	result, err := entrypoint.RecordFeedback(a.ctx, store.NewFeedbackRepo(db), store.FeedbackSubmission{
		SessionID:      payload.SessionID,
		TaskID:         payload.TaskID,
		UserID:         userID,
		Rating:         payload.Rating,
		Comment:        &comment,
		SubjectID:      subject.ID,
		SubjectType:    subject.Type,
		EntrypointType: store.EntrypointTypeSlack,
	}, entrypoint.FeedbackPublishParams{
		Core:      a.core,
		TaskRepo:  store.NewTaskRepo(db),
		EventRepo: store.NewTaskEventRepo(db),
		Logger:    a.logger,
	})
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		a.logger.Debug("slack feedback rejected: already submitted with different content", "taskID", payload.TaskID)
		return result.Feedback.Rating, store.ErrFeedbackConflict
	case err != nil:
		a.logger.Error("save slack feedback error", "taskID", payload.TaskID, "error", err)
		return "", errFeedbackNotRecorded
	}
	return result.Feedback.Rating, nil
}
