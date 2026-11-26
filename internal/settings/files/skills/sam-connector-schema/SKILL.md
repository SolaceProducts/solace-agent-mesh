---
name: sam-connector-schema
description: JSON Schema for Solace Agent Mesh connector configuration — all connector types, subtypes, field requirements, and validation rules.
---

Solace Agent Mesh connector configuration schema. Connectors are reusable credential + tool bundles that get linked to agents. Each connector has a type, subtype, and a values object containing type-specific fields.

For **design guidance** see `sam-connector-design`.

## Schema

```json
{{SCHEMA:connector}}
```

---

## Connector Format

Connector artifacts use **JSON** (not YAML) with this structure:

```json
{
  "name": "My Database",
  "description": "Production PostgreSQL database",
  "type": "sql",
  "subtype": "postgres",
  "icon": "postgresql",
  "values": {
    "hostname": "db.example.com",
    "port": 5432,
    "database": "production",
    "username": "app_user",
    "password": "<<__SAM_REQUIRED__>>"
  }
}
```

**MIME type**: `application/vnd.sam-connector-config+json`

**Key rules:**
- `name` is required and must be 1-255 characters
- `type` must be one of the connector schema enum values, including `sql`, `mcp`, `knowledge_base`, `api`, and `event_mesh`
- `subtype` must be valid for the type (see type-specific sections below)
- `description` is required and must be 10-1000 characters
- `values` must be an object with the fields required by the type/subtype. Use exactly the field names the schema and the type sections below list: the platform rejects a `values` key the type does not declare (for example `host` where the field is `hostname`), and warns about a required field you leave out
- `icon` is optional but **strongly recommended** — it tells the UI which service icon to display (see Icon Field below)
- Use `<<__SAM_REQUIRED__>>` for any secret or unknown field value

---

## Icon Field

The `icon` field tells the UI which service-specific icon to display on the builder canvas. **Always set this field** when creating a connector for a well-known service.

**Allowed values** (enum):
`jira`, `confluence`, `slack`, `github`, `gitlab`, `salesforce`, `servicenow`,
`pagerduty`, `zendesk`, `stripe`, `twilio`, `notion`, `hubspot`,
`trello`, `asana`, `linear`, `bitbucket`, `mongodb`, `elasticsearch`,
`snowflake`, `shopify`, `datadog`, `postgresql`, `mysql`, `redis`,
`microsoft_graph`, `google`, `aws`, `azure`, `web_search`, `other`

**Rules:**
- If the connector targets a service in the list above, set `icon` to the matching value
- If the service is NOT in the list, set `icon` to `"other"` — the UI will use a generic type-based icon
- The field is optional for backward compatibility — existing connectors without it still work (the UI shows a generic type-based icon)
- The value must be lowercase, using underscores for multi-word names (e.g. `microsoft_graph`)

**Examples:**
```json
{"icon": "jira"}           // Jira API connector
{"icon": "github"}         // GitHub MCP connector
{"icon": "postgresql"}     // PostgreSQL SQL connector
{"icon": "snowflake"}      // Snowflake connector
{"icon": "other"}          // Unknown/custom service
```

---

## SQL Connectors

**Subtypes**: `postgres`, `mysql`, `mariadb`, `mssql`, `oracle`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `hostname` | string | yes | — | Database server hostname or IP address (not `host`) |
| `port` | number | no | 5432 (postgres) / 3306 (mysql, mariadb) / 1433 (mssql) / 1521 (oracle) | Server port |
| `database` | string | yes (not `oracle`) | — | Database name |
| `service_name` | string | yes (`oracle` only, instead of `database`) | — | Oracle service name |
| `username` | string | yes | — | Database username |
| `password` | password | yes | — | Database password (secret) |
| `encrypt` | select | no (`mssql` only) | `yes` | `yes`, `no`, or `strict` |
| `trust_server_certificate` | select | no (`mssql` with `encrypt: "yes"`) | `no` | `yes` or `no` |

**Example:**
```json
{
  "name": "Analytics DB",
  "description": "Analytics PostgreSQL database for reporting queries",
  "type": "sql",
  "subtype": "postgres",
  "icon": "postgresql",
  "values": {
    "hostname": "analytics.internal",
    "port": 5432,
    "database": "analytics",
    "username": "<<__SAM_REQUIRED__>>",
    "password": "<<__SAM_REQUIRED__>>"
  }
}
```

---

## MCP Connectors

**Subtypes**: `remote`

Connects to a remote MCP (Model Context Protocol) server.

