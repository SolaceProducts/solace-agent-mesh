// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Evaluator) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Evaluator is a rule, a large language model (LLM) judge, or a decision model that scores an
agent's answers in an experiment.`,
		Fields: map[string]apidoc.Field{
			"ID":                    {Doc: "ID is the evaluator's UUID."},
			"Type":                  {Doc: "Type is always \"evaluator\"."},
			"Name":                  {Doc: "Name is the evaluator's unique name."},
			"Description":           {Doc: "Description says what the evaluator checks, or is null when it has none."},
			"EvaluatorType":         {Doc: "EvaluatorType is the kind of evaluator: llm_judge when a chat model scores, decision when a decision model scores, or heuristic when a rule scores. Fixed when the evaluator is created."},
			"HeuristicType":         {Doc: "HeuristicType names the rule of a heuristic evaluator, such as valid_json, or is null for any other type."},
			"IsDefault":             {Doc: "IsDefault is true for the built-in evaluators that Agent Mesh creates at installation. They cannot be deleted, and the only change they accept is the model of an llm_judge."},
			"Model":                 {Doc: "Model is the alias or UUID of the model configuration that scores. Null for a heuristic evaluator, and for a built-in llm_judge that scores with the general model."},
			"PromptTemplate":        {Doc: "PromptTemplate is the grading prompt of an llm_judge. Null for any other type."},
			"ChoiceScores":          {Doc: "ChoiceScores maps each choice the model can answer with to its score. Null for a heuristic evaluator, and for a decision evaluator whose question is not a choice."},
			"Question":              {Doc: "Question is the one question a decision evaluator asks its model. Null for any other type."},
			"Scoring":               {Doc: "Scoring is how a decision evaluator turns an answer into a score: chosen or expected. Null for any other type."},
			"Reads":                 {Doc: "Reads are the case fields a decision evaluator sends its model, from prompt, response, expected_response, transcript, and artifacts. Null for any other type."},
			"MinConfidence":         {Doc: "MinConfidence is the confidence below which a decision evaluator hands the case to its escalation evaluator. Null when it has none."},
			"EscalateToEvaluatorID": {Doc: "EscalateToEvaluatorID is the UUID of the llm_judge evaluator a decision evaluator hands a case to. Null when it has none."},
			"ExperimentCount":       {Doc: "ExperimentCount is how many experiments the evaluator is attached to."},
			"CreatedBy":             {Doc: "CreatedBy identifies the user who created the evaluator: their email, or their user ID when no email is known."},
			"UpdatedBy":             {Doc: "UpdatedBy identifies the user who last changed the evaluator, in the same form as createdBy."},
			"CreatedTime":           {Doc: "CreatedTime is when the evaluator was created."},
			"UpdatedTime":           {Doc: "UpdatedTime is when the evaluator was last changed."},
		},
	}
}

func (EvaluatorQuestion) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "EvaluatorQuestion is the question a decision evaluator asks its model.",
		Fields: map[string]apidoc.Field{
			"Type":         {Doc: "Type is the kind of question: boolean for a probability that the answer is true, choice for one of the criteria, or score for a level among ordered criteria."},
			"Instructions": {Doc: "Instructions is the question, written for the decision model."},
			"Criteria":     {Doc: "Criteria are the options of a choice question (2 to 26), or the levels of a score question from lowest to highest (2 to 10). For a boolean question, the optional true and false descriptions, or null for none."},
		},
	}
}

func (EvaluatorCriterion) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "EvaluatorCriterion is one option or level of an evaluator's question.",
		Fields: map[string]apidoc.Field{
			"Key":         {Doc: "Key names the option or level. For a choice question, it is the key of the option's score in choiceScores."},
			"Description": {Doc: "Description explains the option or level to the decision model, or is null when it has none."},
		},
	}
}

