// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const budgetAssignmentResourceType = "budgetAssignment"

type BudgetAssignmentIdentity struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Issuer string `json:"issuer,omitempty"`
}

type CreateBudgetAssignmentRequest struct {
	SubjectType string                    `json:"subjectType"`
	SubjectID   string                    `json:"subjectId,omitempty"`
	Identity    *BudgetAssignmentIdentity `json:"identity,omitempty"`
	BudgetID    string                    `json:"budgetId"`
}

type UpdateBudgetAssignmentRequest struct {
	BudgetID string                    `json:"budgetId"`
	Identity *BudgetAssignmentIdentity `json:"identity"`
}

type BudgetAssignmentSubject struct {
	Type        string                    `json:"type"`
	ID          *string                   `json:"id"`
	DisplayName *string                   `json:"displayName"`
	User        *BudgetAssignmentUser     `json:"user,omitempty"`
	Identity    *BudgetAssignmentIdentity `json:"identity,omitempty"`
}

type BudgetSubjectUserIdentity struct {
	Email        *string `json:"email"`
	IsSystemUser bool    `json:"isSystemUser"`
}

type BudgetAssignmentUser struct {
	Email        *string `json:"email"`
	IsSystemUser bool    `json:"isSystemUser"`
	EmailShared  *bool   `json:"emailShared,omitempty"`
	Sub          *string `json:"sub,omitempty"`
	Issuer       *string `json:"issuer,omitempty"`
}

func budgetSubjectUserIdentity(subjectType string, email *string, isSystemUser bool) *BudgetSubjectUserIdentity {
	if subjectType != store.BudgetSubjectUser {
		return nil
	}
	return &BudgetSubjectUserIdentity{Email: email, IsSystemUser: isSystemUser}
}

func budgetAssignmentUser(a *store.BudgetAssignment, expandIdentity bool) *BudgetAssignmentUser {
	base := budgetSubjectUserIdentity(a.SubjectType, a.SubjectEmail, a.SubjectIsSystemUser)
	if base == nil {
		return nil
	}
	u := &BudgetAssignmentUser{Email: base.Email, IsSystemUser: base.IsSystemUser}
	if expandIdentity {
		u.EmailShared = &a.SubjectEmailShared
		if a.SubjectSub != "" {
			u.Sub, u.Issuer = &a.SubjectSub, &a.SubjectIssuer
		}
	}
	return u
}

type BudgetAssignmentBudget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type BudgetAssignmentResponse struct {
	ID          string                  `json:"id"`
	Type        string                  `json:"type"`
	State       string                  `json:"state"`
	Subject     BudgetAssignmentSubject `json:"subject"`
	Budget      BudgetAssignmentBudget  `json:"budget"`
	CreatedBy   string                  `json:"createdBy"`
	CreatedTime RESTTimestamp           `json:"createdTime"`
	UpdatedBy   string                  `json:"updatedBy"`
	UpdatedTime RESTTimestamp           `json:"updatedTime"`
}

func toBudgetAssignmentResponse(a *store.BudgetAssignment, expandIdentity bool) BudgetAssignmentResponse {
	subject := BudgetAssignmentSubject{Type: a.SubjectType, DisplayName: a.SubjectDisplayName}
	if a.State != store.BudgetAssignmentPending {
		subject.ID = &a.SubjectRef
		subject.User = budgetAssignmentUser(a, expandIdentity)
	}
	if expandIdentity {
		subject.Identity = assignmentIdentityOf(a)
	}
	return BudgetAssignmentResponse{
		ID:          a.ID,
		Type:        budgetAssignmentResourceType,
		State:       a.State,
		Subject:     subject,
		Budget:      BudgetAssignmentBudget{ID: a.BudgetID, Name: a.BudgetName},
		CreatedBy:   a.CreatedBy,
		CreatedTime: restTimestamp(a.CreatedTime),
		UpdatedBy:   a.UpdatedBy,
		UpdatedTime: restTimestamp(a.UpdatedTime),
	}
}

func assignmentIdentityOf(a *store.BudgetAssignment) *BudgetAssignmentIdentity {
	switch {
	case a.IdentityType != "":
		return &BudgetAssignmentIdentity{Type: a.IdentityType, Value: a.IdentityValue, Issuer: a.IdentityIssuer}
	case a.SubjectIsSystemUser:
		return &BudgetAssignmentIdentity{Type: store.BudgetIdentitySystem, Value: rbac.SystemUserName(a.SubjectSub)}
	case a.SubjectEmail != nil && !a.SubjectEmailShared:
		return &BudgetAssignmentIdentity{Type: store.BudgetIdentityEmail, Value: *a.SubjectEmail}
	case a.SubjectSub != "":
		return &BudgetAssignmentIdentity{Type: store.BudgetIdentitySub, Value: a.SubjectSub, Issuer: a.SubjectIssuer}
	}
	return nil
}

type UpdateBudgetAssignmentsRequest struct {
	SubjectType string   `json:"subjectType"`
	Add         []string `json:"add"`
	Remove      []string `json:"remove"`
}

type BudgetAssignmentSubjectRef struct {
	ID string `json:"id"`
}

type BudgetAssignmentChangeEntry struct {
	AssignmentID string                     `json:"assignmentId"`
	Subject      BudgetAssignmentSubjectRef `json:"subject"`
}

type BudgetAssignmentMovedEntry struct {
	AssignmentID string                     `json:"assignmentId"`
	Subject      BudgetAssignmentSubjectRef `json:"subject"`
	FromBudget   BudgetAssignmentBudget     `json:"fromBudget"`
}

type BudgetAssignmentUnchangedEntry struct {
	AssignmentID *string                    `json:"assignmentId"`
	Subject      BudgetAssignmentSubjectRef `json:"subject"`
	Reason       string                     `json:"reason"`
}

type UpdateBudgetAssignmentsResponse struct {
	Added     []BudgetAssignmentChangeEntry    `json:"added"`
	Moved     []BudgetAssignmentMovedEntry     `json:"moved"`
	Removed   []BudgetAssignmentChangeEntry    `json:"removed"`
	Unchanged []BudgetAssignmentUnchangedEntry `json:"unchanged"`
}
