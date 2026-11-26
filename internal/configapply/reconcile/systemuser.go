// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var (
	serviceAccountsResource       = platformResource{path: "/rbac/serviceAccounts"}
	legacyServiceAccountsResource = platformResource{path: "/rbac/systemUsers"}
)

const (
	serviceAccountsHeading       = "rbac/service-accounts"
	legacyServiceAccountsHeading = "rbac/system-users"

	legacyServiceAccountSpellingWarning = "resources.systemUsers, kind: systemUser and rbac/system-users/ are the old spellings of resources.serviceAccounts, kind: serviceAccount and rbac/service-accounts/; " +
		"they are deprecated and keep working until at least " + rbac.ServiceAccountRenameRemovalDate + "; see " + rbac.ServiceAccountRenameNoticeURL

	mixedServiceAccountSpellingWarning = "this manifest lists service accounts under both resources.serviceAccounts and resources.systemUsers; " +
		"a sam released before the rename ignores resources.serviceAccounts and plans a delete for every service account listed there, which apply --prune carries out, " +
		"so upgrade every sam that applies this repository, CI images included"
)

func (c *Client) serviceAccounts(ctx context.Context) (platformResource, error) {
	c.servedServiceAccounts.mu.Lock()
	defer c.servedServiceAccounts.mu.Unlock()
	if c.servedServiceAccounts.path != nil {
		return *c.servedServiceAccounts.path, nil
	}
	err := c.do(ctx, http.MethodGet, serviceAccountsResource.apiPath()+"?pageSize=1", nil, nil)
	var httpErr *HTTPError
	switch {
	case err == nil:
		c.servedServiceAccounts.path = &serviceAccountsResource
	case errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound:
		reconcileLogger.Debug("platform serves service accounts on the path from before the rename", "path", legacyServiceAccountsResource.path)
		c.servedServiceAccounts.path = &legacyServiceAccountsResource
	default:
		return platformResource{}, err
	}
	return *c.servedServiceAccounts.path, nil
}

func (c *Client) ServiceAccountsAPIPath(ctx context.Context) (string, error) {
	res, err := c.serviceAccounts(ctx)
	if err != nil {
		return "", err
	}
	return res.apiPath(), nil
}

func (c *Client) CreateSystemUser(ctx context.Context, req wire.CreateSystemUserRequest) (wire.SystemUserResponse, error) {
	res, err := c.serviceAccounts(ctx)
	if err != nil {
		return wire.SystemUserResponse{}, err
	}
	var resp wire.SystemUserResponse
	if err := c.callResource(ctx, res, http.MethodPost, "", req, &resp); err != nil {
		return wire.SystemUserResponse{}, err
	}
	return resp, nil
}

func (c *Client) ListSystemUsers(ctx context.Context) ([]wire.SystemUserResponse, error) {
	res, err := c.serviceAccounts(ctx)
	if err != nil {
		return nil, err
	}
	return listResource[wire.SystemUserResponse](ctx, c, res)
}

func (c *Client) UpdateSystemUser(ctx context.Context, id string, req wire.ReplaceSystemUserRequest) (wire.SystemUserResponse, error) {
	res, err := c.serviceAccounts(ctx)
	if err != nil {
		return wire.SystemUserResponse{}, err
	}
	var resp wire.SystemUserResponse
	if err := c.callResource(ctx, res, http.MethodPut, "/"+url.PathEscape(id), req, &resp); err != nil {
		return wire.SystemUserResponse{}, err
	}
	return resp, nil
}

