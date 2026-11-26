// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const (
	msgHILSubmitted             = "✅ Response submitted."
	msgHILCancelled             = "🛑 Request cancelled."
	msgHILSubmitFailed          = "⚠️ Your response couldn't be submitted. Please try again."
	msgClickFailed              = "Sorry, that didn't go through. Please try again."
	msgClickFailedLater         = "Sorry, that didn't go through. Please try again later."
	msgClickDeclined            = "Sorry, that action can't be completed."
	msgFeedbackUnavailable      = "Sorry, feedback can't be given on this answer."
	msgFeedbackStoredCardFailed = "Your feedback was recorded, but this card couldn't be updated."
	msgNotYourTask              = "Only the person who asked can do that."
	msgRequestEnded             = "This request has already ended."
	msgRequestNotFound          = "I couldn't find this request. It may have already ended."
	msgNotYourFeedback          = "Only the person who chose this rating can submit or cancel it."
	msgFeedbackNotRecorded      = "Sorry, your feedback could not be recorded. Please try again."
	msgCancelling               = "🛑 Cancelling…"
)

func (a *Adapter) processCardClick(ctx context.Context, ev *inboundEvent) processResult {
	verdict := a.authorizeSender(ctx, ev)
	if verdict.Retryable {
		return processResult{outcome: outcomeAuthzUnavailable}
	}
	if verdict.Identity == nil {
		a.refuseSender(ev, verdict)
		return processResult{outcome: outcomeDeclined}
	}

	switch ev.Action[actionMethodParam] {
	case actionCancelTask:
		return a.clickCancel(ctx, ev)
	case actionHILSubmit, actionHILCancel:
		if ev.Action[paramNotify] != "" {
			return a.clickNotifyHIL(ctx, ev)
		}
		return a.clickHIL(ctx, ev, verdict.Identity)
	case actionFeedback:
		return a.clickFeedback(ctx, ev)
	case actionFeedbackCancel:
		return a.clickFeedbackCancel(ctx, ev)
	case actionFeedbackDetails:
		return a.clickFeedbackDetails(ctx, ev, verdict.Identity)
	}
	a.logger.Debug("google chat card click for an unknown action ignored", "actionLen", len(ev.Action[actionMethodParam]))
	return processResult{outcome: outcomeIgnored}
}

type clickStanding int

const (
	standingUnresolved clickStanding = iota
	standingBystander
	standingTaskEnded
	standingTaskNotFound
	standingRequester
)

func (a *Adapter) requesterOf(ctx context.Context, ev *inboundEvent, taskID string) (taskMeta, clickStanding, error) {
	a.tasksMu.Lock()
	st := a.tasks[taskID]
	a.tasksMu.Unlock()
	var meta taskMeta
	switch {
	case st != nil && st.terminal.Load():
		return taskMeta{}, standingTaskEnded, nil
	case st != nil:
		meta = st.snapshot()
	case !isCanonicalUUID(taskID):
		return taskMeta{}, standingTaskNotFound, nil
	default:
		stored, state, err := a.readTaskItems(ctx, taskID)
		if err != nil {
			if ctx.Err() != nil {
				a.logger.Debug("google chat card click: the task items read was cancelled; the click is retried", "taskID", taskID)
			} else {
				a.logger.Warn("google chat card click: the task items could not be read; the click is retried", "taskID", taskID, "error", err)
			}
			return taskMeta{}, standingUnresolved, err
		}
		if state == destinationUndecodable {
			a.logger.Warn("google chat card click: the task destination cannot be decoded", "taskID", taskID)
		}
		if state != destinationFound {
			if a.taskEnded(ctx, taskID) {
				return taskMeta{}, standingTaskEnded, nil
			}
			return taskMeta{}, standingTaskNotFound, nil
		}
		meta = stored
	}
	if ev.SenderName == "" || meta.SenderName != ev.SenderName {
		return meta, standingBystander, nil
	}
	return meta, standingRequester, nil
}

