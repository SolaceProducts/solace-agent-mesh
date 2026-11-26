// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"fmt"
	"slices"
)

type DevScopeHook func(userID string, roles []string) ([]string, bool)

type DevHeaders struct {
	UserID string
	Email  string
	Scopes []string
}

func ForNoopDevCaller(effectiveID string, hdr DevHeaders, hook DevScopeHook) *Subject {
	id := DevIdentity()
	if effectiveID != "" && effectiveID != id.userID {
		id.userID = effectiveID
		id.email = fmt.Sprintf("%s@dev.local", effectiveID)
	}
	if hdr.UserID != "" {
		id.userID = hdr.UserID
		id.email = fmt.Sprintf("%s@dev.local", hdr.UserID)
		if hdr.Email != "" {
			id.email = hdr.Email
		}
	}
	if hook != nil {
		if scopes, ok := hook(id.userID, id.roles); ok {
			id.scopes = slices.Clone(scopes)
		}
	}
	if len(hdr.Scopes) > 0 {
		id.scopes = slices.Clone(hdr.Scopes)
	}
	return id
}

func ForDevMiddlewareCaller(scopeOverride []string) *Subject {
	id := DevIdentity()
	if len(scopeOverride) > 0 {
		id.scopes = slices.Clone(scopeOverride)
	}
	return id
}
