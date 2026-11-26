---
name: sam-agent-schema
description: JSON Schema for Solace Agent Mesh agent configuration — all fields, types, defaults, and examples.
---

Solace Agent Mesh agent configuration schema and practical guide. Every field in an agent's `app_config` is defined below, along with valid values, field interactions, and common patterns.

For **tool details** see `sam-tool-schema`. For **design guidance** see `sam-agent-design`.

## Schema

```yaml
{{SCHEMA:agent}}
```

---

## 1. Identity Fields

Note: System fields (`namespace`, `model`, `display_name`, `session_service`, `artifact_service`, `agent_card_publishing`, `agent_discovery`) are injected by the platform at deployment time and must not be included in generated configs. The one model setting you do write is `model_provider` (section 2).

```yaml
agent_name: my_agent          # Required. Unique ID for broker topics/routing. Use snake_case.
description: "..."             # Required. Free text description of the agent.
version: "1.0"                 # Optional semver string.
agent_type: standard           # "standard" (default) or "workflow" (used by workflow engine).
```

The platform sets `agent_type`; the Builder does not save it.

**Rules:**
- `agent_name` must be unique across the deployment. Convention: `snake_case`, no hyphens.
- For platform-deployed agents, the name is auto-generated as `agent_<uuid>` with underscores.

---

## 2. LLM Model Configuration

### Which model an agent uses: `model_provider`

An agent's model is bound by `model_provider`, a key **inside `app_config`**. It holds the name (the alias on the Models page) of a model the platform has configured:

```yaml
apps:
  - name: my_agent
    app_config:
      agent_name: my_agent
      model_provider: ["planning"]
```

- Get the names from `platform_list_components` with `component_type: model`. Use a chat model; a decision model is refused. A name that is not configured is refused by the save and by the preview.
- Give one entry. The platform binds one model per agent, so a list of two, an empty name, or a value that is not a name is refused.
- Leave `model_provider` out to use the platform default on a new agent. On an existing agent, leaving it out keeps the model the agent already has; `model_provider: []` returns it to the platform default.
- To change the model of an existing agent, edit its artifact and set `app_config.model_provider`. Nothing else changes the model.
- **Never write `model:`**, with a name (`model: general`), a provider string (`model: "openai/gpt-4o"`) or the object form. The platform never reads it, so the agent would stay on its old model. The save and `validate_component_config` refuse it, whether it sits in `app_config` or beside it on the `apps[]` item; so is `model_provider` written beside `app_config` instead of inside it. When the user names a model by its provider string (for example `anthropic/claude-sonnet-5`) and no listed name is clearly that model, ask which listed name they mean, or tell them to add one on the Models page: the Builder cannot create models.

### Runtime config files (`sam run`), not the Builder

A hand-written config file that the platform does not generate sets the model with `model:`. Two forms are supported:

#### Simple string form
```yaml
model: "openai/gpt-4o"
```

#### Object form (for custom endpoints)
```yaml
model:
  model: "anthropic/claude-4-5-sonnet"
  api_base: "${LLM_SERVICE_ENDPOINT}"
  api_key: "${LLM_SERVICE_API_KEY}"
  cache_strategy: "5m"         # "none", "5m", "1h" (default "5m")
```

#### Model string format
Format: `"provider/model-name"` — the provider prefix tells the LLM client which API format to use.

**Supported providers and common models:**

| Provider | Example models |
|----------|---------------|
| `openai` | `openai/gpt-4o`, `openai/gpt-4o-mini`, `openai/o3-mini` |
| `anthropic` | `anthropic/claude-4-5-sonnet`, `anthropic/claude-4-5-haiku` |
| `google` | `google/gemini-3.6-flash`, `google/gemini-3.5-flash`, `google/gemini-3.1-pro-preview` |
| `vertex` | `vertex/gemini-3.5-flash` |
| `bedrock` | `bedrock/anthropic.claude-v2` |
| `azure` | `azure/gpt-4o` (requires api_base pointing to Azure endpoint) |
| `cohere` | `cohere/command-r-plus` |
| `mistral` | `mistral/mistral-large-latest` |
| `groq` | `groq/llama-3.1-70b-versatile` |
| `perplexity` | `perplexity/llama-3.1-sonar-large-128k-online` |
| `ollama` | `ollama/llama3.1` (requires local api_base) |
| `xai` | `xai/grok-2` |
| `openrouter` | `openrouter/meta-llama/llama-3.1-405b-instruct` |

