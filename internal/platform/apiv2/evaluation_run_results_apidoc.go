// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (RunResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunResult is the outcome of one trial: one example run once, with the answer
the agent gave and the score each evaluator gave it.`,
		Fields: map[string]apidoc.Field{
			"ID":                {Doc: "ID is the run result's UUID."},
			"Type":              {Doc: "Type is always \"runResult\"."},
			"RunID":             {Doc: "RunID is the UUID of the run the trial belongs to."},
			"ExampleID":         {Doc: "ExampleID is the UUID of the dataset example the trial ran."},
			"TrialIndex":        {Doc: "TrialIndex numbers the trial among the example's runs in this run, from 0."},
			"InputPrompt":       {Doc: "InputPrompt is the prompt the agent was given, or null when none was recorded."},
			"ExpectedResponse":  {Doc: "ExpectedResponse is the answer the example expects, or null when it has none."},
			"TargetAgent":       {Doc: "TargetAgent is the name of the agent that ran the trial, or null when none was recorded."},
			"AgentResponse":     {Doc: "AgentResponse is the agent's final answer, or null when it gave none."},
			"ResolvedModelName": {Doc: "ResolvedModelName is the model the agent ran under, or null when none was recorded."},
			"DurationSeconds":   {Doc: "DurationSeconds is how long the trial took, or null when it did not finish."},
			"ErrorMessage":      {Doc: "ErrorMessage says why the trial produced no answer, or is null when it did."},
			"FailureKind":       {Doc: "FailureKind is a machine-readable reason a trial ended early. Such a trial has no scores. auth_required means a tool needed a sign-in that the run's service account did not have. That includes the tool of a workflow tool node, which ends the trial auth_required even when the workflow completed without that branch. A workflow trial can also end with node_failed (a node failed), paused_for_input (a node asked for human input), timeout (the workflow or the run's budget ran out), cancelled (the run was stopped), invalid_input (the workflow or its input schema refused the example's input), or rejected (the workflow refused the request outright, for example one the run's principal is not allowed to make). partial_failure is the only kind on a scored trial, and its errorMessage is null. It means the workflow completed without a node that failed, and the trial was scored on what the other nodes produced. Null otherwise."},
			"TargetType":        {Doc: "TargetType is the kind of target the trial ran: \"workflow\" for a trial of a workflow experiment, else \"agent\"."},
			"TokenUsage":        {Doc: "TokenUsage is the token use for the trial, or null when none was reported. For an agent trial, it is the agent's own usage. For a workflow trial, it is the sum of the model calls counted across its nodes when a final response was captured, at least one call was counted, and the workflow did not report more. That sum is a lower bound when tokenBreakdown.incomplete is true. If any of those conditions does not hold, it is what the workflow reported, often null, and tokenBreakdown still has the counted calls."},
			"TokenBreakdown":    {Doc: "TokenBreakdown is a workflow trial's token use by node and by model. Empty when the trial called no model. Null for an agent trial, and for a workflow trial that was never sent, that the run gave up on (timed out or canceled before a response), that was recorded before breakdowns existed, or whose stored breakdown cannot be read."},
			"Cost":              {Doc: "Cost is what a workflow trial's model calls cost, not counting the judge's calls. The value is a decimal string with 10 fractional digits, in the operator's pricing currency. When the deployment tracks spend, a trial never sent because its input did not fit the workflow's input schema (invalid_input, no tokenBreakdown) costs \"0.0000000000\". Null otherwise: for an agent trial, when the deployment does not track spend, while any of the trial's spend is not yet recorded, when a call has no price, when the token count was found incomplete as the trial finished, and for a trial that timed out or was canceled. A partial sum never appears as the total. A cost next to an incomplete tokenBreakdown covers calls the stream did not carry, which the token counts leave out. The cost can change from null to a value as spend is recorded, and can still rise while those calls are recorded. It returns to null once the trial's recorded spend expires."},
			"Scores":            {Doc: "Scores are the scores the evaluators gave the answer. Empty until the trial is scored."},
			"Artifacts":         {Doc: "Artifacts are the files the agent produced during the trial."},
			"InputArtifact":     {Doc: "InputArtifact is the example's input file as the trial received it, or null when the example has none."},
			"ExpectedArtifact":  {Doc: "ExpectedArtifact is the expected file the answer was graded against, or null when the example has none."},
			"CreatedTime":       {Doc: "CreatedTime is when the result was recorded."},
		},
	}
}

func (RunResultTokenUsage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunResultTokenUsage is the token use for one trial. A count is null when it
was not reported, which differs from 0. A workflow trial that uses its
counted node calls (see the run result's tokenUsage) reports every count. In
that case, cachedInputTokens and reasoningTokens are 0 where no counted call
reported them, and totalTokens is inputTokens plus outputTokens. The counts
overlap, so do not add them: cachedInputTokens is part of inputTokens, and
reasoningTokens is part of outputTokens.`,
		Fields: map[string]apidoc.Field{
			"InputTokens":       {Doc: "InputTokens is the tokens sent to the model."},
			"CachedInputTokens": {Doc: "CachedInputTokens is the part of inputTokens the provider served from its prompt cache."},
			"OutputTokens":      {Doc: "OutputTokens is the tokens the model produced."},
			"ReasoningTokens":   {Doc: "ReasoningTokens is the part of outputTokens the model spent thinking."},
			"TotalTokens":       {Doc: "TotalTokens is the total the provider reported, normally inputTokens plus outputTokens."},
		},
	}
}

