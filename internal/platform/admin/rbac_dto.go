// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

const (
	RBACOriginCustom  = "custom"
	RBACOriginYAML    = "yaml"
	RBACOriginBuiltin = "builtin"
)

type RBACRoleResponse struct {
	ID           string    `json:"id" cfgapply:"read-only"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	Scopes       []string  `json:"scopes"`
	Inherits     []string  `json:"inherits,omitempty" cfgapply:"xref=rbacRole"`
	CreatedBy    string    `json:"createdBy,omitempty" cfgapply:"read-only"`
	UpdatedBy    string    `json:"updatedBy,omitempty" cfgapply:"read-only"`
	CreatedAt    Timestamp `json:"createdAt,omitempty" cfgapply:"read-only"`
	UpdatedAt    Timestamp `json:"updatedAt,omitempty" cfgapply:"read-only"`
	Origin       string    `json:"origin" cfgapply:"read-only"`
	NameConflict bool      `json:"nameConflict" cfgapply:"read-only"`
}

type ScopeCatalogEntry struct {
	Scope        string `json:"scope"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ResourceName string `json:"resourceName,omitempty"`
}

type CreateRBACRoleRequest struct {
	Name        string   `json:"name" validate:"required,safename"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	Inherits    []string `json:"inherits" cfgapply:"xref=rbacRole"`
}

type UpdateRBACRoleRequest struct {
	Name        string   `json:"name" validate:"required,safename"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	Inherits    []string `json:"inherits" cfgapply:"xref=rbacRole"`
}

const (
	GrantStateConcrete = store.GrantStateConcrete
	GrantStatePending  = store.GrantStatePending
)

type GrantSubject struct {
	Type   string `json:"type" validate:"required,oneof=email sub legacy"`
	Value  string `json:"value" validate:"required"`
	Issuer string `json:"issuer,omitempty"`
}

type CreateGrantRequest struct {
	Subject GrantSubject `json:"subject" validate:"required"`
	RoleID  string       `json:"roleId" validate:"required" cfgapply:"xref=rbacRole"`
}

type UpdateGrantRequest struct {
	Subject GrantSubject `json:"subject" validate:"required"`
}

type GrantResponse struct {
	ID      string        `json:"id" cfgapply:"read-only"`
	Subject *GrantSubject `json:"subject,omitempty"`
	UserID  string        `json:"userId,omitempty" cfgapply:"read-only"`
	RoleID  string        `json:"roleId" cfgapply:"xref=rbacRole"`
	State   string        `json:"state" cfgapply:"read-only"`
	Origin  string        `json:"origin" cfgapply:"read-only"`
	User    *GrantUser    `json:"user,omitempty" cfgapply:"read-only"`
}

type GrantUser struct {
	Email       *string `json:"email"`
	EmailShared bool    `json:"emailShared"`
	Sub         string  `json:"sub"`
	Issuer      string  `json:"issuer"`
}

type CreateSystemUserRequest struct {
	Name        string   `json:"name" validate:"required,max=255"`
	DisplayName string   `json:"displayName,omitempty"`
	RoleIDs     []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type SystemUserResponse struct {
	ID          string   `json:"id" cfgapply:"read-only"`
	Sub         string   `json:"sub" cfgapply:"read-only"`
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName,omitempty"`
	RoleIDs     []string `json:"roleIds" cfgapply:"xref=rbacRole"`
	Origin      string   `json:"origin" cfgapply:"read-only"`
}

type UpdateSystemUserRequest struct {
	DisplayName string   `json:"displayName,omitempty"`
	RoleIDs     []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type RBACClaimMappingResponse struct {
	ID           string    `json:"id" cfgapply:"read-only"`
	Name         string    `json:"name"`
	OIDCProvider string    `json:"oidcProvider"`
	ClaimKey     string    `json:"claimKey" cfgapply:"read-only"`
	ClaimValue   string    `json:"claimValue"`
	RoleIDs      []string  `json:"roleIds" cfgapply:"xref=rbacRole"`
	CreatedBy    string    `json:"createdBy,omitempty" cfgapply:"read-only"`
	UpdatedBy    string    `json:"updatedBy,omitempty" cfgapply:"read-only"`
	CreatedAt    Timestamp `json:"createdAt,omitempty" cfgapply:"read-only"`
	UpdatedAt    Timestamp `json:"updatedAt,omitempty" cfgapply:"read-only"`
	Origin       string    `json:"origin" cfgapply:"read-only"`
}

type CreateRBACClaimMappingRequest struct {
	Name         string   `json:"name" validate:"required,max=255"`
	OIDCProvider string   `json:"oidcProvider"`
	ClaimKey     string   `json:"claimKey" validate:"max=256" cfgapply:"read-only"`
	ClaimValue   string   `json:"claimValue" validate:"required,max=256"`
	RoleIDs      []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type UpdateRBACClaimMappingRequest struct {
	Name         string   `json:"name" validate:"required,max=255"`
	OIDCProvider string   `json:"oidcProvider"`
	ClaimKey     string   `json:"claimKey" validate:"max=256" cfgapply:"read-only"`
	ClaimValue   string   `json:"claimValue" validate:"required,max=256"`
	RoleIDs      []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type DefaultRolesResponse struct {
	RoleIDs []string `json:"roleIds" cfgapply:"xref=rbacRole"`
	Origin  string   `json:"origin" cfgapply:"read-only"`
}

type SetDefaultRolesRequest struct {
	RoleIDs []string `json:"roleIds" cfgapply:"xref=rbacRole"`
}
