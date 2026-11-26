// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Budget) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Budget is a spending limit for one tier of subjects: the whole mesh, or each
user it applies to.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the budget's UUID."},
			"Type": {Doc: "Type is always \"budget\"."},
			"Name": {Doc: "Name is the budget's name, unique across budgets."},
			"SubjectType": {Doc: `SubjectType is the tier the budget is for: mesh (the whole deployment, which has exactly one
budget) or user (each user it applies to). It is fixed when the budget is created.`},
			"Amount": {Doc: "Amount is the limit per budget period as a decimal string of dollars; null for an unlimited budget."},
			"WarningPercentage": {Doc: `WarningPercentage is the share of the amount, from 1 to 100, at which a warning is raised; null for
no warning. An unlimited budget has none.`},
			"IsDefault": {Doc: `IsDefault is true for the budget that applies to every subject of its tier with no assignment of
its own. The mesh budget is always its tier's default.`},
			"AssignedCount": {Doc: `AssignedCount is how many users are assigned the budget, not counting hidden service accounts, which
hiddenAssignedCount counts.`},
			"HiddenAssignedCount": {Doc: `HiddenAssignedCount is how many hidden service accounts are assigned the budget. A hidden service account
is one Agent Mesh keeps off the Users page.`},
			"PendingCount": {Doc: `PendingCount is how many people who have not signed in yet are assigned the budget by their
identity.`},
			"CreatedBy":   {Doc: "CreatedBy identifies who created the budget."},
			"CreatedTime": {Doc: "CreatedTime is when the budget was created."},
			"UpdatedBy":   {Doc: "UpdatedBy identifies who last changed the budget."},
			"UpdatedTime": {Doc: "UpdatedTime is when the budget was last changed."},
		},
	}
}

func (CreateBudgetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateBudgetRequest creates a budget. A new user budget is never its tier's
default; make it one with PUT /budgets/{id}/makeDefault.`,
		Fields: map[string]apidoc.Field{
			"Name":        {Doc: "Name is the budget's name: 3 to 255 characters without control characters, unique across budgets."},
			"SubjectType": {Doc: "SubjectType is the tier the budget is for: mesh or user. Only one mesh budget can exist."},
			"Amount": {Doc: `Amount is the limit per budget period as a decimal string of dollars with at most 2 decimal
places; send null for an unlimited budget. Leaving it out returns 422.`},
			"WarningPercentage": {Doc: `WarningPercentage is the share of the amount, from 1 to 100, at which a warning is raised; leave it
out or send null for none. An unlimited budget cannot have one.`},
		},
	}
}

func (UpdateBudgetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateBudgetRequest changes only the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the budget to update."},
			"Name": {Doc: `Name is the budget's new name, 3 to 255 characters. Leave it out to keep it. Null or "" fails
with 422.`},
			"Amount": {Doc: `Amount is the new limit as a decimal string of dollars with at most 2 decimal places; leave it out
to keep it, or send null to make the budget unlimited.`},
			"WarningPercentage": {Doc: `WarningPercentage is the new warning share, from 1 to 100; leave it out to keep it, or send null to
remove the warning.`},
			"SubjectType": {Doc: "SubjectType is fixed when the budget is created; including it returns 422."},
			"IsDefault":   {Doc: "IsDefault cannot be set here; including it returns 422. Use PUT /budgets/{id}/makeDefault."},
		},
	}
}

func (listBudgetsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `listBudgetsRequest selects a page of budgets. A query parameter not listed
here fails with 400.`,
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"SubjectType": {Doc: `SubjectType limits the list to one tier: mesh or user; leave it out to list both. Any other value
returns 422, and an empty one 400.`},
			"Search": {Doc: "Search limits the list to budgets whose name contains it, ignoring case."},
			"Sort": {Doc: `Sort orders the list by name or amount, as field:asc or field:desc; leave it out to sort by name,
A to Z. By amount, an unlimited budget counts as larger than any amount.`},
		},
	}
}

func (getBudgetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getBudgetRequest addresses one budget.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the budget's UUID."},
		},
	}
}
