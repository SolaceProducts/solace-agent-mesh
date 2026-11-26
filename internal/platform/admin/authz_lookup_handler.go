// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const defaultAuthzLookupWorkers = 8

const defaultAuthzEnrichmentTimeout = 4 * time.Second

const defaultUserResolveTimeout = 2 * time.Second

const defaultDegradedResolveTTL = 5 * time.Second

const enrichmentAllowanceSlack = 500 * time.Millisecond

const authzLookupDependencyBudget = 4 * time.Second

const (
	defaultEnrichmentBreakerThreshold = 5
	defaultEnrichmentBreakerCooldown  = 30 * time.Second
)

type userIdentityResolver interface {
	Resolve(ctx context.Context, req identityLookupRequest) (identityResolution, error)
}

const (
	systemUserRowFound       = "found"
	systemUserRowMissing     = "missing"
	systemUserRowUnavailable = "unavailable"
)

type systemUserRows interface {
	SystemUserID(ctx context.Context, issuer, sub string) (string, error)
}

type AuthzLookupHandler struct {
	composer           *rbac.Composer
	identity           identity.IdentityService
	userResolver       userIdentityResolver
	systemUsers        systemUserRows
	broker             broker.Broker
	namespace          string
	queue              broker.Queue
	cancel             context.CancelFunc
	workerWG           sync.WaitGroup
	workers            int
	cache              *runtime.MapCache
	cacheTTL           time.Duration
	degradedResolveTTL time.Duration
	userResolveTimeout time.Duration
	enrichmentTimeout  time.Duration
	dependencyBudget   time.Duration

	cacheMu  sync.Mutex
	cacheGen uint64

	gatewayLabels     sync.Map
	gatewayLabelCount atomic.Int64

	unconfiguredWarned sync.Map

	enrichBreakerThreshold int64
	enrichBreakerCooldown  time.Duration
	enrichFailures         atomic.Int64
	breakerOpenUntil       atomic.Int64

	metrics runtime.Metrics
	logger  *slog.Logger
}

const (
	metricAuthzResolveFailures     = "authz_lookup_resolve_failures_total"
	metricAuthzEnrichmentUncounted = "authz_lookup_enrichment_breaker_skipped_total"

	resolveFailureDBError   = "db_error"
	resolveFailureBadClaims = "claims_unparseable"
)

const metricAuthzResolutions = "authz_lookup_resolutions_total"

func negativeResolution(outcome identity.LookupResolution) bool {
	switch outcome {
	case identity.LookupNoMatch, identity.LookupAmbiguous, identity.LookupRefused, identity.LookupNotConfigured:
		return true
	default:
		return false
	}
}

func (h *AuthzLookupHandler) logUnconfiguredResolver(gatewayID string) {
	if _, loaded := h.unconfiguredWarned.LoadOrStore(gatewayID, struct{}{}); loaded {
		return
	}
	h.logger.Error("identity lookup cannot be answered: an entrypoint asked which user holds a claim and no per-user identity store is wired on this platform",
		"gatewayID", gatewayID)
}

func (h *AuthzLookupHandler) countResolution(outcome identity.LookupResolution, gatewayID string) {
	label := string(outcome)
	if outcome == identity.LookupUnknown {
		label = "unknown"
	}
	h.count(metricAuthzResolutions, "resolution", label, "gatewayId", h.boundedGatewayLabel(gatewayID))
}

const maxGatewayMetricLabels = 64

func (h *AuthzLookupHandler) boundedGatewayLabel(gatewayID string) string {
	if gatewayID == "" {
		return ""
	}
	if _, seen := h.gatewayLabels.Load(gatewayID); seen {
		return gatewayID
	}
	if h.gatewayLabelCount.Load() >= maxGatewayMetricLabels {
		return "other"
	}
	if _, loaded := h.gatewayLabels.LoadOrStore(gatewayID, struct{}{}); !loaded {
		h.gatewayLabelCount.Add(1)
	}
	return gatewayID
}

type AuthzLookupConfig struct {
	Namespace                  string
	CacheTTL                   time.Duration
	Workers                    int
	EnrichmentTimeout          time.Duration
	EnrichmentBreakerThreshold int
	EnrichmentBreakerCooldown  time.Duration
	DependencyBudget           time.Duration
	DegradedResolveTTL         time.Duration
	UserResolveTimeout         time.Duration
}

