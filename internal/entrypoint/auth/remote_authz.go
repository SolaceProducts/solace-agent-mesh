// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

type AuthorizationResolution struct {
	Roles            []string
	Scopes           []string
	Profile          *identity.Profile
	EnrichmentStatus identity.EnrichmentStatus
	EnrichmentReason string

	Resolution        identity.LookupResolution
	ResolvedUserID    string
	ResolvedPrincipal string
	RefusalReason     string
	RotationApplied   bool
	RolesSource       identity.RolesSource
	SystemUserID      string
	SystemUserRow     SystemUserRow
}

type AuthorizationClient interface {
	Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (AuthorizationResolution, error)
}

type BrokerAuthorizationClient struct {
	broker       broker.Broker
	namespace    string
	gatewayID    string
	timeout      time.Duration
	maxAttempts  int
	retryBackoff time.Duration
	group        singleflight.Group
	logger       *slog.Logger

	warnedSystemUserRow atomic.Bool
}

type BrokerAuthorizationClientConfig struct {
	Namespace string
	GatewayID string
	Timeout   time.Duration

	MaxAttempts int

	RetryBackoff time.Duration
}

func NewBrokerAuthorizationClient(brk broker.Broker, cfg BrokerAuthorizationClientConfig, logger *slog.Logger) *BrokerAuthorizationClient {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 250 * time.Millisecond
	}
	return &BrokerAuthorizationClient{
		broker:       brk,
		namespace:    cfg.Namespace,
		gatewayID:    cfg.GatewayID,
		timeout:      cfg.Timeout,
		maxAttempts:  cfg.MaxAttempts,
		retryBackoff: cfg.RetryBackoff,
		logger:       logger.With("logger_name", "authz-client"),
	}
}

func (c *BrokerAuthorizationClient) Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (AuthorizationResolution, error) {
	if userID == "" {
		return AuthorizationResolution{}, fmt.Errorf("authz client: user_id is required")
	}

	key := singleflightKey(userID, gatewayContext)

	v, err, _ := c.group.Do(key, func() (any, error) {
		return c.lookup(context.WithoutCancel(ctx), userID, gatewayContext)
	})
	if err != nil {
		return AuthorizationResolution{}, err
	}
	return v.(AuthorizationResolution), nil
}

const marshalFailureSentinel = "!marshal-err!"

func singleflightKey(userID string, gatewayContext map[string]any) string {
	if len(gatewayContext) == 0 {
		return userID
	}
	keys := make([]string, 0, len(gatewayContext))
	for k := range gatewayContext {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write([]byte{0})
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		if b, err := json.Marshal(gatewayContext[k]); err == nil {
			h.Write(b)
		} else {
			h.Write([]byte(marshalFailureSentinel))
		}
		h.Write([]byte{0})
	}
	return userID + "|" + hex.EncodeToString(h.Sum(nil))
}

func (c *BrokerAuthorizationClient) lookup(ctx context.Context, userID string, gatewayContext map[string]any) (AuthorizationResolution, error) {
	payload, err := json.Marshal(map[string]any{
		"user_id":         userID,
		"gateway_context": ensureGatewayID(gatewayContext, c.gatewayID),
	})
	if err != nil {
		return AuthorizationResolution{}, fmt.Errorf("authz client: marshal request: %w", err)
	}

	var lastErr error
	for attempt := range c.maxAttempts {
		if attempt > 0 {
			if err := sleepCtx(ctx, c.retryBackoff*time.Duration(attempt)); err != nil {
				return AuthorizationResolution{}, fmt.Errorf("authz client: lookup canceled: %w", err)
			}
			c.logger.Debug("retrying authz lookup", "userFP", strutil.PIIFingerprint(userID), "attempt", attempt+1, "maxAttempts", c.maxAttempts)
		}
		res, retryable, err := c.lookupOnce(ctx, payload)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !retryable {
			return AuthorizationResolution{}, err
		}
	}
	c.logger.Warn("authz lookup exhausted retries", "userFP", strutil.PIIFingerprint(userID), "attempts", c.maxAttempts, "error", lastErr)
	return AuthorizationResolution{}, lastErr
}

