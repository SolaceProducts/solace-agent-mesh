// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const AuthMethodScheduledTask = "scheduled_task"

const maxScheduledRunAsLen = 255

type SystemUserRow string

const (
	SystemUserRowFound       SystemUserRow = "found"
	SystemUserRowMissing     SystemUserRow = "missing"
	SystemUserRowUnavailable SystemUserRow = "unavailable"
)

var (
	ErrInvalidScheduledRunAs       = errors.New("runAs is not a service account a scheduled task can run as")
	ErrUnknownSystemUser           = errors.New("resolve caller: no such service account")
	ErrIdentityLookupUnavailable   = errors.New("resolve caller: the identity lookup is unavailable")
	ErrIdentityPlatformUnreachable = fmt.Errorf("%w: the platform could not be reached or failed the lookup", ErrIdentityLookupUnavailable)
	ErrSystemUserLookupUnavailable = fmt.Errorf("%w: the service account's row could not be looked up", ErrIdentityLookupUnavailable)
	ErrSystemUserRowNotReported    = errors.New("resolve caller: the platform does not report service account rows")
	ErrOwnerDeactivated            = errors.New("resolve caller: owner's account is deactivated")
	ErrOwnerUnresolved             = errors.New("resolve caller: owner does not resolve to an active user")
)

const refusalInactiveUser = "inactive_user"

func (r SystemUserRow) Recognized() bool {
	switch r {
	case SystemUserRowFound, SystemUserRowMissing, SystemUserRowUnavailable:
		return true
	}
	return false
}

func CheckScheduledRunAs(principal string) error {
	name, prefixed := strings.CutPrefix(principal, rbac.SystemSubPrefix)
	switch {
	case !prefixed:
		return fmt.Errorf("%w: it must name a service account as %s<name>", ErrInvalidScheduledRunAs, rbac.SystemSubPrefix)
	case name == "" || strings.ContainsFunc(name, unicode.IsSpace):
		return fmt.Errorf("%w: the service account name must be one word with no whitespace", ErrInvalidScheduledRunAs)
	case len(principal) > maxScheduledRunAsLen:
		return fmt.Errorf("%w: it must be at most %d characters", ErrInvalidScheduledRunAs, maxScheduledRunAsLen)
	}
	if builtIn, ok := rbac.LookupReservedSystemUser(principal); ok && builtIn.HiddenFromRunAsPicker {
		return fmt.Errorf("%w: %q is a built-in service account that nothing may run as", ErrInvalidScheduledRunAs, principal)
	}
	return nil
}

func classifyLookupFailure(err error) error {
	switch {
	case errors.Is(err, ErrIdentityLookupUnavailable):
		return err
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrIdentityLookupUnavailable, err)
	}
	return err
}

func confirmedLookup(ent Entitlement) error {
	if ent.Resolution == identity.LookupDegraded || !ent.Resolution.Recognized() {
		return fmt.Errorf("%w: the platform answered with a partial entitlement (resolution %q)", ErrIdentityLookupUnavailable, ent.Resolution)
	}
	return nil
}

func IsScheduledRunAsRefusal(err error) bool {
	return errors.Is(err, ErrInvalidScheduledRunAs) || errors.Is(err, ErrUnknownSystemUser) ||
		errors.Is(err, ErrNoEffectiveScopes) || errors.Is(err, ErrNoEntitlementLookup)
}

func IsScheduledOwnerRefusal(err error) bool {
	return errors.Is(err, ErrInvalidExternalID) || errors.Is(err, ErrNoEffectiveScopes) || errors.Is(err, ErrNoEntitlementLookup) ||
		errors.Is(err, ErrOwnerDeactivated) || errors.Is(err, ErrOwnerUnresolved)
}

type OwnerRefusal struct {
	Cause         error
	Resolution    identity.LookupResolution
	RefusalReason string
	RolesSource   identity.RolesSource
}