func NewAuthzLookupHandler(composer *rbac.Composer, identitySvc identity.IdentityService, brk broker.Broker, cfg AuthzLookupConfig, logger *slog.Logger) *AuthzLookupHandler {
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Minute
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultAuthzLookupWorkers
	}
	if cfg.EnrichmentTimeout <= 0 {
		cfg.EnrichmentTimeout = defaultAuthzEnrichmentTimeout
	}
	if cfg.EnrichmentBreakerThreshold == 0 {
		cfg.EnrichmentBreakerThreshold = defaultEnrichmentBreakerThreshold
	}
	if cfg.EnrichmentBreakerCooldown <= 0 {
		cfg.EnrichmentBreakerCooldown = defaultEnrichmentBreakerCooldown
	}
	if cfg.DependencyBudget <= 0 {
		cfg.DependencyBudget = authzLookupDependencyBudget
	}
	if cfg.DegradedResolveTTL <= 0 {
		cfg.DegradedResolveTTL = defaultDegradedResolveTTL
	}
	if cfg.UserResolveTimeout <= 0 {
		cfg.UserResolveTimeout = defaultUserResolveTimeout
	}
	logger = logger.With("logger_name", "authz-lookup")
	if cfg.DependencyBudget < cfg.EnrichmentTimeout-enrichmentAllowanceSlack {
		logger.Warn("authz lookup dependency budget is below the enrichment timeout; the enrichment circuit breaker will never trip",
			"dependencyBudget", cfg.DependencyBudget, "enrichmentTimeout", cfg.EnrichmentTimeout)
	}
	return &AuthzLookupHandler{
		composer:               composer,
		identity:               identitySvc,
		broker:                 brk,
		namespace:              cfg.Namespace,
		workers:                cfg.Workers,
		cache:                  runtime.NewMapCache(),
		cacheTTL:               cfg.CacheTTL,
		degradedResolveTTL:     cfg.DegradedResolveTTL,
		userResolveTimeout:     cfg.UserResolveTimeout,
		enrichmentTimeout:      cfg.EnrichmentTimeout,
		dependencyBudget:       cfg.DependencyBudget,
		enrichBreakerThreshold: int64(cfg.EnrichmentBreakerThreshold),
		enrichBreakerCooldown:  cfg.EnrichmentBreakerCooldown,
		logger:                 logger,
	}
}

func (h *AuthzLookupHandler) Start(ctx context.Context) error {
	ctx, h.cancel = context.WithCancel(ctx)

	topic := a2a.AuthzLookupSubscription(h.namespace)
	queueName := a2a.QueueName(h.namespace, "platform", "authz_lookup")
	queue, err := h.broker.CreateQueue(queueName, broker.QueueOptions{
		Named:         true,
		MaxRedelivery: 1,
	})
	if err != nil {
		return fmt.Errorf("create authz lookup queue: %w", err)
	}
	h.queue = queue
	if err := h.broker.Subscribe(queue, topic); err != nil {
		return fmt.Errorf("subscribe authz lookup: %w", err)
	}

	for range h.workers {
		h.workerWG.Go(func() {
			h.receiveLoop(ctx)
		})
	}
	h.logger.Info("authz lookup handler started", "topic", topic, "cacheTTL", h.cacheTTL, "workers", h.workers)
	return nil
}

func (h *AuthzLookupHandler) Stop(_ context.Context) error {
	if h.cancel != nil {
		h.cancel()
	}
	h.workerWG.Wait()
	return nil
}

func (h *AuthzLookupHandler) InvalidateAll() {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	h.cacheGen++
	h.cache.Clear()
}

func (h *AuthzLookupHandler) cacheGeneration() uint64 {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	return h.cacheGen
}

func (h *AuthzLookupHandler) storeIfCurrent(key string, value any, ttl time.Duration, gen uint64) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.cacheGen == gen {
		h.cache.Set(key, value, runtime.WithTTL(ttl))
	}
}

const ownerScopesCacheKeyPrefix = "ownerscopes|"

