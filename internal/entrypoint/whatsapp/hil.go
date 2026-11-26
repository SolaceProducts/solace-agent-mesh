// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const defaultHILTTL = 10 * time.Minute

const hilDedupTTL = 5 * time.Minute

type pendingHIL struct {
	requestID   string
	taskID      string
	agentName   string
	userID      string
	fromPhone   string
	sessionID   string
	surface     *a2ui.Surface
	promptWamid string
	expiresAt   int64

	authorized       *auth.Subject
	authorizedSource string
	answerWamid      string
}

func promptRouting(row *store.WhatsAppPendingPrompt) *pendingHIL {
	return &pendingHIL{
		requestID:   row.RequestID,
		taskID:      row.TaskID,
		agentName:   row.AgentName,
		userID:      row.UserID,
		fromPhone:   row.SenderID,
		sessionID:   row.SessionID,
		promptWamid: row.PromptWamid,
		expiresAt:   row.ExpiresAt,
	}
}

func decodePendingPrompt(row *store.WhatsAppPendingPrompt) (*pendingHIL, error) {
	surface, err := a2ui.Parse(json.RawMessage(row.SurfaceJSON))
	if err != nil {
		return nil, fmt.Errorf("decode stored a2ui surface for task %s: %w", row.TaskID, err)
	}
	p := promptRouting(row)
	p.surface = surface
	return p, nil
}

func (a *Adapter) withdrawAuthPrompt(row *store.WhatsAppOutbound) {
	withdrew, unrecorded := false, false
	if meta := a.getTaskMeta(row.TaskID); meta != nil {
		withdrew, unrecorded = meta.removeAuthPrompt(row.ID)
	}
	if items := a.taskItems(); items.enabled() {
		claimed, err := items.claim(context.WithoutCancel(a.ctx), row.TaskID, itemAuthPromptPrefix+row.ID)
		withdrew = claimed || err != nil || unrecorded
	}
	if !withdrew {
		return
	}
	a.logger.Warn("authorization prompt withdrawn: it could not be delivered",
		"taskID", row.TaskID, "outboundID", row.ID)
	a.reply(row.TaskID, row.Recipient, msgAuthPromptUndeliverable)
}

func (a *Adapter) handleUserInputRequest(taskID string, req *a2a.UserInputRequestData) {
	meta := a.getTaskMeta(taskID)
	if meta == nil {
		a.logger.Warn("user input request for unknown task", "taskID", taskID)
		return
	}
	a.typingAwaitingUser(meta.FromPhone, taskID)
	info := a.tasks().Get(taskID)
	if info == nil {
		a.logger.Warn("user input request for untracked task", "taskID", taskID)
		return
	}
	surface, err := a2ui.Parse(req.Surface)
	if err != nil {
		a.logger.Error("failed to parse A2UI surface", "taskID", taskID, "error", err)
		return
	}

	payload, mode, ok := hilPromptPayload(surface)
	if !ok {
		a.logger.Warn("HIL surface does not fit a WhatsApp message; pointing the user at the web UI", "taskID", taskID, "requestID", req.RequestID)
		notice := hilWebUINotice(surface.Title, info.AgentName, a.core.WebUIURL())
		if !a.enqueueText(kindNotice, a.targetForMeta(taskID, meta), notice) {
			a.logger.Error("failed to queue the web UI pointer for an interactive prompt", "taskID", taskID, "requestID", req.RequestID)
		}
		return
	}

	payload.Prompt.RequestID = req.RequestID
	payload.Prompt.Surface = req.Surface
	payload.Prompt.ExpiresAt = nowMillis() + promptLifetime(req.ExpiresAt).Milliseconds()

	if outboundID := a.enqueue(kindInteractivePrompt, a.targetForMeta(taskID, meta), payload, ""); outboundID == "" {
		a.logger.Error("failed to queue interactive prompt", "taskID", taskID, "requestID", req.RequestID)
		return
	}
	a.logger.Info("interactive prompt queued", "taskID", taskID, "mode", mode, "requestID", req.RequestID)
}

func (a *Adapter) commitSendState(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound, wamid string, sendErr error) (afterCommit func(), err error) {
	if row.Kind != kindInteractivePrompt {
		return nil, nil
	}
	if sendErr != nil {
		return a.abandonPrompt(ctx, q, row)
	}
	return a.storePendingPrompt(ctx, q, row, wamid)
}

func (a *Adapter) abandonPrompt(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound) (afterCommit func(), err error) {
	if _, err := a.dropPendingPrompt(ctx, q, row); err != nil {
		return nil, err
	}
	var payload outboundPayload
	if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
		return nil, fmt.Errorf("decode prompt payload %s: %w", row.ID, err)
	}
	if payload.Prompt != nil && isIdentityPrompt(payload.Prompt.Kind) {
		prompt := payload.Prompt
		return func() { a.withdrawIdentityPrompt(row, prompt) }, nil
	}
	if payload.Prompt == nil || payload.Prompt.Kind != promptKindHIL || payload.Prompt.RequestID == "" {
		return func() { a.withdrawAuthPrompt(row) }, nil
	}
	p := &pendingHIL{
		requestID: payload.Prompt.RequestID,
		taskID:    row.TaskID,
		agentName: row.AgentName,
		userID:    row.UserID,
		fromPhone: row.Recipient,
		sessionID: row.SessionID,
	}
	return func() { a.tellAgentThePromptFailed(p, row.ID) }, nil
}

