// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func checkPrunedSystemUserAssignments(plan *PlanResult) error {
	conflicts := prunedSystemUserAssignments(plan)
	errs := make([]error, 0, len(conflicts))
	for _, op := range conflicts {
		errs = append(errs, fmt.Errorf("budget %q (%s) lists %s, which this apply deletes because it has no %s file; restore the file or remove the service account from the budget",
			op.Budget, op.Source, rbac.SystemUserPrincipal(op.Identity.Value), serviceAccountFileKind(plan)))
	}
	return errors.Join(errs...)
}

func prunedSystemUserWarnings(plan *PlanResult) []string {
	conflicts := prunedSystemUserAssignments(plan)
	warnings := make([]string, 0, len(conflicts))
	for _, op := range conflicts {
		warnings = append(warnings, fmt.Sprintf("budget %q (%s) lists %s, which has no %s file, so apply --prune would delete it; apply --prune fails until you restore the file or remove the service account from the budget",
			op.Budget, op.Source, rbac.SystemUserPrincipal(op.Identity.Value), serviceAccountFileKind(plan)))
	}
	return warnings
}

func serviceAccountFileKind(plan *PlanResult) string {
	return cmp.Or(plan.SystemUsers.Kind, resolve.ServiceAccountKind)
}

func prunedSystemUserAssignments(plan *PlanResult) []budgetAssignmentOp {
	if plan.SystemUsers == nil || len(plan.SystemUsers.Deletes) == 0 || plan.BudgetAssignments == nil {
		return nil
	}
	deleted := make(map[string]bool, len(plan.SystemUsers.Deletes))
	for _, op := range plan.SystemUsers.Deletes {
		deleted[op.Name] = true
	}
	p := plan.BudgetAssignments
	var conflicts []budgetAssignmentOp
	for _, op := range slices.Concat(p.Creates, p.Moves, p.Unchanged) {
		if op.Identity.Type == store.BudgetIdentitySystem && deleted[op.Identity.Value] {
			conflicts = append(conflicts, op)
		}
	}
	return conflicts
}
