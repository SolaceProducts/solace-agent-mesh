// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type ResourceInstance struct {
	CanonicalName string
	DisplayName   string
}

type ResourceResolver func(ctx context.Context) ([]ResourceInstance, error)

const MsgMiniIDPWriteDisabled = "RBAC management is disabled when SAM is configured with the built-in mini IDP; bring your own IDP to enable role management"

func guardMiniIDPWrite(miniIDPMode bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if miniIDPMode {
			handleServiceError(w, &ForbiddenError{Message: MsgMiniIDPWriteDisabled})
			return
		}
		next(w, r)
	}
}

var (
	rbacRoleSortComparators = map[string]func(a, b RBACRoleResponse) int{
		"name": func(a, b RBACRoleResponse) int { return cmp.Or(compareFold(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) },
		"createdAt": func(a, b RBACRoleResponse) int {
			return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
		},
		"updatedAt": func(a, b RBACRoleResponse) int {
			return cmp.Or(cmp.Compare(a.UpdatedAt, b.UpdatedAt), cmp.Compare(a.ID, b.ID))
		},
	}
)

func ScopeCatalog(ctx context.Context, resolvers map[string]ResourceResolver, expandResources bool, category, search string) ([]ScopeCatalogEntry, error) {
	catalog := rbac.Catalog()
	entries := scopeCatalogEntries(catalog)
	if expandResources {
		concrete, err := resolveResourceScopes(ctx, resolvers, catalog)
		if err != nil {
			return nil, err
		}
		entries = append(entries, concrete...)
	}
	search = strings.TrimSpace(search)
	return filterInPlace(entries, func(e ScopeCatalogEntry) bool {
		if category != "" {
			if cat, _, _ := splitScope(e.Scope); cat != category {
				return false
			}
		}
		return search == "" || containsFold(e.Scope, search) || containsFold(e.Name, search)
	}), nil
}

func scopeCatalogEntries(catalog []rbac.ScopeInfo) []ScopeCatalogEntry {
	out := make([]ScopeCatalogEntry, 0, len(catalog))
	for _, s := range catalog {
		out = append(out, ScopeCatalogEntry{Scope: s.Scope, Name: s.Name, Description: s.Description})
	}
	return out
}

func resolveResourceScopes(ctx context.Context, resolvers map[string]ResourceResolver, catalog []rbac.ScopeInfo) ([]ScopeCatalogEntry, error) {
	var out []ScopeCatalogEntry
	for _, s := range catalog {
		resolve := resolvers[s.Scope]
		if resolve == nil {
			continue
		}
		category, _, verb := splitScope(s.Scope)
		instances, err := resolve(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve %s resource scopes: %w", s.Scope, err)
		}
		for _, inst := range instances {
			out = append(out, ScopeCatalogEntry{
				Scope:        category + ":" + inst.CanonicalName + ":" + verb,
				Name:         verbActionLabel(verb) + " " + inst.DisplayName,
				ResourceName: inst.DisplayName,
			})
		}
	}
	return out, nil
}

func splitScope(scope string) (category, resource, verb string) {
	parts := strings.SplitN(scope, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

func verbActionLabel(verb string) string {
	switch verb {
	case "":
		return ""
	case "read_feedback":
		return "Read feedback of"
	case "read_trace":
		return "Read traces of"
	}
	return strings.ToUpper(verb[:1]) + verb[1:]
}
