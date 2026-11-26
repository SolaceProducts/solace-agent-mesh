// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const budgetTopUpResourceType = "budgetTopUp"

type CreateBudgetTopUpRequest struct {
	SubjectType string
	SubjectID   string
	Amount      string
}

type BudgetTopUpSubject struct {
	Type        string                     `json:"type"`
	ID          string                     `json:"id"`
	DisplayName *string                    `json:"displayName"`
	User        *BudgetSubjectUserIdentity `json:"user,omitempty"`
}

type BudgetTopUpResponse struct {
	ID          string             `json:"id"`
	Type        string             `json:"type"`
	Subject     BudgetTopUpSubject `json:"subject"`
	Amount      string             `json:"amount"`
	ExpiresTime RESTTimestamp      `json:"expiresTime"`
	CreatedBy   string             `json:"createdBy"`
	CreatedTime RESTTimestamp      `json:"createdTime"`
	UpdatedBy   string             `json:"updatedBy"`
	UpdatedTime RESTTimestamp      `json:"updatedTime"`
}

func toBudgetTopUpResponse(t *store.BudgetTopUp) BudgetTopUpResponse {
	return BudgetTopUpResponse{
		ID:   t.ID,
		Type: budgetTopUpResourceType,
		Subject: BudgetTopUpSubject{
			Type: t.SubjectType, ID: t.SubjectRef,
			DisplayName: t.SubjectDisplayName,
			User:        budgetSubjectUserIdentity(t.SubjectType, t.SubjectEmail, t.SubjectIsSystemUser),
		},
		Amount:      budget.FormatAmount(t.Amount),
		ExpiresTime: restTimestamp(t.ExpiresTime),
		CreatedBy:   t.CreatedBy,
		CreatedTime: restTimestamp(t.CreatedTime),
		UpdatedBy:   t.UpdatedBy,
		UpdatedTime: restTimestamp(t.UpdatedTime),
	}
}
