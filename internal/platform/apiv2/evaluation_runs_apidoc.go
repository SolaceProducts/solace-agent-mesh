// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Run) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Run is one model's execution of an experiment: the agent answers every
example in its dataset, and the evaluators score each answer. A trigger
starts one run per model, and those runs share a runGroupId.`,
		Fields: map[string]apidoc.Field{
			"ID":                {Doc: "ID is the run's UUID."},
			"Type":              {Doc: "Type is always \"run\"."},
			"ExperimentID":      {Doc: "ExperimentID is the UUID of the experiment the run belongs to."},
			"RunGroupID":        {Doc: "RunGroupID is the UUID shared by every run one trigger started, one per model."},
			"Status":            {Doc: "Status is the run's state: pending, running, completed, completed_with_warnings, failed, or cancelled."},
			"ProgressCurrent":   {Doc: "ProgressCurrent is how many trials have finished, a trial being one example answered once."},
			"ProgressTotal":     {Doc: "ProgressTotal is how many trials the run has in all: the dataset's examples times the experiment's runsPerExample."},
			"ErrorMessage":      {Doc: "ErrorMessage says why the run failed or was stopped at its deadline. Null otherwise."},
			"SummaryScores":     {Doc: "SummaryScores is the mean score per evaluator over the run's trials. Empty until the run completes. Stays empty for a run that was canceled or stopped at its deadline."},
			"EvaluatorStats":    {Doc: "EvaluatorStats says how each judged evaluator decided the run's scores. Empty for runs scored only by heuristics, and for runs that finished before Agent Mesh began recording these statistics."},
			"Model":             {Doc: "Model is the UUID of the model configuration the run used, the model's name when the experiment names it by alias alone, or null for the agent's own model."},
			"ConfigSnapshot":    {Doc: "ConfigSnapshot is the experiment, dataset, and agent or workflow as they were when the run started, or null when none was recorded."},
			"EvaluatorSnapshot": {Doc: "EvaluatorSnapshot is the evaluators as they were when the run started."},
			"RunName":           {Doc: "RunName names the run in reports. Defaults to the experiment's name when the run started. Null when none was recorded."},
			"Source":            {Doc: "Source says how the run started: manual for a run a person or a client triggered."},
			"RunAs":             {Doc: "RunAs is the service account the run executed as, as system:<name>, or null for the built-in evaluation service account."},
			"CreatedBy":         {Doc: "CreatedBy identifies the user who triggered the run: their email, or their user ID when no email is known."},
			"CreatedTime":       {Doc: "CreatedTime is when the run was triggered."},
			"CompletedTime":     {Doc: "CompletedTime is when the run reached completed, completed_with_warnings, failed, or cancelled. Null while it has not."},
		},
	}
}

func (RunScore) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunScore is an evaluator's mean score over a run's trials.",
		Fields: map[string]apidoc.Field{
			"EvaluatorID": {Doc: "EvaluatorID is the UUID of the evaluator that scored the trials."},
			"Score":       {Doc: "Score is the mean of the evaluator's scores, 0 to 1."},
		},
	}
}

func (RunEvaluatorStats) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunEvaluatorStats is how one judged evaluator, a large language model (LLM) judge or a decision
evaluator, decided a run's scores.`,
		Fields: map[string]apidoc.Field{
			"EvaluatorID":      {Doc: "EvaluatorID is the UUID of the evaluator."},
			"Scored":           {Doc: "Scored counts the scores the evaluator's judges produced."},
			"Escalated":        {Doc: "Escalated counts the decision evaluator's scores that its large language model (LLM) judge produced. Always 0 for an LLM judge."},
			"WithConfidence":   {Doc: "WithConfidence counts the cases a decision evaluator's model answered with a confidence. Always 0 for a large language model (LLM) judge."},
			"EscalatedPercent": {Doc: "EscalatedPercent is the share of a decision evaluator's scores its large language model (LLM) judge produced, 0 to 100, or null for an LLM judge."},
			"MeanConfidence":   {Doc: "MeanConfidence is the decision model's mean confidence, 0 to 1, over the cases it answered with one, or null when there were none."},
			"JudgeUsage":       {Doc: "JudgeUsage totals the judges' token usage across the run."},
		},
	}
}

func (JudgeUsage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "JudgeUsage is the token usage of the judges that scored.",
		Fields: map[string]apidoc.Field{
			"Decision": {Doc: "Decision is the decision model's token usage, or null when none was called."},
			"LLMJudge": {Doc: "LLMJudge is the large language model (LLM) judge's token usage, or null when none was called."},
		},
	}
}

func (JudgeTokenUsage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "JudgeTokenUsage is one judge's token counts.",
		Fields: map[string]apidoc.Field{
			"InputTokens":  {Doc: "InputTokens counts the tokens sent to the judge."},
			"OutputTokens": {Doc: "OutputTokens counts the tokens the judge generated."},
		},
	}
}

