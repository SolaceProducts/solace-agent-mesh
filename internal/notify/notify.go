// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type NotifyExtensions struct {
	AuthMiddleware func(next http.Handler) http.Handler
}

type NotifyCore struct {
	appConfig  map[string]any
	cfg        *NotifyConfig
	ext        NotifyExtensions
	svc        runtime.Services
	logger     *slog.Logger
	handler    http.Handler
	dispatcher *Dispatcher
	corsRegex  *regexp.Regexp

	db            *store.DB
	adoptions     adoptions
	inbox         *store.NotificationRepo
	prefs         *store.PreferenceRepo
	categoryPrefs *store.CategoryPreferenceRepo
	cellPrefs     *store.CellPreferenceRepo
	pending       *store.PendingRequestRepo
	cards         hilCardStore
	channels      *store.UserChannelRepo

	dir Directory

	connectorInvoker *executorInvoker

	capabilities *GatewayCapabilityRegistry

	queue   broker.Queue
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started atomic.Bool

	onHandlerReady func(http.Handler)
}

const notifyWorkers = 8

const pruneInterval = time.Hour

func NewNotifyCore(appConfig map[string]any, ext NotifyExtensions) *NotifyCore {
	return &NotifyCore{appConfig: appConfig, ext: ext}
}

func (n *NotifyCore) SetServices(svc runtime.Services) { n.svc = svc }

func (n *NotifyCore) SetOnHandlerReady(fn func(http.Handler)) { n.onHandlerReady = fn }

func (n *NotifyCore) Init(ctx context.Context, _ config.Config) error {
	cfg, err := ParseNotifyConfig(n.appConfig)
	if err != nil {
		return fmt.Errorf("parse notify config: %w", err)
	}
	n.cfg = cfg
	n.logger = slog.Default().With("logger_name", "notify")

	if cfg.CORSAllowedOriginRegex != "" {
		rx, rerr := regexp.Compile(cfg.CORSAllowedOriginRegex)
		if rerr != nil {
			return fmt.Errorf("invalid cors_allowed_origin_regex %q: %w", cfg.CORSAllowedOriginRegex, rerr)
		}
		n.corsRegex = rx
	}

	if n.svc == nil || n.svc.Broker() == nil {
		return fmt.Errorf("notify: broker is required")
	}

	if cfg.DatabaseURL != "" {
		db, err := store.Open(cfg.DatabaseURL, n.logger)
		if err != nil {
			return fmt.Errorf("open notify db: %w", err)
		}
		if err := db.Migrate(ctx); err != nil {
			return fmt.Errorf("migrate notify db: %w", err)
		}
		n.db = db
		n.inbox = store.NewNotificationRepo(db)
		n.prefs = store.NewPreferenceRepo(db)
		n.categoryPrefs = store.NewCategoryPreferenceRepo(db)
		n.cellPrefs = store.NewCellPreferenceRepo(db)
		n.pending = store.NewPendingRequestRepo(db)
		n.cards = store.NewHILCardRepo(db)
		n.channels = store.NewUserChannelRepo(db)
		n.logger.Info("notify inbox enabled")
	}

	dir := newConfigDirectory(cfg.Directory)
	n.dir = dir
	var inbox Inbox
	if n.inbox != nil {
		inbox = n.inbox
	}
	n.dispatcher = NewDispatcher(dir, inbox, n.logger)
	if n.inbox != nil {
		n.dispatcher.SetDeduplicator(n.inbox)
		n.dispatcher.SetAliases(n.db)
		n.dispatcher.SetInboxSignal(n)
	}
	if n.prefs != nil {
		n.dispatcher.SetPreferences(n.prefs)
	}
	if n.categoryPrefs != nil {
		n.dispatcher.SetCategoryPreferences(n.categoryPrefs)
	}
	if n.cellPrefs != nil {
		n.dispatcher.SetCellPreferences(n.cellPrefs)
	}
	if n.channels != nil {
		n.capabilities = NewGatewayCapabilityRegistry(gatewayCapabilityTTL)
		n.dispatcher.SetProactive(n, n, n.capabilities)
	}

	n.connectorInvoker = newExecutorInvoker(n.svc.Broker(), cfg.Namespace)
	n.dispatcher.SetConnectorInvoker(n.connectorInvoker)

	n.handler = n.buildHandler()
	if n.onHandlerReady != nil {
		n.onHandlerReady(n.handler)
	}

	n.logger.Info("notify service initialized",
		"namespace", cfg.Namespace, "directoryUsers", len(cfg.Directory))
	return nil
}