func (a *Adapter) tellAgentThePromptFailed(p *pendingHIL, outboundID string) {
	if err := a.submitResponse(p, "cancelled", nil); err != nil && !errors.Is(err, errHILAlreadyAnswered) {
		a.logger.Error("failed to cancel an undeliverable interactive prompt",
			"taskID", p.taskID, "requestID", p.requestID, "error", err)
	}
	a.logger.Warn("interactive prompt withdrawn: it could not be delivered",
		"taskID", p.taskID, "requestID", p.requestID, "outboundID", outboundID)
	a.reply(p.taskID, p.fromPhone, msgPromptUndeliverable)
}

func (a *Adapter) withdrawIdentityPrompt(row *store.WhatsAppOutbound, p *promptPayload) {
	if p.Kind != promptKindContactRequest {
		a.loginPrompts.forget(row.Recipient)
		a.logger.Warn("sign-in prompt withdrawn: it could not be delivered", "outboundID", row.ID)
		return
	}
	name, language := "", ""
	if p.Template != nil {
		name, language = p.Template.Name, p.Template.Language
	}
	a.logger.Error("contact-share request undeliverable: the configured template was refused",
		"outboundID", row.ID, "template", name, "language", language,
		"hint", "check that contact_request_template and contact_request_template_language name a template approved in the WhatsApp Manager")
	a.contactRequests.forget(row.Recipient)
	a.reply("", row.Recipient, entrypoint.UnresolvedSenderText(entrypoint.SenderNoValue, ""))
}

func (a *Adapter) storePendingPrompt(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound, wamid string) (afterCommit func(), err error) {
	var payload outboundPayload
	if decodeErr := json.Unmarshal([]byte(row.Payload), &payload); decodeErr != nil {
		return nil, fmt.Errorf("decode prompt payload %s: %w", row.ID, decodeErr)
	}
	if payload.Prompt != nil && isIdentityPrompt(payload.Prompt.Kind) {
		return nil, a.recordIdentityPrompt(ctx, q, row, payload.Prompt, wamid)
	}
	if payload.Prompt == nil || payload.Prompt.Kind != promptKindHIL || payload.Prompt.RequestID == "" {
		return nil, nil
	}
	nowMs := nowMillis()
	expiresAt := payload.Prompt.ExpiresAt
	if expiresAt == 0 {
		expiresAt = nowMs + defaultHILTTL.Milliseconds()
	}
	displaced, err := a.prompts.Upsert(ctx, q, &store.WhatsAppPendingPrompt{
		GatewayID:   a.gatewayID(),
		SenderID:    row.Recipient,
		TaskID:      row.TaskID,
		RequestID:   payload.Prompt.RequestID,
		AgentName:   row.AgentName,
		UserID:      row.UserID,
		SessionID:   row.SessionID,
		SurfaceJSON: string(payload.Prompt.Surface),
		OutboundID:  row.ID,
		PromptWamid: wamid,
		ExpiresAt:   expiresAt,
		CreatedAt:   nowMs,
		UpdatedAt:   nowMs,
	})
	if err != nil {
		return nil, err
	}
	if displaced == nil || displaced.TaskID == row.TaskID {
		return nil, nil
	}
	return func() { a.retireSupersededHIL(displaced, row.TaskID) }, nil
}

func (a *Adapter) dropPendingPrompt(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound) (bool, error) {
	n, err := a.prompts.DeleteByOutbound(ctx, q, a.gatewayID(), row.Recipient, row.ID)
	return n > 0, err
}

func (a *Adapter) withdrawPrompt(row *store.WhatsAppOutbound) {
	if a.withdrawUndeliveredLoginPrompt(row) {
		return
	}
	ctx, cancel := a.settleCtx()
	defer cancel()
	pending, err := a.prompts.Get(ctx, a.gatewayID(), row.Recipient)
	if err != nil {
		a.logger.Error("failed to read the prompt being withdrawn", "outboundID", row.ID, "error", err)
		return
	}
	if pending == nil || pending.OutboundID != row.ID {
		a.withdrawAuthPrompt(row)
		return
	}
	dropped, err := a.dropPendingPrompt(ctx, nil, row)
	if err != nil {
		a.logger.Error("failed to withdraw an undeliverable interactive prompt",
			"outboundID", row.ID, "taskID", row.TaskID, "error", err)
		return
	}
	if !dropped {
		return
	}

	a.tellAgentThePromptFailed(promptRouting(pending), row.ID)
}

var loginCardUnrenderableCodes = map[int]bool{131026: true, 131051: true}

