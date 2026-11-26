---
name: sam-connectors
description: Use when giving a Solace Agent Mesh agent access to external data or services without writing code — query a SQL database (PostgreSQL, MySQL, MariaDB, SQL Server, Oracle), retrieve from a knowledge base (Amazon Bedrock RAG), search Elasticsearch/OpenSearch, look up MongoDB/DynamoDB documents or Neo4j/Neptune graphs, search the internet with cited results (Tavily, Exa), call a REST API from an OpenAPI spec, use a remote MCP server's tools, send Slack messages, or publish to the Solace event mesh — and when creating, editing, or attaching connectors. Not for inbound integrations where users or events reach agents (sam-entrypoints), custom Go/Python tool code or toolsets (sam-tools-and-skills), or authoring the agent itself (sam-author-agent).
---

# sam-connectors

This skill creates and manages Agent Mesh **connectors** — platform-managed, credentialed, reusable connections that give agents outbound access to data and services with **no code**. **Scope check:** if the outside world reaches *into* your agents (users chatting from Slack/Teams, MCP clients calling your agents, mesh events triggering tasks), that is an *entrypoint* → `sam-entrypoints`. Custom Go/Python tool code, toolsets, and skill bundles → `sam-tools-and-skills`. The agent's own instructions/behavior → `sam-author-agent`.

## What a connector is

A **named instance** of a connector type, holding connection config + credentials. Created once at the platform level, attached to agents **by name** (an agent can hold up to 5). Each attached connector contributes its tool(s) to the agent — a SQL query tool, RAG retrieval, one tool per OpenAPI operation, the remote MCP server's tools, Slack send- and update-message tools, one cited web search tool, or one publish-to-topic tool per event_mesh operation. **Connector is the default for reaching data and services; reach for custom code only when no type fits.**

## Picking the type

Route by what the agent should do (full field-level detail: [references/catalog.md](references/catalog.md)):

| The agent should… | Type (subtypes) |
|---|---|
| Query a relational database | `sql` (postgres, mysql, mariadb, mssql, oracle) |
| Answer from a RAG knowledge base | `knowledge_base` (bedrock) |
| Call a REST API you have an OpenAPI spec for | `api` (openapi) |
| Use tools from a remote MCP server | `mcp` (remote) |
| Post/update messages in Slack | `slack` (bot) |
| Send requests to backend services over the Solace mesh | `event_mesh` (solace) |
| Look up documents | `document_db` (mongodb, dynamodb) — experimental |
| Query a graph database | `graph_db` (neo4j, neptune) — experimental |
| Run full-text search over your own Elasticsearch/OpenSearch | `search` (elasticsearch, opensearch) — experimental |
| Search the internet and cite the sources | `web_search` (tavily, exa) — early access |

## Paths, in teaching order

1. **Builder UI** (default). Create the connector in the Builder UI: pick type/subtype, fill the schema-driven form — a single Create page for most types (SQL, knowledge_base, …); some add a step, e.g. the **MCP** connector exposes a tool-selection step. Lead with this — it is also the source of truth for which fields exist.
2. **Declarative config** — a connector is its own resource kind, authored and applied via the **`sam-declarative-config`** skill (`sam config plan` / `apply`). **That skill is the only source of full YAML — this skill names types and field names only.** `sam config pull` exports existing connectors with secret fields rewritten as `${VAR}` placeholders; pulling one UI-created connector is the fastest schema oracle.
3. **REST API** — escape hatch for automation pipelines only.

## Attaching to agents & lifecycle

- **UI**: the agent builder's connectors picker. **Declarative**: the agent's spec lists connector *names* (not config). Creation order: connector first, then the agent that uses it.
- **Editing or deleting a connector automatically redeploys every agent attached to it** — flag this before changing a shared connector in production hours. Uploading a changed `api`/`openapi` spec file also redeploys the agents and workflows bound to it; re-uploading an unchanged file does not.

## Hard rules (each counters an observed failure)