func (n *NotifyCore) Start(ctx context.Context) error {
	brk := n.svc.Broker()
	queueName := a2a.QueueName(n.cfg.Namespace, "notify", "request")
	q, err := brk.CreateQueue(queueName, broker.QueueOptions{
		Named:         true,
		Exclusive:     false,
		MaxRedelivery: 3,
	})
	if err != nil {
		return fmt.Errorf("create notify request queue %q: %w", queueName, err)
	}
	if err := brk.Subscribe(q, a2a.NotifyRequestSubscription(n.cfg.Namespace)); err != nil {
		return fmt.Errorf("subscribe notify requests: %w", err)
	}
	n.queue = q

	loopCtx, cancel := context.WithCancel(ctx)
	n.cancel = cancel

	if n.connectorInvoker != nil {
		if err := n.connectorInvoker.Start(loopCtx); err != nil {
			cancel()
			return fmt.Errorf("start connector invoker: %w", err)
		}
	}

	n.wg.Go(func() { n.receiveLoop(loopCtx, q) })

	if n.pending != nil {
		if err := n.startHILRouter(loopCtx); err != nil {
			cancel()
			return fmt.Errorf("start HIL router: %w", err)
		}
	}

	if n.channels != nil {
		if err := n.startPresenceSubscriber(loopCtx); err != nil {
			cancel()
			return fmt.Errorf("start presence subscriber: %w", err)
		}
	}

	if n.capabilities != nil {
		if err := n.startCapabilityDiscovery(loopCtx); err != nil {
			cancel()
			return fmt.Errorf("start capability discovery: %w", err)
		}
	}

	n.warnIfApprovalsNotAudited()

	n.started.Store(true)
	n.logger.Info("notify service started", "queue", queueName, "workers", notifyWorkers, "retentionDays", n.cfg.RetentionDays)
	return nil
}

func (n *NotifyCore) LeaderJobs() []leader.Job {
	if n.inbox == nil || n.cfg.RetentionDays <= 0 {
		return nil
	}
	return []leader.Job{{
		Name:  leader.JobNotifyInboxPrune,
		Every: pruneInterval,
		Run:   func(ctx context.Context, _ leader.Term) error { return n.prune(ctx) },
	}}
}

func (n *NotifyCore) prune(ctx context.Context) error {
	cutoff := time.Now().Add(-time.Duration(n.cfg.RetentionDays) * 24 * time.Hour).UnixMilli()
	var errs []error
	switch deleted, err := n.inbox.DeleteOlderThan(ctx, cutoff); {
	case err != nil:
		errs = append(errs, fmt.Errorf("prune inbox: %w", err))
	case deleted > 0:
		n.logger.Info("notify: pruned old notifications", "deleted", deleted, "retentionDays", n.cfg.RetentionDays)
	}
	switch keys, kerr := n.inbox.DeleteDedupKeysOlderThan(ctx, cutoff); {
	case kerr != nil:
		errs = append(errs, fmt.Errorf("prune dedup keys: %w", kerr))
	case keys > 0:
		n.logger.Info("notify: pruned old dedup keys", "deleted", keys)
	}

	if n.pending == nil {
		return errors.Join(errs...)
	}
	switch reclaimed, perr := n.pending.DeleteResolvedOlderThan(ctx, cutoff); {
	case perr != nil:
		errs = append(errs, fmt.Errorf("prune resolved HIL requests: %w", perr))
	case reclaimed > 0:
		n.logger.Info("notify: pruned resolved HIL requests", "deleted", reclaimed)
	}
	switch stale, perr := n.pending.DeleteStalePending(ctx, cutoff); {
	case perr != nil:
		errs = append(errs, fmt.Errorf("prune stale pending HIL requests: %w", perr))
	case stale > 0:
		n.logger.Info("notify: pruned stale pending HIL requests", "deleted", stale)
	}
	switch orphans, cerr := n.cards.DeleteOrphans(ctx); {
	case cerr != nil:
		errs = append(errs, fmt.Errorf("prune posted HIL prompts: %w", cerr))
	case orphans > 0:
		n.logger.Info("notify: pruned posted HIL prompts of removed requests", "deleted", orphans)
	}
	return errors.Join(errs...)
}

func (n *NotifyCore) Stop(ctx context.Context) error {
	if n.cancel != nil {
		n.cancel()
	}
	done := make(chan struct{})
	go func() { n.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		n.logger.Warn("notify: stop deadline reached before workers drained", "error", ctx.Err())
	}
	if n.connectorInvoker != nil {
		n.connectorInvoker.Stop()
	}
	if n.db != nil {
		if err := n.db.Close(); err != nil {
			n.logger.Warn("notify: error closing database", "error", err)
		}
	}
	return nil
}

