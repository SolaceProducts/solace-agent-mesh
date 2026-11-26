// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (BudgetTopUp) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `BudgetTopUp is a budget subject's top-up: a temporary amount added to its
budget until the end of the budget period it was set in. Every field is null
while the subject has no active top-up.`,
		Fields: map[string]apidoc.Field{
			"Amount":      {Doc: "Amount is how much the top-up raises the budget, as a decimal string of dollars."},
			"ExpiresTime": {Doc: "ExpiresTime is when the top-up stops applying: the end of the budget period it was last set in."},
			"CreatedBy":   {Doc: "CreatedBy identifies who first set the top-up."},
			"CreatedTime": {Doc: "CreatedTime is when the top-up was first set."},
			"UpdatedBy":   {Doc: "UpdatedBy identifies who last set the top-up's amount."},
			"UpdatedTime": {Doc: "UpdatedTime is when the top-up's amount was last set."},
		},
	}
}

func (SetBudgetTopUpRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "SetBudgetTopUpRequest sets a subject's top-up for the current budget period.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is a user's UUID, or mesh for the mesh. A hidden service account is found too."},
			"Amount": {Doc: `Amount is how much to raise the budget by until the end of the current budget period, as a decimal
string of dollars, at most 100000.00 and with at most 2 decimal places. It replaces the amount of
an active top-up. 0 removes the active top-up. The budget that applies must not be unlimited.`},
		},
	}
}
