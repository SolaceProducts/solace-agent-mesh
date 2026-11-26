A workflow is a deterministic DAG of agent calls, switches, maps, and
loops. The `appConfig` field carries the workflow definition itself;
its shape is documented by the workflow-engine reference rather than
this CLI surface. After apply, a workflow runs through the deploy
phase automatically unless `--no-deploy` is set.

## The workflow body lives under `spec.appConfig`

`name` and `description` are the only top-level (header) fields; the
workflow definition — `nodes`, `output_mapping`, timeouts, etc. — is the
body. Write the body under `spec.appConfig`: that is the canonical shape
and the one this reference uses throughout. A flat `spec.{nodes,
output_mapping, …}` is also accepted — it is the legacy shape and what
`sam config pull` emits for a definition it can hold — and for those both
produce an identical wire payload, so never rewrap a pulled file just to
match this page.

The flat shape has no place for the definition's own `description` or
`name`: without `spec.appConfig` those two keys are the header's, so a flat
`spec.description` is not applied and `sam config plan` warns about it.
`sam config pull` writes a workflow whose definition has either key under
`spec.appConfig`.

The two shapes are mutually exclusive. When `spec.appConfig` is present,
every other key under `spec:` is **silently dropped** — no plan diff, no
error. Put the whole body inside the wrapper, or none of it.

What does fail is `appConfig:` (or `nodes:`) at the document top level
with no `spec:` parent: the resolver reads an empty spec, emits an empty
body, and deploy fails late with a confusing error such as
`output_mapping is required` even though it is present in your file.

```yaml
kind: workflow
name: MyWorkflow                 # header: display label only (see addressing below)
description: One-line summary of what this workflow does.
spec:                            # REQUIRED — the body lives under here
  appConfig:                     # REQUIRED — the workflow definition
    nodes:
      - id: extract
        type: agent
        agent_name: Extractor    # an agent's or a link agent's name:
        input:
          data: "{{workflow.input.data}}"
      - id: summarize
        type: agent
        agent_name: Summarizer
        depends_on: [extract]
        input:
          extracted: "{{extract.output}}"
    output_mapping:              # REQUIRED
      result: "{{summarize.output}}"
```

## What `sam config plan` checks inside the workflow body

Plan diffs the resource envelope and does check references inside the
body — each node's `agent_name`/`workflow_name`, toolset names, and
connector names (it also rejects the retired `connector_refs`). It also
checks that the workflow declares a source for its tool nodes, and runs
the real workflow parser over each tool node, grading the node's
`input:` keys and its `{{node.output.<field>}}` references against the
built-in tool's own schemas:

- A `type: tool` node naming a `tool_name` in a workflow that declares
  none of `tools:`, `toolsets:` or `connectors:` and has no node binding
  a `connector:` **fails the plan** when the workflow is being created or
  changed — that node can never run, because the engine builds its tool
  set only from those sources. A node-level `connector:` anywhere in the
  workflow counts as a source, because deploy adds every tool of that
  connector for all tool nodes — so once any node binds a connector the
  plan-time check cannot catch any other tool node whose `tool_name` is
  undeclared (a built-in missing from `tools:`, or a mis-named connector
  tool; see `connector:` below). Deploy catches it once hydration has made
  every source known; where a source is still opaque it fails at run time as
  an unknown tool. The error names each node and the
  declaration to add: `toolsets: [<Toolset>]` for an uploaded toolset's
  `<Toolset>__<tool>`, a `tools:` builtin entry with the full name for a
  skill tool or a built-in, a `tools:` entry with `tool_type: mcp` for an
  MCP tool, and `connector: <name>` on the node for a platform
  connector's tool (see the skill-tool limit under `tools` below). An
  unchanged workflow gets a warning instead. It is
  reported together with any argument failures below. Saves through the
  REST workflow API are accepted as drafts; plan, Build & Activate and
  deploy are where a workflow with no tool source is refused.
