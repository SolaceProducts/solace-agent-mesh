// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateEvaluatorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateEvaluatorRequest is config apply's copy of
apiv2.CreateEvaluatorRequest.`,
		Fields: map[string]apidoc.Field{
			"Name":        {Doc: "Name is the evaluator's display name (unique per platform)."},
			"Description": {Doc: "Description explains what the evaluator scores."},
			"EvaluatorType": {Doc: `Type selects the evaluator kind. Defaults to "llm_judge" when omitted;
"decision" (Early Access) grades with a decision model instead of an
LLM. "heuristic" evaluators (valid_json, json_diff, …) are
system-seeded and read-only here.`},
			"Model": {Doc: `Model is the model alias used to grade responses: a chat model for
llm_judge evaluators, a decision model for decision evaluators.
Required for both.`},
			"PromptTemplate": {Doc: `PromptTemplate is the grading prompt. Required for llm_judge evaluators
and must contain the {{ Response }} placeholder. Also supports
{{ Prompt }} and {{ Expected Response }} (case-insensitive, canonicalized
on save) and, in an experiment that targets a workflow (Early Access),
{{ Node Output: <node id> }} and {{ Node Input: <node id> }}. The
execution transcript (an agent's, or a workflow's node-by-node
trajectory) and produced artifacts are injected automatically — do not
template those in.`},
			"ChoiceScores": {Doc: `ChoiceScores maps each choice label the judge may return to its numeric
score. Required for llm_judge evaluators; 2–26 entries. A decision
evaluator takes it only for a choice question, with one entry per
criteria key.`},
			"Question": {Doc: `Question is the one typed question a decision evaluator asks. Required
for decision evaluators; not allowed for llm_judge.`},
			"Scoring": {Doc: `Scoring is how a decision evaluator turns an answer into a score:
"chosen" (default) scores the chosen option, as an LLM judge does;
"expected" weights every option by its probability, for a smoother
score.`},
			"Reads": {Doc: `Reads lists the case fields a decision evaluator sends to the decision
model: prompt, response, expected_response, transcript, artifacts.
Defaults to prompt, response and expected_response; must include
response. transcript sends the agent's tool calls and results too, or
a workflow trial's node-by-node trajectory.`},
			"MinConfidence": {Doc: `MinConfidence is the confidence below which a decision evaluator hands
the case to its escalateToEvaluatorId evaluator. Greater than 0 and at
most 1; requires escalateToEvaluatorId.`},
			"EscalateToEvaluatorID": {Doc: `EscalateToEvaluatorID names an llm_judge evaluator that scores a case
the decision model answers below minConfidence, or fails on with a
transient or input error. Optional.`},
		},
	}
}

func (UpdateEvaluatorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateEvaluatorRequest is config apply's copy of
apiv2.UpdateEvaluatorRequest, a PATCH body. The evaluator's ID goes in the
path, not the body.`,
	}
}

func (EvaluatorResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "EvaluatorResponse is config apply's copy of apiv2.Evaluator.",
	}
}
