// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

const (
	BudgetSubjectMesh = "mesh"
	BudgetSubjectUser = "user"
)

const BudgetMeshSubjectRef = "mesh"

const (
	SeededMeshBudgetID        = "00000000-0000-0000-0000-00000000b001"
	SeededDefaultUserBudgetID = "00000000-0000-0000-0000-00000000b002"
)

type Budget struct {
	ID                  string `db:"id"`
	Name                string `db:"name"`
	NameLower           string `db:"name_lower"`
	SubjectType         string `db:"subject_type"`
	Amount              *int64 `db:"amount"`
	WarnPercentage      *int   `db:"warn_percentage"`
	IsDefault           bool   `db:"is_default"`
	CreatedBy           string `db:"created_by"`
	UpdatedBy           string `db:"updated_by"`
	CreatedTime         int64  `db:"created_time"`
	UpdatedTime         int64  `db:"updated_time"`
	AssignedCount       int64  `db:"assigned_count"`
	HiddenAssignedCount int64  `db:"hidden_assigned_count"`
	PendingCount        int64  `db:"pending_count"`
}

type BudgetUpdate struct {
	ID                  string
	Name                string
	Amount              *int64
	WarnPercentage      *int
	UpdatedBy           string
	UpdatedTime         int64
	ExpectedUpdatedTime int64
}

type BudgetFilter struct {
	SubjectType string
	Name        string
	DefaultOnly bool
	SortBy      BudgetSort
	Descending  bool
	Limit       int
	Offset      int
}

type BudgetSort string

const (
	BudgetSortName   BudgetSort = "name"
	BudgetSortAmount BudgetSort = "amount"
)

const (
	BudgetAssignmentConcrete = "concrete"
	BudgetAssignmentPending  = "pending"
)

const (
	BudgetIdentityEmail  = PendingSubjectEmail
	BudgetIdentitySub    = PendingSubjectSub
	BudgetIdentitySystem = "system"
)

type BudgetAssignment struct {
	ID                  string  `db:"id"`
	State               string  `db:"state"`
	SubjectType         string  `db:"subject_type"`
	SubjectRef          string  `db:"subject_ref"`
	BudgetID            string  `db:"budget_id"`
	IdentityType        string  `db:"identity_type"`
	IdentityValue       string  `db:"identity_value"`
	IdentityIssuer      string  `db:"identity_issuer"`
	BudgetName          string  `db:"budget_name"`
	SubjectDisplayName  *string `db:"subject_display_name"`
	SubjectEmail        *string `db:"subject_email"`
	SubjectEmailShared  bool    `db:"subject_email_shared"`
	SubjectSub          string  `db:"subject_sub"`
	SubjectIssuer       string  `db:"subject_issuer"`
	SubjectIsSystemUser bool    `db:"subject_is_system_user"`
	CreatedBy           string  `db:"created_by"`
	UpdatedBy           string  `db:"updated_by"`
	CreatedTime         int64   `db:"created_time"`
	UpdatedTime         int64   `db:"updated_time"`
}

type BudgetIdentity struct {
	Type   string `db:"identity_type"`
	Value  string `db:"identity_value"`
	Issuer string `db:"issuer"`
}

type BudgetAssignmentUpdate struct {
	BudgetID string
	Identity *BudgetIdentity
	Actor    string
	NowMs    int64
}

type BudgetAssignmentUpdated struct {
	PreviousBudgetID string
	PreviousIdentity BudgetIdentity
	Moved            bool
	Rerecorded       bool
}

type BudgetPendingAssignment struct {
	ID            string `db:"id"`
	SubjectType   string `db:"subject_type"`
	IdentityType  string `db:"identity_type"`
	IdentityValue string `db:"identity_value"`
	Issuer        string `db:"issuer"`
	BudgetID      string `db:"budget_id"`
	CreatedBy     string `db:"created_by"`
	UpdatedBy     string `db:"updated_by"`
	CreatedTime   int64  `db:"created_time"`
	UpdatedTime   int64  `db:"updated_time"`
}

type BudgetAssignmentFilter struct {
	HiddenSubjectRefs []string
	BudgetID          string
	SubjectType       string
	SubjectRef        string
	State             string
	Search            string
	SearchIdentity    bool
	Limit             int
	Offset            int
}

type BudgetBindResult struct {
	Bound        *BudgetPendingAssignment
	AssignmentID string
	Superseded   []BudgetPendingAssignment
}

type UserBudgetSubject struct {
	ID           string  `db:"id"`
	DisplayName  *string `db:"display_name"`
	Email        *string `db:"email"`
	IsSystemUser bool    `db:"is_system_user"`
	AssignmentID *string `db:"assignment_id"`
	BudgetID     *string `db:"budget_id"`
	BudgetName   *string `db:"budget_name"`
	BudgetAmount *int64  `db:"budget_amount"`

	TopUpID          *string `db:"top_up_id"`
	TopUpAmount      *int64  `db:"top_up_amount"`
	TopUpExpiresTime *int64  `db:"top_up_expires_time"`
}

type UserBudgetSubjectFilter struct {
	NowMs            int64
	Search           string
	IDs              []string
	HiddenSystemSubs []string
	Descending       bool
	Limit            int
	Offset           int
}

const (
	BudgetAssignmentAdded     = "added"
	BudgetAssignmentMoved     = "moved"
	BudgetAssignmentRemoved   = "removed"
	BudgetAssignmentUnchanged = "unchanged"

	BudgetAssignmentAlreadyAssigned = "already_assigned"
	BudgetAssignmentNotAssigned     = "not_assigned"
)

type BudgetAssignmentChanges struct {
	BudgetID    string
	SubjectType string
	Add         []string
	Remove      []string
	Actor       string
	NowMs       int64
}

type BudgetAssignmentChange struct {
	Outcome        string
	SubjectRef     string
	AssignmentID   string
	FromBudgetID   string
	FromBudgetName string
	Reason         string
	Superseded     []BudgetPendingAssignment
}

type BudgetTopUp struct {
	ID                  string  `db:"id"`
	SubjectType         string  `db:"subject_type"`
	SubjectRef          string  `db:"subject_ref"`
	Amount              int64   `db:"amount"`
	ExpiresTime         int64   `db:"expires_time"`
	SubjectDisplayName  *string `db:"subject_display_name"`
	SubjectEmail        *string `db:"subject_email"`
	SubjectIsSystemUser bool    `db:"subject_is_system_user"`
	CreatedBy           string  `db:"created_by"`
	UpdatedBy           string  `db:"updated_by"`
	CreatedTime         int64   `db:"created_time"`
	UpdatedTime         int64   `db:"updated_time"`
}

type BudgetTopUpGrant struct {
	SubjectType string
	SubjectRef  string
	Amount      int64
	ExpiresTime int64
	Actor       string
	NowMs       int64
}

type BudgetTopUpGranted struct {
	TopUp          *BudgetTopUp
	Created        bool
	PreviousAmount *int64
}
