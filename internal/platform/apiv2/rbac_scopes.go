// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const expandResourceScopes = "resourceScopes"

type ScopeCatalogEntry struct {
	Scope        string  `json:"scope" example:"agent:*:invoke"`
	Name         string  `json:"name" example:"Invoke agents"`
	Description  *string `json:"description" example:"Send tasks to any agent."`
	ResourceName *string `json:"resourceName" example:"Support agent"`
}

type listScopesRequest struct {
	Page     Page      `json:"-"`
	Sort     SortOrder `sort:"scope,name" json:"-"`
	Category string    `query:"category" json:"-" example:"agent"`
	Search   string    `query:"search" json:"-" example:"invoke"`
	Expand   string    `query:"expand" json:"-" example:"resourceScopes"`
}

type scopeHandler struct {
	resolvers map[string]entplatform.ResourceResolver
}

func scopeRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.RBACScopeResolvers == nil {
		return nil
	}
	h := &scopeHandler{resolvers: deps.RBACScopeResolvers}
	return []Route{
		{Method: http.MethodGet, Path: "/rbac/scopes", Scope: rbac.ScopeRBACRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listScopes", Summary: "Get a list of scopes",
				Text: "Returns one page of the scopes that can be granted to a role.", Tag: rbacTag},
			Handler: Handle(h.list, "Unable to list scopes")},
	}
}

func (h *scopeHandler) list(ctx context.Context, req listScopesRequest) (List[ScopeCatalogEntry], error) {
	expand, err := parseExpandOf(req.Expand, expandResourceScopes)
	if err != nil {
		return List[ScopeCatalogEntry]{}, err
	}
	entries, err := entplatform.ScopeCatalog(ctx, h.resolvers, expand, req.Category, req.Search)
	if err != nil {
		return List[ScopeCatalogEntry]{}, err
	}
	slices.SortFunc(entries, scopeComparator(req.Sort))
	out := make([]ScopeCatalogEntry, len(entries))
	for i, e := range entries {
		out[i] = toScopeCatalogEntry(e)
	}
	return PageOf(out, req.Page), nil
}

func parseExpandOf(raw, only string) (bool, error) {
	var expand bool
	for v := range strings.SplitSeq(raw, ",") {
		switch strings.TrimSpace(v) {
		case "":
		case only:
			expand = true
		default:
			return false, QueryError(http.StatusUnprocessableEntity, "expand", "must be "+only)
		}
	}
	return expand, nil
}

func scopeComparator(order SortOrder) func(a, b entplatform.ScopeCatalogEntry) int {
	compare := func(a, b entplatform.ScopeCatalogEntry) int { return cmp.Compare(a.Scope, b.Scope) }
	if order.Field == "name" {
		compare = func(a, b entplatform.ScopeCatalogEntry) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Scope, b.Scope))
		}
	}
	if order.Desc {
		return func(a, b entplatform.ScopeCatalogEntry) int { return compare(b, a) }
	}
	return compare
}

func toScopeCatalogEntry(e entplatform.ScopeCatalogEntry) ScopeCatalogEntry {
	return ScopeCatalogEntry{
		Scope:        e.Scope,
		Name:         e.Name,
		Description:  NullIfZero(e.Description),
		ResourceName: NullIfZero(e.ResourceName),
	}
}