func (a *Adapter) withdrawUndeliveredLoginPrompt(row *store.WhatsAppOutbound) bool {
	var payload outboundPayload
	if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil || payload.Prompt == nil || payload.Prompt.Kind != promptKindLogin {
		return false
	}
	if row.Wamid == "" || !loginCardUnrenderableCodes[row.ErrorCode] ||
		a.loginButtonRefused.active(row.Recipient, store.DefaultPendingAuthTTL) {
		return true
	}
	a.loginButtonRefused.shouldWarn(row.Recipient, store.DefaultPendingAuthTTL)
	a.logger.Warn("sign-in prompt undelivered as a button; sending the link as text", "outboundID", row.ID)
	if payload.Prompt.FallbackText != "" {
		a.reply("", row.Recipient, payload.Prompt.FallbackText)
	}
	return true
}

func (a *Adapter) expirePendingHIL(row *store.WhatsAppPendingPrompt, notify bool) {
	if a.stopped.Load() {
		return
	}
	p := promptRouting(row)
	submitErr := a.submitResponse(p, "timed_out", nil)
	if errors.Is(submitErr, errHILAlreadyAnswered) {
		return
	}
	ctx, cancel := a.settleCtx()
	defer cancel()
	claimed, err := a.prompts.ClaimExpired(ctx, a.gatewayID(), row.SenderID, row.RequestID, nowMillis())
	if err != nil {
		a.logger.Error("failed to retire an expired interactive prompt", "taskID", row.TaskID, "error", err)
		a.hilDedup.Delete(p.requestID)
		return
	}
	if !claimed {
		return
	}
	if submitErr != nil {
		a.logger.Error("failed to submit the timed-out interactive response",
			"taskID", p.taskID, "requestID", p.requestID, "error", submitErr)
	} else {
		a.logger.Info("interactive prompt expired unanswered", "taskID", p.taskID, "requestID", p.requestID)
	}
	if notify {
		a.reply(p.taskID, p.fromPhone, msgPromptTimedOut)
	}
}

func (a *Adapter) retireSupersededHIL(displaced *store.WhatsAppPendingPrompt, taskID string) {
	prev := promptRouting(displaced)
	a.logger.Info("superseding an outstanding interactive prompt",
		"supersededTaskID", prev.taskID, "supersededRequestID", prev.requestID, "taskID", taskID)
	if err := a.submitResponse(prev, "cancelled", nil); err != nil {
		if errors.Is(err, errHILAlreadyAnswered) {
			return
		}
		a.logger.Error("failed to cancel the superseded interactive prompt",
			"supersededTaskID", prev.taskID, "error", err)
	}
	a.reply(prev.taskID, prev.fromPhone, msgPromptSuperseded)
}

func (a *Adapter) handleHILReply(p *pendingHIL, msg *inboundMessage) inboundOutcome {
	if !replyMatchesPrompt(p, msg) {
		a.logger.Warn("interactive reply answers a prompt that is no longer live",
			"wamid", msg.ID, "taskID", p.taskID)
		return a.declineHILReply(p, msgAnswerLatestQuestion)
	}
	id := interactiveReplyID(msg)
	if id == "" {
		subtype := ""
		if msg.Interactive != nil {
			subtype = msg.Interactive.Type
		}
		a.logger.Warn("interactive reply without a reply id ignored",
			"interactiveType", subtype, "wamid", msg.ID, "taskID", p.taskID)
		return a.declineHILReply(p, msgReplyToQuestion)
	}
	if idx, isAction := parseActionRef(id); isAction {
		if !actionIndexValid(p.surface, idx) {
			a.logger.Warn("interactive reply action is not on the pending prompt",
				"id", id, "wamid", msg.ID, "taskID", p.taskID)
			return a.declineHILReply(p, msgAnswerUnmatched)
		}
		return a.submitted(p, a.pressAction(p, idx))
	}
	idx, cancel, ok := parseReplyRef(id)
	if !ok {
		a.logger.Warn("unrecognized interactive reply id", "id", id, "wamid", msg.ID, "taskID", p.taskID)
		return a.declineHILReply(p, msgReplyToQuestion)
	}
	if !cancel && !optionIndexValid(p.surface, idx) {
		a.logger.Warn("interactive reply option is not on the pending prompt",
			"id", id, "wamid", msg.ID, "taskID", p.taskID)
		return a.declineHILReply(p, msgAnswerUnmatched)
	}
	return a.submitted(p, a.resumeHIL(p, idx, cancel))
}

const metricWhatsAppHILResume = "whatsapp_hil_resume"

func (a *Adapter) countHILResume(outcome inboundOutcome) {
	a.meter().Counter(metricWhatsAppHILResume, 1, "gatewayId", a.gatewayID(), "outcome", outcome.String())
}

func (a *Adapter) declineHILReply(p *pendingHIL, notice string) inboundOutcome {
	a.reply(p.taskID, p.fromPhone, notice)
	return outcomeDeclined
}