func (h *AuthzLookupHandler) ResolveOwnerScopes(sub, userID string) (rbac.Resolution, error) {
	if h == nil || h.composer == nil {
		return rbac.Resolution{}, fmt.Errorf("authz lookup: composer unavailable")
	}
	if sub == "" || userID == "" {
		return rbac.Resolution{}, fmt.Errorf("authz lookup: owner sub and user id are both required")
	}
	gatewayContext := map[string]any{"sam_user_id": userID}
	cacheKey := ownerScopesCacheKeyPrefix + cacheKeyFor(sub, "", gatewayContext)
	if cached, ok := h.cache.Get(cacheKey); ok {
		if resolution, ok := cached.(rbac.Resolution); ok {
			return resolution, nil
		}
	}
	gen := h.cacheGeneration()
	resolution, err := h.composer.Lookup(sub, "", gatewayContext)
	if err != nil {
		return rbac.Resolution{}, fmt.Errorf("authz lookup: resolve owner scopes: %w", err)
	}
	h.storeIfCurrent(cacheKey, resolution, h.cacheTTL, gen)
	return resolution, nil
}

func (h *AuthzLookupHandler) SetUserIdentityResolver(r userIdentityResolver) {
	h.userResolver = r
}

func (h *AuthzLookupHandler) SetSystemUserRows(r systemUserRows) {
	h.systemUsers = r
}

func (h *AuthzLookupHandler) SetMetrics(m runtime.Metrics) {
	h.metrics = m
}

func (h *AuthzLookupHandler) count(name string, labels ...string) {
	if h.metrics != nil {
		h.metrics.Counter(name, 1, labels...)
	}
}

func (h *AuthzLookupHandler) resolveCanonicalUser(ctx context.Context, userID, gatewayID string, gatewayContext map[string]any) canonicalUserResolution {
	if h.userResolver == nil {
		outcome := identity.LookupUnknown
		if identityLookupRequestFrom(userID, gatewayID, gatewayContext).fieldAgnostic() {
			outcome = identity.LookupNotConfigured
			h.logUnconfiguredResolver(gatewayID)
		}
		return canonicalUserResolution{Context: gatewayContext, Outcome: outcome}
	}
	if existing, ok := gatewayContext["sam_user_id"].(string); ok && existing != "" {
		return canonicalUserResolution{Context: gatewayContext, Outcome: identity.LookupMatched}
	}
	if emailAttributeUnverified(gatewayContext) {
		h.logger.Warn("identity attribute refused: the IdP reports this email as unverified", "gatewayID", gatewayID)
		return canonicalUserResolution{Context: gatewayContext, Outcome: identity.LookupRefused, RefusalReason: refusalUnverifiedAttribute}
	}
	resolveCtx, cancel := context.WithTimeout(ctx, h.userResolveTimeout)
	defer cancel()
	res, err := h.userResolver.Resolve(resolveCtx, identityLookupRequestFrom(userID, gatewayID, gatewayContext))
	if res.Context == nil {
		if err != nil {
			h.logger.Warn("canonical user resolution failed", "gatewayID", gatewayID, "error", err)
			h.count(metricAuthzResolveFailures, "reason", resolveFailureDBError)
			return canonicalUserResolution{Context: gatewayContext, Outcome: identity.LookupDegraded, Degraded: true}
		}
		return canonicalUserResolution{
			Context:         gatewayContext,
			Outcome:         res.Outcome,
			RefusalReason:   res.RefusalReason,
			RotationApplied: res.RotationApplied,
		}
	}
	resolvedContext := res.Context
	if err != nil {
		samUserID, _ := resolvedContext["sam_user_id"].(string)
		h.logger.Warn("stored claims unparseable; claim-derived roles absent for this user",
			"userId", samUserID, "oidcProvider", resolvedContext["oidc_provider"], "gatewayID", gatewayID)
		h.count(metricAuthzResolveFailures, "reason", resolveFailureBadClaims)
	}
	return canonicalUserResolution{
		Context:         mergeLookupContext(gatewayContext, resolvedContext),
		Outcome:         res.Outcome,
		Principal:       res.Principal,
		RefusalReason:   res.RefusalReason,
		RotationApplied: res.RotationApplied,
	}
}

type canonicalUserResolution struct {
	Context         map[string]any
	Outcome         identity.LookupResolution
	Principal       string
	RefusalReason   string
	RotationApplied bool
	Degraded        bool
}

func identityLookupRequestFrom(userID, gatewayID string, gatewayContext map[string]any) identityLookupRequest {
	req := identityLookupRequest{UserID: userID, GatewayID: gatewayID}
	if key, ok := gatewayContext[gatewayContextIdentityClaimKey].(string); ok {
		req.Key = key
	}
	if value, ok := gatewayContext[gatewayContextIdentityClaimValue].(string); ok {
		req.Value = value
	}
	if token, ok := gatewayContext[gatewayContextContinuityToken].(string); ok {
		req.ContinuityToken = token
	}
	if predecessor, ok := gatewayContext[gatewayContextContinuityPredecessor].(string); ok {
		req.PredecessorToken = predecessor
	}
	if discard, ok := gatewayContext[gatewayContextContinuityDiscard].(bool); ok {
		req.DiscardBinding = discard
	}
	return req
}

