// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const (
	feedbackRatingUp   = "up"
	feedbackRatingDown = "down"

	feedbackRowPrefix = "fb:"
)

var positiveReactions = map[string]struct{}{"👍": {}}

var negativeReactions = map[string]struct{}{"👎": {}}

func classifyReaction(emoji string) (rating string, ok bool) {
	base := stripEmojiModifiers(emoji)
	if base == "" {
		return "", false
	}
	if _, isPos := positiveReactions[base]; isPos {
		return feedbackRatingUp, true
	}
	if _, isNeg := negativeReactions[base]; isNeg {
		return feedbackRatingDown, true
	}
	return "", false
}

func stripEmojiModifiers(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x1F3FB && r <= 0x1F3FF {
			continue
		}
		if r == 0xFE0F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

type feedbackReason struct {
	Key   string
	Title string
}

var feedbackReasons = []feedbackReason{
	{Key: "inaccurate", Title: "Inaccurate"},
	{Key: "confusing", Title: "Confusing"},
	{Key: "incomplete", Title: "Incomplete"},
	{Key: "other", Title: "Something else"},
}

func feedbackReasonTitle(key string) (string, bool) {
	for _, r := range feedbackReasons {
		if r.Key == key {
			return r.Title, true
		}
	}
	return "", false
}

func encodeFeedbackRowID(reason, taskID string) string {
	return feedbackRowPrefix + reason + ":" + taskID
}

func decodeFeedbackRowID(id string) (reason, taskID string, ok bool) {
	rest, hasPrefix := strings.CutPrefix(id, feedbackRowPrefix)
	if !hasPrefix {
		return "", "", false
	}
	reason, taskID, found := strings.Cut(rest, ":")
	if !found {
		return "", "", false
	}
	if _, known := feedbackReasonTitle(reason); !known {
		return "", "", false
	}
	if _, err := uuid.Parse(taskID); err != nil {
		return "", "", false
	}
	return reason, taskID, true
}

var errFeedbackDropped = errors.New("whatsapp feedback dropped")

func (a *Adapter) saveFeedback(row *store.WhatsAppOutbound, rating string, comment *string) (store.FeedbackResult, error) {
	db := a.core.Store()
	if db == nil {
		a.logger.Warn("whatsapp feedback not recorded: entrypoint has no store", "taskID", row.TaskID)
		a.countFeedback(fbOutcomeDropped)
		return store.FeedbackResult{}, errFeedbackDropped
	}
	ctx, cancel := a.settleCtx()
	defer cancel()

	tasks := store.NewTaskRepo(db)
	subject, _, err := entrypoint.FeedbackSubjectForTask(ctx, tasks, row.TaskID)
	switch {
	case errors.Is(err, entrypoint.ErrFeedbackTaskNotFound):
		a.logger.Error("whatsapp feedback dropped: task not found", "taskID", row.TaskID, "userFP", strutil.PIIFingerprint(row.UserID))
		a.countFeedback(fbOutcomeDropped)
		return store.FeedbackResult{}, errFeedbackDropped
	case errors.Is(err, entrypoint.ErrFeedbackTaskUnclassified):
		a.logger.Error("whatsapp feedback dropped: task has no classified handler", "taskID", row.TaskID, "userFP", strutil.PIIFingerprint(row.UserID))
		a.countFeedback(fbOutcomeDropped)
		return store.FeedbackResult{}, errFeedbackDropped
	case err != nil:
		a.logger.Error("whatsapp feedback dropped: task lookup failed", "taskID", row.TaskID, "error", err)
		a.countFeedback(fbOutcomeDropped)
		return store.FeedbackResult{}, errFeedbackDropped
	}

	result, err := entrypoint.RecordFeedback(ctx, store.NewFeedbackRepo(db), store.FeedbackSubmission{
		TaskID:         row.TaskID,
		SessionID:      row.SessionID,
		UserID:         row.UserID,
		Rating:         rating,
		Comment:        comment,
		SubjectID:      subject.ID,
		SubjectType:    subject.Type,
		EntrypointType: store.EntrypointTypeWhatsApp,
	}, entrypoint.FeedbackPublishParams{
		Core:      a.core,
		TaskRepo:  tasks,
		EventRepo: store.NewTaskEventRepo(db),
		Logger:    a.logger,
	})
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		a.logger.Debug("whatsapp feedback rejected: already submitted with different content", "taskID", row.TaskID)
		a.countFeedback(fbOutcomeConflict)
		return result, store.ErrFeedbackConflict
	case err != nil:
		a.logger.Error("whatsapp feedback dropped: save failed", "taskID", row.TaskID, "error", err)
		a.countFeedback(fbOutcomeDropped)
		return store.FeedbackResult{}, errFeedbackDropped
	}
	return result, nil
}

