// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func SpendUser(userProps a2a.UserProperties, verified *auth.Identity) (id, sub string) {
	if verified != nil {
		return VerifiedSpendUser(verified, userProps.UserID)
	}
	return StampedRowOr(userProps.UserUUID, userProps.UserID)
}

func SpendPrincipal(userProps a2a.UserProperties, verified *auth.Identity) string {
	if verified != nil {
		return cmp.Or(verified.UserID, userProps.UserID)
	}
	return userProps.UserID
}

func VerifiedSpendUser(verified *auth.Identity, claimedUserID string) (id, sub string) {
	return StampedRowOr(verified.PlatformUserID, cmp.Or(verified.UserID, claimedUserID))
}

func StampedRowOr(stamped, name string) (id, fallback string) {
	if IsCanonicalID(stamped) {
		return stamped, ""
	}
	return "", name
}
