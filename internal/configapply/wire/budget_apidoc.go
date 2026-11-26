// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateBudgetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateBudgetRequest is config apply's copy of apiv2.CreateBudgetRequest.
Amount is always sent: null makes the budget unlimited.`,
	}
}

func (UpdateBudgetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateBudgetRequest is config apply's copy of apiv2.UpdateBudgetRequest, a
PATCH body. The budget's ID goes in the path, not the body. SubjectType and
IsDefault are read-only on the platform and never sent.`,
	}
}

func (BudgetResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BudgetResponse is config apply's copy of apiv2.Budget.",
	}
}

func (CreateBudgetAssignmentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateBudgetAssignmentRequest is config apply's copy of
apiv2.CreateBudgetAssignmentRequest.`,
	}
}

func (UpdateBudgetAssignmentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateBudgetAssignmentRequest is config apply's copy of
apiv2.UpdateBudgetAssignmentRequest, a PATCH body. The assignment's ID goes
in the path, not the body. SubjectType and SubjectID are fixed on the
platform and never sent.`,
	}
}

func (BudgetAssignmentResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "BudgetAssignmentResponse is config apply's copy of apiv2.BudgetAssignment.",
	}
}
