# What a workflow step guarantees

Differences between node types that change how you design a DAG. For the node types themselves and the
authoring paths, see [workflows.md](workflows.md).

## Retry does not reach a tool node

**Do not rely on workflow retry reaching a `type: tool` node — it does not.** A `retry_strategy:` block on
a tool node parses and is then ignored, as is any key a node type does not use. Nothing warns you, so
**its presence in your YAML is not evidence that it applies.**

Agent nodes can retry, but only when a `retry_strategy` applies — set on the node, or as the workflow's
default. Nothing retries by default. The default policy, `OnFailure`, retries a failure the agent reports
and **not** a timeout or system error; `OnError` covers those and `Always` covers both. A DAG mixing both
node types therefore has uneven resilience: the model-driven steps can get another attempt and the
deterministic ones cannot. Either put the retry-worthy work in an agent node with a `retry_strategy`, or
make the tool idempotent so the whole workflow can simply be run again.

## The workflow's input schema is enforced; its output schema is not

A workflow's `input_schema` — the one describing what a caller passes in — is checked against every run's
input **before any node runs**. Input that does not satisfy it fails the run with `Input validation
failed:` followed by every violated field (a missing `required:` key, a wrong type; truncated if very long)
and, when the schema lists its fields under `properties`, the required and optional fields to send, and no
node and no `on_exit` handler runs. A workflow with no `input_schema` is not checked.

A caller that can only send text still works. When the workflow declares an `input_schema` and the message
text is a JSON object, that object is the workflow input, so a chat message of
`{"destination":"Porto","needsHotel":true}` supplies both fields. The exception: when the schema also accepts
`{"text": "<message>"}` and the object either fails the schema or names a field the schema does not declare
under `properties`, the message is read as `{"text": "<message>"}`, as it always was. Any other text is read
as `{"text": "<message>"}` too. Whichever form is chosen is checked like any other input, so prose passes
only when the schema accepts `{"text": "<message>"}`, for example when `text` is the only required field.
Without an `input_schema`, text is always `{"text": "<message>"}`.

Two limits. An `input_schema` that does not compile is not enforced; the workflow logs a warning when it
starts, so check that a schema you wrote actually rejects bad input. And a JSON Schema `default:` is not
applied to a missing field.

The workflow's own `output_schema` is published to callers and to the visualization and is **not checked
at run time** against what the workflow returns.

Agent nodes are different again:

- an **agent node's input** schema is validated by the agent receiving the call, which fails with an
  input-validation error;
- an **agent node's output** schema is checked, and the model is asked again when its answer does not
  conform — see [structured-output.md](structured-output.md). That is validate-and-retry against a model
  rather than a hard guarantee, so put a real check downstream if a later step depends on the shape: a
  `switch` node that routes bad values away, or a tool that rejects them outright.

## Failure propagation is asymmetric by depth

`fail_fast` defaults to **on**, where a node failure ends the workflow once that node's retries are
exhausted. (A node failing inside an exit handler never fails the workflow.) Turn fail-fast off and two
different rules apply:

- a **direct** dependent of the failed node is skipped, unconditionally — even if its other dependencies
  all succeeded;
- a node **further downstream** is skipped only if **every one** of its inputs was skipped.

So a node with one dead branch and one surviving branch **runs**, receiving nothing for the dead one. It
succeeds, and its result quietly omits whatever that branch was carrying. There is **no** way to mark an
output optional. The skip itself is logged only at debug level — though the run view does show the node as
**skipped**, which is the fastest way to spot it.

⇒ **Guard every cross-branch reference with `coalesce`** (it is a key on the value, not a function inside
the braces — see [data-flow.md](data-flow.md#helpers) for the shape, and note that guessing the
function-call form fails silently), and never assume a skipped upstream will stop the step that consumes
it.

## Mixing agent and tool nodes

Feed an agent's structured output into a tool node with `{{<agent-node>.output.<field>}}`. Keep that
agent's output schema tight — a tool has no judgment to fall back on, so it needs predictable types. The
field must be declared in that schema (the node's `output_schema_override` or the agent's `outputSchema` in
`additionalConfigurations`, the runtime `output_schema` — see [structured-output.md](structured-output.md))
and named in the agent's instruction; an agent node with no declared schema guarantees no fields, so pass
its whole `{{<agent-node>.output}}` instead (see [data-flow.md](data-flow.md)).

## Which steps actually need a model?

Fetching, transforming, writing and rendering do not. Reserve agent nodes for judgment: interpreting free
text, choosing between options, summarizing.

**The cost difference is in time, not just tokens.** Every agent-driven step is a model call, and most of
its wall-clock time goes to the model deciding what to do and describing what it did, not to doing the
work. Replacing agent nodes with tool nodes where no judgment is required can cut a multi-step workflow's
runtime by orders of magnitude — the tool-only path makes zero model calls. Reserve agent nodes for the
steps that need judgment and let tool nodes carry the rest.
