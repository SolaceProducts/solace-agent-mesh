---
name: sam-connector-design
description: Design guidance for Solace Agent Mesh connectors — when to use each type, how they relate to agents, builder workflow, and secret handling.
---

Design guidance for creating Solace Agent Mesh connectors. Connectors are reusable credential + tool bundles that get linked to agents.

For **schema details** see `sam-connector-schema`.

## What Are Connectors?

Connectors provide agents with access to external systems. The list below
illustrates the native connector types; it is not the catalogue of this
deployment. A type can be switched off (the `email` type is off unless the
deployment enables it). Call `platform_list_components` with
`component_type: connector_type` to get the types and subtypes this deployment has
enabled, and never propose a connector type it does not list. When a need
points at a type that is not listed, say which capability is off and what would
enable it; do not substitute an invented tool. `sam-connector-schema` holds the
field schema of each type:
- **SQL** (`sql`): Query databases — PostgreSQL, MySQL, MariaDB, MSSQL, Oracle
- **MCP** (`mcp`): Connect to remote MCP tool servers (GitHub, Stripe, custom servers)
- **API** (`api`): Call REST APIs via OpenAPI specifications
- **Knowledge Base** (`knowledge_base`): RAG retrieval from Amazon Bedrock Knowledge Bases
- **Slack** (`slack`): Post/read messages in a Slack workspace via a Slack bot token (subtype `bot`)
- **Search** (`search`): Full-text search over Elasticsearch or OpenSearch
- **Document DB** (`document_db`): NoSQL document stores — MongoDB, DynamoDB
- **Graph DB** (`graph_db`): Graph databases — Neo4j, Neptune
- **Event Mesh** (`event_mesh`): Publish or request-reply through a Solace event broker from an agent-callable tool
- **Web Search** (`web_search`): Search the public web through Tavily (subtype `tavily`) or Exa (subtype `exa`) and cite the results

Slack, Search, Document DB, Graph DB, and Web Search are FIRST-CLASS connector types —
create them directly (e.g. a Slack request → a `slack` connector), NOT an MCP or
API connector. Reach for MCP only when the user names an MCP/tool server, and for
API only when the user has an OpenAPI/REST spec with no native connector for it.

Connectors are **shared resources** — one connector can be linked to multiple agents (up to 5 agents per connector). This avoids duplicating credentials.

## When to Use Each Type

**SQL** — User mentions:
- Databases, queries, SQL, PostgreSQL, MySQL, MariaDB
- "I want the agent to query our database"
- "Connect to our data warehouse"

**MCP** — User mentions:
- MCP servers, tool servers, remote tools
- Specific MCP servers (GitHub, Stripe, custom MCP servers)
- "Connect to an MCP server"
- SSE or streamable-http connections

**Knowledge Base** — User mentions:
- RAG, knowledge bases, document search
- Amazon Bedrock, AWS knowledge base
- "Search through our documentation"

**API** — User mentions:
- REST APIs, OpenAPI, Swagger
- "Call an external API"
- Specific API services with OpenAPI specs

**Slack** — User mentions:
- Slack, a Slack workspace/channel, a Slack bot, posting or reading Slack messages
- "Create a Slack connector", "connect to Slack", a Slack bot token / app token
- Use a native `slack` connector (subtype `bot`) — do NOT route this to MCP or API

**Search** — User mentions:
- Full-text search, Elasticsearch, OpenSearch
- "Search our Elasticsearch index"

**Web Search** — User mentions:
- Searching the internet or the public web, current events, citing web sources
- Tavily or Exa, or "give the agent web search"
- Use a `web_search` connector with the provider the user names, and
  `<<__SAM_REQUIRED__>>` for `api_key`. Leave `api_base` out: the connector
  supplies the provider's endpoint. When no provider is named, ask which one
  the user has a key for. `search` is the Elasticsearch / OpenSearch type, not web search.
  Its tool is for agent nodes; a workflow tool node cannot use it.

**Document DB** — User mentions:
- MongoDB, DynamoDB, a NoSQL/document store
- "Query our MongoDB collection"

**Graph DB** — User mentions:
- Neo4j, Neptune, a graph database, relationships/traversals
- "Query our knowledge graph"

**Event Mesh** — User mentions:
- Solace event broker, event mesh connector, broker-backed tools
- "Publish to a topic from an agent"
- "Send a command through the event broker"
- "Use request-reply over Solace"
- "Connect the agent to a backend service through the event broker"

