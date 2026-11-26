// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type identityLookupRequest struct {
	UserID string
	Key    string
	Value  string

	GatewayID        string
	ContinuityToken  string
	PredecessorToken string
	DiscardBinding   bool
}

func (r identityLookupRequest) fieldAgnostic() bool {
	return r.Key != "" && r.Value != ""
}

func (r identityLookupRequest) offersPrincipal() bool {
	return r.Key == ClaimKeyPhoneNumber && r.PredecessorToken == "" && !r.DiscardBinding
}

type identityResolution struct {
	Context map[string]any
	Outcome identity.LookupResolution
	UserID  string

	Principal string

	RefusalReason string

	RotationApplied bool
}

const whatsappPrincipalPrefix = "whatsapp:"

const (
	refusalSenderChanged       = "sender_changed"
	refusalInactiveUser        = "inactive_user"
	refusalNoClaimKey          = "no_claim_key"
	refusalUnverifiedAttribute = "unverified_attribute"
)

type usersRepoIdentityResolver struct {
	repo     *store.UsersRepo
	claims   *store.UserIdentityClaimsRepo
	bindings *store.EntrypointIdentityBindingsRepo
	changes  authzChangeOutbox
	now      func() int64
	logger   *slog.Logger

	userIDsByEmail func(ctx context.Context, email string) ([]string, error)
}

func newUsersRepoIdentityResolver(repo *store.UsersRepo, logger *slog.Logger) *usersRepoIdentityResolver {
	return &usersRepoIdentityResolver{
		repo:           repo,
		changes:        authzChangeOutbox{db: repo},
		logger:         logger.With("logger_name", "user-identity-resolver"),
		userIDsByEmail: repo.UserIDsByEmail,
	}
}

func (r *usersRepoIdentityResolver) withAuthzOutbox(repo *store.OutboxRepo, notifier platpkg.OutboxNotifier) *usersRepoIdentityResolver {
	r.changes.repo = repo
	r.changes.notifier = notifier
	return r
}

func (r *usersRepoIdentityResolver) withClaimProjection(claims *store.UserIdentityClaimsRepo) *usersRepoIdentityResolver {
	r.claims = claims
	return r
}

func (r *usersRepoIdentityResolver) withContinuity(bindings *store.EntrypointIdentityBindingsRepo) *usersRepoIdentityResolver {
	r.bindings = bindings
	return r
}

func (r *usersRepoIdentityResolver) nowMs() int64 {
	if r.now != nil {
		return r.now()
	}
	return time.Now().UnixMilli()
}

func (r *usersRepoIdentityResolver) Resolve(ctx context.Context, req identityLookupRequest) (identityResolution, error) {
	if rbac.IsSystemPrincipal(req.UserID) {
		return identityResolution{Outcome: identity.LookupUnknown}, nil
	}
	if req.fieldAgnostic() {
		return r.resolveByClaim(ctx, req)
	}
	return r.resolveByPresentedShape(ctx, req.UserID)
}

func (r *usersRepoIdentityResolver) ResolveByID(ctx context.Context, id string) (identityResolution, error) {
	u, _, err := r.repo.GetByID(ctx, id)
	if err != nil {
		return identityResolution{Outcome: identity.LookupDegraded}, fmt.Errorf("load user: %w", err)
	}
	return resolutionFor(u)
}

