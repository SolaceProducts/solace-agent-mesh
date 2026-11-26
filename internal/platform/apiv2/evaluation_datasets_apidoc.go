// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Dataset) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Dataset is a set of examples, each a prompt and optionally the answer
expected, that an experiment runs an agent against.`,
		Fields: map[string]apidoc.Field{
			"ID":                    {Doc: "ID is the dataset's UUID."},
			"Type":                  {Doc: "Type is always \"dataset\"."},
			"Name":                  {Doc: "Name is the dataset's unique name."},
			"Description":           {Doc: "Description says what the examples cover, or is null when it has none."},
			"IsDefault":             {Doc: "IsDefault is true for the sample dataset that Agent Mesh creates at installation."},
			"ExampleCount":          {Doc: "ExampleCount is how many examples the dataset holds."},
			"InputDataExampleCount": {Doc: "InputDataExampleCount is how many of the dataset's examples carry inputData, the structured input a workflow is given. 0 when none do."},
			"ExperimentCount":       {Doc: "ExperimentCount is how many experiments run the dataset."},
			"CreatedBy":             {Doc: "CreatedBy identifies the user who created the dataset."},
			"UpdatedBy":             {Doc: "UpdatedBy identifies the user who last changed the dataset."},
			"CreatedTime":           {Doc: "CreatedTime is when the dataset was created."},
			"UpdatedTime":           {Doc: "UpdatedTime is when the dataset or one of its examples last changed."},
		},
	}
}

func (CreateDatasetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateDatasetRequest creates an empty dataset. If the name is taken, the request fails with 409.",
		Fields: map[string]apidoc.Field{
			"Name":        {Doc: "Name is the dataset's unique name. Up to 255 bytes."},
			"Description": {Doc: "Description says what the examples cover. Up to 1,000 bytes."},
		},
	}
}

func (UpdateDatasetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateDatasetRequest changes only the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the UUID of the dataset to update."},
			"Name":        {Doc: "Name is the dataset's unique name. Up to 255 bytes. Leave it out to keep it. Null or an empty string fails with 422."},
			"Description": {Doc: "Description says what the examples cover. Up to 1,000 bytes. Leave it out to keep it, or send null to clear it."},
		},
	}
}

func (listDatasetsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listDatasetsRequest selects a page of datasets.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort": {Doc: "Sort orders the list by name, createdTime, updatedTime, exampleCount or experimentCount, as field:asc or field:desc; leave it out for newest first."},
			"Name": {Doc: "Name limits the list to datasets whose name contains this text, ignoring case."},
		},
	}
}