func (RunResultTokenBreakdown) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunResultTokenBreakdown is a workflow trial's token use split by node and by
model. Together, byNode and unattributed add up to the same totals as byModel.`,
		Fields: map[string]apidoc.Field{
			"TraceID":      {Doc: "TraceID is the trial's trace id, or null when it had none."},
			"Incomplete":   {Doc: "Incomplete is true when the count can leave calls out. Any of these makes it true: the capture was cut short, no final response was captured, a node was still running when the workflow ended, a node ended in error or was retried, a map or loop node ran, a sub-workflow ran, a node of an unknown type ran, a call's usage could not be read, or the workflow reported more tokens than were counted. A retried node's earlier attempt can go on calling. The calls of a map or loop node's items are not streamed. A sub-workflow's decision nodes do not report their tokens. The flag is also true when the recorded spend holds more tokens than were counted, because spend also covers the calls a tool node makes, conversation summaries, and empty replies, which are not streamed. That last check runs when the result is read, and only when the deployment tracks spend."},
			"ByNode":       {Doc: "ByNode is each of the workflow's own nodes that called a model, in the order they first did."},
			"Unattributed": {Doc: "Unattributed is the calls no node can be credited with: decision nodes, which report their tokens by model only, and calls forwarded from a sub-task no node is known to have dispatched."},
			"ByModel":      {Doc: "ByModel is every counted call by the model it ran on, sorted by model."},
		},
	}
}

func (RunResultTokenCounts) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunResultTokenCounts is a count of model tokens. The counts overlap, so do
not add them: cachedInputTokens is part of inputTokens and reasoningTokens
part of outputTokens. Each is 0 when nothing was reported for it.`,
		Fields: map[string]apidoc.Field{
			"InputTokens":       {Doc: "InputTokens is the tokens sent to the model."},
			"CachedInputTokens": {Doc: "CachedInputTokens is the part of inputTokens the provider served from its prompt cache."},
			"OutputTokens":      {Doc: "OutputTokens is the tokens the model produced."},
			"ReasoningTokens":   {Doc: "ReasoningTokens is the part of outputTokens the model spent thinking."},
			"TotalTokens":       {Doc: "TotalTokens is the sum of inputTokens and outputTokens."},
		},
	}
}

func (RunResultNodeTokens) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunResultNodeTokens is one of the workflow's own nodes' share of a trial's
tokens, including every agent call made beneath it.`,
		Fields: map[string]apidoc.Field{
			"NodeID": {Doc: "NodeID is the node's id in the workflow."},
			"Models": {Doc: "Models are the models the node's calls ran on, in first-use order."},
			"Tokens": {Doc: "Tokens is the node's token count."},
		},
	}
}

func (RunResultModelTokens) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunResultModelTokens is a trial's token count on one model.",
		Fields: map[string]apidoc.Field{
			"Model":  {Doc: "Model is the model the calls ran on, or null for calls that named none."},
			"Tokens": {Doc: "Tokens is the token count on that model."},
		},
	}
}

func (RunResultScore) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunResultScore is one evaluator's score for a trial's answer.",
		Fields: map[string]apidoc.Field{
			"Key":         {Doc: "Key is the score's UUID, unique within the trial's scores."},
			"EvaluatorID": {Doc: "EvaluatorID is the UUID of the evaluator that gave the score."},
			"Score":       {Doc: "Score is the evaluator's numeric score."},
			"Choice":      {Doc: "Choice is the label a large language model (LLM) evaluator picked, or null for other evaluators."},
			"Passed":      {Doc: "Passed is whether the score reached the pass mark of 0.5, or null when no verdict was recorded."},
			"Reasoning":   {Doc: "Reasoning is the evaluator's explanation of the score, or null when it gave none."},
			"Details":     {Doc: "Details says which judge decided the score, what a decision model answered, and what judging cost. Null for heuristic scores and for scores recorded before Agent Mesh began keeping these details."},
			"CreatedTime": {Doc: "CreatedTime is when the score was recorded."},
		},
	}
}

func (RunScoreDetails) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunScoreDetails records how a judged score was decided.",
		Fields: map[string]apidoc.Field{
			"DecidedBy":        {Doc: "DecidedBy is the judge that decided the score: decision or llm_judge. Null for a score that errored before any judge decided it."},
			"Model":            {Doc: "Model is the decision model version the provider served, or null when no decision model answered."},
			"Answer":           {Doc: "Answer is the decision model's answer, kept when the case was escalated too, or null when it gave none."},
			"Escalated":        {Doc: "Escalated is true when the decision evaluator's large language model (LLM) judge produced the score."},
			"EscalationReason": {Doc: "EscalationReason is why the case was escalated: low_confidence, no_confidence, provider_error, or model_lookup_failed. Null when the case was not escalated."},
			"ProviderError":    {Doc: "ProviderError is the kind of decision model failure, never the provider's own text: unauthorized, rate_limited, timeout, unreachable, invalid_response, input_too_large, config, or refused. Null when there was none."},
			"Usage":            {Doc: "Usage is the token usage of each judge the case called, or null when none was recorded."},
			"DurationMs":       {Doc: "DurationMs is how long scoring the case took in milliseconds, escalation included, or null when not recorded."},
		},
	}
}

func (DecisionAnswer) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DecisionAnswer is a decision model's answer to a decision evaluator's
question.`,
		Fields: map[string]apidoc.Field{
			"Choice":         {Doc: "Choice is the chosen option, the nearest score level, or true or false, or null when the model gave none."},
			"Probability":    {Doc: "Probability is the probability of true for a boolean question, or null for other questions."},
			"Score":          {Doc: "Score is the position among a score question's levels, 0 for the lowest, or null for other questions."},
			"Confidence":     {Doc: "Confidence is how sure the model was, 0 to 1, or null when the provider reported nothing to derive it from."},
			"Probabilities":  {Doc: "Probabilities are each option's or level's probability, by option name."},
			"ScoringApplied": {Doc: "ScoringApplied is how a choice or score answer was turned into a score: chosen or expected. Null for a boolean question."},
		},
	}
}