func (c *Client) DeleteSystemUser(ctx context.Context, id string) error {
	res, err := c.serviceAccounts(ctx)
	if err != nil {
		return err
	}
	return c.callResource(ctx, res, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

func serviceAccountKindOf(r resolve.ResolvedResource) string {
	if r.Kind == "" {
		return resolve.ServiceAccountKind
	}
	return r.Kind
}

func serviceAccountsHeadingFor(kind string) string {
	if kind == resolve.LegacyServiceAccountKind {
		return legacyServiceAccountsHeading
	}
	return serviceAccountsHeading
}

type systemUserPlan struct {
	Kind      string
	Creates   []systemUserOp
	Updates   []systemUserOp
	Deletes   []systemUserOp
	Unchanged []systemUserOp
}

type systemUserOp struct {
	Header string
	Name   string
	Source string
	Create wire.CreateSystemUserRequest

	ID           string
	RevokedRoles []string
}

type systemUserOpResult struct {
	Op     systemUserOp
	Reason string
	Err    error
}

type systemUserOutcome struct {
	Kind     string
	Created  []systemUserOpResult
	Updated  []systemUserOpResult
	Deleted  []systemUserOpResult
	Skipped  []systemUserOpResult
	Failures []systemUserOpResult
}

func readSystemUserSpec(r resolve.ResolvedResource) (name, displayName string, roleNames []string, err error) {
	name, _ = r.Spec["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = r.Name
	}
	displayName, _ = r.Spec["displayName"].(string)

	if sub := rbac.SystemUserPrincipal(name); rbac.IsReservedSystemUserSub(sub) {
		return "", "", nil, fmt.Errorf("%s %q (%s): %q is a built-in service account and cannot be authored — provisioning it adds roles on top of the built-in ones rather than replacing them, so declare a distinct name and point the entrypoint's run_as at it instead", serviceAccountKindOf(r), r.Name, r.SourceFile, sub)
	}

	raw, ok := r.Spec["roleNames"].([]any)
	if !ok {
		return "", "", nil, fmt.Errorf("%s %q (%s): spec.roleNames is required (a list of role names)", serviceAccountKindOf(r), r.Name, r.SourceFile)
	}
	for _, v := range raw {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			roleNames = append(roleNames, s)
		}
	}
	if len(roleNames) == 0 {
		return "", "", nil, fmt.Errorf("%s %q (%s): spec.roleNames must list at least one role", serviceAccountKindOf(r), r.Name, r.SourceFile)
	}
	return name, displayName, roleNames, nil
}

func planSystemUsers(desired []resolve.ResolvedResource, actual []wire.SystemUserResponse, xref rbacRoleXRefAdapter) (*systemUserPlan, error) {
	plan := &systemUserPlan{Kind: resolve.ServiceAccountKind}
	manageable := ConfigManagedSystemUsers(actual)
	byName := make(map[string]wire.SystemUserResponse, len(manageable))
	for _, u := range manageable {
		byName[u.Name] = u
	}
	declared := make(map[string]struct{}, len(desired))
	seen := map[string]string{}
	for _, r := range desired {
		name, displayName, roleNames, err := readSystemUserSpec(r)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[name]; dup {
			return nil, fmt.Errorf("%s %q duplicates %q — service accounts must be unique by name", serviceAccountKindOf(r), r.Name, prev)
		}
		seen[name] = r.Name
		declared[name] = struct{}{}

		roleIDs := make([]string, 0, len(roleNames))
		for _, rn := range roleNames {
			id, err := xref.LookupID("rbacRole", rn)
			if err != nil {
				return nil, fmt.Errorf("%s %q (%s): %w", serviceAccountKindOf(r), r.Name, r.SourceFile, err)
			}
			roleIDs = append(roleIDs, id)
		}
		op := systemUserOp{
			Header: r.Name,
			Name:   name,
			Source: r.SourceFile,
			Create: wire.CreateSystemUserRequest{Name: name, DisplayName: displayName, RoleIDs: roleIDs},
		}
		current, exists := byName[name]
		switch {
		case !exists:
			plan.Creates = append(plan.Creates, op)
		case systemUserSatisfied(current, op.Create):
			plan.Unchanged = append(plan.Unchanged, op)
		default:
			op.ID = current.ID
			op.RevokedRoles = roleDisplayNames(revokedRoleIDs(current.RoleIDs, roleIDs), xref.idToName)
			plan.Updates = append(plan.Updates, op)
		}
	}
	for _, u := range manageable {
		if _, stillDeclared := declared[u.Name]; stillDeclared {
			continue
		}
		plan.Deletes = append(plan.Deletes, systemUserOp{
			Header:       u.Name,
			Name:         u.Name,
			ID:           u.ID,
			RevokedRoles: roleDisplayNames(u.RoleIDs, xref.idToName),
		})
	}
	return plan, nil
}

func ConfigManagedSystemUsers(all []wire.SystemUserResponse) []wire.SystemUserResponse {
	out := make([]wire.SystemUserResponse, 0, len(all))
	for _, u := range all {
		if u.Origin == entplatform.RBACOriginCustom && !rbac.IsReservedSystemUserSub(u.Sub) {
			out = append(out, u)
		}
	}
	return out
}

func systemUserSatisfied(current wire.SystemUserResponse, desired wire.CreateSystemUserRequest) bool {
	if ptrString(current.DisplayName) != desired.DisplayName {
		return false
	}
	return len(revokedRoleIDs(current.RoleIDs, desired.RoleIDs)) == 0 &&
		len(revokedRoleIDs(desired.RoleIDs, current.RoleIDs)) == 0
}

func revokedRoleIDs(current, desired []string) []string {
	wanted := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		wanted[id] = struct{}{}
	}
	var out []string
	for _, id := range current {
		if _, keep := wanted[id]; !keep {
			out = append(out, id)
		}
	}
	return out
}

