// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const (
	msgNotifyDefault = "You have a new notification."
	msgNotYourPrompt = "Only the person this was sent to can answer it."
)

const (
	defaultNotifyDriveBudget = time.Minute
	spaceReadTimeout         = 30 * time.Second
)

var directMessageSpaceRE = regexp.MustCompile(`^spaces/[A-Za-z0-9_-]+$`)

type proactiveAddress struct {
	Space string `json:"space"`
	Email string `json:"email"`
}

func (p proactiveAddress) usable() bool {
	return directMessageSpaceRE.MatchString(p.Space) && isEmailAddress(p.Email)
}

func (a *Adapter) notifyOptions() entrypoint.NotifyDeliverOptions {
	return entrypoint.NotifyDeliverOptions{GatewayType: EntrypointType, Interactable: a.promptsReachAdapter()}
}

func (a *Adapter) promptMeta() taskMeta {
	return taskMeta{taskDestination: taskDestination{AddOn: a.cfg.AddOnServiceAccountEmail != ""}}
}

func (a *Adapter) promptsReachAdapter() bool {
	return a.cfg.Delivery == DeliveryHTTPS && a.buttonsReachAdapter(a.promptMeta())
}

func (a *Adapter) startNotifyDeliverLoop() {
	if a.dms == nil {
		a.logger.Debug("google chat proactive delivery is off: the Chat client cannot read a space")
		return
	}
	a.core.StartNotifyDeliverConsumer(a.ctx, a.notifyOptions(), a.logger, a.handleNotifyDeliver)
}

func (a *Adapter) publishPresence(ctx context.Context, ev *inboundEvent, sender *auth.Subject) {
	addr := proactiveAddress{Space: ev.SpaceName, Email: strings.ToLower(ev.SenderEmail)}
	if !ev.IsDM || !addr.usable() {
		return
	}
	a.core.PublishNotifyPresence(ctx, EntrypointType, sender.UserID(), sender.Email(), addr)
}

const (
	reasonNoConversation = "no_conversation"
	reasonChatRefused    = "chat_refused"
	reasonUnavailable    = "unavailable"
)

func directMessageFailure(err error) (string, slog.Level) {
	switch {
	case errors.Is(err, errNoDirectMessage):
		return reasonNoConversation, slog.LevelWarn
	case errors.Is(err, errChatRefused):
		return reasonChatRefused, slog.LevelError
	default:
		return reasonUnavailable, slog.LevelWarn
	}
}

func recipientField(d a2a.NotifyDeliver) []any {
	return []any{"recipientFP", strutil.PIIFingerprint(d.UserID)}
}

func (a *Adapter) handleNotifyDeliver(d a2a.NotifyDeliver) {
	addr, ok := a.deliveryAddress(d)
	if !ok {
		return
	}
	run := func() {
		defer a.recoverDelivery(d)
		a.makeDelivery(d, addr)
	}
	done := make(chan struct{})
	if a.goLinger(func() {
		defer close(done)
		run()
	}) {
		<-done
		return
	}
	run()
}