**Critical rules:**
- For `openai/` models (which cover any OpenAI-compatible endpoint), `api_base` is either a base URL or the provider's complete chat-completions URL. With a base URL, the client appends `/v1/chat/completions`, dropping one trailing `/v1` from the base first. If the provider does not serve its API under `/v1`, give the complete URL, whose path ends in `/chat/completions` (for example `https://ark.cn-beijing.volces.com/api/v3/chat/completions`); the client calls it exactly as written. Models that accept a request only through the OpenAI Responses API (the `-pro` models, and some GPT-5 models when the request includes tools) do not work with the complete form.
- In a runtime config file, use env vars: `${LLM_SERVICE_ENDPOINT}`, `${LLM_SERVICE_API_KEY}`, `${LLM_SERVICE_GENERAL_MODEL_NAME}`.

### LLM control parameters
```yaml
supports_streaming: false      # Platform default.
max_tokens: 0                  # 0 = the model's own output ceiling for Claude 5.x on the Anthropic, Bedrock, Vertex and Azure providers, otherwise the provider default. Set only if you need to limit output.
# max_llm_calls_per_task: 30   # Model calls per task; 30 is the default. Uncomment and set about 60 only for an agent that gathers data and then builds a document (deck, report).
enable_auto_continuation: true # Allow the agent to continue if response is truncated.
```

The Builder saves every key in this schema. Leaving a key out keeps the agent's stored value; to remove a stored `output_schema`, a setting or a block, write the key with `null`. A value such as `output_schema` is replaced whole, not merged. A key the platform manages (`agent_type`, `internal`, `required_scopes`, `skills_base_path`, `skills_cache_path`, `volumes`, `networks`, and `allow_loopback` inside a built-in tool's `tool_config`) is not saved by the Builder: the save keeps the stored value and warns once if you state one. `null` removes a stored setting with two exceptions: peer access needs `allow_list: []`, and the platform-managed keys cannot be removed from the Builder.

**About `max_llm_calls_per_task`:** a positive whole number is saved with the agent and deployed as written. Zero, a negative number, a fraction or text is refused, and nothing is saved until it is fixed. Leaving the key out of an update keeps the stored limit, and `null` removes it, so the agent runs with the default of 30.

**About `supports_streaming`:** leave it out unless the agent must advertise streaming; a new agent runs with `false`.

---

## 3. Instruction (System Prompt)

```yaml
instruction: |
  You are a security auditor agent...
```

The instruction is the most important field for agent quality. See the `sam-agent-design` skill for the full five-section writing guide. Key points:

- Use YAML block scalar (`|`) for multi-line instructions.
- Structure: Role → Expertise → Guidelines → Constraints → Skill references.
- Keep under ~2000 tokens. Move reference material into skills.
- For interactive agents: include tone/personality guidance and when to ask for clarification.
- For autonomous agents: explicitly state "do not ask for clarification" and describe fallback behavior.

### Injection flags
```yaml
inject_current_time: true          # Stamps each user turn with weekday, date, time and IANA zone
inject_system_purpose: false       # Prepends Solace Agent Mesh platform purpose statement
inject_response_format: false      # Prepends response format guidance
inject_user_profile: false         # Prepends user profile info (enterprise)
```

These are rarely needed — most agents should set their own context in the instruction.

---

## 4. Agent Card (Discovery Identity)

**Required for every agent.** Without an agent card, the agent is invisible to entrypoints and peer agents.

```yaml
agent_card:
  description: "Analyzes code for security vulnerabilities and generates compliance reports."
  version: "1.0"
  defaultInputModes: [text]           # What the agent accepts: text, file, data
  defaultOutputModes: [text, file]    # What the agent produces: text, file, data
  skills:                             # Capabilities (NOT knowledge-bundle skills)
    - id: code-review
      name: Code Review
      description: >-
        Review code for security vulnerabilities, performance issues,
        and coding standard violations.
      tags: [security, code-quality]
      examples:
        - "Review this Python file for SQL injection vulnerabilities"
        - "Check this API endpoint for authentication issues"
    - id: compliance-report
      name: Compliance Reporting
      description: >-
        Generate SOC2, PCI-DSS, and HIPAA compliance reports.
  provider:
    organization: "My Company"
    url: "https://example.com"
```

**Important distinctions:**
- `agent_card.skills` = **capabilities** advertised for discovery (A2A protocol term). These are what the entrypoint and peer agents match against when routing tasks.
- `skills` (top-level) = **knowledge-bundle skills** (directories with reference docs). Completely different concept, confusingly similar name.

**Writing good capability descriptions:**
- Each skill.description should be specific enough that the entrypoint can match a user request to it.
- Include keywords the user would naturally use when asking for this capability.
- Keep descriptions non-overlapping across skills on the same agent.
- Bad: `"Does stuff with code."` — Good: `"Reviews Python code for OWASP Top 10 vulnerabilities with file paths and remediation steps."`

