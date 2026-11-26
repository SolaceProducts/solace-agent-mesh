---
name: sam-workflow-schema
description: JSON Schema for Solace Agent Mesh workflow configuration — node types, templates, conditions, retry, timeouts, exit handlers, and examples.
---

Solace Agent Mesh workflow configuration schema and practical guide. Workflows are DAGs of nodes with deterministic control flow. They publish an agent card and look like any other agent to the system.

For **design guidance** see `sam-workflow-design`. For **agent config** within nodes see `sam-agent-schema`.

## Schema

```yaml
{{SCHEMA:workflow}}
```

---

## 1. Workflow Identity and Required Fields

> **Note:** System fields like `namespace`, `session_service`, and `artifact_service` are injected by the platform at deployment time and must not be included in generated configs.

```yaml
apps:
  - name: my_workflow
    app_config:
      agent_name: my_workflow          # Required. Unique identifier.
      agent_type: workflow             # Required. Must be "workflow".
      description: "..."              # Optional.
      version: "1.0"                  # Optional.
      nodes: [...]                    # Required. At least one node.
      output_mapping: {...}           # Required. Maps workflow output.
```

**Rules:**
- `agent_type: workflow` is required — this is what distinguishes a workflow from an agent.
- `agent_name` must be unique across the deployment, same as agents. Convention: `snake_case`.
- `nodes` and `output_mapping` are both required — a workflow must have at least one node and must define how its output is constructed.

---

## 2. Node Types

Every node has `id` (unique within the workflow) and `type`. Different types require different fields.

### 2.1 Agent Node

Invokes a deployed agent. The most common node type.

```yaml
- id: analyze
  type: agent
  agent_name: Analyzer              # Required. Must match a deployed agent.
  depends_on: [extract]             # Nodes that must complete first.
  input:                            # Data passed to the agent. Values can be templates.
    data: "{{extract.output.text}}" # `extract` declares `text` and `has_data` in its output schema (its agent's
                                    # `output_schema` or an `output_schema_override` on the `extract` node).
  instruction: "Focus on security issues only."  # Optional. Appended to agent's instruction.
  timeout: "2m"                     # Optional. Overrides default_node_timeout.
  when: "'{{extract.output.has_data}}' == 'true'"  # Optional. Skip if false. `has_data` is declared by `extract`.
  retry_strategy:                   # Optional. Overrides workflow-level retry.
    limit: 2
    retry_policy: OnError
  input_schema_override: {...}      # Optional. Override agent's input_schema for this invocation.
  output_schema_override: {...}     # Optional. Override agent's output_schema for this invocation.
```