func (CreateEvaluatorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateEvaluatorRequest creates an evaluator. If the name is taken, the request fails with 409.",
		Fields: map[string]apidoc.Field{
			"Name":                  {Doc: "Name is the evaluator's unique name. Up to 255 bytes."},
			"Description":           {Doc: "Description says what the evaluator checks, 1 to 1,000 bytes."},
			"EvaluatorType":         {Doc: "EvaluatorType is the kind of evaluator: llm_judge or decision. Defaults to llm_judge. Decision evaluators can be created only while they are enabled on the deployment. Heuristic evaluators are built in and cannot be created."},
			"Model":                 {Doc: "Model is the alias or UUID of the model configuration that scores: a chat model for an llm_judge, a decision model for a decision evaluator. Required for both."},
			"PromptTemplate":        {Doc: "PromptTemplate is the llm_judge's grading prompt. Required for an llm_judge and not allowed for a decision evaluator. It must contain {{ Response }} and can use {{ Prompt }} and {{ Expected Response }}. In a workflow experiment, it can also use {{ Node Output: <node id> }} and {{ Node Input: <node id> }} so the judge can grade one node. Each takes a redacted, length-capped preview of what that node returned or took in the trial. In an agent experiment, these placeholders are left as written. Name a node of a nested sub-workflow under the node that ran it, as in {{ Node Output: enrich ▸ validate }}. A map or loop node reports once for the whole node. A map's output lists each item's result. A loop's output says only how many iterations ran and why it stopped. The nodes inside a map or loop cannot be named. While workflow evaluations are turned on, a template is refused if a node placeholder has no node id or is mistyped, such as {{ Node Outputs: x }}."},
			"ChoiceScores":          {Doc: "ChoiceScores maps each choice the model can answer with to its score. An llm_judge needs 2 to 26 choices. A decision evaluator needs one score from 0 to 1 for each option of a choice question, and none for any other question."},
			"Question":              {Doc: "Question is the question a decision evaluator asks. Required for a decision evaluator and not allowed for an llm_judge."},
			"Scoring":               {Doc: "Scoring is how a decision evaluator turns an answer into a score: chosen scores the option the model chose, and expected weighs every option by its probability. Defaults to chosen. Only for a decision evaluator."},
			"Reads":                 {Doc: "Reads are the case fields a decision evaluator sends its model, from prompt, response, expected_response, transcript, and artifacts. Must include response. Defaults to prompt, response, and expected_response. In a workflow experiment, transcript sends the workflow's node-by-node run under the key workflow_trajectory. Only for a decision evaluator."},
			"MinConfidence":         {Doc: "MinConfidence is the confidence below which the case goes to escalateToEvaluatorId: more than 0 and up to 1, and more than 0.5 for a boolean question. Requires escalateToEvaluatorId. Only for a decision evaluator."},
			"EscalateToEvaluatorID": {Doc: "EscalateToEvaluatorID is the UUID of an llm_judge evaluator that scores a case the model is unsure of or fails on. Only for a decision evaluator."},
			"Surface":               {Doc: "Surface is the X-Sam-Client-Surface header: ui or cli names the first-party client that makes the request, and any other value, or no header, counts as api. Used only for usage statistics."},
		},
	}
}

func (UpdateEvaluatorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateEvaluatorRequest changes only the fields it includes. If it includes a
field the evaluator's type does not use, the request fails with 422.`,
		Fields: map[string]apidoc.Field{
			"ID":                    {Doc: "ID is the UUID of the evaluator to update."},
			"Name":                  {Doc: "Name is the evaluator's unique name. Up to 255 bytes. Leave it out to keep it. Null or an empty string fails with 422."},
			"Description":           {Doc: "Description says what the evaluator checks, 1 to 1,000 bytes. Leave it out to keep it. Null or an empty string fails with 422."},
			"EvaluatorType":         {Doc: "EvaluatorType is fixed when the evaluator is created. If the body includes it, the request fails with 422."},
			"Model":                 {Doc: "Model is the alias or UUID of the model configuration that scores. Leave it out to keep it. An empty string fails with 422. Null resets a built-in llm_judge to the general model, and fails with 422 for any other evaluator."},
			"PromptTemplate":        {Doc: "PromptTemplate is the llm_judge's grading prompt. It must contain {{ Response }} and can name a workflow node, as on create. Leave it out to keep it. A template sent back unchanged, apart from placeholder spelling and surrounding whitespace, is never refused for its node placeholders. For an llm_judge, null fails with 422. A decision evaluator has no prompt template, so null is accepted."},
			"ChoiceScores":          {Doc: "ChoiceScores replaces the score of each choice. Leave it out to keep them. For an llm_judge, null fails with 422. For a decision evaluator whose question is no longer a choice, null clears them."},
			"Question":              {Doc: "Question replaces a decision evaluator's question. Leave it out to keep it. Null fails with 422, because a question cannot be cleared. Send choiceScores with a changed choice question."},
			"Scoring":               {Doc: "Scoring is how a decision evaluator turns an answer into a score: chosen or expected. Leave it out to keep it, or send null for chosen."},
			"Reads":                 {Doc: "Reads replaces the case fields a decision evaluator sends its model. Leave it out to keep them, or send null for prompt, response, and expected_response."},
			"MinConfidence":         {Doc: "MinConfidence is the confidence below which a decision evaluator escalates. Leave it out to keep it, or send null to clear it."},
			"EscalateToEvaluatorID": {Doc: "EscalateToEvaluatorID is the UUID of the llm_judge a decision evaluator escalates to. Leave it out to keep it, or send null to clear it."},
		},
	}
}
