A connector is a credentialled binding to an external system
(databases, MCP servers, message brokers). The `type` and `subtype`
fields together select the connector schema; the `values` field
carries the per-type configuration (often including secret-shaped
fields, which `sam config pull` rewrites as `${VAR}` placeholders).
Unlike skills, connectors are referenced from agents by name only —
there is no per-agent override of connector values.

Workflow `type: tool` nodes also reference connectors by name: the
node's `connector:` field names the connector, and `tool:` selects
one of a multi-tool connector's tools by un-suffixed base name
(optional when the connector produces exactly one). The platform
resolves the reference to a concrete `tool_name` at deploy time, and
connector edits or renames auto-redeploy bound workflows. See the
workflow kind reference for the node shape.

A connector subtype carries a maturity stage. The `mongodb` subtype of
`document_db` is in the Beta stage, and `web_search` (both subtypes) is in
the Early Access stage. The `dynamodb` subtype, `graph_db` and `search` are
Experimental, and the generated field reference below tags them
`*(experimental)*`. It leaves Beta and Early Access subtypes untagged, so an
untagged subtype is not necessarily generally available.

A `web_search` connector (`tavily` or `exa`) gives each agent it is bound
to one cited web search tool, named `web_search_` plus the last eight hex
digits of the connector id. It is not the `search` type, which queries the
user's own Elasticsearch or OpenSearch. Keep `api_key` as a `${VAR}`
placeholder and leave `api_base` at the provider default unless the user
names a private mirror or proxy. A `tool_name` override must match
`web_search_[a-z0-9_]+` and be at most 64 characters, or the save is
refused. The save runs one test search with the key: a key the provider
rejects fails the apply for that connector, and any other outcome (a rate,
plan or credit limit, another HTTP status, or the platform not reaching
the provider) saves with a warning, because the platform's network path is
not the agents'. Workflow `type: tool` nodes are not offered this
connector's tool; use an agent node.

### Human-in-the-loop (HIL) on MCP tools

MCP connectors expose many tools from one binding, so HIL is authored
as a sub-map keyed by the tool name the MCP server advertises:

```yaml
spec:
  type: mcp
  values:
    server_url: "https://mcp.atlassian.com/v1/mcp"
    hil:
      tools:
        deleteJiraIssue:
          require_approval: true
          approval_message: "About to delete {{.issueKey}}. Confirm?"
          timeout: "30m"
        transitionJiraIssue:
          require_approval: true
        atlassian_rest_request:
          require_approval_when:
            - arg: method
              in: [POST, PUT, PATCH, DELETE]
          approval_message: "{{.method}} {{.path}}"
```

The keys must match the **registered** tool name — the name Agent Mesh
ends up using, not the raw string the server advertises. Two transformations
apply: any character outside `[A-Za-z0-9_]` becomes `_`, and when the
entry sets `tool_name_prefix:` the prefix is **included**. An advertised
`rest-request` under `tool_name_prefix: atlassian` is therefore keyed
`atlassian_rest_request`. For OAuth-gated MCP servers shipping a static
`manifest:`, start from the `name:` field on each manifest entry and
apply the same two transformations. A key that matches no registered
tool parses cleanly and gates nothing, so the approval never fires.

`require_approval_when:` gates conditionally on the LLM-supplied arg
values rather than all-or-nothing. Useful for MCP tools that expose
multiple HTTP verbs (writes vs reads) under one tool name. A call is
gated when `require_approval` is true OR any rule matches; operators are
`eq`, `in`, `not_in`, `exists`, `not_exists`, `gt`, `lt`, `gte`, `lte`,
and `arg:` accepts dotted paths into nested maps. Missing args and type
mismatches fail open. See *Conditional gating* in
`references/design/agent-design.md` for the full operator semantics.

HIL is **not MCP-specific**. Every connector type that produces tools
takes the same `hil:` block under `values`, and the feature works on
every tool type, including `builtin` and `sam_remote`, where each YAML
entry registers one tool and `hil:` sits directly on the entry. For the
full feature reference (fields, `{{.argName}}` template syntax, design
guidance), see `references/design/agent-design.md` →
*Human-in-the-Loop (HIL)*.

### HIL on the other connector types

A non-MCP connector authors `values.hil` with the same shape as the MCP
example above: `tools:` keyed by the tool's **role**, each entry taking
`require_approval`, `approval_message`, `show_args`, `timeout` and
`require_approval_when`. Fields written beside `tools:` act as defaults
for every tool the connector provides, and a `tools:` entry overrides
them. The key is the role, not the generated `<role>_<suffix>` tool name
and not a custom `tool_name`:

| Connector type | Keys under `hil.tools` |
|----------------|------------------------|
| `slack` | `slack_send_message`, `slack_update` |
| `sql` | `sql_query` |
| `knowledge_base` | `query_bedrock_kb` |
| `document_db` | `mongo_query` or `dynamodb_query` |
| `graph_db` | `neo4j_query` or `neptune_query` |
| `search` | `elasticsearch_query` or `opensearch_query` |
| `web_search` | `web_search` |
| `event_mesh` | each operation's `tool_name`, `event_mesh_request` when it sets none |
| `api` | the snake_case operation tool name (`deleteUser` is `delete_user`); a raw operationId is refused |

An unknown key you write is refused at save (except `mcp`, which is
checked when the agent starts, and an `api` connector whose spec the
platform cannot read, covered below). For the role-keyed types and
`event_mesh`, a stored key the save didn't change that no longer names a tool
is removed with a warning. For `api`, only a spec upload that drops the
operation removes its gate (with a warning); a stored key the spec at
`specification_url` lacks is kept and warned about. An approval field set to
`null` (including `require_approval_when` and `tools`) is refused too.
On update, leaving `hil` out keeps the stored gate and writing it
replaces the whole block. To remove every gate, write `hil: {}` (or
`hil: null`).

