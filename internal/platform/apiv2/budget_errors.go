// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var budgetConflictSentences = map[entplatform.BudgetConflictReason]string{
	entplatform.BudgetConflictNameTaken:             "A budget with this name already exists.",
	entplatform.BudgetConflictMeshExists:            "A mesh budget already exists. Only one is allowed.",
	entplatform.BudgetConflictStale:                 "This budget changed while your edit was being saved. Try again.",
	entplatform.BudgetConflictDefaultChanged:        "The default for this tier changed while the request was running. Retry the request.",
	entplatform.BudgetConflictTierDefault:           "This budget is its tier's default. Make another budget the default before deleting it.",
	entplatform.BudgetConflictMeshUndeletable:       "The mesh budget can't be deleted. Edit its limit instead.",
	entplatform.BudgetConflictAssignedWhileDeleting: "This budget was assigned while it was being deleted. Try again.",
	entplatform.BudgetConflictSubjectAssigned:       "This user already has a budget assignment.",
	entplatform.BudgetConflictIdentityPending:       "This identity already has a pending budget assignment.",
	entplatform.BudgetConflictAssignmentRaced:       "This user's budget assignment changed while saving. Reload and try again.",
	entplatform.BudgetConflictAssignmentsRaced:      "These assignments changed while the save was in progress. Reload them and save again.",
	entplatform.BudgetConflictTopUpUnlimited:        "The budget that applies to this subject is unlimited, so a top-up would have no effect. Set an amount on it first.",
	entplatform.BudgetConflictTopUpNoBudget:         "No budget applies to this subject, so there is no limit to add to. Set a default budget first.",
	entplatform.BudgetConflictTopUpPeriodChanged:    "A new budget period started while this was being saved, and an additional amount has already been granted for it. Saving again replaces that amount for the new period.",
}

func explainBudgetConflict(err error, fallback string) error {
	if reason, ok := entplatform.BudgetConflictReasonOf(err); ok {
		if sentence, known := budgetConflictSentences[reason]; known {
			return &CallerError{Status: http.StatusConflict, Message: sentence, Cause: err}
		}
	}
	return explainConflict(err, fallback)
}