func isCanonicalUUID(s string) bool {
	return len(s) == 36 && uuid.Validate(s) == nil
}

func (a *Adapter) taskEnded(ctx context.Context, taskID string) bool {
	if a.core == nil || a.core.Store() == nil {
		return false
	}
	task, err := store.NewTaskRepo(a.core.Store()).GetByID(ctx, taskID)
	if err != nil {
		a.logger.Warn("google chat card click: the task log could not be read", "taskID", taskID, "error", err)
		return false
	}
	return task != nil && task.EndTime != nil
}

func (a *Adapter) refuseClick(ev *inboundEvent, taskID string, standing clickStanding) (processResult, bool) {
	action := ev.Action[actionMethodParam]
	switch standing {
	case standingRequester:
		return processResult{}, false
	case standingTaskEnded:
		a.logger.Debug("google chat card click on a task that has ended", "taskID", taskID, "action", action)
		if action == actionCancelTask {
			a.replaceClickedCard(ev, "", nil)
			a.postNotice(ev.SpaceName, ev.ThreadName, msgRequestEnded)
		} else {
			a.replaceClickedCard(ev, msgRequestEnded, nil)
		}
	case standingTaskNotFound:
		a.logger.Warn("google chat card click on a task with no task items; the card is left in place", "taskID", taskID, "action", action)
		a.postNotice(ev.SpaceName, ev.ThreadName, msgRequestNotFound)
	default:
		a.logger.Warn("google chat card click refused: clicker did not start the task", "taskID", taskID, "action", action, "clickerFP", strutil.PIIFingerprint(ev.SenderName))
		a.postNotice(ev.SpaceName, ev.ThreadName, msgNotYourTask)
	}
	return processResult{outcome: outcomeDeclined}, true
}

func (a *Adapter) clickCancel(ctx context.Context, ev *inboundEvent) processResult {
	taskID := ev.Action[paramTaskID]
	meta, standing, err := a.requesterOf(ctx, ev, taskID)
	if err != nil {
		return processResult{outcome: outcomeTransient}
	}
	if res, refused := a.refuseClick(ev, taskID, standing); refused {
		return res
	}
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), taskCancelTimeout)
	defer cancel()
	if err := a.core.CancelTask(cancelCtx, taskID, cmp.Or(meta.AgentName, ev.Action[paramAgentName])); err != nil {
		a.logger.Warn("google chat task cancel failed", "taskID", taskID, "error", err)
		return processResult{outcome: outcomeTransient}
	}
	a.logger.Info("google chat task cancel requested", "taskID", taskID)
	a.replaceClickedCard(ev, "", nil)
	a.postNotice(ev.SpaceName, ev.ThreadName, msgCancelling)
	return processResult{outcome: outcomeSubmitted}
}

