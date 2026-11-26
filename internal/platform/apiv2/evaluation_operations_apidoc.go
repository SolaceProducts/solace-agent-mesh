// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (getExperimentOperationRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getExperimentOperationRequest addresses one operation of an experiment.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment the operation acts on."},
			"ID":           {Doc: "ID is the operation's UUID, the runGroupId of the runs it started."},
		},
	}
}

func (listExperimentOperationsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listExperimentOperationsRequest selects a page of an experiment's operations.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment whose operations to list."},
			"Page":         {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}