func (a *Adapter) submitted(p *pendingHIL, submitErr error) inboundOutcome {
	if submitErr == nil {
		return outcomeSubmitted
	}
	if errors.Is(submitErr, errHILAlreadyAnswered) {
		a.logger.Debug("interactive reply already answered", "taskID", p.taskID, "requestID", p.requestID)
		return outcomeSubmitted
	}
	denied := entrypoint.IsAuthorizationDenial(submitErr)
	if !denied {
		a.logger.Error("failed to submit user input response", "taskID", p.taskID, "error", submitErr)
	}
	a.hilDedup.Delete(p.requestID)
	if denied {
		return outcomeHILUnauthorized
	}
	return outcomeTransient
}

func replyMatchesPrompt(p *pendingHIL, msg *inboundMessage) bool {
	if msg.Context == nil || msg.Context.ID == "" || p.promptWamid == "" {
		return true
	}
	return msg.Context.ID == p.promptWamid
}

func optionIndexValid(s *a2ui.Surface, idx int) bool {
	f := primaryField(s)
	return f != nil && idx >= 0 && idx < len(f.Options)
}

func (a *Adapter) handleHILText(p *pendingHIL, text string) inboundOutcome {
	if isCancelText(text, p.surface) {
		return a.submitted(p, a.resumeHIL(p, 0, true))
	}
	field := primaryField(p.surface)
	if field == nil {
		actions := actionChoices(p.surface)
		if len(actions) == 0 {
			return a.submitted(p, a.resumeHIL(p, 0, true))
		}
		if idx, ok := parseTextChoice(text, actions); ok {
			return a.submitted(p, a.pressAction(p, idx))
		}
		return a.repromptHILText(p, text, len(actions))
	}
	if field.Kind == a2ui.FieldText {
		dataModel := p.surface.BuildDataModel()
		a2ui.SetPath(dataModel, field.Path, strings.TrimSpace(text))
		return a.submitted(p, a.submitHIL(p, dataModel))
	}
	if idx, ok := parseTextChoice(text, displayChoices(field)); ok {
		return a.submitted(p, a.resumeHIL(p, idx, false))
	}
	if !looksLikeChoiceNumber(text) {
		if handled, err := a.submitOtherText(p, field, text); handled {
			return a.submitted(p, err)
		}
	}
	return a.repromptHILText(p, text, len(field.Options))
}

func (a *Adapter) repromptHILText(p *pendingHIL, text string, optionCount int) inboundOutcome {
	a.logger.Warn("text reply matches no option of the pending prompt",
		"taskID", p.taskID, "textLen", len(text), "optionCount", optionCount)
	a.reply(p.taskID, p.fromPhone, "Sorry, I didn't understand. "+textPrompt(p.surface))
	return outcomeDeclined
}

func isCancelText(text string, s *a2ui.Surface) bool {
	if _, has := cancelButton(s); !has {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(text), "cancel")
}

func (a *Adapter) submitOtherText(p *pendingHIL, field *a2ui.Field, text string) (handled bool, err error) {
	other := otherTextField(p.surface, field)
	if !field.HasOther() || other == nil {
		return false, nil
	}
	dataModel := p.surface.BuildDataModel()
	a2ui.SetPath(dataModel, field.Path, a2ui.OtherSentinel)
	a2ui.SetPath(dataModel, other.Path, strings.TrimSpace(text))
	return true, a.submitHIL(p, dataModel)
}

func otherTextField(s *a2ui.Surface, choice *a2ui.Field) *a2ui.Field {
	for i := range s.Fields {
		if s.Fields[i].IsOtherTextField() {
			return &s.Fields[i]
		}
	}
	var found *a2ui.Field
	for i := range s.Fields {
		f := &s.Fields[i]
		if f == choice || f.Kind != a2ui.FieldText {
			continue
		}
		if found != nil {
			return nil
		}
		found = f
	}
	return found
}

func looksLikeChoiceNumber(text string) bool {
	_, err := strconv.Atoi(strings.TrimSpace(text))
	return err == nil
}

func (a *Adapter) resumeHIL(p *pendingHIL, optionIdx int, cancel bool) error {
	if cancel {
		return a.submitResponse(p, "cancelled", nil)
	}
	field := primaryField(p.surface)
	dataModel := p.surface.BuildDataModel()
	if field != nil && optionIdx >= 0 && optionIdx < len(field.Options) {
		a2ui.SetPath(dataModel, field.Path, field.Options[optionIdx].Value)
	}
	return a.submitHIL(p, dataModel)
}

func (a *Adapter) submitHIL(p *pendingHIL, dataModel map[string]any) error {
	btn, ok := submitButton(p.surface)
	if !ok {
		return a.submitResponse(p, "answered", dataModel)
	}
	status, data := p.surface.BuildResponse(btn, dataModel)
	return a.submitResponse(p, status, data)
}

var errHILAlreadyAnswered = errors.New("hil answer already submitted")

func (a *Adapter) retireIdentity() *auth.Subject {
	return auth.SystemUserIdentity(a.cfg.RunAs, auth.AuthMethodWhatsApp)
}