**Input/output modes:**
- `text` — plain text messages (almost always included)
- `file` — file uploads (input) or file artifacts (output)
- `data` — structured data (JSON, CSV, etc.)

---

## 5. Tool Groups and Individual Tools

### Built-in tool groups

Use `tool_type: builtin-group` to add all tools in a named group:

```yaml
tools:
  - tool_type: builtin-group
    group_name: artifact_management
```

{{TOOL_GROUPS_TABLE}}

### Individual built-in tools

Use `tool_type: builtin` when you need only specific tools or per-tool configuration:

```yaml
tools:
  - tool_type: builtin
    tool_name: web_request
    hil:
      require_approval: true
      approval_message: "This will make an HTTP request."
      timeout: "5m"
      show_args: true
```

### Secure Tool Runtime-backed builtin tools

Tools that ship with Solace Agent Mesh but run out-of-process in the Secure Tool Runtime
(e.g. `mermaid_diagram_generator`, `create_chart_from_plotly_config`,
`execute_python_code`, `ffmpeg`, `pptx_*`) are declared the same way as
in-process builtins when a built-in group registers them. A tool that no built-in group
registers cannot be saved by the Builder under `tool_type: builtin`: a skill's tools
(`xlsx_read`, `docx_create_document`) come with the skill listed under `skills:`, a
connector's runtime (`execute_sql_query`, `graph_db_query`, `slack`) comes with a
connector linked to the agent, and a custom tool ships in an uploaded toolset.

A save that sets `required_scopes` or `auth` on a built-in entry is refused; remove them from the entry.
A save that sets `effect` on a built-in entry is refused as well, because the platform cannot store it and dropping it would loosen the tool's effect classification.

The custom-tool form below is for runtime config files; the Builder does not save it:

```yaml
tools:
  - tool_type: builtin
    tool_name: analyze_data
    tool_description: "Analyzes dataset and produces statistical summary."
    sandbox_profile: standard         # restrictive | standard | permissive
    parameters:
      dataset:
        type: artifact
        description: "The CSV dataset to analyze"
        required: true
      output_format:
        type: string
        description: "Output format: 'summary' or 'detailed'"
        required: false
        default: "summary"
```

**Sandbox profiles:**
- `restrictive` — 512MB RAM, 60s CPU, network isolated. For pure computation.
- `standard` — 1GB RAM, 300s CPU, network access. Default.
- `permissive` — 4GB RAM, 600s CPU, full access. For heavy processing.

**Special parameter types:**
- `artifact` — accepts an artifact filename as input
- `artifact_list` — accepts a list of artifact filenames

### MCP tools (Model Context Protocol)

```yaml
tools:
  - tool_type: mcp
    connection_params:
      type: stdio                      # stdio | sse | streamable-http
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-postgres"]
      env:
        DATABASE_URL: "${DATABASE_URL}"
    allow_list: [query, list_tables]   # Only expose these tools (optional)
```

**Transport types:**
- `stdio` — local process (command + args). Most common for development.
- `sse` — Server-Sent Events remote server (url required).
- `streamable-http` — HTTP streaming remote server (url required).

**Tool filtering** (mutually exclusive):
- `tool_name` — expose exactly one tool
- `tool_name_prefix` — expose tools matching prefix
- `allow_list` — expose only listed tools
- `deny_list` — expose all except listed tools

**Effect** (optional, per entry): every tool is `read_only` or `side_effecting`, and an MCP tool is `side_effecting` unless told otherwise. A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. Changing the run's own session, such as the skills it has loaded, is not outside it. A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. Every other tool is side_effecting. `effect: side_effecting` marks every tool the entry registers side-effecting; `read_only` is not accepted. `trust_annotations: true` lets the server's own `readOnlyHint: true` make its tools `read_only` only when the server also sends `openWorldHint: false` (an omitted `openWorldHint` counts as `true`, the MCP default) and no `destructiveHint: true`; you vouch that such tools meet all three conditions of the rule, and a trusted read-only tool saves any resource it returns under a fresh generated filename, never one the server's URI chose; left out or `false`, the hint is shown but not used. A skill's own tools entry cannot set it: the key is ignored with a warning. An entry that sets `effect` is not retried in the background when its server is unreachable at startup. A built-in entry that sets `effect` is refused on a save through the Builder or platform.

### Tool selection guidance

