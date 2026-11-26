// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/channels"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

var errConnectorUnavailable = errors.New("connector delivery not available")

type DeliveryRecord struct {
	ChannelType string `json:"channelType"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	SentAt      string `json:"sentAt"`
}

type Inbox interface {
	Add(ctx context.Context, userID, title, body string, metadata map[string]any, source, category string) (string, error)
}

type Deduplicator interface {
	AddDeduped(ctx context.Context, n store.NewNotification, dedup string) (id string, inserted bool, err error)
	ClaimDedup(ctx context.Context, userID, dedup string) (claimed bool, err error)
	HasDedup(ctx context.Context, userID, dedup string) (bool, error)
}

type InboxSignal interface {
	SignalInboxCreated(ctx context.Context, created a2a.NotifyInboxCreated) error
}

type Aliases interface {
	PlatformUserIDFor(ctx context.Context, loginID string) (string, error)
}

type Preferences interface {
	DisabledChannels(ctx context.Context, userID string) (map[string]bool, error)
}

type CategoryPreferences interface {
	DisabledCategories(ctx context.Context, userID string) (map[string]bool, error)
}

const msgCategoryMuted = "notify: skipped: category muted"

const msgInboxCellOff = "notify: skipped: in-app notifications off for category"

const inboxChannelType = "web"

type CellPreferences interface {
	DisabledCells(ctx context.Context, userID string) (map[string]bool, error)
}

func categoryGatesInbox(key string) bool {
	c, err := LookupCategory(key)
	return err == nil && c.GatesInbox
}

const msgChannelNotConfirmed = "notify: skipped: channel not confirmed"

const msgChannelDeclined = "notify: skipped: channel declined by its gateways"

const msgDuplicateDropped = "notify: already delivered under this request key; skipped"

const msgAlreadyDelivered = "already delivered for this request"

type ProactivePublisher interface {
	PublishDeliver(ctx context.Context, channelType string, d a2a.NotifyDeliver) error
}

type AddressStatus int

const (
	AddressUnconfirmed AddressStatus = iota
	AddressConfirmed
)

type LearnedChannel struct {
	ChannelType string
	Address     string
	Status      AddressStatus
}

type LearnedChannels interface {
	ChannelsFor(ctx context.Context, userID string) ([]LearnedChannel, error)
}

type Dispatcher struct {
	dir           Directory
	inbox         Inbox
	dedup         Deduplicator
	aliases       Aliases
	inboxSignal   InboxSignal
	prefs         Preferences
	categoryPrefs CategoryPreferences
	cellPrefs     CellPreferences
	connector     ConnectorInvoker
	proactive     ProactivePublisher
	learned       LearnedChannels
	capabilities  ChannelCapabilities
	httpClient    *http.Client
	logger        *slog.Logger

	skipURLValidation bool
}

func NewDispatcher(dir Directory, inbox Inbox, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		dir:        dir,
		inbox:      inbox,
		httpClient: channels.NewSSRFSafeHTTPClient(),
		logger:     logger.With("logger_name", "notify-dispatcher"),
	}
}

func (d *Dispatcher) SetConnectorInvoker(ci ConnectorInvoker) { d.connector = ci }

func (d *Dispatcher) SetDeduplicator(dd Deduplicator) { d.dedup = dd }

func (d *Dispatcher) SetAliases(a Aliases) { d.aliases = a }

func (d *Dispatcher) SetInboxSignal(s InboxSignal) { d.inboxSignal = s }

func (d *Dispatcher) ResolveRecipient(ctx context.Context, req a2a.NotifyRequest) (_ a2a.NotifyRequest, resolved bool) {
	if req.RecipientPlatformUserID != "" || req.RecipientUserID == "" || d.aliases == nil {
		return req, true
	}
	id, err := d.aliases.PlatformUserIDFor(ctx, req.RecipientUserID)
	if err != nil {
		d.logger.Error("notify: login id alias lookup failed; skipping out-of-band fan-out (inbox unaffected)",
			"requestID", req.RequestID, "error", err)
		return req, false
	}
	req.RecipientPlatformUserID = id
	return req, true
}

func (d *Dispatcher) SetPreferences(p Preferences) { d.prefs = p }

func (d *Dispatcher) SetCategoryPreferences(p CategoryPreferences) { d.categoryPrefs = p }

func (d *Dispatcher) SetCellPreferences(p CellPreferences) { d.cellPrefs = p }

func (d *Dispatcher) disabledCells(ctx context.Context, req a2a.NotifyRequest) (inboxOff bool, offChannels map[string]bool, ok bool) {
	if req.Category == "" || d.cellPrefs == nil {
		return false, nil, true
	}
	cells, err := d.cellPrefs.DisabledCells(ctx, recipientKey(req))
	if err != nil {
		d.logger.Error("notify: matrix preferences lookup failed; skipping out-of-band fan-out (inbox unaffected)",
			"requestID", req.RequestID, "recipient", recipientKey(req), "error", err)
		return false, nil, false
	}
	offChannels = make(map[string]bool, len(cells))
	for key := range cells {
		if channel, found := strings.CutPrefix(key, store.CellPrefix(req.Category)); found {
			offChannels[channel] = true
		}
	}
	return offChannels[store.PrefKey(inboxChannelType, "")], offChannels, true
}

func (d *Dispatcher) SetProactive(p ProactivePublisher, l LearnedChannels, c ChannelCapabilities) {
	d.proactive = p
	d.learned = l
	d.capabilities = c
}

func (d *Dispatcher) Deliver(ctx context.Context, req a2a.NotifyRequest) []DeliveryRecord {
	req, resolved := d.ResolveRecipient(ctx, req)
	md := inboxMetadata(req)
	payload := channels.Payload{Title: req.Title, Message: req.Message, Fields: md}
	key := recipientKey(req)

	mutedAll, mutedOutOfBand := d.categoryMutes(ctx, key, req)
	if mutedAll {
		return nil
	}
	skipOutOfBand := mutedOutOfBand || !resolved

	inboxOff, offChannels, cellsOK := d.disabledCells(ctx, req)
	inboxCellOff := inboxOff && categoryGatesInbox(req.Category)
	dk := d.deliveryKey(req)
	records := d.deliverInbox(ctx, key, req, md, dk, inboxCellOff)
	if skipOutOfBand {
		return records
	}

	configured, err := d.directoryChannels(ctx, req)
	if err != nil {
		d.logger.Error("notify: destination lookup failed",
			"requestID", req.RequestID, "recipient", key, "error", err)
		return append(records, DeliveryRecord{Status: "failed", Error: err.Error(), SentAt: nowISO()})
	}
	disabled, prefsFailed := d.outOfBandOptOuts(ctx, req, offChannels, cellsOK)

	records = append(records, d.fanOutConfigured(ctx, req, dk, payload, configured, disabled, prefsFailed)...)

	if d.proactiveWired() {
		records = append(records, d.deliverProactive(ctx, req, dk, disabled, prefsFailed)...)
	}

	if len(records) == 0 {
		d.logger.Warn("notify: no inbox and no reachable channel for recipient",
			"requestID", req.RequestID, "recipient", key, "requestedChannels", req.Channels)
		return nil
	}
	return records
}

func (d *Dispatcher) categoryMutes(ctx context.Context, key string, req a2a.NotifyRequest) (all, outOfBand bool) {
	if req.Category == "" || d.categoryPrefs == nil {
		return false, false
	}
	muted, gatesInbox := d.categoryMuted(ctx, key, req.Category)
	switch {
	case !muted:
		return false, false
	case gatesInbox:
		d.logger.Debug(msgCategoryMuted, "category", req.Category, "userId", key, "scope", "all")
		return true, false
	default:
		d.logger.Debug(msgCategoryMuted, "category", req.Category, "userId", key, "scope", "outOfBand")
		return false, true
	}
}

func (d *Dispatcher) sendConfigured(ctx context.Context, req a2a.NotifyRequest, ch Channel, payload channels.Payload) DeliveryRecord {
	rec := DeliveryRecord{ChannelType: ch.Type, SentAt: nowISO()}
	var sendErr error
	switch ch.Type {
	case "webhook":
		sendErr = channels.SendWebhook(ctx, d.httpClient, ch.Config, payload, d.skipURLValidation)
	case "connector":
		sendErr = d.deliverConnector(ctx, req, ch, payload)
		if errors.Is(sendErr, errConnectorUnavailable) {
			rec.Status = "skipped"
			rec.Error = "connector delivery unavailable (no invoker wired)"
			return rec
		}
	case "email", "slack", "teams":
		rec.Status = "skipped"
		rec.Error = "deliver " + ch.Type + " via a connector channel (type=connector); direct delivery not supported"
		d.logger.Info("notify: channel requires connector configuration; skipping",
			"requestID", req.RequestID, "channelType", ch.Type)
		return rec
	default:
		rec.Status = "failed"
		rec.Error = "unknown channel type: " + ch.Type
		d.logger.Warn("notify: unknown channel type", "requestID", req.RequestID, "channelType", ch.Type)
		return rec
	}
	if sendErr != nil {
		rec.Status = "failed"
		rec.Error = sendErr.Error()
		d.logger.Error("notify: channel delivery failed",
			"requestID", req.RequestID, "channelType", ch.Type, "error", sendErr)
		return rec
	}
	rec.Status = "sent"
	d.logger.Info("notify: delivered",
		"requestID", req.RequestID, "recipient", recipientKey(req), "channelType", ch.Type)
	return rec
}

func (d *Dispatcher) deliverInbox(ctx context.Context, key string, req a2a.NotifyRequest, md map[string]any, dk string, inboxCellOff bool) []DeliveryRecord {
	if d.inbox == nil || !wantsWeb(req) {
		return nil
	}
	if inboxCellOff {
		d.logger.Debug(msgInboxCellOff, "requestID", req.RequestID, "category", req.Category, "userId", key)
		return []DeliveryRecord{{ChannelType: inboxChannelType, Status: "skipped", Error: "in-app notifications turned off for this type", SentAt: nowISO()}}
	}
	rec := DeliveryRecord{ChannelType: inboxChannelType, SentAt: nowISO()}
	id, inserted, err := d.writeInbox(ctx, key, req, md, dk)
	switch {
	case err != nil:
		rec.Status = "failed"
		rec.Error = err.Error()
		d.logger.Error("notify: inbox write failed", "requestID", req.RequestID, "recipient", key, "error", err)
	case !inserted:
		rec.Status = "skipped"
		rec.Error = msgAlreadyDelivered
		d.logger.Debug(msgDuplicateDropped, "requestID", req.RequestID, "channelType", inboxChannelType)
	default:
		rec.Status = "sent"
		d.logger.Info("notify: inbox row written", "requestID", req.RequestID, "recipient", key)
		d.signalInboxCreated(ctx, key, req, id)
	}
	return []DeliveryRecord{rec}
}

func (d *Dispatcher) deliveryKey(req a2a.NotifyRequest) string {
	if d.dedup == nil || req.Mode == a2a.NotifyModeInput {
		return ""
	}
	return cmp.Or(req.Dedup, req.RequestID)
}

func channelMark(dk, channelType, destination string) string {
	return dk + "#" + channelType + "#" + destination
}

func markDestination(ch Channel) string {
	b, err := json.Marshal(ch.Config)
	if err != nil {
		return destinationKey(ch)
	}
	return destinationKey(ch) + "#" + shortHash(string(b))
}

func (d *Dispatcher) alreadySent(ctx context.Context, req a2a.NotifyRequest, mark string) bool {
	if mark == "" {
		return false
	}
	sent, err := d.dedup.HasDedup(ctx, recipientKey(req), mark)
	if err != nil {
		d.logger.Warn("notify: channel delivery record read failed; sending", "requestID", req.RequestID, "error", err)
		return false
	}
	return sent
}

func (d *Dispatcher) markSent(ctx context.Context, req a2a.NotifyRequest, mark string) {
	if mark == "" {
		return
	}
	if _, err := d.dedup.ClaimDedup(ctx, recipientKey(req), mark); err != nil {
		d.logger.Warn("notify: channel delivery record write failed; a copy of this request sends it again",
			"requestID", req.RequestID, "error", err)
	}
}

func (d *Dispatcher) sendOnce(ctx context.Context, req a2a.NotifyRequest, dk, channelType, destination string, send func() DeliveryRecord) DeliveryRecord {
	var mark string
	if dk != "" {
		mark = channelMark(dk, channelType, destination)
	}
	if d.alreadySent(ctx, req, mark) {
		d.logger.Debug(msgDuplicateDropped, "requestID", req.RequestID, "channelType", channelType)
		return DeliveryRecord{ChannelType: channelType, Status: "skipped", Error: msgAlreadyDelivered, SentAt: nowISO()}
	}
	rec := send()
	if rec.Status == "sent" {
		d.markSent(ctx, req, mark)
	}
	return rec
}

func (d *Dispatcher) outOfBandOptOuts(ctx context.Context, req a2a.NotifyRequest, offChannels map[string]bool, cellsOK bool) (disabled map[string]bool, prefsFailed bool) {
	prefsFailed = !cellsOK
	if d.prefs != nil {
		if dis, err := d.prefs.DisabledChannels(ctx, recipientKey(req)); err != nil {
			d.logger.Error("notify: preferences lookup failed; skipping out-of-band fan-out (inbox unaffected)",
				"requestID", req.RequestID, "recipient", recipientKey(req), "error", err)
			prefsFailed = true
		} else {
			disabled = dis
		}
	}
	if len(offChannels) > 0 && disabled == nil {
		disabled = make(map[string]bool, len(offChannels))
	}
	for k := range offChannels {
		disabled[k] = true
	}
	return disabled, prefsFailed
}

func (d *Dispatcher) fanOutConfigured(ctx context.Context, req a2a.NotifyRequest, dk string, payload channels.Payload, configured []Channel, disabled map[string]bool, prefsFailed bool) []DeliveryRecord {
	key := recipientKey(req)
	var records []DeliveryRecord
	for _, ch := range filterByTypes(configured, req.Channels) {
		if ch.Type == "web" {
			continue
		}
		if prefsFailed || disabled[store.PrefKey(ch.Type, destinationKey(ch))] {
			rec := DeliveryRecord{ChannelType: ch.Type, Status: "skipped", Error: "channel disabled by user preference", SentAt: nowISO()}
			if prefsFailed {
				rec.Error = "preferences unavailable; out-of-band fan-out suppressed"
			}
			d.logger.Info("notify: channel skipped",
				"requestID", req.RequestID, "recipient", key, "channelType", ch.Type, "reason", rec.Error)
			records = append(records, rec)
			continue
		}
		records = append(records, d.sendOnce(ctx, req, dk, ch.Type, markDestination(ch), func() DeliveryRecord {
			return d.sendConfigured(ctx, req, ch, payload)
		}))
	}
	return records
}

func (d *Dispatcher) proactiveWired() bool {
	return d.proactive != nil && d.learned != nil && d.capabilities != nil
}

func (d *Dispatcher) deliverProactive(ctx context.Context, req a2a.NotifyRequest, dk string, disabled map[string]bool, prefsFailed bool) []DeliveryRecord {
	if len(req.Channels) > 0 && !slices.ContainsFunc(req.Channels, gatewayDeliverable) {
		return nil
	}
	key := recipientKey(req)
	learned, err := d.learnedChannels(ctx, req)
	if err != nil {
		d.logger.Error("notify: proactive channel lookup failed",
			"requestID", req.RequestID, "recipient", key, "error", err)
		return lookupFailedRecords(req, err)
	}
	var recs []DeliveryRecord
	for _, lc := range learned {
		ct := lc.ChannelType
		if !gatewayDeliverable(ct) || !wantsChannel(req, ct) {
			continue
		}
		rec := DeliveryRecord{ChannelType: ct, SentAt: nowISO()}
		if prefsFailed || disabled[store.PrefKey(ct, "")] {
			rec.Status = "skipped"
			if prefsFailed {
				rec.Error = "preferences unavailable; proactive delivery suppressed"
			} else {
				rec.Error = "channel disabled by user preference"
			}
			recs = append(recs, rec)
			continue
		}
		if lc.Status != AddressConfirmed {
			rec.Status = "skipped"
			rec.Error = "channel not confirmed by the user"
			d.logger.Debug(msgChannelNotConfirmed,
				"requestID", req.RequestID, "recipient", key, "channelType", ct)
			recs = append(recs, rec)
			continue
		}
		if reason := declinedReason(req, d.capabilities.Capability(ct)); reason != "" {
			rec.Status = "skipped"
			rec.Error = reason
			d.logger.Debug(msgChannelDeclined,
				"requestID", req.RequestID, "recipient", key, "channelType", ct, "reason", reason)
			recs = append(recs, rec)
			continue
		}
		recs = append(recs, d.sendOnce(ctx, req, dk, ct, "", func() DeliveryRecord {
			return d.publishDeliver(ctx, req, key, lc, rec)
		}))
	}
	return recs
}

func (d *Dispatcher) publishDeliver(ctx context.Context, req a2a.NotifyRequest, key string, lc LearnedChannel, rec DeliveryRecord) DeliveryRecord {
	ct := lc.ChannelType
	deliver := a2a.NotifyDeliver{
		RequestID:   req.RequestID,
		Mode:        req.Mode,
		UserID:      cmp.Or(req.RecipientUserID, key),
		ChannelType: ct,
		Address:     json.RawMessage(lc.Address),
		Title:       req.Title,
		Message:     req.Message,
		Surface:     req.Surface,
		ExpiresAt:   req.ExpiresAt,
		Artifacts:   req.Artifacts,
		ApproverSet: req.Approvers != nil,
	}
	if err := d.proactive.PublishDeliver(ctx, ct, deliver); err != nil {
		rec.Status = "failed"
		rec.Error = err.Error()
		d.logger.Error("notify: proactive deliver publish failed",
			"requestID", req.RequestID, "channelType", ct, "error", err)
		return rec
	}
	rec.Status = "sent"
	d.logger.Info("notify: proactive deliver published",
		"requestID", req.RequestID, "recipient", key, "channelType", ct, "mode", req.Mode)
	return rec
}

func declinedReason(req a2a.NotifyRequest, c ChannelCapability) string {
	switch {
	case c.DeclinesDelivery():
		return "no gateway for this channel accepts notifications"
	case req.Mode == a2a.NotifyModeInput && c.DeclinesPrompts():
		return "channel cannot render interactive prompts"
	case req.Approvers != nil && !c.ApproverSets:
		return msgNoVerifiedAnswerer
	default:
		return ""
	}
}

const msgNoVerifiedAnswerer = "channel cannot verify who answers an approver-set prompt"

var directoryChannelTypes = []string{"web", "webhook", "connector", "email"}

func gatewayDeliverable(channelType string) bool {
	return a2a.ValidNotifyChannelType(channelType) && !slices.Contains(directoryChannelTypes, channelType)
}

func lookupFailedRecords(req a2a.NotifyRequest, err error) []DeliveryRecord {
	failed := func(channelType string) DeliveryRecord {
		return DeliveryRecord{ChannelType: channelType, Status: "failed", Error: err.Error(), SentAt: nowISO()}
	}
	if len(req.Channels) == 0 {
		return []DeliveryRecord{failed("")}
	}
	var recs []DeliveryRecord
	for _, ct := range req.Channels {
		if gatewayDeliverable(ct) {
			recs = append(recs, failed(ct))
		}
	}
	return recs
}

func (d *Dispatcher) writeInbox(ctx context.Context, key string, req a2a.NotifyRequest, md map[string]any, dk string) (id string, inserted bool, err error) {
	if dk != "" {
		return d.dedup.AddDeduped(ctx, store.NewNotification{
			UserID: key, Title: req.Title, Body: req.Message, Metadata: md, Source: notifySource(req), Category: req.Category,
		}, dk)
	}
	id, err = d.inbox.Add(ctx, key, req.Title, req.Message, md, notifySource(req), req.Category)
	return id, err == nil, err
}

func (d *Dispatcher) signalInboxCreated(ctx context.Context, key string, req a2a.NotifyRequest, id string) {
	if d.inboxSignal == nil {
		return
	}
	err := d.inboxSignal.SignalInboxCreated(ctx, a2a.NotifyInboxCreated{
		RecipientUserID:         req.RecipientUserID,
		RecipientPlatformUserID: req.RecipientPlatformUserID,
		NotificationID:          id,
	})
	if err != nil {
		d.logger.Warn("notify: inbox created signal not published",
			"requestID", req.RequestID, "notificationID", id, "recipient", key, "error", err)
	}
}

func (d *Dispatcher) directoryChannels(ctx context.Context, req a2a.NotifyRequest) ([]Channel, error) {
	for _, k := range destinationKeys(req) {
		chs, err := d.dir.ChannelsFor(ctx, k)
		if err != nil || len(chs) > 0 {
			return chs, err
		}
	}
	return nil, nil
}

func (d *Dispatcher) learnedChannels(ctx context.Context, req a2a.NotifyRequest) ([]LearnedChannel, error) {
	var out []LearnedChannel
	for _, k := range destinationKeys(req) {
		chs, err := d.learned.ChannelsFor(ctx, k)
		if err != nil {
			return nil, err
		}
		for _, lc := range chs {
			i := slices.IndexFunc(out, func(o LearnedChannel) bool { return o.ChannelType == lc.ChannelType })
			switch {
			case i < 0:
				out = append(out, lc)
			case out[i].Status != AddressConfirmed && lc.Status == AddressConfirmed:
				out[i] = lc
			}
		}
	}
	return out, nil
}

func (d *Dispatcher) categoryMuted(ctx context.Context, userID, category string) (muted, gatesInbox bool) {
	disabled, err := d.categoryPrefs.DisabledCategories(ctx, userID)
	if err != nil {
		d.logger.Warn("notify: category preferences lookup failed; delivering uncategorized",
			"category", category, "userId", userID, "error", err)
		return false, false
	}
	if !disabled[category] {
		return false, false
	}
	cat, err := LookupCategory(category)
	if err != nil {
		return false, false
	}
	return true, cat.GatesInbox
}

func wantsChannel(req a2a.NotifyRequest, channelType string) bool {
	if len(req.Channels) == 0 {
		return true
	}
	return slices.Contains(req.Channels, channelType)
}

func wantsWeb(req a2a.NotifyRequest) bool {
	if len(req.Channels) == 0 {
		return true
	}
	for _, c := range req.Channels {
		if c == "web" {
			return true
		}
	}
	return false
}

var reservedInboxMetadataKeys = []string{"mode", "request_id", "surface"}

func inboxMetadata(req a2a.NotifyRequest) map[string]any {
	var md map[string]any
	if len(req.Metadata) > 0 || req.Mode == a2a.NotifyModeInput {
		md = make(map[string]any, len(req.Metadata)+3)
		for k, v := range req.Metadata {
			if !slices.ContainsFunc(reservedInboxMetadataKeys, func(reserved string) bool { return strings.EqualFold(reserved, k) }) {
				md[k] = v
			}
		}
	}
	if req.Mode != a2a.NotifyModeInput {
		return md
	}
	md["mode"] = a2a.NotifyModeInput
	md["request_id"] = req.RequestID
	if len(req.Surface) > 0 {
		md["surface"] = req.Surface
	}
	return md
}

func notifySource(req a2a.NotifyRequest) string {
	if req.SenderAgent != "" {
		return req.SenderAgent
	}
	return "notify_user"
}

func (d *Dispatcher) deliverConnector(ctx context.Context, req a2a.NotifyRequest, ch Channel, payload channels.Payload) error {
	if d.connector == nil {
		d.logger.Info("notify: connector channel configured but no invoker is wired; skipping",
			"requestID", req.RequestID)
		return errConnectorUnavailable
	}
	cc, err := parseConnectorChannel(ch.Config)
	if err != nil {
		return err
	}
	cc.args[cc.textArg] = composeConnectorText(payload)
	userID := cmp.Or(req.SenderUserID, req.RecipientUserID, recipientKey(req))
	traceID, _ := req.Metadata["trace_id"].(string)
	_, err = d.connector.Invoke(ctx, cc.toolName, cc.strBinary, cc.args, userID, traceID)
	return err
}

func composeConnectorText(p channels.Payload) string {
	if p.Title != "" && p.Message != "" {
		return p.Title + "\n\n" + p.Message
	}
	return cmp.Or(p.Title, p.Message)
}

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
