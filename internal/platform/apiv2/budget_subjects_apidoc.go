// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (BudgetSubject) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetSubject is one subject under budget governance, a user or the whole
mesh, with the budget that applies to it and its active top-up.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the user's UUID, or mesh for the mesh."},
			"Type":        {Doc: "Type is always \"budgetSubject\"."},
			"SubjectType": {Doc: "SubjectType is user or mesh."},
			"DisplayName": {Doc: "DisplayName is the user's display name; null for the mesh, or when the user has none."},
			"User":        {Doc: "User holds what only a user has; null for the mesh."},
			"Budget":      {Doc: "Budget is the budget that applies; null when none does, which only a tier with no default leaves."},
			"TopUp": {Doc: `TopUp is the active top-up, which raises the budget until it expires; null when none is active. It
is shown even while the budget is unlimited, when it has no effect.`},
			"EffectiveAmount": {Doc: `EffectiveAmount is the budget's amount raised by the active top-up, as a decimal string; null while
the budget is unlimited or no budget applies.`},
		},
	}
}

func (BudgetSubjectUser) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetSubjectUser is what a budget subject that is a user has beyond its
name.`,
		Fields: map[string]apidoc.Field{
			"Email": {Doc: "Email is the user's email address; null when none is on record."},
			"IsSystemUser": {Doc: `IsSystemUser is true for a service account, which acts for a pipeline rather than a person.

Deprecated: Reason: system users were renamed to service accounts; read isServiceAccount instead. Removal Date: 2027-12-01.`},
			"IsServiceAccount": {Doc: "IsServiceAccount is true for a service account, which acts for a pipeline rather than a person."},
		},
	}
}

func (listBudgetSubjectsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `listBudgetSubjectsRequest selects a page of the subjects of one tier. A
query parameter not listed here fails with 400.`,
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"SubjectType": {Doc: `SubjectType is the tier to list: user for the users the Users page shows, without hidden service
accounts, or mesh for the mesh, its only subject. Leaving it out fails with 422.`},
			"Search": {Doc: `Search limits the list to users whose display name or email contains it, ignoring case. It fails
with 422 when subjectType is mesh.`},
			"Sort": {Doc: `Sort orders the list by displayName, as displayName:asc or displayName:desc; leave it out to sort A
to Z.`},
		},
	}
}

func (getBudgetSubjectRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getBudgetSubjectRequest addresses one budget subject.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is a user's UUID, or mesh for the mesh. A hidden service account is found too."},
		},
	}
}