| Agent needs to... | Recommended tools |
|-------------------|-------------------|
| Chat with users | `general_agent_tools` + `artifact_management` |
| Process files/documents | `artifact_management` + `general_agent_tools` (for `convert_file_to_markdown`) |
| Fetch web content | `web_tools` |
| Analyze data (CSV, JSON) | `data_analysis` + `artifact_management` |
| Generate/process images | `image_tools` |
| Call external APIs | `web_tools` or MCP tool |
| Access a database | MCP tool (postgres, mysql) or a SQL connector component linked to the agent. Never a `tool_type: builtin` entry named `execute_sql_query` |
| Read or write Excel / Word files | The built-in skill `sam-excel` or `sam-docx` under `skills:`, not a `tool_type: builtin` entry for `xlsx_read` or `docx_create_document` |
| Delegate to other agents | No special tools needed — configure `inter_agent_communication` |

---

## 6. Knowledge-Bundle Skills

Skills are directories containing reference documents that agents can load on demand.

```yaml
skills:
  - name: api-reference                   # Directory name under skills_base_path
  - name: tutorials
```

The platform sets `skills_base_path` and `skills_cache_path`; the Builder does not save them.

**How skills work at runtime:**
1. Agent starts with skills registered but not loaded.
2. Agent calls `load_skill` tool to load a skill's instructions and register its tools.
3. Agent calls `grep_skill_resources` to search reference docs.
4. Agent calls `read_skill_resource` to read specific reference files.

**Instruction integration:** When using skills, include guidance in the instruction:
```
You have access to these skills:
- **api-reference**: API documentation. Load when you need endpoint details.
- **tutorials**: Step-by-step guides. Load when explaining how to do something.

When you need reference material:
1. Load the skill with load_skill
2. Search with grep_skill_resources using broad patterns
3. Read specific sections with read_skill_resource
```

**Decks bring `sam-pptx`.** When the agent can call `pptx_create_presentation` (its tools include
`builtin_document_tools`, or that single tool), saving it adds the built-in skill `sam-pptx` to its
skills. A new agent always gets it. An existing agent gets it only when its artifact lists
`skills:`; otherwise it keeps the skills it has. A listed `skills:` replaces the stored list, so
for an existing agent keep its current skills in the list and add `- name: sam-pptx`. While the
agent has the deck tool, every save that lists `skills:` (even `skills: []`) keeps `sam-pptx`. It covers
planning a deck, gathering the figures inside the call budget, layouts, native charts and a visual
check. Have the instruction load it before building a deck.

---

## 7. Session Service

Controls conversation memory persistence.

```yaml
session_service:
  type: sql                                        # "memory" or "sql"
  database_url: "sqlite:///${SAM_DATA_DIR}/sam.db"  # SQLite or PostgreSQL
  default_behavior: PERSISTENT                      # PERSISTENT or RUN_BASED
```

| Type | Backend | Use When |
|------|---------|----------|
| `memory` | In-process (lost on restart) | Testing, workflow nodes, stateless agents |
| `sql` + SQLite URL | SQLite file | Single-instance deployments, desktop mode |
| `sql` + PostgreSQL URL | PostgreSQL | Production multi-instance deployments |

**Database URL formats:**
- SQLite: `"sqlite:///path/to/file.db"` or `"sqlite:///${SAM_DATA_DIR}/sam.db"`
- PostgreSQL: `"postgresql://user:pass@host:5432/dbname"`

**Session behavior:**
- `PERSISTENT` — sessions survive restarts, conversation history maintained across sessions
- `RUN_BASED` — sessions scoped to a single agent run, cleared on restart

**Guidance:**
- Interactive chat agents → `sql` with `PERSISTENT` (users expect continuity)
- Autonomous event-driven agents → `memory` (each event is independent)
- Workflow node agents → `memory` (each invocation is stateless)

---

## 8. Artifact Service

Controls where artifacts (files) are stored.

```yaml
artifact_service:
  type: filesystem                              # filesystem | memory | s3 | gcs
  base_path: "${SAM_DATA_DIR}/artifacts"
  artifact_scope: namespace                     # namespace | global
```

| Type | Use When |
|------|----------|
| `filesystem` | Default. Single-instance. Uses local disk. |
| `memory` | Testing only. Lost on restart. |
| `s3` | Production cloud. Requires `bucket_name`, `region`. |
| `gcs` | Google Cloud. Requires `bucket_name`, `credentials_file`. |

**Additional fields for cloud backends:**
```yaml
artifact_service:
  type: s3
  bucket_name: "my-bucket"
  region: "us-east-1"
  endpoint_url: "https://..."      # Optional custom endpoint (MinIO, etc.)
```

**Artifact scope:**
- `namespace` (default) — artifacts are scoped to the agent's namespace
- `global` — artifacts accessible across namespaces

---

## 9. Artifact Handling Mode

The runtime accepts `ignore`, `embed` or `reference` for this key, but nothing that runs a Builder-saved agent branches on it: files the user attaches reach the agent the same way in every mode.

```yaml
# artifact_handling_mode: ignore    # ignore | embed | reference. Default: ignore
```

