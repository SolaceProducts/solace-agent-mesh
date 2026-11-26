// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"cmp"
	"strings"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func userKey(platformUserID, loginID string) string {
	return cmp.Or(strings.ToLower(platformUserID), loginID)
}

func recipientKey(req a2a.NotifyRequest) string {
	return userKey(req.RecipientPlatformUserID, req.RecipientUserID)
}

func callerKey(id *auth.Subject) string {
	return userKey(id.PlatformUserID(), id.UserID())
}

func canonicalUserKey(key string) string {
	if u, err := uuid.Parse(key); err == nil {
		return u.String()
	}
	return key
}

func isCaller(id *auth.Subject, target string) bool {
	target = canonicalUserKey(target)
	return target == callerKey(id) || target == canonicalUserKey(id.UserID())
}

func destinationKeys(req a2a.NotifyRequest) []string {
	key := recipientKey(req)
	if req.RecipientUserID == "" || req.RecipientUserID == key {
		return []string{key}
	}
	return []string{key, req.RecipientUserID}
}
