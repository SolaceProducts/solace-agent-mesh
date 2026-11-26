// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Example) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Example is one test case in a dataset. It holds a prompt for the agent, a
structured input for a workflow, or both. It can also hold the expected
answer, an input file for the agent, and an expected file for the evaluators.`,
		Fields: map[string]apidoc.Field{
			"ID":               {Doc: "ID is the example's UUID."},
			"Type":             {Doc: "Type is always \"example\"."},
			"DatasetID":        {Doc: "DatasetID is the UUID of the dataset the example belongs to."},
			"SequenceNumber":   {Doc: "SequenceNumber orders the example within its dataset, from 1. Examples are listed and run in this order."},
			"Prompt":           {Doc: "Prompt is the message the agent is given. Empty for an example that has only inputData."},
			"ExpectedResponse": {Doc: "ExpectedResponse is the answer the evaluators compare the agent's answer to, or null when the example has none."},
			"InputData":        {Doc: "InputData is the structured input a workflow example is given, a JSON object kept exactly as it was sent, or null when the example has none."},
			"ExpectedPath":     {Doc: "ExpectedPath is how a workflow example's trials are expected to run, which the Workflow Path evaluator checks, or null when the example has none."},
			"ExpectedFacts":    {Doc: "ExpectedFacts is what the example's response must and must not say, which the Output Facts evaluator checks, or null when the example has none."},
			"InputArtifact":    {Doc: "InputArtifact is the file sent to the agent with the prompt, or null when the example has none."},
			"ExpectedArtifact": {Doc: "ExpectedArtifact is the text file the evaluators compare the agent's answer to, never sent to the agent, or null when the example has none."},
			"CreatedTime":      {Doc: "CreatedTime is when the example was created."},
			"UpdatedTime":      {Doc: "UpdatedTime is when the example last changed."},
		},
	}
}

func (ExampleArtifact) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ExampleArtifact describes a file stored on an example.",
		Fields: map[string]apidoc.Field{
			"Filename":    {Doc: "Filename is the file's name."},
			"MimeType":    {Doc: "MimeType is the file's media type."},
			"Size":        {Doc: "Size is the file's size in bytes."},
			"Version":     {Doc: "Version is the stored version of the file, from 0. Replacing the file starts again at 0."},
			"ContentHash": {Doc: "ContentHash is the SHA-256 of the file's content, in hexadecimal."},
		},
	}
}

func (ExpectedPath) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ExpectedPath is how a workflow example's trials are expected to run: how
named nodes end, and which tools are called or not called. The Workflow Path
evaluator scores a trial by the share of these expectations it meets. Holds
1 to 100 entries. Only a trial the workflow completes is scored, so a failed
node is graded only when the workflow continues past the failure. A trial
the workflow ends early is recorded with its reason and not scored. A trial
the evaluator cannot grade, because the experiment targets an agent or the
trial's events were not captured to the workflow's end, gets an errored
score. Errored scores are left out of the averages, and the run ends with
warnings.`,
		Fields: map[string]apidoc.Field{
			"Nodes": {Doc: "Nodes are the workflow nodes the path names, each with how it is expected to end, in node id order. Empty when the path names none."},
			"Tools": {Doc: "Tools are the tools the path names, each with whether it is expected to be called, in tool name order. Empty when the path names none."},
		},
	}
}