func (a *Adapter) clickHIL(ctx context.Context, ev *inboundEvent, clicker *auth.Subject) processResult {
	taskID, requestID := ev.Action[paramTaskID], ev.Action[paramRequestID]
	if requestID == "" || taskID == "" {
		return processResult{outcome: outcomeIgnored}
	}
	meta, standing, err := a.requesterOf(ctx, ev, taskID)
	if err != nil {
		return processResult{outcome: outcomeTransient}
	}
	if res, refused := a.refuseClick(ev, taskID, standing); refused {
		return res
	}

	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: requestID, Status: "cancelled"}
	ack := msgHILCancelled
	if ev.Action[actionMethodParam] == actionHILSubmit {
		data, decodeErr := hilResponseData(ev.Action, ev.FormInputs)
		if decodeErr != nil {
			a.logger.Warn("google chat HIL card carried an undecodable form description; dropping the click", "taskID", taskID, "requestID", requestID, "error", decodeErr)
			return processResult{outcome: outcomeIgnored}
		}
		resp.Status, resp.Data, ack = "answered", data, msgHILSubmitted
	}

	identity := auth.ForGoogleChatResponder(clicker, meta.RunAsSwapped, a.cfg.RunAs)
	agentName := cmp.Or(meta.AgentName, ev.Action[paramAgentName])
	answerUserID := a.core.PromptAnswerUserID(taskID, identity.UserID(),
		entrypoint.ChatPlace{Conversation: meta.SpaceName, User: meta.SenderName},
		entrypoint.ChatPlace{Conversation: ev.SpaceName, User: ev.SenderName})
	if err := a.core.SubmitUserInputResponseAttributedTo(a.ctx, agentName, taskID, resp, identity, answerUserID); err != nil {
		if entrypoint.IsAuthorizationDenial(err) {
			return processResult{outcome: outcomeUnauthorized, decline: a.core.SubmitFailureText(err, true)}
		}
		a.logger.Warn("google chat HIL response submit failed", "taskID", taskID, "requestID", requestID, "error", err)
		return processResult{outcome: outcomeTransient, decline: msgHILSubmitFailed}
	}
	a.logger.Debug("google chat HIL response submitted", "taskID", taskID, "requestID", requestID, "hilStatus", resp.Status)
	a.replaceClickedCard(ev, ack, nil)
	return processResult{outcome: outcomeSubmitted}
}

func (a *Adapter) clickFeedback(ctx context.Context, ev *inboundEvent) processResult {
	taskID, sessionID, rating := ev.Action[paramTaskID], ev.Action[paramSessionID], ev.Action[paramRating]
	if !slices.Contains([]string{ratingUp, ratingDown}, rating) || taskID == "" || sessionID == "" || ev.SenderName == "" {
		a.logger.Debug("google chat feedback thumb ignored: it names no usable task, rating or person", "action", ev.Action[actionMethodParam], "taskIDLen", len(taskID), "hasSender", ev.SenderName != "")
		return processResult{outcome: outcomeIgnored}
	}
	details := feedbackDetailsCard(taskID, sessionID, rating, ev.SenderName)
	return a.showFeedbackCard(ctx, ev, taskID, a.routeButtons(details, taskMeta{taskDestination: taskDestination{AddOn: ev.AddOn}}), "")
}

func (a *Adapter) clickFeedbackCancel(ctx context.Context, ev *inboundEvent) processResult {
	taskID, sessionID := ev.Action[paramTaskID], ev.Action[paramSessionID]
	if taskID == "" || sessionID == "" {
		a.logger.Debug("google chat feedback cancel ignored: it names no usable task", "action", ev.Action[actionMethodParam], "taskIDLen", len(taskID))
		return processResult{outcome: outcomeIgnored}
	}
	if !a.clickedByRater(ev, taskID) {
		return processResult{outcome: outcomeDeclined}
	}
	thumbs := feedbackCard(taskID, sessionID)
	return a.showFeedbackCard(ctx, ev, taskID, a.routeButtons(thumbs, taskMeta{taskDestination: taskDestination{AddOn: ev.AddOn}}), "")
}

func (a *Adapter) showFeedbackCard(ctx context.Context, ev *inboundEvent, taskID string, cards []*chat.CardWithId, failureText string) processResult {
	action := ev.Action[actionMethodParam]
	permanentText, transientText := msgFeedbackUnavailable, msgClickFailed
	if failureText != "" {
		permanentText, transientText = failureText, failureText
	}
	if ev.MessageName == "" {
		a.logger.Warn("google chat feedback card not shown: the click names no message", append(taskField(taskID), "action", action)...)
		return processResult{outcome: outcomeIgnored}
	}
	writeCtx, cancel := context.WithTimeout(ctx, feedbackCardWriteTimeout)
	defer cancel()
	err := a.writer.updateOnce(writeCtx, ev.SpaceName, ev.MessageName, messageContent{Cards: cards}, true)
	if err == nil {
		return processResult{outcome: outcomeSubmitted}
	}
	if _, permanent := errors.AsType[*permanentError](err); permanent {
		a.logger.Warn("google chat feedback card refused by chat", append(taskField(taskID), "action", action, "error", err)...)
		a.postNotice(ev.SpaceName, ev.ThreadName, permanentText)
		return processResult{outcome: outcomeDeclined}
	}
	switch {
	case errors.Is(err, errMessageGone):
		a.logger.Debug("google chat feedback card not shown: the message is gone", append(taskField(taskID), "action", action)...)
		return processResult{outcome: outcomeSubmitted}
	case ctx.Err() != nil:
		a.logger.Debug("google chat feedback card not shown: the click's context ended", append(taskField(taskID), "action", action)...)
	default:
		a.logger.Warn("google chat feedback card could not be shown; the click is retried", append(taskField(taskID), "action", action, "error", err)...)
	}
	return processResult{outcome: outcomeTransient, decline: transientText}
}