Use an **Event Mesh Connector** when the agent initiates the interaction by invoking a tool. Use an **Event Mesh Entrypoint** instead when an external broker event arrives first and should autonomously trigger an agent or workflow.

For Event Mesh Connectors, do not infer an input schema from topic names. Ask for Topic Address plus the real operation schema. Once the user provides both, infer low-risk configuration rather than asking a long checklist:
- derive tool name and description from topic, schema, and use case
- match Topic Address variables to schema properties
- use existing `x-solace-payload-path`; otherwise map payload properties to their own names and leave topic-only variables out of payload
- default payload format to JSON
- default Delivery Mode to Guaranteed Messaging
- infer Publish-Subscribe for command/event topics; infer Request-Reply for query/get/check/status topics or when the user needs a backend result
- default Request-Reply to `reply_mode: temp_queue` and `request_expiry_ms: 60000`

Ask follow-up questions only when the inference is ambiguous or high impact. If the user provides only topic addresses, ask them to paste or describe the input schema and payload mappings, or explicitly confirm they will configure schemas later in the connector UI.

After a schema is provided, review every property that the agent can supply. If an ambiguous property lacks a `description`, ask the user for one and add it to the JSON Schema. Ambiguous examples include IDs, codes, action/status fields, quantities, units, dates, free-text notes, business abbreviations, and conditionally required inputs. Do not invent these descriptions unless the user or source schema already supplied the meaning.

When asking for human-authored wording such as tool names, tool descriptions, operation descriptions, or missing schema-property descriptions, offer 2-4 AI-generated options where possible and let the user choose or write their own. Do not use generated options for exact system values such as credentials, URLs, topic addresses, IDs, or pasted schemas.

## Builder Workflow for Connectors

### 1. Identify connector needs during Discovery phase

When the user describes what they want the agent to do, listen for signals that suggest connectors:
- External data sources → SQL, Document DB, Graph DB, Search, or Knowledge Base
- Slack workspace/bot → Slack
- External tool servers → MCP
- External APIs → API (OpenAPI)
- Agent-initiated Solace publish or request-reply flows → Event Mesh

Check each candidate against the enabled types (see "What Are Connectors?"), and don't substitute a tool or built-in that `describe_tools` doesn't show.

### 2. Create connector artifacts BEFORE agent artifacts

Connectors should be created before agents because agents reference connectors. The build manifest should list connectors first.

### 3. Gather information conversationally

Ask about the system, not the credentials:
- "What database system are you using?" (not "What's the password?")
- "Does the MCP server require authentication?" (not "What's the API key?")
- "What region is your knowledge base in?"
- "What is the URL of the API's OpenAPI (Swagger) document?" (an API connector built here needs it; if they only have a file, they create the connector on the Connectors page and the build reuses it)
- "For each Event Mesh operation, can you paste the JSON Schema or Event Portal/AsyncAPI schema and identify which fields go into the Topic Address versus the payload?"
- "Should this Event Mesh operation use Publish-Subscribe or Request-Reply? If Request-Reply, what reply delivery and timeout should it use?"
- "Which Delivery Mode should the outbound message use: Guaranteed Messaging or Direct Messaging?"
- "These schema fields are missing descriptions: action, sku, quantity. What should the agent know about each one?"
- "Which generated tool description best matches this operation? You can choose one or write your own."

Fill in what you learn from conversation and use `<<__SAM_REQUIRED__>>` for everything else, especially secrets.

### 4. Use discovery tools when possible

