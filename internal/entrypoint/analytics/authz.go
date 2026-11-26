// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var ErrForbiddenCrossUser = errors.New("analytics: cross-user access requires " + rbac.ScopeActivityRead)

const AuditTargetAll = "*"

func AuditTarget(ownerUserID, callerUserID string) (target string, crossUser bool) {
	if ownerUserID == "" {
		return AuditTargetAll, true
	}
	return ownerUserID, ownerUserID != callerUserID
}

func ResolveOwnerScope(callerUserID, requestedUserID string, isAdmin bool) (string, error) {
	switch {
	case isAdmin:
		return requestedUserID, nil
	case callerUserID == "":
		return "", ErrForbiddenCrossUser
	case requestedUserID == "" || requestedUserID == callerUserID:
		return callerUserID, nil
	default:
		return "", ErrForbiddenCrossUser
	}
}
