// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const pairSendInterval = 6 * time.Second

const (
	kindText              = "text"
	kindNotice            = "notice"
	kindMedia             = "media"
	kindInteractivePrompt = "interactive_prompt"
	kindFeedbackPrompt    = "feedback_prompt"
)

const (
	promptKindHIL            = "hil"
	promptKindAuth           = "auth"
	promptKindLogin          = "login"
	promptKindContactRequest = "contact_request"

	promptModeButtons  = "buttons"
	promptModeList     = "list"
	promptModeText     = "text"
	promptModeCTAURL   = "cta_url"
	promptModeTemplate = "template"
)

func isIdentityPrompt(promptKind string) bool {
	switch promptKind {
	case promptKindLogin, promptKindContactRequest:
		return true
	}
	return false
}

func fallbackOutboundStore() (dsn, dir string, err error) {
	dir, err = os.MkdirTemp("", "sam-whatsapp-outbound-")
	if err != nil {
		return "", "", fmt.Errorf("create the fallback whatsapp outbound directory: %w", err)
	}
	return "sqlite://" + filepath.Join(dir, "outbound.db"), dir, nil
}

var errOutboundUnsendable = errors.New("whatsapp message cannot be sent")

var errOutboundStale = errors.New("whatsapp message is too old to deliver")

var errUserNotified = errors.New("the user has been told about this failure")

type outboundPayload struct {
	Text    string         `json:"text,omitempty"`
	Media   *mediaPayload  `json:"media,omitempty"`
	Prompt  *promptPayload `json:"prompt,omitempty"`
	Final   bool           `json:"final,omitempty"`
	Resumed bool           `json:"resumed,omitempty"`
}

type mediaPayload struct {
	Filename   string `json:"filename"`
	MIMEType   string `json:"mimeType"`
	MediaType  string `json:"mediaType"`
	InlineText string `json:"inlineText,omitempty"`
}

type promptPayload struct {
	Kind          string          `json:"kind"`
	Mode          string          `json:"mode"`
	RequestID     string          `json:"requestId,omitempty"`
	Surface       json.RawMessage `json:"surface,omitempty"`
	ExpiresAt     int64           `json:"expiresAt,omitempty"`
	ButtonText    string          `json:"buttonText,omitempty"`
	Footer        string          `json:"footer,omitempty"`
	Buttons       []replyButton   `json:"buttons,omitempty"`
	Rows          []listRow       `json:"rows,omitempty"`
	CTA           *ctaPayload     `json:"cta,omitempty"`
	Template      *templateRef    `json:"template,omitempty"`
	LookupValueFP string          `json:"lookupValueFp,omitempty"`
	FallbackText  string          `json:"fallbackText,omitempty"`
}

type ctaPayload struct {
	DisplayText string `json:"displayText"`
	URL         string `json:"url"`
}

