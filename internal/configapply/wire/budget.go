// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateBudgetRequest struct {
	Name              string        `json:"name"`
	SubjectType       string        `json:"subjectType"`
	Amount            Patch[string] `json:"amount,omitzero" clearable:"true"`
	WarningPercentage *int          `json:"warningPercentage"`
}

type UpdateBudgetRequest struct {
	Name              Patch[string] `json:"name,omitzero"`
	Amount            Patch[string] `json:"amount,omitzero" clearable:"true"`
	WarningPercentage Patch[int]    `json:"warningPercentage,omitzero" clearable:"true"`
	SubjectType       Patch[string] `json:"subjectType,omitzero"`
	IsDefault         Patch[bool]   `json:"isDefault,omitzero"`
}

type BudgetResponse struct {
	ID                  string        `json:"id"`
	Type                string        `json:"type"`
	Name                string        `json:"name"`
	SubjectType         string        `json:"subjectType"`
	Amount              *string       `json:"amount"`
	WarningPercentage   *int          `json:"warningPercentage"`
	IsDefault           bool          `json:"isDefault"`
	AssignedCount       int           `json:"assignedCount"`
	HiddenAssignedCount int           `json:"hiddenAssignedCount"`
	PendingCount        int           `json:"pendingCount"`
	CreatedBy           string        `json:"createdBy"`
	CreatedTime         apiv2.Instant `json:"createdTime"`
	UpdatedBy           string        `json:"updatedBy"`
	UpdatedTime         apiv2.Instant `json:"updatedTime"`
}

type CreateBudgetAssignmentRequest struct {
	SubjectType string                                 `json:"subjectType"`
	SubjectID   *string                                `json:"subjectId"`
	Identity    *apiv2.BudgetAssignmentIdentityRequest `json:"identity"`
	BudgetID    string                                 `json:"budgetId"`
}

type UpdateBudgetAssignmentRequest struct {
	BudgetID    Patch[string]                                `json:"budgetId,omitzero"`
	Identity    Patch[apiv2.BudgetAssignmentIdentityRequest] `json:"identity,omitzero"`
	SubjectType Patch[string]                                `json:"subjectType,omitzero"`
	SubjectID   Patch[string]                                `json:"subjectId,omitzero"`
}

type BudgetAssignmentResponse struct {
	ID          string                        `json:"id"`
	Type        string                        `json:"type"`
	State       string                        `json:"state"`
	Subject     apiv2.BudgetAssignmentSubject `json:"subject"`
	Budget      apiv2.BudgetRef               `json:"budget"`
	CreatedBy   string                        `json:"createdBy"`
	CreatedTime apiv2.Instant                 `json:"createdTime"`
	UpdatedBy   string                        `json:"updatedBy"`
	UpdatedTime apiv2.Instant                 `json:"updatedTime"`
}
