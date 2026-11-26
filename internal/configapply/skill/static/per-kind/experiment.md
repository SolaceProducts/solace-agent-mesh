An experiment binds a dataset to a target, an agent or a workflow, and a
list of evaluators, parameterising the evaluation run. Trigger an experiment
from the CLI with `sam eval run <experiment-name>` (it polls the run
to completion and prints a results summary).

Cross-resource references — `datasetId`, `evaluatorIds`,
`primaryEvaluatorId` — accept *names*, not platform UUIDs. The
reconciler resolves names to IDs at apply time using the platform's
list of datasets and evaluators; a dangling reference hard-errors at
plan time with a clear message naming the missing resource.

The optional `models` field pins which model configs the agent runs
under: a list (max 3) of maps, each keyed by `modelConfigId` naming a
model config. Omit it to evaluate the agent's configured model.

`runAs` names the service account the experiment's runs execute as. Give it a
bare name (`runAs: reporting-evals`) and the platform stores the subject
`system:reporting-evals`; an already-prefixed value is kept as-is, so both
spellings of the same principal read as unchanged. The manifest is the
desired state: omitting `runAs` reverts the experiment to the built-in
eval service account, so declare it explicitly on any experiment that needs a
scoped principal.

That built-in principal is `system:eval`, which holds `agent:*:invoke`,
`workflow:*:invoke`, and `tool:*:*`.

Runs carry exactly the scopes that principal holds. The platform resolves it
from one merged view — the baked-in system defaults, the operator's
authorization config, and service accounts and role grants provisioned as platform
records — so a `serviceAccount` you declare in the same manifest takes effect
without touching any mounted YAML.

The named service account does not have to exist when you apply: plan does not
cross-reference it. It does have to hold a role by the time a run starts. A
`runAs` that resolves to no scopes fails the run with a message naming the
principal, rather than executing it under a broader grant, so declare the
`serviceAccount` in the same manifest to avoid the gap. Check the platform log
line `execution service: run publishing as principal` for the `runAs` a run
actually used, and the signer's WARN for a refusal.

`runAs` cannot name a built-in principal an entrypoint runs as (`system:default`,
`system:channel`); `sam config plan` flags it and the platform rejects the write. Tool credentials
are keyed by principal, so an experiment naming one would share a credential
slot with live traffic — and the Connect Tools panel would then offer to
disconnect it out from under that traffic. Declare a service account of your own
instead. Note that this rejection covers only the built-ins: reusing one custom
service account across a live entrypoint's `run_as` and an experiment's `runAs`
collides on the same credential key.

Experiments depend on their referenced datasets and evaluators, so
list them in the manifest's `resources:` block under
`datasets:` and `evaluators:` (or import the relevant sources). The
reconciler applies datasets and evaluators before experiments so a
fresh apply lands cleanly.

### Workflow targets (Early Access)

Set `targetWorkflow` instead of `targetAgent` to evaluate a workflow; set
one or the other, never both. It takes the workflow's name. `sam config
plan` accepts a workflow declared in the same manifest or one already on
the platform, and fails on any other name.

Each example of a workflow experiment's dataset gives the workflow its
input as `inputData`, a JSON object matching the workflow's input schema
(the dataset's `input_data` column). An example with only a prompt sends
the prompt as the workflow's text input. The response the evaluators grade
is the workflow's output.

The platform checks a workflow experiment when it is saved and refuses it
when the workflow does not exist, when the `runAs` principal cannot invoke
it (`workflow:*:invoke` covers every workflow), or when an example's input
does not fit the workflow's input schema or attaches an input file. The
error names each refused example by its sequence number.

On a workflow target, `models` sets the model for the workflow's agent
steps; omit it and each step runs on its own configured model.

Two built-in evaluators check expectations stored on each example:
`Workflow Path` grades the steps a workflow ran against the dataset's
`expected_path` column, and `Output Facts` checks the response against its
`expected_facts` column. Name them in `evaluatorIds` like any other
evaluator.

```yaml
kind: experiment
name: order-exceptions-regression
description: Order exception workflow against the regression set.
spec:
  datasetId: order-exceptions
  targetWorkflow: order_exception_handler
  evaluatorIds: ["Workflow Path", "Output Facts"]
  primaryEvaluatorId: Workflow Path
```
