// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"github.com/google/uuid"
)

var yamlRowNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://solace.com/sam/rbac/yaml-row")) //nolint:forbidigo // intentional UUIDv5: a deterministic namespace for the YAML-row ids below

const (
	YAMLKindRole         = "role"
	YAMLKindGrant        = "grant"
	YAMLKindClaimMapping = "claim_mapping"
)

func YAMLRowID(kind, v1ID string) string {
	return uuid.NewSHA1(yamlRowNamespace, []byte(kind+"\x00"+v1ID)).String() //nolint:forbidigo // intentional UUIDv5: a YAML row has no stored id, so its id must be derived the same on every replica and restart
}

func (s *RBACService) V2RoleID(origin, ref string) string {
	if origin != RBACOriginYAML {
		if _, err := uuid.Parse(ref); err == nil {
			return ref
		}
	}
	if s.composer != nil {
		if resolved := s.composer.ResolveRoleRef(ref); resolved != ref {
			if _, err := uuid.Parse(resolved); err == nil {
				return resolved
			}
		}
	}
	return YAMLRowID(YAMLKindRole, ref)
}

func (s *RBACService) V2RoleIDs(origin string, refs []string) []string {
	out := make([]string, len(refs))
	for i, ref := range refs {
		out[i] = s.V2RoleID(origin, ref)
	}
	return out
}

func (s *RBACService) V2ResolvedRoleID(id string) string {
	return s.V2RoleID(RBACOriginCustom, id)
}

func V2RowID(kind, origin, v1ID string) string {
	if origin == RBACOriginYAML {
		return YAMLRowID(kind, v1ID)
	}
	return v1ID
}

func (s *RBACService) V1RoleID(id string) string {
	if name, ok := s.YAMLRoleName(id); ok {
		return name
	}
	return id
}

func findYAMLRow[T any](rows []T, kind, id string, v1ID func(T) string) (T, bool) {
	for _, row := range rows {
		if YAMLRowID(kind, v1ID(row)) == id {
			return row, true
		}
	}
	var zero T
	return zero, false
}

func (s *RBACService) YAMLRoleName(id string) (string, bool) {
	for name := range s.yamlState().Roles.Roles {
		if YAMLRowID(YAMLKindRole, name) == id {
			return name, true
		}
	}
	return "", false
}