func (r *usersRepoIdentityResolver) resolveByClaim(ctx context.Context, req identityLookupRequest) (identityResolution, error) {
	if r.claims == nil {
		return identityResolution{Outcome: identity.LookupDegraded}, fmt.Errorf("resolve claim %q: claim projection is not wired", req.Key)
	}
	policy, ok := policyForClaimKey(req.Key)
	if !ok {
		r.logger.Warn("identity lookup refused: the request named no claim key to compare the sender's value against")
		return identityResolution{Outcome: identity.LookupRefused, RefusalReason: refusalNoClaimKey}, nil
	}
	canonical := policy.canonical(req.Value)
	if canonical == "" {
		return identityResolution{Outcome: identity.LookupNoMatch}, nil
	}

	ids, err := r.claims.UserIDsByClaim(ctx, req.Key, canonical)
	if err != nil {
		return identityResolution{Outcome: identity.LookupDegraded}, fmt.Errorf("resolve claim %q: %w", req.Key, err)
	}
	switch {
	case len(ids) == 0:
		return identityResolution{Outcome: identity.LookupNoMatch}, nil
	case len(ids) > 1:
		r.logger.Warn("identity lookup refused: claim value matches multiple users",
			"identityClaimKey", req.Key, "matchCount", len(ids), "userIDs", ids)
		return identityResolution{Outcome: identity.LookupAmbiguous}, nil
	}

	u, _, err := r.repo.GetByID(ctx, ids[0])
	if err != nil {
		return identityResolution{Outcome: identity.LookupDegraded}, fmt.Errorf("resolve claim %q: load matched user: %w", req.Key, err)
	}
	if canonicalID(u) == "" {
		r.logger.Warn("identity lookup refused: matched user is inactive or system-provisioned",
			"identityClaimKey", req.Key, "userId", ids[0])
		return identityResolution{Outcome: identity.LookupRefused, RefusalReason: refusalInactiveUser}, nil
	}

	var principal string
	if req.offersPrincipal() {
		principal, err = r.principalFor(ctx, u)
		if err != nil {
			return identityResolution{Outcome: identity.LookupDegraded}, fmt.Errorf("resolve claim %q: %w", req.Key, err)
		}
	}

	verdict, rotationApplied, rotationErr := r.continuityVerdict(ctx, req, canonical, u.ID)
	if rotationErr != nil {
		return identityResolution{Outcome: identity.LookupDegraded}, rotationErr
	}
	if verdict.Withholds() {
		return identityResolution{
			Outcome:         identity.LookupRefused,
			RefusalReason:   refusalSenderChanged,
			RotationApplied: rotationApplied,
		}, nil
	}
	_, gctx, err := userLookupContext(u)
	return identityResolution{
		Context:         gctx,
		Outcome:         identity.LookupMatched,
		UserID:          u.ID,
		Principal:       principal,
		RotationApplied: rotationApplied,
	}, err
}

func (r *usersRepoIdentityResolver) principalFor(ctx context.Context, u *store.User) (string, error) {
	email := canonicalEmailValue(derefStr(u.Email))
	if !strings.Contains(email, "@") || rbac.IsSystemPrincipal(email) {
		return "", nil
	}
	ids, err := r.userIDsByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("check the matched user's email is unique: %w", err)
	}
	switch {
	case len(ids) == 1 && ids[0] == u.ID:
		return email, nil
	case !slices.Contains(ids, u.ID):
		r.logger.Warn("identity lookup matched a user whose stored email does not resolve back to them; the sender keeps their own principal",
			"userId", u.ID, "matchCount", len(ids), "userIDs", ids)
	default:
		r.logger.Warn("identity lookup matched a user whose email another user also holds; the sender keeps their own principal",
			"userId", u.ID, "matchCount", len(ids), "userIDs", ids)
	}
	return "", nil
}

func (r *usersRepoIdentityResolver) resolveByPresentedShape(ctx context.Context, userID string) (identityResolution, error) {
	if value, ok := strings.CutPrefix(userID, whatsappPrincipalPrefix); ok && r.claims != nil {
		return r.resolveByClaim(ctx, identityLookupRequest{
			UserID: userID,
			Key:    ClaimKeyPhoneNumber,
			Value:  value,
		})
	}
	if strings.Contains(userID, "@") {
		ids, err := r.userIDsByEmail(ctx, canonicalEmailValue(userID))
		if err != nil {
			return identityResolution{Outcome: identity.LookupDegraded}, fmt.Errorf("resolve email: %w", err)
		}
		if len(ids) == 1 {
			u, _, getErr := r.repo.GetByID(ctx, ids[0])
			if getErr != nil {
				return identityResolution{Outcome: identity.LookupDegraded}, fmt.Errorf("resolve email: load matched user: %w", getErr)
			}
			return resolutionFor(u)
		}
		if len(ids) > 1 {
			r.logger.Warn("chat identity refused: email matches multiple users",
				"matchCount", len(ids), "userIDs", ids)
			return identityResolution{Outcome: identity.LookupAmbiguous}, nil
		}
		return identityResolution{Outcome: identity.LookupNoMatch}, nil
	}
	return identityResolution{Outcome: identity.LookupUnknown}, nil
}

