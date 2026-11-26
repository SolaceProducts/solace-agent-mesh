// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const defaultUserUpsertWorkers = 4

type UserUpsertHandler struct {
	repo               *store.UsersRepo
	rbacRepo           *store.RBACRepo
	budgetAssignments  *store.BudgetAssignmentsRepo
	budgetsChanged     *BudgetsChangedSignal
	composer           *rbac.Composer
	authzLookupHandler *AuthzLookupHandler
	changes            authzChangeOutbox
	metrics            runtime.Metrics
	broker             broker.Broker
	namespace          string
	queue              broker.Queue
	cancel             context.CancelFunc
	workerWG           sync.WaitGroup
	workers            int
	logger             *slog.Logger
}

type UserUpsertConfig struct {
	Namespace string
	Workers   int
}

func NewUserUpsertHandler(repo *store.UsersRepo, brk broker.Broker, cfg UserUpsertConfig, logger *slog.Logger) *UserUpsertHandler {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultUserUpsertWorkers
	}
	return &UserUpsertHandler{
		repo:      repo,
		changes:   authzChangeOutbox{db: repo},
		broker:    brk,
		namespace: cfg.Namespace,
		workers:   cfg.Workers,
		logger:    logger.With("logger_name", "user-upsert"),
	}
}

func (h *UserUpsertHandler) WithAuthzOutbox(repo *store.OutboxRepo, notifier platpkg.OutboxNotifier) {
	h.changes.repo = repo
	h.changes.notifier = notifier
}

func (h *UserUpsertHandler) SetRBACRepo(repo *store.RBACRepo) { h.rbacRepo = repo }

func (h *UserUpsertHandler) SetBudgetBinder(repo *store.BudgetAssignmentsRepo) {
	h.budgetAssignments = repo
	h.budgetsChanged = NewBudgetsChangedSignal(h.broker, h.namespace, h.logger)
}

func (h *UserUpsertHandler) bindPendingBudget(ctx context.Context, user *store.User, actor string) {
	if h.budgetAssignments == nil {
		return
	}
	result, err := h.budgetAssignments.BindPendingForUser(ctx, user.ID, actor, time.Now().UnixMilli())
	if err != nil {
		h.logger.Warn("user upsert: bind pending budget assignment failed", "userId", user.ID, "error", err)
		return
	}
	if p := result.Bound; p != nil {
		h.budgetsChanged.send(ctx)
		logBudgetAssignmentAudit(h.logger, auditActionMaterialized, &store.BudgetAssignment{
			ID: result.AssignmentID, State: store.BudgetAssignmentConcrete, SubjectType: p.SubjectType,
			SubjectRef: user.ID, IdentityType: p.IdentityType, BudgetID: p.BudgetID,
		}, user.ID, "pendingId", p.ID)
	}
	for i := range result.Superseded {
		superseded := pendingAsAssignment(&result.Superseded[i])
		superseded.SubjectRef = user.ID
		logBudgetAssignmentAudit(h.logger, auditActionSuperseded, superseded, user.ID)
	}
}

func (h *UserUpsertHandler) SetGrantRefresh(composer *rbac.Composer, handler *AuthzLookupHandler, metrics runtime.Metrics) {
	h.composer = composer
	h.authzLookupHandler = handler
	h.metrics = metrics
}

func identityAttributesChanged(before, after *store.User, scimOwned bool, mappedClaimsChanged func() (bool, error)) bool {
	if before == nil {
		return true
	}
	if scimOwned {
		return false
	}
	if !equalOptStr(before.Email, after.Email) || !equalOptStr(before.Phone, after.Phone) ||
		before.OIDCProvider != after.OIDCProvider {
		return true
	}
	changed, err := mappedClaimsChanged()
	return err != nil || changed
}

func (h *UserUpsertHandler) lookupCacheStale(before, after *store.User, scimOwned bool) bool {
	return identityAttributesChanged(before, after, scimOwned, func() (bool, error) {
		changed, err := h.composer.MappedClaimsChanged(before.Source, after.Source)
		if err != nil {
			h.logger.Warn("cannot compare mapped claims; flushing the authz lookup cache conservatively",
				"userId", after.ID, "oidcProvider", after.OIDCProvider, "error", err)
			return changed, err
		}
		return changed || projectionChanged(before.Source, after.Source), nil
	})
}

func equalOptStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (h *UserUpsertHandler) refreshAfterUserWrite(ctx context.Context, user *store.User, actor string, attributesChanged bool) bool {
	if h.authzLookupHandler != nil && attributesChanged {
		h.authzLookupHandler.InvalidateAll()
	}
	if h.rbacRepo == nil {
		return false
	}
	var granted []string
	err := h.changes.commit(ctx, authzOutboxRolesChanged, func(tx *dbutil.Tx) (string, error) {
		var err error
		granted, err = h.rbacRepo.MaterializePendingForUserInTx(ctx, tx, user, actor)
		if err != nil || len(granted) == 0 {
			return "", err
		}
		return user.ID, nil
	})
	if err != nil {
		h.logger.Warn("user upsert: materialize pending grants failed", "issuer", user.Issuer, "sub", user.Sub, "error", err)
		return false
	}
	for _, roleID := range granted {
		logRBACAudit(h.logger, auditActionMaterialized, auditResourceGrant, user.ID+"|"+roleID, actor)
	}
	if len(granted) == 0 {
		return h.grantMaterializedByEarlierAttempt(ctx, user, actor)
	}
	refreshRBACState(ctx, h.composer, h.authzLookupHandler, h.metrics, h.logger)
	return true
}

const earlierAttemptWindow = time.Minute

func (h *UserUpsertHandler) grantMaterializedByEarlierAttempt(ctx context.Context, user *store.User, actor string) bool {
	recent, err := h.rbacRepo.HasGrantMaterializedSince(ctx, user.ID, actor, time.Now().Add(-earlierAttemptWindow))
	if err != nil {
		h.logger.Warn("user upsert: check for a grant an earlier attempt materialized failed", "issuer", user.Issuer, "sub", user.Sub, "error", err)
		return false
	}
	return recent
}

func upsertReply(userID string, rolesChanged bool) *control.Response {
	result := map[string]any{"id": userID}
	if rolesChanged {
		result["roles_changed"] = true
	}
	return control.SuccessResponse(result)
}

func (h *UserUpsertHandler) Start(ctx context.Context) error {
	ctx, h.cancel = context.WithCancel(ctx)

	topic := a2a.UserUpsertSubscription(h.namespace)
	queueName := a2a.QueueName(h.namespace, "platform", "user_upsert")
	queue, err := h.broker.CreateQueue(queueName, broker.QueueOptions{Named: true})
	if err != nil {
		return fmt.Errorf("create user upsert queue: %w", err)
	}
	h.queue = queue
	if err := h.broker.Subscribe(queue, topic); err != nil {
		return fmt.Errorf("subscribe user upsert: %w", err)
	}

	for range h.workers {
		h.workerWG.Go(func() {
			h.receiveLoop(ctx)
		})
	}
	h.logger.Info("user upsert handler started", "topic", topic, "workers", h.workers)
	return nil
}

func (h *UserUpsertHandler) Stop(_ context.Context) error {
	if h.cancel != nil {
		h.cancel()
	}
	h.workerWG.Wait()
	return nil
}

type userUpsertRequest struct {
	Issuer       string   `json:"issuer"`
	Sub          string   `json:"sub"`
	OIDCProvider string   `json:"oidc_provider"`
	DisplayName  string   `json:"display_name"`
	UserName     string   `json:"user_name"`
	Email        string   `json:"email"`
	Phone        string   `json:"phone"`
	Groups       []string `json:"groups"`
	Source       string   `json:"source"`
	GatewayID    string   `json:"gateway_id"`
}

func (h *UserUpsertHandler) receiveLoop(ctx context.Context) {
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := h.broker.Receive(ctx, h.queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				h.logger.Info("user upsert receive loop exiting: queue no longer exists", "queue", h.queue.Name())
				return
			}
			h.logger.Warn("user upsert receive error", "error", err, "backoff", bo.Current())
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			continue
		}
		h.handle(ctx, msg)
	}
}

func (h *UserUpsertHandler) logUnverifiedClaims(user *store.User, notes []unverifiedClaimNote) {
	if user == nil || user.ID == "" {
		return
	}
	for _, n := range notes {
		fields := []any{
			"userId", user.ID,
			"identityClaimKey", n.ClaimKey,
			"verificationClaim", n.VerificationClaim,
			"reason", n.Reason,
			"valueCount", n.Values,
		}
		if n.Reason == reasonValueUnreadable {
			h.logger.Info("identity claim not projected: its value is in a shape the projection cannot read, so an entrypoint matching this claim will refuse this user with no_match",
				fields...)
			continue
		}
		h.logger.Warn("identity claim stored but not usable for authorization, so an entrypoint matching this claim will refuse this user with no_match",
			fields...)
	}
}

