// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type CreateBudgetRequest struct {
	Name              string  `json:"name"`
	SubjectType       string  `json:"subjectType"`
	Amount            *string `json:"amount"`
	AmountSet         bool    `json:"-"`
	WarningPercentage *int    `json:"warningPercentage"`

	IsDefault json.RawMessage `json:"isDefault"`
}

type UpdateBudgetRequest struct {
	Name                 *string `json:"name"`
	NameSet              bool    `json:"-"`
	Amount               *string `json:"amount"`
	AmountSet            bool    `json:"-"`
	WarningPercentage    *int    `json:"warningPercentage"`
	WarningPercentageSet bool    `json:"-"`

	IsDefault   json.RawMessage `json:"isDefault"`
	SubjectType json.RawMessage `json:"subjectType"`
}

const budgetResourceType = "budget"

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
	CreatedTime         RESTTimestamp `json:"createdTime"`
	UpdatedBy           string        `json:"updatedBy"`
	UpdatedTime         RESTTimestamp `json:"updatedTime"`
}

func toBudgetResponse(b *store.Budget) BudgetResponse {
	return BudgetResponse{
		ID:                  b.ID,
		Type:                budgetResourceType,
		Name:                b.Name,
		SubjectType:         b.SubjectType,
		Amount:              formatBudgetAmount(b.Amount),
		WarningPercentage:   b.WarnPercentage,
		IsDefault:           b.IsDefault,
		AssignedCount:       int(b.AssignedCount),
		HiddenAssignedCount: int(b.HiddenAssignedCount),
		PendingCount:        int(b.PendingCount),
		CreatedBy:           b.CreatedBy,
		CreatedTime:         restTimestamp(b.CreatedTime),
		UpdatedBy:           b.UpdatedBy,
		UpdatedTime:         restTimestamp(b.UpdatedTime),
	}
}

func formatBudgetAmount(amount *int64) *string {
	if amount == nil {
		return nil
	}
	formatted := budget.FormatAmount(*amount)
	return &formatted
}