func (RunConfigSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunConfigSnapshot is what a run was configured with when it started, kept so
a report shows the run as it was whatever was edited since.`,
		Fields: map[string]apidoc.Field{
			"Experiment": {Doc: "Experiment is the experiment's settings, or null when none was recorded."},
			"Dataset":    {Doc: "Dataset is the dataset and its examples, or null when none was recorded."},
			"Agent":      {Doc: "Agent is the agent under evaluation, or null when none was recorded, as for a run of a workflow experiment."},
			"Workflow":   {Doc: "Workflow is the workflow under evaluation, or null for a run of an agent experiment."},
		},
	}
}

func (RunWorkflowSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunWorkflowSnapshot is the workflow under evaluation when a run started.",
		Fields: map[string]apidoc.Field{
			"Name":         {Doc: "Name is the workflow's name as the experiment gives it."},
			"InstanceName": {Doc: "InstanceName is the name the workflow listens on, which its trials were sent to, or null when it was not recorded."},
			"InputSchema":  {Doc: "InputSchema is the JSON Schema the workflow declared for its input, which each example's inputData was checked against, or null when the workflow declared none, declared an empty or non-object schema, or it was not recorded."},
		},
	}
}

func (RunExperimentSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunExperimentSnapshot is the experiment's settings when a run started.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the experiment's UUID."},
			"Name":        {Doc: "Name is the experiment's name."},
			"Description": {Doc: "Description says what the experiment measures, or is null when it had none."},
			"Models":      {Doc: "Models are the models the experiment ran its agent under."},
		},
	}
}

func (RunModelSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunModelSnapshot is one model of the experiment when a run started.",
		Fields: map[string]apidoc.Field{
			"ModelConfigID": {Doc: "ModelConfigID is the UUID of the model configuration, or null for a model named by alias alone."},
			"Model":         {Doc: "Model is the provider's name for the model."},
			"Alias":         {Doc: "Alias is the model configuration's alias."},
			"ResolvedModel": {Doc: "ResolvedModel is the provider's name for the model the configuration resolved to, or null when it could not be resolved."},
			"ModelConfig":   {Doc: "ModelConfig is the model configuration as it was, or null when it was not recorded."},
		},
	}
}

func (RunModelConfigSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunModelConfigSnapshot is a model configuration when a run started. It leaves
out the auth settings, but modelParams come back as the configuration set them.`,
		Fields: map[string]apidoc.Field{
			"ID":             {Doc: "ID is the model configuration's UUID, or null when the configuration was deleted before the snapshot."},
			"Alias":          {Doc: "Alias is the model configuration's alias."},
			"Provider":       {Doc: "Provider is the model's provider, or null when none is set."},
			"ModelName":      {Doc: "ModelName is the provider's name for the model, or null when none is set."},
			"APIBase":        {Doc: "APIBase is the provider endpoint, or null for the provider's default."},
			"ModelParams":    {Doc: "ModelParams are the inference parameters the configuration sets, such as temperature."},
			"MaxInputTokens": {Doc: "MaxInputTokens is the model's input token limit, or null when none is set."},
			"Description":    {Doc: "Description says what the configuration is for, or is null when it has none."},
		},
	}
}

func (RunDatasetSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunDatasetSnapshot is the dataset when a run started.",
		Fields: map[string]apidoc.Field{
			"ID":           {Doc: "ID is the dataset's UUID."},
			"Name":         {Doc: "Name is the dataset's name."},
			"ExampleCount": {Doc: "ExampleCount is how many examples the dataset held."},
			"Examples":     {Doc: "Examples are the dataset's examples, in sequence order."},
		},
	}
}

func (RunExampleSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunExampleSnapshot is one dataset example when a run started.",
		Fields: map[string]apidoc.Field{
			"ID":               {Doc: "ID is the example's UUID."},
			"SequenceNumber":   {Doc: "SequenceNumber is the example's position in the dataset, from 1."},
			"Prompt":           {Doc: "Prompt is what the agent was asked. Empty for an example that has only inputData."},
			"ExpectedResponse": {Doc: "ExpectedResponse is the answer the evaluators compare against, or null when the example has none."},
			"InputData":        {Doc: "InputData is the structured input a workflow example was given, a JSON object as it was stored, or null when the example has none."},
			"ExpectedPath":     {Doc: "ExpectedPath is how a workflow example's trials were expected to run, which the Workflow Path evaluator checks, or null when the example has none."},
			"ExpectedFacts":    {Doc: "ExpectedFacts is what the example's response had to say and not say, which the Output Facts evaluator checks, or null when the example has none."},
			"InputArtifact":    {Doc: "InputArtifact is the file sent to the agent with the prompt, or null when the example has none."},
			"ExpectedArtifact": {Doc: "ExpectedArtifact is the file the evaluators compare against, or null when the example has none."},
		},
	}
}

