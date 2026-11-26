// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func NewIdentityTokenSigner(signer *trust.Signer) identity.TokenSigner {
	if signer == nil {
		return nil
	}
	return &identityTokenSigner{signer: signer}
}

type identityTokenSigner struct {
	signer *trust.Signer
}

func (s *identityTokenSigner) SignServiceToken(corrID string) (string, error) {
	return s.signer.Sign(trust.SignClaims{
		Subject: identity.STREnricherAgentName,
		TaskID:  corrID,
		Scopes:  rbac.PlatformIdentityScopes,
	})
}

var ErrEvalPrincipalUnauthorized = errors.New("eval run_as principal is not authorized")

const evalLookupGatewayID = ""

const metricEvalPrincipalRefusals = "eval_principal_resolution_refusals_total"

type evalPrincipalResolver interface {
	Lookup(userID, gatewayID string, gatewayContext map[string]any) (rbac.Resolution, error)
	Rebuild(ctx context.Context) error
}

type evalSignerMetrics interface {
	Counter(name string, delta int64, labels ...string)
}

func NewEvalRunnerSigner(signer *trust.Signer, logger *slog.Logger) eval.TaskAuthSigner {
	if signer == nil {
		return nil
	}
	return &evalRunnerSigner{
		signer: gatewayTaskSignerFromTrust(signer),
		logger: logger,
	}
}

const evalRebuildTimeout = 5 * time.Second

type evalRunnerSigner struct {
	signer  entrypoint.TaskSigner
	logger  *slog.Logger
	metrics evalSignerMetrics

	mu       sync.Mutex
	resolver evalPrincipalResolver
}

func (s *evalRunnerSigner) SetPrincipalResolver(resolver evalPrincipalResolver, metrics evalSignerMetrics) {
	s.mu.Lock()
	s.resolver = resolver
	s.metrics = metrics
	s.mu.Unlock()
}

func (s *evalRunnerSigner) refusalCounter(reason string) {
	s.mu.Lock()
	metrics := s.metrics
	s.mu.Unlock()
	if metrics != nil {
		metrics.Counter(metricEvalPrincipalRefusals, 1, "reason", reason)
	}
}

func (s *evalRunnerSigner) SignTask(ctx context.Context, principal eval.TaskPrincipal, taskID string) (string, error) {
	id, err := s.identityFor(ctx, principal, refreshOnMiss)
	if err != nil {
		return "", err
	}
	return s.signer.SignTask(id, taskID)
}

func (s *evalRunnerSigner) SignTeardown(ctx context.Context, principal, taskID string) (string, error) {
	id, err := s.identityFor(ctx, eval.TaskPrincipal{Subject: principal}, noRefreshOnMiss)
	if err != nil {
		return "", err
	}
	return s.signer.SignTask(id, taskID)
}

const (
	refreshOnMiss   = true
	noRefreshOnMiss = false
)

func (s *evalRunnerSigner) identityFor(ctx context.Context, taskPrincipal eval.TaskPrincipal, refresh bool) (*auth.Subject, error) {
	principal := taskPrincipal.Subject
	s.mu.Lock()
	resolver := s.resolver
	s.mu.Unlock()

	if resolver == nil {
		s.refusalCounter("resolver_unbound")
		s.logger.Warn("eval task signing refused: no authorization state is bound to the eval signer",
			"userID", principal)
		return nil, fmt.Errorf("%w: %q: authorization state is unavailable on this instance", ErrEvalPrincipalUnauthorized, principal)
	}
	res, err := resolver.Lookup(principal, evalLookupGatewayID, nil)
	if err != nil {
		s.refusalCounter("lookup_failed")
		s.logger.Warn("eval task signing refused: authorization lookup failed",
			"userID", principal, "error", err)
		return nil, fmt.Errorf("%w: %q: authorization lookup failed: %v", ErrEvalPrincipalUnauthorized, principal, err)
	}
	if len(res.Scopes) == 0 && refresh {
		if res, err = s.refreshAndLookup(ctx, resolver, principal); err != nil {
			return nil, err
		}
	}
	id, err := auth.ForEvalSystemPrincipal(principal, cmp.Or(taskPrincipal.UserUUID, rbac.ReservedSystemUserID(principal)), res)
	if err != nil {
		s.refusalCounter("no_scopes")
		s.logger.Warn("eval task signing refused: principal holds no RBAC scopes",
			"userID", principal,
			"hint", "grant the service account a role via declarative config (a serviceAccount file naming the role) or authorization_service user_to_role; if the grant already exists, this replica's authorization snapshot could not be refreshed")
		return nil, fmt.Errorf("%w: %q holds no RBAC scopes; grant the service account a role and re-run", ErrEvalPrincipalUnauthorized, principal)
	}
	return id, nil
}

func (s *evalRunnerSigner) refreshAndLookup(ctx context.Context, resolver evalPrincipalResolver, principal string) (rbac.Resolution, error) {
	res, err := rebuildAndLookup(ctx, resolver, principal)
	if errors.Is(err, errEvalSnapshotRefresh) {
		s.logger.Warn("eval task signing: authorization snapshot refresh failed; resolving against the stale snapshot",
			"userID", principal, "error", err)
		return rbac.Resolution{}, nil
	}
	if err != nil {
		s.refusalCounter("lookup_failed")
		s.logger.Warn("eval task signing refused: authorization lookup failed after snapshot refresh",
			"userID", principal, "error", err)
		return rbac.Resolution{}, fmt.Errorf("%w: %q: authorization lookup failed: %v", ErrEvalPrincipalUnauthorized, principal, err)
	}
	return res, nil
}

var errEvalSnapshotRefresh = errors.New("authorization snapshot refresh failed")

func rebuildAndLookup(ctx context.Context, resolver evalPrincipalResolver, principal string) (rbac.Resolution, error) {
	rebuildCtx, cancel := context.WithTimeout(ctx, evalRebuildTimeout)
	defer cancel()
	if err := resolver.Rebuild(rebuildCtx); err != nil {
		return rbac.Resolution{}, fmt.Errorf("%w: %w", errEvalSnapshotRefresh, err)
	}
	return resolver.Lookup(principal, evalLookupGatewayID, nil)
}

func gatewayTaskSignerFromTrust(s *trust.Signer) entrypoint.TaskSigner {
	return &trustTaskSigner{signer: s}
}

type trustTaskSigner struct {
	signer *trust.Signer
}

func (t *trustTaskSigner) SignTask(identity *auth.Subject, taskID string) (string, error) {
	return t.signer.Sign(identity.TaskSignClaims(taskID))
}
