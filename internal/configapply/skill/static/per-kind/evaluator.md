An evaluator scores agent responses against per-example criteria.

Three evaluator types exist:
- `llm_judge` (user-creatable): scores a response by prompting an LLM
  with `promptTemplate`. Required: `model` (LLM model alias), the
  template (must contain `{{ Response }}`), and `choiceScores` (a map
  of choice → score, 2–26 entries).
- `decision` (user-creatable, Early Access): scores a response by asking
  a decision model one typed question. See "Decision-model evaluators"
  below.
- `heuristic` (system-seeded only): rule-based scorers like
  `valid_json`. `Workflow Path` grades an experiment that targets a
  workflow against each example's `expected_path`, and `Output Facts`
  checks any response against each example's `expected_facts` (see the
  dataset kind). These appear in the platform's evaluator list with
  `isDefault: true`; `sam config apply` does not manage them and they
  are filtered out of the desired/actual diff so the reconciler never
  attempts to update or delete them.

Authors write `kind: evaluator` for `llm_judge` and `decision`
evaluators. `type` defaults to `llm_judge` and may be omitted;
`decision` must be written out, and a type cannot be changed after the
evaluator is created.

`promptTemplate` supports these variables (case-insensitive, normalized
on save):
- `{{ Response }}` — the agent's response being graded. **Required** — a
  template without it is rejected at create time.
- `{{ Prompt }}` — the example's prompt.
- `{{ Expected Response }}` — the example's expected response, if set.
- `{{ Node Output: <node id> }}` and `{{ Node Input: <node id> }}` — a
  redacted, JSON-encoded preview (up to 4,000 characters) of what one
  workflow node returned or received in the trial, for grading a single
  step of an experiment that targets a workflow *(Early Access)*. A node
  with no value reads as a note saying why (for example, that it was
  skipped), and a failed node's value is labelled as such. Naming rules:
  - Copy the node id exactly; ids are case-sensitive. The id is not checked
    against the workflow when the evaluator is saved, and an id that
    matches no node gives the judge a note saying so instead of a value.
  - Name a node of a nested sub-workflow under the node that ran it, as in
    `{{ Node Output: enrich ▸ validate }}`.
  - A map or loop node reports once for the whole node; the nodes inside
    it cannot be named. Switch, map and loop nodes have no input to read.

  A mistyped placeholder such as `{{ Node Outputs: x }}`, or one with no
  node id, is rejected when the evaluator is saved. In an experiment that
  targets an agent, a node placeholder is not filled and the judge sees it
  as written.

Example — `evaluators/answer-grounded.yaml`:

```yaml
kind: evaluator
name: answer-grounded
description: Scores whether the answer is grounded in the prompt.
spec:
  type: llm_judge
  model: gpt-4o
  promptTemplate: |
    Prompt: {{ Prompt }}
    Response: {{ Response }}
    Is the response grounded in the prompt and free of invented facts?
    Reply with exactly one label: grounded, partial, or hallucinated.
  choiceScores:
    grounded: 1.0
    partial: 0.5
    hallucinated: 0.0
```

Beyond the `promptTemplate` variables, every `llm_judge` call is
automatically handed the agent's full execution context for the example
— you do not template these in:
- the **execution transcript**: the agent's assistant text interleaved,
  in execution order, with every tool it called (tool name **and** the
  parameter values it passed) and that tool's result (size-capped; very
  large results are truncated). Tools the agent merely had available but
  never invoked are excluded, so a tool counts as "used" only when it
  actually appears as a call.
- any **produced artifacts** (text inlined, binaries as filename-only
  references).

This lets a `promptTemplate` criterion *verify* behaviour from the trace
rather than infer it from the wording of the final answer — e.g. "must
call `date_math` rather than compute the date in-head" or "must route to
the `documents` skill" become checkable against the transcript.

When the experiment targets a workflow *(Early Access)*, the judge gets
the workflow's **trajectory** in place of the agent transcript: each node
in execution order with its type, how it ended (succeeded, failed with
its error, or skipped with the reason), the tools called inside it, the
branch a switch or decision node selected, and the node's input and
output (capped). A criterion can then check the route the workflow took,
not only its final output.

## Decision-model evaluators (Early Access)

A `decision` evaluator asks a registered **decision model** (TypeSafe
Jev, the OpenAI Decisions API or the Gemini Decisions API) one typed question about each response
and maps the answer to a score from 0.0 to 1.0. Use it when the rubric
is a classification, such as "did the agent answer the question?". It
sends the fields its `reads` list names to the decision model's
provider, so review the provider's data-processing terms first.

Required: `type: decision`, `model` (the alias of a decision model, never
a chat model), and `question`. A decision evaluator has no
`promptTemplate`; the question's `instructions` and `criteria` replace
it.

- `question.type` is `boolean`, `choice` or `score`.
  - `choice`: 2–26 `criteria`, each with a `key` and an optional
    `description`, and a `choiceScores` entry for every key.
  - `score`: 2–10 `criteria` (levels), lowest first. Do not set
    `choiceScores`; the lowest level scores 0.0, the highest 1.0, and
    the levels between are evenly spaced.
  - `boolean`: `criteria` is optional, and if present must describe both
    `"true"` and `"false"` (quote the keys so YAML keeps them as text).
    Do not set `choiceScores`.
- `scoring` (`choice` and `score` questions): `chosen` (default) scores
  the option the model picks, as an `llm_judge` does; `expected` weights
  every option by its probability for a smoother score.
- `reads` (default `[prompt, response, expected_response]`): the case
  fields sent to the decision model, from `prompt`, `response`,
  `expected_response`, `transcript` and `artifacts`. It must include
  `response`. For a trial of an experiment that targets a workflow,
  `transcript` sends the workflow's node-by-node trajectory instead of
  the agent's transcript.
- `escalateToEvaluatorId` (optional): the name of an `llm_judge`
  evaluator that scores the cases this evaluator escalates. A decision
  evaluator cannot escalate to another decision evaluator.
- `minConfidence` (optional, needs `escalateToEvaluatorId`): greater
  than 0 and at most 1 (greater than 0.5 for a `boolean` question).
  Answers below it are escalated.

Example — `evaluators/answers-the-question.yaml`:

```yaml
kind: evaluator
name: answers-the-question
description: Did the agent answer every part of the user's question?
spec:
  type: decision
  model: eval-judge
  question:
    type: choice
    instructions: Does the response answer every part of the user's question?
    criteria:
      - key: fully
        description: Answers every part of the question
      - key: partially
        description: Answers some parts and misses others
      - key: not_at_all
        description: Does not answer the question
  choiceScores:
    fully: 1.0
    partially: 0.5
    not_at_all: 0.0
  reads: [prompt, response, expected_response]
  minConfidence: 0.8
  escalateToEvaluatorId: answer-quality-judge
```

When a manifest declares both evaluators, `sam config apply` creates the
`llm_judge` target first; `sam config apply --prune` refuses to delete
an evaluator, or a decision model, that a decision evaluator still
names.

A case is escalated when the model's confidence is below `minConfidence`
or the provider fails with an error other than a rejected or denied API
key or a configuration error. Those two are never escalated, because
that would hide a problem with the decision model's setup. Without
`escalateToEvaluatorId`, any provider failure scores the case as
errored. A run does not start if a decision evaluator's model is
no longer registered or is now a chat model.

Register the decision model first (`kind: model` with `modelType:
decision`).
