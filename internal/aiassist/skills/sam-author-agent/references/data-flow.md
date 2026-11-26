# Data flow between workflow steps

How a value gets from one step to the next, and why it sometimes does not arrive. For what a `tool` node
is and the three kinds of value its `input:` accepts, see [workflows.md](workflows.md); this file covers
what those references actually resolve to.

## Reference scopes

| Reference | Resolves to |
|---|---|
| `{{workflow.input.<key>}}` | a value the caller supplied |
| `{{<node-id>.output}}` | a completed node's whole output |
| `{{<node-id>.output.<field>}}` | one field of it |
| `{{workflow.run_id}}` | the current execution's id, available to **any** node |
| `{{workflow.task_id}}` | the current task's id, available to **any** node |

**An agent node guarantees only the fields it declares.** Reference `{{<node-id>.output.<field>}}` into an
agent node — in `output_mapping`, a node's `input:`, a `when:` or a switch/loop condition — only for a field
declared in that node's `output_schema_override` or its agent's `outputSchema` (in `additionalConfigurations`;
the runtime `output_schema` — see [structured-output.md](structured-output.md)), and make the agent's
instruction (or the node's `instruction`) tell it to return exactly those keys. A node that declares no
output schema guarantees nothing: its output is whatever structured result the agent returned, else
`{"text": "..."}`. If the shape is not declared, bind the whole `{{<node-id>.output}}` instead in
`output_mapping` or a node's `input:`; a switch case or a map node's `items` needs a field, so declare it. A
node-level `output_schema_override` replaces the agent's whole schema, so to add a field to a node that
already has one, extend that schema. A field the
agent did not return fails a consuming node's `input:`, and in `output_mapping` is left as literal `{{…}}`
text in a run that reports success (see below). In a `when:`, a switch case, or a loop condition it is
treated as not matched: the node is skipped, the case declines, or the loop stops with
`stopped_reason: condition_unresolved`.

**In a child workflow, `run_id` is the child's own.** A parent that needs its children's rows or files
grouped under a single identity must pass its own id down as an ordinary input.

**The session id is not reachable from a template.** It resolves to nothing rather than erroring, so no
reference can name a file by session or URI. That does **not** mean a later step cannot find a file — see
[artifact-naming.md](artifact-naming.md) for how a downstream step gets a concrete filename instead.

## Whole-string and embedded references resolve differently

- A reference that is the **entire** value keeps the value's **native type** — an array stays an array, a
  number stays a number.
- A reference **inside a longer string** is interpolated to its text form, and the surrounding characters
  are preserved. That is what lets a filename be built inside an embed marker.

## When a value does not arrive

Four situations, four behaviors. Working out which one you are in is most of the debugging:

| Situation | Result |
|---|---|
| Missing `workflow.input` key | resolves to nothing, **no error** — and the key holding the reference is **left out** of the node's input rather than sent as null |
| Reference to a node that never ran | resolves to nothing, no error |
| **Missing field on a node that *did* run** | **hard error** — the node fails, whether the reference is the whole value or embedded in a longer string. The error names the sibling fields (`available fields: …`) only when the upstream is a **built-in tool node**; for an agent, MCP or OpenAPI node it reports `that value has N fields` instead |
| No `input:` block at all | with exactly one dependency, the node receives that dependency's output; otherwise the workflow input; failing that, an empty map — **except on a `map` or `loop` body node** |

The third row is the surprise: a **typo in a field name behaves nothing like a missing input**. It fails the
node loudly in both forms. On a **built-in tool** upstream the error also names the fields that do exist, so
take the replacement from that list rather than guessing again; on any other node type the keys are withheld
deliberately — a node output can be keyed by user data — and you get only a count, so read the tool's own
result shape instead.

For a built-in tool the same mistake is *also* reported earlier, as a **warning** logged when the workflow
starts, naming the node that holds it. It does not block the deploy and it does not stop the workflow: a
green deploy proves the references parse, never that they resolve. The node still fails when the reference
is reached. (For a statically-configured workflow "when the workflow starts" means AWE process startup, not
deploy.) The one exception is the workflow's own `output_mapping`, which resolves after every node has
already succeeded: an unresolved reference there is left verbatim in the output and logged, rather than
throwing away a completed run.

What is still silent is the *other* two rows — a reference to a node that never ran, or a missing
`workflow.input` key. Those resolve to nothing, and inside a longer string they leave the literal `{{…}}`
text in place. That is the case the rest of this section is about.

Conditions are the exception for a missing `workflow.input` key. In a `when:`, a switch case or a loop
condition, a key the caller left out (or sent as null) is not compared as its literal text: the condition
answers only if the rest of it settles the answer, and otherwise does not match — the node is skipped, the
case falls through, the loop stops. So `'{{workflow.input.flag}}' != 'true'` does **not** match when `flag`
was omitted; test an optional flag for its supplied value instead.

The fourth row is the other one — a node with two dependencies and no `input:` silently receives the
**workflow** input rather than either upstream. (A single dependency that was *skipped* has no output, so
that falls back to the workflow input too.) And that fallback chain does **not** apply to the body node of
a `map` or `loop`: omit `input:` there and the node receives an **empty map**, not the current item. Those
nodes must name what they want — `{{_map_item}}` / `{{_map_index}}` in a `map`, `{{_loop_iteration}}` in a
loop — or they run on nothing while reporting success.

## What an unresolvable reference leaves behind

This covers the two silent cases above — an unknown node, or a missing `workflow.input` key. A missing field
on a node that ran fails the node instead of reaching here. The two do **not** leave the same thing behind
when the reference is the whole value:

- Whole-string, **missing `workflow.input` key** → the key is **dropped** from the node's input, as if the
  author had not written it. The receiving tool applies its own default, so an argument the tool treats as
  optional stays optional.
- Whole-string, **unknown node** → nothing, delivered as a null value.
- Embedded in a longer string, either case → **the literal `{{…}}` text stays in place.**

Three qualifications on the dropped key, each of them a case where a null is the safer answer:

- **A supplied null is not a missing key.** A caller who passes `format: null` supplied a value, and it is
  sent as null.
- **A supplied input of the wrong shape is not a missing key either.** `{{workflow.input.user.id}}` where the
  caller passed `user: "12345"` keeps `userId` and sends null, so the tool's own type rejection names the
  argument instead of the argument vanishing.
- **Only the node's input drops keys.** The workflow's own `output_mapping` still emits the field as null,
  because a parent workflow reads that response back with `{{<node>.output.<field>}}` — a strict reference
  that fails on a field which is not there. Inside a **list** the reference likewise becomes a null element
  rather than being dropped, since dropping it would renumber the elements after it.

That literal case fails **asymmetrically**, and it is the dangerous one:

- in an **unquoted** position it is a syntax error at the receiving end — loud and immediate;
- inside **quotes** it is valid content, written silently, indistinguishable from real data.

So when you build a statement as a string — SQL being the common case — put a constraint on the
**receiving** side that rejects a value which never resolved. Checking for the marker alone is not enough:

- match the marker **anywhere** in the value, not just at the start, or a half-resolved value passes;
- **also require a minimum length**, because an interpolation that resolved to nothing leaves an empty
  string, and an empty string contains no marker to match.

## Helpers

| Helper | What it does | What it skips |
|---|---|---|
| `coalesce` | takes the first value that resolved | values that resolved to **nothing** |
| `concat` | joins the values that resolved | values that resolved to **nothing** |

**A helper is a key on the value, not a function inside the braces.** It replaces the value with a
single-key map whose list is the arguments:

```yaml
input:
  title:
    coalesce:
      - "{{fetch.output.title}}"
      - "{{workflow.input.title}}"
      - "Untitled"
```

Writing it the other way — `title: "{{coalesce(fetch.output.title, 'Untitled')}}"` — is the trap. It is not
a syntax error: the whole thing is read as one path name, matches no node, and resolves to **nothing**,
silently. So the guard you reached for to prevent a silent empty value becomes one.

A `coalesce` whose arguments were **all** missing `workflow.input` keys is itself treated as unsupplied, so
its key is dropped from a node input like a direct reference. Any other losing argument — an unknown node, or
an input the caller supplied as null — makes the result an ordinary null on a key that is still sent.

There are no other helpers — no split, no regex, no arithmetic. Note the skip rule precisely: these skip an
**absent** value, not an empty one. An empty string resolved successfully and passes straight through, so
`coalesce` guards against a missing input but will not screen out a blank one. Anything more involved
belongs in an upstream node that returns the value you want, not in a template.
