// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type rbacDBSource struct {
	repo *store.RBACRepo
}

func newRBACDBSource(repo *store.RBACRepo) *rbacDBSource {
	return &rbacDBSource{repo: repo}
}

func (s *rbacDBSource) Snapshot(ctx context.Context) (*rbac.DBState, error) {
	snap, err := s.repo.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := &rbac.DBState{}
	out.Roles = make([]rbac.DBRole, 0, len(snap.Roles))
	for _, r := range snap.Roles {
		out.Roles = append(out.Roles, rbac.DBRole{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description,
			Scopes:      snap.RoleScopes[r.ID],
			Parents:     snap.RoleInheritance[r.ID],
		})
	}
	out.UserRoles = make([]rbac.DBUserRole, 0, len(snap.UserRoles))
	for _, ur := range snap.UserRoles {
		out.UserRoles = append(out.UserRoles, rbac.DBUserRole{
			UserID:       ur.UserID,
			RoleID:       ur.RoleID,
			SubjectType:  ur.SubjectType,
			SubjectValue: ur.SubjectValue,
			Issuer:       ur.Issuer,
		})
	}
	out.ClaimMappings = make([]rbac.DBClaimMappingEntity, 0, len(snap.ClaimMappings))
	for _, m := range snap.ClaimMappings {
		out.ClaimMappings = append(out.ClaimMappings, rbac.DBClaimMappingEntity{
			OIDCProvider: m.OIDCProvider,
			ClaimKey:     m.ClaimKey,
			ClaimValue:   m.ClaimValue,
			RoleIDs:      snap.ClaimMappingRoles[m.ID],
		})
	}
	out.DefaultRoleIDs = snap.DefaultRoleIDs
	return out, nil
}
