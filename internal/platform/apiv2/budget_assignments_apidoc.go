// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (BudgetAssignment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetAssignment puts one user on one budget, in place of their tier's
default. A pending assignment names a person who has not signed in yet by
their identity, and binds to them at their first sign-in.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the assignment's UUID."},
			"Type": {Doc: "Type is always \"budgetAssignment\"."},
			"State": {Doc: `State is concrete for an assignment to a user, or pending while the identity it names has not
signed in yet.`},
			"Subject":     {Doc: "Subject is who the budget is assigned to."},
			"Budget":      {Doc: "Budget is the budget assigned."},
			"CreatedBy":   {Doc: "CreatedBy identifies who made the assignment."},
			"CreatedTime": {Doc: "CreatedTime is when the assignment was made."},
			"UpdatedBy":   {Doc: "UpdatedBy identifies who last moved the assignment."},
			"UpdatedTime": {Doc: "UpdatedTime is when the assignment was last moved."},
		},
	}
}

func (BudgetAssignmentSubject) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BudgetAssignmentSubject is who a budget is assigned to.",
		Fields: map[string]apidoc.Field{
			"Type":        {Doc: "Type is the subject's type; the only value is user."},
			"ID":          {Doc: "ID is the user's UUID; null for a pending assignment, whose person has no user yet."},
			"DisplayName": {Doc: "DisplayName is the user's display name; null for a pending assignment, or when the user has none."},
			"User":        {Doc: "User holds what only a user has; null for a pending assignment."},
			"Identity": {Doc: `Identity is how a create could name the subject: the identity the assignment was made by, else the
user's system name, email or sub. It is null unless the request sets expand=identity, and also when
nothing names the subject, such as an old assignment whose user was deleted.`},
		},
	}
}

func (BudgetAssignmentUser) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetAssignmentUser is the assigned user's email and kind, and, when the
request sets expand=identity, how they sign in.`,
		Fields: map[string]apidoc.Field{
			"Email": {Doc: "Email is the user's email address; null when none is on record."},
			"IsSystemUser": {Doc: `IsSystemUser is true for a service account, which acts for a pipeline rather than a person.

Deprecated: Reason: system users were renamed to service accounts; read isServiceAccount instead. Removal Date: 2027-12-01.`},
			"IsServiceAccount": {Doc: "IsServiceAccount is true for a service account, which acts for a pipeline rather than a person."},
			"EmailShared": {Doc: `EmailShared is true when another user has the same email, so the email names nobody in particular.
It is null unless the request sets expand=identity.`},
			"Sub": {Doc: `Sub is the user's subject identifier at their identity provider. It is null unless the request sets
expand=identity, and also when the user has none.`},
			"Issuer": {Doc: `Issuer is the identity provider that vouches for sub. It is null unless the request sets
expand=identity, and also when the user has no sub.`},
		},
	}
}

func (BudgetAssignmentIdentity) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetAssignmentIdentity names a person the way they sign in, or a service
account by its name.`,
		Fields: map[string]apidoc.Field{
			"Type":   {Doc: "Type is how value names the subject: email, sub (with issuer) or system (a service account's name)."},
			"Value":  {Doc: "Value is the email, the sub, or the service account's name."},
			"Issuer": {Doc: "Issuer is the identity provider that issued the sub; null unless type is sub."},
		},
	}
}

func (BudgetRef) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BudgetRef is a budget named by its id and name.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the budget's UUID."},
			"Name": {Doc: "Name is the budget's name."},
		},
	}
}

func (BudgetAssignmentIdentityRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetAssignmentIdentityRequest names a person by how they sign in, or a
service account by its name.`,
		Fields: map[string]apidoc.Field{
			"Type": {Doc: `Type is how value names the subject: email, sub or system. A service account is named with system,
never by its sub.`},
			"Value": {Doc: "Value is the email, the sub, or the service account's name. An email is matched ignoring case."},
			"Issuer": {Doc: `Issuer is the identity provider that issued the sub; required when type is sub, and refused
otherwise.`},
		},
	}
}

func (CreateBudgetAssignmentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateBudgetAssignmentRequest assigns one subject to one budget at the
subject's tier. Name the subject with exactly one of subjectId and identity.
A subject that already has an assignment returns 409: move it with PATCH.`,
		Fields: map[string]apidoc.Field{
			"SubjectType": {Doc: "SubjectType is the subject's type; the only value is user."},
			"SubjectID":   {Doc: "SubjectID is the UUID of the user to assign."},
			"Identity": {Doc: `Identity names the person to assign by how they sign in. One no user has yet is assigned pending,
and binds at their first sign-in; a service account that does not exist returns 422.`},
			"BudgetID": {Doc: "BudgetID is the UUID of a budget at the subject's tier."},
		},
	}
}

func (UpdateBudgetAssignmentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateBudgetAssignmentRequest moves an assignment to another budget,
re-records the identity it is found by, or both.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the assignment to update."},
			"BudgetID": {Doc: `BudgetID is the UUID of the budget to move the assignment to, at the subject's tier. Leave it out
to keep the budget. Null or "" fails with 422.`},
			"Identity": {Doc: `Identity changes the identity that names the assignment's user, in the form a create by
identity takes and expand=identity returns. It must name the assignment's current user, or the
request fails with 422. A pending assignment's identity cannot change, since it is who the
assignment binds to. Leave it out to keep it. Null fails with 422.`},
			"SubjectType": {Doc: "SubjectType is fixed; including it returns 422."},
			"SubjectID": {Doc: `SubjectID is fixed; including it returns 422. Delete the assignment and create one for the other
subject.`},
		},
	}
}

func (listBudgetAssignmentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `listBudgetAssignmentsRequest selects a page of budget assignments, ordered
by the name the subject is shown by. A query parameter not listed here fails
with 400.`,
		Fields: map[string]apidoc.Field{
			"Page":        {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"BudgetID":    {Doc: "BudgetID limits the list to the assignments to this budget UUID, or returns 400 if it is not a UUID."},
			"SubjectType": {Doc: "SubjectType limits the list to one subject type; the only value is user."},
			"SubjectID": {Doc: `SubjectID limits the list to the assignment of this user UUID, or returns 400 if it is not a UUID.
A hidden service account's assignment is listed only when named here.`},
			"State":  {Doc: "State limits the list to concrete or pending assignments; leave it out to list both."},
			"Search": {Doc: "Search limits the list to assignments whose subject's display name or email contains it, ignoring\ncase. For a caller with `rbac:_:read`, it also matches the identity an assignment was made by."},
			"Expand": {Doc: "Expand names related data to include. The only value is identity, which fills each subject's\nidentity, sub, issuer and emailShared. It needs `rbac:_:read`, and fails with 403 without it. Any\nother value fails with 422."},
		},
	}
}

func (getBudgetAssignmentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getBudgetAssignmentRequest addresses one budget assignment.",
		Fields: map[string]apidoc.Field{
			"ID":     {Doc: "ID is the UUID of the assignment."},
			"Expand": {Doc: "Expand names related data to include. The only value is identity, which needs `rbac:_:read` and\nfails with 403 without it. Any other value fails with 422."},
		},
	}
}

func (UpdateBudgetAssignmentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateBudgetAssignmentsRequest changes which users a budget is assigned to,
all together or not at all. A user to add who is on another budget is moved
here.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the budget."},
			"SubjectType": {Doc: `SubjectType is the type of the subjects named; the only value is user, and the budget must be a user
budget.`},
			"Add": {Doc: "Add are the UUIDs of the users to put on the budget. Together with remove, at most 1,000 ids."},
			"Remove": {Doc: `Remove are the UUIDs of the users to take off the budget, who fall back to their tier's default. A
user on another budget is left there.`},
		},
	}
}

func (BudgetAssignmentChanges) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetAssignmentChanges is what one save of a budget's assignments did for
each user it named, each list in request order.`,
		Fields: map[string]apidoc.Field{
			"Added":     {Doc: "Added are the users put on the budget who had no assignment."},
			"Moved":     {Doc: "Moved are the users moved to the budget from another one."},
			"Removed":   {Doc: "Removed are the users taken off the budget."},
			"Unchanged": {Doc: "Unchanged are the users the save left as they were, each with why."},
		},
	}
}

func (BudgetSubjectRef) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BudgetSubjectRef names a budget subject by its id.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the user's UUID."},
		},
	}
}

func (AddedBudgetAssignment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AddedBudgetAssignment is a user a save put on the budget.",
		Fields: map[string]apidoc.Field{
			"AssignmentID": {Doc: "AssignmentID is the UUID of the new assignment."},
			"Subject":      {Doc: "Subject is the user."},
		},
	}
}

func (MovedBudgetAssignment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "MovedBudgetAssignment is a user a save moved to the budget.",
		Fields: map[string]apidoc.Field{
			"AssignmentID": {Doc: "AssignmentID is the UUID of the assignment, which keeps its id."},
			"Subject":      {Doc: "Subject is the user."},
			"FromBudget":   {Doc: "FromBudget is the budget the user was on, as it was when the save ran."},
		},
	}
}

func (RemovedBudgetAssignment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RemovedBudgetAssignment is a user a save took off the budget.",
		Fields: map[string]apidoc.Field{
			"AssignmentID": {Doc: "AssignmentID is the UUID of the deleted assignment."},
			"Subject":      {Doc: "Subject is the user."},
		},
	}
}

func (UnchangedBudgetAssignment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UnchangedBudgetAssignment is a user a save left as they were.",
		Fields: map[string]apidoc.Field{
			"AssignmentID": {Doc: "AssignmentID is the UUID of the user's assignment; null for a user to remove who has none."},
			"Subject":      {Doc: "Subject is the user."},
			"Reason": {Doc: `Reason is already_assigned (a user to add already on the budget) or not_assigned (a user to remove
who is not on it, often because someone else moved them; the move is kept).`},
		},
	}
}
