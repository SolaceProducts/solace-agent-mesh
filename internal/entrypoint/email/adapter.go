// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/mail"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/hmacutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func (a *EmailAdapter) metrics() runtime.Metrics {
	if a.core == nil || a.core.Services() == nil {
		return runtime.NoopMetrics{}
	}
	return a.core.Services().Metrics()
}

func init() {
	entrypoint.RegisterAdapterFactory("email", func(cfg map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewEmailAdapter(cfg)
	})
}

type EmailTaskMeta struct {
	OriginalMessageID  string
	OriginalReferences []string
	ReplyTo            EmailAddress
	Subject            string
	UserID             string
	SessionID          string

	streamingMu     sync.Mutex
	streamingBuffer strings.Builder
	artifacts       []capturedArtifact

	missedStream bool
	textRecorded bool
}

type capturedArtifact struct {
	ArtifactID  string `json:"artifact_id,omitempty"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	URI         string `json:"uri"`
	Version     int    `json:"version"`
	Seq         int64  `json:"seq,omitempty"`
}

type EmailAdapter struct {
	core   *entrypoint.GatewayCore
	cfg    *EmailConfig
	rawCfg map[string]any
	logger *slog.Logger

	provider EmailProvider
	queue    *MessageQueue
	worker   *Worker

	driverMu sync.RWMutex
	driver   inboundDriver

	inFlight inFlightSet

	items   taskItems
	inbound mailRecordRepo

	retry retryPolicy

	runMu sync.Mutex
	run   *mailboxRun

	sessKeys *store.GatewaySessionKeyRepo

	replier replySender

	hil            *hilServer
	onHandlerReady func(http.Handler)

	taskMeta sync.Map

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewEmailAdapter(rawCfg map[string]any) (*EmailAdapter, error) {
	cfg, err := ParseEmailConfig(rawCfg)
	if err != nil {
		return nil, err
	}
	return &EmailAdapter{rawCfg: rawCfg, cfg: cfg, retry: defaultRetry}, nil
}

func (a *EmailAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNamed}
}

func (a *EmailAdapter) RelayedSurface() string { return httputil.SurfaceEmail }

func (a *EmailAdapter) Init(_ context.Context, core *entrypoint.GatewayCore) error {
	a.core = core

	if core != nil && core.Logger() != nil {
		a.logger = core.Logger().With("logger_name", "email-adapter")
	} else {
		a.logger = slog.Default().With("logger_name", "email-adapter")
	}

	if a.cfg.MaxAttachmentSizeBytes > 0 {
		if core == nil || core.Services() == nil || core.Services().Artifacts() == nil {
			return fmt.Errorf("email adapter: max_attachment_size_bytes>0 requires an artifact service (none wired by gateway core)")
		}
	}

	a.items = taskItems{logger: a.logger}
	if core != nil {
		if db := core.Store(); db != nil {
			a.sessKeys = store.NewGatewaySessionKeyRepo(db)
			a.items.repo = store.NewEntrypointTaskItemRepo(db)
			a.inbound = store.NewEntrypointInboundRepo(db)
		} else {
			a.logger.Warn("email adapter: no gateway store configured — session reuse disabled, every inbound email will create a new ephemeral session")
		}
	}

	if a.provider == nil {
		switch {
		case a.cfg.UsesBasicAuth():
			a.provider = NewImapProviderBasicAuth(a.cfg, a.logger)
		case a.cfg.UsesOAuth2():
			a.provider = NewImapProviderOAuth2(a.cfg, a.logger)
		default:
			return fmt.Errorf("email adapter: invalid auth configuration (neither basic auth nor OAuth2 set)")
		}
	}

	if a.cfg.UsesBasicAuth() {
		a.replier = &SMTPReplySender{
			Host:     a.cfg.SMTPHost,
			Port:     a.cfg.SMTPPort,
			Username: a.cfg.Mailbox,
			Password: a.cfg.IMAPPassword,
			From:     a.cfg.Mailbox,
			Logger:   a.logger,
		}
	} else if a.cfg.UsesOAuth2() {
		a.replier = NewGraphReplySender(a.cfg, a.logger)
	}

	a.worker = &Worker{
		Provider:                a.provider,
		Resolver:                NewEmailIdentityResolver(a.logger, a.cfg.RequireDMARCForIdentity),
		Policy:                  NewAuthPolicy(a.cfg.PolicyMode, a.logger),
		TrustedAuthservIDs:      a.cfg.TrustedAuthservIDs,
		MaxMessageSizeBytes:     a.cfg.MaxMessageSizeBytes,
		MaxAttachmentSizeBytes:  a.cfg.MaxAttachmentSizeBytes,
		Dispatcher:              &gatewayDispatcher{adapter: a},
		Refusals:                a.core,
		Logger:                  a.logger,
		TrustedInternalTenantID: a.cfg.TrustedInternalTenantID,
		AllowedSenders:          a.cfg.AllowedSenders,
		MIMEFilter:              &MIMEFilter{Allow: a.cfg.MIMEAllowlist, Deny: a.cfg.MIMEDenylist},
	}

	a.queue = a.newQueue()

	if secret := hilSigningSecret(core); a.replier != nil && a.cfg.ExternalBaseURL != "" && secret != "" {
		a.hil = newHILServer(core, hilKey(secret),
			a.cfg.ExternalBaseURL, core.GatewayID(), a.logger)
		a.hil.items = a.items
		a.logger.Info("email HIL magic-link form enabled", "baseURL", a.cfg.ExternalBaseURL)
	} else {
		a.logger.Info("email HIL disabled (need reply transport + external_base_url + session_secret_key); interactive prompts will be declined")
	}
	if a.onHandlerReady != nil {
		a.onHandlerReady(a.hilHandler())
	}

	return nil
}

func hilSigningSecret(core *entrypoint.GatewayCore) string {
	if core == nil {
		return ""
	}
	if cfg := core.Config(); cfg != nil && cfg.SessionSecretKey != "" {
		return cfg.SessionSecretKey
	}
	return os.Getenv("SESSION_SECRET_KEY")
}

func hilKey(secret string) []byte {
	return hmacutil.Sum([]byte("sam-email-hil-v1"), []byte(secret))
}

const driverProbeTimeout = 10 * time.Second

func (a *EmailAdapter) currentDriver() inboundDriver {
	a.driverMu.RLock()
	defer a.driverMu.RUnlock()
	return a.driver
}

func (a *EmailAdapter) setDriver(d inboundDriver) {
	a.driverMu.Lock()
	defer a.driverMu.Unlock()
	a.driver = d
}

func (a *EmailAdapter) makePoller() *Poller {
	return &Poller{
		Provider:        a.provider,
		Queue:           a.queue,
		IntervalSeconds: a.cfg.PollIntervalSeconds,
		FetchLimit:      a.cfg.FetchLimit,
		Logger:          a.logger,
		Metrics:         a.metrics(),
		Claim:           a.inFlight.Claim,
		OnAcked:         a.inFlight.Done,
	}
}

func (a *EmailAdapter) makeIdler(p IdleCapableProvider) *Idler {
	return &Idler{
		Provider:          p,
		Queue:             a.queue,
		FetchLimit:        a.cfg.FetchLimit,
		Logger:            a.logger,
		Metrics:           a.metrics(),
		Claim:             a.inFlight.Claim,
		OnAcked:           a.inFlight.Done,
		KeepaliveInterval: a.cfg.IdleKeepaliveInterval,
	}
}

func (a *EmailAdapter) runDriverSupervisor(ctx context.Context) {
	for {
		err := a.currentDriver().Run(ctx)
		if !errors.Is(err, errIdleDowngrade) {
			return
		}
		if a.cfg.DeliveryMode != DeliveryModeAuto {
			a.logger.LogAttrs(ctx, slog.LevelError, "idle downgrade requested but delivery_mode is strict",
				slog.String("eventType", "email.driver_downgrade_strict_idle"),
			)
			a.runDriverDownAlarm(ctx)
			return
		}
		poller := a.makePoller()
		a.setDriver(poller)
		a.logger.LogAttrs(ctx, slog.LevelWarn, "swapped Idler for Poller",
			slog.String("eventType", "email.driver_swapped"),
			slog.String("from", "*email.Idler"),
			slog.String("to", "*email.Poller"),
		)
	}
}

func (a *EmailAdapter) runDriverDownAlarm(ctx context.Context) {
	metrics := runtime.MetricsOrNoop(a.metrics())
	t := time.NewTicker(driverUnavailableAlarmInterval)
	defer t.Stop()
	a.emitDriverDownAlarm(ctx, metrics)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.emitDriverDownAlarm(ctx, metrics)
		}
	}
}

func (a *EmailAdapter) emitDriverDownAlarm(ctx context.Context, metrics runtime.Metrics) {
	a.logger.LogAttrs(ctx, slog.LevelError, "inbound driver unavailable; mail is not flowing",
		slog.String("eventType", "email.driver_unavailable"),
		slog.String("deliveryMode", string(a.cfg.DeliveryMode)),
	)
	metrics.Counter(MetricDriverUnavailable, 1, "delivery_mode", string(a.cfg.DeliveryMode))
}

func (a *EmailAdapter) selectDriver() (inboundDriver, error) {
	switch a.cfg.DeliveryMode {
	case DeliveryModePoll:
		return a.makePoller(), nil
	case DeliveryModeIdle:
		idleProvider, ok := a.provider.(IdleCapableProvider)
		if !ok || !idleProvider.SupportsIdle() {
			return nil, fmt.Errorf("email adapter: delivery_mode=idle but provider does not advertise IDLE capability")
		}
		return a.makeIdler(idleProvider), nil
	case DeliveryModeAuto, "":
		if idleProvider, ok := a.provider.(IdleCapableProvider); ok && idleProvider.SupportsIdle() {
			return a.makeIdler(idleProvider), nil
		}
		return a.makePoller(), nil
	default:
		return nil, fmt.Errorf("email adapter: unknown delivery_mode %q", a.cfg.DeliveryMode)
	}
}

func (a *EmailAdapter) Start(_ context.Context) error {
	a.ctx, a.cancel = context.WithCancel(context.Background())

	if a.core != nil && a.core.IngressOnLeader() {
		a.logger.LogAttrs(a.ctx, slog.LevelInfo, "email adapter started; the leader fetches the mailbox",
			slog.String("eventType", "email.adapter_started"),
			slog.Int("workerCount", a.cfg.WorkerCount),
			slog.String("deliveryMode", string(a.cfg.DeliveryMode)),
		)
		return nil
	}

	if err := a.openMailbox(a.ctx); err != nil {
		return err
	}
	a.wg.Go(func() { a.runDriverSupervisor(a.ctx) })
	return nil
}

func (a *EmailAdapter) probeForDriverSelection(parent context.Context) error {
	if a.cfg.DeliveryMode == DeliveryModePoll {
		return nil
	}

	probeCtx, cancel := context.WithTimeout(parent, driverProbeTimeout)
	defer cancel()

	if err := a.provider.Connect(probeCtx); err != nil {
		if a.cfg.DeliveryMode == DeliveryModeIdle {
			a.logConnectError(parent, slog.LevelError, "startup probe connect failed (delivery_mode=idle)", "email.driver_probe_failed", err)
			return fmt.Errorf("email adapter: startup connect failed (delivery_mode=idle): %w", err)
		}
		a.logConnectError(parent, slog.LevelWarn, "startup probe connect failed; falling back to poll", "email.driver_probe_failed", err)
	}
	return nil
}

func (a *EmailAdapter) logConnectError(ctx context.Context, level slog.Level, msg, eventType string, err error) {
	class, hint := ClassifyConnectionError(err)
	a.logger.LogAttrs(ctx, level, msg,
		slog.String("eventType", eventType),
		slog.String("errorClass", string(class)),
		slog.String("authMode", a.cfg.AuthMode()),
		slog.String("host", a.cfg.IMAPHost),
		slog.String("mailbox", a.cfg.Mailbox),
		slog.String("hint", hint),
		slog.String("error", sanitizeForLog(err.Error())),
	)
}

func (a *EmailAdapter) logReplySendError(ctx context.Context, msg, eventType string, err error, extra ...slog.Attr) {
	class, hint := ClassifyConnectionError(err)
	attrs := make([]slog.Attr, 0, 5+len(extra))
	attrs = append(attrs,
		slog.String("eventType", eventType),
		slog.String("errorClass", string(class)),
		slog.String("transport", a.replyTransport()),
		slog.String("hint", hint),
	)
	attrs = append(attrs, extra...)
	attrs = append(attrs, slog.String("error", sanitizeForLog(err.Error())))
	a.logger.LogAttrs(ctx, slog.LevelError, msg, attrs...)
}

func maskEmail(addr string) string {
	if addr == "" {
		return ""
	}
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return "***" + addr[i:]
	}
	return "***"
}

func (a *EmailAdapter) replyTransport() string {
	switch a.replier.(type) {
	case *GraphReplySender:
		return "graph"
	case *SMTPReplySender:
		return "smtp"
	default:
		return "unknown"
	}
}

func (a *EmailAdapter) Stop(ctx context.Context) error {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	if a.queue != nil {
		_ = a.queue.Stop(ctx)
	}
	if a.provider != nil {
		_ = a.provider.Disconnect(ctx)
	}
	if a.logger != nil {
		a.logger.LogAttrs(ctx, slog.LevelInfo, "email adapter stopped",
			slog.String("eventType", "email.adapter_stopped"),
		)
	}
	return nil
}

type gatewayDispatcher struct {
	adapter *EmailAdapter
}

func (d *gatewayDispatcher) Dispatch(ctx context.Context, n *NormalizedEmail, caller *gwauth.Subject, trust InternalTrustResult) (bool, error) {
	if caller == nil {
		return false, fmt.Errorf("email dispatch: no caller identity")
	}
	identityID := caller.UserID()
	a := d.adapter

	if handled, err := a.mailRecorded(ctx, n.MessageID); err != nil {
		return false, err
	} else if handled {
		return false, ErrAlreadyHandled
	}

	res := a.resolveAgentName(ctx)
	if res.Misconfigured {
		a.sendMisconfigBounce(ctx, n, trust)
		return false, nil
	}
	agentName := res.Name
	if agentName == "" {
		return false, fmt.Errorf("no agent available to handle email")
	}

	taskID := emailTaskID(a.core.GatewayID(), n.MessageID, n.ProviderUID)
	externalKey := emailExternalKey(n)
	sessionID, err := a.resolveSessionID(ctx, externalKey, agentName, identityID)
	if err != nil {
		return false, fmt.Errorf("resolve session id (external_key=%s): %w", externalKey, err)
	}

	body := stripReplyQuote(n.BodyText)
	if body == "" && n.BodyHTML != "" {
		body = stripReplyQuote(htmlToText(n.BodyHTML))
	}

	parts := []a2a.Part{a2a.NewTextPart(body)}
	if len(n.Attachments) > 0 {
		var artSvc *artifact.ScopedService
		if svc := a.core.Services(); svc != nil {
			artSvc = svc.Artifacts()
		}
		fileParts, ok := attachmentsToFileParts(ctx, n.Attachments, artSvc, identityID, sessionID, a.logger)
		if !ok {
			return false, fmt.Errorf("attachment save failed; retrying")
		}
		parts = append(parts, fileParts...)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     parts,
		TaskID:    taskID,
		ContextID: sessionID,
		Metadata: map[string]any{
			EmailAuthMetadataKey: buildEmailAuthMetadata(n.AuthResults, a.cfg.PolicyMode, trust),
		},
	}

	replyTo := resolveReplyTo(ctx, n, trust, a.logger)
	meta := &EmailTaskMeta{
		OriginalMessageID:  n.MessageID,
		OriginalReferences: n.References,
		ReplyTo:            replyTo,
		Subject:            n.Subject,
		UserID:             identityID,
		SessionID:          sessionID,
	}
	if !a.items.enabled() {
		a.taskMeta.Store(taskID, meta)
	}

	ctx = context.WithoutCancel(ctx)
	if err := a.core.Tasks().Register(taskID, agentName, identityID, entrypoint.TaskIdentity{}); err != nil {
		a.taskMeta.Delete(taskID)
		return false, fmt.Errorf("record task: %w", err)
	}
	if err := a.items.writeDestination(ctx, taskID, meta); err != nil {
		a.taskMeta.Delete(taskID)
		a.core.Tasks().Remove(taskID)
		return false, fmt.Errorf("record reply destination: %w", err)
	}
	if won, err := a.recordMail(ctx, n.MessageID); err != nil {
		a.taskMeta.Delete(taskID)
		return false, err
	} else if !won {
		a.taskMeta.Delete(taskID)
		return false, ErrAlreadyHandled
	}

	if err := a.core.SubmitTask(ctx, agentName, taskID, msg, caller); err != nil {
		a.taskMeta.Delete(taskID)
		switch {
		case entrypoint.IsBudgetRefusal(err):
			a.core.Tasks().Remove(taskID)
			a.sendBudgetBounce(ctx, n, trust, err)
		case entrypoint.IsAgentNotAvailable(err):
			a.core.Tasks().Remove(taskID)
			a.sendNotAvailableBounce(ctx, n, trust, err)
		case a.forgetMail(ctx, n.MessageID):
			a.core.Tasks().Remove(taskID)
		}
		return false, fmt.Errorf("submit task: %w", err)
	}

	var traceID string
	if info := a.core.Tasks().Get(taskID); info != nil {
		traceID = info.TraceID
	}
	a.logger.LogAttrs(ctx, slog.LevelInfo, "email task submitted",
		slog.String("eventType", "email.task_submitted"),
		slog.String("taskID", taskID),
		slog.String("traceID", traceID),
		slog.String("agentName", agentName),
		slog.String("identity", maskEmail(identityID)),
		slog.String("msgID", n.MessageID),
	)
	return true, nil
}

var ErrAlreadyHandled = errors.New("email already handled")

var emailTaskNamespace = uuid.MustParse("6f0d3c52-8f4e-4f7e-9a39-6f3b8d1c2e57")

func emailTaskID(gatewayID, messageID string, uid ProviderUID) string {
	return uuid.NewSHA1(emailTaskNamespace, []byte(gatewayID+"\x00"+messageID+"\x00"+string(uid))).String() //nolint:forbidigo // deterministic key for the native-uuid task_records column
}

func (a *EmailAdapter) mailRecorded(ctx context.Context, messageID string) (bool, error) {
	if a.inbound == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	seen, err := a.inbound.Recorded(ctx, a.core.GatewayID(), messageID)
	if err != nil {
		return false, fmt.Errorf("read mail record: %w", err)
	}
	return seen, nil
}

func (a *EmailAdapter) recordMail(ctx context.Context, messageID string) (bool, error) {
	if a.inbound == nil {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	won, err := a.inbound.Record(ctx, a.core.GatewayID(), messageID)
	if err != nil {
		return false, fmt.Errorf("write mail record: %w", err)
	}
	return won, nil
}

func (a *EmailAdapter) forgetMail(ctx context.Context, messageID string) bool {
	if a.inbound == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), taskItemTimeout)
	defer cancel()
	if err := a.inbound.Forget(ctx, a.core.GatewayID(), messageID); err != nil {
		a.logger.LogAttrs(ctx, slog.LevelError, "mail record not removed after a failed submit; the mail will get no reply",
			slog.String("eventType", "email.mail_record_forget_failed"),
			slog.String("msgID", messageID),
			slog.String("error", sanitizeForLog(err.Error())),
		)
		return false
	}
	return true
}

func (a *EmailAdapter) resolveAgentName(ctx context.Context) entrypoint.AgentResolution {
	if a.core == nil {
		return entrypoint.AgentResolution{}
	}
	reg := a.core.AgentRegistry()
	defaultName := ""
	if a.cfg != nil {
		defaultName = a.cfg.DefaultAgentName
	}
	res := reg.ResolveByDefaultName(defaultName)
	if res.Misconfigured {
		a.logger.LogAttrs(ctx, slog.LevelError, "default_agent_name not found in registry; dropping inbound email",
			slog.String("eventType", "email.default_agent_misconfigured"),
			slog.String("configured", defaultName),
			slog.Any("discovered", reg.Names()),
		)
	}
	return res
}

func (a *EmailAdapter) sendMisconfigBounce(ctx context.Context, n *NormalizedEmail, trust InternalTrustResult) {
	a.sendBounce(ctx, n, trust, "Your message could not be routed. This email entrypoint is configured to send mail to an agent that is not currently available. Please contact the entrypoint operator.",
		bounceKind{failedMsg: "misconfig bounce send failed", failedEvent: "email.misconfig_bounce_failed", sentMsg: "misconfig bounce sent", sentEvent: "email.misconfig_bounce_sent", sentLevel: slog.LevelInfo})
}

func (a *EmailAdapter) sendBudgetBounce(ctx context.Context, n *NormalizedEmail, trust InternalTrustResult, submitErr error) {
	if isAutomatedMail(n.RawHeaders) || strings.EqualFold(n.From.Address, a.cfg.Mailbox) {
		return
	}
	a.sendBounce(ctx, n, trust, a.core.SubmitFailureText(submitErr, false),
		bounceKind{failedMsg: "budget bounce send failed", failedEvent: "email.budget_bounce_failed", sentMsg: "budget bounce sent", sentEvent: "email.budget_bounce_sent", sentLevel: slog.LevelDebug})
}

func (a *EmailAdapter) sendNotAvailableBounce(ctx context.Context, n *NormalizedEmail, trust InternalTrustResult, submitErr error) {
	if isAutomatedMail(n.RawHeaders) || strings.EqualFold(n.From.Address, a.cfg.Mailbox) {
		return
	}
	a.sendBounce(ctx, n, trust, a.core.SubmitFailureText(submitErr, false),
		bounceKind{failedMsg: "agent not available bounce send failed", failedEvent: "email.not_available_bounce_failed", sentMsg: "agent not available bounce sent", sentEvent: "email.not_available_bounce_sent", sentLevel: slog.LevelDebug})
}

func isAutomatedMail(headers map[string][]string) bool {
	if v := strings.TrimSpace(firstHeader(headers, "Auto-Submitted")); v != "" && !strings.EqualFold(v, "no") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(firstHeader(headers, "Precedence"))) {
	case "bulk", "list", "junk", "auto_reply":
		return true
	}
	if firstHeader(headers, "List-Id") != "" || firstHeader(headers, "List-Unsubscribe") != "" ||
		firstHeader(headers, "X-Autoreply") != "" || firstHeader(headers, "X-Autorespond") != "" {
		return true
	}
	return suppressesAutoReplies(firstHeader(headers, "X-Auto-Response-Suppress")) || isDeliveryReport(headers)
}

func suppressesAutoReplies(value string) bool {
	for token := range strings.SplitSeq(value, ",") {
		switch strings.ToLower(strings.TrimSpace(token)) {
		case "all", "autoreply", "oof":
			return true
		}
	}
	return false
}

var deliveryReportSenders = []string{"mailer-daemon", "postmaster"}

func isDeliveryReport(headers map[string][]string) bool {
	if strings.TrimSpace(firstHeader(headers, "Return-Path")) == "<>" {
		return true
	}
	if mediaType, _, err := mime.ParseMediaType(firstHeader(headers, "Content-Type")); err == nil && mediaType == "multipart/report" {
		return true
	}
	from, err := mail.ParseAddress(firstHeader(headers, "From"))
	if err != nil {
		return false
	}
	local, _, _ := strings.Cut(strings.ToLower(from.Address), "@")
	return slices.Contains(deliveryReportSenders, local)
}

type bounceKind struct {
	failedMsg, failedEvent, sentMsg, sentEvent string
	sentLevel                                  slog.Level
}

func (a *EmailAdapter) sendBounce(ctx context.Context, n *NormalizedEmail, trust InternalTrustResult, body string, kind bounceKind) {
	if a.replier == nil {
		return
	}
	replyTo := resolveReplyTo(ctx, n, trust, a.logger)
	if err := a.replier.Send(ctx, ReplyRequest{
		To:                 replyTo.Address,
		Subject:            n.Subject,
		BodyText:           body,
		OriginalMessageID:  n.MessageID,
		OriginalReferences: n.References,
		AutoReply:          true,
	}); err != nil {
		a.logReplySendError(ctx, kind.failedMsg, kind.failedEvent, err,
			slog.String("to", maskEmail(replyTo.Address)),
		)
		return
	}
	a.logger.LogAttrs(ctx, kind.sentLevel, kind.sentMsg,
		slog.String("eventType", kind.sentEvent),
		slog.String("to", maskEmail(replyTo.Address)),
	)
}

func resolveReplyTo(ctx context.Context, n *NormalizedEmail, trust InternalTrustResult, logger *slog.Logger) EmailAddress {
	if n.ReplyTo == nil {
		return n.From
	}
	replyDomain := domainOf(n.ReplyTo.Address)
	if replyDomain == "" {
		return n.From
	}
	aligned := strings.ToLower(strings.TrimSpace(n.AuthResults.AlignedFromDomain))
	if aligned == "" {
		aligned = domainOf(n.From.Address)
	}
	if strings.EqualFold(replyDomain, aligned) {
		return *n.ReplyTo
	}
	if logger != nil {
		logger.LogAttrs(ctx, slog.LevelInfo, "reply-to domain mismatch; falling back to From",
			slog.String("eventType", "email.reply_to_rejected"),
			slog.String("reason", "reply_to_domain_unaligned"),
			slog.String("replyToDomain", replyDomain),
			slog.String("alignedDomain", aligned),
			slog.Bool("internalTrusted", trust.Trusted),
		)
	}
	return n.From
}

func emailExternalKey(n *NormalizedEmail) string {
	root := n.ThreadID
	if root == "" {
		root = n.MessageID
	}
	return root
}

func (a *EmailAdapter) resolveSessionID(ctx context.Context, externalKey, agentName, userID string) (string, error) {
	if a.sessKeys == nil {
		return idgen.New(), nil
	}
	return a.sessKeys.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeEmail,
		ExternalKey: externalKey,
		UserID:      userID,
		AgentID:     &agentName,
	})
}

func (a *EmailAdapter) RendersStatusText(string) bool { return true }

var _ entrypoint.StatusTextRenderer = (*EmailAdapter)(nil)

func (a *EmailAdapter) OnStatusUpdate(ctx context.Context, taskID string, ev *a2a.TaskStatusUpdateEvent, _ []byte) {
	if ev == nil || ev.Status.Message == nil {
		return
	}
	v, ok := a.taskMeta.Load(taskID)
	if !ok {
		return
	}
	meta, ok := v.(*EmailTaskMeta)
	if !ok {
		return
	}

	if req, isHIL := a2ui.ExtractRequest(ev); isHIL {
		if a.hil != nil {
			a.sendHILEmail(ctx, taskID, meta, req)
		} else {
			a.logger.Warn("received HIL request but email HIL is not configured", "taskID", taskID, "requestID", req.RequestID)
		}
		return
	}

	meta.streamingMu.Lock()
	defer meta.streamingMu.Unlock()
	for _, part := range ev.Status.Message.Parts {
		switch part.Kind {
		case "text":
			if part.Text != "" {
				meta.streamingBuffer.WriteString(part.Text)
				if !meta.textRecorded {
					meta.textRecorded = true
					a.items.put(ctx, taskID, itemTextStarted, "")
				}
			}
		case "data":
			a.captureArtifactSavedSignalLocked(ctx, taskID, meta, part.Data)
		}
	}
}

func (a *EmailAdapter) captureArtifactSavedSignalLocked(ctx context.Context, taskID string, meta *EmailTaskMeta, payload any) {
	if a.cfg != nil && a.cfg.MaxOutboundAttachmentSizeBytes == 0 {
		return
	}

	filename, mimeType, version, tags, ok := readArtifactSavedSignal(payload)
	if !ok {
		return
	}
	for _, tag := range tags {
		if tag == a2a.ArtifactTagWorking {
			return
		}
	}

	var artSvc *artifact.ScopedService
	agentName := ""
	if a.core != nil {
		if svc := a.core.Services(); svc != nil {
			artSvc = svc.Artifacts()
		}
		if info := a.core.Tasks().Get(taskID); info != nil {
			agentName = info.AgentName
		}
	}
	if artSvc == nil {
		return
	}

	loc := artSvc.Resolve(agentName, meta.UserID, meta.SessionID, filename)
	uri := buildArtifactURI(loc, version)

	captured := capturedArtifact{
		Filename:    filename,
		ContentType: mimeType,
		URI:         uri,
		Version:     version,
	}
	if containsCapturedArtifactLocked(meta.artifacts, captured) {
		return
	}
	captured.Seq = time.Now().UnixNano()
	meta.artifacts = append(meta.artifacts, captured)
	a.items.recordArtifact(ctx, taskID, captured)
	a.logger.LogAttrs(ctx, slog.LevelDebug, "reply attachment captured",
		slog.String("eventType", "email.reply_attachment_captured"),
		slog.String("taskID", taskID),
		slog.String("name", filename),
		slog.String("contentType", mimeType),
	)
}

func readArtifactSavedSignal(payload any) (filename, mimeType string, version int, tags []string, ok bool) {
	switch p := payload.(type) {
	case map[string]any:
		if t, _ := p["type"].(string); t != string(a2a.SignalArtifactSaved) {
			return "", "", 0, nil, false
		}
		filename, _ = p["filename"].(string)
		mimeType, _ = p["mime_type"].(string)
		switch v := p["version"].(type) {
		case float64:
			version = int(v)
		case int:
			version = v
		case int64:
			version = int(v)
		}
		if raw, ok := p["tags"].([]any); ok {
			for _, t := range raw {
				if s, ok := t.(string); ok {
					tags = append(tags, s)
				}
			}
		}
		if filename == "" {
			return "", "", 0, nil, false
		}
		return filename, mimeType, version, tags, true
	case a2a.ArtifactSavedData:
		if p.Type != string(a2a.SignalArtifactSaved) || p.Filename == "" {
			return "", "", 0, nil, false
		}
		return p.Filename, p.MIMEType, p.Version, p.Tags, true
	}
	return "", "", 0, nil, false
}

func (a *EmailAdapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, _ []byte) {
	v, ok := a.taskMeta.LoadAndDelete(taskID)
	if !ok {
		return
	}
	meta, ok := v.(*EmailTaskMeta)
	if !ok {
		return
	}

	meta.streamingMu.Lock()
	finalText := finalMessageText(task)
	body := meta.streamingBuffer.String()
	if body == "" || (meta.missedStream && finalText != "") {
		body = finalText
	}
	if strings.TrimSpace(body) == "" {
		body = emptyAnswerNotice(task)
	}
	captured := append([]capturedArtifact(nil), meta.artifacts...)
	meta.streamingMu.Unlock()

	if a.replier == nil {
		a.logger.LogAttrs(ctx, slog.LevelInfo, "agent response (no reply transport configured)",
			slog.String("eventType", "email.reply_pending"),
			slog.String("taskID", taskID),
			slog.String("to", maskEmail(meta.ReplyTo.Address)),
			slog.String("originalMessageID", meta.OriginalMessageID),
			slog.Int("bodyLen", len(body)),
		)
		return
	}

	if strings.TrimSpace(body) == "" && len(captured) == 0 {
		a.logger.LogAttrs(ctx, slog.LevelInfo, "reply skipped: empty body",
			slog.String("eventType", "email.reply_skipped"),
			slog.String("taskID", taskID),
		)
		return
	}

	if !a.items.claim(ctx, taskID, itemReplySent) {
		a.logger.LogAttrs(ctx, slog.LevelInfo, "reply already sent by another instance",
			slog.String("eventType", "email.reply_already_sent"),
			slog.String("taskID", taskID),
		)
		return
	}

	attachments, dropped := a.buildOutboundAttachments(ctx, taskID, captured)
	attachments, droppedByTransport := filterUnsupportedAttachments(ctx, a.logger, taskID, a.replier.SupportsAttachments(), attachments)
	dropped = dropped || droppedByTransport
	if dropped {
		body += "\n\n*One or more attachments could not be included in this reply. See gateway logs for details.*"
	}

	err := a.replier.Send(ctx, ReplyRequest{
		To:                 meta.ReplyTo.Address,
		Subject:            meta.Subject,
		BodyText:           body,
		OriginalMessageID:  meta.OriginalMessageID,
		OriginalReferences: meta.OriginalReferences,
		Attachments:        attachments,
	})
	if err != nil {
		a.logReplySendError(ctx, "reply send failed", "email.reply_failed", err,
			slog.String("taskID", taskID),
			slog.String("to", maskEmail(meta.ReplyTo.Address)),
		)
		return
	}

	for _, att := range attachments {
		a.logger.LogAttrs(ctx, slog.LevelInfo, "reply attachment attached",
			slog.String("eventType", "email.reply_attachment_attached"),
			slog.String("taskID", taskID),
			slog.String("name", att.Filename),
			slog.Int("size_bytes", len(att.Bytes)),
		)
	}

	a.logger.LogAttrs(ctx, slog.LevelInfo, "reply sent",
		slog.String("eventType", "email.reply_sent"),
		slog.String("taskID", taskID),
		slog.String("to", maskEmail(meta.ReplyTo.Address)),
		slog.Int("bodyLen", len(body)),
	)
}

func (a *EmailAdapter) OnArtifactUpdate(ctx context.Context, taskID string, ev *a2a.TaskArtifactUpdateEvent, _ []byte) {
	if ev == nil {
		return
	}
	if a.cfg != nil && a.cfg.MaxOutboundAttachmentSizeBytes == 0 {
		return
	}
	for _, tag := range ev.Artifact.Tags {
		if tag == a2a.ArtifactTagWorking {
			return
		}
	}
	v, ok := a.taskMeta.Load(taskID)
	if !ok {
		return
	}
	meta, ok := v.(*EmailTaskMeta)
	if !ok {
		return
	}

	for _, part := range ev.Artifact.Parts {
		if part.Kind != "file" || part.File == nil {
			continue
		}
		uri := part.File.URI
		if uri == "" {
			a.logger.LogAttrs(ctx, slog.LevelDebug, "artifact dropped: no URI",
				slog.String("eventType", "email.reply_attachment_dropped"),
				slog.String("taskID", taskID),
				slog.String("name", part.File.Name),
				slog.String("reason", "no_artifact_uri"),
			)
			continue
		}

		filename := part.File.Name
		if filename == "" {
			filename = ev.Artifact.Name
		}
		mimeType := part.File.MIMEType
		version := versionFromArtifactURI(uri)

		captured := capturedArtifact{
			ArtifactID:  ev.Artifact.ArtifactID,
			Filename:    filename,
			ContentType: mimeType,
			URI:         uri,
			Version:     version,
		}

		meta.streamingMu.Lock()
		if !containsCapturedArtifactLocked(meta.artifacts, captured) {
			captured.Seq = time.Now().UnixNano()
			meta.artifacts = append(meta.artifacts, captured)
			a.items.recordArtifact(ctx, taskID, captured)
			a.logger.LogAttrs(ctx, slog.LevelDebug, "reply attachment captured",
				slog.String("eventType", "email.reply_attachment_captured"),
				slog.String("taskID", taskID),
				slog.String("name", filename),
				slog.String("contentType", mimeType),
			)
		}
		meta.streamingMu.Unlock()
	}
}

func containsCapturedArtifactLocked(s []capturedArtifact, c capturedArtifact) bool {
	for _, e := range s {
		if e.Filename == c.Filename && e.Version == c.Version {
			return true
		}
	}
	return false
}

func versionFromArtifactURI(uri string) int {
	_, v, err := parseArtifactURI(uri)
	if err != nil {
		return 0
	}
	return v
}

func (a *EmailAdapter) OnError(ctx context.Context, taskID string, taskErr *a2a.Error, _ []byte) {
	if err := a.LoadTask(ctx, taskID); err != nil {
		a.logger.Warn("email task items not read for an error reply", "taskID", taskID, "error", err)
	}
	v, ok := a.taskMeta.LoadAndDelete(taskID)
	if !ok {
		a.logger.Warn("error for unknown task", "taskID", taskID)
		return
	}
	meta, ok := v.(*EmailTaskMeta)
	if !ok {
		return
	}
	a.logger.LogAttrs(ctx, slog.LevelWarn, "task error",
		slog.String("eventType", "email.task_error"),
		slog.String("taskID", taskID),
		slog.String("to", maskEmail(meta.ReplyTo.Address)),
		slog.String("error", sanitizeForLog(taskErr.Message)),
	)

	if a.replier == nil {
		return
	}
	if !a.items.claim(ctx, taskID, itemReplySent) {
		a.logger.LogAttrs(ctx, slog.LevelInfo, "reply already sent by another instance",
			slog.String("eventType", "email.reply_already_sent"),
			slog.String("taskID", taskID),
		)
		return
	}
	body := errorReplyText
	if err := a.replier.Send(ctx, ReplyRequest{
		To:                 meta.ReplyTo.Address,
		Subject:            meta.Subject,
		BodyText:           body,
		OriginalMessageID:  meta.OriginalMessageID,
		OriginalReferences: meta.OriginalReferences,
	}); err != nil {
		a.logReplySendError(ctx, "error reply send failed", "email.error_reply_failed", err,
			slog.String("taskID", taskID),
			slog.String("to", maskEmail(meta.ReplyTo.Address)),
		)
		return
	}
	a.logger.LogAttrs(ctx, slog.LevelInfo, "error reply sent",
		slog.String("eventType", "email.error_reply_sent"),
		slog.String("taskID", taskID),
		slog.String("to", maskEmail(meta.ReplyTo.Address)),
	)
}

const (
	errorReplyText     = "Your request couldn't be processed. The system encountered an internal error. Please try again or contact support if the problem persists."
	cancelledReplyText = "Your request was cancelled before an answer was ready. Please send it again."
)

func emptyAnswerNotice(task *a2a.Task) string {
	if task == nil {
		return ""
	}
	switch task.Status.State {
	case a2a.TaskStateCancelled:
		return cancelledReplyText
	case a2a.TaskStateFailed, a2a.TaskStateRejected:
		return errorReplyText
	}
	return ""
}

func finalMessageText(task *a2a.Task) string {
	if task == nil || task.Status.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

var _ entrypoint.TaskStateLoader = (*EmailAdapter)(nil)

func (a *EmailAdapter) LoadTask(ctx context.Context, taskID string) error {
	if !a.items.enabled() {
		return nil
	}
	if _, ok := a.taskMeta.Load(taskID); ok {
		return nil
	}
	items, err := a.items.list(ctx, taskID)
	if err != nil {
		return fmt.Errorf("read email task items: %w", err)
	}
	if meta := metaFromItems(items, a.logger, taskID); meta != nil {
		a.taskMeta.LoadOrStore(taskID, meta)
	}
	return nil
}

func (a *EmailAdapter) OnAgentDiscovered(card *a2a.AgentCard) {
	if a.logger != nil {
		a.logger.Info("agent discovered", "agentName", card.Name)
	}
}

func (a *EmailAdapter) OnAgentRemoved(name string) {
	if a.logger != nil {
		a.logger.Info("agent removed", "agentName", name)
	}
}

type replySender interface {
	Send(ctx context.Context, req ReplyRequest) error
	SupportsAttachments() bool
}

var _ entrypoint.GatewayAdapter = (*EmailAdapter)(nil)
var _ replySender = (*SMTPReplySender)(nil)
var _ replySender = (*GraphReplySender)(nil)