func (c *BrokerAuthorizationClient) lookupOnce(ctx context.Context, payload []byte) (AuthorizationResolution, bool, error) {
	correlationID := idgen.New()
	msg := &broker.Message{
		Payload:       payload,
		ReplyTo:       a2a.AuthzResponseTopic(c.namespace, c.gatewayID) + "/" + correlationID,
		CorrelationID: correlationID,
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	reply, err := c.broker.Request(reqCtx, a2a.AuthzLookupTopic(c.namespace, c.gatewayID), msg, c.timeout)
	if err != nil {
		c.logger.Warn("authz lookup attempt failed", "error", err)
		return AuthorizationResolution{}, true, fmt.Errorf("authz client: lookup: %w: %w", ErrIdentityPlatformUnreachable, err)
	}

	resp, err := control.UnmarshalResponse(reply.Payload)
	if err != nil {
		return AuthorizationResolution{}, false, fmt.Errorf("authz client: unmarshal reply: %w", err)
	}
	if resp.Error != nil {
		denied := fmt.Errorf("authz lookup denied: %s (code=%d)", resp.Error.Message, resp.Error.Code)
		if resp.Error.Code == control.ErrOperationFailed {
			denied = fmt.Errorf("%w: %w", ErrIdentityPlatformUnreachable, denied)
		}
		return AuthorizationResolution{}, false, denied
	}

	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		return AuthorizationResolution{}, false, fmt.Errorf("authz client: unexpected result shape %T", resp.Result)
	}

	out := AuthorizationResolution{
		Roles:  stringSlice(resMap["roles"]),
		Scopes: stringSlice(resMap["scopes"]),
	}
	if status, ok := resMap["enrichment_status"].(string); ok {
		out.EnrichmentStatus = identity.EnrichmentStatus(status)
	}
	if resolution, ok := resMap["resolution"].(string); ok {
		parsed := identity.LookupResolution(resolution)
		if !parsed.Recognized() {
			c.logger.Warn("authz lookup returned a resolution this build does not recognize; holding the sender rather than admitting them",
				"resolutionLen", len(resolution))
			parsed = identity.LookupDegraded
		}
		out.Resolution = parsed
	}
	if resolvedUserID, ok := resMap["resolved_user_id"].(string); ok {
		out.ResolvedUserID = resolvedUserID
	}
	if resolvedPrincipal, ok := resMap["resolved_principal"].(string); ok {
		out.ResolvedPrincipal = resolvedPrincipal
	}
	if refusalReason, ok := resMap["refusal_reason"].(string); ok {
		out.RefusalReason = refusalReason
	}
	if rolesSource, ok := resMap["roles_source"].(string); ok {
		out.RolesSource = identity.RolesSource(rolesSource)
	}
	c.readSystemUserRow(resMap, &out)
	if rotationApplied, ok := resMap["rotation_applied"].(bool); ok {
		out.RotationApplied = rotationApplied
	}
	if reason, ok := resMap["enrichment_reason"].(string); ok {
		out.EnrichmentReason = reason
	}
	if profileRaw, ok := resMap["profile"]; ok && profileRaw != nil {
		profileBytes, marshalErr := json.Marshal(profileRaw)
		if marshalErr != nil {
			return AuthorizationResolution{}, false, fmt.Errorf("authz client: re-marshal profile: %w", marshalErr)
		}
		var p identity.Profile
		if err := json.Unmarshal(profileBytes, &p); err != nil {
			c.logger.Warn("authz lookup: profile decode failed; treating as unavailable", "error", err)
			out.Profile = nil
			out.EnrichmentStatus = identity.EnrichmentUnavailable
			out.EnrichmentReason = "profile decode failed"
		} else {
			out.Profile = &p
		}
	}
	return out, false, nil
}

func (c *BrokerAuthorizationClient) readSystemUserRow(resMap map[string]any, out *AuthorizationResolution) {
	if raw, present := resMap["system_user_id"]; present {
		if id, ok := raw.(string); ok {
			out.SystemUserID = id
		} else {
			c.logger.Warn("authz lookup returned a system_user_id that is not a string; ignoring it", "valueType", fmt.Sprintf("%T", raw))
		}
	}
	raw, present := resMap["system_user_row"]
	if !present {
		return
	}
	state, isString := raw.(string)
	if parsed := SystemUserRow(state); isString && parsed.Recognized() {
		out.SystemUserRow = parsed
		return
	}
	c.warnOnceUnrecognizedSystemUserRow(raw)
	out.SystemUserRow = SystemUserRowUnavailable
}

func (c *BrokerAuthorizationClient) warnOnceUnrecognizedSystemUserRow(raw any) {
	level := slog.LevelDebug
	if c.warnedSystemUserRow.CompareAndSwap(false, true) {
		level = slog.LevelWarn
	}
	c.logger.Log(context.Background(), level, "authz lookup returned a system_user_row this build does not recognize; treating the row as unavailable",
		"valueType", fmt.Sprintf("%T", raw))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

const perRequestLookupAttempts = 1

func BuildBrokerLazyEnrichmentLookup(brk broker.Broker, namespace, gatewayID string, logger *slog.Logger) LazyEnrichmentLookup {
	if brk == nil || gatewayID == "" {
		return nil
	}
	client := NewBrokerAuthorizationClient(brk, BrokerAuthorizationClientConfig{
		Namespace:   namespace,
		GatewayID:   gatewayID,
		Timeout:     2 * time.Second,
		MaxAttempts: perRequestLookupAttempts,
	}, logger)
	return func(ctx context.Context, id *Subject) (CachedProfile, error) {
		gctx := map[string]any{
			"gateway_id": gatewayID,
			"idp_claims": map[string]any{"email": id.email},
		}
		res, err := client.Lookup(ctx, id.userID, gctx)
		if err != nil {
			return CachedProfile{}, err
		}
		return CachedProfile{
			Profile:          res.Profile,
			EnrichmentStatus: res.EnrichmentStatus,
			EnrichmentReason: res.EnrichmentReason,
		}, nil
	}
}

func ensureGatewayID(ctx map[string]any, gatewayID string) map[string]any {
	if gatewayID == "" {
		return ctx
	}
	if ctx == nil {
		return map[string]any{"gateway_id": gatewayID}
	}
	if _, ok := ctx["gateway_id"]; ok {
		return ctx
	}
	out := make(map[string]any, len(ctx)+1)
	maps.Copy(out, ctx)
	out["gateway_id"] = gatewayID
	return out
}

func stringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