`sam config apply` checks the `hil` keys in your manifest before it
changes anything, wherever the manifest settles which tools the connector
provides: the roles of a type with fixed ones, an `event_mesh` connector's
operations, and, for `api`, the operations in your `specification_file`. A
key that names a tool the connector will not provide fails the plan with
the key and the names that are valid. Nothing is checked at plan time for
`mcp`, for an `api` connector that uses `specification_url`, or for a
spec that cannot be parsed (an external `$ref`, for example). The save
still refuses a key you wrote that names no operation in the spec at
`specification_url`, so for `mcp` and an unreadable spec check those names
yourself. For an `api` connector, an apply that creates or updates the
connector prints a warning saying its keys could not be checked when the
platform could read no spec; an apply that leaves the connector unchanged
prints none.

For `api` connectors:

- Deselecting a gated operation from the tool selection keeps its gate.
- Uploading a spec that drops a gated operation removes that gate and
  warns, and `sam config apply` prints the warning. If your manifest
  still gates the dropped operation, the plan fails instead, because the
  `specification_file` is the spec the keys are checked against; change
  `hil` in the same edit as the spec.
- For an `api` connector that uses `specification_url`, the platform reads
  the spec at that URL when you save and refuses a key you write that names
  no operation in it.
- When it cannot read a spec (the URL is not reachable from the platform, or
  the spec cannot be parsed, such as one with an external `$ref`), the save
  warns instead. A key that names no operation then stops every agent bound
  to the connector from starting, or leaves the gate unapplied when an agent
  selects operations with an `allow_list`.

For `event_mesh`, renaming or removing a gated operation removes its gate
and warns, unless the same save writes a different `hil` block. If your
manifest still gates the old operation name, `sam config apply` fails the
plan, so change `hil` in the same edit as the rename.

A connector's gate applies to **every agent that uses the connector**;
there is no per-agent override. Workflow `type: tool` nodes that bind the
connector run the tool without asking, so route a tool that needs
approval through an agent node.

### Mutual TLS on MCP connectors

An MCP server behind mutual TLS or a private CA takes an `ssl_config`
map under `values`, with the keys `ca_bundle`, `client_cert`,
`client_key` and `server_name`:

```yaml
kind: connector
name: internal-mcp
spec:
  type: mcp
  subtype: remote
  values:
    server_url: "https://mcp.internal.example.com/mcp"
    ssl_config:
      ca_bundle: /etc/sam/mcp-tls/ca.pem
      client_cert: /etc/sam/mcp-tls/client.crt
      client_key: /etc/sam/mcp-tls/client.key
      server_name: mcp.internal.example.com
```

Set `client_cert` and `client_key` together or not at all. The three
file fields are paths to PEM files, not PEM content, and the key must be
unencrypted. Each path must be absolute and clean (no `..`, `.` or
repeated slash) and lie inside the directory the operator sets in
`SAM_MCP_CONNECTOR_TLS_DIR` on the Platform service. Without that
variable the Platform service refuses a connector that names a file, and
the message names the variable. The same files must be mounted at the
same paths where the agent runs. With the Helm chart, the operator sets
`samDeployment.mcpConnectorTLS` (`enabled`, `secretName`), which mounts one
Secret of PEM files at `/etc/sam/mcp-tls` on both pods and sets the
variable. `server_name` names no file and needs no directory. `verify` is refused: only the operator turns certificate
checks off, with `SAM_MCP_CONNECTOR_TLS_VERIFY=false`. The connector form
in the UI has no TLS fields, so author and change `ssl_config` only
declaratively or through the API.

On update, leaving `ssl_config` out keeps the stored block (a save in
the UI keeps it too), and writing it replaces the whole block, so taking
it out of the file does not remove it. To remove it, write
`ssl_config: {}` (or `ssl_config: null`). A stored block that fails the
checks above, such as one saved by an older release or one whose
directory the operator has since changed, stays stored but is left out
of the agent's configuration, and the Platform service logs a warning
each time it builds that configuration.

When `sam config apply` saves an MCP connector that has no `manifest`,
the Platform service connects to the server to list its tools. If that
fails (no client certificate, a CA it does not trust, a private address
it may not reach), the connector is still saved, without tools, and
apply prints a `warning:` under the connector's row with the reason.
Fix the cause and apply again with a change to the connector, such as
its description: applying the same files again does not retry the
discovery.

### OpenAPI spec files (`api`/`openapi`)

An `api`/`openapi` connector's tool surface comes from an OpenAPI spec,
supplied one of two ways (mutually exclusive):

- `specification_url` — an external URL the deployed agent fetches live
  at startup. Requires the cluster to have egress to that URL.
- `specification_file` — a local spec file vendored in the repo that
  `sam config apply` uploads as the connector's resource. The agent
  loads it from in-cluster object storage, so it works in deployments
  with no external egress. The path resolves relative to the connector's
  YAML file; the convention is a `connectors/<name>.openapi.(yaml|json)`
  sidecar:

```yaml
kind: connector
name: figma
spec:
  type: api
  subtype: openapi
  values:
    specification_file: figma.openapi.yaml
    base_url: "https://api.figma.com"
    allow_list: "getFile, getComments"
```

Uploads are hash-diffed: an unchanged spec file is a no-op (no
re-upload, no agent redeploy); a changed file redeploys the agents and
workflows bound to the connector, and shows in `sam config
plan` as `update (spec <old> → <new>)`. `sam config pull` round-trips a
spec that was uploaded through the UI wizard by writing the sidecar
file and referencing it via `specification_file`.
