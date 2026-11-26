// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type RunAsRequest struct {
	Real                      *Subject
	IsMultiParty              bool
	UseUserIdentityInChannels bool
	RunAs                     string
}

func ResolveRunAsIdentity(req RunAsRequest) (identity *Subject, swapped bool) {
	if !req.IsMultiParty || req.UseUserIdentityInChannels {
		return req.Real, false
	}
	authMethod := ""
	if req.Real != nil {
		authMethod = req.Real.authMethod
	}
	return SystemUserIdentity(req.RunAs, authMethod), true
}

func SystemUserIdentity(runAs, authMethod string) *Subject {
	principal := rbac.SystemUserPrincipal(runAs)
	return &Subject{
		fullIdentity:  true,
		userID:        principal,
		name:          principal,
		authenticated: true,
		authMethod:    authMethod,
	}
}