- **Go product, Go patterns.** There is no `sam plugin add`, no `sam-sql-database`/`sam-mongodb` plugin, no `apps:`/`app_config:` authoring. Never present Python Agent Mesh mechanisms or YAML shapes.
- **Never write connector YAML from memory** — not even "illustrative" sketches; invented keys get copy-pasted. Builder UI, or `sam-declarative-config`, or pull an existing connector. When unsure of a key, look it up there; guessing is not allowed. **When the user wants YAML *right now*, the fast path is still compliant**: invoke `sam-declarative-config` for the exact shape, or `sam config pull` a UI-created connector. Urgency never licenses memory-YAML.
- **Secrets:** never inline in YAML or echo back in commands. A credential pasted into chat is exposed — say so once, advise rotation, and reference it only as `${VAR}` from then on.
- **"Look up data" asks → connector first, not an MCP server install.** MongoDB/Postgres/search asks are served by `document_db`/`sql`/`search` connectors, not by telling the user to bolt on a third-party MCP server. The MCP *connector* is for when the user already has a remote MCP server — and it is **remote-only** (SSE / streamable HTTP); there is no stdio-command connector.
- **OAuth MCP connector → it needs a `manifest`.** An OAuth-protected remote MCP server can't list its tools at agent startup, because no user is signed in yet. Without an inline `manifest`, the connector deploys but contributes **zero tools**; the agent still starts with its other tools. This applies to `auth_type: oauth` only — `apikey` and `http` auth discover tools normally, and adding a manifest there only costs you `allow_list`/`deny_list`/`tool_name`, which it excludes. Write the manifest (each tool's `name`, `description`, `inputSchema`, and `outputSchema` if the server has one) via `sam-declarative-config`, then confirm the agent actually lists the connector's tools. See [references/catalog.md](references/catalog.md) `mcp`.
- **A connector's metadata + `values` update is one atomic PATCH — a bad sibling field blocks the manifest too.** `sam config apply` sends `name` + `description` + `values` (the manifest lives inside `values`) as a single `PATCH /api/v2/platform/connectors/{id}`; if *any* field fails validation the server 4xxes the whole update and **nothing in `values`** persists. One exception: an `api`/`openapi` connector's `specification_file` is uploaded first, by a separate `PUT .../{id}/resource` (the platform validates a PATCHed `allow_list` against the *stored* spec, so it has to land first). That upload persists the new spec, content hash and `updatedTime` even if the PATCH then 422s — so an `api` connector can be left with a new spec and an old `base_url`, and `updatedTime` moving is not proof the PATCH succeeded. Two things reject it: a `manifest` alongside `allow_list`/`deny_list`/`tool_name` (mutually exclusive — strip **only those three**, and never `selected_tools`; see below), and length, since `name` (3–255) and `description` (10–1000) are enforced on **update** in both directions, exactly as on create (for `name` that holds when the name changes: a changed name must pass the create rules, including `safename`, so control characters and an all-whitespace name get 422, and a name that is already taken gets 409, while resending the stored name unchanged is accepted and keeps it even if it breaks those rules) — a description edited *down* below 10 chars 422s with `description must be at least 10 characters` just as one grown past 1000 does (a field left out keeps its stored value; an empty one is rejected too) — a description that grew past 1000 rejects the PATCH with 422 and silently takes a newly-added manifest down with it (agent stays at 0 tools). If a manifest addition "doesn't take," don't assume the diff ignored it (`sam config plan` correctly shows the update) — read the apply output for a 4xx on a sibling field, and note a wrong `--url`/port hides the 422 behind connection-refused. See [references/catalog.md](references/catalog.md) `mcp`.
- **Never strip `selected_tools` from an MCP connector.** The builder wizard writes `manifest` **and** `selected_tools` together, and that pair is valid and intended — `selected_tools` is the operator's chosen subset, applied as a filter over the manifest at deploy time. It is *not* part of the mutual-exclusivity set: only `tool_name` / `allow_list` / `deny_list` conflict with a manifest. Removing `selected_tools` does **not** error — it silently re-enables every tool in the manifest, including ones the operator deliberately turned off. Conversely it cannot be used without a manifest.
- **Shared-credential model:** every attached agent gets identical access, and Agent Mesh cannot restrict what queries agents run — restrict at the external system (read-only DB user, scoped API key, minimal Slack scopes). See [references/credentials-and-security.md](references/credentials-and-security.md).
- **Web search → `web_search` connector.** Google Custom Search shuts down on 2027-01-01, so the `web_search_google` built-in stops returning results then; for a new or existing agent that needs internet search, create a `web_search` connector (Tavily or Exa key) and attach it. An agent with `deep_research` also searches through the attached connector once it redeploys, so keep `deep_research` and remove only `web_search_google`. Don't confuse it with `search`, which queries the user's own Elasticsearch/OpenSearch. Its tool is offered to agents only: a workflow that needs web search uses an agent node, never a `tool` node.
- **Outbound vs inbound:** "agent posts to Slack / publishes to the mesh" = connector (here). "People or events reach the agent from Slack / the mesh" = entrypoint (`sam-entrypoints`). State which one you picked and why.

## References

| Topic | File |
|---|---|
| Full type catalog — subtypes, fields, tools exposed, auth options, gotchas | [references/catalog.md](references/catalog.md) |
| Credentials, secrets hygiene, shared-access model, RBAC | [references/credentials-and-security.md](references/credentials-and-security.md) |
