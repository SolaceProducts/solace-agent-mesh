// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var defaultRolesResource = platformResource{path: "/rbac/defaultRoles"}

type defaultRolesVerb string

const (
	defaultRolesVerbNone      defaultRolesVerb = "none"
	defaultRolesVerbPut       defaultRolesVerb = "put"
	defaultRolesVerbUnchanged defaultRolesVerb = "unchanged"
)

type defaultRolesPlan struct {
	Verb         defaultRolesVerb
	RoleNames    []string
	CurrentNames []string
}

type defaultRolesOpResult struct {
	Verb   defaultRolesVerb
	Reason string
	Err    error
}

type defaultRolesOutcome struct {
	Applied  *defaultRolesOpResult
	Skipped  *defaultRolesOpResult
	Failures []defaultRolesOpResult
}

func (c *Client) GetDefaultRoles(ctx context.Context) (*wire.DefaultRolesResponse, error) {
	var resp wire.DefaultRolesResponse
	if err := c.callResource(ctx, defaultRolesResource, http.MethodGet, "", nil, &resp); err != nil {
		return nil, fmt.Errorf("get default roles: %w", err)
	}
	return &resp, nil
}

func planDefaultRoles(ctx context.Context, c *Client, desired *manifest.DefaultRolesSpec) (*defaultRolesPlan, error) {
	if desired == nil {
		return &defaultRolesPlan{Verb: defaultRolesVerbNone}, nil
	}
	current, err := c.GetDefaultRoles(ctx)
	if err != nil {
		return nil, err
	}
	currentNames, err := currentDefaultRoleNames(ctx, c, current)
	if err != nil {
		return nil, err
	}
	if slices.Equal(sortedRoleNames(desired.Roles), sortedRoleNames(currentNames)) {
		return &defaultRolesPlan{Verb: defaultRolesVerbUnchanged, RoleNames: desired.Roles, CurrentNames: currentNames}, nil
	}
	return &defaultRolesPlan{Verb: defaultRolesVerbPut, RoleNames: desired.Roles, CurrentNames: currentNames}, nil
}

func currentDefaultRoleNames(ctx context.Context, c *Client, current *wire.DefaultRolesResponse) ([]string, error) {
	if current == nil || len(current.RoleIDs) == 0 {
		return nil, nil
	}
	roles, err := c.ListRBACRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list roles to resolve default-role names: %w", err)
	}
	idToName := make(map[string]string, len(roles))
	for _, r := range roles {
		idToName[r.ID] = r.Name
	}
	names := make([]string, 0, len(current.RoleIDs))
	for _, id := range current.RoleIDs {
		if n, ok := idToName[id]; ok {
			names = append(names, n)
		} else {
			names = append(names, id)
		}
	}
	return names, nil
}

func applyDefaultRoles(ctx context.Context, c *Client, plan *defaultRolesPlan, opts ApplyOptions, roleNameToID map[string]string) *defaultRolesOutcome {
	if plan == nil || plan.Verb == defaultRolesVerbNone {
		return nil
	}
	if plan.Verb == defaultRolesVerbUnchanged {
		return &defaultRolesOutcome{Skipped: &defaultRolesOpResult{Verb: defaultRolesVerbUnchanged}}
	}
	if opts.SkipRBACWrites {
		return &defaultRolesOutcome{Skipped: &defaultRolesOpResult{Verb: defaultRolesVerbPut, Reason: skipReasonMiniIDP}}
	}
	ids, err := resolveDefaultRoleIDs(ctx, c, plan.RoleNames, roleNameToID)
	if err != nil {
		res := defaultRolesOpResult{Verb: defaultRolesVerbPut, Err: err}
		return &defaultRolesOutcome{Failures: []defaultRolesOpResult{res}}
	}
	body := wire.ReplaceDefaultRolesRequest{RoleIDs: ids}
	err = c.callResource(ctx, defaultRolesResource, http.MethodPut, "", body, nil)
	res := defaultRolesOpResult{Verb: defaultRolesVerbPut, Err: err}
	if err != nil {
		return &defaultRolesOutcome{Failures: []defaultRolesOpResult{res}}
	}
	return &defaultRolesOutcome{Applied: &res}
}

func resolveDefaultRoleIDs(ctx context.Context, c *Client, names []string, roleNameToID map[string]string) ([]string, error) {
	var dbNameToID map[string]string
	ids := make([]string, 0, len(names))
	for _, name := range names {
		if id, ok := roleNameToID[name]; ok {
			ids = append(ids, id)
			continue
		}
		if dbNameToID == nil {
			roles, err := c.ListRBACRoles(ctx)
			if err != nil {
				return nil, fmt.Errorf("list roles to resolve default roles: %w", err)
			}
			dbNameToID = make(map[string]string, len(roles))
			for _, r := range roles {
				if r.Origin == entplatform.RBACOriginCustom || r.Origin == entplatform.RBACOriginBuiltin {
					dbNameToID[r.Name] = r.ID
				}
			}
		}
		id, ok := dbNameToID[name]
		if !ok {
			return nil, fmt.Errorf("platform.defaultRoles: role %q is not a known DB role (only DB roles can be defaults)", name)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func sortedRoleNames(in []string) []string {
	out := append([]string(nil), in...)
	slices.Sort(out)
	return out
}
