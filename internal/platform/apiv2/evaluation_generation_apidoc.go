// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (GenerateDatasetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "GenerateDatasetRequest has an AI write a dataset of examples for an agent.",
		Fields: map[string]apidoc.Field{
			"TargetAgent": {Doc: "TargetAgent is the name of the agent the examples are for. The AI reads its description, instructions, and tools."},
			"NumExamples": {Doc: "NumExamples is how many examples to generate, 1 to 5,000."},
			"Focus":       {Doc: "Focus steers the examples toward a topic. Leave it out or send null for no steer."},
			"Caller":      {Doc: "Caller is who the AI's work acts for."},
		},
	}
}

func (GenerateExamplesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "GenerateExamplesRequest has an AI add examples to a dataset.",
		Fields: map[string]apidoc.Field{
			"DatasetID":   {Doc: "DatasetID is the UUID of the dataset to add examples to."},
			"TargetAgent": {Doc: "TargetAgent is the name of the agent the examples are for. The AI reads its description, instructions, and tools."},
			"NumExamples": {Doc: "NumExamples is how many examples to add, at least 1. The dataset can hold up to 5,000 examples."},
			"Focus":       {Doc: "Focus steers the examples toward a topic. Leave it out or send null for no steer."},
			"Caller":      {Doc: "Caller is who the AI's work acts for."},
		},
	}
}

func (getDatasetOperationRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getDatasetOperationRequest addresses one operation of a dataset.",
		Fields: map[string]apidoc.Field{
			"DatasetID": {Doc: "DatasetID is the UUID of the dataset the operation acted on."},
			"ID":        {Doc: "ID is the operation's UUID."},
		},
	}
}

func (listDatasetOperationsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listDatasetOperationsRequest selects a page of a dataset's operations.",
		Fields: map[string]apidoc.Field{
			"DatasetID": {Doc: "DatasetID is the UUID of the dataset whose operations to list."},
			"Page":      {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort":      {Doc: "Sort orders the list by createdTime, as createdTime:asc or createdTime:desc; leave it out for newest first."},
		},
	}
}
