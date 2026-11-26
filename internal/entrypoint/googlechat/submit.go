// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"log/slog"
	"time"

	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const noticeWriteTimeout = 2 * time.Minute

func (a *Adapter) process(ctx context.Context, ev *inboundEvent) processResult {
	switch ev.Kind {
	case kindCardClick:
		return a.processCardClick(ctx, ev)
	case kindMessage:
	default:
		return processResult{outcome: outcomeIgnored}
	}

	verdict := a.authorizeSender(ctx, ev)
	if verdict.Retryable {
		return processResult{outcome: outcomeAuthzUnavailable}
	}
	if verdict.Identity == nil {
		a.refuseSender(ev, verdict)
		return processResult{outcome: outcomeDeclined}
	}
	a.publishPresence(ctx, ev, verdict.Identity)
	if ev.Text == "" && len(ev.Attachments) == 0 {
		return processResult{outcome: outcomeIgnored}
	}
	return a.submit(ev, verdict.Identity)
}

func (a *Adapter) refuseSender(ev *inboundEvent, verdict authzVerdict) {
	senderFP := strutil.PIIFingerprint(ev.SenderName)
	if a.refusalNotices.allow(senderFP, refusalLogWindow) {
		a.logger.Warn("google chat sender refused: not matched to a SAM user",
			"senderFP", senderFP, "cause", verdict.Cause, "resolution", verdict.Resolution, "isDm", ev.IsDM)
	}
	text := entrypoint.UnresolvedSenderText(verdict.Refusal, a.core.SignInURL())
	if verdict.Cause == causeNoEmail {
		text = msgNoEmail
	}
	a.postNotice(ev.SpaceName, ev.ThreadName, text)
}

func (a *Adapter) submit(ev *inboundEvent, sender *auth.Subject) processResult {
	res, refusal := a.trySubmit(ev, sender)
	a.core.ReportSubmitRefusal(refusal)
	return res
}

func (a *Adapter) trySubmit(ev *inboundEvent, sender *auth.Subject) (processResult, string) {
	identity, swapped := auth.ForGoogleChatUser(sender, auth.ChannelPolicy{
		IsMultiParty:              !ev.IsDM,
		UseUserIdentityInChannels: a.cfg.UseUserIdentityInChannels,
		RunAs:                     a.cfg.RunAs,
	})
	submitCtx := a.ctx

	target := a.core.ResolveIngressTarget(submitCtx, entrypoint.IngressTargetRequest{
		Caller:           identity,
		Text:             ev.Text,
		DefaultAgentName: a.cfg.DefaultAgentName,
		SignInLinkable:   true,
	})
	switch {
	case target.Misconfigured:
		if a.gateNotices.allow("default_agent_misconfigured", identityGateNoticeWindow) {
			a.logger.Warn("google chat default agent is not available; check default_agent_name", "defaultAgent", a.cfg.DefaultAgentName)
		}
		a.postNotice(ev.SpaceName, ev.ThreadName, msgMisconfiguredAgent)
		return processResult{outcome: outcomeDeclined}, taxonomy.RejectAgentNotFound
	case target.Refused():
		return processResult{outcome: outcomeUnauthorized, decline: target.Refusal}, target.RefusalReason
	case target.Name == "":
		a.logger.Warn("google chat has no agent to route to yet", "defaultAgent", a.cfg.DefaultAgentName)
		return processResult{outcome: outcomeTransient}, taxonomy.RejectAgentNotFound
	}
	agentName := target.Name

	sessionID, err := a.resolveSessionID(submitCtx, buildExternalKey(ev), agentName, identity.UserID())
	if err != nil {
		a.logger.Error("google chat session could not be resolved", "error", err)
		return processResult{outcome: outcomeTransient}, taxonomy.RejectInternal
	}

	taskID := idgen.New()
	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      "user",
		TaskID:    taskID,
		ContextID: sessionID,
	}
	if ev.Text != "" {
		msg.Parts = []a2a.Part{{Kind: "text", Text: ev.Text}}
	}
	if convCtx := a.conversationContext(ev, swapped); convCtx != "" {
		msg.Metadata = map[string]any{"conversation_context": convCtx}
	}
	if swapped {
		if msg.Metadata == nil {
			msg.Metadata = map[string]any{}
		}
		msg.Metadata["requesting_user"] = sender.UserID()
		msg.Metadata["run_as"] = identity.UserID()
	}

	a.attachFiles(submitCtx, &msg, ev, entrypoint.ArtifactScope{UserID: identity.UserID(), SessionID: sessionID})

	st := &taskState{meta: taskMeta{taskDestination: taskDestination{
		SpaceName:    ev.SpaceName,
		ThreadName:   ev.ThreadName,
		SenderName:   ev.SenderName,
		SessionID:    sessionID,
		AgentName:    agentName,
		IsDM:         ev.IsDM,
		RunAsSwapped: swapped,
		AddOn:        ev.AddOn,
	}}}
	if !a.recordTask(submitCtx, taskID, identity.UserID(), st) {
		return processResult{outcome: outcomeTransient}, taxonomy.RejectInternal
	}

	if err := a.core.SubmitTask(submitCtx, agentName, taskID, msg, identity); err != nil {
		a.forgetTask(taskID)
		a.taskRegistry.Remove(taskID)
		reason := entrypoint.SubmitFailureReason(err)
		a.meter().Counter(metricSubmitFailures, 1, "gatewayId", a.gatewayID, "reason", reason)
		if entrypoint.IsAuthorizationDenial(err) {
			return processResult{outcome: outcomeUnauthorized, decline: a.core.SubmitFailureText(err, true)}, reason
		}
		if entrypoint.IsBudgetRefusal(err) || entrypoint.IsAgentNotAvailable(err) {
			a.postNotice(ev.SpaceName, ev.ThreadName, a.core.SubmitFailureText(err, true))
			return processResult{outcome: outcomeDeclined}, reason
		}
		entrypoint.LogSubmitFailure(a.logger, slog.LevelError, err, "taskID", taskID)
		return processResult{outcome: outcomeTransient, decline: a.core.SubmitFailureText(err, true)}, reason
	}
	st.queue.Placeholder(a.cfg.InitialStatusMessage)

	var traceID string
	if info := a.taskRegistry.Get(taskID); info != nil {
		traceID = info.TraceID
	}
	a.logger.Info("google chat task submitted", "taskID", taskID, "traceID", traceID, "agentName", agentName,
		"resolvedUserId", sender.PlatformUserID(), "isDm", ev.IsDM, "runAsSwapped", swapped,
		"delivery", a.cfg.Delivery, "addOn", ev.AddOn, "textLen", len(ev.Text))
	return processResult{outcome: outcomeSubmitted}, ""
}