- A `type: tool` node whose `tool_name` no declared tool source can
  register **fails the plan** where its failure can stop a run (only the
  unchanged-workflow warning above softens it; no feature setting does),
  and the platform refuses the same workflow at save, Build & Activate and
  deploy. A node naming a tool that nothing registers, or missing a
  required parameter, is refused where its failure can stop a run, and is
  reported as a warning, with the reason, where it cannot: the node is an
  `on_exit` handler that no other node runs; its `when:` is the literal
  `false`; it waits only for nodes that never run, or for a `switch` that
  cannot select it (its cases are the literal `false`, come after a case
  that is the literal `true`, or it is the `default` beside one); it is the
  template of a `map` whose `items` is a literal empty list; or it is the
  body of a `loop`, or the template of a `map`, that itself never runs. A
  `when:`, a switch condition or an `items` expression that reads data is
  never judged, and a loop body runs at least once, so a node behind those
  stays refused. So does a workflow that sets `fail_fast: false`, because
  every node that waits for the failing one is skipped and the workflow
  finishes as a success without doing that work. The tool-name check only
  refuses when every source names its tools up front: built-in entries and an `mcp`
  entry pinned by `tool_name`, `allow_list` or an inline `manifest` (a
  `manifest_file` is not read). An `mcp` entry's registered name is the
  server's name with characters outside `[A-Za-z0-9_]` replaced by `_`,
  and the `tool_name_prefix` put in front of it joined by `_` unless the
  prefix already ends in one (`records` and `get_record` register
  `records_get_record`). That is the name the node must use; the error
  lists the registered names. An `mcp` entry that pins nothing lists its
  tools only once its server is connected, and nothing at plan, save or
  deploy connects to a server, so a node whose name nothing else registers
  is not checked. When that is the only reason the names cannot be judged,
  the plan passes with a warning that names those nodes, and the name fails
  at run time if the server does not register it. Pin the entry with
  `tool_name`, `allow_list` or an inline `manifest` to have the names
  checked. An
  `openapi` entry, a built-in group (its members are whatever build loads
  the workflow), `toolsets:`, `connectors:`, any node-level `connector:`,
  and any name, prefix or entry written with a `${VAR}` or a `{{…}}`
  template, and an `mcp` entry that names its tools in a `manifest_file`,
  make the check stay silent, because those names are only known once
  deployed.
- An `input:` key the built-in does not declare **fails the plan** when
  the workflow is being created or changed and the node can stop a run (the
  same test as above; where it cannot, the plan warns), and a failed plan
  aborts the whole manifest before anything is applied. That key is the
  argument the tool would otherwise drop in silence. A workflow this plan
  is *not* changing is reported as a warning instead — the mistake is already
  deployed and already fails at dispatch, so aborting would only block
  whatever you came to apply.
- A `{{node.output.<field>}}` reference the built-in cannot produce is
  reported as a **warning**, never a failure — failing here would block a
  workflow that has been deployed and running since before the check
  existed.

The two checks treat an unproven schema differently, so read a green
plan carefully. A node carrying `connector:` and a built-in the
plan-time registry does not carry are **skipped by both** — silence, not
approval. But a name an `mcp`/`openapi` entry or a `connectors:` block
could supply still gets an argument **warning**, downgraded from a
failure only because the built-in's schema may not be the one that runs;
the *reference* check goes quiet on those instead.

A `toolsets:` list does **not** soften either check. Whatever the
platform expands it into cannot supply a bare built-in name carrying
different parameters — a built-in group expands only into built-ins, a
custom package is namespaced `<package>__<tool>`, and a hand-written
`tools:` entry with an uploaded toolset's expanded `<package>__<tool>`
name is dropped in favour of the expanded entry — and fails deploy as a
collision when declared beside a nested `workflow:` block — so never
hand-write one.

The schemas being graded against are the ones compiled into your `sam`
binary, so a CLI older than the platform can fail a parameter the
platform has since added — the failure message says so.

What plan still does **not** do is reject a structurally invalid body. A
body that fails to parse (missing `output_mapping`, a mis-wrapped body,
a bad node shape) is reported as a warning naming the workflow and the
parse error — the tool-node checks are skipped for it — and the
structural failure itself surfaces at `apply`→deploy where the full
body is parsed. The exceptions are a dependency cycle, a node that
depends on itself and a node id defined twice: saving refuses those with
a 422 that names the workflow and the node ids, so `apply` stops there
rather than at deploy. Treat a green plan as "the references resolve,"
not "the workflow is valid." Validate structure by applying to a dev
target.