func (a *Adapter) revokeFeedback(row *store.WhatsAppOutbound) (bool, error) {
	db := a.core.Store()
	if db == nil {
		a.logger.Warn("whatsapp feedback not withdrawn: entrypoint has no store", "taskID", row.TaskID)
		return false, errFeedbackDropped
	}
	ctx, cancel := a.settleCtx()
	defer cancel()

	deleted, err := store.NewFeedbackRepo(db).Delete(ctx, row.TaskID, row.UserID)
	if err != nil {
		a.logger.Error("whatsapp feedback withdrawal failed", "taskID", row.TaskID, "error", err)
		return false, errFeedbackDropped
	}
	return deleted, nil
}

func (a *Adapter) handleFeedbackRevoke(row *store.WhatsAppOutbound) inboundOutcome {
	deleted, err := a.revokeFeedback(row)
	if err != nil {
		a.countFeedback(fbOutcomeDropped)
		a.replyForRow(row, msgFeedbackWithdrawFailed)
		return outcomeDeclined
	}
	if deleted {
		a.countFeedback(fbOutcomeRevoked)
		a.logger.Info("whatsapp feedback withdrawn",
			"taskID", row.TaskID, "userFP", strutil.PIIFingerprint(row.UserID))
	} else {
		a.countFeedback(fbOutcomeRemovedNoop)
	}
	return outcomeDeclined
}

func (a *Adapter) countFeedback(outcome string) {
	a.meter().Counter(metricWhatsAppFeedback, 1, "gatewayId", a.gatewayID(), "outcome", outcome)
}

func isRatableKind(kind string) bool {
	return kind == kindText || kind == kindMedia
}

func feedbackAck(lead string) string {
	return lead + "\n" + msgFeedbackCardFooter + "\n" + msgFeedbackWithdraw
}

func feedbackConflictNotice() string {
	return entrypoint.FeedbackAlreadyRecordedNotice + "\n" + msgFeedbackWithdraw
}

func (a *Adapter) handleReaction(msg *inboundMessage) inboundOutcome {
	if msg.Reaction == nil || msg.Reaction.MessageID == "" {
		a.logger.Debug("reaction ignored: no target message", "wamid", msg.ID)
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}
	removal := msg.Reaction.Emoji == ""
	var rating string
	if !removal {
		var ok bool
		rating, ok = classifyReaction(msg.Reaction.Emoji)
		if !ok {
			a.logger.Debug("reaction ignored: emoji not in the sentiment table", "wamid", msg.ID)
			a.countFeedback(fbOutcomeIgnoredUnclassified)
			return outcomeDeclined
		}
	}

	ctx, cancel := a.settleCtx()
	row, err := a.outbound.GetByWamid(ctx, a.gatewayID(), msg.Reaction.MessageID)
	cancel()
	if err != nil {
		a.logger.Error("reaction ignored: outbound lookup failed", "wamid", msg.ID, "error", err)
		a.countFeedback(fbOutcomeDropped)
		return outcomeDeclined
	}
	if row == nil || row.TaskID == "" {
		a.logger.Debug("reaction ignored: not on a tracked task message", "wamid", msg.ID)
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}
	if !isRatableKind(row.Kind) || row.UserID == "" {
		a.logger.Debug("reaction ignored: not on an agent reply", "wamid", msg.ID, "kind", row.Kind)
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}
	if row.Recipient != msg.senderID() {
		a.logger.Warn("reaction ignored: sender is not the message recipient", "wamid", msg.ID, "userFP", strutil.PIIFingerprint(msg.senderID()))
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}

	if removal {
		return a.handleFeedbackRevoke(row)
	}

	result, err := a.saveFeedback(row, rating, nil)
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		a.replyForRow(row, feedbackConflictNotice())
		return outcomeDeclined
	case err != nil:
		return outcomeDeclined
	}
	fb := result.Feedback
	if result.Changed() {
		outcome := fbOutcomeDown
		if rating == feedbackRatingUp {
			outcome = fbOutcomeUp
		}
		a.countFeedback(outcome)
		a.logger.Info("whatsapp feedback recorded from reaction",
			"taskID", row.TaskID, "rating", rating, "userFP", strutil.PIIFingerprint(row.UserID))
	} else {
		a.countFeedback(fbOutcomeUnchanged)
	}

	switch {
	case rating == feedbackRatingUp:
		a.replyForRow(row, feedbackAck(msgFeedbackThanks))
	case fb.Comment != nil:
		a.replyForRow(row, feedbackAck(msgFeedbackNoted))
	default:
		a.sendReasonCard(row)
	}
	return outcomeDeclined
}