func (a *Adapter) recordTask(ctx context.Context, taskID, userID string, st *taskState) bool {
	dest := st.meta.taskDestination
	if err := a.taskRegistry.Register(taskID, dest.AgentName, userID, entrypoint.TaskIdentity{SessionID: dest.SessionID}); err != nil {
		a.logger.Warn("google chat task could not be recorded; the message is retried", "taskID", taskID, "error", err)
		return false
	}
	if err := a.writeDestination(ctx, taskID, dest); err != nil {
		a.taskRegistry.Remove(taskID)
		if ctx.Err() != nil {
			a.logger.Debug("google chat task destination not recorded: the message's context ended", "taskID", taskID)
			return false
		}
		a.logger.Warn("google chat task destination could not be recorded; the message is retried", "taskID", taskID, "error", err)
		return false
	}
	a.taskRegistry.SetMetadata(taskID, dest)
	st.queue = a.newQueue(taskID, st, a.taskControls(taskID, st.meta))
	a.trackTask(taskID, st)
	return true
}

func (a *Adapter) attachFiles(ctx context.Context, msg *a2a.Message, ev *inboundEvent, scope entrypoint.ArtifactScope) {
	if len(ev.Attachments) == 0 {
		return
	}
	var dropped []string
	if a.downloader != nil {
		artifacts := a.core.Services().Artifacts()
		var parts []a2a.Part
		parts, dropped = a.inboundFileParts(ctx, ev.Attachments, func(ctx context.Context, upload entrypoint.ArtifactUpload) (string, bool) {
			return entrypoint.SaveBytesAsArtifact(ctx, artifacts, scope, upload, a.logger)
		})
		msg.Parts = append(msg.Parts, parts...)
	} else {
		for _, att := range ev.Attachments {
			dropped = append(dropped, attachmentLabel(att))
		}
	}
	entrypoint.AppendDroppedFilesNote(msg, dropped)
}

func (a *Adapter) newQueue(taskID string, st *taskState, controls []*chat.CardWithId) *replyQueue {
	meta := st.snapshot()
	logger := a.logger.With("taskID", taskID)
	recorded := meta.anchors()
	q := newReplyQueue(meta.SpaceName, meta.ThreadName, a.writer, logger, meta.anchors(), func(anchors queueAnchors) {
		st.setAnchors(anchors)
		if !st.terminal.Load() {
			recorded = a.writeAnchors(taskID, recorded, anchors)
		}
	})
	q.controls = controls
	q.citations = &st.citations
	if meta.IsDM {
		q.signInLinks = func(ctx context.Context, known []string) ([]string, bool) {
			return a.outstandingSignInLinks(ctx, taskID, known, &recorded)
		}
	}
	return q
}

func (a *Adapter) postNotice(space, thread, text string) {
	a.bgWG.Go(func() {
		ctx, cancel := context.WithTimeout(a.ctx, noticeWriteTimeout)
		defer cancel()
		if _, err := a.writer.create(ctx, space, thread, messageContent{Text: text}); err != nil {
			a.logger.Warn("google chat notice could not be posted", "error", err)
		}
	})
}