func (a *Adapter) submitResponse(p *pendingHIL, status string, data map[string]any) error {
	if !a.hilDedup.SetIfAbsent(p.requestID, struct{}{}, runtime.WithTTL(hilDedupTTL)) {
		return errHILAlreadyAnswered
	}
	a.restoreTaskForResponse(p)
	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: p.requestID,
		Status:    status,
		Data:      data,
	}
	base, cancel := a.settleCtx()
	defer cancel()
	identity, swapped := auth.ForWhatsAppSender(p.authorized, a.cfg.RunAs)
	if !swapped && identity == nil {
		identity = a.retireIdentity()
	}
	ctx := base
	a.typingTrack(p.fromPhone, p.taskID, p.answerWamid)
	place := entrypoint.ChatPlace{Conversation: p.fromPhone, User: p.fromPhone}
	answerUserID := a.core.PromptAnswerUserID(p.taskID, cmp.Or(p.userID, identity.UserID()), place, place)
	if err := a.core.SubmitUserInputResponseAttributedTo(ctx, p.agentName, p.taskID, resp, identity, answerUserID); err != nil {
		if p.answerWamid != "" {
			a.typingAwaitingUser(p.fromPhone, p.taskID)
		}
		return fmt.Errorf("submit user input response for task %s: %w", p.taskID, err)
	}
	return nil
}

func (a *Adapter) restoreTaskForResponse(p *pendingHIL) {
	if p.taskID == "" || a.tasks().Get(p.taskID) != nil {
		return
	}
	if err := a.tasks().Register(p.taskID, p.agentName, p.userID, entrypoint.TaskIdentity{SessionID: p.sessionID}); err != nil {
		a.logger.Warn("failed to restore the record of a task resumed from a stored question", "taskID", p.taskID, "error", err)
		return
	}
	ctx, cancel := a.settleCtx()
	defer cancel()
	if err := a.taskItems().writeDestination(ctx, p.taskID, p.fromPhone, p.sessionID); err != nil {
		a.logger.Warn("failed to restore the destination of a task resumed from a stored question", "taskID", p.taskID, "error", err)
		return
	}
	a.logger.Info("restored a task resumed from a stored question", "taskID", p.taskID)
}