func (a *Adapter) clickedByRater(ev *inboundEvent, taskID string) bool {
	if ev.SenderName != "" && ev.SenderName == ev.Action[paramRater] {
		return true
	}
	a.logger.Warn("google chat feedback card refused: clicker did not choose the rating", append(taskField(taskID), "clickerFP", strutil.PIIFingerprint(ev.SenderName))...)
	a.postNotice(ev.SpaceName, ev.ThreadName, msgNotYourFeedback)
	return false
}

func (a *Adapter) clickFeedbackDetails(ctx context.Context, ev *inboundEvent, clicker *auth.Subject) processResult {
	taskID, sessionID, rating := ev.Action[paramTaskID], ev.Action[paramSessionID], ev.Action[paramRating]
	if !slices.Contains([]string{ratingUp, ratingDown}, rating) || taskID == "" || sessionID == "" {
		return processResult{outcome: outcomeIgnored}
	}
	if !a.clickedByRater(ev, taskID) {
		return processResult{outcome: outcomeDeclined}
	}
	comment := strings.TrimSpace(first(ev.FormInputs[feedbackCommentInput]))
	kept, err := a.recordFeedback(ctx, clicker.UserID(), store.FeedbackSubmission{
		TaskID: taskID, SessionID: sessionID, Rating: rating, Comment: &comment,
	})
	switch {
	case errors.Is(err, errFeedbackNotRecorded):
		return processResult{outcome: outcomeTransient, decline: msgFeedbackNotRecorded}
	case kept.detailsAdded:
		a.logger.Debug("google chat feedback details recorded", "taskID", taskID, "commentLen", len(comment))
	}
	conflict := errors.Is(err, store.ErrFeedbackConflict)
	failureText := msgFeedbackStoredCardFailed
	if conflict {
		a.logger.Debug("google chat feedback not saved: a rating was already recorded", "taskID", taskID, "keptRating", kept.rating, "commentLen", len(comment))
		failureText = entrypoint.FeedbackAlreadyRecordedNotice
	}
	res := a.showFeedbackCard(ctx, ev, taskID, feedbackAckCard(kept.rating), failureText)
	if conflict && res.outcome == outcomeSubmitted {
		a.postNotice(ev.SpaceName, ev.ThreadName, entrypoint.FeedbackAlreadyRecordedNotice)
	}
	return res
}

var errFeedbackNotRecorded = errors.New("google chat feedback could not be recorded")

type keptFeedback struct {
	rating       string
	detailsAdded bool
}

