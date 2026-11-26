# Giving an agent an MCP server's tools

Scope check: this is **outbound** — your agent consumes an external MCP server. Exposing your agents TO external MCP clients is the MCP *entrypoint* (`sam-entrypoints`). A reusable, platform-managed MCP connection shared across agents is the `mcp` **connector** (creation details in `sam-connectors`); attaching one to an agent is covered here.

## Paths, in teaching order

1. **Builder UI — the MCP connector wizard.** Create/select an `mcp` connector (user supplies: server URL — connectors are remote-only, SSE or streamable HTTP, no stdio — plus auth and a tool selection step listing the server's discovered tools), then attach it to the agent in the agent's connectors dialog. This is the no-code path; lead with it.
2. **Declarative config** — author the connector + agent attachment via `sam-declarative-config` (it has the connector kind's exact schema).
3. **Runtime YAML (escape hatch)** — an inline `tool_type: mcp` entry on one agent. Capability level: `connection_params` (transport `stdio` | `sse` | `streamable-http`, with `command`/`args` or `url`), an `auth` block (`type`: `oauth2` | `basic` | `bearer`, with credential fields), TLS via `connection_params.ssl_config` (CA bundle, client cert, verify), and tool filtering via `allow_list` / `deny_list` (or `tool_name` for a single tool). Note stdio is inline-only — the platform-managed `mcp` **connector** is remote-only (`sse` / `streamable-http`). Exact spelling: `sam-declarative-config` — do not write these keys from memory.

## Facts that prevent common errors

- Bearer, basic and oauth2 go through the structured **`auth` block** — `type: oauth2 | basic | bearer` are the only values the runtime honors. Static headers are **not** an auth type: on an inline `tool_type: mcp` entry put them in `connection_params.headers`; on an `mcp` connector use `custom_headers` (or `auth_type: apikey` with the header location). A `headers:` key at the tool-entry top level is ignored, and so is `auth: {type: headers}` — it sets the auth mode and supplies no headers, so every request goes out unauthenticated.
- Tool filtering is **`allow_list` / `deny_list`** (mutually exclusive options), not `tool_filter`.
- Several inline entries may name one server, each with its own `tool_name`, `allow_list`, `deny_list` or `tool_name_prefix` (for example one entry per tool a workflow's tool nodes call). They share one connection, so repeat the same `connection_params` (URL, or command and args; headers; `ssl_config`), `auth` block and manifest-or-live choice in every entry for that server: a later entry that differs is refused, its tools are not registered, and the agent's log names the settings that differ. An entry whose filter matches none of the server's tools registers nothing and logs a warning listing the server's tool names.
- **Effect.** Every tool is classified `read_only` or `side_effecting`, and an MCP tool is `side_effecting` by default. A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. Changing the run's own session, such as the skills it has loaded, is not outside it. A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. Every other tool is side_effecting. A server's `readOnlyHint` makes a tool `read_only` only on an inline entry that sets `trust_annotations: true`; without it the hint is shown (`effect_source: untrusted_hint`) but ignored, and a `destructiveHint: true` or an `openWorldHint: true` always counts, and an omitted `openWorldHint` counts as `true`, the MCP specification's default, so a trusted tool is `read_only` only when its server sends `readOnlyHint: true` and `openWorldHint: false` explicitly. Setting `trust_annotations: true` means you vouch that the server's tools meet all three conditions of the rule above, not only the first. A trusted read-only tool saves any resource it returns under a fresh generated filename, never one the server's URI chose. `effect: side_effecting` on the entry marks every tool it registers side-effecting and beats a trusted hint; `read_only` is refused there. An entry with `effect` is not retried in the background when its server is unreachable at startup, like `hil`, so its tools register only when the server answers at startup. `trust_annotations` applies at registration, so it also works when the server registers late. A connector-attached MCP tool cannot set either key and stays `side_effecting`. A skill's own `tools:` entry cannot set `trust_annotations` either: the key is ignored with a warning, because trusting a server's claims is the operator's choice on the agent's entry. A skill entry may still set `effect: side_effecting`.
- Secrets go in env vars via `${VAR}` substitution, never inline.
- Internal-CA TLS is supported on the MCP connection itself (`ssl_config`) — no need to touch the host trust store.
- Inline tool vs connector: inline binds to one agent's config; the connector is platform-managed and reusable. Prefer the connector unless the user explicitly hand-manages one agent's runtime YAML.

## Per-user OAuth servers on an inline entry: static manifests

A remote MCP server that needs each user's OAuth token (Atlassian, GitHub, Linear) answers the startup `tools/list` with 401, because no user has signed in yet, so the agent would come up with zero tools. Pre-declare the tools instead: a `manifest:` list (or a `manifest_file:` path to a YAML file holding the same list) on the `tool_type: mcp` entry. The agent registers those tools at startup without contacting the server, and opens the connection on the first tool call, with the calling user's token attached.

```yaml
- tool_type: mcp
  connection_params:
    type: sse
    url: https://mcp.atlassian.com/v1/sse
  auth:
    type: oauth2
  manifest:
    - name: getJiraIssue
      description: Get a Jira issue by ID or key.
      inputSchema:
        type: object
        properties:
          cloudId: { type: string }
          issueIdOrKey: { type: string }
        required: [cloudId, issueIdOrKey]
```

- `manifest` and `manifest_file` are mutually exclusive. Setting both, or a `manifest_file` that is missing or unparseable, fails agent startup.
- An empty list (`manifest: []`, or a file whose content is `[]`) means "no manifest": live discovery at startup, not zero tools.
- Not supported on `stdio` connections; a local subprocess server does not block `tools/list` on user auth, so use live discovery there.
- `manifest_file` resolves relative to the agent's working directory, and `${VAR}` substitution applies.
- Nothing checks the manifest against the server. If the server renames a parameter or adds a required field, calls fail at invocation time; regenerate the manifest when the server changes.
- When a user has not authorized yet, the first call returns an authorization prompt instead of a result; after the user signs in, the next call goes through.

## OAuth endpoint and client discovery

An `auth: {type: oauth2}` block with no `scheme` discovers the endpoints from `connection_params.url`: it follows the server's 401 `WWW-Authenticate` `resource_metadata` hint (falling back to the resource's `/.well-known/oauth-protected-resource`), then probes the authorization server's OAuth and OpenID Connect metadata documents. Discovered endpoints are cached for 24 hours per server URL; restart the agent to re-discover sooner. To skip discovery (air-gapped hosts, a proxy that blocks `.well-known`, or a server that advertises several authorization servers), set both `scheme.authorization_url` and `scheme.token_url`. Scopes and `token_endpoint_auth_method` you set are never overridden by discovery.

The OAuth client comes from one of three places, and the agent logs which one at INFO:

1. `auth.credential.client_id` / `client_secret` in the YAML (pre-registered).
2. Dynamic client registration against the discovered `registration_endpoint` (log line `oauth2 dynamic client registration`). The registered client is stored with the users' tokens and reused, so every user of the server shares one client ID.
3. Environment fallback when the server has no registration endpoint or registration fails with a transport or server error (log line `oauth2 using env-var client credentials`): `TOOL_OAUTH_CLIENT_ID` + `TOOL_OAUTH_CLIENT_SECRET`, else `MCP_CLIENT_ID` + `MCP_CLIENT_SECRET`. One pair serves every OAuth2 MCP entry that has no client of its own.

The sign-in URL carries `prompt=consent` by default, which Google needs to re-issue refresh tokens. For Microsoft Entra ID set `scheme.disable_consent_prompt: true`: Entra otherwise asks each user for their own consent even when an admin has already consented, and tenants that block user consent show "Approval required".

A refresh rejected with `invalid_grant` clears only that user's token (they sign in again). `invalid_client` clears the registered client and triggers a fresh registration.

## Verify

Ask the agent "what tools do you have?" — the server's tools should appear (skill-bundled/MCP names may be prefixed). Auth failures surface in the agent's startup logs; diagnosis beyond that → `sam-operate`.