func mergeLookupContext(base, resolved map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(resolved))
	maps.Copy(merged, base)
	liveClaims, ok := base["idp_claims"].(map[string]any)
	hasLiveClaims := ok && len(liveClaims) > 0
	for k, v := range resolved {
		if hasLiveClaims && (k == "idp_claims" || k == "oidc_provider") {
			continue
		}
		merged[k] = v
	}
	return merged
}

type lookupRequest struct {
	UserID         string         `json:"user_id"`
	GatewayContext map[string]any `json:"gateway_context"`
}

type lookupResponse struct {
	Roles            []string          `json:"roles"`
	Scopes           []string          `json:"scopes"`
	Profile          *identity.Profile `json:"profile,omitempty"`
	EnrichmentStatus string            `json:"enrichment_status,omitempty"`
	EnrichmentReason string            `json:"enrichment_reason,omitempty"`

	Resolution        string `json:"resolution,omitempty"`
	ResolvedUserID    string `json:"resolved_user_id,omitempty"`
	ResolvedPrincipal string `json:"resolved_principal,omitempty"`

	SystemUserID  string `json:"system_user_id,omitempty"`
	SystemUserRow string `json:"system_user_row,omitempty"`

	RefusalReason string `json:"refusal_reason,omitempty"`

	RotationApplied bool `json:"rotation_applied,omitempty"`

	RolesSource string `json:"roles_source,omitempty"`
}

const (
	gatewayContextIdentityClaimKey      = "identity_claim_key"
	gatewayContextIdentityClaimValue    = "identity_claim_value"
	gatewayContextContinuityToken       = "continuity_token"
	gatewayContextContinuityPredecessor = "continuity_predecessor"
	gatewayContextContinuityDiscard     = "continuity_discard"
	gatewayContextRolesChanged          = "roles_changed"
)

func (h *AuthzLookupHandler) receiveLoop(ctx context.Context) {
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
				h.logger.Info("authz lookup receive loop exiting: queue no longer exists", "queue", h.queue.Name())
				return
			}
			h.logger.Debug("authz lookup receive error", "error", err, "backoff", bo.Current())
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