The Builder saves this key, but leave it out unless you need a different mode: leaving it out keeps the stored value, and `null` removes it. The agent is told about files the user attaches in any mode; give it `artifact_management` and say in its instruction to `list_artifacts` and `load_artifact` to open them.

**Related settings:**
```yaml
text_artifact_content_max_length: 32768   # Max chars for embedded artifact content
# enable_embed_resolution: true               # Document embeds (math, datetime, status_update) in the prompt. Default: on when the agent has load_artifact
# enable_artifact_content_instruction: true   # Document the artifact_content embed in the prompt. Default: on when the agent has load_artifact
```

### 9.1 An agent can AUTHOR embeds — say so in its instruction

The settings above govern what an agent *reads*. Separately, an agent can **write**
an embed directive into a document or response, and the platform resolves it later:
early embeds (`datetime`, `math`, `uuid`) resolve when the artifact is saved, while
**late embeds (`artifact_content`, `template_liquid`) are preserved verbatim and
resolved by the entrypoint at download time.**

🛑 **This capability is invisible unless the instruction states it, and the failure
is expensive.** An agent asked to assemble a report over images it just produced
will otherwise assume it must inline the bytes — and base64 of even a few PNGs runs
to hundreds of thousands of output tokens. The observed behavior is not an error
message: the agent announces the report, fails to emit it, and the turn ends
"successfully" with no artifact.

**When generating any agent that produces a document referencing artifacts —
reports, summaries with charts, anything with an image — its `instruction` must
include the directive syntax and the prohibition:**

```yaml
instruction: |
  ...
  When your output references an artifact, write an embed directive — never paste
  the artifact's contents or base64 bytes into your response.

    Image:      <img src="«artifact_content:chart.png >>> format:datauri»">
    Text/data:  «artifact_content:results.csv >>> format:csv»

  The directive is resolved when the document is downloaded, so the bytes never
  pass through your output. Valid `format:` values are text, json, json_pretty,
  csv, datauri, and numeric specs like .2f — any other name silently degrades to
  plain text. The target is the artifact's filename.
```

**Prefer a workflow when there is no judgement to apply.** If the document is the
same shape every run, a `.samt` template rendered by an `instantiate_template` tool
node is deterministic and costs zero LLM calls — see `sam-templates` and
`sam-workflow-schema` §2.6. Instruct an agent to author embeds when the report's
*structure* genuinely varies per run.

---

## 10. Tool Output Handling

Controls what happens when tool results are large.

```yaml
tool_output_save_threshold_bytes: 2048    # Auto-save as artifact if output > 2KB
tool_output_llm_return_max_bytes: 4096    # Truncate what the LLM sees to 4KB

# MCP-specific thresholds (separate from built-in tools)
mcp_tool_response_save_threshold_bytes: 2048
mcp_tool_llm_return_max_bytes: 4096
```

When a tool returns more than `tool_output_save_threshold_bytes`, the output is saved as an artifact. The LLM sees a truncated preview (up to `tool_output_llm_return_max_bytes`) plus a reference to the full artifact.

---

## 11. Structured Invocation (Workflow Nodes)

When an agent is used as a workflow node, it can accept and produce structured data.

```yaml
input_schema:
  type: object
  properties:
    text: { type: string, description: "Text to analyze" }
    language: { type: string, enum: ["en", "fr", "de"] }
  required: ["text"]

output_schema:
  type: object
  properties:
    sentiment: { type: string, enum: ["positive", "negative", "neutral"] }
    confidence: { type: number, minimum: 0, maximum: 1 }
    keywords: { type: array, items: { type: string } }
  required: ["sentiment", "confidence"]

validation_max_retries: 2    # Retry if output doesn't match schema (default 2)
```

Leaving `input_schema` or `output_schema` out keeps the stored schema; `output_schema: null` removes it.

**Design rules for structured agents:**
- Set `supports_streaming: false`
- Don't include `ask_user_question` tool
- Keep output schemas simple — deeply nested schemas with many required fields are unreliable
- Use `enum` for categorical outputs (more reliable than free-text)
- Include the schema description in the instruction so the LLM knows the target format

---

## 12. Human-in-the-Loop (HIL)

HIL is configured **per entry**, on the tool's own entry under `tools:`. There is no
agent-level `hil:` block — one was planned and never built, so a block written at
the top of `app_config` gates nothing at all. Never emit one.

An entry-level block applies to **every tool that entry registers**. For `builtin`
that is one. For `openapi` it is one per spec operation, for `builtin-group` every
member of the group, and for `mcp` every tool the server advertises — so a bare
`require_approval: true` there gates reads alongside writes. Use the `tools:`
sub-map to gate individual tools of a multi-tool entry.