func (a *Adapter) recoverDelivery(d a2a.NotifyDeliver) {
	if r := recover(); r != nil {
		a.logger.Error("google chat notify deliver: panic making a delivery; it is dropped",
			"requestID", d.RequestID, "panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
	}
}

func (a *Adapter) deliveryAddress(d a2a.NotifyDeliver) (proactiveAddress, bool) {
	var addr proactiveAddress
	if err := json.Unmarshal(d.Address, &addr); err != nil || !addr.usable() {
		a.logger.Error("google chat notify deliver: unusable address; dropping", append([]any{"requestID", d.RequestID, "decodeFailed", err != nil}, recipientField(d)...)...)
		return proactiveAddress{}, false
	}
	if !a.cfg.emailDomainAllowed(addr.Email) {
		a.logger.Warn("google chat notify deliver: the recipient's email domain is not allowed by allowed_email_domains; skipping out-of-band (inbox unaffected)", append([]any{"requestID", d.RequestID}, recipientField(d)...)...)
		return proactiveAddress{}, false
	}
	return addr, true
}

func (a *Adapter) makeDelivery(d a2a.NotifyDeliver, addr proactiveAddress) {
	ctx, cancel := a.abandonableContext(noticeWriteTimeout)
	defer cancel()
	if !a.isDirectMessageWithApp(ctx, d, addr) {
		return
	}
	if d.Mode == a2a.NotifyModeInput {
		a.deliverProactiveHIL(ctx, d, addr)
		return
	}
	a.deliverProactiveNotice(ctx, d, addr)
}

func (a *Adapter) isDirectMessageWithApp(ctx context.Context, d a2a.NotifyDeliver, addr proactiveAddress) bool {
	readCtx, cancel := context.WithTimeout(ctx, spaceReadTimeout)
	defer cancel()
	var direct bool
	err := a.writer.readWithRetry(readCtx, "get_space", func() error {
		var readErr error
		direct, readErr = a.dms.IsDirectMessage(readCtx, addr.Space)
		return readErr
	})
	if err != nil {
		if ctx.Err() != nil {
			a.logAbandoned(d)
			return false
		}
		reason, level := directMessageFailure(err)
		a.logger.Log(ctx, level, "google chat notify deliver: cannot reach the direct message; skipping out-of-band (inbox unaffected)",
			append(append([]any{"requestID", d.RequestID, "notifyReason", reason}, recipientField(d)...), causeFields(primaryKeys, err)...)...)
		return false
	}
	if !direct {
		a.logger.Warn("google chat notify deliver: the address is not a direct message with the app; refusing", append([]any{"requestID", d.RequestID}, recipientField(d)...)...)
	}
	return direct
}

func (a *Adapter) deliverProactiveHIL(ctx context.Context, d a2a.NotifyDeliver, addr proactiveAddress) {
	surface, err := a2ui.Parse(d.Surface)
	if err != nil {
		a.logger.Error("google chat notify deliver: the input request has an unreadable surface", "requestID", d.RequestID, "errorType", fmt.Sprintf("%T", err))
		return
	}
	notice := messageContent{FallbackText: cmpLabel(surface.Title, hilDefaultTitle), Cards: hilWebUINoticeCard(surface, d.RequestID, a.core.WebUIURL())}
	cards, ok := notifyHILCard(surface, d.RequestID, addr.Email)
	switch {
	case !a.promptsReachAdapter():
		a.logger.Warn("google chat notify deliver: a click on the prompt would not reach this entrypoint; pointing the person at the web UI", "requestID", d.RequestID)
		a.postProactive(ctx, d, addr, notice)
		return
	case !ok:
		a.logger.Warn("google chat notify deliver: the input request does not fit a card; pointing the person at the web UI", "requestID", d.RequestID)
		a.postProactive(ctx, d, addr, notice)
		return
	}
	prompt := messageContent{FallbackText: notice.FallbackText, Cards: a.routeButtons(cards, a.promptMeta())}
	postErr := a.writeProactive(ctx, addr, prompt)
	switch {
	case postErr == nil:
		return
	case ctx.Err() != nil:
		a.logAbandoned(d)
		return
	case !isTerminalWriteError(postErr):
		a.logNotPosted(d, postErr)
		return
	}
	a.logger.Warn("google chat notify deliver: Chat refused the prompt's card; pointing the person at the web UI", "requestID", d.RequestID)
	a.postProactive(ctx, d, addr, notice)
}

func (a *Adapter) deliverProactiveNotice(ctx context.Context, d a2a.NotifyDeliver, addr proactiveAddress) {
	text := d.Title
	if d.Message != "" {
		if text != "" {
			text += "\n"
		}
		text += d.Message
	}
	for _, part := range splitForChat(cmp.Or(renderForChat(text), msgNotifyDefault), maxMessageRunes) {
		if !a.postProactive(ctx, d, addr, messageContent{Text: part}) {
			break
		}
	}
	a.deliverProactiveArtifacts(ctx, d, addr)
}

func (a *Adapter) writeProactive(ctx context.Context, addr proactiveAddress, content messageContent) error {
	_, err := a.writer.create(ctx, addr.Space, "", content)
	return err
}

func (a *Adapter) postProactive(ctx context.Context, d a2a.NotifyDeliver, addr proactiveAddress, content messageContent) bool {
	err := a.writeProactive(ctx, addr, content)
	switch {
	case err == nil:
		return true
	case ctx.Err() != nil:
		a.logAbandoned(d)
	default:
		a.logNotPosted(d, err)
	}
	return false
}

func (a *Adapter) logNotPosted(d a2a.NotifyDeliver, err error) {
	a.logger.Warn("google chat notify deliver: the message could not be posted; skipping out-of-band (inbox unaffected)",
		append(append([]any{"requestID", d.RequestID}, recipientField(d)...), causeFields(primaryKeys, err)...)...)
}

func (a *Adapter) logAbandoned(d a2a.NotifyDeliver) {
	a.logger.Warn("google chat notify deliver: the adapter stopped or the delivery timed out before it was made; skipping out-of-band (inbox unaffected)",
		append([]any{"requestID", d.RequestID}, recipientField(d)...)...)
}

func (a *Adapter) handOverFiles(d a2a.NotifyDeliver, addr proactiveAddress) ([]entrypoint.SavedArtifact, map[string]a2a.NotifyArtifact) {
	owned := make(map[string]a2a.NotifyArtifact, len(d.Artifacts))
	var order []string
	for _, art := range d.Artifacts {
		if rbac.IsNonDelegatablePrincipal(art.UserID) || art.UserID != d.UserID || (isEmailAddress(art.UserID) && !strings.EqualFold(art.UserID, addr.Email)) {
			a.logger.Warn("google chat notify deliver: refusing a file that is not the recipient's", append([]any{"requestID", d.RequestID, "filenameLen", len(art.Filename)}, recipientField(d)...)...)
			continue
		}
		if _, seen := owned[art.Filename]; !seen {
			order = append(order, art.Filename)
		}
		owned[art.Filename] = art
	}
	files := make([]entrypoint.SavedArtifact, 0, len(order))
	for _, name := range order {
		art := owned[name]
		files = append(files, entrypoint.SavedArtifact{Filename: art.Filename, MIMEType: art.MimeType, Version: art.Version})
	}
	return files, owned
}

func (a *Adapter) deliverProactiveArtifacts(ctx context.Context, d a2a.NotifyDeliver, addr proactiveAddress) {
	if len(d.Artifacts) == 0 {
		return
	}
	store := a.core.Services().Artifacts()
	if store == nil {
		a.logger.Warn("google chat notify deliver: no artifact service; the files are not offered", "requestID", d.RequestID)
		return
	}
	files, owned := a.handOverFiles(d, addr)
	if len(files) == 0 {
		return
	}
	if a.drive == nil {
		a.postProactive(ctx, d, addr, messageContent{Text: artifactNotice(namesOf(files), a.signInURL())})
		return
	}
	handed, rest := files[:min(len(files), maxDriveArtifactsPerTask)], files[min(len(files), maxDriveArtifactsPerTask):]
	if len(rest) > 0 {
		a.postProactive(ctx, d, addr, messageContent{Text: artifactNotice(namesOf(rest), a.signInURL())})
	}
	a.handOffToDrive(&driveHandOff{
		taskID: d.RequestID, space: addr.Space, email: addr.Email, files: handed, budget: a.notifyDriveBudget,
		readFile: func(ctx context.Context, file entrypoint.SavedArtifact) ([]byte, error) {
			art := owned[file.Filename]
			return a.readArtifact(ctx, store, store.Resolve(art.AgentName, art.UserID, art.SessionID, art.Filename), file.Version)
		},
	})
}

func (a *Adapter) clickNotifyHIL(ctx context.Context, ev *inboundEvent) processResult {
	requestID := ev.Action[paramRequestID]
	if requestID == "" || ev.Action[paramNotify] != notifyRoutedValue || ev.Action[paramTaskID] != "" {
		a.logger.Warn("google chat proactive prompt click ignored: the card's routing is not one this entrypoint renders", "requestID", requestID)
		return processResult{outcome: outcomeIgnored}
	}
	if recipient := ev.Action[paramRecipient]; recipient == "" || !strings.EqualFold(recipient, ev.SenderEmail) {
		a.logger.Warn("google chat proactive prompt click refused: the clicker is not who it was sent to", "requestID", requestID, "clickerFP", strutil.PIIFingerprint(ev.SenderName))
		a.postNotice(ev.SpaceName, ev.ThreadName, msgNotYourPrompt)
		return processResult{outcome: outcomeDeclined}
	}
	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: requestID, Status: "cancelled"}
	ack := msgHILCancelled
	if ev.Action[actionMethodParam] == actionHILSubmit {
		data, err := hilResponseData(ev.Action, ev.FormInputs)
		if err != nil {
			a.logger.Warn("google chat proactive prompt carried an undecodable form description; dropping the click", "requestID", requestID, "errorType", fmt.Sprintf("%T", err))
			return processResult{outcome: outcomeIgnored}
		}
		resp.Status, resp.Data, ack = "answered", data, msgHILSubmitted
	}
	if err := a.publishNotifyReply(ctx, resp); err != nil {
		return processResult{outcome: outcomeTransient, decline: msgHILSubmitFailed}
	}
	a.logger.Debug("google chat proactive prompt answered", "requestID", requestID, "hilStatus", resp.Status)
	a.replaceClickedCard(ev, ack, nil)
	return processResult{outcome: outcomeSubmitted}
}

func (a *Adapter) publishNotifyReply(ctx context.Context, resp a2a.UserInputResponseData) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		a.logger.Error("google chat notify reply: marshal failed", "requestID", resp.RequestID, "error", err)
		return fmt.Errorf("marshal notify reply: %w", err)
	}
	topic := a2a.NotifyReplyTopic(a.core.Namespace(), resp.RequestID)
	if err := a.core.Services().Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		a.logger.Warn("google chat notify reply: publish failed", "requestID", resp.RequestID, "error", err)
		return fmt.Errorf("publish notify reply: %w", err)
	}
	return nil
}
