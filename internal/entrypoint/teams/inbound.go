// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const msgInboundAbandoned = "Sorry, I couldn't process your message. Please send it again."

const (
	msgFileConsentAbandoned = "Sorry, I couldn't upload the file '%s'. Please try again."
	msgFileConsentUploaded  = "✅ %s uploaded"
	msgFileConsentDeclined  = "Declined"
)

const conversationKeyBytes = 8

type inboundHold struct {
	key   string
	token int64
}

func (h *inboundHold) renew(core *entrypoint.GatewayCore) bool {
	if h == nil || h.key == "" || core == nil {
		return true
	}
	token, held := core.InboundRenew(h.key, h.token)
	h.token = token
	return held
}

func inboundMessageKey(conversationID, activityID string) string {
	sum := sha256.Sum256([]byte(conversationID))
	return hex.EncodeToString(sum[:conversationKeyBytes]) + ":" + activityID
}

func (a *TeamsAdapter) receiveActivity(ctx context.Context, activity *Activity, body []byte) (func(), error) {
	if activity.Type != ActivityTypeMessage || activity.From.ID == a.cfg.AppID || isCardClick(activity) {
		return a.processUnrecorded(activity), nil
	}
	text, files, ok := a.taskMessage(activity)
	if !ok || activity.ID == "" {
		return nil, nil
	}
	key := inboundMessageKey(activity.Conversation.ID, activity.ID)
	claim, err := a.core.AcceptInbound(ctx, key, string(body))
	if err != nil {
		a.logger.Warn("teams message not recorded; refusing it so Bot Framework sends it again", "messageKey", key, "error", err)
		return nil, err
	}
	if claim.Outcome == entrypoint.InboundSkip {
		a.logger.Debug("dropping a copy of a teams message another instance took", "messageKey", key)
		return nil, nil
	}
	hold := &inboundHold{key: key, token: claim.Token}
	return func() {
		a.settleInbound(hold, a.submitTeamsTask(context.Background(), activity, text, files, hold))
	}, nil
}

func (a *TeamsAdapter) processUnrecorded(activity *Activity) func() {
	return func() { a.handleActivity(context.Background(), activity) }
}

func (a *TeamsAdapter) settleInbound(hold *inboundHold, settled bool) {
	if hold == nil || hold.key == "" {
		return
	}
	if !settled {
		a.logger.Warn("teams message not settled; another instance will process it after its lease", "messageKey", hold.key)
		return
	}
	a.core.InboundDone(hold.key, hold.token)
}

func parseInbound(payload string) (*Activity, error) {
	var activity Activity
	if err := json.Unmarshal([]byte(payload), &activity); err != nil {
		return nil, fmt.Errorf("parse recorded teams activity: %w", err)
	}
	return &activity, nil
}

func (a *TeamsAdapter) ReplayInbound(_ context.Context, msg entrypoint.InboundMessage) {
	activity, err := parseInbound(msg.Payload)
	if err != nil {
		a.logger.Error("recorded teams message cannot be replayed", "messageKey", msg.ID, "error", err)
		a.core.InboundDone(msg.ID, msg.Token)
		return
	}
	hold := &inboundHold{key: msg.ID, token: msg.Token}
	if consent, ok := a.recordedFileConsent(activity); ok {
		a.settleInbound(hold, a.handleFileConsentAccept(activity, consent, hold, true))
		return
	}
	if submit, ok := a.recordedFeedbackSubmit(activity); ok {
		a.settleInbound(hold, a.handleFeedbackSubmit(submit, true))
		return
	}
	text, files, ok := a.taskMessage(activity)
	if !ok {
		a.core.InboundDone(msg.ID, msg.Token)
		return
	}
	a.settleInbound(hold, a.submitTeamsTask(context.Background(), activity, text, files, hold))
}

func (a *TeamsAdapter) recordedFileConsent(activity *Activity) (*FileConsentCardResponse, bool) {
	if activity.Type != ActivityTypeInvoke || (activity.Name != InvokeFileConsent && activity.Name != InvokeTeamsFileConsent) {
		return nil, false
	}
	consent, ok := a.fileConsentOf(activity)
	return consent, ok && consent.Action == "accept"
}

func (a *TeamsAdapter) recordedFeedbackSubmit(activity *Activity) (*Activity, bool) {
	if activity.Type != ActivityTypeInvoke || activity.Name != InvokeMessageSubmitAction {
		return nil, false
	}
	return a.feedbackSubmitOf(activity)
}

func (a *TeamsAdapter) AbandonInbound(_ context.Context, msg entrypoint.InboundMessage) bool {
	activity, err := parseInbound(msg.Payload)
	if err != nil {
		a.logger.Error("recorded teams message cannot be read to tell the user", "messageKey", msg.ID, "error", err)
		return true
	}
	if consent, ok := a.recordedFileConsent(activity); ok {
		filename := cmp.Or(parseConsentFields(consent.Context).filename, "the file")
		return a.replyUserError(activity, fmt.Sprintf(msgFileConsentAbandoned, filename))
	}
	if _, ok := a.recordedFeedbackSubmit(activity); ok {
		ctx, cancel := context.WithTimeout(a.ctx, a.feedbackWorkBound())
		defer cancel()
		return a.sendFeedbackNotice(ctx, activity, "", entrypoint.FeedbackNotRecordedNotice)
	}
	return a.replyUserError(activity, msgInboundAbandoned)
}

var _ entrypoint.InboundReplayer = (*TeamsAdapter)(nil)