```yaml
tools:
  - tool_type: builtin
    tool_name: web_request
    hil:
      require_approval: true
      approval_message: "Fetch {{.url}}?"   # Go text/template over the tool args
      timeout: "5m"                          # Go duration string; default 45m
      show_args: true                        # default true
```

| Field | Type | Default | Purpose |
|---|---|---|---|
| `require_approval` | bool | `false` | Gate every call to this tool. |
| `require_approval_when` | list of rules | empty | Gate per call on argument values. OR-ed with `require_approval`. |
| `approval_message` | string | empty | Prompt text; rendered as a Go `text/template` over the tool's args. |
| `show_args` | bool | `true` | Whether the args panel is rendered. |
| `timeout` | duration string | 45m, fixed | Wait before auto-denying. There is no config key that changes the default — set `timeout` on the entry. |
| `tools` | map of tool name → block | empty | Per-tool overrides on a multi-tool entry. Keyed by the tool's registered name. |

**Approvals on built-in tools.** The Builder saves a `hil:` block on a `tool_type: builtin`
entry for that agent, and on a `tool_type: builtin-group` entry for every member tool the
group grants. To gate one member of a group, give that tool its own `builtin` entry with its
own `hil:`. Leaving `hil` out keeps the stored gate; `hil: null` or `hil: {}` removes it, and the
save names each tool whose stored gate no longer requires approval. A `tool_config` on
a `builtin-group` entry is not saved: put `tool_config` on each tool's own `builtin` entry.
A group's `hil` may carry a `tools:` map of per-member overrides; a name in it that is not a
member of the group is refused.

```yaml
tools:
  - tool_type: builtin-group
    group_name: web_tools
    hil:
      require_approval: true
  - tool_type: builtin
    tool_name: web_request
    hil:
      require_approval: true
      approval_message: "Fetch {{.url}}?"
```

**Rules:**
- A `hil:` block with neither `require_approval` nor `require_approval_when` is a
  **no-op** — the other fields alone gate nothing.
- Reaches `builtin`, `builtin-group`, `openapi`, tools from every connector type that
  provides tools (MCP, `api`, SQL, Slack, knowledge base, document and graph databases,
  search, event mesh, email) through the connector's `values.hil`, and tools from
  a `kind: toolset` package. **Not** skill-bundled tools, where the block is silently
  dropped. `tool_type: sam_remote` no longer exists.
- HIL only works for interactive agents with a connected user. A gated tool called on
  a channel that cannot prompt is **denied**, not auto-approved.
- Gates only add, as in the runtime. A tool entry's `hil` is laid over its group's `hil` field by field, so it can
  change `approval_message`, `show_args` and `timeout`, but it cannot turn the group's gate off or make an
  always-on (`require_approval: true`) gate conditional: a `require_approval: false`, `{}` or `null` there is overridden, as is
  a block that states rules without `require_approval: true` over an always-on gate (the rules are kept, `require_approval`
  is put back), the tool stays gated, and the save warns.
  When the group's gate is itself rules-only, the tool's own `require_approval_when` replaces the group's rules. The
  same holds for a group's `hil.tools:` entry for a member, and a `null` there is refused. To stop gating one
  member, remove the group's `hil` and gate the other members on their own entries.
- Two groups that grant one tool resolve the same way in either order: `require_approval: true` beats rules-only,
  which beats a block that does not gate, which beats `null`. The save warns when they differ and says which it kept.
- Never enable HIL on autonomous or workflow agents — they have no user to approve.
- Set appropriate timeouts — too short and the user doesn't have time to review.

For the full authoring surface — conditional-gating operators, MCP key naming, message
templating — see `sam-agent-design`.

---

## 13. Peer Delegation

Enable an agent to discover and delegate to other agents:

```yaml
inter_agent_communication:
  allow_list: ["agent_a", "agent_b*"]  # Glob patterns for allowed peers
  deny_list: []                        # Exclude specific agents
  request_timeout_seconds: 120         # One delegation to a peer agent (default 120)
  workflow_request_timeout_seconds: 600  # Delegating to a workflow — a whole DAG (default 600)
  sub_task_timeout_seconds: 600        # A self-delegated sub-task (default 600)

max_call_depth: 6                      # Max recursion depth to prevent infinite loops
```

🛑 **A workflow and a sub-task need far larger budgets than a peer exchange.** Leave the
last two unset and each gets its own 600s default. Setting `request_timeout_seconds`
alone makes that value govern all three — so a peer-sized value there will abandon a
workflow mid-DAG. Set the specific key when you need a peer timeout that is short.

**How it works:** The agent discovers peers via their agent cards, matches tasks to capability descriptions, and delegates automatically. No explicit wiring needed. Agent discovery is enabled automatically by the platform.