func surfacePrompt(s *a2ui.Surface, field *a2ui.Field) string {
	var fieldLabel string
	if field != nil {
		fieldLabel = field.Label
	}
	parts := make([]string, 0, 4)
	for _, p := range []string{s.Title, detailText(s.Details), s.Question, fieldLabel} {
		if p != "" && !slices.Contains(parts, p) {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "Please choose an option:"
	}
	return strings.Join(parts, "\n\n")
}

func buttonsForField(field *a2ui.Field, s *a2ui.Surface) []replyButton {
	btns := make([]replyButton, 0, len(field.Options)+1)
	for i, o := range field.Options {
		if o.Value == a2ui.OtherSentinel {
			continue
		}
		btns = append(btns, replyButton{ID: optionRefID(i), Title: truncate(o.Label, waButtonTitleMax)})
	}
	if _, has := cancelButton(s); has && len(btns) < waMaxButtons {
		btns = append(btns, replyButton{ID: cancelRefID, Title: "Cancel"})
	}
	return btns
}

func rowsForField(field *a2ui.Field, s *a2ui.Surface) []listRow {
	rows := make([]listRow, 0, len(field.Options)+1)
	for i, o := range field.Options {
		if o.Value == a2ui.OtherSentinel {
			continue
		}
		rows = append(rows, listRow{ID: optionRefID(i), Title: truncate(o.Label, waListTitleMax), Description: truncate(o.Description, waListDescMax)})
	}
	if _, has := cancelButton(s); has && len(rows) < waMaxListRows {
		rows = append(rows, listRow{ID: cancelRefID, Title: "Cancel"})
	}
	return rows
}

func textPrompt(s *a2ui.Surface) string {
	return truncate(textPromptBody(s), waTextBodyMax)
}

func textPromptBody(s *a2ui.Surface) string {
	field := primaryField(s)
	var b strings.Builder
	b.WriteString(surfacePrompt(s, field))
	actions := actionChoices(s)
	switch {
	case field != nil && field.Kind != a2ui.FieldText:
		writeChoices(&b, displayChoices(field))
		if field.HasOther() && otherTextField(s, field) != nil {
			b.WriteString(" Or reply with your own answer.")
		}
	case field == nil && len(actions) > 0:
		writeChoices(&b, actions)
	default:
		b.WriteString("\n\nReply with your answer.")
	}
	if _, has := cancelButton(s); has {
		b.WriteString(` Reply "cancel" to cancel.`)
	}
	return b.String()
}

func writeChoices(b *strings.Builder, choices []displayChoice) {
	b.WriteString("\n")
	for i, c := range choices {
		fmt.Fprintf(b, "\n%d. %s", i+1, c.label)
	}
	b.WriteString("\n\nReply with the number of your choice.")
}

func hilPromptPayload(s *a2ui.Surface) (payload outboundPayload, mode hilMode, ok bool) {
	hasDetails := len(s.Details) > 0
	mode, field := classifySurface(s)
	if mode != hilText {
		body := surfacePrompt(s, field)
		if !hasDetails || fitsParked(body, waBodyTextMax) {
			return nativePromptPayload(s, mode, field, truncate(body, waBodyTextMax)), mode, true
		}
	}
	body := textPromptBody(s)
	if hasDetails && !fitsParked(body, waTextBodyMax) {
		return outboundPayload{}, hilText, false
	}
	return outboundPayload{Text: truncate(body, waTextBodyMax), Prompt: &promptPayload{Kind: promptKindHIL, Mode: promptModeText}}, hilText, true
}

func nativePromptPayload(s *a2ui.Surface, mode hilMode, field *a2ui.Field, body string) outboundPayload {
	payload := outboundPayload{Text: body, Prompt: &promptPayload{Kind: promptKindHIL}}
	switch mode {
	case hilButtons:
		payload.Prompt.Mode = promptModeButtons
		payload.Prompt.Buttons = buttonsForField(field, s)
	case hilList:
		payload.Prompt.Mode = promptModeList
		payload.Prompt.ButtonText = "Choose"
		payload.Prompt.Rows = rowsForField(field, s)
	case hilActions:
		payload.Prompt.Mode = promptModeButtons
		payload.Prompt.Buttons = buttonsForActions(s)
	}
	return payload
}

func fitsParked(body string, limit int) bool {
	return utf8.RuneCountInString(body) <= limit-utf8.RuneCountInString(msgParkedReplyPrefix+"\n\n")
}

func hilWebUINotice(title, agent, webUIURL string) string {
	const cannotShow = "This agent needs input that can't be shown in WhatsApp."
	lead := asWritten(a2ui.Clip(singleLine(title), waMaxHILLabelRunes))
	if agent != "" {
		lead = cmp.Or(lead, "Request") + " from " + asWritten(a2ui.Clip(singleLine(agent), waMaxHILLabelRunes))
	}
	if lead != "" {
		lead += ": "
	}
	if webUIURL == "" {
		return lead + cannotShow + " Please use the web UI to respond."
	}
	return lead + cannotShow + " Please respond in the web UI: " + webUIURL
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\u0085", " ", " ", " ", " ", " ")

func singleLine(s string) string { return lineBreaks.Replace(s) }

var unixNewlines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

const (
	waMaxHILValueRunes = 1000
	waMaxHILLabelRunes = 100
)

const waMarkupChars = "*_~`"

func detailText(details []a2ui.Detail) string {
	parts := make([]string, 0, len(details))
	for _, d := range details {
		text := unixNewlines.Replace(d.Text)
		switch d.Kind {
		case a2ui.DetailValue:
			label := a2ui.Clip(singleLine(d.Label), waMaxHILLabelRunes)
			parts = append(parts, asWritten(label)+":\n"+monospaceValue(text))
		case a2ui.DetailNote:
			note := a2ui.Clip(text, waMaxHILValueRunes)
			if strings.ContainsAny(note, waMarkupChars) {
				parts = append(parts, monospace(note))
			} else {
				parts = append(parts, "_"+note+"_")
			}
		default:
			parts = append(parts, asWritten(a2ui.Clip(text, waMaxHILValueRunes)))
		}
	}
	return strings.Join(parts, "\n\n")
}

func asWritten(s string) string {
	if strings.ContainsAny(s, waMarkupChars) {
		return monospace(s)
	}
	return s
}

func monospaceValue(value string) string {
	if value == "" {
		return "_(empty)_"
	}
	return monospace(a2ui.Clip(value, waMaxHILValueRunes))
}

func monospace(s string) string {
	s = breakBacktickRuns(s)
	if strings.HasPrefix(s, "`") {
		s = zeroWidthSpace + s
	}
	if strings.HasSuffix(s, "`") {
		s += zeroWidthSpace
	}
	return "```" + s + "```"
}

func actionChoices(s *a2ui.Surface) []displayChoice {
	if len(s.Fields) > 0 {
		return nil
	}
	out := make([]displayChoice, 0, len(s.Buttons))
	for i, b := range s.Buttons {
		out = append(out, displayChoice{origIdx: i, label: buttonLabel(b)})
	}
	return out
}

func buttonsForActions(s *a2ui.Surface) []replyButton {
	btns := make([]replyButton, 0, len(s.Buttons))
	for i, b := range s.Buttons {
		btns = append(btns, replyButton{ID: actionRefID(i), Title: buttonLabel(b)})
	}
	return btns
}

func buttonLabel(b a2ui.Button) string {
	switch {
	case b.Label != "":
		return b.Label
	case b.IsCancel():
		return "Cancel"
	default:
		return "Submit"
	}
}

func actionRefID(i int) string { return "act:" + strconv.Itoa(i) }

func parseActionRef(id string) (idx int, ok bool) {
	rest, found := strings.CutPrefix(id, "act:")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n >= 0
}

func actionIndexValid(s *a2ui.Surface, idx int) bool {
	return len(s.Fields) == 0 && idx >= 0 && idx < len(s.Buttons)
}

func (a *Adapter) pressAction(p *pendingHIL, idx int) error {
	status, data := p.surface.BuildResponse(p.surface.Buttons[idx], p.surface.BuildDataModel())
	return a.submitResponse(p, status, data)
}

func primaryField(s *a2ui.Surface) *a2ui.Field {
	for i := range s.Fields {
		if !s.Fields[i].IsOtherTextField() {
			return &s.Fields[i]
		}
	}
	return nil
}

func submitButton(s *a2ui.Surface) (a2ui.Button, bool) {
	for _, b := range s.Buttons {
		if !b.IsCancel() {
			return b, true
		}
	}
	return a2ui.Button{}, false
}

func cancelButton(s *a2ui.Surface) (a2ui.Button, bool) {
	for _, b := range s.Buttons {
		if b.IsCancel() {
			return b, true
		}
	}
	return a2ui.Button{}, false
}

func interactiveReplyID(msg *inboundMessage) string {
	if msg.Interactive == nil {
		return ""
	}
	if msg.Interactive.ButtonReply != nil {
		return msg.Interactive.ButtonReply.ID
	}
	if msg.Interactive.ListReply != nil {
		return msg.Interactive.ListReply.ID
	}
	return ""
}

func promptLifetime(expiresAt string) time.Duration {
	return max(hilTTL(expiresAt), minPromptLifetime)
}

const minPromptLifetime = time.Minute

func hilTTL(expiresAt string) time.Duration {
	if expiresAt == "" {
		return defaultHILTTL
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return defaultHILTTL
	}
	d := time.Until(t)
	if d <= 0 {
		return defaultHILTTL
	}
	return d
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if max == 1 {
		return string(r[:1])
	}
	return string(r[:max-1]) + "…"
}

const (
	waMaxButtons     = 3
	waMaxListRows    = 10
	waButtonTitleMax = 20
	waListTitleMax   = 24
	waListDescMax    = 72
	waBodyTextMax    = 1024
	waTextBodyMax    = 4096
)

type hilMode int

const (
	hilButtons hilMode = iota
	hilList
	hilText
	hilActions
)

const cancelRefID = "cancel"

func classifySurface(s *a2ui.Surface) (hilMode, *a2ui.Field) {
	if len(s.Fields) == 0 {
		if actionsFitButtons(s.Buttons) {
			return hilActions, nil
		}
		return hilText, nil
	}
	f := singleChoiceField(s)
	if f == nil {
		return hilText, nil
	}
	concrete := concreteOptions(f)
	if len(concrete) == 0 {
		return hilText, nil
	}
	for _, o := range concrete {
		if utf8.RuneCountInString(o.Label) > waListTitleMax {
			return hilText, nil
		}
	}
	if len(concrete) <= waMaxButtons && allLabelsFit(concrete, waButtonTitleMax) {
		return hilButtons, f
	}
	if len(concrete) <= waMaxListRows {
		return hilList, f
	}
	return hilText, nil
}

func singleChoiceField(s *a2ui.Surface) *a2ui.Field {
	var found *a2ui.Field
	for i := range s.Fields {
		f := &s.Fields[i]
		if f.IsOtherTextField() {
			continue
		}
		if f.Kind != a2ui.FieldSingleChoice {
			return nil
		}
		if found != nil {
			return nil
		}
		found = f
	}
	return found
}

func concreteOptions(f *a2ui.Field) []a2ui.Option {
	out := make([]a2ui.Option, 0, len(f.Options))
	for _, o := range f.Options {
		if o.Value == a2ui.OtherSentinel {
			continue
		}
		out = append(out, o)
	}
	return out
}

func actionsFitButtons(btns []a2ui.Button) bool {
	if len(btns) == 0 || len(btns) > waMaxButtons {
		return false
	}
	for _, b := range btns {
		if utf8.RuneCountInString(buttonLabel(b)) > waButtonTitleMax {
			return false
		}
	}
	return true
}

func allLabelsFit(opts []a2ui.Option, max int) bool {
	for _, o := range opts {
		if utf8.RuneCountInString(o.Label) > max {
			return false
		}
	}
	return true
}

func optionRefID(i int) string { return "opt:" + strconv.Itoa(i) }

func parseReplyRef(id string) (idx int, isCancel bool, ok bool) {
	if id == cancelRefID {
		return 0, true, true
	}
	if rest, found := strings.CutPrefix(id, "opt:"); found {
		if n, err := strconv.Atoi(rest); err == nil && n >= 0 {
			return n, false, true
		}
	}
	return 0, false, false
}

type displayChoice struct {
	origIdx int
	label   string
}

func displayChoices(f *a2ui.Field) []displayChoice {
	out := make([]displayChoice, 0, len(f.Options))
	for i, o := range f.Options {
		if o.Value == a2ui.OtherSentinel {
			continue
		}
		out = append(out, displayChoice{origIdx: i, label: o.Label})
	}
	return out
}

func parseTextChoice(text string, choices []displayChoice) (idx int, ok bool) {
	t := strings.TrimSpace(text)
	if n, err := strconv.Atoi(t); err == nil {
		if n >= 1 && n <= len(choices) {
			return choices[n-1].origIdx, true
		}
		return 0, false
	}
	for _, c := range choices {
		if strings.EqualFold(t, c.label) {
			return c.origIdx, true
		}
	}
	return 0, false
}

const promptSweepInterval = time.Minute

const promptSweepBatch = 100

func (a *Adapter) livePendingPrompt(senderID string) *pendingHIL {
	row, err := a.prompts.Get(a.ctx, a.gatewayID(), senderID)
	if err != nil {
		a.logger.Error("failed to read the pending interactive prompt", "error", err)
		return nil
	}
	if row == nil {
		return nil
	}
	if row.ExpiresAt <= nowMillis() {
		a.expirePendingHIL(row, false)
		return nil
	}
	p, err := decodePendingPrompt(row)
	if err != nil {
		a.logger.Error("pending interactive prompt could not be decoded; retiring it", "taskID", row.TaskID, "error", err)
		a.retireUnreadablePrompt(row)
		return nil
	}
	return p
}

func (a *Adapter) tellAgentAParkedPromptDied(row *store.WhatsAppOutbound) {
	if row.Kind != kindInteractivePrompt {
		return
	}
	var payload outboundPayload
	if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
		a.logger.Error("retired parked prompt could not be decoded", "outboundID", row.ID, "error", err)
		return
	}
	if payload.Prompt == nil || payload.Prompt.Kind != promptKindHIL || payload.Prompt.RequestID == "" {
		a.withdrawAuthPrompt(row)
		return
	}
	a.tellAgentThePromptFailed(&pendingHIL{
		requestID: payload.Prompt.RequestID,
		taskID:    row.TaskID,
		agentName: row.AgentName,
		userID:    row.UserID,
		fromPhone: row.Recipient,
		sessionID: row.SessionID,
	}, row.ID)
}

func (a *Adapter) retireUnreadablePrompt(row *store.WhatsAppPendingPrompt) {
	n, err := a.prompts.DeleteAnswered(a.ctx, a.gatewayID(), row.SenderID, row.TaskID, row.RequestID)
	if err != nil {
		a.logger.Error("failed to retire an unreadable interactive prompt", "taskID", row.TaskID, "error", err)
		return
	}
	if n == 0 {
		return
	}
	if err := a.submitResponse(promptRouting(row), "cancelled", nil); err != nil && !errors.Is(err, errHILAlreadyAnswered) {
		a.logger.Error("failed to cancel an unreadable interactive prompt", "taskID", row.TaskID, "error", err)
	}
}

func (a *Adapter) sweepExpiredPrompts() {
	ctx, cancel := a.settleCtx()
	defer cancel()
	rows, err := a.prompts.ListExpired(ctx, a.gatewayID(), nowMillis(), promptSweepBatch)
	if err != nil {
		a.logger.Error("failed to list expired interactive prompts", "error", err)
		return
	}
	for i := range rows {
		if a.stopped.Load() {
			return
		}
		a.expirePendingHIL(&rows[i], true)
	}
}

func (a *Adapter) runPromptSweep(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.sweepIfOwner()
		}
	}
}