func roleDisplayNames(ids []string, idToName map[string]string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		if name, ok := idToName[id]; ok && name != "" {
			out[i] = name
			continue
		}
		out[i] = id
	}
	return out
}

func applySystemUsers(ctx context.Context, c *Client, plan *systemUserPlan, opts ApplyOptions, roleNameToID map[string]string) *systemUserOutcome {
	if plan == nil {
		return &systemUserOutcome{Kind: resolve.ServiceAccountKind}
	}
	outcome := &systemUserOutcome{Kind: plan.Kind}
	if opts.SkipRBACWrites {
		for _, ops := range [][]systemUserOp{plan.Creates, plan.Updates, plan.Deletes} {
			for _, op := range ops {
				outcome.Skipped = append(outcome.Skipped, systemUserOpResult{Op: op, Reason: skipReasonMiniIDP})
			}
		}
		return outcome
	}
	for _, op := range plan.Creates {
		create := op.Create
		create.RoleIDs = resolveRoleIDsAtApply(create.RoleIDs, roleNameToID)

		res := systemUserOpResult{Op: op}
		if _, err := c.CreateSystemUser(ctx, create); err != nil {
			res.Err = err
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		res := systemUserOpResult{Op: op}
		if _, err := c.UpdateSystemUser(ctx, op.ID, wire.ReplaceSystemUserRequest{
			DisplayName: op.Create.DisplayName,
			RoleIDs:     resolveRoleIDsAtApply(op.Create.RoleIDs, roleNameToID),
		}); err != nil {
			res.Err = err
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		outcome.Updated = append(outcome.Updated, res)
	}
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, systemUserOpResult{Op: op})
			continue
		}
		res := systemUserOpResult{Op: op}
		if err := c.DeleteSystemUser(ctx, op.ID); err != nil {
			res.Err = err
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		outcome.Deleted = append(outcome.Deleted, res)
	}
	return outcome
}

func resolveRoleIDsAtApply(roleIDs []string, roleNameToID map[string]string) []string {
	out := make([]string, len(roleIDs))
	for i, rid := range roleIDs {
		if !looksLikeUUID(rid) {
			if id, ok := roleNameToID[rid]; ok {
				rid = id
			}
		}
		out[i] = rid
	}
	return out
}

func systemUserLabel(op systemUserOp) string {
	if op.Header != "" {
		return op.Header
	}
	return op.Name
}

func systemUserUpdateLabel(op systemUserOp, verb string) string {
	if len(op.RevokedRoles) == 0 {
		return verb
	}
	return fmt.Sprintf("%s (revokes %s)", verb, strings.Join(op.RevokedRoles, ", "))
}

func renderSystemUserPlan(w io.Writer, p *systemUserPlan, opts FormatOptions) {
	renderRBACGenericPlan(w, serviceAccountsHeadingFor(p.Kind), len(p.Creates), len(p.Updates), len(p.Deletes), len(p.Unchanged), func() {
		for _, op := range p.Creates {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(systemUserLabel(op))), color(opts, ansiGreen, "create"))
		}
		for _, op := range p.Updates {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiYellow, "~ "+padName(systemUserLabel(op))), color(opts, ansiYellow, systemUserUpdateLabel(op, "update")))
		}
		for _, op := range p.Deletes {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(systemUserLabel(op))), color(opts, ansiRed, planDeleteLabel(opts)))
		}
		for _, op := range p.Unchanged {
			_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "= "+padName(systemUserLabel(op))), color(opts, ansiDim, noChangeLabel))
		}
	})
}

func renderSystemUserApply(w io.Writer, o *systemUserOutcome, opts FormatOptions) {
	if o == nil || (len(o.Created)+len(o.Updated)+len(o.Deleted)+len(o.Skipped)+len(o.Failures)) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, serviceAccountsHeadingFor(o.Kind))
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(systemUserLabel(r.Op))), color(opts, ansiGreen, "created"))
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiYellow, "~ "+padName(systemUserLabel(r.Op))), color(opts, ansiYellow, systemUserUpdateLabel(r.Op, "updated")))
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(systemUserLabel(r.Op))), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(systemUserLabel(r.Op))), color(opts, ansiDim, rbacSkipLabel(r.Reason)))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(systemUserLabel(r.Op))), color(opts, ansiRed, "write"+failedSuffix), r.Err)
	}
}
