// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const defaultFeedbackWorkTimeout = 10 * time.Second

func (a *TeamsAdapter) feedbackWorkBound() time.Duration {
	return cmp.Or(a.feedbackWorkTimeout, defaultFeedbackWorkTimeout)
}

type feedbackSubmitData struct {
	Rating  string `json:"rating"`
	Comment string `json:"comment"`
}

const maxFeedbackCommentLen = 2000

func (a *TeamsAdapter) submitFeedback(ctx context.Context, activity *Activity, fbCtx feedbackContext) (bool, error) {
	if activity.Value == nil {
		return false, nil
	}
	var data feedbackSubmitData
	if err := json.Unmarshal(activity.Value, &data); err != nil {
		a.logger.Warn("invalid feedback submit data", "error", err)
		return false, nil
	}
	if data.Rating != "up" && data.Rating != "down" {
		a.logger.Warn("feedback submit with unexpected rating; ignored", "taskID", fbCtx.TaskID)
		return false, nil
	}
	if len(data.Comment) > maxFeedbackCommentLen {
		a.logger.Warn("feedback comment truncated", "taskID", fbCtx.TaskID,
			"length", len(data.Comment), "limit", maxFeedbackCommentLen)
		data.Comment = data.Comment[:runeBoundaryAtOrBefore(data.Comment, maxFeedbackCommentLen)]
	}
	return a.recordFeedback(ctx, activity, fbCtx, data)
}

func runeBoundaryAtOrBefore(s string, limit int) int {
	cut := limit
	for cut > 0 && cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

func (a *TeamsAdapter) sendFeedbackNotice(ctx context.Context, activity *Activity, taskID, text string) bool {
	if activity.ServiceURL == "" || activity.Conversation.ID == "" {
		a.logger.Debug("feedback notice skipped: no routable conversation", "taskID", taskID)
		return false
	}
	meta := &TeamsTaskMeta{ServiceURL: activity.ServiceURL, ConversationID: activity.Conversation.ID}
	if _, err := sendTeamsMessage(ctx, a.botClient, meta, text, nil, ""); err != nil {
		a.logger.Warn("feedback notice send failed", "taskID", taskID, "error", err)
		return false
	}
	return true
}

func (a *TeamsAdapter) recordFeedback(ctx context.Context, activity *Activity, fbCtx feedbackContext, data feedbackSubmitData) (bool, error) {
	db := a.core.Store()
	if db == nil {
		return false, nil
	}
	if fbCtx.TaskID == "" || fbCtx.SessionID == "" {
		a.logger.Warn("teams feedback missing task/session id; not recorded",
			"taskID", fbCtx.TaskID, "sessionID", fbCtx.SessionID)
		return false, nil
	}

	if activity.From.ID == "" && activity.From.AadObjectID == "" {
		a.logger.Warn("teams feedback dropped: no identity material on activity", "taskID", fbCtx.TaskID)
		return false, nil
	}
	userID := ""
	if a.emailCache != nil {
		id := a.emailCache.ResolveIdentity(ctx, activity.From.ID, activity.From.Name,
			activity.From.AadObjectID, activity.ServiceURL, activity.Conversation.ID)
		if id != nil {
			userID = id.UserID()
		}
	}
	if userID == "" {
		a.logger.Warn("teams feedback dropped: identity resolution returned nothing", "taskID", fbCtx.TaskID)
		return false, nil
	}

	subject, _, err := entrypoint.FeedbackSubjectForTask(ctx, store.NewTaskRepo(db), fbCtx.TaskID)
	switch {
	case errors.Is(err, entrypoint.ErrFeedbackTaskNotFound):
		a.logger.Error("teams feedback dropped: task not found", "taskID", fbCtx.TaskID, "userID", userID)
		return false, nil
	case errors.Is(err, entrypoint.ErrFeedbackTaskUnclassified):
		a.logger.Error("teams feedback dropped: task has no classified handler to attribute",
			"taskID", fbCtx.TaskID, "userID", userID)
		return false, nil
	case err != nil:
		return false, fmt.Errorf("teams feedback task lookup: %w", err)
	}

	_, err = entrypoint.RecordFeedback(ctx, store.NewFeedbackRepo(db), store.FeedbackSubmission{
		SessionID:      fbCtx.SessionID,
		TaskID:         fbCtx.TaskID,
		UserID:         userID,
		Rating:         data.Rating,
		Comment:        &data.Comment,
		SubjectID:      subject.ID,
		SubjectType:    subject.Type,
		EntrypointType: store.EntrypointTypeTeamsBot,
	}, entrypoint.FeedbackPublishParams{
		Core:      a.core,
		TaskRepo:  store.NewTaskRepo(db),
		EventRepo: store.NewTaskEventRepo(db),
		Logger:    a.logger,
	})
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		a.logger.Debug("teams feedback rejected: already submitted with different content", "taskID", fbCtx.TaskID)
		return true, store.ErrFeedbackConflict
	case err != nil:
		return false, fmt.Errorf("save teams feedback: %w", err)
	}
	return true, nil
}