**Default behavior:** If `inter_agent_communication` is omitted or `allow_list` is empty (`[]`), the agent **cannot call any other agents**. You must explicitly list which peers an agent is allowed to delegate to. Use `["*"]` only for coordinator/orchestrator agents that genuinely need access to all agents.

**What the Builder saves:** `allow_list`, `deny_list` and the three `*_timeout_seconds` keys are saved with the agent and deployed as written when the Builder's server-side save is on, which is the default (the older per-component save does not carry the block); other keys in the block are reported and not saved. Each `allow_list` entry is either a glob pattern or the exact, case-sensitive `agent_name` of an agent in the same build or already on the platform (a remote agent or workflow also counts). A name that is neither, or a malformed pattern, makes the save and the preview fail naming every offending entry (a name the agent already holds is kept with a note instead), so list the specialists you are building with their `agent_name` verbatim. A pattern that matches nothing yet is accepted with a warning, and a pattern such as `name*` is how to reach an agent the platform does not manage (one started from files, or in another deployment). Leaving the block out of an update keeps the stored one; an explicit `allow_list: []` (or an `allow_list:` with no value) removes every peer. The keys you state are merged into whatever peer settings the agent already has, and nothing else there is touched. If what is stored cannot be updated safely (both spellings stored, a key in the other spelling inside a block, a value of the wrong type, a block that is null or not an object, or stored settings the platform cannot read), the save is refused with one error naming it. The agent page has no control for these settings: rewrite the agent's `additionalConfigurations` with the agents API or `sam config apply` (both replace the whole blob) so it holds one `interAgentCommunication` block, or leave `inter_agent_communication` out of that agent's artifact to keep what is stored. An orchestrator that omits its specialists can only fork copies of itself, never call them.

**Be selective with peer access.** Don't grant `["*"]` by default. Instead:
- Use `platform_list_components` to discover what agents are deployed
- Add only the specific agents that this agent needs to collaborate with
- Use glob patterns (e.g., `"data_*"`) when the agent needs a category of peers

**When to enable:**
- Coordinator/router agents that orchestrate specialists — use `["*"]` or list specific specialists
- Agents that need capabilities from specific other deployed agents — list those agents by name
- NOT needed for agents that work independently (most agents)

---

## 14. Streaming and Output Control

```yaml
stream_batching_threshold_bytes: 100   # Min bytes before flushing a stream chunk
max_message_size_bytes: 10000000       # Max message size (10MB default)
```

These rarely need adjustment. Only tune if you have specific latency or message size requirements.

---

## 15. Volumes

Named storage volumes for tools that need persistent file access:

```yaml
volumes:
  - name: workspace
    description: "Shared workspace for file processing"
    provision: auto_create        # static | auto_create | prompt_user
    volume_id: "vol-123"          # For static provisioning
    ttl: "24h"                    # Auto-delete after duration
```

Volumes are bound to tools via `volume_bindings` in the tool config. This is an advanced feature for tools that need filesystem access beyond artifacts. The Builder does not save `volumes` or `networks`: they are set with `sam config apply`, and a save that states them keeps the stored value and warns.

---

## 16. Standard Environment Variables

When generating configs for platform deployment, use these standard env var patterns:

| Variable | Purpose | Default |
|----------|---------|---------|
| `${NAMESPACE, solace-agent-mesh}` | Broker namespace | `solace-agent-mesh` |
| `${LLM_SERVICE_ENDPOINT}` | LLM API base URL | — |
| `${LLM_SERVICE_API_KEY}` | LLM API key | — |
| `${LLM_SERVICE_GENERAL_MODEL_NAME}` | Default model name | — |
| `${SAM_DATA_DIR}` | Data directory (DB, artifacts) | `~/.sam/data` |
| `${SAM_SKILLS_DIR}` | Skills directory | `~/.sam/skills` |

**Connector secret pattern:** `${FIELDNAME_CONNECTOR_UUID}` (e.g., `${PASSWORD_abc123}`)

Always prefer env vars over hardcoded values for endpoints, keys, and paths.

---

## 17. Common Configuration Patterns

### Interactive chat agent
```yaml
apps:
  - name: chat_assistant
    app_config:
      agent_name: chat_assistant
      instruction: |
        You are a helpful assistant. Be conversational and clear.
        When the user's request is ambiguous, ask for clarification.
      tools:
        - tool_type: builtin-group
          group_name: general_agent_tools
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: "General-purpose assistant for questions and tasks."
        skills:
          - id: general-help
            name: General Help
            description: "Answer questions and assist with everyday tasks."
```