func (n *NotifyCore) Health() error {
	if n.started.Load() && n.svc != nil && n.svc.Broker() != nil && !n.svc.Broker().IsConnected() {
		return errors.New("notify: broker disconnected")
	}
	return nil
}

func (n *NotifyCore) receiveLoop(ctx context.Context, q broker.Queue) {
	const maxBackoff = 5 * time.Second
	backoff := 50 * time.Millisecond
	sem := make(chan struct{}, notifyWorkers)
	for {
		msg, err := n.svc.Broker().Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			n.logger.Warn("notify: receive error; backing off", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, maxBackoff)
			continue
		}
		backoff = 50 * time.Millisecond
		if msg == nil {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		n.wg.Go(func() {
			defer func() { <-sem }()
			n.handleMessage(ctx, msg)
		})
	}
}

func (n *NotifyCore) handleMessage(ctx context.Context, msg *broker.Message) {
	defer func() {
		if r := recover(); r != nil {
			n.logger.Error("notify: panic handling message; acking to avoid poison-pill redelivery",
				"panic", r, "topic", msg.Topic)
			msg.Ack()
		}
	}()

	var req a2a.NotifyRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		n.logger.Error("notify: malformed NotifyRequest; dropping", "error", err, "topic", msg.Topic)
		msg.Ack()
		return
	}
	if req.Approvers != nil && req.Mode != a2a.NotifyModeInput {
		n.logger.Error("notify: NotifyRequest carries approvers outside input mode; dropping",
			"topic", msg.Topic, "requestID", req.RequestID, "reason", refusalApproverNotInput)
		msg.Ack()
		return
	}
	if recipientKey(req) == "" && req.Approvers == nil {
		n.logger.Error("notify: NotifyRequest names no recipient; dropping",
			"topic", msg.Topic, "requestID", req.RequestID)
		msg.Ack()
		return
	}
	if req.RecipientPlatformUserID != "" {
		u, err := uuid.Parse(req.RecipientPlatformUserID)
		if err != nil {
			n.logger.Error("notify: NotifyRequest recipient_platform_user_id is not a users.id; dropping",
				"topic", msg.Topic, "requestID", req.RequestID)
			msg.Ack()
			return
		}
		req.RecipientPlatformUserID = u.String()
	}
	if up := msg.UserProperties; up != nil {
		if principal := up[a2a.PropUserID]; principal != "" && req.SenderUserID != "" && principal != req.SenderUserID {
			n.logger.Warn("notify: NotifyRequest sender_user_id does not match broker principal",
				"requestID", req.RequestID, "claimedSender", req.SenderUserID, "brokerPrincipal", principal)
		}
	}

	if req.Mode == a2a.NotifyModeInput {
		if n.pending == nil {
			n.logger.Error("notify: input-mode (HIL) request requires a database; dropping",
				"requestID", req.RequestID, "recipient", recipientKey(req))
			msg.Ack()
			return
		}
		if n.handleInputRequest(ctx, req, msg.Redelivered || !msg.NackRedelivers()) {
			msg.Nack()
			return
		}
		msg.Ack()
		return
	}

	records := n.dispatcher.Deliver(ctx, req)
	if len(records) > 0 {
		sent, failed, skipped := 0, 0, 0
		for _, r := range records {
			switch r.Status {
			case "sent":
				sent++
			case "failed":
				failed++
			case "skipped":
				skipped++
			}
		}
		switch {
		case sent > 0:
		case failed > 0:
			n.logger.Error("notify: no delivery channel succeeded",
				"requestID", req.RequestID, "recipient", recipientKey(req),
				"deliveryCount", len(records), "failedCount", failed, "skippedCount", skipped)
		default:
			n.logger.Debug("notify: every delivery channel was skipped",
				"requestID", req.RequestID, "recipient", recipientKey(req),
				"deliveryCount", len(records), "skippedCount", skipped)
		}
	}
	msg.Ack()
}