### Base fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `server_url` | string | yes | — | MCP server endpoint URL |
| `connection_type` | select | yes | `streamable-http` | `streamable-http` or `sse` |
| `auth_type` | select | yes | `none` | `none`, `apikey`, `http`, or `oauth` |
| `custom_headers` | key_value | no | — | Extra HTTP headers sent on every call |
| `tool_name` / `allow_list` / `deny_list` | string | no | — | Tool selection; at most one of the three |
| `tool_name_prefix` | string | no | — | Prefix added to every registered tool name |
| `hil` | object | no | — | Per-tool approval gates, keyed by the server's registered tool names. See [Tool Approvals](#tool-approvals-hil) |

### Auth-specific fields (conditional on `auth_type`)

**When `auth_type: "apikey"`:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth_apikey_location` | select | yes | `header` or `query` |
| `auth_apikey_name` | string | yes | Header or query parameter name, e.g. `Authorization` or `X-API-Key` |
| `auth_apikey_value` | password | yes | The API key value (secret) |

**When `auth_type: "http"`:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth_http_scheme` | select | yes | `basic` or `bearer` |
| `auth_http_bearer_token` | password | when `bearer` | Bearer token (secret) |
| `auth_http_basic_username` | string | when `basic` | Basic auth username |
| `auth_http_basic_password` | password | when `basic` | Basic auth password (secret) |

**When `auth_type: "oauth"`:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth_oauth_mode` | select | yes | `discovery` (dynamic client registration; the fields below are left out) or `manual` |
| `auth_oauth_authorization_url` | string | when `manual` | OAuth authorization endpoint |
| `auth_oauth_token_url` | string | when `manual` | OAuth token endpoint |
| `auth_oauth_client_id` | string | when `manual` | OAuth client ID |
| `auth_oauth_client_secret` | password | no | OAuth client secret (secret) |
| `auth_oauth_scopes` | string | no | Space-separated scopes |

**Example:**
```json
{
  "name": "GitHub Tools",
  "description": "GitHub tools exposed through a remote MCP server",
  "type": "mcp",
  "subtype": "remote",
  "icon": "github",
  "values": {
    "server_url": "https://mcp.github.com/sse",
    "connection_type": "sse",
    "auth_type": "apikey",
    "auth_apikey_location": "header",
    "auth_apikey_name": "Authorization",
    "auth_apikey_value": "<<__SAM_REQUIRED__>>"
  }
}
```

---

## Knowledge Base Connectors

**Subtypes**: `bedrock`

Connects to an Amazon Bedrock Knowledge Base for RAG.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `kb_id` | string | yes | — | Bedrock Knowledge Base ID |
| `region` | string | yes | `us-east-1` | AWS region |
| `auth_type` | select | yes | `access_key` | `access_key` or `iam` |
| `aws_access_key_id` | password | when `access_key` | — | AWS Access Key ID (secret) |
| `aws_secret_access_key` | password | when `access_key` | — | AWS Secret Access Key (secret) |
| `aws_account_id` | string | when `iam` | — | AWS account ID that owns the role |
| `role_name` | string | when `iam` | — | IAM role name to assume |
| `session_name` | string | no (`iam`) | — | Role session name |
| `external_id` | password | no (`iam`) | — | External ID for cross-account roles (secret) |

**Example:**
```json
{
  "name": "Product KB",
  "description": "Product documentation knowledge base on Amazon Bedrock",
  "type": "knowledge_base",
  "subtype": "bedrock",
  "icon": "aws",
  "values": {
    "kb_id": "KB12345",
    "region": "us-east-1",
    "auth_type": "access_key",
    "aws_access_key_id": "<<__SAM_REQUIRED__>>",
    "aws_secret_access_key": "<<__SAM_REQUIRED__>>"
  }
}
```

---

## API Connectors

**Subtypes**: `openapi`

Connects to a REST API described by an OpenAPI (Swagger) document; each operation in the spec becomes a tool.

**The spec source is required.** The Builder can only save connector values; it cannot upload a spec file, and connectors do not accept an inline spec. Set `specification_url` to the http(s) URL of the OpenAPI JSON or YAML document. Without it the connector saves and deploys with no tools, and `validate_component_config` rejects it. `specification_url` is the one field that never takes `<<__SAM_REQUIRED__>>`. Never invent or guess a spec URL: if your task did not include one, stop and report that back instead of writing the connector (the Builder then keeps the component in the manifest as `status: blocked` with a `blocked_reason`, never deletes it, and no draft is saved). Editing an existing connector whose spec was uploaded on the Connectors page is the exception: keep the `specification_url` it already has (or leave it out); the upload stays in place.

### Base fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `specification_url` | string | yes (in the Builder) | — | http(s) URL of the OpenAPI document |
| `base_url` | string | yes | — | Base server URL the operations are called against (`http://` or `https://`) |
| `auth_type` | select | yes | `none` | `none`, `apikey`, `http`, or `oauth` |
| `allow_list` | text | no, except for a connector a workflow tool node binds | — | Comma-separated string of operation IDs to expose; omit to expose every operation. **Required when a workflow tool node binds the connector** (`connector: <name>`) unless its spec was uploaded on the Connectors page or an `allow_list` is already stored on the platform: a workflow's tool names are resolved at deploy without fetching the spec, so a connector with only a `specification_url` is refused at deploy, and full validation rejects it first. The list limits the connector's tools for every agent and workflow bound to it, so list every operationId its consumers need, e.g. `findPetsByStatus,getPetById`. For a connector that already exists with neither a stored `allow_list` nor an uploaded spec, the user uploads its OpenAPI spec on the Connectors page rather than the Builder writing a list |
| `custom_headers` | key_value | no | — | Extra HTTP headers sent on every call |