Both tool-node checks also run past `apply`, so a green deploy is not the
end of it: the undeclared `input:` key fails that node when it
dispatches, and the unproducible reference is logged as a warning when
the workflow starts, then fails its node at run time when the reference
resolves. Read the log after applying a workflow with tool nodes.

## Addressing a deployed workflow

The workflow's broker card name is generated: `workflow_<id>` — the
workflow's UUID with `-` replaced by `_`, the same scheme
platform-deployed **agents** use (`agent_<id>`). Your config `name:` is
published as that card's *display name*, and entrypoints and workflow
nodes resolve display names for you, so submitting a task to the friendly
`name:` does work. Target a workflow from a `workflow` node: an agent node
considers only agent cards, so naming a workflow from one fails with a
dispatch error telling you to use a workflow node.

Three places where the generated name still matters:

- **RBAC.** A deployed workflow's invoke scope is
  `workflow:<dashed-uuid>:invoke` — the UUID form, never the friendly
  name. A role granting `workflow:<name>:invoke` will not authorize it.
- **Anything addressing the broker topic directly**, which uses the card
  name verbatim.
- **An agent-node target that resolves to nothing does not error.** The
  task publishes to a topic with no subscriber and times out silently
  once the node's timeout expires — five minutes by default — so a typo
  looks like a long hang rather than a "not found." A **workflow**-node
  target that resolves to no deployed workflow instead fails fast with an
  explicit dispatch error in most cases. The silent, timeout-prone
  publish is reserved for four routable exceptions: an instance-name-form
  target, a self-reference, a YAML-authored parent, or a **cold card
  cache** — no agent cards discovered yet on this instance, which is the
  one you are most likely to hit in practice, shortly after a restart and
  before the first card broadcast lands.

Duplicate display names behave differently by caller. An entrypoint
resolves them to the lowest-sorted card name. An agent node considers
only agent cards and a workflow node only workflow cards; Test-mode
cards are never targets. A node whose name matches only a card of the
other kind fails with a dispatch error telling you to use the other node
type. A node whose name matches more than one card fails with a dispatch
error naming every matching card. A component that is no longer running
can leave its card in the workflow's discovery cache; restart the
workflow to drop it. Otherwise, delete or rename the stale component, or
target the generated card name. An orphaned instance that is still
running keeps republishing its card, so stop it first by restarting the
process that hosts it. Read the generated name from the `name` field of the
card the workflow publishes (`GET /api/v1/agentCards`) — the Agent Mesh
UI's **Agents** and **Workflows** tabs show the friendly display name, not
this one.

## Authoring keys vs. the field tables below

A few node fields are authored under a short YAML key that differs from
the field's internal name. Write these keys:

- **map** — `node:` (the per-item template node) and exactly one of
  `items:` / `with_items:` / `with_param:` (the list to iterate); *not*
  `template_node`/`items_expression`.
- **switch** — `cases: [{condition, node}]` and `default:`; `when` is an
  alias for `condition` and `then` for `node`; *not* `default_case`.
- **loop** — `node:` (body), `condition:` (required), `max_iterations:`,
  `delay:`; *not* `body_node`.
- **decision** *(Early Access)* — `questions:` is a map keyed by question
  name, not a list, and each question carries its own `instructions:`; a
  node-level `instruction:` is ignored. A decision node takes no
  `retry_strategy:` (the provider already retries rate-limit and overload
  responses, and the workflow fails to load if one is set).
  `images:` (only for a decision model that reads images: a Jev model, an
  OpenAI or Gemini decision model, or `openai/gpt-6-luna-decisions` through OpenRouter) is a list of image artifact
  references or templates, not a map; the `decision:` block caps them with
  `max_images:` (default 5, at most 128) and `max_image_bytes:` (default
  20 MiB, at most 64 MiB), where `0` means the default. Going over a cap is
  `input_too_large`, and more entries without a `{{...}}` template than
  `max_images` fail the workflow at load.
- **agent, workflow, switch, loop and map nodes** — `retry_strategy:` for
  the retry policy; a tool node never retries. The parser only accepts
  `retry_strategy` (or `retryStrategy`); a node written with `retry:` is
  accepted and **silently gets no retry policy**.