**Agent node requirements:**
- The agent referenced by `agent_name` must be deployed separately as its own agent config.
- Workflow node agents should use `supports_streaming: false` and define `output_schema` so the workflow can reliably consume structured output.
- 🛑 **Reference `{{<id>.output.<field>}}` only for a field the node declares** — in its `output_schema_override` or its agent's `output_schema` — and make the agent's `instruction` (or the node's `instruction`) tell it to return exactly those keys. An agent node that declares no output schema guarantees no fields: its output is whatever structured result the agent returned, else `{"text": "..."}` — so not even `text` is guaranteed: an agent that returns a structured result has no `text`. When the shape is not declared, bind the whole `{{<id>.output}}` in `output_mapping` or a node `input:`; a switch case or a map node's `items` needs a field, so declare it. To add a field to a node that already has a schema, extend that schema — a new `output_schema_override` replaces the agent's whole schema. This applies to `output_mapping`, node `input:`, `when:`, switch/loop conditions and the `<route_on>` field a rerouting decision node reads from its escalate target alike. Build validation (`validate_build_manifest` with `full_validation: true`) rejects a field reference into an agent node in the build that declares no output schema, and a field outside the schema it does declare (`properties` and `required` both count as declared) — in a node `input:`, the `<route_on>` field a rerouting decision node reads from its escalate agent, a switch case `condition`, a map node's items, and `output_mapping`, but not in a `when:` or a loop `condition`. Only agent nodes are field-checked; a schema built from `allOf`/`oneOf`/`anyOf`/`$ref` is not judged; a node that does not exist inside `coalesce` or `concat` is not an error; an undeclared `workflow.input` field is a warning, not an error — unless it is a near-miss spelling of a declared field (case differences count) or is read through `{{workflow_input.output.<field>}}`, where an input the caller omits does not resolve (it fails a node input or map items, stays as template text in `output_mapping`, and makes a switch case not match); both are errors.
- Workflow node agents should NOT include `ask_user_question` tool — they run autonomously within the workflow.
- When building a workflow, the builder must generate **both** the workflow config AND a separate agent config for each distinct `agent_name` referenced in nodes.

### 2.2 Switch Node

Routes execution to one of several target nodes based on conditions. First matching case wins.

```yaml
- id: route
  type: switch
  depends_on: [classify]            # `category` declared in classify's output schema.
  cases:                            # Required. Evaluated in order.
    - condition: "'{{classify.output.category}}' == 'premium'"
      node: premium_handler         # Target node ID.
    - condition: "'{{classify.output.category}}' == 'standard'"
      node: standard_handler
  default: fallback_handler         # Optional. Target if no case matches.
```

**Switch rules:**
- Cases are evaluated in order — first match wins.
- `condition` uses expr-lang/expr syntax (see Section 6).
- `node` (alias: `then`) specifies the target node ID.
- Target nodes must include the switch node in their `depends_on` array.
- Only the matched target executes; other targets are skipped.
- The switch node's own output is `{selected_branch, selected_case_index, cases_unresolved}`. `selected_branch` is `""` and `selected_case_index` is `-1` when nothing matched and there is no `default`; `cases_unresolved` counts cases that referenced a field no node produced — whether they abstained or answered on the arms that did resolve. Any non-zero count means part of what you wrote is dead; a non-zero count next to an empty `selected_branch` means the switch did not make the decision you wrote at all.
- Use `coalesce` in `output_mapping` to converge results from switch branches (see Section 4).

### 2.3 Map Node

Iterates over a collection, executing a template node for each item in parallel.

```yaml
- id: process_all
  type: map
  depends_on: [split]               # `items` declared in split's output schema.
  node: process_one                 # Required. Template node ID to execute per item.
  items: "{{split.output.items}}"   # Required. Template expression for the collection.
  concurrency_limit: 5              # Optional. Max parallel executions. 0 = unlimited.
  max_items: 100                    # Optional. Safety limit (default 100).
```

The template node receives special variables:
- `{{_map_item}}` — the current item from the collection
- `{{_map_index}}` — the current index (0-based)

```yaml
- id: process_one
  type: agent
  agent_name: ItemProcessor
  input:
    item: "{{_map_item}}"
    index: "{{_map_index}}"
```

**Map rules:**
- The template node (`process_one`) should NOT appear in other nodes' `depends_on` — it's invoked by the map node.
- The map node's output is an array of all template node outputs, in order.
- Set exactly one items source: `items`, `with_param` (alias `withParam`), or `with_items` (alias `withItems`). Each takes a template expression or a literal list; `with_items` is the conventional key for a literal list.
- No other key is accepted — a map node with `items_expression` fails to parse with `requires one of: 'items', 'withParam', or 'withItems'`.

### 2.4 Loop Node

Repeats a body node while a condition is true.

```yaml
- id: refine
  type: loop
  node: refine_step                 # Required. Body node ID to repeat.
  condition: "{{refine_step.output.quality_score}} < 0.9"  # Required. Continue while true.
  max_iterations: 5                 # Optional. Safety limit (default 100).
  delay: "1s"                       # Optional. Delay between iterations.
```

The body node receives:
- `{{_loop_iteration}}` — current iteration counter (0-based)

```yaml
- id: refine_step
  type: agent
  agent_name: Refiner               # `quality_score` and `result` declared in refine_step's output schema.
  input:
    previous: "{{refine_step.output.result}}"
    iteration: "{{_loop_iteration}}"
```

**Loop rules:**
- **Always set `max_iterations`** to prevent infinite loops.
- The condition is checked after each iteration — the body always runs at least once.
- The loop node's output is **not** the body's output. It is `{iterations_completed, stopped_reason}`, where `stopped_reason` is one of `condition_false` (including a condition an omitted `workflow.input` key decided — see §5), `max_iterations` or `condition_unresolved` (the condition referenced a field no node produced). The body's own output is addressed under the **body node's** id.
- The body node should NOT appear in other nodes' `depends_on`.

### 2.5 Workflow Node

Invokes another deployed workflow as a nested sub-workflow.

```yaml
- id: validate
  type: workflow
  workflow_name: ValidationWorkflow  # Required. Must match a deployed workflow.
  depends_on: [prepare]
  input:
    data: "{{prepare.output}}"
```

**Workflow node rules:**
- The referenced workflow must be deployed separately.
- Input/output follows the nested workflow's own `input_schema`/`output_schema`.
- Useful for reusable sub-processes or reducing complexity of large workflows.

### 2.6 Tool Node

Calls a single tool directly — no agent and no LLM in the loop. Use it for a plain, deterministic step (fetch a value, transform data, render a template) where an agent would add nothing.

```yaml
# Tool source declaration: EVERY tool a `type: tool` node invokes must be
# declared — a toolset via `toolsets:`, built-ins, skill tools and MCP tools
# via `tools:`, a platform connector's tools via `connector:` on a node.
toolsets: [WeatherToolset]

nodes:
  - id: fetch_weather
    type: tool
    tool_name: WeatherToolset__get_current_weather  # Required. Its source must be declared above.
    depends_on: [get_city]
    input:                                           # Becomes the tool's arguments.
      city: "{{get_city.output.city}}"
```

**Tool node rules:**
- `tool_name` must match a tool available to the workflow, and that tool's source MUST be declared: toolset tools by listing the toolset in `toolsets:`, built-in tools as a `tools:` entry with `tool_type: builtin`, skill tools as a `tools:` entry with `tool_type: builtin` and the full `<skill>__<tool>` name, MCP tools by `connector: <name>` plus `tool: <server tool name>` when a platform MCP connector exists, otherwise as a `tools:` entry with `tool_type: mcp` and its `connection_params` (see "MCP tool nodes" below), a platform connector's tools by `connector: <name>` on the node (plus optional `tool: <base_name>` to pick one). Give every node that calls a connector tool its own `connector: <name>`. Deploy adds all of that connector's tools to the workflow, but naming one by `tool_name` on another node works only with the exact emitted name — an API connector's operation name (e.g. `get_pet`) or a custom `tool_name` the connector emits as-is. Other connector tools carry a suffix from the connector id (e.g. `sql_query_1a2b3c4d`), so a guessed name passes validation (the connector node already counts as a source) and fails at runtime as an unknown tool. The workflow-level `connectors:` list only declares an inline engine connector (`{name, type, subtype, config}`), never one from the connector store — an entry restating a node's `connector:` is rejected by validation and save, because the engine cannot build it and the workflow would fail at startup. A workflow with `tool_name` tool nodes is rejected by validation and deploy only when it declares no `tools:`, `toolsets:` or `connectors:` and no node binds a `connector:`. A `tool_name` missing from a non-empty declaration fails at runtime with "references unknown tool" — validation does not catch that, so declare every one.
- A `tools:` entry may carry a **`tool_config:`** block — per-tool settings applying to that tool alone, such as a response-size threshold or a response projection. This is where an MCP tool's `extract_fields` goes.
- A tool from an uploaded **toolset** uses its namespaced name (`<Toolset>__<tool>`, e.g. `WeatherToolset__get_current_weather`) and is declared by listing the toolset in the workflow-level `toolsets:` list. This applies only when the prefix is an uploaded toolset — the same `<prefix>__<tool>` shape is used by skill tools (next rule). The platform expands it at deploy into the tool entries, with the toolset's configuration, credentials and approval settings, and redeploys the workflow when the toolset changes. Never hand-write a `tools:` entry for a toolset tool — it is dropped in favour of the expanded entry, and fails deploy as a collision when declared beside a nested `workflow:` block. The Builder cannot create toolsets — they are uploaded on the Toolsets page — so a referenced toolset must already exist on the platform.
- A tool bundled with a **skill** also uses a namespaced name (`<skill>__<tool>`) but is NOT a toolset tool: declare it as a `tools:` entry `{tool_type: builtin, tool_name: <skill>__<tool>}` with the full namespaced name. It resolves at run time through the Secure Tool Runtime that hosts the skill. Never list a skill under `toolsets:` — platform save rejects it as an unknown toolset. When a prefix could be either, check whether it names a skill first. The entry gets **none** of the skill's stored platform config or credentials — workflows do not receive skill config the way an agent with the skill attached does. It works for a skill that needs no platform config; put non-secret values in an inline `tool_config:` on the entry; when the skill needs credentials, use an agent node whose agent has the skill attached instead of a tool node.
- 🛑 **The `<Name>__<tool>` convention is for toolset and skill tools only — never apply it to an MCP tool.** An MCP tool registers under its own server-side name, sanitized, with a prefix added only if the entry sets `tool_name_prefix`. So the `search_repositories` tool on a server you know as `github` registers as **`search_repositories`**, and a node naming `github__search_repositories` fails with "references unknown tool". Copying the toolset prefix onto an MCP tool is the single most common way to author a tool node that deploys cleanly and then cannot run.
- Arguments come from the node's `input:` mapping; the tool's result becomes the node's output, consumable by downstream nodes as `{{<id>.output...}}`.
- No `agent_name` — a tool node runs the tool itself.
- **Describe the tool before you write the node.** Call `describe_tools` with `{tool_name: <name>}` (a built-in, or a `<Toolset>__<tool>` tool) or `{connector: <connector name>, tool: <base name>}` (a connector already on the platform; leave `tool` out to list its tools, up to 50). Take `input:` keys only from the result's `parameters` and `{{<id>.output.<field>}}` references only from its `result`. The preview's `in(...)` and `out(...)` names are enough; `load_artifact` `described_tools.json` for types, descriptions and nested result fields. When the preview ends with `load_artifact described_tools.json for …`, those parts were left out; load it before writing them. `?` means unknown: `in(?)` is parameters unknown, `out(?)` is no result fields declared (`result` is `null`), and `in()` means it takes no parameters. When `result` is `null` the tool declares no result fields: reference the whole `{{<id>.output}}` or use an agent node, and never invent a field. A node on a connector writes `connector: <connectorName>` exactly as the result returns it, and `tool:` as its `name`. For a tool `describe_tools` can see, use a tool node only when the preview says `toolNode=yes` (`toolNode.usable` in the artifact). For a connector this build creates, which it cannot see yet: an MCP tool only when `discover_mcp_tools` lists it, an API operation only when its `operationId` is in the connector's `allow_list`, and any other connector type is allowed with the arguments from the connector node rules below (`query` for SQL).

#### Built-in tool fields — input parameter names are NOT result field names

🛑 **A tool's `input:` keys and its `{{<id>.output.<field>}}` keys are two separate namespaces, and reusing a name from one in the other is the single most common way to author a tool node that deploys and then fails.** `web_request` takes `output_artifact_filename` and returns `filename`; `query_data_with_sql` takes `output_filename` and returns `output_filename`. Neither name transfers.

Both columns below are generated from the tool registry, so they are the names the runtime actually accepts and produces. **Read the row before writing the node — never infer a field name from a parameter name, from another tool, or from what the name ought to be.** An `input:` key the tool does not declare fails `sam config plan` for a built-in whose schema the CLI can vouch for, and fails the node at dispatch otherwise; an output reference the tool cannot produce is reported as a plan warning, logged again when the workflow starts, and fails the node at run time when the reference resolves. Where the tool's result schema came from discovery rather than being hand-written — an MCP server declaring `outputSchema`, for instance — the reference is warned about when the workflow starts but not at plan time, because a CLI cannot tell that name from a built-in that shares it. For an MCP tool the declared `properties` name the fields inside its typed result, addressed as `{{<id>.output.mcp_structured_content.<field>}}`; the node's own top-level output keys are the SAM envelope, not the server's field names.

`**req**` marks a required input. `*opt*` marks a result field only some successful runs produce — present on one code path and absent on another, and referencing one on the run that omits it **fails the node**. The guards are uneven: `status` reports `success` either way, and `coalesce` still propagates the missing-field error instead of falling through to its next argument. A **condition** — a `when:`, a switch case or a loop condition — answers only when the references that *did* resolve already settle the answer whichever way the missing one would have gone. Otherwise it is treated as **not matched**: a `when:` skips its node, a switch case declines to select its branch, and a loop stops with `stopped_reason: condition_unresolved`. Either way a WARN names the reference, and the switch's output carries `cases_unresolved`. This is why an unresolved reference cannot quietly flip a gate: left as literal text it would compare unequal to almost anything, so `!= 'x'` over a missing field would be **true** and dispatch a branch on data nobody produced. The same holds for a `workflow.input` key the caller did not supply, except that leaving out an optional input is not an error, so that outcome is an ordinary not-matched — no WARN, no stale-reference flag, not counted in `cases_unresolved` (see §5). An `instruction:` is the exception and always ships the literal `{{…}}` to the model, because prose has no "did not match" to fall back to. The workflow also warns at startup about a stale field in any of them — and for an MCP or OpenAPI tool, whose result fields it cannot know, that startup warning never fires, so the runtime WARN is the only signal you will get. Bind the whole map as `{{<id>.output}}` and let the consumer deal with the absence, or do not reference an optional field at all. The one place it does not fail is the workflow's own `output_mapping`, which runs after every node has succeeded: there the reference is left verbatim in the output and logged, rather than voiding the run. Every tool node output also carries `status`, and carries `message` when the tool set one — both are added only if the tool did not already supply that key, so treat `message` as optional in the sense above.

The table is a quick reference for the built-ins a built agent can bind: the in-process built-ins that declare their schemas in Go, and the STR-backed ones the built-in groups name (`pdf_ops`, `ffmpeg`, `execute_python_code` and the rest). An STR-backed tool declares no result fields, so its result column reads "not declared", and a few have no parameters listed at all because no definition is compiled in to read them from. It leaves out what a built agent cannot bind: the Builder and platform tools, the analytics, Datadog and Activity Monitor tools, and in-process tools that belong to no toolset. **`describe_tools` is authoritative.** It reads the live tool on this deployment, so call it before you bind any tool or write a tool node: it returns the parameters, the result fields where the tool declares them, and whether the tool can back a tool node here. It is also the only way to see toolset tools, MCP tools and connector tools, which this table never carries.

<!-- BEGIN GENERATED: builtin-tool-fields -->
| Tool | `input:` parameter names | Result field names (`{{<id>.output.<field>}}`) |
|---|---|---|
| `append_to_artifact` | `content_chunk` (string) **req**, `filename` (string) **req**, `mime_type` (string) **req** | `appended_from_version` (integer) *opt*, `filename` (string), `new_version` (integer), `total_size_bytes` (integer) |
| `artifact_grep` | `file_path` (string), `filename` (string), `pattern` (string) **req**, `version` (integer) | `binary_files` (array) *opt*, `count` (integer) *opt*, `matches` (string) *opt*, `message_to_llm` (string) *opt*, `skipped_files` (array) *opt* |
| `artifact_search_and_replace_regex` | `filename` (string) **req**, `is_regexp` (boolean), `new_description` (string), `new_filename` (string), `regexp_flags` (string), `replace_expression` (string), `replacements` (array), `search_expression` (string) | `filename` (string), `match_count` (integer) *opt*, `no_matches` (boolean) *opt*, `replacement_results` (array) *opt*, `replacements_made` (integer) *opt*, `source_filename` (string) *opt*, `source_version` (integer) *opt*, `total_matches` (integer) *opt*, `total_replacements` (integer) *opt*, `version` (integer) |
| `ask_user_question` | `component_name` (string), `message` (string), `questions` (array) **req** | `answers` (object) *opt*, `error_code` (string) *opt*, `message` (string) *opt*, `status` (string) |
| `convert_file_to_markdown` | `input_filename` (string) **req** | *not declared — verify against the tool before binding* |
| `convert_pdf_to_markdown` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `create_chart_from_plotly_config` | `config_content` (string) **req**, `config_format` (string) **req**, `output_filename` (string) **req**, `output_format` (string) | *not declared — verify against the tool before binding* |
| `create_image_from_description` | `image_description` (string) **req**, `output_filename` (string) | *no result fields — the output is an artifact* |
| `create_sqlite_db` | `input_filename` (string) **req**, `output_db_filename` (string) **req**, `table_name` (string) | `message` (string), `output_filename` (string), `output_version` (integer), `row_count` (integer), `status` (string), `table_name` (string) |
| `deep_research` | `kb_ids` (array), `max_iterations` (integer), `max_runtime_minutes` (integer), `max_runtime_seconds` (integer), `research_question` (string) **req**, `research_type` (string), `sources` (array) | `artifact_filename` (string) *opt*, `artifact_version` (integer) *opt*, `iterations_completed` (integer) *opt*, `plan_id` (string) *opt*, `rag_metadata` (array) *opt*, `reason` (string) *opt*, `response_artifact` (object) *opt*, `status` (string), `total_sources` (integer) *opt* |
| `delete_artifact` | `confirm_delete` (boolean), `filename` (string) **req**, `version` (integer) | `confirmation_required` (boolean) *opt*, `filename` (string), `version_count` (integer) *opt*, `versions` (array) *opt*, `versions_deleted` (integer) *opt* |
| `delete_scheduled_task` | `task_id` (string) **req** | `deleted` (boolean), `id` (string), `success` (boolean) |
| `describe_audio` | `input_audio` (string) **req**, `prompt` (string) | `audio_filename` (string), `audio_version` (integer), `description` (string) |
| `describe_image` | `input_image` (string) **req**, `prompt` (string) | `description` (string), `image_filename` (string), `image_version` (integer) |
| `edit_image_with_gemini` | `edit_prompt` (string) **req**, `input_image` (string) **req**, `output_filename` (string), `use_pro_model` (boolean) | `original_filename` (string), `original_version` (integer) |
| `execute_python_code` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `ffmpeg` | `command` (string) **req**, `input_files` (array) **req**, `output_filename` (string) **req**, `timeout_seconds` (integer) | *not declared — verify against the tool before binding* |
| `ffprobe` | `command` (string), `input_file` (string) **req**, `timeout_seconds` (integer) | *not declared — verify against the tool before binding* |
| `generate_image_with_gemini` | `image_description` (string) **req**, `output_filename` (string), `use_pro_model` (boolean) | `model_used` (string), `output_filename` (string), `output_version` (integer), `result_preview` (string), `used_pro_model` (boolean) |
| `get_current_time` | *none* | `current_time` (string), `date` (string), `day_of_week` (string), `formatted_time` (string), `time` (string), `timestamp` (integer), `timezone` (string), `timezone_abbreviation` (string), `timezone_offset` (string) |
| `html_to_pdf` | `images` (array), `input_document` (string) **req**, `margins_inches` (number), `orientation` (string), `output_filename` (string), `page_size` (string), `print_background` (boolean), `timeout_seconds` (integer) | *not declared — verify against the tool before binding* |
| `image_magick` | `command` (string) **req**, `input_images` (array) **req**, `output_filename` (string) **req**, `timeout_seconds` (integer) | *not declared — verify against the tool before binding* |
| `list_artifacts` | *none* | `artifacts` (array) |
| `list_scheduled_tasks` | `enabled_only` (boolean) | `pageNumber` (integer), `pageSize` (integer), `tasks` (array), `total` (integer) |
| `load_artifact` | `filename` (string) **req**, `include_line_numbers` (boolean), `limit` (integer), `load_metadata_only` (boolean), `max_content_length` (integer), `offset` (integer), `version` (integer) | `content` (string) *opt*, `description` (string) *opt*, `filename` (string), `message_to_llm` (string) *opt*, `metadata` (object) *opt*, `mime_type` (string), `schema` (object) *opt*, `size_bytes` (integer), `status` (string), `total_lines` (integer) *opt*, `version` (integer) |
| `merge_structured_data` | `input_filename` (string) **req**, `patch` (object) **req**, `path` (string), `result_description` (string) | `format` (string), `merge_path` (string) *opt*, `message` (string), `output_filename` (string), `output_version` (integer), `status` (string) |
| `mermaid_diagram_generator` | `format` (string), `mermaid_syntax` (string) **req**, `output_filename` (string) | *not declared — verify against the tool before binding* |
| `pdf_extract_text` | `input_document` (string) **req**, `max_bytes` (integer), `max_pages` (integer) | *not declared — verify against the tool before binding* |
| `pdf_ops` | `additional_pdfs` (array), `input_pdf` (string) **req**, `operation` (string) **req**, `output_filename` (string), `page_range` (string), `password` (string), `rotation` (integer), `watermark_opts` (string), `watermark_text` (string) | *not declared — verify against the tool before binding* |
| `pptx_create_presentation` | `author` (string), `images` (array), `output_name` (string), `slides` (string) **req**, `style` (object), `theme` (object), `title` (string) **req** | *not declared — verify against the tool before binding* |
| `query_data_with_sql` | `input_files` (object) **req**, `jmespath_transforms` (object), `output_filename` (string), `output_format` (string), `result_description` (string), `sql_query` (string) **req** | `message` (string), `output_filename` (string), `output_version` (integer), `result_preview` (array), `result_rows` (integer), `result_truncated` (boolean), `status` (string) |
| `reinterpret_artifact` | `description` (string), `input_filename` (string) **req**, `mime_type` (string), `output_filename` (string) | `mime_type` (string), `output_filename` (string), `output_version` (integer), `schema` (any), `status` (string) |
| `render_document_to_images` | `dpi` (integer), `input_document` (string) **req**, `max_pages` (integer), `pages` (string), `timeout_seconds` (integer) | *not declared — verify against the tool before binding* |
| `schedule_task` | `message` (string) **req**, `name` (string), `schedule` (string) **req**, `schedule_type` (string) **req**, `target_name` (string) **req**, `target_type` (string), `timeout_seconds` (integer), `timezone` (string) | `name` (string), `schedule_type` (string), `scheduled_task_id` (string), `status` (string), `target_name` (string) |
| `set_scheduled_task_enabled` | `enabled` (boolean) **req**, `task_id` (string) **req** | `createdBy` (string) *opt*, `description` (string) *opt*, `enabled` (boolean), `id` (string), `lastRunTime` (string) *opt*, `name` (string) *opt*, `nextRunTime` (string) *opt*, `runCount` (integer) *opt*, `scheduleExpression` (string) *opt*, `scheduleType` (string) *opt*, `source` (string) *opt*, `success` (boolean) *opt*, `targetAgentName` (string) *opt*, `targetType` (string) *opt*, `taskMessage` (string) *opt*, `timezone` (string) *opt* |
| `transform_data_with_jmespath` | `input_filename` (string) **req**, `jmespath_expression` (string) **req**, `output_filename` (string), `result_description` (string) | `message` (string), `output_filename` (string), `output_version` (integer), `result_preview` (any), `result_rows` (integer) *opt*, `result_truncated` (boolean), `status` (string) |
| `update_scheduled_task` | `description` (string), `message` (string), `name` (string), `schedule_expression` (string), `schedule_type` (string), `task_id` (string) **req**, `timezone` (string) | `createdBy` (string), `description` (string) *opt*, `enabled` (boolean), `id` (string), `lastRunTime` (string) *opt*, `name` (string), `nextRunTime` (string) *opt*, `runCount` (integer), `scheduleExpression` (string), `scheduleType` (string), `source` (string), `targetAgentName` (string), `targetType` (string), `taskMessage` (string), `timezone` (string) |
| `volume_append` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_delete` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_edit` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_glob` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_grep` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_list` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_mkdir` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_move` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_read` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_stat` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `volume_write` | *not generated — read the tool's own description* | *not declared — verify against the tool before binding* |
| `web_request` | `body` (string), `headers` (object), `method` (string), `output_artifact_filename` (string), `url` (string) **req** | `filename` (string), `original_content_type` (string), `processed_content_type` (string), `response_status_code` (integer), `size_bytes` (integer), `truncated` (boolean) *opt* |
| `web_search_google` | `date_restrict` (string), `max_results` (integer), `query` (string) **req**, `safe_search` (string), `search_type` (string) | `formatted_results` (string), `num_results` (integer), `search_turn` (integer) |
<!-- END GENERATED: builtin-tool-fields -->

Tools reached any other way are **not** in that table: toolset tools (`<Toolset>__<tool>`), MCP tools, and connector tools. Call `describe_tools` for their `parameters`, and for their `result` when they declare one. Most declare none, and an MCP tool's `outputSchema` is optional, so where `result` is `null` verify the fields against a real response — for MCP that means the `extract_fields` projection below, which is the only way a downstream node can read anything out of a saved response. `instantiate_template` is a skill tool rather than a registered built-in; its contract is documented in its own section below.

#### MCP tool nodes — and projecting a usable output from the response

A `type: tool` node can call an **MCP** tool. When a platform MCP connector exists for the server, bind it (next paragraph). Otherwise declare the server in the workflow-level `tools:` list with its connection details, and give it a `tool_config:` block — per-tool settings that apply to this tool only.

🛑 **`connection_params` is MANDATORY on every `tool_type: mcp` entry, and omitting it fails silently at deploy time.** An entry with no connection details cannot be parsed, so the runtime logs the failure, registers nothing, and *carries on* — the workflow deploys, reports success, and then every node naming that tool dies with "references unknown tool". Nothing rejects the config on the way in. Never emit a `tool_type: mcp` entry until you have a concrete `type:` and `url:` to put in it.

🛑 **When a platform MCP connector exists for the server, bind it — do not copy its URL into the workflow.** `connector: <name>` plus `tool: <server tool name>` binds a node to one tool of the connector, so the URL, auth and later connector edits come from the connector. `tool` is required unless the connector's own config pins exactly one tool. The connector's stored tool list decides what can be bound: a name outside it is rejected at save with the tools it does offer, and a connector with no stored tool list (and no `tool_name` pin; an OAuth connector needs a manifest instead of the pin) is refused: re-save it against a reachable server, or, for an OAuth connector or one holding its address or credentials as `${...}` references, add a `manifest` listing its tools (a connector cannot hold a manifest beside `tool_name`, `allow_list` or `deny_list`, so remove those first). The node addresses the tool under the connector's `tool_name_prefix` if it sets one; a node bound to an MCP tool whose name another bound MCP connector also serves is rejected at save, so give one a `tool_name_prefix` (a clash with a `tools:` entry, a toolset tool, or an API or SQL connector tool is not checked, and whichever registers first wins silently).

Declare a `tools:` entry (`tool_type: mcp` + `connection_params` + `tool_name`) only for a server that has no platform connector, or when the node needs the `tool_config:` response projection below — a connector-bound node cannot carry one. That projection is the only way a downstream node can read anything out of a saved response.

```yaml
tools:
  - tool_type: mcp
    tool_name: detect_objects
    connection_params:
      type: streamable-http
      url: "http://127.0.0.1:9666/mcp"
    tool_config:
      # 1. Force the save path, so the digest below is produced at all.
      mcp_tool_response_save_threshold_bytes: 1
      # 2. Project the fields this workflow needs out of the response.
      mcp_intelligent_processing:
        extract_fields:
          count: "count"
          counts: "counts"
          boxes: "detections[].{x1: floor(box.x1), y1: floor(box.y1), x2: ceil(box.x2), y2: ceil(box.y2), label: class_name}"

nodes:
  - id: detect
    type: tool
    tool_name: detect_objects
    depends_on: [fetch]
    input:
      image: "«artifact_content:{{fetch.output.filename}} >>> format:datauri»"
```

Downstream nodes then read `{{detect.output.extracted_fields.counts.car}}`, `{{detect.output.extracted_fields.boxes}}`, and so on.

**Why the projection is necessary.** A large MCP response is saved to an artifact rather than returned inline, so the node's own output carries only a reference — there is no field for a downstream node to bind to. `extract_fields` is a map of *your* chosen name to a **JMESPath** expression evaluated against the response; the results ride along under `extracted_fields` even when the bulk payload is withheld. `response_skeleton: true` is the coarser alternative: a structural digest of the top level, useful for loop-control fields when you don't know the shape in advance.

🛑 **The digest is only produced when the response is SAVED.** A response under both the save threshold and the return-size limit takes an early path that never computes it — so `extracted_fields` is simply absent, and every downstream reference fails. Setting `mcp_tool_response_save_threshold_bytes: 1` forces the save path for any non-empty response. **Without it, `extract_fields` can appear configured and still produce nothing.** (A response containing non-text content also forces saving, regardless of size.)

⚠️ **Each extracted field is capped at 4096 bytes, strings included.** A field within the cap arrives whole. A JMESPath expression can select an arbitrarily large subtree; one whose serialized form exceeds the cap is replaced by a marker reading `(value too large: N bytes …)`. That marker is a *string*, so a node expecting a list or number fails with a **type** error, not a missing-field error.

Keep a projection under the cap by narrowing it — select only the fields you need, and reduce their size. Reshaping full-precision floats to integers roughly halves a coordinate list, which doubles how many items fit:

```
boxes: "detections[].{x1: floor(box.x1), y1: floor(box.y1), x2: ceil(box.x2), y2: ceil(box.y2), label: class_name}"
```

Floor on the top-left and ceil on the bottom-right keeps the integer box containing the original, so nothing is clipped.

🛑 **A JMESPath expression that errors or matches nothing is skipped silently** — no key is emitted and the node still reports success. The failure surfaces one node later as `field "extracted_fields" not found in node output`, which points at the *consumer*, not at the bad expression. Distinguish the two causes by what is present: the field **missing** means the expression matched nothing (or the save path never ran); the field **present but holding the "value too large" marker** means it overflowed the cap.

**Verify a projection against a real response before building on it.** The expressions are evaluated against the tool's actual output shape, and neither a typo nor a wrong path is reported as an error.

#### Connector-backed tool nodes

A tool node can name a **connector** instead of a tool. Use this to run a query or send a message through a configured connector with no agent and no LLM — the deterministic way to persist a workflow's results.

```yaml
- id: record
  type: tool
  connector: traffic-db          # instead of tool_name — a platform connector by name
  depends_on: [detect]
  input:
    query: |
      INSERT INTO traffic_counts (run_id, camera_url, object_class, qty)
      VALUES ('{{workflow.run_id}}', '{{workflow.input.camera_url}}', 'car', {{detect.output.extracted_fields.counts.car}})
      ON CONFLICT (run_id, camera_url, object_class) DO NOTHING
```

**Connector node rules:**
- `connector` and `tool_name` are alternatives — supply exactly one. A node with neither is a config error.
- `connector` is a **platform reference**, resolved to a concrete tool at deploy time. It therefore requires deploying through the platform; a config that names a connector cannot be run standalone, and the error says so explicitly rather than failing at runtime.
- The connector must already exist on the platform, or be a `connectors:` entry in this build.
- **An API (OpenAPI) connector bound by a node needs an `allow_list` or an uploaded spec.** Deploy resolves a workflow's tool names without fetching the spec, so a connector that has only a `specification_url` is refused at deploy ("fetches its spec from specification_url at runtime"), and full validation rejects the build first. For a connector this build creates, set `values.allow_list` to a comma-separated string of the operationIds the workflow's tool nodes call, plus any its bound agents need: `allow_list` limits the connector for every consumer. For a connector that already exists on the platform, including one you reuse as `origin: platform`, only what is stored there counts. Do not add an `allow_list` to it in this build: the connector is shared, and an `allow_list` limits it for every agent and workflow that already uses it. If validation says an existing connector has neither a stored `allow_list` nor an uploaded spec, stop and ask the user to upload its OpenAPI spec on the Connectors page (the document at the connector's current `specification_url` is the file to upload; they can then also choose which operations it exposes), then validate again. Never write `values.allow_list` as an empty value or a JSON array for a connector that has one stored. A stored `allow_list` or an uploaded spec exempts the connector.
- Inputs are the connector tool's own arguments (`query` for SQL). Its result becomes the node's output. `describe_tools` with `{connector: <connector name>, tool: <base name>}` lists the arguments (and the result, when declared); write `connector: <connectorName>` exactly as it returns it, because a name that differs in case or spacing binds nothing.
- Quote string interpolations and leave numeric ones unquoted, exactly as the target SQL dialect requires — see the SQL caution in §5.
- Never write a table or column name the user did not give. When the schema is not confirmed, run the query in an agent node with the connector's query tool (it sees the live schema when it runs), or ask the user for the table layout and then use a tool node. Say in the plan and the final message that the queries were not checked against the database.
- Prefer an idempotent statement (`ON CONFLICT DO NOTHING`, `MERGE`). A node can be retried by `retry_strategy`, and a re-fired schedule can replay the same inputs.

#### instantiate_template tool node — render a report template

A workflow can render a `.samt` report template with no LLM in the loop: a plain `type: tool` node calling the built-in `instantiate_template`. Declare it in the workflow-level `tools:` list like any other tool-node tool.

```yaml
tools:
  - tool_type: builtin
    tool_name: query_data_with_sql
  - tool_type: builtin
    tool_name: instantiate_template

nodes:
  - id: aggregate
    type: tool
    tool_name: query_data_with_sql     # saves an artifact of aggregated rows
    input:
      input_files:
        sales: "{{workflow.input.input_filename}}"   # YAML map, never a JSON string
      sql_query: "SELECT region, SUM(revenue) AS total FROM sales GROUP BY region"
      output_filename: "sales_totals"
      output_format: "csv"

  - id: build_report
    type: tool
    tool_name: instantiate_template
    depends_on: [aggregate]
    input:
      skill_name: quarterly-report      # the skill bundling the .samt under assets/
      asset: report.html.samt           # the file under assets/ ("assets/" prefix optional)
      substitutions:                    # values for the template's @@KEY@@ placeholders
        report_title: "{{workflow.input.title}}"
      data_inputs:                      # template's logical input name → upstream artifact
        sales_rows: "{{aggregate.output.output_filename}}"

output_mapping:
  report: "{{build_report.output.filename}}"
```

Note the binding fields: `query_data_with_sql` returns its saved artifact's name as `output_filename`, while `instantiate_template` returns `filename` — bind to the upstream tool's actual result field, from the generated table in §2.6 for a registered built-in. For an agent-node upstream, bind to whatever field of its `output_schema` carries the artifact filename.

**Input contract:**
- The template source is EITHER `skill_name` + `asset` (a `.samt` bundled in a skill's `assets/`) OR `source_artifact` (a `.samt` artifact in the current session). A deployed workflow should always use the skill form — the authoring session's artifacts don't exist at run time.
- `substitutions` — map of the template's declared `@@KEY@@` placeholder names to values. Every required substitution without a default must be present.
- `data_inputs` — map binding each of the template's declared **logical input names** to an artifact filename produced upstream (`{{<node>.output...}}` resolving to that filename). The tool stages each bound artifact into the filename the template's contract declares and validates it against the declared schema/columns, so the workflow never reproduces the template's internal filenames.
- `output_filename` — optional override for the rendered artifact's name; defaults to the template's declared `output_filename`.

**Output:** `{{<id>.output.filename}}` is the rendered artifact's name; `{{<id>.output.version}}` its version. Map it into `output_mapping` to make the rendered report the workflow's result.

**Failure semantics:** a contract violation — a missing required substitution, a data artifact that fails the declared schema/columns, an embed that doesn't resolve — fails the node **terminally**. There is no LLM in a tool node to read the findings and retry, so upstream nodes must emit artifacts matching the template's declared schemas exactly.

The template's contract (its declared substitutions and data_inputs, with schemas) comes from `read_template`; the instruction that requested this workflow should include that contract verbatim. Never guess the input names or shapes.

### 2.7 Decision Node

Sends the node's `input` to a decision model (TypeSafe Jev, OpenAI `gpt-6-luna` or Gemini `gemini-decision-1`), asks it typed questions in one call, and optionally routes on the answer. Use it for narrow classify-and-route steps where a full agent turn is overkill; keep open-ended reasoning in agent nodes. Use one only where it can run: call `platform_list_components` with `component_type: model`, and use a decision node only when it lists a model with `modelType: decision` and reports `decision_nodes_enabled: true`. Otherwise use a classify agent and a `switch`.

```yaml
decision:
  model_alias: jev          # a Models-registry entry with modelType decision; never an api_key
nodes:
  - id: triage
    type: decision
    input: {ticket: "{{workflow.input.ticket}}"}   # what the model reads; every question is asked about all of it
    questions:
      department:
        type: choice        # boolean | choice | score
        instructions: "Which team should own this ticket?"
        criteria: {billing: "Payment issues", technical: "Bugs or outages", other: "Anything else"}
      urgent: {type: boolean, instructions: "The ticket needs attention right now"}
    route_on: department    # optional; must name a choice question
    routes: {billing: billing_agent, technical: tech_agent}
    min_confidence: 0.8     # below this, route to escalate
    escalate: review_agent
    escalation_mode: handoff  # review_agent answers the ticket itself; leave it out to let its answer pick the route
    default: general_agent  # catches `other`, the one option with no route
```

- Every `routes`, `escalate` and `default` target must list the decision node in `depends_on`; unselected targets are skipped.
- `images:` (only for a decision model that reads images: a Jev model, on TypeSafe's own API or through OpenRouter, an OpenAI or Gemini decision model, or `openai/gpt-6-luna-decisions` through OpenRouter on the `typesafe` provider) sends image artifacts with the input in the same call: a list whose entries are each an artifact reference (`name`, `name:version` or an `artifact://` URI) or a template resolving to one or to a list, such as `"{{inspect.output.created_artifact}}"`. PNG, JPEG and WebP are accepted; one whose longer side is over 8000 px is downscaled to 8000 px. An entry that resolves to nothing is skipped. That covers an omitted, null or empty workflow input, a null or empty output field, a node that completed without returning any output, and a node the workflow pruned. A pruned node is one whose `when:` was false, one on a branch that a `switch` or decision node did not take, or one skipped because every node it depends on was pruned. A missing, non-image or unreadable artifact fails the node even under `on_provider_error: escalate`. So does an entry that reads a node that failed, or a node skipped because an upstream node failed or a `when:` or `switch` condition referenced an output field its node did not return. So does an entry that reads the target of a `switch` case that could not be evaluated, even when the switch then took a later case or its `default`. So does an entry that names an output field the node did not return, or that reads a node that has not run and is not upstream of the decision node. Add that node to `depends_on`. An entry that embeds a template in other text, such as `{{workflow.input.photo}}:2`, follows these rules as a whole. When a reference in it resolves to nothing, the whole entry is skipped or fails the node. The `decision:` block caps a call at `max_images` (0 uses the default 5; at most 128) and `max_image_bytes` (0 uses the default 20 MiB, counted after downscaling; at most 64 MiB). An artifact stored larger than `max_image_bytes`, images over either cap, or an image that needs downscaling and declares more than 40 megapixels is `input_too_large`, which `on_provider_error: escalate` escalates; more literal (non-template) entries than `max_images` fail the workflow at load. Loading and downscaling share the call's 90% of the node `timeout`, and running out while loading is a `timeout` that `on_provider_error: escalate` escalates. Any other TypeSafe model reads only text and rejects a node with `images:`, so use one only when the decision model is a Jev model, an OpenAI or Gemini decision model, or GPT-6 Luna Decisions through OpenRouter. The model reads the input as text followed by the images, so a question refers to an image in words. On TypeSafe's own API, any Jev image over 40 megapixels is `input_too_large`, even when neither side is over 8000 px. The output's `images` lists each image as loaded (`name`, `version`, `mime_type`, `bytes`), never the bytes; for Jev on TypeSafe's own API, before it is fitted to TypeSafe's limits.
- With `route_on` and `routes` set, every `route_on` option needs a route unless `default` is set; a missing route with no `default` fails the workflow at load. Only add `default` when some option has no route (as `other` above); a `default` behind routes that cover every option can never be reached.
- `score` criteria must be a list of 2 to 10 levels, lowest first (`- low: ...`). A score answer is `score` (a number from 0 to levels-1 that can land between levels), `probabilities` keyed by your level keys, and `legend` mapping each level number to its key (`{"0": "low", "1": "medium", ...}`).
- `boolean` criteria are optional; when given they must describe both `true` and `false`, and nothing else (quote the keys: `criteria: {"true": needs a human, "false": routine}`).
- `escalation_mode` decides what an escalation does. `reroute` (the default): the escalate target answers the same question, and its answer picks the route as if the model had answered confidently. `handoff`: the escalate target's reply is the result and every route is skipped; use it when the escalate target finishes the work itself (it answers the request, or is a human-review step), and whenever the escalate target is also a route or the `default`. Under `reroute`, the answer is read from the escalate target's output field named after `route_on` (for a decision escalate target, its own answer to a choice question of that name), and every route and `default` target waits for the escalate target without listing it in `depends_on`:

  ```yaml
    escalate: reclassify      # escalation_mode: reroute is the default
  - id: reclassify
    type: agent
    agent_name: Classifier
    depends_on: [triage]
    input: {ticket: "{{workflow.input.ticket}}"}
    instruction: "Return `department`: exactly one of billing, technical, other."
    output_schema_override:   # declares the route_on field the decision reads; enum = the route_on options
      type: object
      properties: {department: {type: string, enum: [billing, technical, other]}}
      required: [department]
  - {id: billing_agent, type: agent, agent_name: Billing, depends_on: [triage], input: {ticket: "{{workflow.input.ticket}}"}}
  ```

  🛑 A rerouting escalate agent must declare the `<route_on>` field (the rule in §2.1): an agent with no output schema returns `{"text": ...}`, so every escalated run would fail. Give it an `enum` of exactly the `route_on` options and mark it `required`: the answer is matched exactly and case-sensitively (only surrounding whitespace is ignored), so `Billing` does not match `billing`. Build validation field-checks that field like a node `input:` (only when the escalate target is an agent node); this path only runs on a low-confidence or failed decision, so anything it cannot check shows up late.

  At load, a rerouting decision node fails unless: the escalate target is an `agent`, `workflow`, `tool` or `decision` node (a decision escalate target must ask a choice question named after `route_on` whose options are all `route_on` options, and must not escalate itself); it is not a loop body, a map template or an exit handler; no other node (a switch or another decision node) routes to it; no route or `default` is the escalate target itself; and the escalate target does not already depend, directly or through other nodes, on a route or `default` target, since routes wait for it and that would be a cycle. Each error names `escalation_mode: handoff` as the alternative. `escalation_choice` was removed: a workflow that still sets it fails to load.

  At run time, a confident answer still routes directly and skips `reclassify`. An escalation waits for `reclassify`, then routes on its answer: an option with a route runs that route, an option with no route runs `default`, and every other route is skipped. If the escalate target is skipped, no route runs. An answer that is missing, not a string, or not a `route_on` option fails the escalate node, with an error that names only the answer's type and length and the valid options. That failure is retried, for an agent or workflow escalate target, under its node-level or workflow-level `retry_strategy`, since re-asking an agent can fix a bad label (a tool or decision escalate target is never retried). If it still fails, the workflow fails under `fail_fast: true` (the default); with `fail_fast: false` the run completes with every route skipped.
- `on_provider_error` is `fail` (the default) or `escalate`. `escalate` routes to the `escalate` target in three cases: a transient provider failure (a timeout, a rate limit, an unreachable or 5xx provider, or an invalid response), an input too large for the decision model, and a refusal: an OpenAI refusal or a Gemini safety block. A larger LLM agent can usually still handle an input that is too large. OpenAI can refuse a question, and a refusal of any question fails the whole call as `refused`; a Gemini safety filter blocking the request (a 400 with a safety code) fails it as `refused` too. The refusal text and the block message are never logged or returned. Authentication and configuration failures always fail the node, whatever `on_provider_error` says. These are rejected credentials (401 or 403, or Gemini's 400 with reason `API_KEY_INVALID`), any other 4xx the provider returns (such as a 422 for a request it refuses as invalid), an OpenAI account with no credits or quota left (a 429 that is not retried), a redirect from the API base to another origin, which is refused when the key is sent in a named header (Gemini), and a `model_alias` that is unbound or not a decision model. The one 4xx that escalates is a rejection of the input's size, which is `input_too_large`. That is a 413, a 4xx whose error type is `max_tokens_exceeded`, or OpenAI's `context_length_exceeded`.
- A decision node takes no `retry_strategy`: the workflow fails at load if it has one. The provider client already retries rate-limit and overload responses within `timeout_seconds`.
- Output: `{{triage.output.answers.department.choice}}`, `.confidence`, `{{triage.output.answers.urgent.value}}`, plus `selected_branch`, `routed_branch`, `escalated` and `escalation_reason` (`low_confidence` or `provider_error`). `selected_branch` is the node the decision itself selected, so after an escalation it is the escalate target. `routed_branch` is the route or `default` target that actually ran: the same as `selected_branch` on a direct route, the route chosen from the escalation's answer under `escalation_mode: reroute` (filled in before that route runs), and `""` when no route ran (a handoff escalation, a skipped escalation, or a node without `route_on`). Read `routed_branch` to learn which route ran. An escalation after a provider failure also sets `provider_error` to the failure category: `rate_limited`, `timeout`, `unreachable`, `invalid_response`, `input_too_large` or `refused` (the other two categories, `unauthorized` and `config`, fail the node instead). It is a category, never the provider's error text, so branch on it rather than parsing it. After a provider-failure escalation `answers` is empty (`{}`), and reading an answer resolves to a missing field: it fails the node (and the run, under the default `fail_fast: true`). So with `on_provider_error: escalate`, read `{{<node>.output.answers...}}` only in a route or `default` target of a decision with `escalation_mode: handoff` that is not also the escalate target, or in a node reached only through one: those run only when the model answered. Every other node that runs after the decision sees `answers: {}`: the escalate target and everything after it, nodes that depend on the decision without being one of its targets, joins that also depend on a node outside the routes, and every route and `default` target of a rerouting decision. Otherwise keep `on_provider_error: fail`. A low-confidence escalation keeps its answers.
- Never write an API key or `${VAR}` into the `decision:` block: a saved workflow stores it literally. Use `model_alias`; if no decision model exists, tell the user to add one on the Models page (provider TypeSafe, OpenAI or Google AI Studio, offered while decision nodes are enabled) or with `sam config apply` (`kind: model`, `provider: typesafe`, `provider: openai` or `provider: google_ai_studio`, `modelType: decision`).

### Required Fields Summary

| Node Type | Required Fields | Key Optional Fields |
|-----------|----------------|---------------------|
| `agent` | `id`, `type`, `agent_name` | `input`, `instruction`, `depends_on`, `when`, `timeout`, `retry_strategy`, `input_schema_override`, `output_schema_override` |
| `switch` | `id`, `type`, `cases` | `default`, `depends_on`, `when` |
| `map` | `id`, `type`, `node`, `items` or `with_items` | `concurrency_limit`, `max_items`, `depends_on`, `when`, `timeout` |
| `loop` | `id`, `type`, `node`, `condition` | `max_iterations`, `delay`, `depends_on`, `when`, `timeout` |
| `workflow` | `id`, `type`, `workflow_name` | `input`, `depends_on`, `when`, `timeout` |
| `tool` | `id`, `type`, and **either** `tool_name` **or** `connector` | `input`, `depends_on`, `when`, `timeout` |
| `decision` | `id`, `type`, `questions` (plus a workflow `decision: {model_alias}`) | `input`, `images`, `route_on`, `routes`, `min_confidence`, `escalate`, `escalation_mode`, `default`, `on_provider_error`, `depends_on`, `when`, `timeout` |

---

## 3. Dependencies and Parallelism

```yaml
depends_on: [node_a, node_b]    # This node runs after both node_a and node_b complete.
```

- Nodes with no `depends_on` (or empty array) run immediately when the workflow starts.
- Nodes whose dependencies are all satisfied run in parallel automatically.
- **If you reference a node's output in a template, that node MUST be in `depends_on`.**

🛑 **Parallel branches that write artifacts MUST write to distinct names.** Artifact names are a flat per-session namespace, not scoped per node or per branch. Fan four copies of the same sub-workflow out and let each auto-name its output, and all four collide onto one name — the branches overwrite each other, the last writer wins, and every branch still reports success. The result is a plausible report in which several sections show the same data.

Whenever a tool or sub-workflow accepts an output-name argument (`output_filename`, `output_basename`, `output_artifact_filename` — the exact name varies by tool, so take it from §2.6's table for a built-in), pass a **distinct value per branch**, derived from something already unique: the node id, the map index, the run id.

```yaml
- id: annotate_1
  type: workflow
  workflow_name: DetectAndAnnotateFromUrl
  input:
    image_url: "{{workflow.input.image_1}}"
    output_basename: "source_1"      # distinct per branch — load-bearing, not cosmetic
- id: annotate_2
  type: workflow
  workflow_name: DetectAndAnnotateFromUrl
  input:
    image_url: "{{workflow.input.image_2}}"
    output_basename: "source_2"
```

The same applies inside a `map` node, where every iteration runs the same template node: derive the name from `{{_map_index}}`.

⚠️ **Deleting an artifact is name-scoped too.** A cleanup node that removes an artifact by name removes **all versions** of that name — so a shared name means one branch can delete another branch's data.

Example — `analyze_a` and `analyze_b` run in parallel, `merge` waits for both:
```yaml
nodes:
  - id: analyze_a
    type: agent
    agent_name: AnalyzerA
    input: { data: "{{workflow.input.data}}" }
  - id: analyze_b
    type: agent
    agent_name: AnalyzerB
    input: { data: "{{workflow.input.data}}" }
  - id: merge
    type: agent
    agent_name: Merger
    depends_on: [analyze_a, analyze_b]
    input:
      result_a: "{{analyze_a.output}}"
      result_b: "{{analyze_b.output}}"
```

---

## 4. Output Mapping

**Required.** Defines how the workflow's output is constructed from node outputs.

### Simple mapping
```yaml
nodes:
  - id: final_step
    type: agent
    agent_name: Reporter
    instruction: 'Return a JSON object with exactly one key, "summary": a one-paragraph summary.'
    output_schema_override:          # declares the field output_mapping reads below
      type: object
      properties:
        summary: { type: string }
      required: [summary]

output_mapping:
  result: "{{final_step.output}}"            # whole output — needs no declared schema
  summary: "{{final_step.output.summary}}"   # a field — only because final_step declares it
```

A field reference such as `{{final_step.output.summary}}` is valid only for a field the node declares in `output_schema_override` or its agent's `output_schema`, with an instruction telling the agent to return that key. Without a declared schema, map the whole `{{final_step.output}}`. An `output_mapping` reference that does not resolve is left verbatim (see the §5 template rule on a field that does not exist), so the run completes and returns the literal `{{final_step.output.summary}}` text.

### Coalesce (first non-nil value)

Essential for switch convergence — picks whichever branch actually executed:
```yaml
output_mapping:
  result:
    coalesce:
      - "{{premium_handler.output}}"
      - "{{standard_handler.output}}"
      - "{{fallback_handler.output}}"
      - "No result available"          # Literal fallback string
```

Coalesce evaluates in order and returns the first non-nil value. Use it whenever multiple code paths could produce the result (switch branches, optional nodes with `fail_fast: false`).

**`coalesce` and `concat` work at ANY depth, not just at the top of `output_mapping`.** Value resolution recurses, so an operator is recognized wherever it appears — including nested inside a node's `input:`. That is how you supply a defaulted argument to a tool:

```yaml
- id: report
  type: tool
  tool_name: instantiate_template
  input:
    substitutions:
      report_title:
        coalesce:
          - "{{workflow.input.report_title}}"   # a template, so it can resolve to nil
          - "Quarterly Report"                  # a literal — this IS the fallback
```

🛑 **Three ways to break an operator silently:**
- **A sibling key.** An operator is a map with **exactly one** key. Add anything beside `coalesce` and it stops being an operator and renders as a raw map.
- **A non-bare template entry.** `"{{x}} — suffix"` is an interpolated string, so it resolves to that literal text and is *never* nil. It always wins and every entry after it is dead. Keep operator entries bare, and keep the tail a plain literal.
- **An empty string.** `""` is not nil, so it wins and the value renders blank.

⚠️ **`coalesce` is for a default you choose, not for keeping an unset input out of the call.** A node `input:` key whose value is a bare `{{workflow.input.<key>}}` the caller did not supply is **dropped from the arguments**, so the tool's own default applies without a guard. Reach for `coalesce` when the fallback must be a value *you* name — and note what is NOT dropped: a caller-supplied `null`, an input supplied with the wrong shape to navigate into, and anything in the workflow's own `output_mapping`. Those still arrive as an explicit nil, which a tool that stringifies its arguments can turn into the literal text `<nil>` while reporting success.

### Concat (string construction)
```yaml
output_mapping:
  topic:
    concat:
      - "results/"
      - "{{workflow.input.category}}"
      - "/"
      - "{{process.output.id}}"
```

### Type preservation rules
- `"{{node.output.count}}"` — preserves the original type (number, array, object)
- `"Found {{node.output.count}} items"` — always returns a string (embedded template)
- Missing values return nil (safe for coalesce chains) — except an unsupplied `workflow.input` key in a node `input:`, whose key is dropped instead (see §5)

---

## 5. Template Expressions

Templates use `{{...}}` syntax to reference data. Available variables:

| Expression | Description |
|-----------|-------------|
| `{{workflow.input}}` | Entire workflow input |
| `{{workflow.input.field}}` | Specific input field |
| `{{workflow.input.field.nested}}` | Nested input field |
| `{{node_id.output}}` | Entire output of a completed node |
| `{{node_id.output.field}}` | Specific output field |
| `{{workflow.run_id}}` | This execution's unique id |
| `{{workflow.task_id}}` | The task this execution belongs to |
| `{{workflow.status}}` | Workflow status (in exit handlers) |
| `{{workflow.error.message}}` | Error message (in exit handlers) |
| `{{_map_item}}` | Current item in a map iteration |
| `{{_map_index}}` | Current index in a map iteration (0-based) |
| `{{_loop_iteration}}` | Current loop iteration counter (0-based) |

**Rules:**
- A bare template `"{{x}}"` — the expression is the ENTIRE string value — **preserves the original type** (number, array, object, string). This case is checked first, so `classes: "{{workflow.input.classes}}"` delivers a real array, not `"[a b c]"`.
- A string that **mixes** `{{...}}` with other text is **interpolated**: each expression is replaced by its string value and the surrounding text is preserved verbatim. The result is always a string. Both forms work in node `input:` and in `output_mapping`.
- **Choose deliberately between the two.** Whenever you need the value's real type — an array, a number, a map handed to a tool argument — the template must be the whole string. Adding so much as a space around it silently converts it to a string.
- A reference to a node that never ran, or a missing `workflow.input` key, resolves to nothing rather than erroring — safe for coalesce chains. The two differ in what they leave behind in a **node `input:`**: an unsupplied `workflow.input` key is **dropped from the arguments**, so the receiving tool applies its own default; an unknown node arrives as an explicit nil on a key that is still sent. Three things are NOT dropped — a caller-supplied `null`, an input supplied with the wrong shape to navigate into (`{{workflow.input.user.id}}` where `user` is a string), and the workflow's own `output_mapping`, which keeps the field so a parent's strict `{{<node>.output.<field>}}` reference does not fail on it.
- In an interpolated string, either case is left **verbatim**: `{{workflow.input.camera_url}}` stays in the text as those literal characters, it does not become empty and it is not dropped. So guard anything you interpolate into SQL, a filename or a URL on the receiving side.
- In a **condition** (`when:`, a switch case, a loop condition) a `workflow.input` key the caller did not supply — or supplied as `null` — is not compared as that literal text. It is unresolved, so the condition answers only if the rest of it settles the answer either way, and is otherwise **not matched**: a `when:` skips its node, a switch case does not match (so `default:` is taken), and a loop **stops** with `stopped_reason: condition_false`. That is an ordinary outcome, not a stale reference: no WARN, no `unresolved_reference` flag, and it does not count toward `cases_unresolved`. The same holds when such a condition also references a node that did not run, such as one on a branch not taken. The unquoted form behaves the same (`{{workflow.input.n}} > 5` does not match when `n` is missing); a condition that does not parse, or fails to evaluate whatever the missing input would have been, still fails the node; one with an arm that fails only for some values of the missing input simply does not match, so an arm reached only through an omitted input is not checked at all — a misspelled function name there goes unreported until the input is supplied.
  - So `when: "'{{workflow.input.call_id}}' != '' or '{{workflow.input.text}}' != ''"` runs the node when either input was supplied non-empty and skips it when neither was supplied.
  - The flip side: `'{{workflow.input.dry_run}}' != 'true'` over an optional `dry_run` the caller left out does **not** match — in a `when:` it skips the node, in a loop condition it stops the loop. To branch on an optional flag, gate on the supplied value (`== 'true'`) and route the other case through a switch `default:`, or make the input `required`. A loop condition has no such route: "continue unless an optional flag is set" cannot be written over an omitted input (`not (… == 'true')` stops too), so make that input `required`. A loop stopped this way reports `condition_false`, the same as a body that reported done.
  - A `default:` declared in `input_schema` is **not** applied at run time, so it does not stand in for an omitted input here.
  - A key the caller supplied as `""` is data and compares normally.
- A reference to a field that does **not** exist on a node that DID run is a hard error and fails the node — in a bare template and in an interpolated string alike. That is the misspelled-field case, and it is deliberately loud. The workflow's own `output_mapping` is the exception: there the reference is left verbatim and logged (§2.6), so a field an agent node never declared comes back as literal `{{…}}` text in a run that reports success.
- Deep field access uses dot notation: `{{node.output.results.0.name}}`.

🛑 **`{{workflow.run_id}}` is per execution, so a sub-workflow's is its OWN, not its caller's.** To group rows written by several parallel branches of one run, resolve the id in the **parent** and pass it down as an explicit input; reading `{{workflow.run_id}}` inside each child yields four different values and four ungrouped sets of rows.

```yaml
- id: annotate_1
  type: workflow
  workflow_name: DetectAndAnnotateFromUrl
  input:
    run_id: "{{workflow.run_id}}"    # the PARENT's id — shared by every branch
```

⚠️ **Don't let a caller supply the grouping id instead.** A schedule sends the same message on every fire, so a caller-supplied id would be identical each time — and combined with an idempotent `ON CONFLICT DO NOTHING` insert, the first run writes its rows and every run after it writes none, each reporting success.

**Interpolation is what makes a parameterized late embed possible.** An embed directive is opaque to the workflow engine — the `«...»` and `>>>` markers pass through untouched — so a node can build one whose target is a filename produced upstream, and the tool's own resolver expands it later:

```yaml
- id: detect
  type: tool
  tool_name: detect_objects
  depends_on: [fetch]
  input:
    # {{fetch.output.filename}} is substituted here; the «...» directive is
    # preserved and resolved downstream, so the image bytes never pass
    # through the workflow engine or any LLM context.
    image: "«artifact_content:{{fetch.output.filename}} >>> format:datauri»"
```

The same mechanism lets a connector node build a SQL statement from node outputs — see "Connector-backed tool nodes" in §2.6.

⚠️ **Interpolating into SQL is string substitution, not parameter binding.** Values are pasted in as text. Only build a statement this way from values you control — node outputs you produced, workflow inputs you declared and constrained in `input_schema`. Never interpolate free-form text that originated with an end user.

**Anti-patterns.** The first three fail *silently* — the expression resolves to nil or stays literal, so the node reports success on wrong data. The last one fails *loudly*, and is the only one a check will catch for you:
- `{{nodes.<id>.output...}}` — there is NO `nodes.` prefix. Reference a node by its bare id: `{{<node_id>.output.<field>}}`. A node id that matches nothing resolves to nothing and is left in the value verbatim, so `report_{{fetchh.output.filename}}.pdf` reaches the tool as a filename with braces in it. The workflow warns about that when it starts, naming the nodes that do exist — unless the reference sits inside a `coalesce` or `concat`, where naming a node that may not exist is how a fallback is written.
- Liquid filters — `{{x | default: 'y'}}`, `{{x | replace: ' ', '_'}}` are Liquid syntax, not workflow templates. Not supported.
- JSON-encoded strings for nested tool args — write `input_files: {sales: "{{workflow.input.file}}"}` as a YAML map, never `input_files: '{"sales": "{{workflow.input.file}}"}'`. The expression inside the JSON string *does* get interpolated, which is what makes this deceptive: the argument arrives as a **string** that merely looks like an object, and the tool rejects it or reads nothing. Structure belongs in YAML; templates fill the leaves.
- Invented output field names — bind to the field the upstream node actually returns. A tool node's output is the tool's result data (e.g. `query_data_with_sql` returns `output_filename`; `instantiate_template` returns `filename`); an agent node's output follows its `output_schema_override` or its agent's `output_schema`, and an agent node that declares neither guarantees **no** fields — bind its whole `{{<id>.output}}`, or declare the fields and name them in the agent's instruction. Build validation rejects a field reference into an agent node in the build that declares no output schema, and a field outside the schema it does declare. On a **built-in** the workflow logs a warning as it starts, naming the fields that do exist (§2.6 lists them per tool), and the node fails at run time when the reference resolves. On an **MCP tool whose server declares an `outputSchema`** you get that same start-up warning, naming the fields the schema declares — including for a field inside `mcp_structured_content`, which is checked one level down — but no plan-time check, because a CLI cannot tell a discovered schema from a built-in that shares the name. On any other MCP, OpenAPI, toolset or connector tool there is no schema to check against, so nothing is checked up front and the run-time failure is the only signal you get. In `output_mapping` none of these fail the run — the reference is left verbatim in the output.

---

## 6. Condition Expressions

Used in `when` (conditional node execution) and switch `cases`.

Uses the `expr-lang/expr` engine. Available operators:
- Comparison: `==`, `!=`, `<`, `>`, `<=`, `>=`
- Logical: `and`, `or`, `not`
- Membership: `in`
- **There is no nil check.** `!= nil`, `== nil` and `!= null` do not work against a field and never did: a template resolves to text before the expression is parsed, so there is nothing for `nil` to compare against, and `null` is not an expr literal at all (`== null` errors in every state, present or absent). To branch on "the tool found nothing", have the tool return a present-but-empty value and compare against it — an MCP tool that sends `structuredContent: {}` gives you `'{{n.output.mcp_structured_content}}' == '{}'`. A condition that instead references a field the node did not produce answers only if the rest of it settles the answer either way; otherwise it abstains, and either way it warns (see §2.6).

**Critical: quote string templates in conditions:**
```yaml
# Correct — template result compared as string:
condition: "'{{classify.output.category}}' == 'premium'"

# Wrong — unquoted template, expr engine gets confused:
condition: "{{classify.output.category}} == 'premium'"
```

**Numeric comparisons don't need quotes:**
```yaml
condition: "{{score_node.output.score}} > 0.8"
```

**Combining conditions:**
```yaml
when: "'{{node.output.status}}' == 'ready' and {{node.output.count}} > 0"
```

The rule when one reference in a compound condition names a field the node did not produce: **the condition answers only if its answer is the same however that reference would have come out; otherwise it does not match.** Either way the workflow logs a WARN naming it. So `'{{a.output.x}}' == 'no' or '{{a.output.present}}' == 'yes'` still matches on the live arm, while `'{{a.output.present}}' == 'yes' and '{{a.output.x}}' != '0'` does **not** — the missing field decides that one, and answering it would run a branch on data nobody produced.

---

## 7. Timeouts

### Workflow-level timeout
```yaml
workflow_timeout: "10m"    # Max total workflow duration. Go duration string.
```

### Default node timeout
```yaml
default_node_timeout: "2m"  # Applied to nodes without their own timeout.
```

### Per-node timeout
```yaml
- id: slow_step
  type: agent
  agent_name: SlowAgent
  timeout: "5m"             # Overrides default_node_timeout for this node.
```

**Go duration strings:** `"30s"`, `"5m"`, `"1h"`, `"1h30m"`.

**Priority order:**
1. Node-specific `timeout` (highest)
2. Workflow `default_node_timeout`
3. No timeout (unlimited — avoid in production)

---

## 8. Retry Strategy

Exponential backoff for transient failures. Can be set at workflow level (default for all nodes) or per node.

### Workflow-level default
```yaml
retry_strategy:
  limit: 3                    # Max retries. Total executions = limit + 1.
  retry_policy: OnError       # OnFailure | OnError | Always
  backoff:
    duration: "1s"            # Initial delay.
    factor: 2.0               # Exponential multiplier: 1s, 2s, 4s, 8s...
    max_duration: "30s"       # Cap on backoff delay.
```

### Per-node override
```yaml
- id: critical_step
  type: agent
  agent_name: CriticalAgent
  retry_strategy:
    limit: 5
    retry_policy: OnError
    backoff:
      duration: "2s"
      factor: 2.0
      max_duration: "1m"
```

**Retry policies:**
- `OnFailure` — retry when the agent reports failure (not on timeouts or system errors)
- `OnError` — retry on any error including timeouts (most common choice)
- `Always` — retry regardless of outcome (rarely useful)

---

## 9. Execution Control

```yaml
fail_fast: true       # Default. Stop entire workflow on first node failure.
max_call_depth: 6     # Max nesting depth for nested workflow calls.
```

**`fail_fast: true`** (default) — if any node fails, cancel running nodes and stop. Use when all steps are essential.

**`fail_fast: false`** — continue executing other branches despite failures. Use when:
- Some paths are optional
- You want partial results
- Independent branches should complete even if one fails

With `fail_fast: false`, always use `coalesce` in output mapping to handle missing results from failed branches.

---

## 10. Exit Handlers

Nodes that run after workflow completion, regardless of success or failure.

### Simple form (always runs)
```yaml
on_exit: "cleanup_node"
```

### Object form (conditional)
```yaml
on_exit:
  always: "cleanup"          # Runs on any outcome.
  on_success: "notify"       # Runs only on success.
  on_failure: "alert_ops"    # Runs only on failure.
  on_cancel: "cleanup_partial"  # Runs only on cancellation.
```

Exit handler nodes can inspect workflow status:
```yaml
- id: alert_ops
  type: agent
  agent_name: Alerter
  input:
    status: "{{workflow.status}}"
    error: "{{workflow.error.message}}"
```

**Rules:**
- Exit handler nodes must be defined in the `nodes` array like any other node.
- They should NOT appear in regular nodes' `depends_on` chains.
- They run after all regular nodes complete (or fail).

---

## 11. Workflow Agent Card

Workflows publish an agent card just like agents — this is how they're discovered by entrypoints and peer agents.

```yaml
agent_card:
  description: "Processes incoming data through extraction, analysis, and reporting."
  skills:
    - id: data-pipeline
      name: Data Pipeline
      description: "End-to-end data processing with extraction, analysis, and report generation."

card_publish_interval_seconds: 5    # How often to publish. Same guidance as agents.
```

**Rules:**
- Without an `agent_card`, the workflow is invisible to entrypoints and cannot receive tasks.
- `card_publish_interval_seconds: 5` for builder-generated configs (reliable discovery).
- The `skills` entries describe the workflow's capabilities — same concept as agent card skills.

---

## 12. Input/Output Schemas

Optional but recommended for workflows that need data validation.

```yaml
input_schema:
  type: object
  properties:
    text: { type: string, description: "Text to process" }
    options:
      type: object
      properties:
        language: { type: string, enum: ["en", "fr", "de"] }
  required: ["text"]

output_schema:
  type: object
  properties:
    result: { type: string }
    confidence: { type: number, minimum: 0, maximum: 1 }
  required: ["result"]
```

- Input is validated before any node runs. A run whose input does not satisfy `input_schema` fails with `Input validation failed:` and every violated field, and no node or `on_exit` handler runs. A workflow with no `input_schema`, or one whose schema is not a valid JSON Schema (it warns at startup), is not checked.
- With an `input_schema` declared, a plain-text message whose text is a JSON object is read as the input document, unless the schema also accepts `{"text": "<message>"}` and the object fails the schema or names an undeclared field; any other text is `{"text": "<message>"}`. The chosen form is checked like any input.
- `output_schema` is published to callers and the visualization; it is not checked against the workflow's result.
- Useful for workflows invoked as nested sub-workflows or via A2A.

---

## 13. Environment Variables

Same conventions as agents:

| Variable | Purpose |
|----------|---------|
| `${NAMESPACE, solace-agent-mesh}` | Broker namespace |
| `${LLM_SERVICE_ENDPOINT}` | LLM API base URL (for agent nodes) |
| `${LLM_SERVICE_API_KEY}` | LLM API key (for agent nodes) |
| `${LLM_SERVICE_GENERAL_MODEL_NAME}` | Default model (for agent nodes) |

Note: Environment variables are used in the **agent configs** referenced by workflow nodes, not typically in the workflow config itself (which contains node definitions and templates).

---

## 14. Common Configuration Patterns

### Linear pipeline
```yaml
apps:
  - name: data_pipeline
    app_config:
      agent_name: data_pipeline
      agent_type: workflow
      workflow_timeout: "10m"
      default_node_timeout: "3m"
      output_mapping:
        result: "{{format.output}}"
      agent_card:
        description: "Processes data through extraction, analysis, and formatting."
        skills:
          - id: pipeline
            name: Data Processing
            description: "End-to-end data processing pipeline."
      card_publish_interval_seconds: 5
      nodes:
        - id: extract
          type: agent
          agent_name: Extractor
          input: { raw_data: "{{workflow.input.data}}" }
        - id: analyze
          type: agent
          agent_name: Analyzer
          depends_on: [extract]
          input: { extracted: "{{extract.output}}" }
        - id: format
          type: agent
          agent_name: Formatter
          depends_on: [analyze]
          input: { analysis: "{{analyze.output}}" }
```

### Fan-out / fan-in (parallel branches)
```yaml
apps:
  - name: multi_analysis
    app_config:
      agent_name: multi_analysis
      agent_type: workflow
      output_mapping:
        report: "{{aggregate.output}}"
      agent_card:
        description: "Performs parallel security and performance analysis."
        skills:
          - id: analysis
            name: Code Analysis
            description: "Parallel security and performance code analysis."
      card_publish_interval_seconds: 5
      nodes:
        - id: security_scan
          type: agent
          agent_name: SecurityScanner
          input: { code: "{{workflow.input.code}}" }
        - id: performance_scan
          type: agent
          agent_name: PerformanceScanner
          input: { code: "{{workflow.input.code}}" }
        - id: aggregate
          type: agent
          agent_name: ReportWriter
          depends_on: [security_scan, performance_scan]
          input:
            security: "{{security_scan.output}}"
            performance: "{{performance_scan.output}}"
```

### Conditional routing (switch)
```yaml
apps:
  - name: routed_pipeline
    app_config:
      agent_name: routed_pipeline
      agent_type: workflow
      output_mapping:
        result:
          coalesce:
            - "{{premium_handler.output}}"
            - "{{standard_handler.output}}"
            - "{{fallback_handler.output}}"
      agent_card:
        description: "Routes requests to appropriate processing tier."
        skills:
          - id: routing
            name: Request Routing
            description: "Classifies and routes requests to premium or standard processing."
      card_publish_interval_seconds: 5
      nodes:
        - id: classify
          type: agent
          agent_name: Classifier            # Classifier's output_schema declares `tier` and `data`
          input: { request: "{{workflow.input}}" }
        - id: route
          type: switch
          depends_on: [classify]
          cases:
            - condition: "'{{classify.output.tier}}' == 'premium'"
              node: premium_handler
            - condition: "'{{classify.output.tier}}' == 'standard'"
              node: standard_handler
          default: fallback_handler
        - id: premium_handler
          type: agent
          agent_name: PremiumProcessor
          depends_on: [route]
          input: { data: "{{classify.output.data}}" }
        - id: standard_handler
          type: agent
          agent_name: StandardProcessor
          depends_on: [route]
          input: { data: "{{classify.output.data}}" }
        - id: fallback_handler
          type: agent
          agent_name: FallbackProcessor
          depends_on: [route]
          input: { data: "{{classify.output.data}}" }
```

### Batch processing (map)
```yaml
apps:
  - name: batch_processor
    app_config:
      agent_name: batch_processor
      agent_type: workflow
      output_mapping:
        results: "{{process_all.output}}"
        summary: "{{summarize.output}}"
      agent_card:
        description: "Processes batches of items in parallel."
        skills:
          - id: batch
            name: Batch Processing
            description: "Process collections of items with parallel execution."
      card_publish_interval_seconds: 5
      nodes:
        - id: process_all
          type: map
          node: process_one
          items: "{{workflow.input.items}}"
          concurrency_limit: 5
          max_items: 50
        - id: process_one
          type: agent
          agent_name: ItemProcessor
          input:
            item: "{{_map_item}}"
            index: "{{_map_index}}"
        - id: summarize
          type: agent
          agent_name: Summarizer
          depends_on: [process_all]
          input: { results: "{{process_all.output}}" }
```

### Iterative refinement (loop)
```yaml
apps:
  - name: refinement_loop
    app_config:
      agent_name: refinement_loop
      agent_type: workflow
      output_mapping:
        result: "{{refine_step.output.result}}"
        iterations: "{{refine_step.output.iteration_count}}"
      agent_card:
        description: "Iteratively refines output until quality threshold is met."
        skills:
          - id: refine
            name: Iterative Refinement
            description: "Repeatedly improves output until quality score exceeds threshold."
      card_publish_interval_seconds: 5
      nodes:
        - id: refine
          type: loop
          node: refine_step
          condition: "{{refine_step.output.quality_score}} < 0.9"
          max_iterations: 5
          delay: "1s"
        - id: refine_step
          type: agent
          agent_name: Refiner               # Refiner's output_schema declares `result`,
                                            # `iteration_count` and `quality_score`
          input:
            previous: "{{refine_step.output.result}}"
            iteration: "{{_loop_iteration}}"
            original: "{{workflow.input.text}}"
```

### Workflow with error handling and exit handlers
```yaml
apps:
  - name: robust_pipeline
    app_config:
      agent_name: robust_pipeline
      agent_type: workflow
      workflow_timeout: "15m"
      default_node_timeout: "3m"
      fail_fast: false
      retry_strategy:
        limit: 2
        retry_policy: OnError
        backoff:
          duration: "2s"
          factor: 2.0
          max_duration: "30s"
      on_exit:
        always: "cleanup"
        on_failure: "alert"
      output_mapping:
        result:
          coalesce:
            - "{{process.output}}"
            - "Error: processing failed"
      agent_card:
        description: "Robust data pipeline with retry and error handling."
        skills:
          - id: process
            name: Robust Processing
            description: "Process data with automatic retry and failure alerting."
      card_publish_interval_seconds: 5
      nodes:
        - id: process
          type: agent
          agent_name: Processor
          input: { data: "{{workflow.input}}" }
          retry_strategy:
            limit: 3
            retry_policy: OnError
        - id: cleanup
          type: agent
          agent_name: Cleaner
          input:
            status: "{{workflow.status}}"
        - id: alert
          type: agent
          agent_name: Alerter
          input:
            error: "{{workflow.error.message}}"
            status: "{{workflow.status}}"
```

### Nested sub-workflow
```yaml
apps:
  - name: orchestrator
    app_config:
      agent_name: orchestrator
      agent_type: workflow
      output_mapping:
        result: "{{process.output}}"
      agent_card:
        description: "Orchestrates validation and processing sub-workflows."
        skills:
          - id: orchestrate
            name: Orchestration
            description: "Coordinates validation and processing workflows."
      card_publish_interval_seconds: 5
      nodes:
        - id: validate
          type: workflow
          workflow_name: ValidationWorkflow
          input: { data: "{{workflow.input}}" }
        - id: process
          type: workflow
          workflow_name: ProcessingWorkflow
          depends_on: [validate]
          input: { validated: "{{validate.output}}" }
```

---

## 15. Multi-Component Build Requirements

When building a workflow, the builder must generate multiple configs:

1. **The workflow config** — the `agent_type: workflow` YAML shown above.
2. **Agent configs for each node** — every distinct `agent_name` referenced in agent nodes needs a separate agent config. These agents should be configured as autonomous workflow nodes:
   - `supports_streaming: false`
   - `output_schema` defined for structured output
   - No `ask_user_question` tool (no `general_agent_tools` group)
   - `artifact_management` tools if the agent needs to work with artifacts
   - Instruction should state the agent operates autonomously
3. **Sub-workflow configs** — any `workflow_name` referenced in workflow nodes needs its own workflow config.
4. **Build manifest** — tracks all generated components with status, dependencies, and env vars.

The agent configs must be saved first, then the workflow config, because the workflow depends on the agents being available.

---

## 16. Field Interaction Rules

Critical constraints not captured in the JSON Schema:

1. **Template references require `depends_on`**: If a node uses `{{other_node.output}}`, `other_node` must be in that node's `depends_on`. Otherwise the referenced node may not have completed.

2. **Switch targets need switch in `depends_on`**: Nodes targeted by a switch case must include the switch node in their `depends_on` array.

3. **Map/loop body nodes are NOT dependencies**: The template/body node of a map or loop is invoked by the map/loop — it should NOT appear in other nodes' `depends_on`.

4. **`fail_fast: false` pairs with `coalesce`**: When using `fail_fast: false`, always use `coalesce` in `output_mapping` to handle missing results from failed branches.

5. **Exit handler nodes are separate**: Exit handler nodes defined in `on_exit` should not appear in regular nodes' `depends_on` chains. They run after all regular nodes complete.

6. **`agent_card` required for discoverability**: Without an `agent_card`, the workflow is invisible to entrypoints. Always include one with at least one `skills` entry.

7. **`agent_type: workflow` is required**: Forgetting this makes the config be treated as a regular agent, not a workflow.

8. **Node IDs must be unique**: Every node in the `nodes` array must have a unique `id`. Node IDs are used in template expressions and `depends_on` references.

9. **Circular dependencies are forbidden**: The DAG must be acyclic. A node cannot depend on itself or create a dependency cycle (except through loop/map body nodes which handle iteration internally).

10. **String comparisons in conditions need quotes**: Always quote template expressions in string comparisons: `'{{node.output.x}}' == 'value'`. Numeric comparisons don't need quotes.
