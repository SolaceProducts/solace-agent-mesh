// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func pullBudgets(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	budgets, err := c.ListBudgets(ctx)
	if err != nil {
		if opts.OnlyKind != "" {
			return nil, nil, err
		}
		if errors.Is(err, reconcile.ErrBudgetsUnavailable) {
			return nil, nil, nil
		}
		if herr, ok := errors.AsType[*reconcile.HTTPError](err); ok && herr.StatusCode == http.StatusForbidden {
			res.Warnings = append(res.Warnings, "budgets not pulled: listing budgets needs the budget:*:read scope")
			return nil, nil, nil
		}
		return nil, nil, err
	}

	users, err := pullBudgetUsers(ctx, c, budgets, res)
	if err != nil {
		return nil, nil, err
	}

	slices.SortFunc(budgets, func(a, b wire.BudgetResponse) int { return strings.Compare(a.Name, b.Name) })
	var entries []fileEntry
	var names []string
	usedFiles := map[string]struct{}{}
	for _, b := range budgets {
		if opts.Name != "" && b.Name != opts.Name {
			continue
		}
		file := dedupeFileName(budgetFileName(b.Name), usedFiles)
		spec := map[string]any{"subjectType": b.SubjectType, "amount": nil}
		if b.Amount != nil {
			spec["amount"] = *b.Amount
		}
		if b.WarningPercentage != nil {
			spec["warningPercentage"] = *b.WarningPercentage
		}
		if b.IsDefault && b.SubjectType != store.BudgetSubjectMesh {
			spec["isDefault"] = true
		}
		if u, ok := users[b.ID]; ok {
			spec["users"] = u
		}
		raw, err := emitYAML(resourceDoc{Kind: "budget", Name: b.Name, Spec: spec})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{relPath: filepath.Join("budgets", file+".yaml"), content: raw})
		names = append(names, b.Name)
	}
	return entries, names, nil
}

func budgetFileName(name string) string {
	if file := resourceFileName(name); file != "" {
		return file
	}
	return "budget"
}

func dedupeFileName(file string, used map[string]struct{}) string {
	folded := strings.ToLower(file)
	return file + strings.TrimPrefix(dedupeName(folded, used), folded)
}

func pullBudgetUsers(ctx context.Context, c *reconcile.Client, budgets []wire.BudgetResponse, res *Result) (map[string][]any, error) {
	if !slices.ContainsFunc(budgets, func(b wire.BudgetResponse) bool { return b.AssignedCount+b.PendingCount > 0 }) {
		return nil, nil
	}
	assignments, err := c.ListBudgetAssignments(ctx)
	var herr *reconcile.HTTPError
	if errors.As(err, &herr) && herr.StatusCode == http.StatusForbidden {
		res.Warnings = append(res.Warnings, "budget users not pulled: listing budget assignments requires the rbac:*:read scope as well as budget:*:read")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	type entry struct {
		key  string
		item any
	}
	names := make(map[string]string, len(budgets))
	for _, b := range budgets {
		names[b.ID] = b.Name
	}
	idx := reconcile.IndexBudgetAssignments(assignments)

	var warnings []string
	byBudget := map[string][]entry{}
	unwritable := map[string]string{}
	for i := range assignments {
		a := &assignments[i]
		if reconcile.IsHiddenBudgetSubject(a) {
			continue
		}
		if idx.IsStale(a) {
			warnings = append(warnings, fmt.Sprintf("budget %q: pending assignment %s left out, because its user already has an assignment; it is removed at their next sign-in", names[a.Budget.ID], a.ID))
			continue
		}
		id, reason := writtenBudgetIdentity(idx, a)
		if id == nil {
			if _, seen := unwritable[a.Budget.ID]; !seen {
				unwritable[a.Budget.ID] = reason
			}
			continue
		}
		byBudget[a.Budget.ID] = append(byBudget[a.Budget.ID], entry{key: reconcile.BudgetIdentityKey(*id), item: budgetUserItem(*id)})
	}
	for budgetID, reason := range unwritable {
		delete(byBudget, budgetID)
		warnings = append(warnings, fmt.Sprintf("budget %q: users not pulled, because %s; the budget's assignments are left to the UI", names[budgetID], reason))
	}
	slices.Sort(warnings)
	res.Warnings = append(res.Warnings, warnings...)

	out := make(map[string][]any, len(byBudget))
	for budgetID, entries := range byBudget {
		slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.key, b.key) })
		items := make([]any, len(entries))
		for i, e := range entries {
			items[i] = e.item
		}
		out[budgetID] = items
	}
	return out, nil
}

func writtenBudgetIdentity(idx reconcile.BudgetAssignmentIndex, a *wire.BudgetAssignmentResponse) (*apiv2.BudgetAssignmentIdentity, string) {
	id := a.Subject.Identity
	if id == nil {
		return nil, "an assignee has no recorded identity and its user no longer exists"
	}
	if idx.Names(*id, a) {
		return id, ""
	}
	if u := a.Subject.User; u != nil && u.Sub != nil && u.Issuer != nil && !reconcile.IsServiceAccount(u) {
		sub := &apiv2.BudgetAssignmentIdentity{Type: store.BudgetIdentitySub, Value: *u.Sub, Issuer: u.Issuer}
		if idx.Names(*sub, a) {
			return sub, ""
		}
	}
	return nil, "an assignee's recorded email was also used to assign another user, and the assignee has no sub to write instead"
}

func budgetUserItem(id apiv2.BudgetAssignmentIdentity) any {
	switch id.Type {
	case store.BudgetIdentityEmail:
		return id.Value
	case store.BudgetIdentitySystem:
		return rbac.SystemUserPrincipal(id.Value)
	}
	var issuer string
	if id.Issuer != nil {
		issuer = *id.Issuer
	}
	return map[string]any{"type": id.Type, "value": id.Value, "issuer": issuer}
}
