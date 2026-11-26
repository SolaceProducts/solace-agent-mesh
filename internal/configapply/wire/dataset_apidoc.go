// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateDatasetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateDatasetRequest is config apply's copy of apiv2.CreateDatasetRequest.",
		Fields: map[string]apidoc.Field{
			"Name":         {Doc: "Name is the dataset's display name (unique per platform)."},
			"Description":  {Doc: "Description summarises what the dataset's examples cover."},
			"ExamplesFile": {Doc: "ExamplesFile names a CSV sidecar (path relative to this YAML) holding\nthe dataset's examples. Reconciled by `sam config apply` via separate\nexample API calls — it is never sent on the dataset create/update\nrequest. Columns: sequence_number, prompt, expected_response (RFC 4180\nquoting). `sam config pull` writes it back as <name>.examples.csv."},
		},
	}
}

func (UpdateDatasetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateDatasetRequest is config apply's copy of apiv2.UpdateDatasetRequest,
a PATCH body. The dataset's ID goes in the path, not the body.`,
	}
}

func (DatasetResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DatasetResponse is config apply's copy of apiv2.Dataset.",
	}
}

func (CreateExampleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateExampleRequest is config apply's copy of apiv2.CreateExampleRequest.
The dataset's ID goes in the path, not the body.`,
	}
}

func (UpdateExampleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateExampleRequest is config apply's copy of apiv2.UpdateExampleRequest,
a PATCH body. The example's ID goes in the path, not the body.`,
	}
}

func (ExampleResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ExampleResponse is config apply's copy of apiv2.Example.",
	}
}