- **every node type** — node `timeout:` is a duration string (`"30s"`,
  `"5m"`).

**Read `design/workflow-design.md` before hand-authoring a workflow.** It
carries the worked YAML for every node type with the correct keys, the
retry/timeout rules (including the fact that retry never reaches a tool
node), and how to reshape data across a tool-to-tool seam. This page is
the schema and the key names; that one is how to assemble them.

`type: tool` nodes call tools directly (no agent, no LLM). Three things
can feed them, and they do not all sit at the same level:

- **`toolsets`** — toolset or built-in-group names, the same names agents
  accept in `spec.toolsets:`. Expanded into tools at deploy time; a
  custom toolset's tools are addressed as `<toolset>__<tool>` in the
  node's `tool_name:`, while a built-in group's tools keep their bare
  names. Placement-insensitive.
- **`connector:` on the tool node** — binds one platform connector to
  that node (plus optional `tool:` to pick one of its tools by base
  name), resolved at deploy time. Give every node that calls a connector
  tool its own `connector:`. For an `mcp` connector `tool:` is the tool's name on the MCP server, checked
  against the connector's stored tool list (its `manifest` narrowed by `selected_tools`, `allow_list` and
  `deny_list`) and required unless that list holds one tool; the node is addressed under the connector's
  `tool_name_prefix`. Deploy adds every tool of that connector to
  the workflow, but naming one by `tool_name` on another node works only
  with the exact emitted name — an API connector's operation name (e.g.
  `get_pet`) or a custom `tool_name` the connector emits as-is. Other
  connector tools carry a suffix from the connector id (e.g.
  `sql_query_1a2b3c4d`), so a guessed name passes the plan; deploy refuses
  it where every source is then known, and otherwise it fails at run time as
  an unknown tool. This is the only way to reference a
  connector from the connector store. A `web_search` connector is refused
  on a tool node at save; call web search from an `agent` node whose agent
  lists the connector under `spec.connectors`.
  For an API connector with an **uploaded spec** (`specification_file`),
  the platform grades the node's `input:` keys against the selected
  operation's real parameters at save and at deploy: a required parameter
  the input never names is refused with the operation's parameter names
  listed (use the spec's names, snake_cased: `carrierCode` is
  `carrier_code`, and a name that is a Python keyword gets a `param_`
  prefix: `from` is `param_from`; a request body's properties are
  parameters too). Like an unknown tool name, it is refused where the
  node's failure can stop a run and only warned about where it cannot. A key
  the operation does not declare is reported, not refused: the call goes
  out without it, so its value is silently dropped, and the platform logs a
  warning while the Builder's validation shows one. A `{{…}}` value, an
  omitted optional parameter, a node with no `input:` and a whole-`input:`
  `coalesce` or `concat` are never refused for their input keys, nor is a
  node whose keys are written as a template or a `${VAR}`, or an operation
  whose parameters the spec does not settle (request-body media types
  that disagree, or a tool name another operation also produces). A
  parameter declared on the operation's path item is one of its
  parameters, and an operation-level parameter of the same name and
  location replaces it (header names compare without regard to case).
  A node bound to an `allow_list` entry the uploaded spec has no operation
  for is refused at save and deploy, with or without an `input:`, where its
  failure can stop a run: the error names the entry and lists the spec's
  operations, because no tool is registered under that name; the Builder's
  validation reports it as an error too. Where the node cannot stop a run,
  the save and deploy go through and the Builder warns. That includes an
  automatic redeploy after an unrelated edit to the connector: a stored
  workflow whose node now binds an entry the spec lacks keeps its previous
  instance serving, with a failed deployment row carrying the message, only
  when the node can stop a run.
  `sam config plan` cannot see a connector's spec, so it does not run this
  check; the failure shows at `apply` as the workflow's failed row. A
  connector with only a `specification_url` and no `allow_list` is refused
  at deploy, because the platform cannot list its tool names; with an
  `allow_list` it deploys, and its input keys go unchecked.
  Placement-insensitive. A workflow-level `connectors:` list is also read
  (same two levels as `tools:` below), but it declares a connector inline
  rather than referencing one from the connector store.