### Auth-specific fields (conditional on `auth_type`)

**When `auth_type: "apikey"`:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth_apikey_location` | select | yes | `header` or `query` |
| `auth_apikey_name` | string | yes | Header or query parameter name, e.g. `X-API-Key` |
| `auth_apikey_value` | password | yes | The API key (secret) |

**When `auth_type: "http"`:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth_http_scheme` | select | yes | `basic` or `bearer` |
| `auth_http_basic_username` | string | when `basic` | Username |
| `auth_http_basic_password` | password | when `basic` | Password (secret) |
| `auth_http_bearer_token` | password | when `bearer` | Bearer token (secret) |

**When `auth_type: "oauth"`:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth_oauth_authorization_url` | string | yes | Authorization endpoint |
| `auth_oauth_token_url` | string | yes | Token endpoint |
| `auth_oauth_client_id` | string | yes | OAuth client id |
| `auth_oauth_client_secret` | password | no | Client secret (secret; empty for PKCE public clients) |
| `auth_oauth_refresh_url` | string | no | Refresh endpoint, if different from the token endpoint |
| `auth_oauth_scopes` | string | no | Space-separated scopes to request, e.g. `openid email read:api` |
| `auth_oauth_token_endpoint_auth_method` | select | no | `client_secret_basic`, `client_secret_post`, or `none` |
| `auth_oauth_pkce` | select | no | `enabled` (default) sends a PKCE S256 challenge with each sign-in; `disabled` only for a provider that rejects PKCE. Cannot be `disabled` with method `none` |

Call `parse_openapi_spec` on the spec URL to detect which auth the API expects before choosing `auth_type`. It also lists the API's operations and their parameters.

**Example:**
```json
{
  "name": "Weather API",
  "description": "Current weather lookups from the Weather REST API.",
  "type": "api",
  "subtype": "openapi",
  "icon": "other",
  "values": {
    "specification_url": "https://api.weather.example/openapi.json",
    "base_url": "https://api.weather.example/v1",
    "auth_type": "apikey",
    "auth_apikey_location": "header",
    "auth_apikey_name": "X-API-Key",
    "auth_apikey_value": "<<__SAM_REQUIRED__>>"
  }
}
```

---

## Event Mesh Connectors

**Subtypes**: `solace`

Exposes one or more agent-callable tools that publish messages or perform request-reply through a Solace event broker. Use this when the agent initiates the broker interaction. Use an Event Mesh Entrypoint instead when incoming broker events should trigger autonomous agent or workflow processing.

### Base fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `broker_url` | string | yes | — | Event broker SMF URL, for example `tcps://host:55443` |
| `broker_vpn` | string | yes | — | Message VPN for the broker connection |
| `broker_username` | string | yes | — | Broker client username |
| `broker_password` | password | yes | — | Broker client password |
| `operations` | array | yes | — | One or more agent-facing tools that share this broker connection |

### Operation fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool_name` | string | yes | Agent-facing tool name, lowercase with underscores |
| `tool_description` | string | yes | What the tool does and when the agent should call it |
| `topic` | string | yes | Solace topic address. Use `{property_name}` for dynamic topic levels |
| `wait_for_response` | boolean | yes | `true` for Request-Reply, `false` for Publish-Subscribe |
| `payload_format` | string | yes | `json`, `yaml`, or `text` |
| `input_schema` | object | yes | JSON Schema object for agent inputs |
| `qos` | integer | no | `1` for Guaranteed Messaging, `0` for Direct Messaging |
| `request_expiry_ms` | number | no | Request-Reply timeout in milliseconds |
| `reply_mode` | string | no | `temp_queue` or `p2p_inbox` for Request-Reply replies |

