// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

func TestYAMLRoleSeedScopes(t *testing.T) {
	repoRoot := repoRootFromTest(t)

	cases := []struct {
		name         string
		path         string
		mapPath      []string
		internalOnly bool
	}{
		{
			name:         "dev_idp_rbac_roles",
			path:         filepath.Join(repoRoot, "dev", "idp", "rbac-roles.yaml"),
			mapPath:      []string{"roles"},
			internalOnly: true,
		},
		{
			name:         "test_platform_rbac_roles",
			path:         filepath.Join(repoRoot, "test", "platform", "testdata", "rbac_roles.yaml"),
			mapPath:      []string{"roles"},
			internalOnly: true,
		},
		{
			name:         "test_gateway_rbac_roles",
			path:         filepath.Join(repoRoot, "test", "gateway", "testdata", "rbac_roles.yaml"),
			mapPath:      []string{"roles"},
			internalOnly: true,
		},
		{
			name:         "configs_e2e_harness_auth_rbac_roles",
			path:         filepath.Join(repoRoot, "configs-e2e-harness", "auth", "rbac-roles.yaml"),
			mapPath:      []string{"roles"},
			internalOnly: true,
		},
		{
			name:    "embedded_system_defaults",
			path:    filepath.Join(repoRoot, "internal", "rbac", "defaults", "system_defaults.yaml"),
			mapPath: []string{"roles"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.internalOnly {
				sourcetree.RequireInternal(t)
			}
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			roles := extractRolesMap(t, data, tc.mapPath)
			if len(roles) == 0 {
				t.Fatalf("no roles found at path %v in %s — parser likely broke", tc.mapPath, tc.path)
			}
			for roleName, scopes := range roles {
				if err := validateScopes(scopes); err != nil {
					var v *ValidationError
					if errors.As(err, &v) {
						t.Errorf("role %q in %s: %s\ndetails: %v", roleName, tc.path, v.Message, v.Details)
					} else {
						t.Errorf("role %q in %s: %v", roleName, tc.path, err)
					}
				}
			}
		})
	}
}

func extractRolesMap(t *testing.T, data []byte, mapPath []string) map[string][]string {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cur := any(root)
	for _, segment := range mapPath {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[segment]
	}
	rolesMap, ok := cur.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(rolesMap))
	for roleName, roleVal := range rolesMap {
		roleObj, ok := roleVal.(map[string]any)
		if !ok {
			continue
		}
		scopesAny, ok := roleObj["scopes"]
		if !ok {
			out[roleName] = nil
			continue
		}
		scopesList, ok := scopesAny.([]any)
		if !ok {
			t.Errorf("role %q: scopes is not a list (%T)", roleName, scopesAny)
			continue
		}
		scopes := make([]string, 0, len(scopesList))
		for _, s := range scopesList {
			if str, ok := s.(string); ok {
				scopes = append(scopes, str)
			}
		}
		out[roleName] = scopes
	}
	return out
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found walking up from working directory")
		}
		dir = parent
	}
}