- **MCP connectors**: If the user provides a server URL and no auth is needed (or they've provided credentials), use `discover_mcp_tools` to list available tools and their parameters. This helps write a better connector description, and it gives the parameter names a workflow tool node needs.
- **API connectors**: Use `parse_openapi_spec` to detect what authentication the API requires (this sets the correct `auth_type`) and to list the API's operations and their parameters, as `toolName: in(param*, ...)` in the preview and in full in `openapi_spec.json`. A workflow tool node needs an `allow_list` or an uploaded spec; a connector created from only a spec URL needs the operation's `operationId` in its `allow_list`, because a `toolName` is the operationId snake-cased and cut to 60 characters; when the preview shows `toolName [operationId: X]`, write `X`. With a long operation list, pass `operations: [names]` to read specific ones. If the preview says `operations: could not be read (<reason>)`, don't guess operations: use an agent node, or ask the user to upload a bundled single-file spec on the Connectors page.
- **Connectors already on the platform**: use `describe_tools` with `{connector: <exact name>}` to see their tools' parameters (up to 50 are listed; add `tool` to describe one by name).

If discovery isn't possible (auth required but not provided), skip it — the UI handles discovery after credentials are filled in.

### 5. Validate before saving

Always validate connector artifacts with `validate_component_config`:
```
validate_component_config(
  config_yaml: '<the JSON string>',
  mime_type: 'application/vnd.sam-connector-config+json'
)
```

Note: despite the parameter name `config_yaml`, pass JSON for connectors.

## Secret Handling

**Never include real secrets in artifacts.** Always use `<<__SAM_REQUIRED__>>`:
- Passwords, API keys, tokens, secret keys
- Any credential the user hasn't explicitly provided in chat

The UI detects `<<__SAM_REQUIRED__>>` and blocks build and test until it is replaced. No dialog collects it: the user tells you the value and you rewrite the artifact, or they edit the connector on the Connectors page once it is saved.

It's fine to include non-secret values the user mentions:
- Hostnames, ports, database names
- URLs, regions, connection types
- Auth types (API connectors: none/apikey/http/oauth)

**Validate a credential the user DID provide before proceeding.** When the user
supplies a concrete secret value in chat (e.g. a Slack bot token), put it in the
config and then re-run `validate_component_config` on that concrete config — do NOT
validate only the `<<__SAM_REQUIRED__>>` placeholder and then swap the value in
unchecked. If validation fails on the provided value (e.g. "Bot Token must start
with 'xoxb-'", or a minimum-length error), do NOT stamp the component validated
or claim it's deployed. Instead tell the user in prose exactly what's wrong and ask
them to give you the corrected value in the chat, then write it into the artifact with
`artifact_search_and_replace_regex`. Never ask for a secret through `ask_user_question`
— that field is not masked. (Or offer to save with the `<<__SAM_REQUIRED__>>`
placeholder, which they can replace later by telling you or, once the connector is
saved, on the Connectors page.) A
malformed credential will be rejected by the platform at save time, so proceeding
only produces a confusing failure.

## Connector-Agent Relationship

- Each agent can have up to 5 connectors
- Each connector generates tool entries in the agent's YAML, and the shape depends on the
  connector type:
  - SQL, Slack, Search, Document DB, Graph DB, Knowledge Base and Event Mesh
    connectors emit `tool_type: builtin` entries with a generated per-connector
    `tool_name`, each backed by a shared runtime binary (a SQL connector's tool runs on
    `execute_sql_query`, a Slack connector's on `slack` and `slack_update`).
  - An **MCP** connector emits a single `tool_type: mcp` entry carrying connection
    params. Its tool names usually come from the remote MCP server at connect time,
    so they are not knowable from the YAML. A `manifest` or a `tool_name` on the entry
    does pin them — the manifest IS the registered tool set, and `tool_name` narrows to
    that one tool. A `tool_name_prefix` does not: the names stay whatever the server
    advertises, each carrying that prefix.
  - An **API** connector emits a `tool_type: openapi` entry pointing at the spec; its
    tool names come from the OpenAPI operations.

  Read the generated YAML rather than guessing a connector tool's name.
- Connectors are referenced by ID in the agent record
- The platform handles linking connectors to agents — the builder just creates the connector artifact and lists it in the build manifest
- Approval gates for connector tools live on the connector (`values.hil`), see
  `sam-connector-schema` → Tool Approvals. Tell the user they apply to every agent using
  the connector, and that workflow tool nodes bound to it run without asking, so a tool
  that needs approval goes through an agent node. Keys are role names (`sql_query`,
  `slack_send_message`), and
  `api` keys are the snake_case operation tool name. Writing `hil` replaces the whole
  block; `"hil": {}` or `null` removes it in a build. Deselecting an `api` operation keeps
  its gate, but uploading a spec that drops it, or renaming or removing an `event_mesh`
  operation, removes that gate with a warning that the save reports. To keep a gate on a
  renamed `event_mesh` operation, write the new `hil` in the same build.

## Build Manifest

Include connectors in the build manifest with their type and subtype:

```yaml
components:
  - filename: my_database.json
    type: connector
    name: my_database
    description: PostgreSQL connector for the analytics database.
    connector_type: sql
    connector_subtype: postgres
  - filename: my_agent.yaml
    type: agent
    name: my_agent
    description: Answers questions against the analytics database.
    connectors:
      - my_database
```

`filename` names the saved artifact and is required on every component — write
`filename: ""` for one that is already running rather than omitting the key.