func (h *UserUpsertHandler) handle(ctx context.Context, msg *broker.Message) {
	defer msg.Ack()

	replyTo := msg.ReplyTo
	if replyTo == "" {
		up := a2a.DecodeUserProperties(msg.UserProperties)
		replyTo = up.ReplyTo
	}
	if replyTo == "" {
		h.logger.Warn("user upsert request missing reply_to, dropping")
		return
	}
	corrID := msg.CorrelationID

	var req userUpsertRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		h.logger.Warn("user upsert: invalid payload", "error", err, "correlationID", corrID)
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrInvalidRequest, "invalid upsert payload"))
		return
	}
	if req.Issuer == "" || req.Sub == "" {
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrInvalidRequest, "issuer and sub are required"))
		return
	}
	if rbac.IsSystemPrincipal(req.Sub) {
		h.logger.Warn("user upsert: rejecting reserved system: subject", "issuer", req.Issuer, "correlationID", corrID)
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrInvalidRequest, "sub must not use the reserved system: prefix"))
		return
	}

	actor := req.Email
	if actor == "" {
		actor = req.Sub
	}
	user := &store.User{
		Issuer:       req.Issuer,
		Sub:          req.Sub,
		OIDCProvider: req.OIDCProvider,
		DisplayName:  optStr(req.DisplayName),
		UserName:     optStr(req.UserName),
		Email:        optStr(req.Email),
		Phone:        optStr(req.Phone),
		Source:       req.Source,
		CreatedBy:    actor,
		UpdatedBy:    actor,
	}

	projected, unverified := projectIdentityClaims(req.Source)
	claims := store.IdentityClaimWrite{Replace: true, Claims: projected}
	logClaimNotesOnlyWhenProjectionWritten := func(scimOwned bool) {
		if scimOwned {
			return
		}
		h.logUnverifiedClaims(user, unverified)
	}

	corroborateBindingsAfterCommitBestEffort := func() {
		if !claims.Replace {
			return
		}
		if err := h.repo.CorroborateBindings(ctx, user.ID, claims.Claims); err != nil {
			h.logger.Warn("entrypoint binding corroboration failed; the user is provisioned and will resolve on the claim match, and the next login retries the binding",
				"userId", user.ID, "error", err)
		}
	}

	scimOwned, attributesChanged, err := h.upsertUser(ctx, user, req.Groups, claims)
	if err != nil {
		if errors.Is(err, store.ErrUserPhoneConflict) {
			h.logger.Warn("user upsert: phone already in use, persisting without phone", "issuer", req.Issuer, "sub", req.Sub)
			user.Phone = nil
			scimOwned, attributesChanged, err = h.upsertUser(ctx, user, req.Groups, claims)
			if err != nil {
				h.logger.Error("user upsert failed after dropping phone", "issuer", req.Issuer, "sub", req.Sub, "error", err)
				h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrOperationFailed, "upsert failed"))
				return
			}
			logClaimNotesOnlyWhenProjectionWritten(scimOwned)
			corroborateBindingsAfterCommitBestEffort()
			h.bindPendingBudget(ctx, user, actor)
			rolesChanged := h.refreshAfterUserWrite(ctx, user, actor, attributesChanged)
			h.reply(ctx, replyTo, corrID, upsertReply(user.ID, rolesChanged))
			return
		}
		h.logger.Error("user upsert failed", "issuer", req.Issuer, "sub", req.Sub, "error", err)
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrOperationFailed, "upsert failed"))
		return
	}

	logClaimNotesOnlyWhenProjectionWritten(scimOwned)
	corroborateBindingsAfterCommitBestEffort()
	h.bindPendingBudget(ctx, user, actor)
	rolesChanged := h.refreshAfterUserWrite(ctx, user, actor, attributesChanged)
	h.reply(ctx, replyTo, corrID, upsertReply(user.ID, rolesChanged))
}

func (h *UserUpsertHandler) upsertUser(ctx context.Context, user *store.User, groups []string, claims store.IdentityClaimWrite) (scimOwned, attributesChanged bool, err error) {
	err = h.changes.commit(ctx, authzOutboxUsersChanged, func(tx *dbutil.Tx) (string, error) {
		before, readErr := h.repo.GetByIssuerSubInTx(ctx, tx, user.Issuer, user.Sub)
		if readErr != nil {
			return "", readErr
		}
		scimOwned = before != nil && before.ProvisionedVia == "scim" && user.ProvisionedVia != "scim"
		if writeErr := h.repo.UpsertUserWithClaimsInTx(ctx, tx, user, groups, claims); writeErr != nil {
			return "", writeErr
		}
		attributesChanged = h.lookupCacheStale(before, user, scimOwned)
		if !attributesChanged {
			return "", nil
		}
		return user.ID, nil
	})
	return scimOwned, attributesChanged, err
}

func (h *UserUpsertHandler) reply(ctx context.Context, topic, correlationID string, resp *control.Response) {
	data, err := control.MarshalResponse(resp)
	if err != nil {
		h.logger.Error("marshal user upsert response failed", "error", err)
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := &broker.Message{Payload: data, CorrelationID: correlationID}
	if pubErr := h.broker.Publish(pubCtx, topic, out); pubErr != nil {
		h.logger.Error("publish user upsert response failed", "topic", topic, "error", pubErr)
	}
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