func (h *AuthzLookupHandler) handle(ctx context.Context, msg *broker.Message) {
	defer msg.Ack()

	replyTo := msg.ReplyTo
	if replyTo == "" {
		up := a2a.DecodeUserProperties(msg.UserProperties)
		replyTo = up.ReplyTo
	}
	if replyTo == "" {
		h.logger.Warn("authz lookup request missing reply_to, dropping")
		return
	}
	corrID := msg.CorrelationID

	var req lookupRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrInvalidRequest, "invalid lookup payload"))
		return
	}
	if req.UserID == "" {
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrInvalidRequest, "user_id is required"))
		return
	}

	var gatewayID string
	if raw, ok := req.GatewayContext["gateway_id"]; ok && raw != nil {
		s, isString := raw.(string)
		if !isString {
			h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrInvalidRequest, "gateway_id must be a string"))
			return
		}
		gatewayID = s
	}
	topicGatewayID := a2a.AuthzLookupGatewayID(msg.Topic)
	if topicGatewayID != "" && gatewayID != "" && topicGatewayID != gatewayID {
		h.logger.Warn("authz lookup refused: the request body names a different entrypoint than the topic it was published on",
			"topicGatewayID", topicGatewayID, "bodyGatewayID", gatewayID)
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrInvalidRequest,
			"gateway_id does not match the topic it was published on"))
		return
	}
	if topicGatewayID != "" {
		gatewayID = topicGatewayID
	}
	cacheKey := cacheKeyFor(req.UserID, gatewayID, req.GatewayContext)
	if cached, ok := h.cache.Get(cacheKey); ok {
		if resp, ok := cached.(lookupResponse); ok {
			h.countResolution(identity.LookupResolution(resp.Resolution), gatewayID)
			h.reply(ctx, replyTo, corrID, control.SuccessResponse(resp))
			return
		}
	}

	gen := h.cacheGeneration()

	depCtx, cancelDeps := context.WithTimeout(ctx, h.dependencyBudget)
	defer cancelDeps()

	rolesChanged, _ := req.GatewayContext[gatewayContextRolesChanged].(bool)
	if rolesChanged {
		if err := h.composer.Rebuild(depCtx); err != nil {
			h.logger.Warn("authz lookup: rebuild after a login's grant failed; resolving against the current graph",
				append(systemUserAttr(req.UserID), "gatewayID", gatewayID, "error", err)...)
		}
	}
	userRes := h.resolveCanonicalUser(depCtx, req.UserID, gatewayID, req.GatewayContext)
	gatewayContext, lookupOutcome := userRes.Context, userRes.Outcome
	resolution, err := h.composer.Lookup(cmp.Or(userRes.Principal, req.UserID), gatewayID, gatewayContext)
	if err != nil {
		h.logger.Error("composer lookup failed",
			append(systemUserAttr(req.UserID), "gatewayID", gatewayID, "error", err)...)
		h.reply(ctx, replyTo, corrID, control.ErrorResponse(control.ErrOperationFailed, "lookup failed"))
		return
	}

	resp := lookupResponse{
		Roles:           resolution.Roles,
		Scopes:          resolution.Scopes,
		Resolution:      string(lookupOutcome),
		RefusalReason:   userRes.RefusalReason,
		RotationApplied: userRes.RotationApplied,
		RolesSource:     string(rolesSourceOf(resolution)),
	}
	if lookupOutcome == identity.LookupMatched {
		if samUserID, ok := gatewayContext["sam_user_id"].(string); ok {
			resp.ResolvedUserID = samUserID
		}
		resp.ResolvedPrincipal = userRes.Principal
	}
	systemUserDegraded := h.addSystemUserRow(depCtx, &resp, req.UserID, gatewayID)
	h.countResolution(lookupOutcome, gatewayID)

	switch {
	case h.identity == nil:
		resp.EnrichmentStatus = string(identity.EnrichmentNotConfigured)
	case emailAttributeUnverified(req.GatewayContext):
		resp.EnrichmentStatus = string(identity.EnrichmentUnavailable)
		resp.EnrichmentReason = "identity attribute unverified"
	case h.enrichmentBreakerOpen():
		resp.EnrichmentStatus = string(identity.EnrichmentUnavailable)
		resp.EnrichmentReason = "enrichment temporarily suppressed after repeated failures"
	default:
		claims := claimsForIdentityResolve(gatewayContext)
		allowance := h.enrichmentTimeout
		if deadline, ok := depCtx.Deadline(); ok {
			allowance = min(allowance, time.Until(deadline))
		}
		budgetStarved := allowance < h.enrichmentTimeout-enrichmentAllowanceSlack

		resolveCtx, cancel := context.WithTimeout(depCtx, h.enrichmentTimeout)
		idRes, idErr := h.identity.Resolve(resolveCtx, claims)
		cancel()
		switch {
		case idErr != nil:
			h.logger.Warn("identity resolve failed; returning unavailable",
				append(systemUserAttr(req.UserID), "gatewayID", gatewayID, "error", idErr, "budgetStarved", budgetStarved)...)
			resp.EnrichmentStatus = string(identity.EnrichmentUnavailable)
			resp.EnrichmentReason = "identity service error"
			h.recordEnrichmentFailure(budgetStarved, gatewayID)
		case idRes == nil:
			resp.EnrichmentStatus = string(identity.EnrichmentNotConfigured)
		default:
			resp.Profile = idRes.Profile
			resp.EnrichmentStatus = string(idRes.EnrichmentStatus)
			resp.EnrichmentReason = idRes.EnrichmentReason
			switch idRes.EnrichmentStatus {
			case identity.EnrichmentOK:
				h.recordEnrichmentResult(true)
			case identity.EnrichmentUnavailable:
				h.recordEnrichmentFailure(budgetStarved, gatewayID)
			}
		}
	}

	if userRes.RotationApplied {
		h.InvalidateAll()
		h.reply(ctx, replyTo, corrID, control.SuccessResponse(resp))
		return
	}
	ttl := h.cacheTTL
	if userRes.Degraded || systemUserDegraded || negativeResolution(lookupOutcome) {
		ttl = h.degradedResolveTTL
	}
	if !rolesChanged {
		h.storeIfCurrent(cacheKey, resp, ttl, gen)
	}
	h.reply(ctx, replyTo, corrID, control.SuccessResponse(resp))
}