func targetFromRow(row *store.WhatsAppOutbound) outboundTarget {
	return outboundTarget{
		recipient: row.Recipient, taskID: row.TaskID, traceID: row.TraceID,
		userID: row.UserID, agentName: row.AgentName, sessionID: row.SessionID,
	}
}

func (a *Adapter) replyForRow(row *store.WhatsAppOutbound, text string) {
	a.enqueueText(kindNotice, targetFromRow(row), text)
}

func (a *Adapter) sendReasonCard(row *store.WhatsAppOutbound) {
	rows := make([]listRow, 0, len(feedbackReasons))
	for _, r := range feedbackReasons {
		rows = append(rows, listRow{ID: encodeFeedbackRowID(r.Key, row.TaskID), Title: r.Title})
	}
	id := a.enqueue(kindFeedbackPrompt, targetFromRow(row), outboundPayload{
		Text: msgFeedbackCardBody,
		Prompt: &promptPayload{
			Kind:       "feedback",
			Mode:       promptModeList,
			ButtonText: msgFeedbackCardButton,
			Rows:       rows,
			Footer:     msgFeedbackCardFooter,
		},
	}, "")
	if id == "" {
		a.logger.Warn("feedback reason card could not be queued", "taskID", row.TaskID)
	}
}

func feedbackListReplyID(msg *inboundMessage) (string, bool) {
	if msg.Type != "interactive" || msg.Interactive == nil || msg.Interactive.ListReply == nil {
		return "", false
	}
	id := msg.Interactive.ListReply.ID
	if _, _, ok := decodeFeedbackRowID(id); !ok {
		return "", false
	}
	return id, true
}

func (a *Adapter) handleFeedbackListReply(msg *inboundMessage) inboundOutcome {
	reasonKey, taskID, ok := decodeFeedbackRowID(msg.Interactive.ListReply.ID)
	if !ok {
		return outcomeDeclined
	}
	if msg.Context == nil || msg.Context.ID == "" {
		a.logger.Warn("feedback reason ignored: tap carries no context id", "taskID", taskID)
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}
	ctx, cancel := a.settleCtx()
	card, err := a.outbound.GetByWamid(ctx, a.gatewayID(), msg.Context.ID)
	cancel()
	if err != nil {
		a.logger.Error("feedback reason ignored: card lookup failed", "taskID", taskID, "error", err)
		a.countFeedback(fbOutcomeDropped)
		return outcomeDeclined
	}
	if card == nil {
		a.logger.Debug("feedback reason ignored: card row not found", "taskID", taskID)
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}
	if card.Kind != kindFeedbackPrompt || card.TaskID != taskID {
		a.logger.Warn("feedback reason ignored: row id does not match the card it answers",
			"taskID", taskID, "cardTaskID", card.TaskID, "cardKind", card.Kind)
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}
	if card.Recipient != msg.senderID() {
		a.logger.Warn("feedback reason ignored: sender is not the message recipient", "taskID", taskID, "userFP", strutil.PIIFingerprint(msg.senderID()))
		a.countFeedback(fbOutcomeIgnoredUntracked)
		return outcomeDeclined
	}
	title, _ := feedbackReasonTitle(reasonKey)
	result, err := a.saveFeedback(card, feedbackRatingDown, &title)
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		a.replyForRow(card, feedbackConflictNotice())
		return outcomeDeclined
	case err != nil:
		return outcomeDeclined
	}
	if result.Changed() {
		a.countFeedback(fbOutcomeReason)
		a.logger.Info("whatsapp feedback reason recorded",
			"taskID", taskID, "commentLen", len(title), "userFP", strutil.PIIFingerprint(card.UserID))
	} else {
		a.countFeedback(fbOutcomeUnchanged)
	}
	a.replyForRow(card, feedbackAck(msgFeedbackNoted))
	return outcomeDeclined
}
