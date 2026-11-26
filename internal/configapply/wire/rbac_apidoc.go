// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateRoleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateRoleRequest is config apply's copy of apiv2.CreateRoleRequest.",
	}
}

func (ReplaceRoleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceRoleRequest is config apply's copy of apiv2.ReplaceRoleRequest. The
role's ID goes in the path, not the body.`,
	}
}

func (RoleResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RoleResponse is config apply's copy of apiv2.Role.",
	}
}

func (CreateGrantRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateGrantRequest is config apply's copy of apiv2.CreateGrantRequest.",
	}
}

func (UpdateGrantRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateGrantRequest is config apply's copy of apiv2.UpdateGrantRequest, a
PATCH body. The grant's ID goes in the path, not the body.`,
	}
}

func (GrantResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "GrantResponse is config apply's copy of apiv2.Grant.",
	}
}

func (CreateClaimMappingRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateClaimMappingRequest is config apply's copy of
apiv2.CreateClaimMappingRequest.`,
	}
}

func (ReplaceClaimMappingRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceClaimMappingRequest is config apply's copy of
apiv2.ReplaceClaimMappingRequest. The mapping's ID goes in the path, not the
body.`,
	}
}

func (ClaimMappingResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ClaimMappingResponse is config apply's copy of apiv2.ClaimMapping.",
	}
}

func (CreateSystemUserRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateSystemUserRequest is config apply's copy of
apiv2.CreateSystemUserRequest.`,
	}
}

func (ReplaceSystemUserRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceSystemUserRequest is config apply's copy of
apiv2.ReplaceSystemUserRequest. The service account's ID goes in the path, not
the body.`,
	}
}

func (SystemUserResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SystemUserResponse is config apply's copy of apiv2.SystemUser.",
	}
}

func (ReplaceDefaultRolesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceDefaultRolesRequest is config apply's copy of
apiv2.ReplaceDefaultRolesRequest.`,
	}
}

func (DefaultRolesResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DefaultRolesResponse is config apply's copy of apiv2.DefaultRoles.",
	}
}