func (DecisionOptionProbability) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DecisionOptionProbability is the probability a decision model gave one
option or level.`,
		Fields: map[string]apidoc.Field{
			"Option":      {Doc: "Option is the option's or level's key, as the evaluator's question defines it."},
			"Probability": {Doc: "Probability is the option's probability, 0 to 1."},
		},
	}
}

func (RunResultArtifact) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunResultArtifact is a file attached to a trial: one the agent produced, the
example's input file, or its expected file.`,
		Fields: map[string]apidoc.Field{
			"Filename": {Doc: "Filename is the file's name, which can contain slashes."},
			"MimeType": {Doc: "MimeType is the file's media type, or null when none was recorded."},
			"Version":  {Doc: "Version is the recorded version of the file, from 0."},
			"Size":     {Doc: "Size is the file's size in bytes, or null when none was recorded."},
		},
	}
}

func (RunResultExecutionData) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunResultExecutionData is what the platform captured while one trial ran.",
		Fields: map[string]apidoc.Field{
			"RunID":           {Doc: "RunID is the UUID of the run the trial belongs to."},
			"ExampleID":       {Doc: "ExampleID is the UUID of the dataset example the trial ran."},
			"TrialIndex":      {Doc: "TrialIndex numbers the trial among the example's runs in this run, from 0."},
			"Messages":        {Doc: "Messages are the event broker messages captured during the trial, in the order they arrived."},
			"AgentResponse":   {Doc: "AgentResponse is the agent's final answer, or null when it gave none."},
			"DurationSeconds": {Doc: "DurationSeconds is how long the trial took."},
		},
	}
}

func (RunResultMessage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RunResultMessage is one event broker message captured during a trial.",
		Fields: map[string]apidoc.Field{
			"Topic":        {Doc: "Topic is the event broker topic the message was published to."},
			"Payload":      {Doc: "Payload is the Agent-to-Agent (A2A) message as it was published, with the A2A protocol's own keys."},
			"CapturedTime": {Doc: "CapturedTime is when Agent Mesh received the message, or null when not recorded."},
		},
	}
}

func (RunResultTaskEvents) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunResultTaskEvents is the event stream recorded while one trial ran, which
the activity diagram replays.`,
		Fields: map[string]apidoc.Field{
			"Events": {Doc: "Events are the recorded events, oldest first. Each has the Agent-to-Agent (A2A) stream's keys: event_type, timestamp, solace_topic, direction, source_entity, target_entity, message_id, task_id, payload_summary, and full_payload."},
		},
	}
}

func (listRunResultsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listRunResultsRequest selects a page of one run's results.",
		Fields: map[string]apidoc.Field{
			"RunID": {Doc: "RunID is the run's UUID."},
			"Page":  {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}

func (exportRunResultsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "exportRunResultsRequest names a run and a file format.",
		Fields: map[string]apidoc.Field{
			"RunID":  {Doc: "RunID is the run's UUID."},
			"Format": {Doc: "Format is the file format: csv or json. Defaults to csv."},
		},
	}
}

func (runResultIDRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "runResultIDRequest addresses one run result.",
		Fields: map[string]apidoc.Field{
			"RunResultID": {Doc: "RunResultID is the run result's UUID."},
		},
	}
}

func (runResultArtifactRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "runResultArtifactRequest names one file of a run result.",
		Fields: map[string]apidoc.Field{
			"RunResultID": {Doc: "RunResultID is the run result's UUID."},
			"Filename":    {Doc: "Filename is the file's name as the run result lists it. It can contain slashes."},
			"Version":     {Doc: "Version pins a recorded version of the file, as an integer of 0 or more. Defaults to the highest version the run result recorded for the file."},
		},
	}
}