func (a *Adapter) sweepIfOwner() {
	if !a.ownsIngress() {
		return
	}
	a.sweepExpiredPrompts()
	a.sweepStaleParks()
	a.sweepExpiredPendingAuth()
}

func (a *Adapter) clearAnsweredPrompt(senderID string, p *pendingHIL) {
	ctx, cancel := a.settleCtx()
	defer cancel()
	cleared, err := a.prompts.DeleteAnswered(ctx, a.gatewayID(), senderID, p.taskID, p.requestID)
	if err != nil {
		a.logger.Error("failed to clear the answered interactive prompt", "taskID", p.taskID, "error", err)
		return
	}
	if cleared == 0 {
		a.logger.Debug("the answered interactive prompt had already been displaced",
			"taskID", p.taskID, "requestID", p.requestID)
	}
}

func (a *Adapter) clearPendingHIL(senderID, taskID string) {
	ctx, cancel := a.settleCtx()
	defer cancel()
	if _, err := a.prompts.DeleteByTask(ctx, a.gatewayID(), senderID, taskID); err != nil {
		a.logger.Error("failed to clear the pending interactive prompt", "taskID", taskID, "error", err)
	}
	withdrawn, err := a.outbound.SupersedeQueuedOfKind(ctx, a.gatewayID(), taskID, kindInteractivePrompt, nowMillis())
	if err != nil {
		a.logger.Error("failed to withdraw a queued interactive prompt", "taskID", taskID, "error", err)
		return
	}
	if withdrawn > 0 {
		a.logger.Info("withdrew an interactive prompt whose task ended before it was sent",
			"taskID", taskID, "withdrawn", withdrawn)
	}
}
