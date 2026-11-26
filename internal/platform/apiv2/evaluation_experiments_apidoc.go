// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Experiment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Experiment runs an agent against every example in a dataset and scores each
answer with the experiment's evaluators.`,
		Fields: map[string]apidoc.Field{
			"ID":                 {Doc: "ID is the experiment's UUID."},
			"Type":               {Doc: "Type is always \"experiment\"."},
			"Name":               {Doc: "Name is the experiment's unique name."},
			"Description":        {Doc: "Description says what the experiment measures, or is null when it has none."},
			"DatasetID":          {Doc: "DatasetID is the UUID of the dataset whose examples the experiment runs, or null when that dataset was deleted."},
			"DatasetName":        {Doc: "DatasetName is the dataset's name, or null when the dataset was deleted."},
			"DatasetUpdatedTime": {Doc: "DatasetUpdatedTime is when the dataset last changed, or null when the dataset was deleted."},
			"TargetType":         {Doc: "TargetType is what the experiment evaluates: agent or workflow."},
			"TargetAgent":        {Doc: "TargetAgent is the name of the agent under evaluation. Empty for a workflow experiment, whose targetType is workflow."},
			"TargetWorkflow":     {Doc: "TargetWorkflow is the name of the workflow under evaluation, or null for an agent experiment."},
			"Models":             {Doc: "Models are the models the agent runs under, one run each. Empty when the agent runs under its own model."},
			"RunsPerExample":     {Doc: "RunsPerExample is how many times each example runs, 1 to 10."},
			"MaxWorkers":         {Doc: "MaxWorkers is how many examples run at once, 1 to 20."},
			"EvaluatorIDs":       {Doc: "EvaluatorIDs are the UUIDs of the evaluators that score each answer."},
			"PrimaryEvaluatorID": {Doc: "PrimaryEvaluatorID is the UUID of the evaluator whose score is the headline result, or null when none is chosen."},
			"IsDefault":          {Doc: "IsDefault is true for the sample experiment that Agent Mesh creates at installation."},
			"RunAs":              {Doc: "RunAs is the service account the experiment's runs execute as, as system:<name>, or null for the built-in evaluation service account."},
			"LatestRunStatus":    {Doc: "LatestRunStatus is the status of the experiment's most recent run: pending, running, completed, completed_with_warnings, failed, or cancelled. Null when the experiment has never run."},
			"LatestRunTime":      {Doc: "LatestRunTime is when the experiment's most recent run started, or null when it has never run."},
			"CreatedBy":          {Doc: "CreatedBy identifies the user who created the experiment: their email, or their user ID when no email is known."},
			"UpdatedBy":          {Doc: "UpdatedBy identifies the user who last changed the experiment, in the same form as createdBy."},
			"CreatedTime":        {Doc: "CreatedTime is when the experiment was created."},
			"UpdatedTime":        {Doc: "UpdatedTime is when the experiment was last changed."},
		},
	}
}

func (ExperimentModel) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ExperimentModel is one model an experiment runs its agent under.",
		Fields: map[string]apidoc.Field{
			"ModelConfigID": {Doc: "ModelConfigID is the UUID of the model configuration to run under, or null for a model named by alias alone, as the sample experiment's is."},
			"Model":         {Doc: "Model is the provider's name for the model, or \"\" for a model named by modelConfigId alone."},
			"Alias":         {Doc: "Alias is the model configuration's alias, or \"\" for a model named by modelConfigId alone."},
		},
	}
}