- **`tools`** — a raw tool list in the same shape agents accept (an
  inline MCP server, for instance). The runtime reads it from **either**
  level: nested inside `workflow:` alongside `nodes:`, or as a sibling of
  `workflow:` in the deployed `app_config`. A skill tool goes here as
  `{tool_type: builtin, tool_name: <skill>__<tool>}`, but it gets none of
  the skill's stored platform config or credentials — workflows have no
  skill hydration. Put non-secret values in an inline `tool_config:` on
  the entry; when the skill needs credentials, use an agent node whose
  agent has the skill attached.

A tool node's **arguments** go in its `input:` map, and what a tool
returns is reachable only as `{{<node>.output.<key>}}` from the tool's
structured data. A file the tool wrote is reachable too: the framework adds
`{{<node>.output.created_artifact}}`, the name this call saved pinned to the
version it wrote (`report.json:3`), plus `created_artifact_name` (the bare
name) and `created_artifact_version` (the number), so a downstream node can
read it without the producing tool having reported it. Pass
`created_artifact` unchanged to a step that reads the file — built-in tools
that read an artifact accept `name:version` — and use
`created_artifact_name` where a step needs the filename itself, such as an
output filename or `delete_artifact`. The pin is what keeps concurrent `map`
iterations, which share one artifact session, from reading a sibling's file
of the same name. The keys are present only when the call saved **exactly
one** — a call that saved several has none, because the template language
cannot index a list — and a tool publishing its own key of any of these
names keeps it. The *contents* of a file are never in this namespace; pass the name to a
tool that reads it. The exception is `query_data_with_sql` and
`transform_data_with_jmespath`: their rows come back inline as
`result_preview`, limited by default to 50 rows and 2048 bytes (a single
oversized row is still returned whole), with `result_truncated` set when the
preview was cut. When a map's `items:` reads
`{{<node>.output.result_preview}}`, raise `max_result_preview_rows` /
`max_result_preview_bytes` in that tool's `tools:` entry `tool_config`, or the
map silently iterates over only the rows that fit. When the upstream tool's output
shape does not match the downstream tool's parameters, no `{{…}}`
expression can adapt it: see "Reshape Data Between Tool Nodes" in
`design/workflow-design.md`, which covers the `extract_fields` route and
the way a shape mismatch fails as a *successful* node.

Both placements work, so the nesting no longer has to be reasoned about:

```yaml
spec:
  appConfig:
    tools:                         # sibling of workflow: (also valid nested inside it)
      - tool_type: mcp
        tool_name: search
        connection_params: { ... }
    workflow:
      nodes:
        - id: call
          type: tool
          tool_name: search
          input:
            q: "{{workflow.input.q}}"
      output_mapping:
        r: "{{call.output}}"
```

Deploy-time toolset and connector hydration writes its generated entries
at the sibling level, so a workflow can end up with both lists populated.
When the same tool name appears at both, the sibling (`app_config`) entry
wins and the nested copy is dropped — the startup log names it. That
de-duplication matches on name and applies to `tool_type: builtin`
entries only; connector-backed entries (MCP, OpenAPI) declared at both
levels are both kept, and collide later at registration instead. One rule
still bites in the flat shape: `spec.tools:` beside `spec.workflow:`
works, `spec.tools:` beside `spec.appConfig:` is silently dropped.

The example above keeps a `workflow:` wrapper because it is showing the
sibling placement. That wrapper is optional: the body may also sit
directly under `appConfig:`, in which case `tools:` goes alongside
`nodes:` and `output_mapping:` there — matching the `appConfig:` field
table below, which lists all three as siblings. Both forms deploy. What
must not happen is splitting one body across the two, since anything
outside the `appConfig:` wrapper is dropped without a diff (see the top
of this page).

### `hil:` on a workflow's `tools:` entry

A `tools:` entry may carry the same `hil:` block an agent's entry takes. On a
workflow it is parsed and validated, so a malformed block fails config load.
A workflow has no way to ask a person for approval, so a `type: tool` node
whose call the gate covers fails instead of running the tool, and a gate a
bound connector sets on its tool counts the same way. The workflow names the
affected tools at startup and records each refused call in the audit log. If
a tool needs human approval, call it from an agent node, where the person is
asked.

See the toolset kind reference for a worked workflow example.