func (a *Adapter) recordFeedback(ctx context.Context, userID string, sub store.FeedbackSubmission) (keptFeedback, error) {
	clicked := keptFeedback{rating: sub.Rating}
	db := a.core.Store()
	if db == nil {
		a.logger.Warn("google chat feedback dropped: the gateway has no database", append(taskField(sub.TaskID), "hasDetails", hasFeedbackDetails(sub))...)
		return clicked, nil
	}
	if !isCanonicalUUID(sub.TaskID) {
		a.logger.Warn("google chat feedback dropped: the card names a task id this gateway did not mint", "taskIDLen", len(sub.TaskID), "hasDetails", hasFeedbackDetails(sub))
		return clicked, nil
	}
	taskRepo := store.NewTaskRepo(db)
	subject, _, err := entrypoint.FeedbackSubjectForTask(ctx, taskRepo, sub.TaskID)
	switch {
	case errors.Is(err, entrypoint.ErrFeedbackTaskNotFound), errors.Is(err, entrypoint.ErrFeedbackTaskUnclassified):
		a.logger.Error("google chat feedback dropped: the task could not be attributed", "taskID", sub.TaskID, "hasDetails", hasFeedbackDetails(sub), "error", err)
		return clicked, nil
	case err != nil:
		return clicked, a.feedbackNotRecorded(ctx, sub, err)
	}
	sub.UserID = userID
	sub.SubjectID, sub.SubjectType = subject.ID, subject.Type
	sub.EntrypointType = store.EntrypointTypeGoogleChat
	result, err := entrypoint.RecordFeedback(ctx, store.NewFeedbackRepo(db), sub, entrypoint.FeedbackPublishParams{
		Core:      a.core,
		TaskRepo:  taskRepo,
		EventRepo: store.NewTaskEventRepo(db),
		Logger:    a.logger,
	})
	switch {
	case errors.Is(err, store.ErrFeedbackConflict):
		return keptFeedback{rating: result.Feedback.Rating}, err
	case err != nil:
		return clicked, a.feedbackNotRecorded(ctx, sub, err)
	}
	detailsAdded := result.Outcome == store.FeedbackDetailsAdded || (result.Outcome == store.FeedbackInserted && hasFeedbackDetails(sub))
	if result.Outcome == store.FeedbackInserted {
		a.meter().Counter(metricFeedback, 1, "gatewayId", a.gatewayID, "rating", result.Feedback.Rating, "part", "rating")
	}
	if detailsAdded {
		a.meter().Counter(metricFeedback, 1, "gatewayId", a.gatewayID, "rating", result.Feedback.Rating, "part", "details")
	}
	return keptFeedback{rating: result.Feedback.Rating, detailsAdded: detailsAdded}, nil
}

func (a *Adapter) feedbackNotRecorded(ctx context.Context, sub store.FeedbackSubmission, err error) error {
	if ctx.Err() != nil {
		a.logger.Debug("google chat feedback not recorded: the click's context ended", "taskID", sub.TaskID)
		return errFeedbackNotRecorded
	}
	commentLen := 0
	if sub.Comment != nil {
		commentLen = len(*sub.Comment)
	}
	a.logger.Warn("google chat feedback could not be saved; the click is retried", "taskID", sub.TaskID,
		"hasDetails", hasFeedbackDetails(sub), "commentLen", commentLen, "error", err)
	return errFeedbackNotRecorded
}

func hasFeedbackDetails(sub store.FeedbackSubmission) bool {
	return sub.Comment != nil && *sub.Comment != ""
}

func (a *Adapter) replaceClickedCard(ev *inboundEvent, text string, cards []*chat.CardWithId) {
	if ev.MessageName == "" {
		return
	}
	a.bgWG.Go(func() {
		ctx, cancel := context.WithTimeout(a.ctx, noticeWriteTimeout)
		defer cancel()
		err := a.writer.update(ctx, ev.SpaceName, ev.MessageName, messageContent{Text: text, Cards: cards}, true)
		if err != nil && !errors.Is(err, errMessageGone) {
			a.logger.Warn("google chat clicked card could not be replaced", append(taskField(ev.Action[paramTaskID]), "action", ev.Action[actionMethodParam], "error", err)...)
		}
	})
}

func taskField(taskID string) []any {
	if isCanonicalUUID(taskID) {
		return []any{"taskID", taskID}
	}
	return []any{"taskIDLen", len(taskID)}
}
