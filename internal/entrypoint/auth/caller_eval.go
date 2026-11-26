// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const AuthMethodEvalSystem = "eval_system"

func ForEvalSystemPrincipal(principal, platformUserID string, res rbac.Resolution) (*Subject, error) {
	if len(res.Scopes) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoEffectiveScopes, principal)
	}
	return &Subject{
		fullIdentity:   true,
		userID:         principal,
		platformUserID: platformUserID,
		roles:          slices.Clone(res.Roles),
		scopes:         slices.Clone(res.Scopes),
		authenticated:  true,
		authMethod:     AuthMethodEvalSystem,
	}, nil
}