func (RunExampleArtifactSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunExampleArtifactSnapshot is a file attached to a dataset example when a run started.",
		Fields: map[string]apidoc.Field{
			"Filename":    {Doc: "Filename is the file's name."},
			"Version":     {Doc: "Version is the file's version, from 0."},
			"MimeType":    {Doc: "MimeType is the file's media type, or null when it is not known."},
			"Size":        {Doc: "Size is the file's size in bytes, or null when it is not known."},
			"ContentHash": {Doc: "ContentHash is the hash of the file's content, or null when it is not known."},
		},
	}
}

func (RunAgentSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunAgentSnapshot is the agent under evaluation when a run started.",
		Fields: map[string]apidoc.Field{
			"Name":         {Doc: "Name is the agent's name."},
			"Description":  {Doc: "Description says what the agent does, or is null when it has none."},
			"AgentType":    {Doc: "AgentType is the kind of agent, or null for an agent discovered on the mesh."},
			"SystemPrompt": {Doc: "SystemPrompt is the agent's instructions, or null when it has none or Agent Mesh does not hold them, as for a discovered agent."},
			"Toolsets":     {Doc: "Toolsets are the names of the toolsets the agent uses."},
			"Tools":        {Doc: "Tools are the tools a discovered agent advertises. Empty for an agent that Agent Mesh manages."},
			"Connectors":   {Doc: "Connectors are the connectors the agent uses."},
			"Skills":       {Doc: "Skills are the skills the agent has."},
			"InputModes":   {Doc: "InputModes are the media types the agent accepts."},
			"OutputModes":  {Doc: "OutputModes are the media types the agent produces."},
			"ModelConfig":  {Doc: "ModelConfig is the model configuration the agent used, or null when it was not recorded."},
			"Origin":       {Doc: "Origin is where the agent came from: discovered for an agent found on the mesh that Agent Mesh does not manage, or null for one it manages."},
		},
	}
}

func (RunAgentToolSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunAgentToolSnapshot is a tool a discovered agent advertised.",
		Fields: map[string]apidoc.Field{
			"Key":         {Doc: "Key is the tool's identifier, or null when none was advertised."},
			"Name":        {Doc: "Name is the tool's name."},
			"Description": {Doc: "Description says what the tool does, or is null when none was advertised."},
		},
	}
}

func (RunAgentConnectorSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunAgentConnectorSnapshot is a connector the agent used.",
		Fields: map[string]apidoc.Field{
			"ID":            {Doc: "ID is the connector's UUID."},
			"Name":          {Doc: "Name is the connector's name."},
			"ConnectorType": {Doc: "ConnectorType is the kind of connector."},
			"Subtype":       {Doc: "Subtype narrows the connector's kind, or is null when it has none."},
		},
	}
}

func (RunAgentSkillSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunAgentSkillSnapshot is a skill the agent had.",
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the skill's identifier, or null when none is recorded."},
			"Name":        {Doc: "Name is the skill's name."},
			"Description": {Doc: "Description says what the skill does, or is null when it has none."},
		},
	}
}

func (RunEvaluatorSnapshot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunEvaluatorSnapshot is an evaluator when a run started.",
		Fields: map[string]apidoc.Field{
			"ID":                {Doc: "ID is the evaluator's UUID."},
			"Name":              {Doc: "Name is the evaluator's name."},
			"Description":       {Doc: "Description says what the evaluator checks, or is null when it has none."},
			"EvaluatorType":     {Doc: "EvaluatorType is the kind of evaluator: llm_judge for one a model scores, or heuristic for one a rule scores."},
			"HeuristicType":     {Doc: "HeuristicType names the rule of a heuristic evaluator, or is null for an llm_judge."},
			"IsDefault":         {Doc: "IsDefault is true for an evaluator that Agent Mesh creates at installation."},
			"Model":             {Doc: "Model is the alias or UUID of the model configuration an llm_judge runs under, or null for a heuristic."},
			"PromptTemplate":    {Doc: "PromptTemplate is the prompt an llm_judge sends its model, or null for a heuristic."},
			"ChoiceScores":      {Doc: "ChoiceScores maps each choice an llm_judge can answer with to its score, 0 to 1. Empty for a heuristic."},
			"ResolvedModelName": {Doc: "ResolvedModelName is the provider's name for the model an llm_judge ran under, or null when it could not be resolved."},
			"ModelConfig":       {Doc: "ModelConfig is the model configuration an llm_judge ran under as it was, or null when it was not recorded."},
		},
	}
}

func (listExperimentRunsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listExperimentRunsRequest selects a page of an experiment's runs.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment whose runs to list."},
			"Page":         {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}

func (TriggerExperimentRunRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "TriggerExperimentRunRequest starts a run of an experiment. Send {} to start one with the defaults.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment to run."},
			"RunName":      {Doc: "RunName names the runs in reports. Defaults to the experiment's name."},
		},
	}
}