func (ExpectedFacts) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ExpectedFacts is what an example's response must and must not say, which the
Output Facts evaluator scores a trial against. A value counts as said when it
appears as a whole phrase in the response's text. Matching ignores case,
spacing, how accents are encoded, and where in a JSON response a value sits.
A response that is one JSON value, alone or as the whole body of a single
code fence, is read as its values only, so its keys and nulls never match.
With any other text around it, the response is read as text. A value written
as a number also matches a JSON number of the same value, so 19.90 matches
19.9. A workflow's output numbers arrive at double precision. A phrase inside
a longer word, number, or identifier does not count, so 5 is not said by 1.5
or P-5. In a script written without spaces, such as Chinese, Japanese, or
Thai, a phrase is found anywhere in the text. Holds 1 to 100 values across
both lists, each up to 500 characters. Each value can appear only once, in
one list.`,
		Fields: map[string]apidoc.Field{
			"Required":  {Doc: "Required are the values the response must contain, in the order given. Empty when there are none."},
			"Forbidden": {Doc: "Forbidden are the values the response must not contain, in the order given. Empty when there are none."},
		},
	}
}

func (ExpectedNode) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ExpectedNode is how one workflow node is expected to end.",
		Fields: map[string]apidoc.Field{
			"NodeID":      {Doc: "NodeID is the node's id in the workflow. A path names each node once. Name a node of a nested sub-workflow under the node that ran it, as \"outer ▸ inner\". Name a map or loop by its own node. The node it runs for each item or iteration publishes no events of its own, so assert on the tools that node calls instead."},
			"Expectation": {Doc: "Expectation is how the node is expected to end: ran (it started, whatever its result), not_ran (it never started), succeeded, failed, or skipped (the workflow decided not to run it, such as a branch not taken)."},
		},
	}
}

func (ExpectedTool) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ExpectedTool is whether one tool is expected to be called.",
		Fields: map[string]apidoc.Field{
			"ToolName":    {Doc: "ToolName is the tool's name. A path names each tool once."},
			"Expectation": {Doc: "Expectation is whether the tool is called: called (at least once anywhere in the trial, nested sub-workflows and delegated peer agents included) or not_called. A delegation to a peer agent is a call to the tool peer_<agent name>."},
		},
	}
}

func (ImportExamplesResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ImportExamplesResult is the outcome of an import.",
		Fields: map[string]apidoc.Field{
			"Created": {Doc: "Created is how many examples the import added."},
			"Skipped": {Doc: "Skipped is how many rows had neither a prompt nor an inputData and were left out."},
			"Dropped": {Doc: "Dropped is how many rows were left out because the dataset reached its limit."},
			"Limit":   {Doc: "Limit is the most examples a dataset can hold."},
		},
	}
}

func (CreateExampleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateExampleRequest adds an example to a dataset.",
		Fields: map[string]apidoc.Field{
			"DatasetID":        {Doc: "DatasetID is the UUID of the dataset to add the example to."},
			"Prompt":           {Doc: "Prompt is the message the agent is given. Cannot be empty unless inputData is set."},
			"ExpectedResponse": {Doc: "ExpectedResponse is the answer to compare the agent's answer to. Leave it out or send null for none."},
			"InputData":        {Doc: "InputData is the structured input for a workflow example: a JSON object of up to 256 KiB, kept exactly as sent. Leave it out or send null for none. Setting it fails with 422 unless workflow evaluations are turned on."},
			"ExpectedPath":     {Doc: "ExpectedPath is how the example's trials are expected to run, for the Workflow Path evaluator. Leave it out or send null for none. Setting it fails with 422 unless workflow evaluations are turned on."},
			"ExpectedFacts":    {Doc: "ExpectedFacts is what the example's response must and must not say, for the Output Facts evaluator. Leave it out or send null for none. Setting it fails with 422 unless workflow evaluations are turned on."},
			"SequenceNumber":   {Doc: "SequenceNumber places the example in the dataset, from 1. Leave it out to add the example after the others. If another example in the dataset has the number, the request fails with 422."},
		},
	}
}

func (UpdateExampleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateExampleRequest changes only the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID":               {Doc: "ID is the UUID of the example to update."},
			"Prompt":           {Doc: "Prompt is the message the agent is given. Leave it out to keep it. Null fails with 422, and so does an empty string unless the example has inputData after the update."},
			"ExpectedResponse": {Doc: "ExpectedResponse is the answer to compare the agent's answer to. Leave it out to keep it, or send null to clear it."},
			"InputData":        {Doc: "InputData replaces the structured input of a workflow example with a JSON object of up to 256 KiB. Leave it out to keep it, or send null to clear it. An example with no prompt cannot clear it. Setting it fails with 422 unless workflow evaluations are turned on, but clearing it does not."},
			"ExpectedPath":     {Doc: "ExpectedPath replaces how the example's trials are expected to run. Leave it out to keep it, or send null to clear it. Setting it fails with 422 unless workflow evaluations are turned on, but clearing it does not."},
			"ExpectedFacts":    {Doc: "ExpectedFacts replaces what the example's response must and must not say. Leave it out to keep it, or send null to clear it. Setting it fails with 422 unless workflow evaluations are turned on, but clearing it does not."},
			"SequenceNumber":   {Doc: "SequenceNumber is fixed when the example is created. If the body includes it, the request fails with 422."},
			"DatasetID":        {Doc: "DatasetID is fixed when the example is created. If the body includes it, the request fails with 422."},
		},
	}
}

func (listExamplesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listExamplesRequest selects a page of a dataset's examples.",
		Fields: map[string]apidoc.Field{
			"DatasetID": {Doc: "DatasetID is the UUID of the dataset."},
			"Page":      {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort":      {Doc: "Sort orders the list by sequenceNumber, createdTime or updatedTime, as field:asc or field:desc; leave it out for sequence order."},
			"Content":   {Doc: "Content limits the list to examples whose prompt or expected response contains this text, ignoring case."},
		},
	}
}

func (importExamplesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "importExamplesRequest uploads a file of examples to add to a dataset.",
		Fields: map[string]apidoc.Field{
			"DatasetID": {Doc: "DatasetID is the UUID of the dataset to add the examples to."},
			"File":      {Doc: "File is a .csv or .json file of examples."},
		},
	}
}

func (exportExamplesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "exportExamplesRequest names a dataset and a file format.",
		Fields: map[string]apidoc.Field{
			"DatasetID": {Doc: "DatasetID is the UUID of the dataset."},
			"Format":    {Doc: "Format is the file format: csv or json. Defaults to csv."},
		},
	}
}

func (exampleIDRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "exampleIDRequest addresses the example a file belongs to.",
		Fields: map[string]apidoc.Field{
			"ExampleID": {Doc: "ExampleID is the example's UUID."},
		},
	}
}

func (setExampleArtifactRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "setExampleArtifactRequest uploads the file an example holds in one slot.",
		Fields: map[string]apidoc.Field{
			"ExampleID":   {Doc: "ExampleID is the example's UUID."},
			"File":        {Doc: "File is the file to store. It replaces the one in the slot."},
			"Description": {Doc: "Description says what the file is for. Leave it out for none."},
		},
	}
}

func (exampleArtifactRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "exampleArtifactRequest names one file of an example.",
		Fields: map[string]apidoc.Field{
			"ExampleID": {Doc: "ExampleID is the example's UUID."},
			"Filename":  {Doc: "Filename is the file's name as the example lists it."},
			"Version":   {Doc: "Version is the version of the file to get, which must be the one the example lists. Leave it out to get that version. If it names another version, the request fails with 404."},
		},
	}
}