func (e *OwnerRefusal) Error() string {
	return fmt.Sprintf("%v (resolution=%q refusal_reason=%q roles_source=%q)", e.Cause, e.Resolution, e.RefusalReason, e.RolesSource)
}

func (e *OwnerRefusal) Unwrap() error { return e.Cause }

func refusedOwner(cause error, ent Entitlement) error {
	return &OwnerRefusal{Cause: cause, Resolution: ent.Resolution, RefusalReason: ent.RefusalReason, RolesSource: ent.RolesSource}
}

func admitScheduledOwner(ent Entitlement) error {
	switch ent.Resolution {
	case identity.LookupMatched, identity.LookupUnknown:
		return nil
	case identity.LookupNoMatch:
		if ent.RolesSource == identity.RolesSourceAssigned {
			return nil
		}
	case identity.LookupRefused:
		if ent.RefusalReason == refusalInactiveUser {
			return refusedOwner(ErrOwnerDeactivated, ent)
		}
	}
	return refusedOwner(ErrOwnerUnresolved, ent)
}

func ForScheduledRunAs(ctx context.Context, principal string, lookups EntitlementLookup) (*Subject, error) {
	if err := CheckScheduledRunAs(principal); err != nil {
		return nil, err
	}
	if lookups == nil {
		return nil, ErrNoEntitlementLookup
	}
	ent, err := lookups.Lookup(ctx, principal, nil)
	if err != nil {
		return nil, classifyLookupFailure(err)
	}
	if partial := confirmedLookup(ent); partial != nil {
		return nil, partial
	}
	rowID, err := systemUserRowID(principal, ent)
	if err != nil {
		return nil, err
	}
	if len(ent.Scopes) == 0 {
		return nil, refusedPrincipal(ErrNoEffectiveScopes, principal)
	}
	return &Subject{
		fullIdentity:   true,
		userID:         principal,
		name:           principal,
		roles:          slices.Clone(ent.Roles),
		scopes:         slices.Clone(ent.Scopes),
		authenticated:  true,
		authMethod:     AuthMethodScheduledTask,
		platformUserID: rowID,
	}, nil
}

func systemUserRowID(principal string, ent Entitlement) (string, error) {
	switch rowID := cmp.Or(ent.SystemUserID, rbac.ReservedSystemUserID(principal)); {
	case rowID != "":
		return rowID, nil
	case ent.SystemUserRow == SystemUserRowMissing:
		return "", refusedPrincipal(ErrUnknownSystemUser, principal)
	case ent.SystemUserRow == "":
		return "", refusedPrincipal(ErrSystemUserRowNotReported, principal)
	default:
		return "", refusedPrincipal(ErrSystemUserLookupUnavailable, principal)
	}
}

func refusedPrincipal(reason error, principal string) error {
	return fmt.Errorf("%w: %q", reason, principal)
}

func ForScheduledTaskOwner(ctx context.Context, userID string, lookups EntitlementLookup) (*Subject, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: empty userID", ErrInvalidExternalID)
	}
	if lookups == nil {
		return nil, ErrNoEntitlementLookup
	}
	ent, err := lookups.Lookup(ctx, userID, nil)
	if err != nil {
		return nil, classifyLookupFailure(err)
	}
	if partial := confirmedLookup(ent); partial != nil {
		return nil, partial
	}
	if err := admitScheduledOwner(ent); err != nil {
		return nil, err
	}
	if len(ent.Scopes) == 0 {
		return nil, ErrNoEffectiveScopes
	}
	return &Subject{
		fullIdentity:     true,
		userID:           userID,
		roles:            slices.Clone(ent.Roles),
		scopes:           slices.Clone(ent.Scopes),
		authenticated:    true,
		authMethod:       AuthMethodScheduledTask,
		profile:          ent.Profile,
		enrichmentStatus: ent.EnrichmentStatus,
		enrichmentReason: ent.EnrichmentReason,
		platformUserID:   ent.PlatformUserID,
	}, nil
}