func (CreateExperimentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateExperimentRequest creates an experiment. If the name is taken, the request fails with 409.",
		Fields: map[string]apidoc.Field{
			"Name":               {Doc: "Name is the experiment's unique name. Up to 255 characters."},
			"Description":        {Doc: "Description says what the experiment measures."},
			"DatasetID":          {Doc: "DatasetID is the UUID of the dataset whose examples the experiment runs."},
			"TargetAgent":        {Doc: "TargetAgent is the name of the agent to evaluate. Up to 255 characters. Send it or targetWorkflow, not both."},
			"TargetWorkflow":     {Doc: "TargetWorkflow is the name of the workflow to evaluate, in place of targetAgent. Up to 255 characters. Refused unless workflow evaluations are turned on."},
			"Models":             {Doc: "Models are the models to run the agent under, one run each. Up to 3. Leave it out to use the agent's own model."},
			"RunsPerExample":     {Doc: "RunsPerExample is how many times each example runs, 1 to 10. Defaults to 1."},
			"MaxWorkers":         {Doc: "MaxWorkers is how many examples run at once, 1 to 20. Defaults to 4."},
			"EvaluatorIDs":       {Doc: "EvaluatorIDs are the UUIDs of 1 to 5 evaluators to score each answer with."},
			"PrimaryEvaluatorID": {Doc: "PrimaryEvaluatorID is the UUID of the evaluator whose score is the headline result. Must be one of evaluatorIds."},
			"RunAs":              {Doc: "RunAs names the service account the runs execute as. A bare name becomes system:<name>. Leave it out for the built-in evaluation service account. Built-in service accounts other than the evaluation one are not allowed, because the experiment would share their tool credentials with the Agent Mesh features that run as them."},
		},
	}
}

func (UpdateExperimentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateExperimentRequest changes only the fields it includes.",
		Fields: map[string]apidoc.Field{
			"ID":                 {Doc: "ID is the UUID of the experiment to update."},
			"Name":               {Doc: "Name is the experiment's unique name. Leave it out to keep it. Null or an empty string fails with 422."},
			"Description":        {Doc: "Description says what the experiment measures. Leave it out to keep it, or send null to clear it."},
			"DatasetID":          {Doc: "DatasetID is the UUID of the dataset to run. Leave it out to keep it. Null fails with 422."},
			"TargetAgent":        {Doc: "TargetAgent is the agent to evaluate. Leave it out to keep the target. Null or an empty string fails with 422. Sending it on a workflow experiment makes it an agent experiment."},
			"TargetWorkflow":     {Doc: "TargetWorkflow is the workflow to evaluate, in place of targetAgent. Leave it out to keep the target. Null or an empty string fails with 422. Sending it on an agent experiment makes it a workflow experiment. Send targetAgent or targetWorkflow, not both."},
			"Models":             {Doc: "Models replaces the models, up to 3. Leave it out to keep them, or send null to run the agent's own model."},
			"RunsPerExample":     {Doc: "RunsPerExample is how many times each example runs, 1 to 10. Leave it out to keep it. Null fails with 422."},
			"MaxWorkers":         {Doc: "MaxWorkers is how many examples run at once, 1 to 20. Leave it out to keep it. Null fails with 422."},
			"EvaluatorIDs":       {Doc: "EvaluatorIDs replaces the evaluators, 1 to 5 UUIDs. Leave it out to keep them. Null fails with 422."},
			"PrimaryEvaluatorID": {Doc: "PrimaryEvaluatorID is the UUID of the headline evaluator, one of the evaluators. Leave it out to keep it, or send null to clear it."},
			"RunAs":              {Doc: "RunAs names the service account the runs execute as. Leave it out to keep it, or send null for the built-in evaluation service account."},
		},
	}
}

func (listExperimentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listExperimentsRequest selects a page of experiments.",
		Fields: map[string]apidoc.Field{
			"Page":        {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort":        {Doc: "Sort orders the list by name or createdTime, as field:asc or field:desc; leave it out for newest first."},
			"DatasetID":   {Doc: "DatasetID limits the list to experiments that run this dataset UUID."},
			"EvaluatorID": {Doc: "EvaluatorID limits the list to experiments this evaluator UUID scores."},
			"TargetAgent": {Doc: "TargetAgent limits the list to experiments that evaluate this agent name."},
		},
	}
}