### Autonomous workflow node agent
```yaml
apps:
  - name: sentiment_analyzer
    app_config:
      agent_name: sentiment_analyzer
      supports_streaming: false
      instruction: |
        You are a sentiment analysis engine. You operate autonomously.
        Analyze the provided text and return structured results.
        Do not ask for clarification — use your best judgment.
      input_schema:
        type: object
        properties:
          text: { type: string, description: "Text to analyze" }
        required: ["text"]
      output_schema:
        type: object
        properties:
          sentiment: { type: string, enum: ["positive", "negative", "neutral"] }
          confidence: { type: number, minimum: 0, maximum: 1 }
        required: ["sentiment", "confidence"]
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: "Analyzes text sentiment with confidence scores."
        skills:
          - id: sentiment
            name: Sentiment Analysis
            description: "Analyze text for positive, negative, or neutral sentiment."
```

### Coordinator/router agent with peer delegation
```yaml
apps:
  - name: coordinator
    app_config:
      agent_name: coordinator
      instruction: |
        You are a coordinator agent. Your role is to understand user requests
        and delegate to the most appropriate specialist agent.

        Available specialists will be discovered automatically.
        Route tasks based on capability descriptions.
        If no specialist matches, handle the request yourself.
      tools:
        - tool_type: builtin-group
          group_name: general_agent_tools
        - tool_type: builtin-group
          group_name: artifact_management
      inter_agent_communication:
        allow_list: ["*"]
        request_timeout_seconds: 120
      max_call_depth: 5
      agent_card:
        description: "Routes tasks to specialist agents and coordinates multi-agent work."
        skills:
          - id: coordination
            name: Task Coordination
            description: "Understands requests and delegates to the best specialist."
```

### Skill-based expert agent
```yaml
apps:
  - name: docs_expert
    app_config:
      agent_name: docs_expert
      instruction: |
        You are a documentation expert with deep knowledge in our product docs.

        You have access to these skills:
        - **product-docs**: Full product documentation. Load when answering product questions.
        - **api-reference**: API endpoint reference. Load when the user asks about APIs.

        When you need information:
        1. Load the appropriate skill with load_skill
        2. Search with grep_skill_resources using broad patterns
        3. Read specific sections with read_skill_resource
      skills:
        - name: product-docs
        - name: api-reference
      tools:
        - tool_type: builtin-group
          group_name: general_agent_tools
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: "Expert on product documentation and API reference."
        skills:
          - id: product-help
            name: Product Help
            description: "Answer questions about product features and usage."
          - id: api-help
            name: API Reference
            description: "Explain API endpoints, parameters, and examples."
```

### Web-enabled research agent
```yaml
apps:
  - name: researcher
    app_config:
      agent_name: researcher
      instruction: |
        You are a research assistant. You can fetch web content and analyze it.
        When asked to research a topic, gather information from multiple sources,
        synthesize findings, and produce a clear summary.
      tools:
        - tool_type: builtin-group
          group_name: general_agent_tools
        - tool_type: builtin-group
          group_name: artifact_management
        - tool_type: builtin-group
          group_name: web_tools
      agent_card:
        description: "Researches topics by fetching and analyzing web content."
        skills:
          - id: research
            name: Web Research
            description: "Research topics using web sources and produce summaries."
```

---

## 18. Field Interaction Rules

These constraints are not captured in the JSON Schema but are critical for correct configs:

1. **Interactive agents + `general_agent_tools`**: An agent that talks to a user directly should include `general_agent_tools` (which has ask_user_question). Do not mark an agent interactive through `supports_streaming`; it only advertises streaming on the agent card.

2. **`ask_user_question` and autonomous agents**: Never include `general_agent_tools` (or ask_user_question individually) on agents that run autonomously (workflow nodes, event-driven agents). They have no user to ask and will block indefinitely.

3. **`inter_agent_communication` requires agent discovery**: Agent discovery is automatically enabled by the platform when `inter_agent_communication` is configured.

4. **`skills` need `skills_base_path`**: the platform sets it for you; the Builder does not save it, so do not state it.

5. **`input_schema`/`output_schema` implies workflow node**: Agents with structured schemas are workflow nodes. Set `supports_streaming: false` and don't include ask_user_question.

6. **An agent that works on attached files should have artifact tools**: give it `artifact_management` and have its instruction say to open attached files with `list_artifacts` and `load_artifact`. Do not rely on `artifact_handling_mode` for this; files reach the agent in every mode.

7. **`hil` requires interactive context**: Per-tool HIL only works when a user is connected — there is no agent-level form. Don't enable on autonomous agents.

8. **`agent_card` must have `skills`**: The agent_card requires at least one skill entry for entrypoint discovery to work. An agent with an empty skills array is effectively undiscoverable.