func rolesSourceOf(res rbac.Resolution) identity.RolesSource {
	if res.DefaultRoles {
		return identity.RolesSourceDefaults
	}
	return identity.RolesSourceAssigned
}

func (h *AuthzLookupHandler) addSystemUserRow(ctx context.Context, resp *lookupResponse, userID, gatewayID string) (degraded bool) {
	if h.systemUsers == nil || !rbac.IsSystemPrincipal(userID) {
		return false
	}
	id, err := h.systemUsers.SystemUserID(ctx, rbac.SystemUserIssuer, userID)
	if err != nil {
		h.logger.Warn("authz lookup: service account row lookup failed; replying that its existence is unknown",
			"systemUser", userID, "gatewayID", gatewayID, "error", err)
		resp.SystemUserRow = systemUserRowUnavailable
		return true
	}
	resp.SystemUserID = id
	resp.SystemUserRow = systemUserRowMissing
	if id != "" {
		resp.SystemUserRow = systemUserRowFound
	}
	return false
}

func (h *AuthzLookupHandler) enrichmentBreakerOpen() bool {
	until := h.breakerOpenUntil.Load()
	return until != 0 && time.Now().UnixNano() < until
}

func (h *AuthzLookupHandler) recordEnrichmentResult(ok bool) {
	if h.enrichBreakerThreshold < 0 {
		return
	}
	if ok {
		if h.enrichFailures.Swap(0) != 0 {
			h.breakerOpenUntil.Store(0)
			h.logger.Info("enrichment circuit breaker closed; provider recovered")
		}
		return
	}
	n := h.enrichFailures.Add(1)
	if n < h.enrichBreakerThreshold {
		return
	}
	h.breakerOpenUntil.Store(time.Now().Add(h.enrichBreakerCooldown).UnixNano())
	if n == h.enrichBreakerThreshold {
		h.logger.Warn("enrichment circuit breaker opened; suppressing profile enrichment",
			"consecutiveFailures", h.enrichBreakerThreshold, "cooldown", h.enrichBreakerCooldown)
	}
}

func (h *AuthzLookupHandler) recordEnrichmentFailure(budgetStarved bool, gatewayID string) {
	if budgetStarved {
		h.count(metricAuthzEnrichmentUncounted)
		h.logger.Debug("enrichment failure not counted toward the breaker; the dependency budget cut its allowance short",
			"gatewayID", gatewayID)
		return
	}
	h.recordEnrichmentResult(false)
}

func claimsForIdentityResolve(gatewayContext map[string]any) map[string]any {
	if gatewayContext == nil {
		return nil
	}
	if raw, ok := gatewayContext["idp_claims"]; ok {
		if m, isMap := raw.(map[string]any); isMap {
			return m
		}
	}
	return gatewayContext
}

func emailAttributeUnverified(gatewayContext map[string]any) bool {
	claims, ok := gatewayContext["idp_claims"].(map[string]any)
	if !ok {
		return false
	}
	raw, present := claims["email_verified"]
	if !present {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return !v
	case string:
		return !strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return true
	}
}

func systemUserAttr(userID string) []any {
	if rbac.IsSystemPrincipal(userID) {
		return []any{"systemUser", userID}
	}
	return nil
}

func cacheKeyFor(userID, gatewayID string, gatewayContext map[string]any) string {
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write([]byte{0})
	h.Write([]byte(gatewayID))
	h.Write([]byte{0})
	if len(gatewayContext) > 0 {
		keys := make([]string, 0, len(gatewayContext))
		for k := range gatewayContext {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			h.Write([]byte(k))
			h.Write([]byte{0})
			b, err := json.Marshal(gatewayContext[k])
			if err != nil {
				h.Write([]byte("!marshal-err!"))
			} else {
				h.Write(b)
			}
			h.Write([]byte{0})
		}
	}
	return userID + "|" + gatewayID + "|" + hex.EncodeToString(h.Sum(nil))
}

func (h *AuthzLookupHandler) reply(ctx context.Context, topic, correlationID string, resp *control.Response) {
	data, err := control.MarshalResponse(resp)
	if err != nil {
		h.logger.Error("marshal authz lookup response failed", "error", err)
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := &broker.Message{Payload: data, CorrelationID: correlationID}
	if pubErr := h.broker.Publish(pubCtx, topic, out); pubErr != nil {
		h.logger.Error("publish authz lookup response failed", "topic", topic, "error", pubErr)
	}
}