Use `x-solace-payload-path` on JSON Schema properties to map inputs into the outbound message payload. Use `x-solace-context-expression` for values sourced from runtime context rather than the agent.

**Generation rule:** Never invent Event Mesh operation schemas from Topic Address text. A valid generated connector needs a user-provided JSON Schema or a user-provided source schema from Event Portal, AsyncAPI, or OpenAPI. If the user has not provided schema details, ask for them or leave the connector as a draft for the connector UI; do not guess payload fields, required fields, enum values, defaults, or descriptions.

**Operation defaults:** When Topic Address and schema are present, infer low-risk settings instead of asking for every field. Default payload format to `json`, Delivery Mode to Guaranteed Messaging (`qos: 1`), Request-Reply `reply_mode` to `temp_queue`, and `request_expiry_ms` to `60000`. Infer Publish-Subscribe for command/event topics and Request-Reply for query/get/check/status topics or when the user needs a backend result. Ask only when the Message Exchange Pattern or another setting is ambiguous or high impact.

**Schema description review:** Agent-provided schema properties should have descriptions when the name alone is not enough for the LLM to use the field correctly. Ask the user for missing descriptions for ambiguous IDs, codes, status/action fields, quantities, units, dates, business abbreviations, free-text fields, or conditionally required inputs, then add those descriptions to the JSON Schema.

**Example:**
```json
{
  "name": "Order Event Broker",
  "description": "Lets agents send order commands through the Solace event broker",
  "type": "event_mesh",
  "subtype": "solace",
  "icon": "other",
  "values": {
    "broker_url": "<<__SAM_REQUIRED__>>",
    "broker_vpn": "<<__SAM_REQUIRED__>>",
    "broker_username": "<<__SAM_REQUIRED__>>",
    "broker_password": "<<__SAM_REQUIRED__>>",
    "operations": [
      {
        "tool_name": "create_order_command",
        "tool_description": "Create an order command for backend fulfillment.",
        "topic": "retail/orders/{store_id}/commands/create/v1",
        "wait_for_response": false,
        "payload_format": "json",
        "qos": 1,
        "input_schema": {
          "type": "object",
          "required": ["store_id", "sku", "quantity"],
          "properties": {
            "store_id": { "type": "string" },
            "sku": { "type": "string", "x-solace-payload-path": "order.sku" },
            "quantity": { "type": "integer", "x-solace-payload-path": "order.quantity" }
          }
        }
      }
    ]
  }
}
```

---

## Tool Approvals (hil)

Every connector type that produces tools accepts a `values.hil` block: a human approves a tool call before it runs. Author it only when the user asks for approval before something happens (sending, deleting, writing, querying a sensitive system).

```
"hil": { "tools": { "<role>": { "require_approval": true, "approval_message": "..." } } }
```

### Keys are role names

The key under `tools` is the tool's **role**. It is not the generated `<role>_<suffix>` tool name and not a custom `tool_name`. A key you write that is not a role of the connector is refused at save, and the error lists the valid ones (`mcp` is the exception: its keys are checked when the agent starts, as are an `api` connector's when the platform cannot read its spec, see below). For the role-keyed types and `event_mesh`, a stored key the save didn't change that no longer names a tool is removed with a warning; for `api` see below.

| Connector | Keys under `hil.tools` |
|-----------|------------------------|
| `email` | `send_email` |
| `slack` | `slack_send_message`, `slack_update` |
| `sql` | `sql_query` |
| `knowledge_base` | `query_bedrock_kb` |
| `document_db` | `mongo_query` (`mongodb`), `dynamodb_query` (`dynamodb`) |
| `graph_db` | `neo4j_query` (`neo4j`), `neptune_query` (`neptune`) |
| `search` | `elasticsearch_query` (`elasticsearch`), `opensearch_query` (`opensearch`) |
| `web_search` | `web_search` |
| `event_mesh` | each operation's `tool_name`; `event_mesh_request` when the operation sets none |
| `api` | the snake_case operation tool name: `deleteUser` becomes `delete_user`. Raw operationIds are refused |
| `mcp` | the server's registered tool names, unchanged (a `tool_name_prefix` is included) |

### Fields

