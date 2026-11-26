// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "encoding/json"

type UserRoleGrantsResponse struct {
	RoleIDs []string `json:"roleIds" cfgapply:"xref=rbacRole"`
}

type SetUserRoleGrantsRequest struct {
	RoleIDs []string `json:"roleIds" cfgapply:"xref=rbacRole"`
}

type UserRoleAssignment struct {
	RoleID     string `json:"roleId"`
	Source     string `json:"source"`
	Manageable bool   `json:"manageable"`
}

type UserResponse struct {
	ID             string               `json:"id"`
	DisplayName    string               `json:"displayName"`
	UserName       string               `json:"userName"`
	Email          string               `json:"email"`
	Issuer         string               `json:"issuer"`
	Sub            string               `json:"sub"`
	OIDCProvider   string               `json:"oidcProvider"`
	ProvisionedVia string               `json:"provisionedVia"`
	Active         bool                 `json:"active"`
	Groups         []string             `json:"groups"`
	Roles          []UserRoleAssignment `json:"roles"`
	LastLoginAt    Timestamp            `json:"lastLoginAt"`
	CreatedBy      string               `json:"createdBy"`
	UpdatedBy      string               `json:"updatedBy"`
	CreatedAt      Timestamp            `json:"createdAt"`
	UpdatedAt      Timestamp            `json:"updatedAt"`

	Budget                expanded[*BudgetSubjectBudget] `json:"budget,omitzero"`
	BudgetTopUp           expanded[*BudgetSubjectTopUp]  `json:"budgetTopUp,omitzero"`
	EffectiveBudgetAmount expanded[*string]              `json:"effectiveBudgetAmount,omitzero"`
}

type expanded[T any] struct {
	set   bool
	value T
}

func expandedTo[T any](value T) expanded[T] { return expanded[T]{set: true, value: value} }

func (e expanded[T]) IsZero() bool { return !e.set }

func (e expanded[T]) Value() T { return e.value }

func (e expanded[T]) MarshalJSON() ([]byte, error) { return json.Marshal(e.value) }

type ComponentAccessUser struct {
	ID           string `json:"id"`
	DisplayName  string `json:"displayName"`
	UserName     string `json:"userName"`
	Email        string `json:"email"`
	IsSystemUser bool   `json:"isSystemUser"`
}

type UserIdentityClaimResponse struct {
	ClaimKey            string    `json:"claimKey"`
	ClaimValueCanonical string    `json:"claimValueCanonical"`
	Verified            bool      `json:"verified"`
	UpdatedAt           Timestamp `json:"updatedAt"`
}

type RevokeIdentityClaimsResponse struct {
	Revoked       int `json:"revoked"`
	BindingsReset int `json:"bindingsReset"`
}
