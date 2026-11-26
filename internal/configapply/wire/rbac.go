// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateRoleRequest struct {
	Name        string   `json:"name" validate:"required,safename"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	Inherits    []string `json:"inherits" cfgapply:"xref=rbacRole"`
}

type ReplaceRoleRequest struct {
	Name        string   `json:"name" validate:"required,safename"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	Inherits    []string `json:"inherits" cfgapply:"xref=rbacRole"`
}

type RoleResponse struct {
	ID           string        `json:"id" cfgapply:"read-only"`
	Type         string        `json:"type" cfgapply:"read-only"`
	Name         string        `json:"name"`
	Description  *string       `json:"description"`
	Scopes       []string      `json:"scopes"`
	Inherits     []string      `json:"inherits" cfgapply:"xref=rbacRole"`
	Origin       string        `json:"origin" cfgapply:"read-only"`
	NameConflict bool          `json:"nameConflict" cfgapply:"read-only"`
	CreatedBy    *string       `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy    *string       `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime  apiv2.Instant `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime  apiv2.Instant `json:"updatedTime" cfgapply:"read-only"`
}

type CreateGrantRequest struct {
	Subject apiv2.CreateGrantSubject `json:"subject" validate:"required"`
	RoleID  string                   `json:"roleId" validate:"required" cfgapply:"xref=rbacRole"`
}

type UpdateGrantRequest struct {
	Subject Patch[apiv2.CreateGrantSubject] `json:"subject,omitzero" validate:"-"`
	RoleID  Patch[string]                   `json:"roleId,omitzero" cfgapply:"read-only"`
	UserID  Patch[string]                   `json:"userId,omitzero" cfgapply:"read-only"`
}

type GrantResponse struct {
	ID          string              `json:"id" cfgapply:"read-only"`
	Type        string              `json:"type" cfgapply:"read-only"`
	Subject     *apiv2.GrantSubject `json:"subject"`
	UserID      *string             `json:"userId" cfgapply:"read-only"`
	RoleID      string              `json:"roleId" cfgapply:"xref=rbacRole"`
	State       *string             `json:"state" cfgapply:"read-only"`
	Origin      string              `json:"origin" cfgapply:"read-only"`
	CreatedBy   *string             `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy   *string             `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime apiv2.Instant       `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime apiv2.Instant       `json:"updatedTime" cfgapply:"read-only"`
	User        *apiv2.GrantUser    `json:"user" cfgapply:"read-only"`
}

type CreateClaimMappingRequest struct {
	Name         string   `json:"name" validate:"required,max=255"`
	OIDCProvider string   `json:"oidcProvider"`
	ClaimKey     string   `json:"claimKey" validate:"max=256" cfgapply:"read-only"`
	ClaimValue   string   `json:"claimValue" validate:"required,max=256"`
	RoleIDs      []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type ReplaceClaimMappingRequest struct {
	Name         string   `json:"name" validate:"required,max=255"`
	OIDCProvider string   `json:"oidcProvider"`
	ClaimKey     string   `json:"claimKey" validate:"max=256" cfgapply:"read-only"`
	ClaimValue   string   `json:"claimValue" validate:"required,max=256"`
	RoleIDs      []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type ClaimMappingResponse struct {
	ID           string        `json:"id" cfgapply:"read-only"`
	Type         string        `json:"type" cfgapply:"read-only"`
	Name         *string       `json:"name"`
	OIDCProvider *string       `json:"oidcProvider"`
	ClaimKey     *string       `json:"claimKey" cfgapply:"read-only"`
	ClaimValue   string        `json:"claimValue"`
	RoleIDs      []string      `json:"roleIds" cfgapply:"xref=rbacRole"`
	Origin       string        `json:"origin" cfgapply:"read-only"`
	CreatedBy    *string       `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy    *string       `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime  apiv2.Instant `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime  apiv2.Instant `json:"updatedTime" cfgapply:"read-only"`
}

type CreateSystemUserRequest struct {
	Name        string   `json:"name" validate:"required,max=255"`
	DisplayName string   `json:"displayName"`
	RoleIDs     []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type ReplaceSystemUserRequest struct {
	DisplayName string   `json:"displayName"`
	RoleIDs     []string `json:"roleIds" validate:"required,min=1" cfgapply:"xref=rbacRole"`
}

type SystemUserResponse struct {
	ID          string        `json:"id" cfgapply:"read-only"`
	Type        string        `json:"type" cfgapply:"read-only"`
	Name        string        `json:"name"`
	Sub         string        `json:"sub" cfgapply:"read-only"`
	DisplayName *string       `json:"displayName"`
	RoleIDs     []string      `json:"roleIds" cfgapply:"xref=rbacRole"`
	Origin      string        `json:"origin" cfgapply:"read-only"`
	CreatedBy   *string       `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy   *string       `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime apiv2.Instant `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime apiv2.Instant `json:"updatedTime" cfgapply:"read-only"`
}

type ReplaceDefaultRolesRequest struct {
	RoleIDs []string `json:"roleIds" cfgapply:"xref=rbacRole"`
}

type DefaultRolesResponse struct {
	RoleIDs []string `json:"roleIds" cfgapply:"xref=rbacRole"`
	Origin  string   `json:"origin" cfgapply:"read-only"`
}