func resolutionFor(u *store.User) (identityResolution, error) {
	if canonicalID(u) == "" {
		if u == nil {
			return identityResolution{Outcome: identity.LookupNoMatch}, nil
		}
		return identityResolution{Outcome: identity.LookupRefused, RefusalReason: refusalInactiveUser}, nil
	}
	_, gctx, err := userLookupContext(u)
	return identityResolution{Context: gctx, Outcome: identity.LookupMatched, UserID: u.ID}, err
}

func canonicalID(u *store.User) string {
	if u == nil || u.ProvisionedVia == rbac.ProvisionedViaSystem || !u.Active {
		return ""
	}
	return u.ID
}

func (r *usersRepoIdentityResolver) applyRotation(ctx context.Context, matchedUserID string, rotate func(tx *dbutil.Tx) (bool, error)) (bool, error) {
	var applied bool
	err := r.changes.commit(ctx, authzOutboxUsersChanged, func(tx *dbutil.Tx) (string, error) {
		var err error
		applied, err = rotate(tx)
		if err != nil || !applied {
			return "", err
		}
		return matchedUserID, nil
	})
	return applied, err
}

func (r *usersRepoIdentityResolver) continuityVerdict(ctx context.Context, req identityLookupRequest, canonical, matchedUserID string) (store.ContinuityVerdict, bool, error) {
	if r.bindings == nil || req.GatewayID == "" {
		return store.ContinuityFirstSeen, false, nil
	}
	if req.DiscardBinding {
		applied, err := r.applyRotation(ctx, matchedUserID, func(tx *dbutil.Tx) (bool, error) {
			return r.bindings.DiscardInTx(ctx, tx, req.GatewayID, req.Key, canonical, matchedUserID, r.nowMs())
		})
		if err != nil {
			return store.ContinuityFirstSeen, false, fmt.Errorf("resolve claim %q: discard binding: %w", req.Key, err)
		}
		if applied {
			r.logger.Warn("identity binding tombstoned: the provider reports this value changed hands",
				"identityClaimKey", req.Key, "gatewayID", req.GatewayID)
			return store.ContinuityAwaitingCorroboration, true, nil
		}
		return store.ContinuityFirstSeen, false, nil
	}
	rebound := false
	if req.PredecessorToken != "" {
		applied, err := r.applyRotation(ctx, matchedUserID, func(tx *dbutil.Tx) (bool, error) {
			return r.bindings.RebindInTx(ctx, tx, req.GatewayID, req.Key, canonical,
				store.TokenRotation{Predecessor: req.PredecessorToken, Successor: req.ContinuityToken}, r.nowMs())
		})
		if err != nil {
			return store.ContinuityFirstSeen, false, fmt.Errorf("resolve claim %q: rebind: %w", req.Key, err)
		}
		rebound = applied
	}
	verdict, err := r.bindings.ObserveContinuity(ctx, req.GatewayID, req.Key, canonical, req.ContinuityToken, matchedUserID, r.nowMs())
	if err != nil {
		return store.ContinuityFirstSeen, rebound, fmt.Errorf("resolve claim %q: continuity: %w", req.Key, err)
	}
	if verdict == store.ContinuityReassigned {
		r.logger.Info("identity binding reassigned: the directory now names a different holder for a withheld value",
			"identityClaimKey", req.Key, "gatewayID", req.GatewayID, "userId", matchedUserID)
	}
	if !verdict.Withholds() {
		return verdict, rebound, nil
	}
	r.logger.Warn("identity lookup refused: this value reached the entrypoint from a sender it did not before and is withheld; revoke the holder's identity claims to let them re-enrol if the change was theirs",
		"identityClaimKey", req.Key, "gatewayID", req.GatewayID, "continuityVerdict", verdict.Reason())
	return verdict, rebound, nil
}
