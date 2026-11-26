// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "errors"

type BudgetConflictReason string

const (
	BudgetConflictNameTaken             BudgetConflictReason = "name_taken"
	BudgetConflictMeshExists            BudgetConflictReason = "mesh_exists"
	BudgetConflictStale                 BudgetConflictReason = "stale"
	BudgetConflictDefaultChanged        BudgetConflictReason = "default_changed"
	BudgetConflictTierDefault           BudgetConflictReason = "tier_default"
	BudgetConflictMeshUndeletable       BudgetConflictReason = "mesh_undeletable"
	BudgetConflictAssignedWhileDeleting BudgetConflictReason = "assigned_while_deleting"
	BudgetConflictSubjectAssigned       BudgetConflictReason = "subject_assigned"
	BudgetConflictIdentityPending       BudgetConflictReason = "identity_pending"
	BudgetConflictAssignmentRaced       BudgetConflictReason = "assignment_raced"
	BudgetConflictAssignmentsRaced      BudgetConflictReason = "assignments_raced"
	BudgetConflictTopUpUnlimited        BudgetConflictReason = "top_up_unlimited"
	BudgetConflictTopUpNoBudget         BudgetConflictReason = "top_up_no_budget"
	BudgetConflictTopUpPeriodChanged    BudgetConflictReason = "top_up_period_changed"
)

type budgetConflict struct {
	reason   BudgetConflictReason
	conflict *ConflictError
}

func (e *budgetConflict) Error() string { return e.conflict.Error() }
func (e *budgetConflict) Unwrap() error { return e.conflict }

func newBudgetConflict(reason BudgetConflictReason, conflict *ConflictError) error {
	return &budgetConflict{reason: reason, conflict: conflict}
}

func BudgetConflictReasonOf(err error) (BudgetConflictReason, bool) {
	c, ok := errors.AsType[*budgetConflict](err)
	if !ok {
		return "", false
	}
	return c.reason, true
}
