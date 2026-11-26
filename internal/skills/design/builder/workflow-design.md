Where this guide says to look something up, load this skill with `load_skill`.

| Topic | Skill |
|---|---|
| Workflow schema | `sam-workflow-schema` |
| Per-tool inputs and result fields | `sam-workflow-schema`, the per-tool table generated from the registry |

`validate_build_manifest` with `full_validation: true` enforces "An Agent Node Guarantees Only the Fields It Declares" for agent nodes defined in the build, in a node `input:`, a switch case `condition`, a map node's items, and `output_mapping` — not in a `when:` or a loop `condition`. It rejects a field reference into a node that declares no output schema, and a field outside the schema the node does declare; a schema's `properties` and `required` list both count as declared. It does not judge a schema built from `allOf`/`oneOf`/`anyOf`/`$ref`, does not flag a node that does not exist inside `coalesce` or `concat`, and reports an undeclared `workflow.input` field as a warning, not an error — unless it is a near-miss spelling of a declared field (case differences count) or is read through `{{workflow_input.output.<field>}}`, where an input the caller omits does not resolve (it fails a node input or map items, stays as template text in `output_mapping`, and makes a switch case not match); it reports both as errors.

Fix such an error without deleting the reference:
- **Node declares no output schema:** add `output_schema_override` (with an `instruction` naming the keys), or map the whole `{{<node>.output}}` — the whole output is valid only in `output_mapping` and a node `input:`.
- **`'<field>' not in <node>'s output schema`:** add the field to the EXISTING schema — the agent's `output_schema`, or the node's existing `output_schema_override`. A new override replaces the agent's whole schema.
- **Switch case or map items:** declare the field; no other fix applies.

### Decision Nodes

A `type: decision` node asks a decision model typed questions about its `input` in one call of a few hundred tokens, and can route on the answer. Prefer it to the classify-agent → `switch` pair from Complex Conditions when the step is a narrow classification: which team owns a ticket, whether a document is privileged, how severe an incident is. Keep open-ended reasoning, tool use and anything that needs the conversation in agent nodes.

It can run only where the deployment has both a decision model and decision nodes enabled. Before proposing one, call `platform_list_components` with `component_type: model`: use a decision node only when a model with `modelType: decision` is listed and `decision_nodes_enabled` is true, and name that model in the workflow's `decision: {model_alias: <name>}` block. Otherwise build the classify-agent → `switch` pair; when the user asked for a decision node, say what is missing. With `decision_nodes_enabled` false, an administrator must enable workflow decision nodes first, and only then can a decision model be added on the Models page (provider TypeSafe, OpenAI or Google AI Studio); with it true but no decision model listed, the user adds one there.

Design it in Phase 2 like a switch:
- one `choice` question names the route (`route_on`), and its options are the `routes` keys; `default` is required when some option has no route (the load fails without it), and only then — behind routes that cover every option it loads but never runs;
- `escalate` must be reached by `min_confidence` (a lower answer escalates) or `on_provider_error: escalate`; it usually names the classify agent you would otherwise have built;
- by default (`escalation_mode: reroute`) the escalate agent's answer still picks the route: it returns a field named after `route_on`, declared as a required `enum` of exactly the `route_on` options — nothing checks the enum before a run, and an answer outside the options fails it (matching trims whitespace and is case-sensitive). Routes wait for it on their own, so do not add it to their `depends_on`, and no route or `default` may be the escalate target;
- set `escalation_mode: handoff` when the escalate target finishes the work itself (it answers the request, or is a human-review step): its reply is the result and no route runs. Handoff is also required when the escalate target is a route or the `default`;
- every route, `default` and `escalate` target lists the decision node in `depends_on`, and a decision node takes no `retry_strategy`;
- with `on_provider_error: escalate`, read `{{<node>.output.answers...}}` only in a route or `default` target of a decision with `escalation_mode: handoff` that is not also the escalate target, or in a node reached only through one: those run only when the model answered. After a provider-failure escalation `answers` is `{}` in every other node that runs: the escalate target and everything after it, nodes that depend on the decision without being one of its targets, joins that also depend on a node outside the routes, and every route and `default` target of a rerouting decision. Reading an answer there loads, but fails the node at run time (and the run, under the default `fail_fast: true`); otherwise keep `on_provider_error: fail`. A low-confidence escalation keeps its answers;
- other questions answered in the same call (a `boolean` urgency flag, a `score` severity) are read downstream as `{{<node>.output.answers.<question>...}}`; ask only what something reads, and pass each answer to the nodes that act on it (for example `urgent: "{{triage.output.answers.urgent.value}}"` in each desk agent's `input`), within the `on_provider_error: escalate` rule above;
- a `boolean` question's `instructions` is a statement the model scores true or false ("The ticket needs an answer today"), not a question;
- the node's `input` is what the model reads, and every question is asked about all of it, so write questions plainly ("Which team should own this ticket?") rather than repeating the input in each one; name a field in backticks only when the input has several and a question is about one of them.

The full field rules, the output shape and the load-time checks are in `sam-workflow-schema` §2.7.