type templateRef struct {
	Name     string `json:"name"`
	Language string `json:"language"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

type outboundTarget struct {
	recipient string
	taskID    string
	traceID   string
	userID    string
	agentName string
	sessionID string
}

func (a *Adapter) targetFor(taskID, recipient string) outboundTarget {
	t := outboundTarget{recipient: recipient, taskID: taskID}
	if taskID == "" {
		return t
	}
	info := a.tasks().Get(taskID)
	if info == nil {
		return t
	}
	t.traceID, t.userID, t.agentName = info.TraceID, info.UserID, info.AgentName
	if meta, ok := info.Metadata.(*taskMeta); ok && meta != nil {
		t.sessionID = meta.SessionID
	}
	return t
}

func (a *Adapter) targetForMeta(taskID string, meta *taskMeta) outboundTarget {
	t := a.targetFor(taskID, meta.FromPhone)
	if t.sessionID == "" {
		t.sessionID = meta.SessionID
	}
	return t
}

func (a *Adapter) enqueue(kind string, target outboundTarget, payload outboundPayload, artifactRef string) string {
	if _, ending := a.endingTasks.Load(target.taskID); ending && target.taskID != "" {
		payload.Final = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		a.logger.Error("failed to encode outbound whatsapp payload",
			"kind", kind, "taskID", target.taskID, "error", err)
		return ""
	}
	nowMs := nowMillis()
	row := &store.WhatsAppOutbound{
		ID:          idgen.New(),
		GatewayID:   a.gatewayID(),
		Recipient:   target.recipient,
		TaskID:      target.taskID,
		TraceID:     target.traceID,
		UserID:      target.userID,
		AgentName:   target.agentName,
		SessionID:   target.sessionID,
		Kind:        kind,
		Payload:     string(body),
		ArtifactRef: artifactRef,
		State:       store.WhatsAppOutboundQueued,
		CreatedAt:   nowMs,
		UpdatedAt:   nowMs,
	}
	ctx, cancel := a.settleCtx()
	defer cancel()
	if err := a.ledger.Enqueue(ctx, nil, row); err != nil {
		a.logger.Error("failed to queue outbound whatsapp message",
			"kind", kind, "taskID", target.taskID, "outboundID", row.ID, "error", err)
		return ""
	}
	a.wakeSender(row.Recipient)
	return row.ID
}

const adapterStopBudget = 20 * time.Second

const adapterSettleTimeout = 10 * time.Second

func (a *Adapter) settleCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(a.ctx), adapterSettleTimeout)
}

const maxReplyChunks = 3

const attachmentNote = "(Message too long — full response attached as a file.)"

func (a *Adapter) reply(taskID, to, text string) {
	a.enqueueText(kindNotice, a.targetFor(taskID, to), text)
}

func (a *Adapter) replyQueued(taskID, to, text string) bool {
	return a.enqueueText(kindNotice, a.targetFor(taskID, to), text)
}

func (a *Adapter) replyAgentText(target outboundTarget, text string) {
	a.enqueueText(kindText, target, text)
}

func (a *Adapter) replyResumed(taskID, to, text string) {
	a.enqueue(kindNotice, a.targetFor(taskID, to), outboundPayload{Text: text, Resumed: true}, "")
}

func (a *Adapter) enqueueText(kind string, target outboundTarget, text string) bool {
	chunks := chunkText(text, waTextBodyMax)
	if len(chunks) > maxReplyChunks {
		return a.enqueueTextWithAttachment(kind, target, text)
	}
	queued := true
	for _, chunk := range chunks {
		if a.enqueue(kind, target, outboundPayload{Text: chunk}, "") == "" {
			queued = false
		}
	}
	return queued
}

func (a *Adapter) enqueueTextWithAttachment(kind string, target outboundTarget, text string) bool {
	budget := waTextBodyMax - utf8.RuneCountInString(attachmentNote) - 1
	preview := a.enqueue(kind, target, outboundPayload{Text: truncate(text, budget) + "\n" + attachmentNote}, "")
	attachment := a.enqueue(kindMedia, target, outboundPayload{Media: &mediaPayload{
		Filename:   "response.txt",
		MIMEType:   mimeTextPlain,
		MediaType:  mediaTypeForMIME(mimeTextPlain),
		InlineText: rejoinBacktickRuns(text),
	}}, "")
	if attachment == "" && preview != "" {
		a.reply(target.taskID, target.recipient, msgAttachmentUndeliverable)
	}
	return preview != ""
}

type mediaPolicy struct {
	mime      string
	mediaType string
	reason    string
	ok        bool
}

func (a *Adapter) checkOutboundMedia(mimeType, name string, size int64) mediaPolicy {
	resolved := resolveOutboundMIME(mimeType, name)
	if !metaAcceptedMediaMIME[resolved] {
		label := name
		if label == "" {
			label = "a file"
		}
		return mediaPolicy{mime: resolved, reason: fmt.Sprintf("(I couldn't send %s — WhatsApp doesn't accept that file type.)", label)}
	}
	mediaType := mediaTypeForMIME(resolved)
	if limit := a.outboundMediaCapBytes(mediaType); limit > 0 && size > limit {
		return mediaPolicy{mime: resolved, mediaType: mediaType, reason: fmt.Sprintf(
			"(An attachment was too large to send — the limit is %d MB for this file type.)", limit/(1024*1024))}
	}
	return mediaPolicy{mime: resolved, mediaType: mediaType, ok: true}
}

func (a *Adapter) executeSend(ctx context.Context, row *store.WhatsAppOutbound) (string, error) {
	if staleForDelivery(row, nowMillis()) {
		return "", errOutboundStale
	}
	var payload outboundPayload
	if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
		return "", fmt.Errorf("%w: decode payload: %w", errOutboundUnsendable, err)
	}
	a.applyRowMarks(row, &payload)
	if !sendableOutsideWindow(&payload) && !a.windowOpen(ctx, row.Recipient) {
		return "", errWindowClosed
	}
	if wasParked(row) && payload.Text != "" {
		limit := waTextBodyMax
		if payload.Prompt != nil && payload.Prompt.Mode != promptModeText {
			limit = waBodyTextMax
		}
		payload.Text = truncate(msgParkedReplyPrefix+"\n\n"+payload.Text, limit)
	}
	a.awaitTypingPost(ctx, row.Recipient)
	switch row.Kind {
	case kindMedia:
		return a.sendMediaRow(ctx, row, payload.Media)
	case kindInteractivePrompt, kindFeedbackPrompt:
		return a.sendPromptRow(ctx, row, payload)
	default:
		wamid, err := a.client.SendText(ctx, row.Recipient, payload.Text)
		a.auditOutbound(row, "text", "", []byte(payload.Text), wamid, err)
		return wamid, err
	}
}

func (a *Adapter) applyRowMarks(row *store.WhatsAppOutbound, payload *outboundPayload) {
	if row.TaskID == "" {
		return
	}
	if payload.Final {
		a.endTaskOnUserSide(row.Recipient, row.TaskID)
		return
	}
	if payload.Resumed {
		a.typingResumed(row.Recipient, row.TaskID)
		return
	}
	if row.Kind == kindInteractivePrompt && payload.Prompt != nil &&
		(payload.Prompt.Kind == promptKindHIL || payload.Prompt.Kind == promptKindAuth) {
		a.typingAwaitingUser(row.Recipient, row.TaskID)
	}
}

func (a *Adapter) endTaskOnUserSide(recipient, taskID string) {
	a.typingFinished(recipient, taskID)
	a.senders.taskFinished(recipient, taskID)
}

type uploadedMedia struct {
	mediaID    string
	mediaType  string
	filename   string
	contentLen int
	contentSHA string
}

func (a *Adapter) sendMediaRow(ctx context.Context, row *store.WhatsAppOutbound, media *mediaPayload) (string, error) {
	if media == nil {
		return "", fmt.Errorf("%w: media row carries no file", errOutboundUnsendable)
	}
	if !a.mediaTransfers.acquire(ctx) {
		return "", fmt.Errorf("%w: shutting down before a media transfer slot came free", errOutboundUnsendable)
	}
	up, err := a.uploadMediaRow(ctx, row, media)
	a.mediaTransfers.release()
	if err != nil {
		return "", err
	}
	wamid, sendErr := a.client.SendMedia(ctx, row.Recipient, up.mediaID, up.mediaType, up.filename)
	a.auditOutboundRecord(outboundAudit{
		wamid:      wamid,
		msgType:    up.mediaType,
		recipient:  row.Recipient,
		agentName:  row.AgentName,
		taskID:     row.TaskID,
		traceID:    row.TraceID,
		contentRef: up.filename,
		contentLen: up.contentLen,
		contentSHA: up.contentSHA,
		sendErr:    sendErr,
	})
	return wamid, sendErr
}

func (a *Adapter) uploadMediaRow(ctx context.Context, row *store.WhatsAppOutbound, media *mediaPayload) (uploadedMedia, error) {
	data, mimeType, ok := a.loadOutboundBytes(ctx, row, media)
	if !ok {
		return uploadedMedia{}, fmt.Errorf("%w: file could not be loaded", errOutboundUnsendable)
	}
	policy := a.checkOutboundMedia(mimeType, media.Filename, int64(len(data)))
	if !policy.ok {
		a.logger.Info("outbound whatsapp media rejected by policy",
			"outboundID", row.ID, "mediaType", policy.mediaType, "mime", policy.mime, "size", len(data))
		a.reply(row.TaskID, row.Recipient, policy.reason)
		return uploadedMedia{}, fmt.Errorf("%w: %w: %s", errOutboundUnsendable, errUserNotified, policy.mime)
	}
	filename := media.Filename
	if filename == "" {
		filename = "file"
	}
	up := uploadedMedia{
		mediaType:  policy.mediaType,
		filename:   filename,
		contentLen: len(data),
		contentSHA: contentDigest(data),
	}
	mediaID, err := a.client.UploadMedia(ctx, data, policy.mime, filename)
	if err != nil {
		a.auditOutbound(row, up.mediaType, filename, data, "", err)
		return uploadedMedia{}, err
	}
	up.mediaID = mediaID
	return up, nil
}

func (a *Adapter) loadOutboundBytes(ctx context.Context, row *store.WhatsAppOutbound, media *mediaPayload) ([]byte, string, bool) {
	if media.InlineText != "" {
		return []byte(media.InlineText), media.MIMEType, true
	}
	if row.ArtifactRef == "" {
		a.logger.Warn("media row has neither inline text nor an artifact reference", "outboundID", row.ID)
		return nil, "", false
	}
	return a.loadArtifactBytes(ctx, row, media.MIMEType)
}

func (a *Adapter) sendPromptRow(ctx context.Context, row *store.WhatsAppOutbound, payload outboundPayload) (string, error) {
	p := payload.Prompt
	if p == nil {
		return "", fmt.Errorf("%w: prompt row carries no prompt", errOutboundUnsendable)
	}
	wamid, err := a.sendPromptShape(ctx, row.Recipient, payload.Text, p)
	msgType := promptAuditType(p.Mode)
	if err != nil && p.FallbackText != "" && !promptRetryWorthwhile(err) {
		a.logger.Warn("interactive whatsapp prompt refused; falling back to text",
			append([]any{"outboundID", row.ID, "mode", p.Mode}, safeGraphErrFields(err)...)...)
		msgType = "text"
		wamid, err = a.client.SendText(ctx, row.Recipient, p.FallbackText)
		if p.Kind == promptKindLogin {
			a.loginButtonRefused.shouldWarn(row.Recipient, store.DefaultPendingAuthTTL)
		}
	}
	a.auditOutbound(row, msgType, "", []byte(payload.Text), wamid, err)
	return wamid, err
}

func promptAuditType(mode string) string {
	switch mode {
	case promptModeText:
		return "text"
	case promptModeTemplate:
		return "template"
	default:
		return "interactive"
	}
}

func (a *Adapter) sendPromptShape(ctx context.Context, to, body string, p *promptPayload) (string, error) {
	switch p.Mode {
	case promptModeButtons:
		return a.client.SendInteractiveButtons(ctx, to, body, p.Buttons)
	case promptModeList:
		return a.client.SendInteractiveListWithFooter(ctx, to, body, p.ButtonText, p.Rows, p.Footer)
	case promptModeCTAURL:
		if p.CTA == nil {
			return "", fmt.Errorf("%w: cta_url prompt carries no button", errOutboundUnsendable)
		}
		return a.client.SendInteractiveCTAURL(ctx, to, body, p.CTA.DisplayText, p.CTA.URL)
	case promptModeTemplate:
		if p.Template == nil || p.Template.Name == "" {
			return "", fmt.Errorf("%w: template prompt names no template", errOutboundUnsendable)
		}
		return a.client.SendTemplate(ctx, to, p.Template.Name, p.Template.Language)
	default:
		return a.client.SendText(ctx, to, body)
	}
}

func sendableOutsideWindow(payload *outboundPayload) bool {
	return payload.Prompt != nil && payload.Prompt.Mode == promptModeTemplate
}

func promptRetryWorthwhile(err error) bool {
	_, _, retryable, _ := classifySendError(err)
	return retryable
}

func (a *Adapter) onSendResult(row *store.WhatsAppOutbound, _ string, err error) {
	if err == nil {
		a.typingAfterSend(row.Recipient)
		return
	}
	a.onDeliveryFailed(row, !errors.Is(err, errUserNotified))
}

func (a *Adapter) onDeliveryFailed(row *store.WhatsAppOutbound, notify bool) {
	switch row.Kind {
	case kindInteractivePrompt:
		a.withdrawPrompt(row)
	case kindMedia:
		if notify {
			a.reply(row.TaskID, row.Recipient, msgAttachmentUndeliverable)
		}
	case kindText:
		a.withdrawRemainingChunks(row)
	}
}

func (a *Adapter) withdrawRemainingChunks(row *store.WhatsAppOutbound) {
	ctx, cancel := a.settleCtx()
	defer cancel()
	a.endTaskOnUserSide(row.Recipient, row.TaskID)
	withdrawn, err := a.outbound.SupersedeQueuedOfKind(ctx, a.gatewayID(), row.TaskID, kindText, nowMillis())
	if err != nil {
		a.logger.Error("failed to withdraw the rest of an undelivered whatsapp reply",
			"taskID", row.TaskID, "error", err)
		return
	}
	if withdrawn > 0 {
		a.logger.Warn("withdrew the remainder of a whatsapp reply after a delivery failure",
			"taskID", row.TaskID, "withdrawn", withdrawn)
		a.meter().Counter(metricWhatsAppSends, int64(withdrawn),
			"gatewayId", a.gatewayID(), "outcome", "superseded")
	}
}

func (a *Adapter) auditOutbound(row *store.WhatsAppOutbound, msgType, contentRef string, content []byte, wamid string, sendErr error) {
	a.auditOutboundRecord(outboundAudit{
		wamid:      wamid,
		msgType:    msgType,
		recipient:  row.Recipient,
		agentName:  row.AgentName,
		taskID:     row.TaskID,
		traceID:    row.TraceID,
		contentRef: contentRef,
		contentLen: len(content),
		contentSHA: contentDigest(content),
		sendErr:    sendErr,
	})
}