| Field | Type | Description |
|-------|------|-------------|
| `require_approval` | bool | Pause for approval before every call |
| `approval_message` | string | Go template shown to the approver; `{{.to}}` interpolates the tool argument `to` |
| `show_args` | bool | Show the call's arguments to the approver |
| `timeout` | string | Wait limit as a duration string, e.g. `"30m"` |
| `require_approval_when` | list | Gate only calls whose arguments match a rule |

Fields written beside `tools` apply to every tool the connector provides. An entry under `tools` overrides them for that role. A null approval field (`"require_approval": null`, and likewise `require_approval_when` or `tools`) is refused; leave the field out instead. An unknown key under `tools` is refused too, except for `mcp`, whose keys are checked when the agent starts, and for an `api` connector whose spec the platform cannot read (see below). An `api` connector's keys are checked at save against an uploaded spec or a spec readable at its `specification_url`.

**Example:**
```json
{
  "name": "Outbound Email",
  "description": "SMTP connector that asks a human before any email is sent",
  "type": "email",
  "subtype": "smtp",
  "icon": "other",
  "values": {
    "smtp_host": "<<__SAM_REQUIRED__>>",
    "smtp_tls": "starttls",
    "smtp_auth_type": "plain",
    "smtp_username": "<<__SAM_REQUIRED__>>",
    "smtp_password": "<<__SAM_REQUIRED__>>",
    "envelope_from": "<<__SAM_REQUIRED__>>",
    "recipient_allowlist": "example.com",
    "hil": {
      "tools": {
        "send_email": {
          "require_approval": true,
          "approval_message": "Send to {{.to}}?"
        }
      }
    }
  }
}
```

### Updating a saved connector

- Leaving `hil` out keeps the stored gate.
- Writing `hil` replaces the whole block, so repeat every gate you want to keep.
- `"hil": {}` removes every gate. `"hil": null` removes them in the Builder, but on the raw API `null` keeps the stored gate, so write `{}`.

### api and event_mesh

- `api`: deselecting a gated operation from the tool selection keeps its gate. Uploading a spec that drops a gated operation removes that gate and warns; the Connectors page and `sam config apply` show the warning. A key you write that names no operation in the uploaded spec is refused at save.
- `api` that uses `specification_url` (the Builder cannot upload a spec, so this is every api connector it creates): the platform reads the spec at that URL when the connector is created, updated or previewed, and refuses a key you write that names no operation in it, with the valid names in the error. A key already in the stored gate is left alone only when `hil` is omitted, null or sent back unchanged; writing a changed `hil` block re-checks every key in it. A stored key the readable spec lacks is kept and warned about, not removed.
- `api` whose spec the platform cannot read (the URL is not reachable from the platform, or the spec cannot be parsed, for example one with an external `$ref`): keys cannot be checked at save and the save warns. A key that names no operation then stops every agent bound to the connector from starting, or leaves the gate unapplied when an agent selects operations with an `allow_list`, so check the names against the spec yourself.
- `event_mesh`: renaming or removing a gated operation removes its gate and warns, unless the same save writes a different `hil` block. The Builder reports the warning in the save result.

### Reach (tell the user)

- A connector's gate applies to **every agent that uses the connector**, not to one agent. If only one agent needs approval, say so and use a separate connector.
- Workflow `tool` nodes pause for approval only when the deployment has **both** `workflow_tool_approval_enforcement` and `workflow_hil_pause_resume` on, so check the deployment before promising a gate there.
  - With enforcement on but pause/resume off, the node fails instead of running.
  - With enforcement turned off, a workflow tool node runs the tool without asking anyone.
- To guarantee approval, have an agent node call the tool.

---

## Other Connector Types

`document_db` (`mongodb`, `dynamodb`), `graph_db` (`neo4j`, `neptune`), `search` (`elasticsearch`, `opensearch`), `web_search` (`tavily`, `exa`) and `slack` (`bot`) have no section here. For these, and for any `type` / `subtype` above, the `oneOf` branch in the Schema at the top is the authority: the keys of `values.properties` in the branch whose `type` and `subtype` match are the only keys the connector accepts, and its `required` list is what you must supply (or fill with `<<__SAM_REQUIRED__>>`). Database and search connectors use `hostname`, not `host` or `server`. A `web_search` connector needs only `api_key`; leave `api_base` out unless the user names a private mirror or proxy, and a `tool_name` override must keep the web_search prefix and an underscore, such as `web_search_news`.

---

## Placeholder Convention

Use `<<__SAM_REQUIRED__>>` as the value for:
- All **secret/password** fields (always — never include real secrets)
- Any **required field** whose value was not mentioned in the conversation

The UI detects this placeholder and blocks build and test until it is replaced. No dialog collects it.