type CreateNotifyRequest struct {
	RecipientUserID string         `json:"recipientUserId,omitempty"`
	Mode            string         `json:"mode,omitempty"`
	Channels        []string       `json:"channels,omitempty"`
	Title           string         `json:"title,omitempty"`
	Message         string         `json:"message"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

func (n *NotifyCore) buildHandler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/notify", n.handleSubmit)
	api.HandleFunc("GET /api/v1/notify/inbox", n.handleInboxList)
	api.HandleFunc("POST /api/v1/notify/inbox/read-all", n.handleInboxMarkAllRead)
	api.HandleFunc("DELETE /api/v1/notify/inbox", n.handleInboxClearAll)
	api.HandleFunc("POST /api/v1/notify/inbox/{id}/read", n.handleInboxMarkRead)
	api.HandleFunc("POST /api/v1/notify/inbox/{id}/unread", n.handleInboxMarkUnread)
	api.HandleFunc("DELETE /api/v1/notify/inbox/{id}", n.handleInboxDelete)
	api.HandleFunc("POST /api/v1/notify/inbox/{id}/respond", n.handleInboxRespond)
	api.HandleFunc("GET /api/v1/notify/preferences", n.handlePreferencesList)
	api.HandleFunc("PUT /api/v1/notify/preferences", n.handlePreferencesSet)
	api.HandleFunc("GET /api/v1/notify/preferences/categories", n.handleCategoryPreferencesList)
	api.HandleFunc("PUT /api/v1/notify/preferences/categories", n.handleCategoryPreferencesSet)
	api.HandleFunc("GET /api/v1/notify/preferences/matrix", n.handleMatrixGet)
	api.HandleFunc("PUT /api/v1/notify/preferences/matrix", n.handleMatrixUpdate)
	api.HandleFunc("GET /api/v1/notify/channels", n.handleChannelsList)
	api.HandleFunc("POST /api/v1/notify/channels/{channelType}/confirm", n.handleChannelConfirmStart)
	api.HandleFunc("POST /api/v1/notify/channels/{channelType}/disable", n.handleChannelDisable)
	api.HandleFunc("POST /api/v1/notify/channels/{channelType}/confirm/verify", n.handleChannelConfirmVerify)

	authMiddleware := auth.DevMiddleware()
	if n.ext.AuthMiddleware != nil {
		authMiddleware = n.ext.AuthMiddleware
	}
	inner := http.MaxBytesHandler(api, 1<<20)
	chained := httputil.Chain(
		httputil.RequestIDMiddleware,
		httputil.CORSMiddleware(n.cfg.CORSAllowedOrigins, n.corsRegex),
		authMiddleware,
		auth.RestrictAPIKeys(api, inner, nil),
		n.adoptLoginRows,
	)(inner)

	top := http.NewServeMux()
	top.HandleFunc("GET /api/v1/notify/health", func(w http.ResponseWriter, _ *http.Request) {
		if err := n.Health(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	top.Handle("/", chained)
	return top
}

func (n *NotifyCore) handleSubmit(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok || id == nil || id.UserID() == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", n.logger)
		return
	}
	var body CreateNotifyRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), n.logger)
		return
	}
	if body.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required", n.logger)
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = a2a.NotifyModeNotify
	}
	if mode == a2a.NotifyModeInput {
		writeError(w, http.StatusBadRequest, "mode 'input' (HIL) is not supported yet", n.logger)
		return
	}
	recipient, recipientPlatformID := id.UserID(), id.PlatformUserID()
	if target := canonicalUserKey(body.RecipientUserID); target != "" && !isCaller(id, target) {
		if !id.HasScope(rbac.ScopeNotifySendOthers) {
			writeError(w, http.StatusForbidden, "not permitted to notify another user", n.logger)
			return
		}
		recipient, recipientPlatformID = target, ""
	}

	req := a2a.NotifyRequest{
		RequestID:               idgen.New(),
		RecipientUserID:         recipient,
		RecipientPlatformUserID: recipientPlatformID,
		Mode:                    mode,
		SenderUserID:            id.UserID(),
		Channels:                body.Channels,
		Title:                   body.Title,
		Message:                 body.Message,
		Metadata:                body.Metadata,
	}
	records := n.dispatcher.Deliver(r.Context(), req)
	status := summarizeDeliveries(records)
	code := http.StatusOK
	if status == "failed" {
		code = http.StatusBadGateway
	}
	writeJSON(w, code, map[string]any{"requestId": req.RequestID, "status": status, "deliveries": records})
}

func summarizeDeliveries(records []DeliveryRecord) string {
	if len(records) == 0 {
		return "no_channels_matched"
	}
	sent, failed := 0, 0
	for _, r := range records {
		switch r.Status {
		case "sent":
			sent++
		case "failed":
			failed++
		}
	}
	switch {
	case sent == 0 && failed == 0:
		return "skipped"
	case sent == 0:
		return "failed"
	case failed > 0:
		return "partial"
	default:
		return "delivered"
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var (
	_ runtime.Component = (*NotifyCore)(nil)
	_ leader.JobSource  = (*NotifyCore)(nil)
)
